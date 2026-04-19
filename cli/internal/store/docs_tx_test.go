package store

import (
	"testing"

	"github.com/apresai/2ndbrain/internal/document"
)

// makeDoc constructs a minimal valid Document for tests that don't care about
// heading structure or bodies beyond ID/title/type.
func makeDoc(id, title, body string) *document.Document {
	return &document.Document{
		ID:          id,
		Path:        id + ".md",
		Title:       title,
		Type:        "note",
		Status:      "draft",
		Tags:        []string{},
		Body:        body,
		CreatedAt:   "2026-04-19T00:00:00Z",
		ModifiedAt:  "2026-04-19T00:00:00Z",
		ContentHash: "h-" + id,
		Frontmatter: map[string]any{"id": id, "title": title, "type": "note"},
	}
}

func TestUpsertDocumentTx_CommitWrites(t *testing.T) {
	db := openTestDB(t)
	tx, err := db.Conn().Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}

	doc := makeDoc("doc-1", "Doc One", "body")
	if err := db.UpsertDocumentTx(tx, doc); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	var count int
	if err := db.Conn().QueryRow("SELECT COUNT(*) FROM documents WHERE id = ?", "doc-1").Scan(&count); err != nil {
		t.Fatalf("query: %v", err)
	}
	if count != 1 {
		t.Errorf("documents count = %d, want 1", count)
	}
}

func TestUpsertDocumentTx_RollbackReverts(t *testing.T) {
	db := openTestDB(t)
	tx, err := db.Conn().Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}

	doc := makeDoc("doc-rollback", "Phantom", "body")
	if err := db.UpsertDocumentTx(tx, doc); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatalf("rollback: %v", err)
	}

	var count int
	if err := db.Conn().QueryRow("SELECT COUNT(*) FROM documents WHERE id = ?", "doc-rollback").Scan(&count); err != nil {
		t.Fatalf("query: %v", err)
	}
	if count != 0 {
		t.Errorf("documents count = %d, want 0 (rollback should revert)", count)
	}
}

func TestUpsertChunksTx_CommitAndRollback(t *testing.T) {
	db := openTestDB(t)
	seedParentDoc(t, db, "doc-c", "Chunks Parent")

	chunks := []document.Chunk{
		{ID: "c1", DocID: "doc-c", HeadingPath: "", Level: 0, Content: "first", ContentHash: "h1", StartLine: 1, EndLine: 2, SortOrder: 0},
		{ID: "c2", DocID: "doc-c", HeadingPath: "## Two", Level: 2, Content: "second", ContentHash: "h2", StartLine: 3, EndLine: 4, SortOrder: 1},
	}

	// Commit path
	tx, err := db.Conn().Begin()
	if err != nil {
		t.Fatalf("begin commit-tx: %v", err)
	}
	if err := db.UpsertChunksTx(tx, chunks); err != nil {
		t.Fatalf("upsert chunks: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	count := countChunksForDoc(t, db, "doc-c")
	if count != 2 {
		t.Errorf("chunks count after commit = %d, want 2", count)
	}

	// Rollback path: try to add a 3rd chunk, then roll back — chunks table
	// should remain at 2.
	tx, err = db.Conn().Begin()
	if err != nil {
		t.Fatalf("begin rollback-tx: %v", err)
	}
	extra := []document.Chunk{{ID: "c3", DocID: "doc-c", Content: "third", ContentHash: "h3"}}
	if err := db.UpsertChunksTx(tx, extra); err != nil {
		t.Fatalf("upsert extra: %v", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	count = countChunksForDoc(t, db, "doc-c")
	if count != 2 {
		t.Errorf("chunks count after rollback = %d, want 2 (third chunk should not persist)", count)
	}
}

// countChunksForDoc / countTagsForDoc / countLinksForDoc make the SCAN
// error bubble up as t.Fatal instead of silently returning 0 — a broken
// query would otherwise cause rollback assertions to pass vacuously
// (0 == 0 when the query itself failed).
func countChunksForDoc(t *testing.T, db *DB, docID string) int {
	t.Helper()
	var count int
	if err := db.Conn().QueryRow("SELECT COUNT(*) FROM chunks WHERE doc_id = ?", docID).Scan(&count); err != nil {
		t.Fatalf("query chunks: %v", err)
	}
	return count
}

func countTagsForDoc(t *testing.T, db *DB, docID string) int {
	t.Helper()
	var count int
	if err := db.Conn().QueryRow("SELECT COUNT(*) FROM tags WHERE doc_id = ?", docID).Scan(&count); err != nil {
		t.Fatalf("query tags: %v", err)
	}
	return count
}

func countLinksForDoc(t *testing.T, db *DB, docID string) int {
	t.Helper()
	var count int
	if err := db.Conn().QueryRow("SELECT COUNT(*) FROM links WHERE source_id = ?", docID).Scan(&count); err != nil {
		t.Fatalf("query links: %v", err)
	}
	return count
}

// seedParentDoc commits a minimal parent document so FK-dependent tests
// (chunks/tags/links) can target a real row. Any failure is fatal — if we
// can't commit a fixture, downstream assertions are meaningless.
func seedParentDoc(t *testing.T, db *DB, id, title string) {
	t.Helper()
	tx, err := db.Conn().Begin()
	if err != nil {
		t.Fatalf("begin seed: %v", err)
	}
	if err := db.UpsertDocumentTx(tx, makeDoc(id, title, "")); err != nil {
		t.Fatalf("seed upsert: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("seed commit: %v", err)
	}
}

func TestUpsertTagsTx_CommitAndRollback(t *testing.T) {
	db := openTestDB(t)
	seedParentDoc(t, db, "doc-t", "Tags Parent")

	// Commit path
	tx, err := db.Conn().Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := db.UpsertTagsTx(tx, "doc-t", []string{"alpha", "beta"}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if got := countTagsForDoc(t, db, "doc-t"); got != 2 {
		t.Errorf("tags after commit = %d, want 2", got)
	}

	// Rollback path: replace with 5 tags but roll back — should stay at 2.
	tx, err = db.Conn().Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := db.UpsertTagsTx(tx, "doc-t", []string{"a", "b", "c", "d", "e"}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if got := countTagsForDoc(t, db, "doc-t"); got != 2 {
		t.Errorf("tags after rollback = %d, want 2 (original alpha+beta should remain)", got)
	}
}

func TestUpsertLinksTx_CommitAndRollback(t *testing.T) {
	db := openTestDB(t)
	seedParentDoc(t, db, "doc-l", "Links Parent")

	// Commit path
	tx, err := db.Conn().Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	links := []document.WikiLink{
		{Target: "target-a"},
		{Target: "target-b", Heading: "section"},
	}
	if err := db.UpsertLinksTx(tx, "doc-l", links); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if got := countLinksForDoc(t, db, "doc-l"); got != 2 {
		t.Errorf("links after commit = %d, want 2", got)
	}

	// Rollback path
	tx, err = db.Conn().Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	more := []document.WikiLink{{Target: "x"}, {Target: "y"}, {Target: "z"}}
	if err := db.UpsertLinksTx(tx, "doc-l", more); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if got := countLinksForDoc(t, db, "doc-l"); got != 2 {
		t.Errorf("links after rollback = %d, want 2", got)
	}
}
