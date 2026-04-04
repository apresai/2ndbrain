package search

import (
	"math"
	"sort"
)

// ScoredDoc is a document with a similarity score from vector search.
type ScoredDoc struct {
	DocID string
	Score float64
}

// CosineSimilarity computes the cosine similarity between two vectors.
func CosineSimilarity(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		normA += float64(a[i]) * float64(a[i])
		normB += float64(b[i]) * float64(b[i])
	}
	denom := math.Sqrt(normA) * math.Sqrt(normB)
	if denom == 0 {
		return 0
	}
	return dot / denom
}

// VectorSearch performs brute-force cosine similarity search over all embeddings.
func VectorSearch(query []float32, docIDs []string, embeddings [][]float32, limit int) []ScoredDoc {
	if len(docIDs) != len(embeddings) || len(docIDs) == 0 {
		return nil
	}

	scored := make([]ScoredDoc, len(docIDs))
	for i, emb := range embeddings {
		scored[i] = ScoredDoc{
			DocID: docIDs[i],
			Score: CosineSimilarity(query, emb),
		}
	}

	sort.Slice(scored, func(i, j int) bool {
		return scored[i].Score > scored[j].Score
	})

	if limit > len(scored) {
		limit = len(scored)
	}
	return scored[:limit]
}

// DocLookup provides document metadata for vector-only results.
type DocLookup interface {
	GetDocumentByID(id string) (Result, bool)
}

// ReciprocalRankFusion combines BM25 and vector search results using RRF.
// score = Σ 1/(k + rank_i) where k=60.
// If lookup is provided, vector-only results get populated with full metadata.
func ReciprocalRankFusion(bm25Results []Result, vectorResults []ScoredDoc, limit int, lookup DocLookup) []Result {
	const k = 60.0

	scores := make(map[string]float64)
	resultMap := make(map[string]Result)

	// Score BM25 results by rank
	for rank, r := range bm25Results {
		scores[r.DocID] += 1.0 / (k + float64(rank+1))
		resultMap[r.DocID] = r
	}

	// Score vector results by rank
	for rank, v := range vectorResults {
		scores[v.DocID] += 1.0 / (k + float64(rank+1))
		if _, exists := resultMap[v.DocID]; !exists {
			// Try to look up full metadata for vector-only results
			if lookup != nil {
				if doc, ok := lookup.GetDocumentByID(v.DocID); ok {
					resultMap[v.DocID] = doc
				} else {
					resultMap[v.DocID] = Result{DocID: v.DocID}
				}
			} else {
				resultMap[v.DocID] = Result{DocID: v.DocID}
			}
		}
	}

	// Collect and sort by combined RRF score
	type rrfEntry struct {
		docID string
		score float64
	}
	entries := make([]rrfEntry, 0, len(scores))
	for id, s := range scores {
		entries = append(entries, rrfEntry{id, s})
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].score > entries[j].score
	})

	if limit > len(entries) {
		limit = len(entries)
	}

	results := make([]Result, limit)
	for i := 0; i < limit; i++ {
		r := resultMap[entries[i].docID]
		r.Score = entries[i].score
		results[i] = r
	}
	return results
}
