package search

import (
	"strings"
	"testing"

	"github.com/apresai/2ndbrain/internal/store"
)

// A vector-only hit used to come back with an empty Content: RRF builds such a
// row from GetDocumentByID, which selects metadata and no body. So the results
// semantic search exists to find were the ones with nothing to read, in
// `search --json`, in the CLI's snippet line, in kb_search, and in the app.
//
// This is the BRUTE-FORCE leg (whole-document embeddings, no chunk id), which
// additionally has to learn which chunk it is talking about.
func TestHybridSearch_VectorOnlyHitCarriesItsChunkText_BruteForce(t *testing.T) {
	db := setupSearchDB(t)
	// The BM25 query word appears in neither document, so every row in the
	// result set arrived through the vector leg alone.
	insertTestDoc(t, db, "d-sem", "sem.md", "Semantic Only", "note", "draft",
		"# Orbital Mechanics\n\n"+strings.Repeat("Delta-v budgets and transfer windows. ", 20)+"\n")

	if err := db.SetEmbedding("d-sem", []float32{1, 0}, "t", "h"); err != nil {
		t.Fatalf("SetEmbedding: %v", err)
	}
	ids, vecs, err := db.AllEmbeddings()
	if err != nil {
		t.Fatalf("AllEmbeddings: %v", err)
	}
	engine := NewEngine(db.Conn())

	res, mode, err := engine.HybridSearch(Options{Query: "zzzznomatch", Limit: 10}, []float32{1, 0}, ids, vecs)
	if err != nil {
		t.Fatalf("HybridSearch: %v", err)
	}
	if mode != ModeHybrid {
		t.Fatalf("mode = %q, want hybrid (the vector leg must have run)", mode)
	}
	if len(res) != 1 {
		t.Fatalf("want the one semantic hit, got %d", len(res))
	}
	got := res[0]
	if got.Content == "" {
		t.Fatal("a vector-only hit came back with no content")
	}
	if got.ChunkID == "" {
		t.Error("a vector-only hit came back with no chunk id, so nothing can cite or rerank it")
	}
	if got.HeadingPath == "" {
		t.Error("a vector-only hit came back with no heading path")
	}
	want := firstChunkPrefix(t, db, "d-sem", 200)
	if got.Content != want {
		t.Errorf("content = %q,\nwant the first 200 characters of the matched chunk %q", got.Content, want)
	}
}

// The PRIMARY path is the per-chunk vec0 KNN, a different query that returns a
// chunk id, so it hydrates from that chunk rather than the document's first.
func TestHybridSearch_VectorOnlyHitCarriesItsChunkText_Vec0(t *testing.T) {
	db := setupSearchDB(t)
	insertTestDoc(t, db, "d-sem", "sem.md", "Semantic Only", "note", "draft",
		"# Orbital Mechanics\n\n"+strings.Repeat("Delta-v budgets and transfer windows. ", 20)+"\n")

	const dim = 2
	// Fatal, not Skip: sqlite-vec is compiled into the pure-Go build
	// unconditionally, so an unavailable vec_chunks is a defect, not a
	// capability gap.
	if err := db.EnsureVecChunks(dim); err != nil {
		t.Fatalf("EnsureVecChunks: %v", err)
	}
	chunkID := firstChunkID(t, db, "d-sem")
	if err := db.SetDocChunkVectors("d-sem", []store.ChunkVector{{
		ChunkID: chunkID, DocID: "d-sem", ContentHash: "h", Vector: []float32{1, 0},
	}}, "t"); err != nil {
		t.Fatalf("SetDocChunkVectors: %v", err)
	}
	if err := db.SetEmbedding("d-sem", []float32{1, 0}, "t", "h"); err != nil {
		t.Fatalf("SetEmbedding: %v", err)
	}
	ids, vecs, err := db.AllEmbeddings()
	if err != nil {
		t.Fatalf("AllEmbeddings: %v", err)
	}
	engine := NewEngine(db.Conn())

	// Confirm the vec0 branch is the one under test; otherwise this duplicates
	// the brute-force test and proves nothing new.
	if _, ok, verr := engine.vecChunkSearchByDoc([]float32{1, 0}, 40, 0, len(vecs)); verr != nil || !ok {
		t.Fatalf("vec0 path not taken (ok=%v err=%v); this test would otherwise duplicate the brute-force one", ok, verr)
	}

	res, mode, err := engine.HybridSearch(Options{Query: "zzzznomatch", Limit: 10}, []float32{1, 0}, ids, vecs)
	if err != nil {
		t.Fatalf("HybridSearch: %v", err)
	}
	if mode != ModeHybrid {
		t.Fatalf("mode = %q, want hybrid", mode)
	}
	if len(res) != 1 {
		t.Fatalf("want the one semantic hit, got %d", len(res))
	}
	if res[0].ChunkID != chunkID {
		t.Errorf("chunk id = %q, want the matched chunk %q", res[0].ChunkID, chunkID)
	}
	want := firstChunkPrefix(t, db, "d-sem", 200)
	if res[0].Content != want {
		t.Errorf("content = %q,\nwant the first 200 characters of the matched chunk %q", res[0].Content, want)
	}
}

// A note with no body has no chunks at all, so the join is an OUTER one: an
// empty snippet is the honest answer, and it must not be an error.
func TestHybridSearch_VectorOnlyHitOnABodylessNote(t *testing.T) {
	db := setupSearchDB(t)
	insertTestDoc(t, db, "d-empty", "empty.md", "Empty", "note", "draft", "")

	if err := db.SetEmbedding("d-empty", []float32{1, 0}, "t", "h"); err != nil {
		t.Fatalf("SetEmbedding: %v", err)
	}
	ids, vecs, err := db.AllEmbeddings()
	if err != nil {
		t.Fatalf("AllEmbeddings: %v", err)
	}
	engine := NewEngine(db.Conn())

	res, _, err := engine.HybridSearch(Options{Query: "zzzznomatch", Limit: 10}, []float32{1, 0}, ids, vecs)
	if err != nil {
		t.Fatalf("HybridSearch on a body-less note: %v", err)
	}
	if len(res) != 1 {
		t.Fatalf("want the one hit, got %d", len(res))
	}
	if res[0].Content != "" {
		t.Errorf("content = %q, want empty: the note has no body to snippet", res[0].Content)
	}
	if res[0].Path != "empty.md" {
		t.Errorf("path = %q, want empty.md", res[0].Path)
	}
}

// firstChunkID returns the id of the chunk hydration would pick for a document.
func firstChunkID(t *testing.T, db *store.DB, docID string) string {
	t.Helper()
	var id string
	err := db.Conn().QueryRow(`
		SELECT id FROM chunks WHERE doc_id = ?
		ORDER BY sort_order, start_line, id LIMIT 1`, docID).Scan(&id)
	if err != nil {
		t.Fatalf("first chunk of %s: %v", docID, err)
	}
	return id
}

// firstChunkPrefix returns exactly what SQL substr would cut, so the assertion
// pins the snippet's content rather than restating the implementation in Go
// (where a byte slice would also cut a multi-byte rune in half).
func firstChunkPrefix(t *testing.T, db *store.DB, docID string, n int) string {
	t.Helper()
	var content string
	err := db.Conn().QueryRow(`
		SELECT substr(content, 1, ?) FROM chunks WHERE doc_id = ?
		ORDER BY sort_order, start_line, id LIMIT 1`, n, docID).Scan(&content)
	if err != nil {
		t.Fatalf("first chunk text of %s: %v", docID, err)
	}
	if content == "" {
		t.Fatalf("fixture produced no chunk text for %s", docID)
	}
	return content
}
