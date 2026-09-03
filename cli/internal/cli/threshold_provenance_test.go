package cli

import (
	"context"
	"strings"
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

// The HIGH finding: `models bench` seeded its row from the MERGED catalog, so a
// bench run wrote the builtin's own similarity threshold into the user file. It
// defaults to --summary-scope global, so one run in one vault made every vault
// on the machine report "(user calibration)" for a number nobody measured.
//
// The pre-existing bench test uses an id that is NOT in the builtin catalog and
// therefore cannot reproduce this at all; a real builtin embedding id is the
// whole point of this case.
func TestSaveBenchmarkSummaryDoesNotMirrorTheBuiltinThreshold(t *testing.T) {
	setupModelsAddHome(t)
	cfg := ai.AIConfig{Provider: "bedrock", EmbeddingModel: novaEmbeddingID}
	summary := &ai.BenchmarkSummary{RanAt: "2026-09-03T00:00:00Z", AvgLatencyMs: 120, VaultDocCount: 3}

	if err := saveBenchmarkSummary(cfg, ai.ScopeGlobal, "", "bedrock", novaEmbeddingID, "embedding", summary); err != nil {
		t.Fatalf("saveBenchmarkSummary: %v", err)
	}

	for _, m := range ai.LoadUserCatalog("") {
		if m.ID != novaEmbeddingID {
			continue
		}
		if m.Benchmark == nil {
			t.Error("bench summary was not persisted")
		}
		if m.RecommendedSimilarityThreshold != 0 || m.ThresholdSource != "" {
			t.Errorf("bench mirrored a threshold into the user catalog: %v (source %q)",
				m.RecommendedSimilarityThreshold, m.ThresholdSource)
		}
	}

	got, source := cfg.ResolveSimilarityThresholdFull(t.TempDir())
	if got != 0.25 || source != ai.ThresholdSourceModel {
		t.Errorf("after a bench run the threshold resolved as (%v, %q), want (0.25, %q)",
			got, source, ai.ThresholdSourceModel)
	}
}

// A bench run must not destroy a calibration the user really did take.
func TestSaveBenchmarkSummaryKeepsAStampedCalibration(t *testing.T) {
	setupModelsAddHome(t)
	cfg := ai.AIConfig{Provider: "bedrock", EmbeddingModel: novaEmbeddingID}
	if err := saveCalibration(ai.ScopeGlobal, "", "bedrock", novaEmbeddingID, 0.41, cfg); err != nil {
		t.Fatalf("saveCalibration: %v", err)
	}

	summary := &ai.BenchmarkSummary{RanAt: "2026-09-03T00:00:00Z", AvgLatencyMs: 120}
	if err := saveBenchmarkSummary(cfg, ai.ScopeGlobal, "", "bedrock", novaEmbeddingID, "embedding", summary); err != nil {
		t.Fatalf("saveBenchmarkSummary: %v", err)
	}

	for _, m := range ai.LoadUserCatalog("") {
		if m.ID != novaEmbeddingID {
			continue
		}
		if m.RecommendedSimilarityThreshold != 0.41 || m.ThresholdSource != ai.ThresholdSourceUser {
			t.Errorf("bench erased the user's calibration: %v (source %q), want 0.41 (%q)",
				m.RecommendedSimilarityThreshold, m.ThresholdSource, ai.ThresholdSourceUser)
		}
	}
}

// promotedEntry seeds a promoted / probed row from the merged catalog, so it
// carried the builtin threshold into every `models list --promote`, `models
// wizard`, `ai setup`, `models test --save`, `models verify`, and `models
// discover --validate` save.
func TestPromotionPathsNeverCarryTheBuiltinThreshold(t *testing.T) {
	setupModelsAddHome(t)
	base := findBuiltinModel("bedrock", novaEmbeddingID)
	if base == nil {
		t.Fatalf("%s is not in the builtin catalog; this test cannot detect mirroring without it", novaEmbeddingID)
	}
	if base.RecommendedSimilarityThreshold == 0 {
		t.Fatalf("%s has no builtin recommendation; this test cannot detect mirroring without one", novaEmbeddingID)
	}

	result := &ai.TestProbeResult{
		ModelID: novaEmbeddingID, Provider: "bedrock", Type: "embedding",
		OK: true, Detail: "dims=1024",
	}
	if got := promotedEntry(base, result); got.RecommendedSimilarityThreshold != 0 || got.ThresholdSource != "" {
		t.Errorf("promotedEntry carried the builtin threshold: %v (source %q)",
			got.RecommendedSimilarityThreshold, got.ThresholdSource)
	}

	// The failing-probe branch copies the base row wholesale, which is the
	// other way the builtin value reached the user file.
	fail := &ai.TestProbeResult{
		ModelID: novaEmbeddingID, Provider: "bedrock", Type: "embedding",
		OK: false, Detail: "denied", Code: ai.TestErrAccessDenied,
	}
	got := catalogEntryFromTestResult(context.Background(), ai.AIConfig{Provider: "bedrock"}, "", fail)
	if got.RecommendedSimilarityThreshold != 0 || got.ThresholdSource != "" {
		t.Errorf("a failing probe carried the builtin threshold: %v (source %q)",
			got.RecommendedSimilarityThreshold, got.ThresholdSource)
	}
}

// preserveUserThreshold is the single rule every save path goes through: it
// strips a mirrored value and restores only the user's own.
func TestPreserveUserThreshold(t *testing.T) {
	setupModelsAddHome(t)
	entry := ai.ModelInfo{
		ID: novaEmbeddingID, Provider: "bedrock", Type: "embedding",
		RecommendedSimilarityThreshold: 0.25, // as copied off a merged row
	}

	// Nothing stored: the mirrored value is dropped.
	got := entry
	preserveUserThreshold(ai.ScopeGlobal, "", &got)
	if got.RecommendedSimilarityThreshold != 0 || got.ThresholdSource != "" {
		t.Errorf("with no stored calibration, got %v (source %q), want 0 and empty",
			got.RecommendedSimilarityThreshold, got.ThresholdSource)
	}

	// A stored calibration is carried forward, value and stamp together.
	if err := ai.SaveUserCatalogEntry(ai.ScopeGlobal, "", ai.ModelInfo{
		ID: novaEmbeddingID, Provider: "bedrock", Type: "embedding",
		RecommendedSimilarityThreshold: 0.41, ThresholdSource: ai.ThresholdSourceUser,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	got = entry
	preserveUserThreshold(ai.ScopeGlobal, "", &got)
	if got.RecommendedSimilarityThreshold != 0.41 || got.ThresholdSource != ai.ThresholdSourceUser {
		t.Errorf("stored calibration lost: got %v (source %q), want 0.41 (%q)",
			got.RecommendedSimilarityThreshold, got.ThresholdSource, ai.ThresholdSourceUser)
	}
}

// `ai status` used to tell every user with a high threshold to "clear the saved
// calibration", including users whose value came from the builtin catalog. The
// message now names the actual cause, so the lookup behind it has to be right.
func TestThresholdCalibrationOrigin(t *testing.T) {
	setupModelsAddHome(t)
	vaultRoot := t.TempDir()

	if got := thresholdCalibrationOrigin(vaultRoot, "bedrock", novaEmbeddingID); !strings.Contains(got, "no user-catalog row") {
		t.Errorf("with nothing stored, got %q, want a line saying no row carries it", got)
	}

	if err := ai.SaveUserCatalogEntry(ai.ScopeGlobal, "", ai.ModelInfo{
		ID: novaEmbeddingID, Provider: "bedrock", Type: "embedding",
		RecommendedSimilarityThreshold: 0.65, ThresholdSource: ai.ThresholdSourceUser,
	}); err != nil {
		t.Fatalf("seed global: %v", err)
	}
	globalPath, err := ai.CatalogPathForScope(ai.ScopeGlobal, "")
	if err != nil {
		t.Fatal(err)
	}
	got := thresholdCalibrationOrigin(vaultRoot, "bedrock", novaEmbeddingID)
	if !strings.Contains(got, globalPath) {
		t.Errorf("origin %q does not name the global catalog %q", got, globalPath)
	}
	if !strings.Contains(got, "calibrate") {
		t.Errorf("origin %q does not say a stamped row was saved by calibrate or add", got)
	}

	// The vault catalog overlays the global one, so it wins. Unstamped here, so
	// the message must say the value predates the stamp.
	if err := ai.SaveUserCatalogEntry(ai.ScopeVault, vaultRoot, ai.ModelInfo{
		ID: novaEmbeddingID, Provider: "bedrock", Type: "embedding",
		RecommendedSimilarityThreshold: 0.7,
	}); err != nil {
		t.Fatalf("seed vault: %v", err)
	}
	vaultPath, err := ai.CatalogPathForScope(ai.ScopeVault, vaultRoot)
	if err != nil {
		t.Fatal(err)
	}
	got = thresholdCalibrationOrigin(vaultRoot, "bedrock", novaEmbeddingID)
	if !strings.Contains(got, vaultPath) {
		t.Errorf("origin %q does not prefer the vault catalog %q", got, vaultPath)
	}
	if !strings.Contains(got, "before 2nb stamped provenance") {
		t.Errorf("origin %q does not flag the unstamped pre-stamp value", got)
	}
}
