package ai

import (
	"math"
	"testing"
)

func TestResolveHybridWeights(t *testing.T) {
	cases := []struct {
		name            string
		bm25, vector    float64
		wantBM, wantVec float64
	}{
		{"unset defaults to 1/1", 0, 0, 1, 1},
		{"explicit weights kept", 0.5, 2.0, 0.5, 2.0},
		{"negative treated as unset", -1, -2, 1, 1},
		{"one set one unset", 0, 3, 1, 3},
		// Non-finite weights (e.g. a hand-edited config.yaml) normalize to 1.0
		// rather than poisoning the RRF ranking with NaN/Inf.
		{"NaN normalizes to 1", math.NaN(), 2, 1, 2},
		{"+Inf normalizes to 1", math.Inf(1), 0, 1, 1},
		{"-Inf normalizes to 1", math.Inf(-1), 1.5, 1, 1.5},
	}
	for _, tc := range cases {
		cfg := AIConfig{BM25Weight: tc.bm25, VectorWeight: tc.vector}
		b, v := cfg.ResolveHybridWeights()
		if b != tc.wantBM || v != tc.wantVec {
			t.Errorf("%s: ResolveHybridWeights() = (%g, %g), want (%g, %g)", tc.name, b, v, tc.wantBM, tc.wantVec)
		}
	}
}
