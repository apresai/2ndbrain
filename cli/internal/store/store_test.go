package store

import (
	"path/filepath"
	"testing"

	"github.com/apresai/2ndbrain/internal/document"
)

func openTestDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestOpen_CreatesSchema(t *testing.T) {
	db := openTestDB(t)
	// Verify tables exist
	tables := []string{"documents", "chunks", "links", "tags", "schema_version"}
	for _, table := range tables {
		var count int
		err := db.conn.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&count)
		if err != nil {
			t.Fatalf("query %s: %v", table, err)
		}
		if count != 1 {
			t.Errorf("table %s not found", table)
		}
	}
}

func TestUpsertDocument_Insert(t *testing.T) {
	db := openTestDB(t)
	doc := &document.Document{
		ID: "test-123", Path: "test.md", Title: "Test Doc",
		Type: "note", Status: "draft", CreatedAt: "2025-01-01T00:00:00Z",
		ModifiedAt: "2025-01-01T00:00:00Z", Frontmatter: map[string]any{"title": "Test Doc"},
	}
	if err := db.UpsertDocument(doc); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	got, err := db.GetDocumentByPath("test.md")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ID != "test-123" {
		t.Errorf("ID = %q, want test-123", got.ID)
	}
	if got.Title != "Test Doc" {
		t.Errorf("Title = %q, want Test Doc", got.Title)
	}
}

func TestUpsertDocument_Update(t *testing.T) {
	db := openTestDB(t)
	doc := &document.Document{
		ID: "test-123", Path: "test.md", Title: "Original",
		Type: "note", Status: "draft", CreatedAt: "2025-01-01T00:00:00Z",
		ModifiedAt: "2025-01-01T00:00:00Z", Frontmatter: map[string]any{},
	}
	db.UpsertDocument(doc)

	doc.Title = "Updated"
	db.UpsertDocument(doc)

	got, _ := db.GetDocumentByPath("test.md")
	if got.Title != "Updated" {
		t.Errorf("Title = %q, want Updated", got.Title)
	}
}

func TestDeleteDocument_Cascade(t *testing.T) {
	db := openTestDB(t)
	doc := &document.Document{
		ID: "del-123", Path: "del.md", Title: "Delete Me",
		Type: "note", Status: "draft", CreatedAt: "2025-01-01T00:00:00Z",
		ModifiedAt: "2025-01-01T00:00:00Z", Frontmatter: map[string]any{},
	}
	db.UpsertDocument(doc)
	db.UpsertChunks([]document.Chunk{{ID: "c1", DocID: "del-123", HeadingPath: "# H", Content: "text", ContentHash: "h"}})
	db.UpsertTags("del-123", []string{"tag1"})
	db.UpsertLinks("del-123", []document.WikiLink{{Target: "other"}})

	if err := db.DeleteDocument("del-123"); err != nil {
		t.Fatalf("delete: %v", err)
	}

	// Verify cascades
	var count int
	db.conn.QueryRow("SELECT COUNT(*) FROM chunks WHERE doc_id = 'del-123'").Scan(&count)
	if count != 0 {
		t.Errorf("chunks count = %d, want 0", count)
	}
	db.conn.QueryRow("SELECT COUNT(*) FROM tags WHERE doc_id = 'del-123'").Scan(&count)
	if count != 0 {
		t.Errorf("tags count = %d, want 0", count)
	}
	db.conn.QueryRow("SELECT COUNT(*) FROM links WHERE source_id = 'del-123'").Scan(&count)
	if count != 0 {
		t.Errorf("links count = %d, want 0", count)
	}
}

func TestUpsertTags_ReplacesPrevious(t *testing.T) {
	db := openTestDB(t)
	doc := &document.Document{
		ID: "tag-123", Path: "tag.md", Title: "Tags", Type: "note", Status: "draft",
		CreatedAt: "2025-01-01T00:00:00Z", ModifiedAt: "2025-01-01T00:00:00Z",
		Frontmatter: map[string]any{},
	}
	db.UpsertDocument(doc)
	db.UpsertTags("tag-123", []string{"old1", "old2"})

	// Replace with new tags
	db.UpsertTags("tag-123", []string{"new1"})

	var count int
	db.conn.QueryRow("SELECT COUNT(*) FROM tags WHERE doc_id = 'tag-123'").Scan(&count)
	if count != 1 {
		t.Errorf("tags count = %d, want 1", count)
	}
}

func TestResolveLinks(t *testing.T) {
	db := openTestDB(t)
	// Create two documents
	doc1 := &document.Document{
		ID: "d1", Path: "doc-one.md", Title: "Doc One", Type: "note", Status: "draft",
		CreatedAt: "2025-01-01T00:00:00Z", ModifiedAt: "2025-01-01T00:00:00Z",
		Frontmatter: map[string]any{},
	}
	doc2 := &document.Document{
		ID: "d2", Path: "doc-two.md", Title: "Doc Two", Type: "note", Status: "draft",
		CreatedAt: "2025-01-01T00:00:00Z", ModifiedAt: "2025-01-01T00:00:00Z",
		Frontmatter: map[string]any{},
	}
	db.UpsertDocument(doc1)
	db.UpsertDocument(doc2)

	// Create a link from doc1 -> "doc-two" (without .md)
	db.UpsertLinks("d1", []document.WikiLink{{Target: "doc-two"}})

	// Resolve
	if err := db.ResolveLinks(); err != nil {
		t.Fatalf("resolve: %v", err)
	}

	// Verify
	var targetID string
	var resolved int
	db.conn.QueryRow("SELECT target_id, resolved FROM links WHERE source_id = 'd1'").Scan(&targetID, &resolved)
	if targetID != "d2" {
		t.Errorf("target_id = %q, want d2", targetID)
	}
	if resolved != 1 {
		t.Errorf("resolved = %d, want 1", resolved)
	}
}
