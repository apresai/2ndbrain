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
// OR not yet covering the whole corpus, so HybridSearch can fall back to the
// brute-force path.
//
// wantCoverage is the number of documents that have a doc-level embedding (the
// brute-force corpus size). vec_chunks is populated lazily per-doc as notes are
// (re)embedded, so on a vault upgraded to the vec0 index it can hold only the
// few docs touched since. Taking the vec0 path then would silently hide every
// not-yet-re-embedded doc from the vector channel (the brute-force fallback
// over the full doc-level corpus would be unreachable). Every doc with a
// doc-level embedding went through embed.Document, which writes BOTH a chunk
// vector and the mean doc vector, so once the vault is fully migrated the
// distinct-doc count in vec_chunks equals wantCoverage. Until then we defer to
// brute-force, which covers every embedded doc.
func (e *Engine) vecChunkSearchByDoc(query []float32, k int, minScore float64, wantCoverage int) (results []ScoredDoc, ok bool, err error) {
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
	if wantCoverage > 0 {
		var covered int
		if err := e.db.QueryRow(`SELECT COUNT(DISTINCT doc_id) FROM vec_chunks`).Scan(&covered); err != nil {
			return nil, false, err
		}
		if covered < wantCoverage {
			return nil, false, nil
		}
	}
	if k <= 0 {
		k = 40
	}
	rows, err := e.db.Query(
		`SELECT chunk_id, doc_id, distance FROM vec_chunks WHERE embedding MATCH ? AND k = ? ORDER BY distance`,
		vecLiteral(query), k,
	)
	if err != nil {
		return nil, false, fmt.Errorf("vec0 chunk knn: %w", err)
	}
	defer rows.Close()

	best := make(map[string]float64)
	bestChunk := make(map[string]string) // doc_id -> winning chunk_id
	var order []string
	for rows.Next() {
		var chunkID, docID string
		var dist sql.NullFloat64
		if err := rows.Scan(&chunkID, &docID, &dist); err != nil {
			return nil, false, err
		}
		if !dist.Valid {
			continue // NULL distance from a poisoned (zero-norm/NaN) stored row
		}
		cos := 1 - dist.Float64 // cosine distance -> cosine similarity
		if minScore > 0 && cos < minScore {
			continue
		}
		if cur, seen := best[docID]; !seen {
			best[docID] = cos
			bestChunk[docID] = chunkID
			order = append(order, docID)
		} else if cos > cur {
			best[docID] = cos
			bestChunk[docID] = chunkID
		}
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}

	// Resolve each winning chunk's heading path so a vector-only hit can be
	// windowed around the matched section. Best-effort: a miss leaves it "".
	headings := e.headingPathsForChunks(bestChunk)

	results = make([]ScoredDoc, 0, len(order))
	for _, id := range order {
		cid := bestChunk[id]
		results = append(results, ScoredDoc{DocID: id, Score: best[id], ChunkID: cid, HeadingPath: headings[cid]})
	}
	sort.SliceStable(results, func(i, j int) bool { return results[i].Score > results[j].Score })
	return results, true, nil
}

// headingPathsForChunks maps each chunk_id to its heading_path from the chunks
// table in one round-trip. Missing rows (file changed since index) are absent,
// so the caller treats them as "" (windowing falls back to the note head).
func (e *Engine) headingPathsForChunks(byDoc map[string]string) map[string]string {
	out := make(map[string]string)
	seen := make(map[string]bool)
	var ids []string
	for _, cid := range byDoc {
		if cid != "" && !seen[cid] {
			seen[cid] = true
			ids = append(ids, cid)
		}
	}
	if len(ids) == 0 {
		return out
	}
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	q := `SELECT id, heading_path FROM chunks WHERE id IN (?` + strings.Repeat(",?", len(ids)-1) + `)`
	rows, err := e.db.Query(q, args...)
	if err != nil {
		return out // best-effort
	}
	defer rows.Close()
	for rows.Next() {
		var id, hp string
		if err := rows.Scan(&id, &hp); err != nil {
			return out
		}
		out[id] = hp
	}
	return out
}
