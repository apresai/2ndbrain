// Package embed is the single source of truth for embedding a document into the
// vector index, shared by `2nb index`, `2nb create`, and the MCP write tools.
//
// It embeds each heading-bounded chunk and stores the per-chunk vectors in
// vec_chunks (sqlite-vec vec0) for chunk-level KNN, and stores the MEAN of the
// chunk vectors as the document-level embedding (documents.embedding) so
// calibrate / VectorCompat / the brute-force fallback keep working with no
// extra API call.
package embed

import (
	"context"
	"fmt"
	"strings"

	"github.com/apresai/2ndbrain/internal/ai"
	"github.com/apresai/2ndbrain/internal/document"
	"github.com/apresai/2ndbrain/internal/store"
)

// Document embeds parsed's chunks and stores them under docID. It returns the
// number of chunks embedded; an empty document yields (0, nil) — skipped, not an
// error, since providers like Amazon Nova-2 reject empty input. docID is the
// stored document id; it is pinned onto parsed so chunk IDs (derived from the
// doc id) match the chunks table even when the file's frontmatter id differs or
// is absent (auto-assigned notes).
func Document(ctx context.Context, db *store.DB, embedder ai.EmbeddingProvider, docID string, parsed *document.Document, model string) (int, error) {
	parsed.ID = docID

	// ChunkForStorage matches the indexer's chunk set (heading chunks with any
	// oversized section sub-split), so the vec search's vec_chunks -> chunks
	// chunk_id join stays valid and no chunk exceeds Nova's embed limits.
	chunks := document.ChunkForStorage(parsed)
	// Dedup the kept chunks by ID (last occurrence wins), mirroring the chunks
	// table's INSERT ... ON CONFLICT(id) DO UPDATE collapse. Two sections that
	// share a heading path (e.g. two `## Notes`, a repeated changelog date)
	// produce the SAME chunk_id (makeChunkID = sha256(docID+":"+headingPath)),
	// and vec0's chunk_id PRIMARY KEY has no UPSERT — a duplicate INSERT would
	// hard-error and leave the doc with no embedding. Deduping before embedding
	// also avoids paying for an embedding call on the discarded collision.
	idx := make(map[string]int, len(chunks))
	var embedChunks []document.Chunk
	for _, ch := range chunks {
		if strings.TrimSpace(ch.Content) == "" {
			continue
		}
		if i, dup := idx[ch.ID]; dup {
			embedChunks[i] = ch // last wins
			continue
		}
		idx[ch.ID] = len(embedChunks)
		embedChunks = append(embedChunks, ch)
	}
	if len(embedChunks) == 0 {
		return 0, nil
	}
	contents := make([]string, len(embedChunks))
	for i, ch := range embedChunks {
		contents[i] = ch.Content
	}

	vecs, err := embedder.Embed(ctx, contents)
	if err != nil {
		return 0, fmt.Errorf("embed chunks: %w", err)
	}
	if len(vecs) != len(contents) || len(vecs[0]) == 0 {
		return 0, fmt.Errorf("provider returned %d embeddings for %d chunks", len(vecs), len(contents))
	}

	if err := db.EnsureVecChunks(len(vecs[0])); err != nil {
		return 0, fmt.Errorf("ensure vec_chunks: %w", err)
	}
	cvs := make([]store.ChunkVector, len(embedChunks))
	for i, ch := range embedChunks {
		cvs[i] = store.ChunkVector{ChunkID: ch.ID, DocID: docID, ContentHash: ch.ContentHash, Vector: vecs[i]}
	}
	if err := db.SetDocChunkVectors(docID, cvs, model); err != nil {
		return 0, fmt.Errorf("store chunk vectors: %w", err)
	}

	// Build the doc-level mean from the usable vectors only: a NaN/Inf or
	// zero-norm chunk vector would poison documents.embedding (and the
	// brute-force fallback that scans it) the same way it poisons vec0's cosine
	// KNN. If every chunk vector is degenerate, skip the doc (0, nil) — the same
	// non-error path as an empty document.
	usable := make([][]float32, 0, len(vecs))
	for _, v := range vecs {
		if store.FiniteNonZero(v) {
			usable = append(usable, v)
		}
	}
	if len(usable) == 0 {
		return 0, nil
	}

	parsed.ComputeContentHash()
	if err := db.SetEmbedding(docID, meanPool(usable), model, parsed.ContentHash); err != nil {
		return 0, fmt.Errorf("store doc embedding: %w", err)
	}
	return len(cvs), nil
}

// meanPool returns the element-wise mean of the chunk vectors. Cosine is
// scale-invariant, so no renormalization is needed. The caller guarantees a
// non-empty, equal-length set.
func meanPool(vecs [][]float32) []float32 {
	dim := len(vecs[0])
	out := make([]float32, dim)
	for _, v := range vecs {
		for j := 0; j < dim && j < len(v); j++ {
			out[j] += v[j]
		}
	}
	inv := float32(1) / float32(len(vecs))
	for j := range out {
		out[j] *= inv
	}
	return out
}
