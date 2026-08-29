package bench

import (
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite" // pure-Go, CGO-free SQLite driver (registers "sqlite")
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
    vault_doc_count INTEGER NOT NULL DEFAULT 0,
    plane           TEXT    NOT NULL DEFAULT '',
    region          TEXT    NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_runs_model ON runs(provider, model_id);
CREATE INDEX IF NOT EXISTS idx_runs_ts    ON runs(timestamp);

CREATE TABLE IF NOT EXISTS schema_version (
    version INTEGER NOT NULL
);
`

const benchSchemaVersion = 2

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
	Plane         string `json:"plane,omitempty"`
	Region        string `json:"region,omitempty"`
}

// DB wraps a SQLite connection for benchmark storage.
type DB struct {
	conn *sql.DB
}

// Open opens or creates bench.db at the given path.
func Open(dbPath string) (*DB, error) {
	// Bare path (no file: prefix) so a path with URI metacharacters (e.g. '%')
	// stays literal; modernc still parses _pragma from the query. See store/db.go.
	conn, err := sql.Open("sqlite", dbPath+"?_pragma=journal_mode(WAL)&_pragma=foreign_keys(on)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("open bench db: %w", err)
	}
	if _, err := conn.Exec(schema); err != nil {
		conn.Close()
		return nil, fmt.Errorf("migrate bench db: %w", err)
	}
	db := &DB{conn: conn}
	if err := db.migrate(); err != nil {
		conn.Close()
		return nil, fmt.Errorf("migrate bench db: %w", err)
	}
	return db, nil
}

// migrate brings an existing bench.db to benchSchemaVersion.
//
// schema_version has no primary key and older Opens ran INSERT OR IGNORE
// VALUES (1) every time, so a real vault holds dozens of identical rows.
// The gate reads MAX(version) and collapses the stamp to a single row.
func (db *DB) migrate() error {
	var ver int
	err := db.conn.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_version`).Scan(&ver)
	if err != nil {
		return err
	}
	if ver < benchSchemaVersion {
		if err := db.ensureRunsRouteColumns(); err != nil {
			return err
		}
		if _, err := db.conn.Exec(`DELETE FROM schema_version`); err != nil {
			return err
		}
		if _, err := db.conn.Exec(`INSERT INTO schema_version (version) VALUES (?)`, benchSchemaVersion); err != nil {
			return err
		}
	} else {
		if _, err := db.conn.Exec(`
			DELETE FROM schema_version WHERE rowid NOT IN (
				SELECT MIN(rowid) FROM schema_version WHERE version = (SELECT MAX(version) FROM schema_version)
			)`); err != nil {
			return err
		}
	}
	return nil
}

func (db *DB) ensureRunsRouteColumns() error {
	hasPlane, err := db.hasColumn("runs", "plane")
	if err != nil {
		return err
	}
	if !hasPlane {
		if _, err := db.conn.Exec(`ALTER TABLE runs ADD COLUMN plane TEXT NOT NULL DEFAULT ''`); err != nil {
			return err
		}
	}
	hasRegion, err := db.hasColumn("runs", "region")
	if err != nil {
		return err
	}
	if !hasRegion {
		if _, err := db.conn.Exec(`ALTER TABLE runs ADD COLUMN region TEXT NOT NULL DEFAULT ''`); err != nil {
			return err
		}
	}
	return nil
}

func (db *DB) hasColumn(table, col string) (bool, error) {
	rows, err := db.conn.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return false, err
		}
		if name == col {
			return true, nil
		}
	}
	return false, rows.Err()
}

// BackfillMissingRoutes stamps pre-migration runs (empty plane and region)
// with the route lookup currently resolves for that model. The backfill is
// inferred rather than recorded: those probes ran before routes existed, and
// this is the endpoint they were almost certainly measured against. Leaving
// them empty groups them separately from post-migration rows and shows a
// ghost row in the compare matrix across the upgrade. Runs once; rows that
// already have a route are left alone.
func (db *DB) BackfillMissingRoutes(lookup func(provider, modelID string) (plane, region string)) error {
	if lookup == nil {
		return nil
	}
	rows, err := db.conn.Query(`SELECT DISTINCT provider, model_id FROM runs WHERE plane = '' AND region = ''`)
	if err != nil {
		return err
	}
	defer rows.Close()
	type pair struct{ provider, modelID string }
	var pairs []pair
	for rows.Next() {
		var p pair
		if err := rows.Scan(&p.provider, &p.modelID); err != nil {
			return err
		}
		pairs = append(pairs, p)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, p := range pairs {
		plane, region := lookup(p.provider, p.modelID)
		if plane == "" && region == "" {
			continue
		}
		if _, err := db.conn.Exec(
			`UPDATE runs SET plane = ?, region = ? WHERE provider = ? AND model_id = ? AND plane = '' AND region = ''`,
			plane, region, p.provider, p.modelID,
		); err != nil {
			return err
		}
	}
	return nil
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
		`INSERT INTO runs (timestamp, provider, model_id, probe, latency_ms, ok, detail, vault_doc_count, plane, region)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.Timestamp, r.Provider, r.ModelID, r.Probe, r.LatencyMs,
		boolToInt(r.OK), r.Detail, r.VaultDocCount, r.Plane, r.Region,
	)
	return err
}

// ListRuns returns the most recent benchmark runs, up to limit.
func (db *DB) ListRuns(limit int) ([]Run, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := db.conn.Query(
		`SELECT id, timestamp, provider, model_id, probe, latency_ms, ok, detail, vault_doc_count, plane, region
		 FROM runs ORDER BY timestamp DESC LIMIT ?`, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanRuns(rows)
}

// LatestRunsPerModel returns the most recent run for each
// (model, probe, plane, region) tuple, ordered by probe then latency
// ascending (fastest first). Two routes of one model are two rows.
func (db *DB) LatestRunsPerModel() ([]Run, error) {
	rows, err := db.conn.Query(`
		SELECT r.id, r.timestamp, r.provider, r.model_id, r.probe, r.latency_ms, r.ok, r.detail, r.vault_doc_count, r.plane, r.region
		FROM runs r
		INNER JOIN (
			SELECT provider, model_id, probe, plane, region, MAX(timestamp) AS max_ts
			FROM runs
			GROUP BY provider, model_id, probe, plane, region
		) latest ON r.provider = latest.provider
			AND r.model_id = latest.model_id
			AND r.probe = latest.probe
			AND r.plane = latest.plane
			AND r.region = latest.region
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
			&r.LatencyMs, &ok, &r.Detail, &r.VaultDocCount, &r.Plane, &r.Region); err != nil {
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
