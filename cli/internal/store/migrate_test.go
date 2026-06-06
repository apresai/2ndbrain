package store

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// TestMigrate_V2toV3 synthesizes a real schema-v2 database, then opens it through
// store.Open (which runs the connection-pinned applyMigration) and asserts the
// v3 artifacts appear and pre-existing data survives.
func TestMigrate_V2toV3(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "index.db")

	// Build a v2 database directly: v1 schema + v2 ALTERs + version=2 + a row.
	raw, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(schemaV1); err != nil {
		t.Fatalf("schemaV1: %v", err)
	}
	for _, stmt := range schemaV2Statements {
		if _, err := raw.Exec(stmt); err != nil {
			t.Fatalf("schemaV2 %q: %v", stmt, err)
		}
	}
	if _, err := raw.Exec("UPDATE schema_version SET version = 2"); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec("INSERT INTO documents (id, path) VALUES ('d1', 'a.md')"); err != nil {
		t.Fatal(err)
	}
	raw.Close()

	// Open through the real path: migrate() should bump v2 -> v3.
	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	assertVersion(t, db, 3)
	assertTableExists(t, db, "aliases")
	assertColumnExists(t, db, "chunks", "block_id")
	assertColumnExists(t, db, "links", "block_id")

	// Pre-existing data must survive the migration unchanged.
	var path string
	if err := db.conn.QueryRow("SELECT path FROM documents WHERE id = 'd1'").Scan(&path); err != nil {
		t.Fatalf("query preserved doc: %v", err)
	}
	if path != "a.md" {
		t.Errorf("document not preserved: path = %q", path)
	}

	// Re-opening an already-v3 DB is a no-op.
	db.Close()
	db2, err := Open(dbPath)
	if err != nil {
		t.Fatalf("re-open: %v", err)
	}
	defer db2.Close()
	assertVersion(t, db2, 3)
}

func assertVersion(t *testing.T, db *DB, want int) {
	t.Helper()
	var got int
	if err := db.conn.QueryRow("SELECT MAX(version) FROM schema_version").Scan(&got); err != nil {
		t.Fatalf("read version: %v", err)
	}
	if got != want {
		t.Errorf("schema_version = %d, want %d", got, want)
	}
}

func assertTableExists(t *testing.T, db *DB, name string) {
	t.Helper()
	var n int
	if err := db.conn.QueryRow(
		"SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?", name,
	).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("table %q does not exist after migration", name)
	}
}

func assertColumnExists(t *testing.T, db *DB, table, column string) {
	t.Helper()
	var n int
	if err := db.conn.QueryRow(
		"SELECT COUNT(*) FROM pragma_table_info(?) WHERE name = ?", table, column,
	).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("column %s.%s missing after migration", table, column)
	}
}
