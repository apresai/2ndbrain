package search

import (
	"math"
	"testing"
)

func TestCosineSimilarity(t *testing.T) {
	tests := []struct {
		name string
		a, b []float32
		want float64
	}{
		{"identical", []float32{1, 0, 0}, []float32{1, 0, 0}, 1.0},
		{"orthogonal", []float32{1, 0, 0}, []float32{0, 1, 0}, 0.0},
		{"opposite", []float32{1, 0, 0}, []float32{-1, 0, 0}, -1.0},
		{"similar", []float32{1, 1, 0}, []float32{1, 0, 0}, 1.0 / math.Sqrt(2)},
		{"empty", []float32{}, []float32{}, 0.0},
		{"mismatched", []float32{1, 2}, []float32{1, 2, 3}, 0.0},
		{"zero vector", []float32{0, 0, 0}, []float32{1, 0, 0}, 0.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CosineSimilarity(tt.a, tt.b)
			if math.Abs(got-tt.want) > 1e-6 {
				t.Errorf("CosineSimilarity = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestVectorSearchThreshold(t *testing.T) {
	query := []float32{1, 0, 0}
	docIDs := []string{"a", "b", "c"}
	embeddings := [][]float32{
		{0, 1, 0},     // orthogonal to query
		{1, 0, 0},     // identical to query
		{0.7, 0.7, 0}, // similar to query
	}

	results := VectorSearchThreshold(query, docIDs, embeddings, 3, 0)

	if len(results) != 3 {
		t.Fatalf("got %d results, want 3", len(results))
	}

	// First result should be "b" (identical)
	if results[0].DocID != "b" {
		t.Errorf("first result = %q, want %q", results[0].DocID, "b")
	}
	if math.Abs(results[0].Score-1.0) > 1e-6 {
		t.Errorf("first score = %v, want 1.0", results[0].Score)
	}

	// Second should be "c" (similar)
	if results[1].DocID != "c" {
		t.Errorf("second result = %q, want %q", results[1].DocID, "c")
	}

	// Third should be "a" (orthogonal)
	if results[2].DocID != "a" {
		t.Errorf("third result = %q, want %q", results[2].DocID, "a")
	}
}

func TestVectorSearchLimit(t *testing.T) {
	query := []float32{1, 0}
	docIDs := []string{"a", "b", "c"}
	embeddings := [][]float32{{1, 0}, {0, 1}, {0.5, 0.5}}

	results := VectorSearchThreshold(query, docIDs, embeddings, 2, 0)
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}
}

func TestReciprocalRankFusion(t *testing.T) {
	bm25 := []Result{
		{DocID: "a", Score: 10},
		{DocID: "b", Score: 8},
		{DocID: "c", Score: 5},
	}
	vector := []ScoredDoc{
		{DocID: "c", Score: 0.95},
		{DocID: "d", Score: 0.80},
		{DocID: "a", Score: 0.70},
	}

	results := ReciprocalRankFusion(bm25, vector, 4, nil, 1, 1)

	if len(results) != 4 {
		t.Fatalf("got %d results, want 4", len(results))
	}

	// "a" should be top — it's rank 1 in BM25 and rank 3 in vector
	// "c" should also be high — rank 3 in BM25 and rank 1 in vector
	// Both "a" and "c" appear in both lists so they get double RRF boost
	topIDs := map[string]bool{results[0].DocID: true, results[1].DocID: true}
	if !topIDs["a"] || !topIDs["c"] {
		t.Errorf("expected 'a' and 'c' in top 2, got %q and %q", results[0].DocID, results[1].DocID)
	}
}

func TestRRFEmptyInputs(t *testing.T) {
	results := ReciprocalRankFusion(nil, nil, 10, nil, 1, 1)
	if len(results) != 0 {
		t.Errorf("expected 0 results for empty inputs, got %d", len(results))
	}
}

// TestReciprocalRankFusion_VectorOnlyHeading locks the parent-document Phase-2
// wiring: a vector-only hit carries its matched chunk's heading/id into the
// merged Result, but a doc that also matched BM25 keeps the BM25 heading.
func TestReciprocalRankFusion_VectorOnlyHeading(t *testing.T) {
	bm25 := []Result{{DocID: "a", Path: "a.md", HeadingPath: "## BM25 Section"}}
	vector := []ScoredDoc{
		{DocID: "a", Score: 0.9, HeadingPath: "## Semantic Section"},         // also matched BM25
		{DocID: "b", Score: 0.8, HeadingPath: "## Deep", ChunkID: "chunk-b"}, // vector-only
	}
	byID := map[string]Result{}
	for _, r := range ReciprocalRankFusion(bm25, vector, 5, nil, 1, 1) {
		byID[r.DocID] = r
	}
	if byID["a"].HeadingPath != "## BM25 Section" {
		t.Errorf("BM25 hit heading overwritten by vector: got %q", byID["a"].HeadingPath)
	}
	if byID["b"].HeadingPath != "## Deep" || byID["b"].ChunkID != "chunk-b" {
		t.Errorf("vector-only matched chunk not propagated: heading=%q chunk=%q", byID["b"].HeadingPath, byID["b"].ChunkID)
	}
}

func TestReciprocalRankFusion_Weighting(t *testing.T) {
	bm25 := []Result{{DocID: "a", Score: 10}}       // a: BM25 rank 1 only
	vector := []ScoredDoc{{DocID: "b", Score: 0.9}} // b: vector rank 1 only

	// Equal weights → a and b tie (each is rank 1 in its own channel).
	eq := ReciprocalRankFusion(bm25, vector, 2, nil, 1, 1)
	if len(eq) != 2 || eq[0].Score != eq[1].Score {
		t.Fatalf("equal weights: expected a tie, got %+v", eq)
	}

	// Vector weighted up → the vector-only doc b outranks the bm25-only doc a.
	if got := ReciprocalRankFusion(bm25, vector, 2, nil, 1, 3); got[0].DocID != "b" {
		t.Errorf("vector-weighted: top = %q, want b", got[0].DocID)
	}
	// BM25 weighted up → a outranks b.
	if got := ReciprocalRankFusion(bm25, vector, 2, nil, 3, 1); got[0].DocID != "a" {
		t.Errorf("bm25-weighted: top = %q, want a", got[0].DocID)
	}
	// Non-positive weights default to 1.0 (equal-weight RRF).
	if got := ReciprocalRankFusion(bm25, vector, 2, nil, 0, 0); got[0].Score != eq[0].Score {
		t.Errorf("zero weights should default to 1.0: got %.6f, want %.6f", got[0].Score, eq[0].Score)
	}
}
