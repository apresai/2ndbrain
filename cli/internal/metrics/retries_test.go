package metrics

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// TestEmbedRetriesRoundTrip: the count has to survive the write, which means
// the column, the INSERT list and the SELECT list all have to agree. They are
// three hand-maintained lists, which is exactly the kind of thing that silently
// shifts a column by one.
func TestEmbedRetriesRoundTrip(t *testing.T) {
	db := openTestDB(t)
	mustRecord(t, db, Operation{Operation: OpReembed, DurationMs: 1000, Embedded: 4, EmbedRetries: 7, OK: true})

	last, err := db.LastByOp(OpIndex, OpReembed)
	if err != nil {
		t.Fatalf("lastByOp: %v", err)
	}
	if last == nil {
		t.Fatal("no build recorded")
	}
	if last.EmbedRetries != 7 {
		t.Errorf("embed_retries = %d, want 7", last.EmbedRetries)
	}
	if last.Embedded != 4 {
		t.Errorf("embedded = %d, want 4: a shifted column would corrupt its neighbors too", last.Embedded)
	}

	agg, err := db.Aggregates()
	if err != nil {
		t.Fatalf("aggregates: %v", err)
	}
	if got := agg[OpReembed].EmbedRetries; got != 7 {
		t.Errorf("aggregate embed_retries = %d, want 7", got)
	}
}

// TestMigrationAddsEmbedRetriesToAnExistingDB is the migration proof: an
// existing metrics.db carries the user's whole index history, so the column has
// to be ALTERed in, not created by dropping the table. The v2 DB here is built
// by the real v2 statements, so the test exercises the actual upgrade path.
func TestMigrationAddsEmbedRetriesToAnExistingDB(t *testing.T) {
	path := filepath.Join(t.TempDir(), "metrics.db")

	// Build a v2 database by hand: base schema, the v2 ALTERs, version 2.
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open raw: %v", err)
	}
	if _, err := raw.Exec(schema); err != nil {
		t.Fatalf("base schema: %v", err)
	}
	for _, stmt := range metricsSchemaV2 {
		if _, err := raw.Exec(stmt); err != nil {
			t.Fatalf("v2 migration: %v", err)
		}
	}
	if _, err := raw.Exec(`INSERT INTO schema_version (version) VALUES (2)`); err != nil {
		t.Fatalf("seed version: %v", err)
	}
	// One historical row, to prove the upgrade preserves it.
	if _, err := raw.Exec(
		`INSERT INTO operations (ts, operation, duration_ms, docs_indexed) VALUES ('2026-01-01T00:00:00Z', 'index', 1234, 42)`,
	); err != nil {
		t.Fatalf("seed row: %v", err)
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("close raw: %v", err)
	}

	db, err := Open(path)
	if err != nil {
		t.Fatalf("open (this is the migration): %v", err)
	}
	defer db.Close()

	var version int
	if err := db.conn.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_version`).Scan(&version); err != nil {
		t.Fatalf("read version: %v", err)
	}
	if version != maxMetricsSchemaVersion {
		t.Errorf("schema_version = %d, want %d", version, maxMetricsSchemaVersion)
	}

	ops, err := db.Recent(0)
	if err != nil {
		t.Fatalf("recent: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("history rows = %d, want the 1 pre-existing row preserved", len(ops))
	}
	if ops[0].DocsIndexed != 42 || ops[0].DurationMs != 1234 {
		t.Errorf("pre-existing row changed: %+v", ops[0])
	}
	if ops[0].EmbedRetries != 0 {
		t.Errorf("pre-existing embed_retries = %d, want the 0 default", ops[0].EmbedRetries)
	}

	// And the upgraded DB accepts a value in the new column.
	mustRecord(t, db, Operation{Operation: OpIndex, DurationMs: 10, EmbedRetries: 3, OK: true})
	last, _ := db.LastByOp(OpIndex)
	if last == nil || last.EmbedRetries != 3 {
		t.Errorf("after migration the new column does not round-trip: %+v", last)
	}
}
