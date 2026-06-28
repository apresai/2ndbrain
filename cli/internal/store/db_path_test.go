package store

import (
	"path/filepath"
	"testing"
)

// TestOpen_SpecialCharPath guards the bare-path DSN. A vault folder containing
// URI metacharacters — notably '%' (percent), also spaces, '#', '&', apostrophe
// — must open. A file:-URI DSN percent-decodes the path and fails on these (e.g.
// a folder literally named "pct%20enc" becomes "pct enc"); the bare-path form
// keeps the path literal. Obsidian/iCloud vault folders routinely have spaces.
func TestOpen_SpecialCharPath(t *testing.T) {
	for _, name := range []string{"My Vault", "weird#hash", "pct%20enc", "Chad's & Co"} {
		t.Run(name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), name)
			db, err := Open(filepath.Join(dir, "index.db"))
			if err != nil {
				t.Fatalf("Open with path %q: %v", name, err)
			}
			defer db.Close()
			// Migrations ran and the connection is usable.
			var v int
			if err := db.conn.QueryRow("SELECT MAX(version) FROM schema_version").Scan(&v); err != nil {
				t.Fatalf("query after open (%q): %v", name, err)
			}
			if v < 1 {
				t.Errorf("schema_version = %d, want >= 1", v)
			}
		})
	}
}
