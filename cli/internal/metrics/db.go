// Package metrics is the vault's local "observatory": a per-vault SQLite
// sidecar (.2ndbrain/metrics.db) recording the performance of vault operations
// — index/reindex/re-embed builds plus search and ask query latency — so the
// CLI and the macOS app can answer "how long did the last index take, and how
// many docs/sec?" entirely from local data.
//
// It deliberately mirrors internal/bench (same pure-Go modernc driver, same WAL
// DSN, same Open/Close shape) but is a separate store: bench.db is benchmark
// PROBE history (model latency you run deliberately); metrics.db is passive
// operation TELEMETRY. Recording is always best-effort at the call sites — a
// metrics write must never fail the operation it measures.
package metrics

import (
	"database/sql"
	"fmt"
	"math"
	"sort"
	"time"

	_ "modernc.org/sqlite" // pure-Go, CGO-free SQLite driver (registers "sqlite")
)

// Operation type names stored in the operations.operation column.
const (
	OpIndex    = "index"     // full-vault index build
	OpIndexDoc = "index_doc" // single-document (incremental) reindex
	OpReembed  = "reembed"   // full forced re-embed (--force-reembed)
	OpSearch   = "search"    // hybrid/BM25 search query
	OpAsk      = "ask"       // RAG ask query
)

// DefaultPerTypeCap bounds how many rows of EACH operation type are retained.
// Partitioning by type means a flood of `search` rows can never evict the
// (low-volume, high-value) `index` build history. ~200/type keeps the file tiny
// while preserving a long-enough window to chart trends.
const DefaultPerTypeCap = 200

const schema = `
CREATE TABLE IF NOT EXISTS operations (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    ts              TEXT    NOT NULL,                 -- RFC3339 UTC, op start
    operation       TEXT    NOT NULL,                 -- index|index_doc|reembed|search|ask
    source          TEXT    NOT NULL DEFAULT 'cli',   -- cli|mcp|app
    duration_ms     INTEGER NOT NULL,
    ok              INTEGER NOT NULL DEFAULT 1,
    error           TEXT    NOT NULL DEFAULT '',
    files_scanned   INTEGER NOT NULL DEFAULT 0,
    docs_indexed    INTEGER NOT NULL DEFAULT 0,
    chunks_created  INTEGER NOT NULL DEFAULT 0,
    links_found     INTEGER NOT NULL DEFAULT 0,
    embedded        INTEGER NOT NULL DEFAULT 0,
    embed_skipped   INTEGER NOT NULL DEFAULT 0,
    embed_failed    INTEGER NOT NULL DEFAULT 0,
    embed_ms        INTEGER NOT NULL DEFAULT 0,
    total_chars     INTEGER NOT NULL DEFAULT 0,
    embedding_model TEXT    NOT NULL DEFAULT '',
    embedding_dims  INTEGER NOT NULL DEFAULT 0,
    result_count    INTEGER NOT NULL DEFAULT 0,
    mode            TEXT    NOT NULL DEFAULT '',
    cli_version     TEXT    NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_operations_op_ts ON operations(operation, ts DESC);

CREATE TABLE IF NOT EXISTS schema_version (
    version INTEGER PRIMARY KEY
);

-- PRIMARY KEY makes this INSERT OR IGNORE genuinely idempotent: a second open
-- conflicts on the existing row and is ignored. Without the PK the row would be
-- re-inserted on every open, and Open runs on every index/search/ask.
INSERT OR IGNORE INTO schema_version (version) VALUES (1);
`

// Operation is one recorded vault operation. Index/embed fields are zero for
// query ops (search/ask) and the query fields are zero for index ops. The
// Per-second fields are DERIVED at read time by WithRates and are never stored.
type Operation struct {
	ID         int64  `json:"id,omitempty"`
	Timestamp  string `json:"ts"`
	Operation  string `json:"operation"`
	Source     string `json:"source"`
	DurationMs int64  `json:"duration_ms"`
	OK         bool   `json:"ok"`
	Error      string `json:"error,omitempty"`

	// index / embed
	FilesScanned   int    `json:"files_scanned,omitempty"`
	DocsIndexed    int    `json:"docs_indexed,omitempty"`
	ChunksCreated  int    `json:"chunks_created,omitempty"`
	LinksFound     int    `json:"links_found,omitempty"`
	Embedded       int    `json:"embedded,omitempty"`
	EmbedSkipped   int    `json:"embed_skipped,omitempty"`
	EmbedFailed    int    `json:"embed_failed,omitempty"`
	EmbedMs        int64  `json:"embed_ms,omitempty"`
	TotalChars     int    `json:"total_chars,omitempty"`
	EmbeddingModel string `json:"embedding_model,omitempty"`
	EmbeddingDims  int    `json:"embedding_dims,omitempty"`

	// query
	ResultCount int    `json:"result_count,omitempty"`
	Mode        string `json:"mode,omitempty"`

	CLIVersion string `json:"cli_version,omitempty"`

	// derived (computed by WithRates, not persisted)
	DocsPerSec       float64 `json:"docs_per_sec,omitempty"`
	EmbeddingsPerSec float64 `json:"embeddings_per_sec,omitempty"`
	CharsPerSec      float64 `json:"chars_per_sec,omitempty"`
}

// WithRates returns a copy of the operation with the derived per-second rates
// filled in. Guards against a zero duration (no divide-by-zero). Embedding
// throughput prefers the embed-phase duration but falls back to the total.
func (o Operation) WithRates() Operation {
	if o.DurationMs > 0 {
		secs := float64(o.DurationMs) / 1000.0
		if o.DocsIndexed > 0 {
			o.DocsPerSec = round2(float64(o.DocsIndexed) / secs)
		}
		if o.TotalChars > 0 {
			o.CharsPerSec = round2(float64(o.TotalChars) / secs)
		}
	}
	embedSecs := float64(o.EmbedMs) / 1000.0
	if embedSecs <= 0 {
		embedSecs = float64(o.DurationMs) / 1000.0
	}
	if embedSecs > 0 && o.Embedded > 0 {
		o.EmbeddingsPerSec = round2(float64(o.Embedded) / embedSecs)
	}
	return o
}

func round2(f float64) float64 { return math.Round(f*100) / 100 }

// Aggregate is the per-operation-type rollup returned by Aggregates.
type Aggregate struct {
	Count         int     `json:"count"`
	AvgMs         float64 `json:"avg_ms"`
	P50Ms         int64   `json:"p50_ms"`
	AvgDocsPerSec float64 `json:"avg_docs_per_sec,omitempty"`
}

// DB wraps a SQLite connection for the metrics store.
type DB struct {
	conn *sql.DB
}

// Open opens or creates metrics.db at the given path. The schema is created
// idempotently on every open (CREATE TABLE IF NOT EXISTS), so an existing file
// is upgraded transparently and a fresh vault gets the table on first record.
func Open(dbPath string) (*DB, error) {
	// Bare path (no file: prefix) so a path with URI metacharacters (e.g. '%')
	// stays literal; modernc still parses _pragma from the query. See store/db.go.
	//
	// busy_timeout is deliberately LOWER than the index DB's 5s: metrics are
	// best-effort telemetry written synchronously on the hot path (every
	// index/search/ask, incl. the app's per-save `index --doc` burst). Under
	// write-lock contention we'd rather wait briefly then drop the metric than
	// stall the operation we're measuring for up to 5 seconds.
	conn, err := sql.Open("sqlite", dbPath+"?_pragma=journal_mode(WAL)&_pragma=foreign_keys(on)&_pragma=busy_timeout(2000)")
	if err != nil {
		return nil, fmt.Errorf("open metrics db: %w", err)
	}
	if _, err := conn.Exec(schema); err != nil {
		conn.Close()
		return nil, fmt.Errorf("migrate metrics db: %w", err)
	}
	return &DB{conn: conn}, nil
}

// Close closes the database connection.
func (db *DB) Close() error {
	return db.conn.Close()
}

// Clear deletes all recorded operations and returns how many rows were removed.
func (db *DB) Clear() (int64, error) {
	res, err := db.conn.Exec(`DELETE FROM operations`)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// Record stores one operation and then prunes that operation type back to
// DefaultPerTypeCap rows. Timestamp/Source default to now/"cli" when empty.
func (db *DB) Record(op Operation) error {
	if op.Timestamp == "" {
		op.Timestamp = time.Now().UTC().Format(time.RFC3339)
	}
	if op.Source == "" {
		op.Source = "cli"
	}
	_, err := db.conn.Exec(
		`INSERT INTO operations (
			ts, operation, source, duration_ms, ok, error,
			files_scanned, docs_indexed, chunks_created, links_found,
			embedded, embed_skipped, embed_failed, embed_ms, total_chars,
			embedding_model, embedding_dims, result_count, mode, cli_version
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		op.Timestamp, op.Operation, op.Source, op.DurationMs, boolToInt(op.OK), op.Error,
		op.FilesScanned, op.DocsIndexed, op.ChunksCreated, op.LinksFound,
		op.Embedded, op.EmbedSkipped, op.EmbedFailed, op.EmbedMs, op.TotalChars,
		op.EmbeddingModel, op.EmbeddingDims, op.ResultCount, op.Mode, op.CLIVersion,
	)
	if err != nil {
		return err
	}
	return db.prune(op.Operation, DefaultPerTypeCap)
}

// prune keeps only the newest `keep` rows of the given operation type. Uses the
// monotonic rowid for "newest" so it is stable regardless of timestamp ties.
func (db *DB) prune(operation string, keep int) error {
	if keep <= 0 {
		return nil
	}
	_, err := db.conn.Exec(
		`DELETE FROM operations
		 WHERE operation = ?
		   AND id NOT IN (
		       SELECT id FROM operations WHERE operation = ? ORDER BY id DESC LIMIT ?
		   )`,
		operation, operation, keep,
	)
	return err
}

// Recent returns the most recent operations across all types, newest first.
// limit <= 0 returns all rows.
func (db *DB) Recent(limit int) ([]Operation, error) {
	q := `SELECT ` + opColumns + ` FROM operations ORDER BY id DESC`
	var rows *sql.Rows
	var err error
	if limit > 0 {
		rows, err = db.conn.Query(q+` LIMIT ?`, limit)
	} else {
		rows, err = db.conn.Query(q)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanOps(rows)
}

// LastByOp returns the single most recent operation whose type is one of the
// given names (e.g. LastByOp(OpIndex, OpReembed) for the headline "last build").
// Returns (nil, nil) when no matching row exists.
func (db *DB) LastByOp(operations ...string) (*Operation, error) {
	if len(operations) == 0 {
		return nil, nil
	}
	placeholders := make([]string, len(operations))
	args := make([]any, len(operations))
	for i, op := range operations {
		placeholders[i] = "?"
		args[i] = op
	}
	q := `SELECT ` + opColumns + ` FROM operations WHERE operation IN (` +
		joinComma(placeholders) + `) ORDER BY id DESC LIMIT 1`
	rows, err := db.conn.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ops, err := scanOps(rows)
	if err != nil || len(ops) == 0 {
		return nil, err
	}
	op := ops[0].WithRates()
	return &op, nil
}

// Aggregates returns per-operation-type rollups (count, avg/p50 duration, and
// average docs/sec for build ops). Computed in Go over the retained rows.
func (db *DB) Aggregates() (map[string]Aggregate, error) {
	ops, err := db.Recent(0)
	if err != nil {
		return nil, err
	}
	type acc struct {
		durations []int64
		rateSum   float64
		rateN     int
	}
	buckets := map[string]*acc{}
	for _, o := range ops {
		a := buckets[o.Operation]
		if a == nil {
			a = &acc{}
			buckets[o.Operation] = a
		}
		a.durations = append(a.durations, o.DurationMs)
		if r := o.WithRates().DocsPerSec; r > 0 {
			a.rateSum += r
			a.rateN++
		}
	}
	out := make(map[string]Aggregate, len(buckets))
	for op, a := range buckets {
		out[op] = Aggregate{
			Count:         len(a.durations),
			AvgMs:         round2(meanInt64(a.durations)),
			P50Ms:         medianInt64(a.durations),
			AvgDocsPerSec: round2(safeDiv(a.rateSum, float64(a.rateN))),
		}
	}
	return out, nil
}

const opColumns = `id, ts, operation, source, duration_ms, ok, error,
	files_scanned, docs_indexed, chunks_created, links_found,
	embedded, embed_skipped, embed_failed, embed_ms, total_chars,
	embedding_model, embedding_dims, result_count, mode, cli_version`

func scanOps(rows *sql.Rows) ([]Operation, error) {
	var ops []Operation
	for rows.Next() {
		var o Operation
		var ok int
		if err := rows.Scan(
			&o.ID, &o.Timestamp, &o.Operation, &o.Source, &o.DurationMs, &ok, &o.Error,
			&o.FilesScanned, &o.DocsIndexed, &o.ChunksCreated, &o.LinksFound,
			&o.Embedded, &o.EmbedSkipped, &o.EmbedFailed, &o.EmbedMs, &o.TotalChars,
			&o.EmbeddingModel, &o.EmbeddingDims, &o.ResultCount, &o.Mode, &o.CLIVersion,
		); err != nil {
			return nil, err
		}
		o.OK = ok != 0
		ops = append(ops, o)
	}
	return ops, rows.Err()
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func joinComma(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += ", "
		}
		out += p
	}
	return out
}

func meanInt64(xs []int64) float64 {
	if len(xs) == 0 {
		return 0
	}
	var sum int64
	for _, x := range xs {
		sum += x
	}
	return float64(sum) / float64(len(xs))
}

func medianInt64(xs []int64) int64 {
	if len(xs) == 0 {
		return 0
	}
	s := make([]int64, len(xs))
	copy(s, xs)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	return s[len(s)/2]
}

func safeDiv(a, b float64) float64 {
	if b == 0 {
		return 0
	}
	return a / b
}
