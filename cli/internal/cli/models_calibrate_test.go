package cli

import (
	"math"
	"math/rand"
	"testing"
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
