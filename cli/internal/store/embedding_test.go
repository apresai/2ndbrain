package store

import (
	"math"
	"path/filepath"
	"strings"
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

func TestSampleEmbeddingDim(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// Empty vault → 0
	dim, err := db.SampleEmbeddingDim()
	if err != nil {
		t.Fatalf("SampleEmbeddingDim empty: %v", err)
	}
	if dim != 0 {
		t.Errorf("empty vault: got dim=%d, want 0", dim)
	}

	// Add a 768-dim embedding → 768
	db.Conn().Exec(`INSERT INTO documents (id, path, title) VALUES ('a', 'a.md', 'A')`)
	vec768 := make([]float32, 768)
	for i := range vec768 {
		vec768[i] = float32(i) * 0.001
	}
	if err := db.SetEmbedding("a", vec768, "nomic", "h1"); err != nil {
		t.Fatal(err)
	}
	dim, err = db.SampleEmbeddingDim()
	if err != nil {
		t.Fatalf("SampleEmbeddingDim 768: %v", err)
	}
	if dim != 768 {
		t.Errorf("768d blob: got dim=%d, want 768", dim)
	}

	// Add a 1024-dim embedding — sample still returns one consistent dim
	// (we only sample one row; the caller is responsible for detecting
	// mixed-dim vaults via DistinctEmbeddingModels).
	db.Conn().Exec(`INSERT INTO documents (id, path, title) VALUES ('b', 'b.md', 'B')`)
	vec1024 := make([]float32, 1024)
	if err := db.SetEmbedding("b", vec1024, "nova", "h2"); err != nil {
		t.Fatal(err)
	}
	dim, err = db.SampleEmbeddingDim()
	if err != nil {
		t.Fatalf("SampleEmbeddingDim mixed: %v", err)
	}
	if dim != 768 && dim != 1024 {
		t.Errorf("mixed vault: got dim=%d, want 768 or 1024", dim)
	}
}

func TestDistinctEmbeddingModels(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// No embeddings → empty slice
	models, err := db.DistinctEmbeddingModels()
	if err != nil {
		t.Fatalf("empty: %v", err)
	}
	if len(models) != 0 {
		t.Errorf("empty vault: got %d models, want 0", len(models))
	}

	// One model
	db.Conn().Exec(`INSERT INTO documents (id, path, title) VALUES ('a', 'a.md', 'A')`)
	db.Conn().Exec(`INSERT INTO documents (id, path, title) VALUES ('b', 'b.md', 'B')`)
	db.SetEmbedding("a", []float32{1, 2, 3}, "nomic", "h1")
	db.SetEmbedding("b", []float32{4, 5, 6}, "nomic", "h2")
	models, _ = db.DistinctEmbeddingModels()
	if len(models) != 1 || models[0] != "nomic" {
		t.Errorf("single model: got %v, want [nomic]", models)
	}

	// Two models
	db.Conn().Exec(`INSERT INTO documents (id, path, title) VALUES ('c', 'c.md', 'C')`)
	db.SetEmbedding("c", []float32{7, 8, 9}, "nova", "h3")
	models, _ = db.DistinctEmbeddingModels()
	if len(models) != 2 {
		t.Errorf("mixed models: got %d, want 2 (%v)", len(models), models)
	}

	// Empty string model doesn't count (filtered by SQL)
	db.Conn().Exec(`INSERT INTO documents (id, path, title) VALUES ('d', 'd.md', 'D')`)
	db.SetEmbedding("d", []float32{0, 0, 0}, "", "h4")
	models, _ = db.DistinctEmbeddingModels()
	if len(models) != 2 {
		t.Errorf("empty-model should be filtered: got %d, want 2", len(models))
	}
}

func TestEmbeddingCounts(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// Empty vault
	total, embedded, err := db.EmbeddingCounts()
	if err != nil {
		t.Fatalf("empty: %v", err)
	}
	if total != 0 || embedded != 0 {
		t.Errorf("empty: got (%d, %d), want (0, 0)", total, embedded)
	}

	// 3 docs, 2 embedded
	db.Conn().Exec(`INSERT INTO documents (id, path, title) VALUES ('a', 'a.md', 'A')`)
	db.Conn().Exec(`INSERT INTO documents (id, path, title) VALUES ('b', 'b.md', 'B')`)
	db.Conn().Exec(`INSERT INTO documents (id, path, title) VALUES ('c', 'c.md', 'C')`)
	db.SetEmbedding("a", []float32{1, 2}, "m", "h")
	db.SetEmbedding("b", []float32{3, 4}, "m", "h")

	total, embedded, err = db.EmbeddingCounts()
	if err != nil {
		t.Fatalf("seeded: %v", err)
	}
	if total != 3 || embedded != 2 {
		t.Errorf("seeded: got (%d, %d), want (3, 2)", total, embedded)
	}
}

func TestMigrateSchemaVersionCeiling(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "ceiling.db")
	db, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	// Set version to one above the ceiling and close so the next Open
	// re-runs migrate() with the tampered version.
	if _, err := db.Conn().Exec(`UPDATE schema_version SET version = ?`, MaxSchemaVersion+1); err != nil {
		t.Fatal(err)
	}
	db.Close()

	_, err = Open(dbPath)
	if err == nil {
		t.Fatal("expected error opening DB with future schema version, got nil")
	}
	// Error should mention the version and upgrade hint.
	msg := err.Error()
	if !strings.Contains(msg, "schema v") || !strings.Contains(msg, "supports up to") {
		t.Errorf("error message doesn't match expected ceiling error: %q", msg)
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
