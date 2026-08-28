package cli

import (
	"strings"
	"testing"

	"github.com/apresai/2ndbrain/internal/ai"
)

// TestRawModelWriteClearsStaleRoute is the regression test for the worst
// remaining defect: the PR's own config fields reintroducing the misroute it
// exists to remove.
//
// `config set ai.<slot>_model` writes model+plane+region as one unit, but the
// RAW setConfigValue path is still reached by `ai setup`, `ai local`, and
// `models wizard --set-active`, which write only the id. Leaving the previous
// model's plane and region behind pins the NEW model to the OLD endpoint —
// e.g. picking Claude after openai.gpt-5.5 left the slot on @mantle/us-east-2,
// and the mantle client was then built for a Claude id.
func TestRawModelWriteClearsStaleRoute(t *testing.T) {
	cfg := ai.AIConfig{
		Provider:         "bedrock",
		GenerationModel:  "openai.gpt-5.5",
		GenerationPlane:  ai.PlaneMantle,
		GenerationRegion: "us-east-2",
		EmbeddingModel:   "old.embed",
		EmbeddingPlane:   ai.PlaneClassic,
		EmbeddingRegion:  "us-west-2",
	}
	cfg.Rerank.Model, cfg.Rerank.Plane, cfg.Rerank.Region = "old.rerank", ai.PlaneClassic, "us-east-1"

	for _, tc := range []struct {
		key, value string
		plane      func() ai.Plane
		region     func() string
	}{
		{"ai.generation_model", "us.anthropic.claude-sonnet-5",
			func() ai.Plane { return cfg.GenerationPlane }, func() string { return cfg.GenerationRegion }},
		{"ai.embedding_model", "new.embed",
			func() ai.Plane { return cfg.EmbeddingPlane }, func() string { return cfg.EmbeddingRegion }},
		{"ai.rerank.model", "new.rerank",
			func() ai.Plane { return cfg.Rerank.Plane }, func() string { return cfg.Rerank.Region }},
	} {
		t.Run(tc.key, func(t *testing.T) {
			if err := setConfigValue(&cfg, tc.key, tc.value); err != nil {
				t.Fatalf("setConfigValue: %v", err)
			}
			if p := tc.plane(); p != "" {
				t.Errorf("%s left a stale plane %q; the new model would be pinned to the old endpoint", tc.key, p)
			}
			if r := tc.region(); r != "" {
				t.Errorf("%s left a stale region %q", tc.key, r)
			}
		})
	}
}

// TestPersistProbedRegionIsAuthoritative pins that the probe result overwrites
// whatever route the base row carried.
//
// Fill-only-empty was wrong here in a way that destroyed state: on a FAILED
// probe the entry is copied wholesale from a base row that may be a SIBLING
// route, and a non-empty stale Region then blocked its own correction, so the
// verdict landed on an endpoint that was never probed.
func TestPersistProbedRegionIsAuthoritative(t *testing.T) {
	entry := ai.ModelInfo{
		ID: "m", Provider: "bedrock", Type: "generation",
		Plane: ai.PlaneMantle, Region: "us-east-2",
		InvokeStrategy: ai.StrategyBedrockMantleResponses,
	}
	result := &ai.TestProbeResult{
		ModelID: "m", Provider: "bedrock",
		Plane: ai.PlaneClassic, Region: "us-west-2",
	}
	persistProbedRegion(&entry, result, "us-east-1")

	if entry.Plane != ai.PlaneClassic || entry.Region != "us-west-2" {
		t.Errorf("route = %s, want the endpoint actually probed (classic/us-west-2)", entry.Route().String())
	}
	// A classic row must not keep a sibling's mantle envelope.
	if entry.InvokeStrategy == ai.StrategyBedrockMantleResponses {
		t.Error("a classic row kept the mantle invoke_strategy grafted from a sibling")
	}
}

// TestEnableDisableAppliesToEveryRoute pins that enable/disable is intent about
// the MODEL. Setting the flag on one of N rows left the model in dropdowns via
// its other routes, since filterEnabled is per-row — a silently ineffective
// command, reachable from the GUI.
func TestEnableDisableAppliesToEveryRoute(t *testing.T) {
	_, root := newContractVault(t)
	for _, region := range []string{"us-east-1", "us-east-2", "us-west-2"} {
		if err := ai.SaveUserCatalogEntry(ai.ScopeVault, root, ai.ModelInfo{
			ID: "fake.multi", Provider: "bedrock", Type: "generation", Tier: ai.TierUserVerified,
			Plane: ai.PlaneClassic, Region: region,
		}); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := runCLIArgs(t, root, "models", "disable", "fake.multi", "--provider", "bedrock", "--scope", "vault"); err != nil {
		t.Fatalf("models disable: %v", err)
	}

	rows := 0
	for _, m := range ai.LoadUserCatalog(root) {
		if m.ID != "fake.multi" {
			continue
		}
		rows++
		if m.Enabled == nil || *m.Enabled {
			t.Errorf("route %s was left enabled; the model stays in dropdowns via its other routes", m.Route().String())
		}
	}
	if rows != 3 {
		t.Fatalf("expected all 3 routes to survive the disable, got %d", rows)
	}
}

// TestUnroutedSlotErrorCommandsAreValidKeys pins that every printed recovery
// command names a REAL config key. Rerank's keys are nested
// (`ai.rerank.model`), so the `ai.<slot>_model` pattern produced
// `ai.rerank_model`, which config set rejects — and this message is the sole
// recovery path from an unrouted slot.
func TestUnroutedSlotErrorCommandsAreValidKeys(t *testing.T) {
	settable := map[string]bool{}
	for _, k := range settableConfigKeys {
		settable[k] = true
	}
	for _, slot := range []string{"generation", "embedding", "rerank"} {
		err := &ai.UnroutedSlotError{
			Slot:  slot,
			Model: "m",
			Candidates: []ai.ModelInfo{
				{ID: "m", Provider: "bedrock", Plane: ai.PlaneClassic, Region: "us-east-1"},
			},
		}
		for _, line := range strings.Split(err.Error(), "\n") {
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, "2nb config set ") {
				continue
			}
			key := strings.Fields(line)[3]
			if !settable[key] {
				t.Errorf("slot %q suggests %q, which config set rejects as an unknown key", slot, key)
			}
		}
	}
}
