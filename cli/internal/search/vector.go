package search

import (
	"math"
	"sort"
)

// ScoredDoc is a document with a similarity score from vector search. ChunkID /
// HeadingPath identify the document's best-matching chunk (the per-chunk vec0
// winner), so a vector-only hit still knows WHICH section matched — used by the
// parent-document context assembler to window a long note around the answer.
type ScoredDoc struct {
	DocID       string
	Score       float64
	ChunkID     string
	HeadingPath string
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

// VectorSearchThreshold performs brute-force cosine similarity search and
// drops any hit below minScore before sorting. minScore <= 0 means "no filter".
// Use ai.AIConfig.ResolveSimilarityThreshold() to pick a default.
func VectorSearchThreshold(query []float32, docIDs []string, embeddings [][]float32, limit int, minScore float64) []ScoredDoc {
	if len(docIDs) != len(embeddings) || len(docIDs) == 0 {
		return nil
	}

	scored := make([]ScoredDoc, 0, len(docIDs))
	for i, emb := range embeddings {
		score := CosineSimilarity(query, emb)
		if minScore > 0 && score < minScore {
			continue
		}
		scored = append(scored, ScoredDoc{
			DocID: docIDs[i],
			Score: score,
		})
	}

	sort.Slice(scored, func(i, j int) bool {
		return scored[i].Score > scored[j].Score
	})

	if limit > 0 && limit < len(scored) {
		scored = scored[:limit]
	}
	return scored
}

// DocLookup provides document metadata for vector-only results.
type DocLookup interface {
	GetDocumentByID(id string) (Result, bool)
}

// ReciprocalRankFusion combines BM25 and vector search results using RRF.
// score = Σ weight_i/(k + rank_i) where k=60. bm25Weight/vectorWeight bias the
// fusion toward keyword or semantic recall; a non-positive weight defaults to
// 1.0 (classic equal-weight RRF).
// If lookup is provided, vector-only results get populated with full metadata.
// The raw cosine similarity from the vector channel is preserved on each
// result as VectorScore, so callers can surface it alongside the opaque
// RRF score and judge relevance directly.
func ReciprocalRankFusion(bm25Results []Result, vectorResults []ScoredDoc, limit int, lookup DocLookup, bm25Weight, vectorWeight float64) []Result {
	const k = 60.0
	if bm25Weight <= 0 {
		bm25Weight = 1.0
	}
	if vectorWeight <= 0 {
		vectorWeight = 1.0
	}

	scores := make(map[string]float64)
	resultMap := make(map[string]Result)
	vectorScores := make(map[string]float64)

	// Score BM25 results by rank
	for rank, r := range bm25Results {
		scores[r.DocID] += bm25Weight / (k + float64(rank+1))
		resultMap[r.DocID] = r
	}

	// Score vector results by rank and remember the raw cosine
	for rank, v := range vectorResults {
		scores[v.DocID] += vectorWeight / (k + float64(rank+1))
		vectorScores[v.DocID] = v.Score
		if _, exists := resultMap[v.DocID]; !exists {
			// Vector-only result: look up full metadata, and carry the matched
			// chunk's heading/id so the parent-document assembler can window a
			// long note around the section that matched. (A doc already in the
			// map came from BM25 and keeps its own HeadingPath — never touched.)
			r := Result{DocID: v.DocID}
			if lookup != nil {
				if doc, ok := lookup.GetDocumentByID(v.DocID); ok {
					r = doc
				}
			}
			r.HeadingPath = v.HeadingPath
			r.ChunkID = v.ChunkID
			resultMap[v.DocID] = r
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
		r.VectorScore = vectorScores[entries[i].docID]
		results[i] = r
	}
	return results
}
