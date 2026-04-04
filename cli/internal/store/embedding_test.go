package store

import (
	"math"
	"path/filepath"
	"testing"
)

func TestEmbeddingRoundTrip(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// Insert a test document
	_, err = db.Conn().Exec(`INSERT INTO documents (id, path, title) VALUES ('doc1', 'test.md', 'Test')`)
	if err != nil {
		t.Fatal(err)
	}

	// Set embedding
	embedding := []float32{0.1, 0.2, 0.3, -0.4, 0.5}
	if err := db.SetEmbedding("doc1", embedding, "test-model", "hash123"); err != nil {
		t.Fatalf("SetEmbedding: %v", err)
	}

	// Get embedding
	got, err := db.GetEmbedding("doc1")
	if err != nil {
		t.Fatalf("GetEmbedding: %v", err)
	}
	if len(got) != len(embedding) {
		t.Fatalf("got %d dims, want %d", len(got), len(embedding))
	}
	for i, v := range got {
		if math.Abs(float64(v-embedding[i])) > 1e-6 {
			t.Errorf("dim %d: got %v, want %v", i, v, embedding[i])
		}
	}
}

func TestEmbeddingCount(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// Insert docs, some with embeddings
	db.Conn().Exec(`INSERT INTO documents (id, path, title) VALUES ('a', 'a.md', 'A')`)
	db.Conn().Exec(`INSERT INTO documents (id, path, title) VALUES ('b', 'b.md', 'B')`)
	db.Conn().Exec(`INSERT INTO documents (id, path, title) VALUES ('c', 'c.md', 'C')`)

	db.SetEmbedding("a", []float32{1, 2, 3}, "model", "h1")
	db.SetEmbedding("b", []float32{4, 5, 6}, "model", "h2")

	count, err := db.EmbeddingCount()
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Errorf("EmbeddingCount = %d, want 2", count)
	}
}

func TestDocumentsNeedingEmbedding(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	db.Conn().Exec(`INSERT INTO documents (id, path, title, content_hash) VALUES ('a', 'a.md', 'A', 'hash1')`)
	db.Conn().Exec(`INSERT INTO documents (id, path, title, content_hash) VALUES ('b', 'b.md', 'B', 'hash2')`)

	// a has an embedding, b does not
	db.SetEmbedding("a", []float32{1, 2}, "model-v1", "hash1")

	docs, err := db.DocumentsNeedingEmbedding("model-v1")
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 1 {
		t.Fatalf("got %d docs needing embedding, want 1", len(docs))
	}
	if docs[0].ID != "b" {
		t.Errorf("expected doc 'b' needs embedding, got %q", docs[0].ID)
	}
}

func TestAllEmbeddings(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	db.Conn().Exec(`INSERT INTO documents (id, path, title) VALUES ('a', 'a.md', 'A')`)
	db.Conn().Exec(`INSERT INTO documents (id, path, title) VALUES ('b', 'b.md', 'B')`)
	db.SetEmbedding("a", []float32{1, 0}, "m", "h")
	db.SetEmbedding("b", []float32{0, 1}, "m", "h")

	ids, vecs, err := db.AllEmbeddings()
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 || len(vecs) != 2 {
		t.Errorf("got %d ids, %d vecs, want 2 each", len(ids), len(vecs))
	}
}

func TestFloat32ByteRoundTrip(t *testing.T) {
	original := []float32{0.0, 1.0, -1.0, 3.14, -2.718, 1e-10, 1e10}
	bytes := float32sToBytes(original)
	result := bytesToFloat32s(bytes)

	if len(result) != len(original) {
		t.Fatalf("length mismatch: %d vs %d", len(result), len(original))
	}
	for i, v := range result {
		if v != original[i] {
			t.Errorf("index %d: got %v, want %v", i, v, original[i])
		}
	}
}
