package store

import (
	"context"
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
const MaxSchemaVersion = 3

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
		if err := db.applyMigration(2, schemaV2Statements, func(_ string, err error) bool {
			return isDuplicateColumn(err)
		}); err != nil {
			return err
		}
	}

	if version < 3 {
		if err := db.applyMigration(3, schemaV3Statements, isDuplicateColumnOrTable); err != nil {
			return err
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

// applyMigration runs stmts and bumps schema_version to targetVersion while
// holding a SQLite EXCLUSIVE lock. Every statement (BEGIN/DDL/UPDATE/COMMIT)
// runs on a single pinned *sql.Conn so the transaction can't be split across
// the *sql.DB connection pool — a raw "BEGIN EXCLUSIVE" Exec on the pool can
// land on a different connection than the matching COMMIT, leaving the lock
// stuck on an idle connection and the COMMIT erroring with "no transaction".
// Idempotent: re-reads the version inside the lock and skips if another
// process already migrated, and tolerates duplicate column/table errors (a
// half-applied prior run) via isDup.
func (db *DB) applyMigration(targetVersion int, stmts []string, isDup func(stmt string, err error) bool) error {
	ctx := context.Background()
	conn, err := db.conn.Conn(ctx)
	if err != nil {
		return fmt.Errorf("migrate v%d acquire conn: %w", targetVersion, err)
	}
	defer conn.Close()

	if _, err := conn.ExecContext(ctx, "BEGIN EXCLUSIVE"); err != nil {
		return fmt.Errorf("migrate v%d lock: %w", targetVersion, err)
	}

	migrateErr := func() error {
		var version int
		if err := conn.QueryRowContext(ctx, "SELECT version FROM schema_version").Scan(&version); err != nil {
			return fmt.Errorf("migrate re-read version: %w", err)
		}
		if version >= targetVersion {
			return nil // another process won the race; nothing to do
		}
		for _, stmt := range stmts {
			if _, err := conn.ExecContext(ctx, stmt); err != nil && !isDup(stmt, err) {
				return fmt.Errorf("migrate to v%d: %w", targetVersion, err)
			}
		}
		if _, err := conn.ExecContext(ctx, "UPDATE schema_version SET version = ?", targetVersion); err != nil {
			return fmt.Errorf("migrate to v%d bump version: %w", targetVersion, err)
		}
		return nil
	}()

	if migrateErr != nil {
		if _, rbErr := conn.ExecContext(ctx, "ROLLBACK"); rbErr != nil {
			slog.Warn("migrate rollback failed", "err", rbErr)
		}
		return migrateErr
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return fmt.Errorf("migrate v%d commit: %w", targetVersion, err)
	}
	return nil
}

func isDuplicateColumn(err error) bool {
	return err != nil && strings.Contains(err.Error(), "duplicate column name:")
}

func isDuplicateColumnOrTable(stmt string, err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "duplicate column name:") ||
		strings.Contains(msg, "already exists")
}
