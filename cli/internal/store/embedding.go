package store

import (
	"database/sql"
	"encoding/binary"
	"fmt"
	"math"
)

// EmbeddingSnapshot captures the embedding-related columns for one document.
// It is used by callers that need to roll back a bulk re-embedding attempt
// after a provider or network failure.
type EmbeddingSnapshot struct {
	DocID string
	Blob  []byte
	Model string
	Hash  string
}

// SetEmbedding stores a document's embedding vector.
func (db *DB) SetEmbedding(docID string, embedding []float32, model string, contentHash string) error {
	blob := float32sToBytes(embedding)
	_, err := db.conn.Exec(
		`UPDATE documents SET embedding = ?, embedding_model = ?, embedding_hash = ? WHERE id = ?`,
		blob, model, contentHash, docID,
	)
	return err
}

// GetEmbedding retrieves a document's embedding vector.
func (db *DB) GetEmbedding(docID string) ([]float32, error) {
	var blob []byte
	err := db.conn.QueryRow(`SELECT embedding FROM documents WHERE id = ?`, docID).Scan(&blob)
	if err != nil {
		return nil, err
	}
	if blob == nil {
		return nil, nil
	}
	return bytesToFloat32s(blob), nil
}

// DocumentsNeedingEmbedding returns documents whose content hash differs from their embedding hash,
// or whose embedding model doesn't match the requested model.
func (db *DB) DocumentsNeedingEmbedding(model string) ([]struct {
	ID          string
	Path        string
	ContentHash string
}, error) {
	rows, err := db.conn.Query(`
		SELECT id, path, content_hash FROM documents
		WHERE content_hash != embedding_hash
		   OR COALESCE(embedding_model, '') != ?
		   OR embedding IS NULL
	`, model)
	if err != nil {
		return nil, fmt.Errorf("query documents needing embedding: %w", err)
	}
	defer rows.Close()

	var docs []struct {
		ID          string
		Path        string
		ContentHash string
	}
	for rows.Next() {
		var d struct {
			ID          string
			Path        string
			ContentHash string
		}
		if err := rows.Scan(&d.ID, &d.Path, &d.ContentHash); err != nil {
			return nil, err
		}
		docs = append(docs, d)
	}
	return docs, rows.Err()
}

// AllEmbeddings returns all document IDs and their embeddings for vector search.
func (db *DB) AllEmbeddings() ([]string, [][]float32, error) {
	rows, err := db.conn.Query(`SELECT id, embedding FROM documents WHERE embedding IS NOT NULL`)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	var ids []string
	var vecs [][]float32
	for rows.Next() {
		var id string
		var blob []byte
		if err := rows.Scan(&id, &blob); err != nil {
			return nil, nil, err
		}
		if blob == nil {
			continue
		}
		ids = append(ids, id)
		vecs = append(vecs, bytesToFloat32s(blob))
	}
	return ids, vecs, rows.Err()
}

// EmbeddingCount returns the number of documents with embeddings.
func (db *DB) EmbeddingCount() (int, error) {
	var count int
	err := db.conn.QueryRow(`SELECT COUNT(*) FROM documents WHERE embedding IS NOT NULL`).Scan(&count)
	return count, err
}

// SampleEmbeddingDim reads one non-null embedding blob and returns its
// dimensionality (length in bytes / 4 — embeddings are packed float32).
// Returns (0, nil) when no embeddings exist. Used by portability checks
// to detect dimension mismatch against a currently-configured provider
// without scanning the whole table or adding a schema column.
func (db *DB) SampleEmbeddingDim() (int, error) {
	var blob []byte
	err := db.conn.QueryRow(`SELECT embedding FROM documents WHERE embedding IS NOT NULL LIMIT 1`).Scan(&blob)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return len(blob) / 4, nil
}

// DistinctEmbeddingModels returns the set of non-empty model strings that
// produced current embeddings. Length > 1 indicates a mixed-provider vault
// which the user should normalize via `2nb index --force-reembed`.
func (db *DB) DistinctEmbeddingModels() ([]string, error) {
	rows, err := db.conn.Query(`
		SELECT DISTINCT embedding_model FROM documents
		WHERE embedding IS NOT NULL AND embedding_model != ''
		ORDER BY embedding_model
	`)
	if err != nil {
		return nil, fmt.Errorf("query distinct embedding models: %w", err)
	}
	defer rows.Close()

	var models []string
	for rows.Next() {
		var m string
		if err := rows.Scan(&m); err != nil {
			return nil, err
		}
		models = append(models, m)
	}
	return models, rows.Err()
}

// DistinctEmbeddingDims returns the distinct vector dimensions present in the
// vault, derived from each embedding blob's length (bytes / 4, packed float32)
// with no schema column. Length > 1 indicates a Matryoshka dimension change
// that was only partially re-embedded: such a vault mixes incomparable vector
// widths and needs `2nb index --force-reembed`. The single-sample
// SampleEmbeddingDim can match the active provider yet miss the off-dim docs,
// so this is the authoritative mixed-dimension check.
func (db *DB) DistinctEmbeddingDims() ([]int, error) {
	rows, err := db.conn.Query(`
		SELECT DISTINCT length(embedding)/4 AS dim FROM documents
		WHERE embedding IS NOT NULL
		ORDER BY dim
	`)
	if err != nil {
		return nil, fmt.Errorf("query distinct embedding dims: %w", err)
	}
	defer rows.Close()

	var dims []int
	for rows.Next() {
		var d int
		if err := rows.Scan(&d); err != nil {
			return nil, err
		}
		dims = append(dims, d)
	}
	return dims, rows.Err()
}

// EmbeddingCounts returns (total_docs, embedded_docs, embeddable_unembedded)
// in a single query. Used by `2nb ai status` to avoid round trips when
// showing vault embedding state.
//
// embeddableUnembedded counts documents that *should* have an embedding but
// don't: they have no embedding yet carry at least one chunk (i.e. real,
// non-empty content). Documents with no chunks are empty/whitespace-only
// notes that the embed pass deliberately skips (Amazon Nova-2 and similar
// reject zero-length input), so they are excluded here — otherwise a vault
// holding even one blank "Untitled.md" would report a perpetual, unfixable
// "stale" state. A chunk exists for a document iff its body is non-empty
// after comment stripping, which is exactly the same condition the embed
// pass uses to skip (see embedDocumentsWithProvider), so the two stay in
// lockstep.
func (db *DB) EmbeddingCounts() (total, embedded, embeddableUnembedded int, err error) {
	err = db.conn.QueryRow(`
		SELECT COUNT(*),
		       COALESCE(SUM(CASE WHEN embedding IS NOT NULL THEN 1 ELSE 0 END), 0),
		       COALESCE(SUM(CASE WHEN embedding IS NULL
		                          AND EXISTS (SELECT 1 FROM chunks c WHERE c.doc_id = documents.id)
		                         THEN 1 ELSE 0 END), 0)
		FROM documents
	`).Scan(&total, &embedded, &embeddableUnembedded)
	return total, embedded, embeddableUnembedded, err
}

// InvalidateAllEmbeddings clears the embedding_hash column on every row
// that has an embedding, which makes DocumentsNeedingEmbedding match
// them all on the next index pass. Used by `2nb index --force-reembed`
// when the user intentionally switches providers and wants to re-embed
// the entire vault now instead of waiting for per-document drift to
// trigger it. Returns the number of rows affected so the caller can
// report a useful progress line.
func (db *DB) InvalidateAllEmbeddings() (int64, error) {
	res, err := db.conn.Exec(`UPDATE documents SET embedding_hash = '' WHERE embedding IS NOT NULL`)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// SnapshotEmbeddings returns the current embedding columns for every document.
// Rows with NULL embeddings are included so a failed bulk re-embed can also
// clear any partial embedding written for a previously-unembedded document.
func (db *DB) SnapshotEmbeddings() ([]EmbeddingSnapshot, error) {
	rows, err := db.conn.Query(`
		SELECT id, embedding, COALESCE(embedding_model, ''), COALESCE(embedding_hash, '')
		FROM documents
	`)
	if err != nil {
		return nil, fmt.Errorf("snapshot embeddings: %w", err)
	}
	defer rows.Close()

	var snapshots []EmbeddingSnapshot
	for rows.Next() {
		var s EmbeddingSnapshot
		if err := rows.Scan(&s.DocID, &s.Blob, &s.Model, &s.Hash); err != nil {
			return nil, fmt.Errorf("scan embedding snapshot: %w", err)
		}
		if s.Blob != nil {
			s.Blob = append([]byte(nil), s.Blob...)
		}
		snapshots = append(snapshots, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate embedding snapshot: %w", err)
	}
	return snapshots, nil
}

// RestoreEmbeddings restores embedding columns captured by SnapshotEmbeddings.
func (db *DB) RestoreEmbeddings(snapshots []EmbeddingSnapshot) error {
	tx, err := db.conn.Begin()
	if err != nil {
		return fmt.Errorf("begin restore embeddings: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
		UPDATE documents
		SET embedding = ?, embedding_model = ?, embedding_hash = ?
		WHERE id = ?
	`)
	if err != nil {
		return fmt.Errorf("prepare restore embeddings: %w", err)
	}
	defer stmt.Close()

	for _, s := range snapshots {
		if _, err := stmt.Exec(s.Blob, s.Model, s.Hash, s.DocID); err != nil {
			return fmt.Errorf("restore embedding %s: %w", s.DocID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit restore embeddings: %w", err)
	}
	return nil
}

func float32sToBytes(fs []float32) []byte {
	buf := make([]byte, len(fs)*4)
	for i, f := range fs {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(f))
	}
	return buf
}

func bytesToFloat32s(buf []byte) []float32 {
	fs := make([]float32, len(buf)/4)
	for i := range fs {
		fs[i] = math.Float32frombits(binary.LittleEndian.Uint32(buf[i*4:]))
	}
	return fs
}
