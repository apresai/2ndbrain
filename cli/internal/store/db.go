package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "github.com/mattn/go-sqlite3"
)

// MaxSchemaVersion is the highest schema version this binary understands.
// A vault whose schema_version row exceeds this value was produced by a
// newer 2nb and will be refused at open time with an upgrade hint — this
// is cheaper than risking silent corruption if a future migration adds
// required columns or behavior.
const MaxSchemaVersion = 2

type DB struct {
	conn *sql.DB
	path string
}

func Open(dbPath string) (*DB, error) {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return nil, fmt.Errorf("create db directory: %w", err)
	}

	conn, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL&_foreign_keys=on&_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	if err := conn.Ping(); err != nil {
		conn.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}

	db := &DB{conn: conn, path: dbPath}
	if err := db.migrate(); err != nil {
		conn.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}

	return db, nil
}

func (db *DB) Close() error {
	return db.conn.Close()
}

func (db *DB) Conn() *sql.DB {
	return db.conn
}

func (db *DB) migrate() error {
	if _, err := db.conn.Exec(schemaV1); err != nil {
		return err
	}

	var version int
	err := db.conn.QueryRow("SELECT version FROM schema_version").Scan(&version)
	if err != nil {
		return err
	}

	if version < 2 {
		// Use EXCLUSIVE lock to prevent concurrent migration races (C1 fix)
		if _, err := db.conn.Exec("BEGIN EXCLUSIVE"); err != nil {
			return fmt.Errorf("migrate lock: %w", err)
		}
		// Re-read version inside the lock
		db.conn.QueryRow("SELECT version FROM schema_version").Scan(&version)
		if version < 2 {
			if _, err := db.conn.Exec(schemaV2); err != nil {
				db.conn.Exec("ROLLBACK")
				// Treat duplicate column as success (another process already migrated)
				if !isDuplicateColumn(err) {
					return fmt.Errorf("migrate v1→v2: %w", err)
				}
			}
		}
		db.conn.Exec("COMMIT")
	}

	// Refuse to open a vault produced by a newer 2nb. Older binaries
	// reading unknown columns are usually fine, but future migrations may
	// introduce required invariants (e.g., new NOT NULL columns) that this
	// binary can't satisfy. Fail loud here rather than quietly.
	if version > MaxSchemaVersion {
		return fmt.Errorf("vault uses schema v%d but this 2nb binary supports up to v%d — upgrade with 'brew upgrade apresai/tap/twonb'", version, MaxSchemaVersion)
	}

	return nil
}

func isDuplicateColumn(err error) bool {
	return err != nil && fmt.Sprintf("%v", err) == "duplicate column name: embedding"
}
