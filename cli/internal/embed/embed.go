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

	chunks := document.ChunkDocument(parsed)
	var contents []string
	var embedChunks []document.Chunk
	for _, ch := range chunks {
		if strings.TrimSpace(ch.Content) == "" {
			continue
		}
		embedChunks = append(embedChunks, ch)
		contents = append(contents, ch.Content)
	}
	if len(contents) == 0 {
		return 0, nil
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

	parsed.ComputeContentHash()
	if err := db.SetEmbedding(docID, meanPool(vecs), model, parsed.ContentHash); err != nil {
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
