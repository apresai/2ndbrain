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

func TestVectorSearch(t *testing.T) {
	query := []float32{1, 0, 0}
	docIDs := []string{"a", "b", "c"}
	embeddings := [][]float32{
		{0, 1, 0},   // orthogonal to query
		{1, 0, 0},   // identical to query
		{0.7, 0.7, 0}, // similar to query
	}

	results := VectorSearch(query, docIDs, embeddings, 3)

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

	results := VectorSearch(query, docIDs, embeddings, 2)
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

	results := ReciprocalRankFusion(bm25, vector, 4)

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
	results := ReciprocalRankFusion(nil, nil, 10)
	if len(results) != 0 {
		t.Errorf("expected 0 results for empty inputs, got %d", len(results))
	}
}
