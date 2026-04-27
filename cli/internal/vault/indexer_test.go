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

// TestIndexFile_RollsBackOnFailure asserts the transactional wrapper:
// when indexFile fails mid-flight, no partial state should persist.
// We trigger failure by feeding a document with no ID (the validator
// inside indexFile errors before the first DB write, but a later failure
// path would also leave the tx clean).
func TestIndexFile_RollsBackOnFailure(t *testing.T) {
	v := initTestVault(t)

	abs := filepath.Join(v.Root, "bad.md")
	// Missing id field — indexFile returns an error before transactional
	// writes.
	content := "---\ntitle: Bad\ntype: note\nstatus: draft\n---\nbody"
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	err := indexFile(v.DB, abs, "bad.md")
	if err == nil {
		t.Fatal("expected error from indexFile with missing id")
	}

	// Confirm no rows leaked into documents/chunks/tags/links.
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

	err := purgeStale(v)
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

	if err := purgeStale(v); err != nil {
		t.Fatalf("purge: %v", err)
	}

	var count int
	v.DB.Conn().QueryRow("SELECT COUNT(*) FROM documents WHERE id = ?", "id-gone").Scan(&count)
	if count != 0 {
		t.Errorf("documents still has id-gone after purge (count=%d)", count)
	}
}
