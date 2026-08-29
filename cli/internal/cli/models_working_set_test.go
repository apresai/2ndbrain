package cli

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/apresai/2ndbrain/internal/ai"
)

// decodeModelList parses the flat `models list --json` array (the shape
// emitted without --discover).
func decodeModelList(t *testing.T, body []byte) []ai.ModelInfo {
	t.Helper()
	var models []ai.ModelInfo
	if err := json.Unmarshal(body, &models); err != nil {
		t.Fatalf("models list parse: %v (body=%s)", err, truncate(body, 500))
	}
	return models
}

func modelByID(models []ai.ModelInfo, id string) (ai.ModelInfo, bool) {
	for _, m := range models {
		if m.ID == id {
			return m, true
		}
	}
	return ai.ModelInfo{}, false
}

// TestContract_ModelsListWorkingField asserts the working flag reaches
// `models list --json` and means what the GUI needs it to mean: the active
// models are always in (so a picker is never empty on a fresh vault) and a
// builtin verified model nobody has probed is not.
func TestContract_ModelsListWorkingField(t *testing.T) {
	_, root := newContractVault(t)
	cfg := ai.DefaultAIConfig()

	got, err := runCLIArgs(t, root, "models", "list", "--provider", "bedrock", "--json", "--porcelain")
	if err != nil {
		t.Fatalf("models list: %v (out=%s)", err, truncate(got, 300))
	}
	models := decodeModelList(t, got)
	if len(models) == 0 {
		t.Fatal("bedrock catalog must not be empty")
	}

	for _, id := range []string{cfg.EmbeddingModel, cfg.GenerationModel} {
		m, ok := modelByID(models, id)
		if !ok {
			t.Fatalf("active model %s missing from the list", id)
		}
		if !m.Working {
			t.Errorf("active model %s must be working even untested", id)
		}
	}

	const unprobedID = "us.anthropic.claude-sonnet-4-6"
	if m, ok := modelByID(models, unprobedID); ok && m.Working {
		t.Errorf("%s is verified but unprobed — working must be false, got %+v", unprobedID, m)
	}

	// omitempty on Working would drop false and leave Swift unable to tell
	// "old CLI" from "not working". output.Write pretty-prints with a space
	// after the colon (`"working": false`).
	if !bytes.Contains(got, []byte(`"working": false`)) {
		t.Errorf("models list --json must include working:false on non-working rows, got %s", truncate(got, 400))
	}
	if !bytes.Contains(got, []byte(`"working": true`)) {
		t.Errorf("models list --json must include working:true on active rows, got %s", truncate(got, 400))
	}
}

// TestContract_ModelsListWorkingSetFilter drives the --working-set filter
// through the real command path across the two states that matter: a fresh
// vault (active models only) and one with a recorded passing probe.
func TestContract_ModelsListWorkingSetFilter(t *testing.T) {
	_, root := newContractVault(t)
	cfg := ai.DefaultAIConfig()

	got, err := runCLIArgs(t, root, "models", "list", "--provider", "bedrock", "--working-set", "--json", "--porcelain")
	if err != nil {
		t.Fatalf("models list --working-set: %v (out=%s)", err, truncate(got, 300))
	}
	fresh := decodeModelList(t, got)
	if len(fresh) != 2 {
		t.Fatalf("fresh vault working set should be the 2 active models, got %d: %+v", len(fresh), fresh)
	}
	for _, m := range fresh {
		if m.ID != cfg.EmbeddingModel && m.ID != cfg.GenerationModel {
			t.Errorf("unexpected member of the fresh working set: %s", m.ID)
		}
	}

	// Record a passing probe the way `models verify` does, then re-list.
	const probedID = "us.anthropic.claude-sonnet-4-6"
	if err := ai.SaveUserCatalogEntry(ai.ScopeVault, root, ai.ModelInfo{
		ID: probedID, Provider: "bedrock", Type: "generation",
		Tier: ai.TierUserVerified, TestedAt: time.Now().UTC().Format(time.RFC3339),
		TestLatencyMs: 380,
	}); err != nil {
		t.Fatalf("seed passing probe: %v", err)
	}

	got, err = runCLIArgs(t, root, "models", "list", "--provider", "bedrock", "--working-set", "--json", "--porcelain")
	if err != nil {
		t.Fatalf("models list --working-set after probe: %v (out=%s)", err, truncate(got, 300))
	}
	after := decodeModelList(t, got)
	if _, ok := modelByID(after, probedID); !ok {
		t.Errorf("a model with a passing probe must join the working set, got %d members", len(after))
	}
	if len(after) != 3 {
		t.Errorf("working set should be the 2 active models plus the probed one, got %d: %+v", len(after), after)
	}

	// An explicit disable removes it again: the working set is what a picker
	// may offer, so a hidden model can never be a member.
	if _, err := runCLIArgs(t, root, "models", "disable", probedID, "--provider", "bedrock", "--scope", "vault"); err != nil {
		t.Fatalf("models disable: %v", err)
	}
	got, err = runCLIArgs(t, root, "models", "list", "--provider", "bedrock", "--working-set", "--json", "--porcelain")
	if err != nil {
		t.Fatalf("models list --working-set after disable: %v (out=%s)", err, truncate(got, 300))
	}
	if m, ok := modelByID(decodeModelList(t, got), probedID); ok {
		t.Errorf("disabled model %s leaked into the working set: %+v", probedID, m)
	}
}

// TestContract_ModelsListFailedActiveNotInWorkingSet: an active model whose
// last probe is access_denied stays in the full list but is not working and
// is dropped from --working-set.
func TestContract_ModelsListFailedActiveNotInWorkingSet(t *testing.T) {
	_, root := newContractVault(t)
	cfg := ai.DefaultAIConfig()
	if err := ai.SaveUserCatalogEntry(ai.ScopeVault, root, ai.ModelInfo{
		ID: cfg.GenerationModel, Provider: "bedrock", Type: "generation",
		TestedAt:      time.Now().UTC().Format(time.RFC3339),
		TestError:     "403 access denied",
		TestErrorCode: string(ai.TestErrAccessDenied),
	}); err != nil {
		t.Fatalf("seed failed active probe: %v", err)
	}

	got, err := runCLIArgs(t, root, "models", "list", "--provider", "bedrock", "--json", "--porcelain")
	if err != nil {
		t.Fatalf("models list: %v", err)
	}
	full := decodeModelList(t, got)
	active, ok := modelByID(full, cfg.GenerationModel)
	if !ok {
		t.Fatalf("active %s missing from the full list", cfg.GenerationModel)
	}
	if active.Working {
		t.Errorf("active + access_denied must serialize working:false, got %+v", active)
	}

	got, err = runCLIArgs(t, root, "models", "list", "--provider", "bedrock", "--working-set", "--json", "--porcelain")
	if err != nil {
		t.Fatalf("models list --working-set: %v", err)
	}
	if m, ok := modelByID(decodeModelList(t, got), cfg.GenerationModel); ok {
		t.Errorf("failed active leaked into --working-set: %+v", m)
	}
}
