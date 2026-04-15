package store

import (
	"database/sql"
	"encoding/binary"
	"fmt"
	"math"
)

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
		   OR embedding_model != ?
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

// EmbeddingCounts returns (total_docs, embedded_docs) in a single query.
// Used by `2nb ai status` to avoid two round trips when showing vault
// embedding state.
func (db *DB) EmbeddingCounts() (total, embedded int, err error) {
	err = db.conn.QueryRow(`
		SELECT COUNT(*),
		       COALESCE(SUM(CASE WHEN embedding IS NOT NULL THEN 1 ELSE 0 END), 0)
		FROM documents
	`).Scan(&total, &embedded)
	return total, embedded, err
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
