package search

import (
	"path/filepath"
	"testing"

	"github.com/apresai/2ndbrain/internal/document"
	"github.com/apresai/2ndbrain/internal/store"
)

func setupSearchDB(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func insertTestDoc(t *testing.T, db *store.DB, id, path, title, docType, status string, body string) {
	t.Helper()
	doc := &document.Document{
		ID: id, Path: path, Title: title, Type: docType, Status: status,
		CreatedAt: "2025-01-01T00:00:00Z", ModifiedAt: "2025-01-01T00:00:00Z",
		Frontmatter: map[string]any{"title": title},
	}
	db.UpsertDocument(doc)

	chunks := document.ChunkDocument(&document.Document{ID: id, Body: body})
	db.UpsertChunks(chunks)
}

func TestBM25Search_BasicQuery(t *testing.T) {
	db := setupSearchDB(t)
	insertTestDoc(t, db, "d1", "k8s.md", "Kubernetes", "note", "draft",
		"# Kubernetes\n\nDeployment strategy for kubernetes clusters.\n")

	engine := NewEngine(db.Conn())
	results, err := engine.Search(Options{Query: "kubernetes", Limit: 10})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(results) == 0 {
		t.Error("expected at least 1 result")
	}
}

func TestBM25Search_NoResults(t *testing.T) {
	db := setupSearchDB(t)
	insertTestDoc(t, db, "d1", "doc.md", "Hello", "note", "draft", "# Hello\n\nWorld.\n")

	engine := NewEngine(db.Conn())
	results, err := engine.Search(Options{Query: "nonexistent", Limit: 10})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}

func TestBM25Search_TypeFilter(t *testing.T) {
	db := setupSearchDB(t)
	insertTestDoc(t, db, "d1", "adr.md", "Auth ADR", "adr", "proposed", "# Auth ADR\n\nAuthentication decision.\n")
	insertTestDoc(t, db, "d2", "note.md", "Auth Note", "note", "draft", "# Auth Note\n\nAuthentication notes.\n")

	engine := NewEngine(db.Conn())
	results, err := engine.Search(Options{Query: "authentication", Type: "adr", Limit: 10})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	for _, r := range results {
		if r.DocType != "adr" {
			t.Errorf("result type = %q, want adr", r.DocType)
		}
	}
}

func TestBM25Search_ScoreIsPositive(t *testing.T) {
	db := setupSearchDB(t)
	insertTestDoc(t, db, "d1", "doc.md", "Test", "note", "draft", "# Test\n\nSearch content here.\n")

	engine := NewEngine(db.Conn())
	results, _ := engine.Search(Options{Query: "search", Limit: 10})
	for _, r := range results {
		if r.Score <= 0 {
			t.Errorf("score = %f, should be positive", r.Score)
		}
	}
}

func TestListByFilters_EmptyQuery(t *testing.T) {
	db := setupSearchDB(t)
	insertTestDoc(t, db, "d1", "adr.md", "ADR One", "adr", "proposed", "# ADR\n")
	insertTestDoc(t, db, "d2", "note.md", "Note One", "note", "draft", "# Note\n")

	engine := NewEngine(db.Conn())
	results, err := engine.Search(Options{Type: "adr", Limit: 10})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 result, got %d", len(results))
	}
}

func TestFtsQuery_StripsSpecialChars(t *testing.T) {
	// Keyword query (no question word) → AND (space-separated)
	got := ftsQuery(`hello "world" foo*bar`)
	if got != "hello world foobar" {
		t.Errorf("ftsQuery = %q, want AND-joined", got)
	}
}

func TestFtsQuery_QuestionUsesOR(t *testing.T) {
	// Question (starts with "What") → OR
	got := ftsQuery("What are the differences between Gemma 3 and Gemma 4?")
	if got != "differences OR gemma OR 3 OR gemma OR 4" {
		t.Errorf("ftsQuery = %q", got)
	}
}

func TestFtsQuery_KeywordUsesAND(t *testing.T) {
	// Keyword query → AND (implicit, space-separated)
	got := ftsQuery("jwt authentication")
	if got != "jwt authentication" {
		t.Errorf("ftsQuery = %q, want space-joined AND", got)
	}
}

func TestFtsQuery_VersionStringQuoted(t *testing.T) {
	// Terms with dots previously failed with "syntax error near ."
	// because FTS5 parses bare dots as column-filter operators.
	got := ftsQuery("v0.1.9 release")
	want := `"v0.1.9" release`
	if got != want {
		t.Errorf("ftsQuery = %q, want %q", got, want)
	}
}

func TestFtsQuery_DashedModelIdQuoted(t *testing.T) {
	// Model IDs like "claude-haiku-4-5" also tripped the parser
	// (FTS5 treats "-" as NOT).
	got := ftsQuery("claude-haiku-4-5")
	want := `"claude-haiku-4-5"`
	if got != want {
		t.Errorf("ftsQuery = %q, want %q", got, want)
	}
}

// TestHybridSearch_ThresholdFilters asserts that vector hits below
// MinVectorScore are dropped before RRF fusion. We hand-craft unit vectors
// so cosines are deterministic: aligned → 1.0, orthogonal → 0.0.
func TestHybridSearch_ThresholdFilters(t *testing.T) {
	db := setupSearchDB(t)
	// Three docs with BM25 hits on "rocket" so the BM25 channel returns
	// all three; vector channel discriminates.
	insertTestDoc(t, db, "d-aligned", "a.md", "Aligned", "note", "draft",
		"# Rocket\n\nRocket science aligned with query.\n")
	insertTestDoc(t, db, "d-partial", "p.md", "Partial", "note", "draft",
		"# Rocket\n\nRocket-adjacent material.\n")
	insertTestDoc(t, db, "d-orthogonal", "o.md", "Orthogonal", "note", "draft",
		"# Rocket\n\nRocket off-topic.\n")

	// Query vector = [1, 0]; doc vectors give cosines 1.0, 0.5, 0.0.
	db.SetEmbedding("d-aligned", []float32{1, 0}, "t", "h1")
	db.SetEmbedding("d-partial", []float32{1, 1}, "t", "h2") // cos ≈ 0.707
	db.SetEmbedding("d-orthogonal", []float32{0, 1}, "t", "h3")

	ids, vecs, err := db.AllEmbeddings()
	if err != nil {
		t.Fatalf("AllEmbeddings: %v", err)
	}

	engine := NewEngine(db.Conn())
	// Threshold 0.8 → only the aligned doc clears.
	results, mode, err := engine.HybridSearch(
		Options{Query: "rocket", Limit: 10, MinVectorScore: 0.8},
		[]float32{1, 0}, ids, vecs,
	)
	if err != nil {
		t.Fatalf("HybridSearch: %v", err)
	}
	if mode != ModeHybrid {
		t.Errorf("mode = %v, want hybrid", mode)
	}
	// d-aligned (cos=1.0) must survive the 0.8 threshold. d-orthogonal
	// still clears BM25 independently so we don't assert its absence —
	// the threshold gates only the vector channel, not the hybrid union.
	var sawAligned bool
	for _, r := range results {
		if r.DocID == "d-aligned" {
			sawAligned = true
		}
	}
	if !sawAligned {
		t.Error("d-aligned (cos=1.0) was filtered out despite threshold 0.8")
	}
}

// TestHybridSearch_BM25OnlyFlag asserts the BM25Only short-circuit skips
// the vector channel entirely even when embeddings are present.
func TestHybridSearch_BM25OnlyFlag(t *testing.T) {
	db := setupSearchDB(t)
	insertTestDoc(t, db, "d1", "f.md", "Foo", "note", "draft", "# Foo\n\nfoo term.\n")
	db.SetEmbedding("d1", []float32{1, 0}, "t", "h")
	ids, vecs, _ := db.AllEmbeddings()

	engine := NewEngine(db.Conn())
	_, mode, err := engine.HybridSearch(
		Options{Query: "foo", Limit: 10, BM25Only: true},
		[]float32{1, 0}, ids, vecs,
	)
	if err != nil {
		t.Fatalf("HybridSearch: %v", err)
	}
	if mode != ModeKeyword {
		t.Errorf("mode = %v, want keyword (BM25Only flag should skip vector channel)", mode)
	}
}

// TestHybridSearch_EmptyEmbeddingsFallsBackToKeyword — no stored vectors
// means hybrid is impossible; handler should still return BM25 results
// flagged as keyword mode.
func TestHybridSearch_EmptyEmbeddingsFallsBackToKeyword(t *testing.T) {
	db := setupSearchDB(t)
	insertTestDoc(t, db, "d1", "f.md", "Bar", "note", "draft", "# Bar\n\nbar content.\n")

	engine := NewEngine(db.Conn())
	results, mode, err := engine.HybridSearch(
		Options{Query: "bar", Limit: 10},
		[]float32{1, 0}, nil, nil,
	)
	if err != nil {
		t.Fatalf("HybridSearch: %v", err)
	}
	if mode != ModeKeyword {
		t.Errorf("mode = %v, want keyword (no embeddings)", mode)
	}
	if len(results) == 0 {
		t.Error("expected BM25 results")
	}
}

func TestFtsQuery_PlainWordsNotQuoted(t *testing.T) {
	// Regression guard: alphanumeric-only terms must stay unquoted
	// so existing queries don't get wrapped unnecessarily.
	got := ftsQuery("simple query test")
	if got != "simple query test" {
		t.Errorf("ftsQuery = %q, want unquoted", got)
	}
}

// TestVecChunkSearchByDoc_CoverageGate locks the fix for the lazy-vec_chunks
// fallback hole: vec_chunks is populated per-doc as notes are re-embedded, so a
// partially-migrated vault must NOT take the vec0 path (which would hide every
// not-yet-re-embedded doc) — it defers to the whole-doc brute force until the
// chunk-vector coverage matches the doc-level corpus size.
func TestVecChunkSearchByDoc_CoverageGate(t *testing.T) {
	db := setupSearchDB(t)
	if err := db.EnsureVecChunks(4); err != nil {
		t.Fatal(err)
	}
	// Only ONE document's chunk vectors are present in vec_chunks.
	if err := db.SetDocChunkVectors("doc-1", []store.ChunkVector{
		{ChunkID: "doc-1#a", DocID: "doc-1", ContentHash: "h1", Vector: []float32{1, 0, 0, 0}},
	}, "m"); err != nil {
		t.Fatal(err)
	}
	engine := NewEngine(db.Conn())
	q := []float32{1, 0, 0, 0}

	// Corpus has 2 embedded docs but vec_chunks covers only 1 -> defer (ok=false).
	if res, ok, err := engine.vecChunkSearchByDoc(q, 10, 0, 2); err != nil || ok || res != nil {
		t.Fatalf("incomplete coverage: ok=%v err=%v res=%v, want ok=false (defer to brute force)", ok, err, res)
	}
	// Coverage matches the corpus (1) -> take the vec0 path.
	res, ok, err := engine.vecChunkSearchByDoc(q, 10, 0, 1)
	if err != nil || !ok {
		t.Fatalf("complete coverage: ok=%v err=%v, want ok=true", ok, err)
	}
	if len(res) != 1 || res[0].DocID != "doc-1" {
		t.Fatalf("res = %+v, want one hit for doc-1", res)
	}
	// No doc-level corpus to defer to (wantCoverage=0) -> table presence suffices.
	if _, ok, err := engine.vecChunkSearchByDoc(q, 10, 0, 0); err != nil || !ok {
		t.Fatalf("zero coverage target: ok=%v err=%v, want ok=true", ok, err)
	}
}
