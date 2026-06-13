package cli

import (
	"context"
	"testing"

	"github.com/apresai/2ndbrain/internal/ai"
)

// TestPersistProbe_NoOpOnNilOrFailure proves persistProbe never writes a
// catalog entry for a missing or failed probe. Pure logic, no provider needed:
// after a nil and a failed probe, the vault's user catalog stays empty.
func TestPersistProbe_NoOpOnNilOrFailure(t *testing.T) {
	_, root := newContractVault(t)

	persistProbe(root, nil)
	persistProbe(root, &ai.TestProbeResult{
		ModelID:  "amazon.titan-embed-text-v2:0",
		Provider: "bedrock",
		Type:     "embedding",
		OK:       false,
		Detail:   "AccessDenied",
	})

	if got := userCatalogEntries(root); len(got) != 0 {
		t.Errorf("user catalog has %d entries after nil/failed probes, want 0", len(got))
	}
}

// TestAISetupPersistsPassingProbe runs a REAL Bedrock embedding probe (no
// mocks) and asserts persistProbe (the step `ai setup` now performs after a
// successful probe) writes the model to the per-vault user catalog as
// user_verified, so it shows up in `2nb models list`. Skips when Bedrock is
// not reachable, per the no-mock policy.
func TestAISetupPersistsPassingProbe(t *testing.T) {
	ctx := context.Background()
	cfg := ai.DefaultAIConfig()
	if !ai.CheckBedrockCredentials(ctx, cfg.Bedrock) {
		t.Skip("no Bedrock credentials configured; skipping live ai-setup persistence test")
	}

	_, root := newContractVault(t)

	result, err := ai.TestProbeModel(ctx, cfg, cfg.EmbeddingModel, "bedrock", "embedding")
	if err != nil || result == nil || !result.OK {
		t.Skipf("Bedrock embedding probe did not pass (model access?): err=%v result=%+v", err, result)
	}

	persistProbe(root, result)

	entries := userCatalogEntries(root)
	var found *ai.ModelInfo
	for i := range entries {
		if entries[i].Provider == "bedrock" && entries[i].ID == cfg.EmbeddingModel {
			found = &entries[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("model %s not persisted to user catalog after passing probe", cfg.EmbeddingModel)
	}
	if found.Tier != ai.TierUserVerified {
		t.Errorf("persisted entry tier = %q, want %q", found.Tier, ai.TierUserVerified)
	}
	if found.TestedAt == "" {
		t.Error("persisted entry has no TestedAt; a passing probe should stamp the test time")
	}
}

// userCatalogEntries returns only the entries the user catalog files actually
// contain (LoadUserCatalog merges builtin/discovered data in callers, but the
// raw per-vault + global files are what persistProbe writes). We read the
// merged user catalog and that is sufficient for these tests since a fresh
// temp HOME means the global catalog is empty.
func userCatalogEntries(vaultRoot string) []ai.ModelInfo {
	return ai.LoadUserCatalog(vaultRoot)
}
