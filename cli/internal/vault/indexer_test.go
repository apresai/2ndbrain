package vault

import (
	"os"
	"path/filepath"
	"testing"
)

// initTestVault mirrors testutil.NewTestVault but lives in-package so we can
// test unexported helpers (indexFile, purgeStale) without an import cycle.
func initTestVault(t *testing.T) *Vault {
	t.Helper()
	v, err := Init(t.TempDir())
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	t.Cleanup(func() { v.Close() })
	return v
}

// writeDoc writes a markdown file with valid frontmatter and returns its
// absolute path. IDs must be stable strings so the test can assert
// follow-up queries deterministically.
func writeDoc(t *testing.T, v *Vault, relPath, id, title, body string) string {
	t.Helper()
	abs := filepath.Join(v.Root, relPath)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nid: " + id + "\ntitle: " + title + "\ntype: note\nstatus: draft\n---\n" + body
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return abs
}

// TestIndexSingleFile_IndexesWikilinks is the regression test for the MCP
// kb_create fix: after the handler was rewritten to call IndexSingleFile,
// wikilinks in newly-created documents should appear in the links table
// (previously they were silently skipped).
func TestIndexSingleFile_IndexesWikilinks(t *testing.T) {
	v := initTestVault(t)

	// B has to exist before A's link can resolve to a target_id; index both.
	bPath := writeDoc(t, v, "b.md", "id-b", "Doc B", "Target doc.")
	aPath := writeDoc(t, v, "a.md", "id-a", "Doc A", "See [[Doc B]] for details.")

	if err := IndexSingleFile(v, bPath); err != nil {
		t.Fatalf("index B: %v", err)
	}
	if err := IndexSingleFile(v, aPath); err != nil {
		t.Fatalf("index A: %v", err)
	}

	var count int
	err := v.DB.Conn().QueryRow(
		"SELECT COUNT(*) FROM links WHERE source_id = ? AND target_id = ?",
		"id-a", "id-b",
	).Scan(&count)
	if err != nil {
		t.Fatalf("query links: %v", err)
	}
	if count != 1 {
		t.Errorf("resolved links A→B = %d, want 1 (wikilink wasn't indexed)", count)
	}
}

// TestIndexFile_RollsBackOnFailure asserts the transactional wrapper: when
// indexFile fails mid-flight, no partial state persists. The failure is forced
// AFTER the document upsert, by a trigger that aborts the chunk insert, so the
// documents row is the thing that must not survive. (This test used to get its
// failure from a path collision between two ids, which was itself the bug
// TestIndexSingleFile_RecreatedNoteWithNewIDReplacesTheOldRow covers.)
func TestIndexFile_RollsBackOnFailure(t *testing.T) {
	v := initTestVault(t)

	if _, err := v.DB.Conn().Exec(
		`CREATE TRIGGER fail_chunk_insert BEFORE INSERT ON chunks
		 BEGIN SELECT RAISE(ABORT, 'forced chunk failure'); END`,
	); err != nil {
		t.Fatal(err)
	}

	abs := writeDoc(t, v, "bad.md", "id-2", "Bad", "body with #tag and [[Link]]")
	if err := indexFile(v.DB, abs, "bad.md"); err == nil {
		t.Fatal("expected indexFile to fail on the aborted chunk insert")
	}

	// The upsert ran before the failure; the rollback must have undone it.
	for _, table := range []string{"documents", "chunks", "tags", "links"} {
		var count int
		if err := v.DB.Conn().QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if count != 0 {
			t.Errorf("%s had %d rows after failed index (should be 0)", table, count)
		}
	}
}

// TestPurgeStale_ReturnsErrorOnClosedDB verifies the new error-propagation
// signature — previously a SELECT failure went to stderr and the caller
// had no way to know coverage was incomplete.
func TestPurgeStale_ReturnsErrorOnClosedDB(t *testing.T) {
	v := initTestVault(t)
	if err := v.DB.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}

	_, err := purgeStale(v, nil)
	if err == nil {
		t.Fatal("purgeStale on closed DB should return error")
	}
}

func TestCountRows_ReturnsScanError(t *testing.T) {
	v := initTestVault(t)
	if err := v.DB.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}

	if _, err := countRows(v.DB, "chunks"); err == nil {
		t.Fatal("countRows on closed DB should return error")
	}
}

// TestPurgeStale_RemovesMissingFiles exercises the happy path: a doc
// indexed but whose file was later deleted from disk should be removed
// from the documents table on the next purge.
func TestPurgeStale_RemovesMissingFiles(t *testing.T) {
	v := initTestVault(t)

	abs := writeDoc(t, v, "ephemeral.md", "id-gone", "Ephemeral", "poof")
	if err := IndexSingleFile(v, abs); err != nil {
		t.Fatalf("index: %v", err)
	}
	if err := os.Remove(abs); err != nil {
		t.Fatalf("remove file: %v", err)
	}

	if _, err := purgeStale(v, nil); err != nil {
		t.Fatalf("purge: %v", err)
	}

	var count int
	v.DB.Conn().QueryRow("SELECT COUNT(*) FROM documents WHERE id = ?", "id-gone").Scan(&count)
	if count != 0 {
		t.Errorf("documents still has id-gone after purge (count=%d)", count)
	}
}

func TestIndex_CanvasAndBase(t *testing.T) {
	v := initTestVault(t)

	// Write a dummy markdown target so canvas link has something to resolve to
	writeDoc(t, v, "engineering/auth-model.md", "id-auth", "Auth Model", "Auth details")

	// Write canvas
	canvasJSON := `{
		"nodes": [
			{"id": "n1", "type": "text", "text": "Core auth logic"},
			{"id": "n2", "type": "file", "file": "engineering/auth-model.md"}
		],
		"edges": [
			{"id": "e1", "fromNode": "n1", "toNode": "n2"}
		]
	}`
	absCanvas := filepath.Join(v.Root, "board.canvas")
	if err := os.WriteFile(absCanvas, []byte(canvasJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	// Write base
	baseYAML := `
base:
  name: Prod Settings
  timeout: 500
`
	absBase := filepath.Join(v.Root, "config.base")
	if err := os.WriteFile(absBase, []byte(baseYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	stats, err := IndexVault(v, nil)
	if err != nil {
		t.Fatalf("IndexVault: %v", err)
	}

	if stats.DocsIndexed < 3 {
		t.Errorf("expected at least 3 docs indexed, got %d", stats.DocsIndexed)
	}

	// Verify canvas in DB
	var cType string
	err = v.DB.Conn().QueryRow("SELECT doc_type FROM documents WHERE path = ?", "board.canvas").Scan(&cType)
	if err != nil {
		t.Fatalf("query board.canvas: %v", err)
	}
	if cType != "canvas" {
		t.Errorf("expected doc_type = canvas, got %q", cType)
	}

	// Verify base in DB
	var bType string
	err = v.DB.Conn().QueryRow("SELECT doc_type FROM documents WHERE path = ?", "config.base").Scan(&bType)
	if err != nil {
		t.Fatalf("query config.base: %v", err)
	}
	if bType != "base" {
		t.Errorf("expected doc_type = base, got %q", bType)
	}

	// Verify chunks for canvas (text node)
	var canvasChunks int
	err = v.DB.Conn().QueryRow("SELECT COUNT(*) FROM chunks WHERE doc_id = (SELECT id FROM documents WHERE path = ?)", "board.canvas").Scan(&canvasChunks)
	if err != nil {
		t.Fatalf("query canvas chunks: %v", err)
	}
	if canvasChunks == 0 {
		t.Error("expected board.canvas chunks, got 0")
	}

	// Verify chunks for base (flattened properties)
	var baseChunks int
	err = v.DB.Conn().QueryRow("SELECT COUNT(*) FROM chunks WHERE doc_id = (SELECT id FROM documents WHERE path = ?)", "config.base").Scan(&baseChunks)
	if err != nil {
		t.Fatalf("query base chunks: %v", err)
	}
	if baseChunks == 0 {
		t.Error("expected config.base chunks, got 0")
	}
}

// TestIndexSingleFile_RecreatedNoteWithNewIDReplacesTheOldRow is the
// regression test for `UNIQUE constraint failed: documents.path`. A note that
// is deleted outside 2nb and recreated at the same path from a template gets
// a fresh frontmatter id while the old row still holds the path. The upsert
// conflicts on id alone, so every index of that note failed until a full
// index purged the stale row. Path is identity: the old row is replaced.
func TestIndexSingleFile_RecreatedNoteWithNewIDReplacesTheOldRow(t *testing.T) {
	v := initTestVault(t)

	abs := writeDoc(t, v, "daily.md", "id-old", "Daily", "First body with [[Other]].")
	if err := IndexSingleFile(v, abs); err != nil {
		t.Fatalf("index original: %v", err)
	}

	if err := os.Remove(abs); err != nil {
		t.Fatal(err)
	}
	writeDoc(t, v, "daily.md", "id-new", "Daily", "Second body.")
	if err := IndexSingleFile(v, abs); err != nil {
		t.Fatalf("index recreated note: %v", err)
	}

	var ids []string
	rows, err := v.DB.Conn().Query("SELECT id FROM documents WHERE path = ?", "daily.md")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if len(ids) != 1 || ids[0] != "id-new" {
		t.Fatalf("rows at daily.md = %v, want exactly [id-new]", ids)
	}

	// Nothing of the old row may outlive it.
	for _, table := range []string{"chunks", "links"} {
		col := "doc_id"
		if table == "links" {
			col = "source_id"
		}
		var n int
		if err := v.DB.Conn().QueryRow("SELECT COUNT(*) FROM "+table+" WHERE "+col+" = ?", "id-old").Scan(&n); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if n != 0 {
			t.Errorf("%d %s rows still belong to the replaced id", n, table)
		}
	}

	// Indexing it again is a plain update, not another replacement.
	if err := IndexSingleFile(v, abs); err != nil {
		t.Fatalf("reindex: %v", err)
	}
}
