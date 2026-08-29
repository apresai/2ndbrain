package cli

import (
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/apresai/2ndbrain/internal/ai"
)

func TestPercentile(t *testing.T) {
	// A simple ascending sequence lets us hand-check percentile lookups.
	sorted := []float64{0.0, 0.1, 0.2, 0.3, 0.4, 0.5, 0.6, 0.7, 0.8, 0.9, 1.0}
	tests := []struct {
		q    float64
		want float64
	}{
		{0, 0.0},
		{0.5, 0.5},
		{1.0, 1.0},
		{-0.5, 0.0}, // clamp low
		{1.5, 1.0},  // clamp high
	}
	for _, tt := range tests {
		got := percentile(sorted, tt.q)
		if math.Abs(got-tt.want) > 1e-9 {
			t.Errorf("percentile(%v) = %v, want %v", tt.q, got, tt.want)
		}
	}

	if got := percentile(nil, 0.5); got != 0 {
		t.Errorf("percentile on empty slice should return 0, got %v", got)
	}
}

func TestSampleUnrelatedCosines(t *testing.T) {
	// Build 10 orthogonal-ish vectors so cosines land in a known band.
	vecs := make([][]float32, 10)
	for i := range vecs {
		v := make([]float32, 8)
		v[i%8] = 1.0
		v[(i+1)%8] = 0.5
		vecs[i] = v
	}
	rng := rand.New(rand.NewSource(42))
	got := sampleUnrelatedCosines(rng, vecs, 20)
	if len(got) == 0 {
		t.Fatal("expected non-empty sample")
	}
	if len(got) > 20 {
		t.Errorf("sample count = %d, want <= 20", len(got))
	}
	for _, c := range got {
		if math.IsNaN(c) || c < -1.001 || c > 1.001 {
			t.Errorf("cosine out of range: %v", c)
		}
	}
}

func TestSampleUnrelatedCosines_SkipsMismatchedDims(t *testing.T) {
	// Mixed-dimension vectors should be skipped, not crash.
	vecs := [][]float32{
		{1, 0, 0},
		{0, 1, 0},
		{1, 0}, // mismatched
		{0, 1, 0, 0},
	}
	rng := rand.New(rand.NewSource(1))
	got := sampleUnrelatedCosines(rng, vecs, 50)
	// The only compatible pair is (0,1) — both 3-dim. Others should be skipped.
	// We can't guarantee exactly one sample (depends on dedup + random), but
	// every returned cosine must come from matching dims.
	for _, c := range got {
		if math.IsNaN(c) {
			t.Errorf("NaN in output")
		}
	}
}

func TestSampleUnrelatedCosines_EmptyInputs(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	if got := sampleUnrelatedCosines(rng, nil, 10); got != nil {
		t.Errorf("nil vecs should return nil, got %v", got)
	}
	if got := sampleUnrelatedCosines(rng, [][]float32{{1, 0}}, 10); got != nil {
		t.Errorf("single-vec input should return nil (no pairs), got %v", got)
	}
}

// TestSaveCalibrationPreservesExistingRowFields pins that --save is a
// one-field write. SaveUserCatalogEntry replaces the matching route wholesale,
// so constructing a fresh six-field ModelInfo destroyed the stored verdict,
// any user price override, and the enabled pointer.
func TestSaveCalibrationPreservesExistingRowFields(t *testing.T) {
	_, root := newContractVault(t)
	modelID := "amazon.nova-2-multimodal-embeddings-v1:0"
	enabled := false
	seed := ai.ModelInfo{
		ID:                             modelID,
		Provider:                       "bedrock",
		Type:                           "embedding",
		Tier:                           ai.TierUserVerified,
		Plane:                          ai.PlaneClassic,
		Region:                         "us-east-1",
		TestedAt:                       "2026-08-01T00:00:00Z",
		PriceOverride:                  true,
		PriceIn:                        1.25,
		PriceSource:                    "user",
		Enabled:                        &enabled,
		Notes:                          "keep me",
		RecommendedSimilarityThreshold: 0.20,
	}
	if err := ai.SaveUserCatalogEntry(ai.ScopeVault, root, seed); err != nil {
		t.Fatalf("seed: %v", err)
	}

	cfg := ai.AIConfig{
		Provider:        "bedrock",
		EmbeddingModel:  modelID,
		EmbeddingPlane:  ai.PlaneClassic,
		EmbeddingRegion: "us-east-1",
	}
	if err := saveCalibration(ai.ScopeVault, root, "bedrock", modelID, 0.33, cfg); err != nil {
		t.Fatalf("saveCalibration: %v", err)
	}

	got, ok := ai.UserCatalogEntry(ai.ScopeVault, root, seed.Route())
	if !ok {
		t.Fatal("calibrate --save dropped the stored row")
	}
	if got.RecommendedSimilarityThreshold != 0.33 {
		t.Errorf("threshold = %v, want 0.33", got.RecommendedSimilarityThreshold)
	}
	if got.TestedAt != seed.TestedAt {
		t.Errorf("verdict timestamp erased: got %q", got.TestedAt)
	}
	if !got.PriceOverride || got.PriceIn != 1.25 || got.PriceSource != "user" {
		t.Errorf("price override erased: override=%v in=%v source=%q", got.PriceOverride, got.PriceIn, got.PriceSource)
	}
	if got.Enabled == nil || *got.Enabled {
		t.Errorf("enabled pointer erased: %v", got.Enabled)
	}
	if got.Notes != "keep me" {
		t.Errorf("notes erased: %q", got.Notes)
	}
}

func TestSaveCalibrationUpgradesPrerouteRow(t *testing.T) {
	_, root := newContractVault(t)
	modelID := "amazon.nova-2-multimodal-embeddings-v1:0"
	enabled := false
	seed := ai.ModelInfo{
		ID:            modelID,
		Provider:      "bedrock",
		Type:          "embedding",
		Tier:          ai.TierUserVerified,
		TestedAt:      "2026-08-01T00:00:00Z",
		PriceOverride: true,
		PriceIn:       1.25,
		PriceSource:   "user",
		Enabled:       &enabled,
		Notes:         "keep me",
	}
	if err := ai.SaveUserCatalogEntry(ai.ScopeVault, root, seed); err != nil {
		t.Fatalf("seed: %v", err)
	}
	cfg := ai.AIConfig{Provider: "bedrock", EmbeddingModel: modelID}
	if err := saveCalibration(ai.ScopeVault, root, "bedrock", modelID, 0.33, cfg); err != nil {
		t.Fatalf("saveCalibration: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(root, ".2ndbrain", "models.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(data), "id: "+modelID); n != 1 {
		t.Fatalf("calibrate --save on a pre-route row produced %d file rows, want 1:\n%s", n, data)
	}
	got, ok := ai.UserCatalogEntry(ai.ScopeVault, root, ai.RouteKey{
		Provider: "bedrock", ID: modelID, Plane: ai.PlaneClassic,
	})
	if !ok {
		t.Fatal("routed lookup missed the upgraded pre-route row")
	}
	if got.TestedAt != seed.TestedAt || got.Enabled == nil || *got.Enabled || got.Notes != "keep me" {
		t.Errorf("stored fields lost: tested_at=%q enabled=%v notes=%q", got.TestedAt, got.Enabled, got.Notes)
	}
	if got.RecommendedSimilarityThreshold != 0.33 {
		t.Errorf("threshold = %v, want 0.33", got.RecommendedSimilarityThreshold)
	}
}
