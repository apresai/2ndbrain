package store

import (
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
