package bench

import (
	"database/sql"
	"fmt"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

const schema = `
CREATE TABLE IF NOT EXISTS favorites (
    provider   TEXT NOT NULL,
    model_id   TEXT NOT NULL,
    model_type TEXT NOT NULL,
    added_at   TEXT NOT NULL,
    PRIMARY KEY (provider, model_id)
);

CREATE TABLE IF NOT EXISTS runs (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    timestamp       TEXT    NOT NULL,
    provider        TEXT    NOT NULL,
    model_id        TEXT    NOT NULL,
    probe           TEXT    NOT NULL,
    latency_ms      INTEGER NOT NULL,
    ok              INTEGER NOT NULL DEFAULT 1,
    detail          TEXT    NOT NULL DEFAULT '',
    vault_doc_count INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_runs_model ON runs(provider, model_id);
CREATE INDEX IF NOT EXISTS idx_runs_ts    ON runs(timestamp);

CREATE TABLE IF NOT EXISTS schema_version (
    version INTEGER NOT NULL
);

INSERT OR IGNORE INTO schema_version (version) VALUES (1);
`

// Favorite represents a model the user wants to benchmark regularly.
type Favorite struct {
	Provider  string `json:"provider"`
	ModelID   string `json:"model_id"`
	ModelType string `json:"model_type"`
	AddedAt   string `json:"added_at"`
}

// Run represents a single benchmark probe execution.
type Run struct {
	ID            int    `json:"id"`
	Timestamp     string `json:"timestamp"`
	Provider      string `json:"provider"`
	ModelID       string `json:"model_id"`
	Probe         string `json:"probe"`
	LatencyMs     int64  `json:"latency_ms"`
	OK            bool   `json:"ok"`
	Detail        string `json:"detail,omitempty"`
	VaultDocCount int    `json:"vault_doc_count"`
}

// DB wraps a SQLite connection for benchmark storage.
type DB struct {
	conn *sql.DB
}

// Open opens or creates bench.db at the given path.
func Open(dbPath string) (*DB, error) {
	conn, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL&_foreign_keys=on&_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("open bench db: %w", err)
	}
	if _, err := conn.Exec(schema); err != nil {
		conn.Close()
		return nil, fmt.Errorf("migrate bench db: %w", err)
	}
	return &DB{conn: conn}, nil
}

// Close closes the database connection.
func (db *DB) Close() error {
	return db.conn.Close()
}

// AddFavorite adds a model to the favorites list. Idempotent (INSERT OR IGNORE).
func (db *DB) AddFavorite(provider, modelID, modelType string) error {
	_, err := db.conn.Exec(
		`INSERT OR IGNORE INTO favorites (provider, model_id, model_type, added_at) VALUES (?, ?, ?, ?)`,
		provider, modelID, modelType, time.Now().UTC().Format(time.RFC3339),
	)
	return err
}

// RemoveFavorite removes a model from the favorites list.
func (db *DB) RemoveFavorite(provider, modelID string) error {
	_, err := db.conn.Exec(
		`DELETE FROM favorites WHERE provider = ? AND model_id = ?`,
		provider, modelID,
	)
	return err
}

// ListFavorites returns all favorited models, ordered by when they were added.
func (db *DB) ListFavorites() ([]Favorite, error) {
	rows, err := db.conn.Query(`SELECT provider, model_id, model_type, added_at FROM favorites ORDER BY added_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var favs []Favorite
	for rows.Next() {
		var f Favorite
		if err := rows.Scan(&f.Provider, &f.ModelID, &f.ModelType, &f.AddedAt); err != nil {
			return nil, err
		}
		favs = append(favs, f)
	}
	return favs, rows.Err()
}

// InsertRun stores a benchmark run result.
func (db *DB) InsertRun(r *Run) error {
	_, err := db.conn.Exec(
		`INSERT INTO runs (timestamp, provider, model_id, probe, latency_ms, ok, detail, vault_doc_count)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		r.Timestamp, r.Provider, r.ModelID, r.Probe, r.LatencyMs,
		boolToInt(r.OK), r.Detail, r.VaultDocCount,
	)
	return err
}

// ListRuns returns the most recent benchmark runs, up to limit.
func (db *DB) ListRuns(limit int) ([]Run, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := db.conn.Query(
		`SELECT id, timestamp, provider, model_id, probe, latency_ms, ok, detail, vault_doc_count
		 FROM runs ORDER BY timestamp DESC LIMIT ?`, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanRuns(rows)
}

// LatestRunsPerModel returns the most recent run for each (model, probe) pair,
// ordered by probe then latency ascending (fastest first).
func (db *DB) LatestRunsPerModel() ([]Run, error) {
	rows, err := db.conn.Query(`
		SELECT r.id, r.timestamp, r.provider, r.model_id, r.probe, r.latency_ms, r.ok, r.detail, r.vault_doc_count
		FROM runs r
		INNER JOIN (
			SELECT provider, model_id, probe, MAX(timestamp) AS max_ts
			FROM runs
			GROUP BY provider, model_id, probe
		) latest ON r.provider = latest.provider
			AND r.model_id = latest.model_id
			AND r.probe = latest.probe
			AND r.timestamp = latest.max_ts
		ORDER BY r.probe, r.latency_ms`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanRuns(rows)
}

func scanRuns(rows *sql.Rows) ([]Run, error) {
	var runs []Run
	for rows.Next() {
		var r Run
		var ok int
		if err := rows.Scan(&r.ID, &r.Timestamp, &r.Provider, &r.ModelID, &r.Probe,
			&r.LatencyMs, &ok, &r.Detail, &r.VaultDocCount); err != nil {
			return nil, err
		}
		r.OK = ok != 0
		runs = append(runs, r)
	}
	return runs, rows.Err()
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
