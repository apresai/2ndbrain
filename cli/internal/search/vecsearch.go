package search

import (
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// vecLiteral formats a vector as the sqlite-vec JSON array literal used to bind
// a query vector to a vec0 MATCH.
func vecLiteral(v []float32) string {
	var b strings.Builder
	b.Grow(len(v) * 9)
	b.WriteByte('[')
	for i, f := range v {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.FormatFloat(float64(f), 'g', -1, 32))
	}
	b.WriteByte(']')
	return b.String()
}

// vecChunkSearchByDoc runs the per-chunk sqlite-vec (vec0) KNN and rolls the
// chunk hits up to document level, keeping the best cosine per document. This
// is the in-SQL replacement for the Go brute-force over whole-document
// embeddings: the vector signal is now per-chunk (a query that matches one
// section scores its document via that section, not a diluted whole-doc
// vector). Returns ok=false (not an error) when the vec_chunks table is absent
// so HybridSearch can fall back to the brute-force path.
func (e *Engine) vecChunkSearchByDoc(query []float32, k int, minScore float64) (results []ScoredDoc, ok bool, err error) {
	if len(query) == 0 {
		return nil, false, nil
	}
	var name string
	switch err := e.db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name='vec_chunks'`).Scan(&name); {
	case errors.Is(err, sql.ErrNoRows):
		return nil, false, nil
	case err != nil:
		return nil, false, err
	}
	if k <= 0 {
		k = 40
	}
	rows, err := e.db.Query(
		`SELECT doc_id, distance FROM vec_chunks WHERE embedding MATCH ? AND k = ? ORDER BY distance`,
		vecLiteral(query), k,
	)
	if err != nil {
		return nil, false, fmt.Errorf("vec0 chunk knn: %w", err)
	}
	defer rows.Close()

	best := make(map[string]float64)
	var order []string
	for rows.Next() {
		var docID string
		var dist float64
		if err := rows.Scan(&docID, &dist); err != nil {
			return nil, false, err
		}
		cos := 1 - dist // cosine distance -> cosine similarity
		if minScore > 0 && cos < minScore {
			continue
		}
		if cur, seen := best[docID]; !seen {
			best[docID] = cos
			order = append(order, docID)
		} else if cos > cur {
			best[docID] = cos
		}
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}

	results = make([]ScoredDoc, 0, len(order))
	for _, id := range order {
		results = append(results, ScoredDoc{DocID: id, Score: best[id]})
	}
	sort.SliceStable(results, func(i, j int) bool { return results[i].Score > results[j].Score })
	return results, true, nil
}
