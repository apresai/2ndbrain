package vault

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/apresai/2ndbrain/internal/store"
)

// A failed id lookup is a FAILURE, not "this note has never been indexed".
//
// indexFile reuses the surrogate id the note's path already carries, and mints
// a new one when there is no row. It used to treat EVERY lookup error as the
// second case, so any transient database error (SQLITE_BUSY is the reachable
// one: an app or plugin holding a read while the CLI reindexes one note) minted
// a fresh id for a note that already had a row. The upsert then failed on the
// documents.path UNIQUE constraint, which reports a constraint rather than the
// contention that caused it, and RetryBusy could not retry because a constraint
// failure is not a busy error.
//
// Forcing a real SQLITE_BUSY deterministically would mean either a fake driver
// (which this repo does not allow) or out-waiting the 5s busy_timeout four
// times. A CLOSED database gives the same branch a real driver error with no
// wait, and the assertion is the one that matters: the failure is reported
// against the LOOKUP, not swallowed into a new id.
func TestIndexFile_ReportsAFailedIDLookup(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "index.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	notePath := filepath.Join(dir, "note.md")
	if err := os.WriteFile(notePath, []byte("---\ntitle: Note\n---\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	err = indexFile(db, notePath, "note.md")
	if err == nil {
		t.Fatal("indexFile succeeded against a closed database")
	}
	if !strings.Contains(err.Error(), "look up the index row") {
		t.Errorf("error should name the id lookup that failed, got: %v", err)
	}
}
