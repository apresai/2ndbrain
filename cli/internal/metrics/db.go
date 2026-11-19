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
	"context"
	"database/sql"
	"fmt"
	"math"
	"sort"
	"strings"
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
	OpChat     = "chat"      // multi-turn chat RAG turn
	OpPolish   = "polish"    // note copy-edit / grounded links
	OpFix      = "fix"       // vault-wide AI-assisted links & schema fix
	OpSuggest  = "suggest"   // suggest-links / suggest-target
	OpProbe    = "probe"     // live model connectivity/smoke probe
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
	FilesScanned  int `json:"files_scanned,omitempty"`
	DocsIndexed   int `json:"docs_indexed,omitempty"`
	ChunksCreated int `json:"chunks_created,omitempty"`
	LinksFound    int `json:"links_found,omitempty"`
	Embedded      int `json:"embedded,omitempty"`
	EmbedSkipped  int `json:"embed_skipped,omitempty"`
	EmbedFailed   int `json:"embed_failed,omitempty"`
	// EmbedRetries is how many provider retries the operation rode out
	// (throttling, server faults). Recorded because wall time alone cannot tell
	// a slow model from a throttled account, and the two have different fixes.
	// Schema v3.
	EmbedRetries   int    `json:"embed_retries,omitempty"`
	EmbedMs        int64  `json:"embed_ms,omitempty"`
	TotalChars     int    `json:"total_chars,omitempty"`
	EmbeddingModel string `json:"embedding_model,omitempty"`
	EmbeddingDims  int    `json:"embedding_dims,omitempty"`

	// query
	ResultCount int    `json:"result_count,omitempty"`
	Mode        string `json:"mode,omitempty"`

	// tokens — input/output tokens consumed by the op. Generation (ask) uses the
	// provider's actual usage; embeddings/search estimate (chars/4). Schema v2.
	InputTokens  int `json:"input_tokens,omitempty"`
	OutputTokens int `json:"output_tokens,omitempty"`

	// Per-slot provider/model attribution & estimated USD cost. Schema v4.
	EmbeddingProvider  string  `json:"embedding_provider,omitempty"`
	GenerationProvider string  `json:"generation_provider,omitempty"`
	GenerationModel    string  `json:"generation_model,omitempty"`
	EmbedInputTokens   int     `json:"embed_input_tokens,omitempty"`
	GenInputTokens     int     `json:"gen_input_tokens,omitempty"`
	GenOutputTokens    int     `json:"gen_output_tokens,omitempty"`
	CostUSD            float64 `json:"cost_usd,omitempty"`
	KnownPricing       bool    `json:"known_pricing,omitempty"`

	CLIVersion string `json:"cli_version,omitempty"`

	// UsageLegs carries slot-level usage for unpruned ai_usage_ledger upserts.
	// Not stored directly in the operations row JSON.
	UsageLegs []UsageLeg `json:"-"`

	// derived (computed by WithRates, not persisted)
	DocsPerSec       float64 `json:"docs_per_sec,omitempty"`
	EmbeddingsPerSec float64 `json:"embeddings_per_sec,omitempty"`
	CharsPerSec      float64 `json:"chars_per_sec,omitempty"`
}

// UsageLeg captures one model slot's token and cost attribution within an
// operation so hybrid combos (e.g., local embeddings + cloud generation)
// attribute cloud spend accurately in ai_usage_ledger.
type UsageLeg struct {
	Provider     string
	Model        string
	Slot         string // "generation" | "embedding" | "rerank"
	Operation    string
	IsCloud      bool
	Calls        int
	InputTokens  int
	OutputTokens int
	CostUSD      float64
	KnownPricing bool
}

// LedgerRow is one aggregated daily entry in ai_usage_ledger.
type LedgerRow struct {
	Day           string  `json:"day"`
	Provider      string  `json:"provider"`
	Model         string  `json:"model"`
	Slot          string  `json:"slot"`
	Operation     string  `json:"operation"`
	IsCloud       bool    `json:"is_cloud"`
	Calls         int     `json:"calls"`
	InputTokens   int     `json:"input_tokens"`
	OutputTokens  int     `json:"output_tokens"`
	CostUSD       float64 `json:"cost_usd"`
	UnpricedCalls int     `json:"unpriced_calls"`
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
	// Token sums across the retained window for this operation type.
	TokensIn  int `json:"tokens_in,omitempty"`
	TokensOut int `json:"tokens_out,omitempty"`
	// EmbedRetries is the total provider retries across the retained window.
	EmbedRetries int `json:"embed_retries,omitempty"`
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
	db := &DB{conn: conn}
	if err := db.migrate(); err != nil {
		conn.Close()
		return nil, fmt.Errorf("migrate metrics db: %w", err)
	}
	return db, nil
}

// maxMetricsSchemaVersion is the highest schema version this binary writes.
const maxMetricsSchemaVersion = 4

// metricsSchemaV2 adds the token columns. Run as an idempotent ALTER (rather than
// baked into the v1 CREATE) so an EXISTING metrics.db — carrying the user's
// sequential-vs-parallel reembed history — gains the columns with DEFAULT 0
// instead of being dropped and recreated.
var metricsSchemaV2 = []string{
	`ALTER TABLE operations ADD COLUMN input_tokens INTEGER NOT NULL DEFAULT 0`,
	`ALTER TABLE operations ADD COLUMN output_tokens INTEGER NOT NULL DEFAULT 0`,
}

// metricsSchemaV3 adds the provider-retry count. Additive ALTER for the same
// reason v2 was: an existing metrics.db carries the user's whole index history
// and must gain the column rather than be recreated. Deliberately NOT added to
// the base `schema` const, so a fresh DB takes the same migration path an
// upgraded one does and the migration is exercised on every install.
var metricsSchemaV3 = []string{
	`ALTER TABLE operations ADD COLUMN embed_retries INTEGER NOT NULL DEFAULT 0`,
}

// metricsSchemaV4 adds per-slot provider/model/cost attribution to operations
// plus an unpruned daily ai_usage_ledger table so pruning operations to 200
// rows never discards historical AI usage or estimated cloud spend.
var metricsSchemaV4 = []string{
	`ALTER TABLE operations ADD COLUMN embedding_provider TEXT NOT NULL DEFAULT ''`,
	`ALTER TABLE operations ADD COLUMN generation_provider TEXT NOT NULL DEFAULT ''`,
	`ALTER TABLE operations ADD COLUMN generation_model TEXT NOT NULL DEFAULT ''`,
	`ALTER TABLE operations ADD COLUMN embed_input_tokens INTEGER NOT NULL DEFAULT 0`,
	`ALTER TABLE operations ADD COLUMN gen_input_tokens INTEGER NOT NULL DEFAULT 0`,
	`ALTER TABLE operations ADD COLUMN gen_output_tokens INTEGER NOT NULL DEFAULT 0`,
	`ALTER TABLE operations ADD COLUMN cost_usd REAL NOT NULL DEFAULT 0`,
	`ALTER TABLE operations ADD COLUMN known_pricing INTEGER NOT NULL DEFAULT 0`,
	`CREATE TABLE IF NOT EXISTS ai_usage_ledger (
		day            TEXT    NOT NULL,
		provider       TEXT    NOT NULL,
		model          TEXT    NOT NULL,
		slot           TEXT    NOT NULL,
		operation      TEXT    NOT NULL,
		is_cloud       INTEGER NOT NULL DEFAULT 0,
		calls          INTEGER NOT NULL DEFAULT 0,
		input_tokens   INTEGER NOT NULL DEFAULT 0,
		output_tokens  INTEGER NOT NULL DEFAULT 0,
		cost_usd       REAL    NOT NULL DEFAULT 0,
		unpriced_calls INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY (day, provider, model, slot, operation)
	)`,
	`CREATE INDEX IF NOT EXISTS idx_ai_usage_day ON ai_usage_ledger(day DESC)`,
	`INSERT OR IGNORE INTO ai_usage_ledger (
		day, provider, model, slot, operation, is_cloud, calls, input_tokens, output_tokens, cost_usd, unpriced_calls
	)
	SELECT
		substr(ts, 1, 10),
		CASE
			WHEN embedding_provider != '' THEN embedding_provider
			WHEN embedding_model LIKE 'embeddinggemma-%' OR embedding_model LIKE 'gemma4-%' THEN 'llama-local'
			WHEN embedding_model LIKE 'gemini-%' THEN 'gemini'
			WHEN embedding_model LIKE 'amazon.%' OR embedding_model LIKE 'cohere.%' THEN 'bedrock'
			WHEN embedding_model LIKE '%/%' THEN 'openrouter'
			ELSE ''
		END AS inferred_provider,
		embedding_model,
		'embedding',
		operation,
		CASE
			WHEN embedding_model LIKE 'embeddinggemma-%' OR embedding_model LIKE 'gemma4-%' THEN 0
			ELSE 1
		END AS is_cloud,
		SUM(CASE WHEN embedded > 0 THEN embedded ELSE 1 END),
		SUM(input_tokens),
		0,
		0,
		SUM(CASE WHEN embedding_model LIKE 'embeddinggemma-%' OR embedding_model LIKE 'gemma4-%' THEN 0 ELSE 1 END)
	FROM operations
	WHERE embedding_model != '' AND input_tokens > 0 AND length(ts) >= 10
	GROUP BY substr(ts, 1, 10), inferred_provider, embedding_model, operation`,
}

// migrate brings an existing operations table up to maxMetricsSchemaVersion.
func (db *DB) migrate() error {
	// Seed the single version row ONLY when the table is empty. A bare
	// `INSERT OR IGNORE VALUES (1)` in the base schema would re-insert 1 once
	// migrate UPDATEs the row to 2 (the PK is then 2, so 1 no longer conflicts),
	// accumulating rows on every open. OR IGNORE also makes a concurrent
	// double-seed safe (the loser conflicts on the PK).
	if _, err := db.conn.Exec(
		`INSERT OR IGNORE INTO schema_version (version) SELECT 1 WHERE NOT EXISTS (SELECT 1 FROM schema_version)`,
	); err != nil {
		return err
	}
	var version int
	if err := db.conn.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_version`).Scan(&version); err != nil {
		return err
	}
	// A DB written by a NEWER binary (version > what we know) is left untouched:
	// reads select named columns, so any extra columns are harmless, and this is
	// best-effort telemetry — never block an older binary on a forward-version DB.
	if version >= maxMetricsSchemaVersion {
		return nil
	}
	if version < 2 {
		if err := db.applyMigration(2, metricsSchemaV2); err != nil {
			return err
		}
	}
	if version < 3 {
		if err := db.applyMigration(3, metricsSchemaV3); err != nil {
			return err
		}
	}
	if version < 4 {
		if err := db.applyMigration(4, metricsSchemaV4); err != nil {
			return err
		}
	}
	return nil
}

// applyMigration runs the statements for one version bump under BEGIN EXCLUSIVE
// on a pinned connection, re-checking the version inside the lock so concurrent
// openers (CLI + MCP + app) can't double-migrate. Duplicate-column errors are
// tolerated (a crash between ALTER and the version bump leaves the column added).
func (db *DB) applyMigration(target int, stmts []string) error {
	conn, err := db.conn.Conn(context.Background())
	if err != nil {
		return err
	}
	defer conn.Close()
	ctx := context.Background()

	if _, err := conn.ExecContext(ctx, "BEGIN EXCLUSIVE"); err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(ctx, "ROLLBACK")
		}
	}()

	var version int
	if err := conn.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_version`).Scan(&version); err != nil {
		return err
	}
	if version >= target {
		// Mirror the success path: only mark committed AFTER COMMIT succeeds, so
		// a transient COMMIT failure still triggers the deferred ROLLBACK rather
		// than returning the pinned conn to the pool with an open txn.
		if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
			return err
		}
		committed = true
		return nil
	}
	for _, s := range stmts {
		if _, err := conn.ExecContext(ctx, s); err != nil && !isDuplicateColumn(err) {
			return err
		}
	}
	// schema_version holds a single PK row; move it to the target version.
	if _, err := conn.ExecContext(ctx, `UPDATE schema_version SET version = ?`, target); err != nil {
		return err
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return err
	}
	committed = true
	return nil
}

func isDuplicateColumn(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "duplicate column")
}

// Close closes the database connection.
func (db *DB) Close() error {
	return db.conn.Close()
}

// Clear deletes all recorded operations and daily usage ledger entries and
// returns how many operation rows were removed.
func (db *DB) Clear() (int64, error) {
	if _, err := db.conn.Exec(`DELETE FROM ai_usage_ledger`); err != nil {
		return 0, err
	}
	res, err := db.conn.Exec(`DELETE FROM operations`)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// Record stores one operation, upserts any UsageLegs into ai_usage_ledger, and
// prunes that operation type back to DefaultPerTypeCap rows. Timestamp/Source
// default to now/"cli" when empty.
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
			embedded, embed_skipped, embed_failed, embed_retries, embed_ms, total_chars,
			embedding_model, embedding_dims,
			embedding_provider, generation_provider, generation_model,
			embed_input_tokens, gen_input_tokens, gen_output_tokens,
			cost_usd, known_pricing,
			result_count, mode,
			input_tokens, output_tokens, cli_version
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		op.Timestamp, op.Operation, op.Source, op.DurationMs, boolToInt(op.OK), op.Error,
		op.FilesScanned, op.DocsIndexed, op.ChunksCreated, op.LinksFound,
		op.Embedded, op.EmbedSkipped, op.EmbedFailed, op.EmbedRetries, op.EmbedMs, op.TotalChars,
		op.EmbeddingModel, op.EmbeddingDims,
		op.EmbeddingProvider, op.GenerationProvider, op.GenerationModel,
		op.EmbedInputTokens, op.GenInputTokens, op.GenOutputTokens,
		op.CostUSD, boolToInt(op.KnownPricing),
		op.ResultCount, op.Mode,
		op.InputTokens, op.OutputTokens, op.CLIVersion,
	)
	if err != nil {
		return err
	}

	day := op.Timestamp
	if len(day) >= 10 {
		day = day[:10]
	} else {
		day = time.Now().UTC().Format("2006-01-02")
	}
	for _, leg := range op.UsageLegs {
		if leg.Calls <= 0 && leg.InputTokens <= 0 && leg.OutputTokens <= 0 {
			continue
		}
		legOp := leg.Operation
		if legOp == "" {
			legOp = op.Operation
		}
		unpriced := 0
		if leg.IsCloud && !leg.KnownPricing {
			unpriced = leg.Calls
			if unpriced <= 0 {
				unpriced = 1
			}
		}
		if _, err := db.conn.Exec(
			`INSERT INTO ai_usage_ledger (
				day, provider, model, slot, operation, is_cloud,
				calls, input_tokens, output_tokens, cost_usd, unpriced_calls
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(day, provider, model, slot, operation) DO UPDATE SET
				calls = calls + excluded.calls,
				input_tokens = input_tokens + excluded.input_tokens,
				output_tokens = output_tokens + excluded.output_tokens,
				cost_usd = cost_usd + excluded.cost_usd,
				unpriced_calls = unpriced_calls + excluded.unpriced_calls,
				is_cloud = excluded.is_cloud`,
			day, leg.Provider, leg.Model, leg.Slot, legOp, boolToInt(leg.IsCloud),
			leg.Calls, leg.InputTokens, leg.OutputTokens, leg.CostUSD, unpriced,
		); err != nil {
			return err
		}
	}
	return db.prune(op.Operation, DefaultPerTypeCap)
}

// UsageEntries returns aggregated daily AI usage ledger rows on or after
// sinceDay (YYYY-MM-DD), or all rows when sinceDay is empty, newest day first.
func (db *DB) UsageEntries(sinceDay string) ([]LedgerRow, error) {
	q := `SELECT day, provider, model, slot, operation, is_cloud, calls, input_tokens, output_tokens, cost_usd, unpriced_calls
		FROM ai_usage_ledger`
	var rows *sql.Rows
	var err error
	if sinceDay != "" {
		rows, err = db.conn.Query(q+` WHERE day >= ? ORDER BY day DESC, provider, model, slot, operation`, sinceDay)
	} else {
		rows, err = db.conn.Query(q + ` ORDER BY day DESC, provider, model, slot, operation`)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []LedgerRow
	for rows.Next() {
		var r LedgerRow
		var isCloud int
		if err := rows.Scan(
			&r.Day, &r.Provider, &r.Model, &r.Slot, &r.Operation, &isCloud,
			&r.Calls, &r.InputTokens, &r.OutputTokens, &r.CostUSD, &r.UnpricedCalls,
		); err != nil {
			return nil, err
		}
		r.IsCloud = isCloud != 0
		out = append(out, r)
	}
	return out, rows.Err()
}

// HistoricalEmbedThroughput returns the measured docs/sec and chars/sec from
// the most recent successful bulk embed build (>= 3 embedded docs) matching
// the active embedding model (and provider when recorded).
func (db *DB) HistoricalEmbedThroughput(provider, model string) (docsPerSec, charsPerSec float64, ok bool) {
	if model == "" {
		return 0, 0, false
	}
	var embedded, totalChars int
	var embedMs int64
	err := db.conn.QueryRow(
		`SELECT embedded, total_chars, embed_ms
		 FROM operations
		 WHERE ok = 1
		   AND embedded >= 3
		   AND embed_ms > 0
		   AND total_chars > 0
		   AND embedding_model = ?
		   AND (embedding_provider = ? OR embedding_provider = '')
		 ORDER BY id DESC
		 LIMIT 1`,
		model, provider,
	).Scan(&embedded, &totalChars, &embedMs)
	if err != nil || embedMs <= 0 || embedded <= 0 || totalChars <= 0 {
		return 0, 0, false
	}
	secs := float64(embedMs) / 1000.0
	return float64(embedded) / secs, float64(totalChars) / secs, true
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
		durations           []int64
		rateSum             float64
		rateN               int
		tokensIn, tokensOut int
		retries             int
	}
	buckets := map[string]*acc{}
	for _, o := range ops {
		a := buckets[o.Operation]
		if a == nil {
			a = &acc{}
			buckets[o.Operation] = a
		}
		a.durations = append(a.durations, o.DurationMs)
		a.tokensIn += o.InputTokens
		a.tokensOut += o.OutputTokens
		a.retries += o.EmbedRetries
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
			TokensIn:      a.tokensIn,
			TokensOut:     a.tokensOut,
			EmbedRetries:  a.retries,
		}
	}
	return out, nil
}

const opColumns = `id, ts, operation, source, duration_ms, ok, error,
	files_scanned, docs_indexed, chunks_created, links_found,
	embedded, embed_skipped, embed_failed, embed_retries, embed_ms, total_chars,
	embedding_model, embedding_dims,
	embedding_provider, generation_provider, generation_model,
	embed_input_tokens, gen_input_tokens, gen_output_tokens,
	cost_usd, known_pricing,
	result_count, mode,
	input_tokens, output_tokens, cli_version`

func scanOps(rows *sql.Rows) ([]Operation, error) {
	var ops []Operation
	for rows.Next() {
		var o Operation
		var ok, knownPricing int
		if err := rows.Scan(
			&o.ID, &o.Timestamp, &o.Operation, &o.Source, &o.DurationMs, &ok, &o.Error,
			&o.FilesScanned, &o.DocsIndexed, &o.ChunksCreated, &o.LinksFound,
			&o.Embedded, &o.EmbedSkipped, &o.EmbedFailed, &o.EmbedRetries, &o.EmbedMs, &o.TotalChars,
			&o.EmbeddingModel, &o.EmbeddingDims,
			&o.EmbeddingProvider, &o.GenerationProvider, &o.GenerationModel,
			&o.EmbedInputTokens, &o.GenInputTokens, &o.GenOutputTokens,
			&o.CostUSD, &knownPricing,
			&o.ResultCount, &o.Mode,
			&o.InputTokens, &o.OutputTokens, &o.CLIVersion,
		); err != nil {
			return nil, err
		}
		o.OK = ok != 0
		o.KnownPricing = knownPricing != 0
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
