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
