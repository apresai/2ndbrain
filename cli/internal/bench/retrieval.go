package bench

import (
	"database/sql"
	"errors"
	"fmt"
	"math"
	"sort"

	"github.com/apresai/2ndbrain/internal/search"
)

// MinLinksForRetrievalProbe is the minimum number of resolved wikilink
// pairs a vault must have before the retrieval-quality probe produces
// a meaningful score. Below this, the probe returns ErrTooFewLinks.
const MinLinksForRetrievalProbe = 10

// DefaultRetrievalK is the cutoff for MRR@K. K=10 matches typical
// information-retrieval benchmarks and is short enough that a user can
// scan top-10 search results without friction.
const DefaultRetrievalK = 10

// ErrTooFewLinks is returned when the vault has fewer than
// MinLinksForRetrievalProbe resolved wikilink pairs — the score
// would be noise. Callers should treat this as "probe skipped".
var ErrTooFewLinks = errors.New("too few resolved wikilinks to compute retrieval-quality score")

// RetrievalQualityResult carries both the numeric score and the
// ground-truth size that produced it, so comparisons between runs
// aren't misleading when the vault grew.
type RetrievalQualityResult struct {
	// ScoreMRR is the mean reciprocal rank of the link target across all
	// (source, target) pairs, capped at K. Value is in [0, 1]; 1 means
	// every link target was the top-ranked semantic neighbor of its
	// source, 0 means no target appeared within top-K for any pair.
	ScoreMRR float64 `json:"score_mrr"`

	// ScoreRecallAtK is the fraction of pairs where the target appears
	// in top-K results — simpler to interpret than MRR for end users.
	ScoreRecallAtK float64 `json:"score_recall_at_k"`

	// K is the cutoff used (usually DefaultRetrievalK).
	K int `json:"k"`

	// PairsUsed is the number of (source, target) wikilink pairs the
	// probe evaluated against. Surfaced so a 0.80 score on 500 pairs
	// is visibly different from 0.80 on 10 pairs.
	PairsUsed int `json:"pairs_used"`

	// Documents is the vault document count at probe time, for context.
	Documents int `json:"documents"`
}

// RetrievalQualityProbe scores the vault's currently-stored embeddings
// by checking whether resolved wikilink targets appear in the top-K
// semantic neighbors of their source. Reads from DB directly; makes no
// AI API calls. Returns ErrTooFewLinks if the vault is too sparse.
//
// Only considers pairs where both source and target have stored
// embeddings — newly-created docs without embeddings are skipped.
func RetrievalQualityProbe(db *sql.DB) (RetrievalQualityResult, error) {
	return runRetrievalProbe(db, DefaultRetrievalK)
}

func runRetrievalProbe(db *sql.DB, k int) (RetrievalQualityResult, error) {
	if k <= 0 {
		k = DefaultRetrievalK
	}

	// Pull embeddings: parallel slices of docID and []float32.
	ids, vectors, err := loadEmbeddings(db)
	if err != nil {
		return RetrievalQualityResult{}, fmt.Errorf("load embeddings: %w", err)
	}
	if len(ids) == 0 {
		return RetrievalQualityResult{}, ErrTooFewLinks
	}

	// Index by doc ID for quick lookup.
	index := make(map[string]int, len(ids))
	for i, id := range ids {
		index[id] = i
	}

	pairs, err := loadResolvedLinkPairs(db)
	if err != nil {
		return RetrievalQualityResult{}, fmt.Errorf("load links: %w", err)
	}

	// Filter pairs whose endpoints both have embeddings; anything else
	// can't contribute to a semantic-neighbor score.
	usable := make([][2]int, 0, len(pairs))
	for _, p := range pairs {
		srcIdx, sOK := index[p[0]]
		tgtIdx, tOK := index[p[1]]
		if !sOK || !tOK || srcIdx == tgtIdx {
			continue
		}
		usable = append(usable, [2]int{srcIdx, tgtIdx})
	}
	if len(usable) < MinLinksForRetrievalProbe {
		return RetrievalQualityResult{
			K:         k,
			PairsUsed: len(usable),
			Documents: len(ids),
		}, ErrTooFewLinks
	}

	// For each pair, rank the source's neighbors by cosine and look up
	// where the target sits. We recompute per-source rather than caching
	// an N×N matrix — vaults with N>5k would otherwise blow memory and
	// the probe already dominates cheap vaults' disk read.
	var mrrSum float64
	hits := 0
	for _, pair := range usable {
		srcIdx, tgtIdx := pair[0], pair[1]
		rank := rankOfTarget(vectors, srcIdx, tgtIdx, k)
		if rank == 0 {
			continue
		}
		mrrSum += 1.0 / float64(rank)
		hits++
	}

	return RetrievalQualityResult{
		ScoreMRR:       mrrSum / float64(len(usable)),
		ScoreRecallAtK: float64(hits) / float64(len(usable)),
		K:              k,
		PairsUsed:      len(usable),
		Documents:      len(ids),
	}, nil
}

// rankOfTarget returns the 1-indexed position of targetIdx among the
// top-k most similar neighbors of sourceIdx, or 0 if the target isn't
// in the top-k.
func rankOfTarget(vectors [][]float32, sourceIdx, targetIdx, k int) int {
	type scored struct {
		idx   int
		score float64
	}
	src := vectors[sourceIdx]
	scores := make([]scored, 0, len(vectors)-1)
	for i, v := range vectors {
		if i == sourceIdx {
			continue
		}
		scores = append(scores, scored{idx: i, score: search.CosineSimilarity(src, v)})
	}
	sort.Slice(scores, func(i, j int) bool { return scores[i].score > scores[j].score })
	if len(scores) > k {
		scores = scores[:k]
	}
	for rank, s := range scores {
		if s.idx == targetIdx {
			return rank + 1
		}
	}
	return 0
}

// loadEmbeddings mirrors (store.DB).AllEmbeddings without importing the
// store package (which would create a dependency cycle).
func loadEmbeddings(db *sql.DB) (ids []string, vectors [][]float32, err error) {
	rows, err := db.Query(`SELECT id, embedding FROM documents WHERE embedding IS NOT NULL`)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var id string
		var blob []byte
		if err := rows.Scan(&id, &blob); err != nil {
			return nil, nil, err
		}
		vec, ok := decodeEmbedding(blob)
		if !ok {
			continue
		}
		ids = append(ids, id)
		vectors = append(vectors, vec)
	}
	return ids, vectors, rows.Err()
}

// decodeEmbedding converts a raw float32-LE blob to a []float32. Returns
// (_, false) when the blob length isn't a multiple of 4.
func decodeEmbedding(blob []byte) ([]float32, bool) {
	if len(blob) == 0 || len(blob)%4 != 0 {
		return nil, false
	}
	n := len(blob) / 4
	out := make([]float32, n)
	for i := 0; i < n; i++ {
		bits := uint32(blob[i*4]) |
			uint32(blob[i*4+1])<<8 |
			uint32(blob[i*4+2])<<16 |
			uint32(blob[i*4+3])<<24
		out[i] = math.Float32frombits(bits)
	}
	return out, true
}

// loadResolvedLinkPairs returns (source_id, target_id) pairs where the
// link is resolved. Unresolved links point at nothing useful for scoring.
func loadResolvedLinkPairs(db *sql.DB) ([][2]string, error) {
	rows, err := db.Query(`
		SELECT source_id, target_id
		FROM links
		WHERE resolved = 1 AND target_id IS NOT NULL AND target_id != ''
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var pairs [][2]string
	for rows.Next() {
		var src, tgt string
		if err := rows.Scan(&src, &tgt); err != nil {
			return nil, err
		}
		pairs = append(pairs, [2]string{src, tgt})
	}
	return pairs, rows.Err()
}
