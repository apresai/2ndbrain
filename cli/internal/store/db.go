package store

import (
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

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

		// Compute migration outcome inside the transaction. ROLLBACK and
		// COMMIT are driven by whether migrateErr is set — emitting both
		// (old code) leaves SQLite in "no active transaction" state and the
		// subsequent COMMIT fails, which used to surface as a spurious error
		// on the duplicate-column success path.
		var migrateErr error
		if err := db.conn.QueryRow("SELECT version FROM schema_version").Scan(&version); err != nil {
			migrateErr = fmt.Errorf("migrate re-read version: %w", err)
		} else if version < 2 {
			for _, stmt := range schemaV2Statements {
				if _, err := db.conn.Exec(stmt); err != nil && !isDuplicateColumn(err) {
					migrateErr = fmt.Errorf("migrate v1→v2: %w", err)
					break
				}
			}
			if migrateErr == nil {
				if _, err := db.conn.Exec("UPDATE schema_version SET version = 2"); err != nil {
					migrateErr = fmt.Errorf("migrate v1→v2 bump version: %w", err)
				}
			}
		}

		if migrateErr != nil {
			if _, rbErr := db.conn.Exec("ROLLBACK"); rbErr != nil {
				slog.Warn("migrate rollback failed", "err", rbErr)
			}
			return migrateErr
		}
		if _, err := db.conn.Exec("COMMIT"); err != nil {
			return fmt.Errorf("migrate commit: %w", err)
		}
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
	return err != nil && strings.Contains(err.Error(), "duplicate column name:")
}
