package store

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// The named driver `sqlite3_2nb` must lower wal_autocheckpoint on every
// connection AND must not break the default "sqlite3" driver that bench/db.go
// and the legacy migrate path still open with.
func TestNamedDriver_AutocheckpointAndDefaultStillRegistered(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	var pages int
	if err := db.Conn().QueryRow("PRAGMA wal_autocheckpoint").Scan(&pages); err != nil {
		t.Fatalf("read wal_autocheckpoint: %v", err)
	}
	if pages != 256 {
		t.Errorf("wal_autocheckpoint = %d, want 256 (ConnectHook didn't run)", pages)
	}

	// The default driver must remain registered for raw opens elsewhere.
	raw, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("raw sqlite3 open: %v", err)
	}
	defer raw.Close()
	if err := raw.Ping(); err != nil {
		t.Errorf("default sqlite3 driver no longer usable: %v", err)
	}
}

func TestCheckpoint_TruncatesWAL(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	// Inflate the WAL with ~2MB of writes so there's something to checkpoint.
	if _, err := db.Conn().Exec("CREATE TABLE blobs (x BLOB)"); err != nil {
		t.Fatalf("create table: %v", err)
	}
	blob := make([]byte, 4096)
	for i := 0; i < 500; i++ {
		if _, err := db.Conn().Exec("INSERT INTO blobs (x) VALUES (?)", blob); err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}

	res, err := db.Checkpoint()
	if err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}
	if res.Busy {
		t.Errorf("checkpoint reported busy with no concurrent reader")
	}
	if res.WALBytesAfter > res.WALBytesBefore {
		t.Errorf("WAL grew across checkpoint: before=%d after=%d", res.WALBytesBefore, res.WALBytesAfter)
	}
	if res.DBBytes <= 0 {
		t.Errorf("DBBytes should be positive, got %d", res.DBBytes)
	}
	// TRUNCATE with no active reader should leave the -wal file at/near zero.
	if res.WALBytesAfter > 64*1024 {
		t.Errorf("WAL not truncated: after=%d bytes", res.WALBytesAfter)
	}
}

func TestCheckpoint_PathHelpers(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "idx.db")
	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()
	if db.Path() != dbPath {
		t.Errorf("Path() = %q, want %q", db.Path(), dbPath)
	}
	if db.WALPath() != dbPath+"-wal" {
		t.Errorf("WALPath() = %q, want %q", db.WALPath(), dbPath+"-wal")
	}
}
