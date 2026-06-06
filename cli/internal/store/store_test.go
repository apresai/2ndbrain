package store

import (
	"database/sql"
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

func TestSchemaV3Migration(t *testing.T) {
	db := openTestDB(t)

	// Verify the aliases table exists
	var count int
	err := db.conn.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='aliases'").Scan(&count)
	if err != nil {
		t.Fatalf("query aliases: %v", err)
	}
	if count != 1 {
		t.Errorf("aliases table not created by migration")
	}

	// Verify the columns in chunks and links
	var countChunksCol, countLinksCol int
	db.conn.QueryRow("SELECT COUNT(*) FROM pragma_table_info('chunks') WHERE name='block_id'").Scan(&countChunksCol)
	if countChunksCol != 1 {
		t.Errorf("chunks table is missing block_id column")
	}
	db.conn.QueryRow("SELECT COUNT(*) FROM pragma_table_info('links') WHERE name='block_id'").Scan(&countLinksCol)
	if countLinksCol != 1 {
		t.Errorf("links table is missing block_id column")
	}
}

func TestUpsertDocument_RenamePreservesEmbedding(t *testing.T) {
	db := openTestDB(t)

	// Insert doc with a surrogate ID
	doc := &document.Document{
		ID: "surrogate-1", Path: "original.md", Title: "Original",
		Type: "note", Status: "draft", CreatedAt: "2025-01-01T00:00:00Z",
		ModifiedAt: "2025-01-01T00:00:00Z", Frontmatter: map[string]any{},
	}
	if err := db.UpsertDocument(doc); err != nil {
		t.Fatalf("upsert doc: %v", err)
	}

	// Set embedding
	emb := []float32{0.1, 0.2, 0.3}
	if err := db.SetEmbedding("surrogate-1", emb, "test-model", "hash-123"); err != nil {
		t.Fatalf("set embedding: %v", err)
	}

	// Verify embedding is set
	gotEmb, err := db.GetEmbedding("surrogate-1")
	if err != nil {
		t.Fatalf("get embedding: %v", err)
	}
	if len(gotEmb) != 3 || gotEmb[0] != 0.1 {
		t.Errorf("expected embedding {0.1, 0.2, 0.3}, got %v", gotEmb)
	}

	// Rename: upsert with the same ID but different path
	doc.Path = "renamed.md"
	if err := db.UpsertDocument(doc); err != nil {
		t.Fatalf("rename upsert: %v", err)
	}

	// Verify the path is updated
	gotDoc, err := db.GetDocumentByPath("renamed.md")
	if err != nil {
		t.Fatalf("get renamed doc: %v", err)
	}
	if gotDoc.ID != "surrogate-1" {
		t.Errorf("expected ID 'surrogate-1', got %q", gotDoc.ID)
	}

	// Verify old path is gone (since path is UNIQUE)
	_, err = db.GetDocumentByPath("original.md")
	if err == nil {
		t.Errorf("expected original.md path to be gone, but it was found")
	}

	// Verify embedding is still present
	gotEmbAfter, err := db.GetEmbedding("surrogate-1")
	if err != nil {
		t.Fatalf("get embedding after rename: %v", err)
	}
	if len(gotEmbAfter) != 3 || gotEmbAfter[0] != 0.1 {
		t.Errorf("expected embedding to be preserved, got %v", gotEmbAfter)
	}
}

func TestResolveLinks_ObsidianNative(t *testing.T) {
	db := openTestDB(t)

	// Create test documents with paths, titles, and aliases
	doc1 := &document.Document{
		ID: "d1", Path: "engineering/architecture.md", Title: "System Architecture", Type: "note", Status: "draft",
		Frontmatter: map[string]any{"aliases": []any{"arch", "design-doc"}},
	}
	doc2 := &document.Document{
		ID: "d2", Path: "marketing/architecture.md", Title: "Marketing Architecture", Type: "note", Status: "draft",
		Frontmatter: map[string]any{},
	}
	doc3 := &document.Document{
		ID: "d3", Path: "main.md", Title: "Main Document", Type: "note", Status: "draft",
		Frontmatter: map[string]any{},
	}

	db.UpsertDocument(doc1)
	db.UpsertDocument(doc2)
	db.UpsertDocument(doc3)

	// Populating the aliases table inside store
	tx, err := db.conn.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertAliasesTx(tx, "d1", []string{"arch", "design-doc"}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	// Links to test:
	// - "arch" (alias lookup -> d1)
	// - "engineering/architecture" (shortest unique path -> d1)
	// - "marketing/architecture" (shortest unique path -> d2)
	// - "main" (basename lookup -> d3)
	// - "architecture" (ambiguous -> should not resolve)
	db.UpsertLinks("d3", []document.WikiLink{
		{Target: "arch"},
		{Target: "engineering/architecture"},
		{Target: "marketing/architecture"},
		{Target: "main"},
		{Target: "architecture"},
	})

	if err := db.ResolveLinks(); err != nil {
		t.Fatalf("resolve error: %v", err)
	}

	// Verify alias link resolves to d1
	var target1 sql.NullString
	db.conn.QueryRow("SELECT target_id FROM links WHERE source_id = 'd3' AND target_raw = 'arch'").Scan(&target1)
	if target1.String != "d1" {
		t.Errorf("expected 'arch' to resolve to d1, got %q", target1.String)
	}

	// Verify shortest unique path link resolves to d1
	var target2 sql.NullString
	db.conn.QueryRow("SELECT target_id FROM links WHERE source_id = 'd3' AND target_raw = 'engineering/architecture'").Scan(&target2)
	if target2.String != "d1" {
		t.Errorf("expected 'engineering/architecture' to resolve to d1, got %q", target2.String)
	}

	// Verify shortest unique path link resolves to d2
	var target3 sql.NullString
	db.conn.QueryRow("SELECT target_id FROM links WHERE source_id = 'd3' AND target_raw = 'marketing/architecture'").Scan(&target3)
	if target3.String != "d2" {
		t.Errorf("expected 'marketing/architecture' to resolve to d2, got %q", target3.String)
	}

	// Verify basename link resolves to d3
	var target4 sql.NullString
	db.conn.QueryRow("SELECT target_id FROM links WHERE source_id = 'd3' AND target_raw = 'main'").Scan(&target4)
	if target4.String != "d3" {
		t.Errorf("expected 'main' to resolve to d3, got %q", target4.String)
	}

	// Verify ambiguous link does not resolve
	var target5 sql.NullString
	db.conn.QueryRow("SELECT target_id FROM links WHERE source_id = 'd3' AND target_raw = 'architecture'").Scan(&target5)
	if target5.Valid {
		t.Errorf("expected ambiguous link 'architecture' to remain unresolved, got %q", target5.String)
	}
}
