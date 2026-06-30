package metrics

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// TestMigrateV1ToV2PreservesRows is the headline guarantee: opening a v1
// metrics.db (operations table without token columns) migrates it to v2 by
// ALTER ADD COLUMN, so the user's prior sequential-vs-parallel reembed history
// survives — not dropped — with the new token columns defaulting to 0.
func TestMigrateV1ToV2PreservesRows(t *testing.T) {
	p := filepath.Join(t.TempDir(), "metrics.db")

	// Hand-build a v1-shaped DB: the operations table WITHOUT input/output_tokens,
	// schema_version at 1, and one recorded reembed row.
	raw, err := sql.Open("sqlite", p)
	if err != nil {
		t.Fatal(err)
	}
	_, err = raw.Exec(`
		CREATE TABLE operations (
		    id INTEGER PRIMARY KEY AUTOINCREMENT, ts TEXT NOT NULL, operation TEXT NOT NULL,
		    source TEXT NOT NULL DEFAULT 'cli', duration_ms INTEGER NOT NULL, ok INTEGER NOT NULL DEFAULT 1,
		    error TEXT NOT NULL DEFAULT '', files_scanned INTEGER NOT NULL DEFAULT 0, docs_indexed INTEGER NOT NULL DEFAULT 0,
		    chunks_created INTEGER NOT NULL DEFAULT 0, links_found INTEGER NOT NULL DEFAULT 0, embedded INTEGER NOT NULL DEFAULT 0,
		    embed_skipped INTEGER NOT NULL DEFAULT 0, embed_failed INTEGER NOT NULL DEFAULT 0, embed_ms INTEGER NOT NULL DEFAULT 0,
		    total_chars INTEGER NOT NULL DEFAULT 0, embedding_model TEXT NOT NULL DEFAULT '', embedding_dims INTEGER NOT NULL DEFAULT 0,
		    result_count INTEGER NOT NULL DEFAULT 0, mode TEXT NOT NULL DEFAULT '', cli_version TEXT NOT NULL DEFAULT ''
		);
		CREATE TABLE schema_version (version INTEGER PRIMARY KEY);
		INSERT INTO schema_version (version) VALUES (1);
		INSERT INTO operations (ts, operation, source, duration_ms, ok, docs_indexed)
		    VALUES ('2026-06-29T00:00:00Z', 'reembed', 'cli', 555000, 1, 153);
	`)
	if err != nil {
		t.Fatalf("seed v1: %v", err)
	}
	raw.Close()

	// Open through metrics → runs migrate (v1 → v2).
	db, err := Open(p)
	if err != nil {
		t.Fatalf("open/migrate: %v", err)
	}
	defer db.Close()

	ops, err := db.Recent(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 1 {
		t.Fatalf("rows after migrate = %d, want 1 (history preserved, not dropped)", len(ops))
	}
	if ops[0].Operation != "reembed" || ops[0].DocsIndexed != 153 || ops[0].DurationMs != 555000 {
		t.Errorf("migrated row corrupted: %+v", ops[0])
	}
	if ops[0].InputTokens != 0 || ops[0].OutputTokens != 0 {
		t.Errorf("token defaults = %d/%d, want 0/0", ops[0].InputTokens, ops[0].OutputTokens)
	}

	// New records round-trip the token columns.
	if err := db.Record(Operation{Operation: OpAsk, DurationMs: 2000, OK: true, InputTokens: 1200, OutputTokens: 340}); err != nil {
		t.Fatalf("record: %v", err)
	}
	last, err := db.LastByOp(OpAsk)
	if err != nil || last == nil || last.InputTokens != 1200 || last.OutputTokens != 340 {
		t.Errorf("token round-trip failed: %+v (err %v)", last, err)
	}

	// Idempotent: schema_version stays a single row across re-open (no accumulation).
	var n int
	if err := db.conn.QueryRow(`SELECT COUNT(*) FROM schema_version`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("schema_version rows = %d, want 1", n)
	}
}

// TestMigrateForwardVersionLeftUntouched locks the "DB too new" guard: a
// metrics.db stamped at a version ABOVE what this binary knows (written by a
// newer install) must open cleanly and be left as-is — reads use named columns
// so extra columns are harmless, and this best-effort telemetry must never block
// an older binary. The guard must NOT downgrade the version or error.
func TestMigrateForwardVersionLeftUntouched(t *testing.T) {
	p := filepath.Join(t.TempDir(), "metrics.db")

	// First materialize a real, current-schema DB...
	db, err := Open(p)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	mustRecord(t, db, Operation{Operation: OpSearch, DurationMs: 12, OK: true})
	db.Close()

	// ...then stamp it forward to a version this binary doesn't know.
	raw, err := sql.Open("sqlite", p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`UPDATE schema_version SET version = ?`, maxMetricsSchemaVersion+1); err != nil {
		t.Fatalf("stamp forward: %v", err)
	}
	raw.Close()

	// Re-open: migrate() must take the forward-compat early return, not error.
	db2, err := Open(p)
	if err != nil {
		t.Fatalf("open forward-version db: %v", err)
	}
	defer db2.Close()

	var version int
	if err := db2.conn.QueryRow(`SELECT MAX(version) FROM schema_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != maxMetricsSchemaVersion+1 {
		t.Errorf("version = %d, want %d (left untouched, not downgraded)", version, maxMetricsSchemaVersion+1)
	}
	// The prior row is still readable through the named-column scan.
	if ops, err := db2.Recent(0); err != nil || len(ops) != 1 {
		t.Errorf("recent = %d rows (err %v), want 1 (readable on a forward-version db)", len(ops), err)
	}
}
