package eval

import (
	"math"
	"testing"

	"github.com/apresai/2ndbrain/internal/ai"
)

func approx(a, b float64) bool { return math.Abs(a-b) < 1e-6 }

func TestCosine(t *testing.T) {
	cases := []struct {
		name string
		a, b []float32
		want float64
	}{
		{"identical", []float32{1, 0}, []float32{1, 0}, 1},
		{"orthogonal", []float32{1, 0}, []float32{0, 1}, 0},
		{"opposite", []float32{1, 0}, []float32{-1, 0}, -1},
		{"45deg", []float32{1, 1}, []float32{1, 0}, 1 / math.Sqrt2},
		{"scale-invariant", []float32{2, 0}, []float32{5, 0}, 1},
		{"zero-norm", []float32{0, 0}, []float32{1, 0}, 0},
	}
	for _, tc := range cases {
		got := cosine(tc.a, tc.b, l2(tc.a), l2(tc.b))
		if !approx(got, tc.want) {
			t.Errorf("%s: cosine = %.6f, want %.6f", tc.name, got, tc.want)
		}
	}
}

func TestL2AndMean(t *testing.T) {
	if got := l2([]float32{3, 4}); !approx(got, 5) {
		t.Errorf("l2([3,4]) = %v, want 5", got)
	}
	if got := mean([]float64{1, 2, 3}); !approx(got, 2) {
		t.Errorf("mean = %v, want 2", got)
	}
	if got := mean(nil); got != 0 {
		t.Errorf("mean(nil) = %v, want 0", got)
	}
}

func TestPctNearestRank(t *testing.T) {
	s := []float64{1, 2, 3, 4, 5} // sorted
	// nearest-rank: idx = ceil(p*n)-1
	if got := pct(s, 0.50); got != 3 { // ceil(2.5)-1 = 2 -> s[2]=3
		t.Errorf("pct p50 = %v, want 3", got)
	}
	if got := pct(s, 0.90); got != 5 { // ceil(4.5)-1 = 4 -> s[4]=5
		t.Errorf("pct p90 = %v, want 5", got)
	}
	if got := pct(s, 0.95); got != 5 {
		t.Errorf("pct p95 = %v, want 5", got)
	}
	if got := pct(nil, 0.5); got != 0 {
		t.Errorf("pct(nil) = %v, want 0", got)
	}
}

// TestSuggestThreshold pins the data-driven 0.25 derivation that ships as the
// Nova-2 builtin: just above negative p95, capped under true-match p50.
func TestSuggestThreshold(t *testing.T) {
	cases := []struct {
		name            string
		negP95, trueP50 float64
		want            float64
	}{
		{"nova measured -> 0.25", 0.229, 0.344, 0.25}, // 0.229+0.02=0.249 -> 0.25
		{"clamped under true p50", 0.50, 0.30, 0.29},  // 0.52 capped to 0.30-0.01=0.29
		{"clean separation", 0.10, 0.80, 0.12},        // 0.12 < 0.79 cap
	}
	for _, tc := range cases {
		if got := suggestThreshold(tc.negP95, tc.trueP50); !approx(got, tc.want) {
			t.Errorf("%s: suggestThreshold(%.3f, %.3f) = %.2f, want %.2f", tc.name, tc.negP95, tc.trueP50, got, tc.want)
		}
	}
}

// TestScorePurpose drives the ranking/metric math over a hand-built corpus.
func TestScorePurpose(t *testing.T) {
	// Perfect case: each query equals its own document (orthonormal corpus), so
	// every true match is rank 1 and negatives are orthogonal (cos 0).
	docs := [][]float32{{1, 0, 0}, {0, 1, 0}, {0, 0, 1}}
	norms := []float64{l2(docs[0]), l2(docs[1]), l2(docs[2])}
	queries := [][]float32{{1, 0, 0}, {0, 1, 0}, {0, 0, 1}}
	r := scorePurpose(ai.PurposeQuery, docs, norms, queries, 10)
	if r.N != 3 || !approx(r.MRRAtK, 1) || !approx(r.RecallAt1, 1) || !approx(r.RecallAtK, 1) {
		t.Errorf("perfect: N=%d MRR=%.3f R@1=%.3f R@k=%.3f, want 3/1/1/1", r.N, r.MRRAtK, r.RecallAt1, r.RecallAtK)
	}
	if !approx(r.TrueP50, 1) {
		t.Errorf("perfect: TrueP50=%.3f, want 1", r.TrueP50)
	}

	// Rank-2 case: each query points at the OTHER document, so the true match is
	// always rank 2 (MRR = 1/2, R@1 = 0, R@10 = 1).
	d2 := [][]float32{{1, 0}, {0, 1}}
	n2 := []float64{1, 1}
	q2 := [][]float32{{0, 1}, {1, 0}}
	r2 := scorePurpose(ai.PurposeIndex, d2, n2, q2, 10)
	if !approx(r2.MRRAtK, 0.5) || !approx(r2.RecallAt1, 0) || !approx(r2.RecallAtK, 1) {
		t.Errorf("rank-2: MRR=%.3f R@1=%.3f R@k=%.3f, want 0.5/0/1", r2.MRRAtK, r2.RecallAt1, r2.RecallAtK)
	}
}
