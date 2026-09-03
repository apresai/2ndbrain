package cli

import (
	"testing"

	"github.com/apresai/2ndbrain/internal/ai"
)

// novaEmbeddingID is a real builtin embedding model with a real builtin
// recommendation (0.25). Provenance tests MUST use a model that is in the
// builtin catalog: a made-up id has no builtin value to mirror, so it cannot
// reproduce the bug at all.
const novaEmbeddingID = "amazon.nova-2-multimodal-embeddings-v1:0"

// resetModelsAddFlags clears the `models add` flag values AND cobra's per-flag
// Changed bits, which survive Execute() inside one test binary. Without this a
// later test in the package would see a stale --similarity-threshold as if the
// user had typed it.
func resetModelsAddFlags(t *testing.T) {
	t.Helper()
	reset := func() {
		addProvider, addType, addName, addNotes, addScope = "", "", "", "", "vault"
		addDimensions, addContextLen = 0, 0
		addPriceIn, addPriceOut, addPriceRequest, addThreshold = 0, 0, 0, 0
		for _, name := range []string{"provider", "type", "name", "dimensions", "context-length",
			"price-in", "price-out", "price-request", "notes", "similarity-threshold", "scope"} {
			if f := modelsAddCmd.Flags().Lookup(name); f != nil {
				_ = modelsAddCmd.Flags().Set(name, f.DefValue)
				f.Changed = false
			}
		}
	}
	reset()
	t.Cleanup(reset)
}

// `models calibrate --save` is one of only two paths allowed to author a
// threshold, so it must stamp its provenance. Without the stamp a calibration
// that lands on the builtin number is indistinguishable from a mirror and the
// resolver would discard it.
func TestSaveCalibrationStampsProvenance(t *testing.T) {
	_, root := newContractVault(t)
	cfg := ai.AIConfig{Provider: "bedrock", EmbeddingModel: novaEmbeddingID}

	// 0.25 is exactly the builtin recommendation: the case only the stamp can
	// distinguish.
	if err := saveCalibration(ai.ScopeVault, root, "bedrock", novaEmbeddingID, 0.25, cfg); err != nil {
		t.Fatalf("saveCalibration: %v", err)
	}

	got, ok := ai.UserCatalogEntry(ai.ScopeVault, root, ai.RouteKey{
		Provider: "bedrock", ID: novaEmbeddingID, Plane: ai.PlaneClassic,
	})
	if !ok {
		t.Fatal("calibrate --save wrote no row")
	}
	if got.ThresholdSource != ai.ThresholdSourceUser {
		t.Errorf("threshold_source = %q, want %q", got.ThresholdSource, ai.ThresholdSourceUser)
	}
	if _, source := cfg.ResolveSimilarityThresholdFull(root); source != ai.ThresholdSourceUserCalibration {
		t.Errorf("a stamped calibration resolved as %q, want %q", source, ai.ThresholdSourceUserCalibration)
	}
}

// `models add --similarity-threshold` is the other allowed author. The first
// add for a model never reaches the merge path, so both paths must stamp.
func TestModelsAddStampsExplicitThreshold(t *testing.T) {
	_, root := newContractVault(t)
	resetModelsAddFlags(t)

	if _, err := runCLIArgs(t, root, "models", "add", novaEmbeddingID,
		"--provider", "bedrock", "--type", "embedding",
		"--similarity-threshold", "0.25", "--scope", "vault"); err != nil {
		t.Fatalf("models add: %v", err)
	}

	var found bool
	for _, m := range ai.LoadUserCatalog(root) {
		if m.ID != novaEmbeddingID {
			continue
		}
		found = true
		if m.RecommendedSimilarityThreshold != 0.25 {
			t.Errorf("threshold = %v, want 0.25", m.RecommendedSimilarityThreshold)
		}
		if m.ThresholdSource != ai.ThresholdSourceUser {
			t.Errorf("threshold_source = %q, want %q", m.ThresholdSource, ai.ThresholdSourceUser)
		}
	}
	if !found {
		t.Fatal("models add wrote no row for the model")
	}

	cfg := ai.AIConfig{Provider: "bedrock", EmbeddingModel: novaEmbeddingID}
	if _, source := cfg.ResolveSimilarityThresholdFull(root); source != ai.ThresholdSourceUserCalibration {
		t.Errorf("an explicitly added threshold resolved as %q, want %q", source, ai.ThresholdSourceUserCalibration)
	}
}
