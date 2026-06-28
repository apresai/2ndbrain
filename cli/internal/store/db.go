package store

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite" // pure-Go, CGO-free SQLite driver (registers "sqlite")
)

// driverName is the pure-Go modernc SQLite driver (CGO-free), registered by the
// blank import above. wal_autocheckpoint is lowered to 256 pages (~1MB) per
// connection via the DSN _pragma in Open: the SQLite default (1000 pages, ~4MB)
// runs PASSIVE-only and never truncates the -wal file, so it parks at its
// high-water mark (the observed 11x WAL bloat). 256 keeps the steady-state WAL
// small; `2nb vault checkpoint` (TRUNCATE) shrinks it on demand.
const driverName = "sqlite"

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

	// _txlock=immediate makes database/sql's Begin()/BeginTx() issue
	// "BEGIN IMMEDIATE", taking the write lock up front. This closes the
	// SQLITE_BUSY_SNAPSHOT gap on a read→write upgrade (which returns
	// immediately, bypassing busy_timeout) for the write transactions in
	// indexFile/ResolveLinks; plain reads (Query/QueryRow, no explicit tx) are
	// unaffected.
	conn, err := sql.Open(driverName, "file:"+dbPath+"?_pragma=journal_mode(WAL)&_pragma=foreign_keys(on)&_pragma=busy_timeout(5000)&_pragma=wal_autocheckpoint(256)&_txlock=immediate")
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	// Close idle pooled connections after 2 minutes so a long-lived process (the
	// GUI watcher, an MCP server, a REPL) doesn't keep a read connection open
	// indefinitely — a parked reader holds the WAL read-mark and prevents a
	// checkpoint from reclaiming WAL frames, which is the other half of the WAL
	// bloat. A fresh connection is cheap to reopen on the next query.
	conn.SetConnMaxIdleTime(2 * time.Minute)

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

// Path returns the absolute path to the SQLite database file.
func (db *DB) Path() string { return db.path }

// WALPath returns the path to the write-ahead-log sidecar (`<db>-wal`).
func (db *DB) WALPath() string { return db.path + "-wal" }

// CheckpointResult reports the outcome of a WAL checkpoint.
type CheckpointResult struct {
	WALBytesBefore    int64 `json:"wal_bytes_before"`
	WALBytesAfter     int64 `json:"wal_bytes_after"`
	DBBytes           int64 `json:"db_bytes"`
	PagesTotal        int   `json:"pages_total"`        // frames in the WAL the checkpoint saw
	PagesCheckpointed int   `json:"pages_checkpointed"` // frames written back to the DB
	Busy              bool  `json:"busy"`               // a reader/writer blocked a full TRUNCATE
}

// Checkpoint collapses the WAL into the main database and truncates the -wal
// file. It runs a PASSIVE pass first (flush whatever it can without contending),
// then TRUNCATE (the only mode that shrinks the -wal file on disk). It is
// GUI-safe: if a concurrent reader holds the WAL read-mark, TRUNCATE reports
// busy=true and leaves the file rather than erroring, so a caller can surface a
// partial result instead of failing. Stats are file sizes before/after.
func (db *DB) Checkpoint() (CheckpointResult, error) {
	res := CheckpointResult{WALBytesBefore: fileSize(db.WALPath())}

	// PASSIVE never blocks; ignore its row (best-effort flush before TRUNCATE).
	_, _ = db.conn.Exec("PRAGMA wal_checkpoint(PASSIVE)")

	var busy, log, checkpointed int
	if err := db.conn.QueryRow("PRAGMA wal_checkpoint(TRUNCATE)").Scan(&busy, &log, &checkpointed); err != nil {
		return res, fmt.Errorf("wal_checkpoint(TRUNCATE): %w", err)
	}
	res.Busy = busy != 0
	// On a busy TRUNCATE SQLite reports log == checkpointed == -1 (it couldn't
	// determine the frame counts because a reader held the WAL); clamp to 0 so
	// callers and the human/JSON output never show a confusing "-1 of -1". The
	// Busy flag is the unambiguous signal.
	res.PagesTotal = max(0, log)
	res.PagesCheckpointed = max(0, checkpointed)
	res.WALBytesAfter = fileSize(db.WALPath())
	res.DBBytes = fileSize(db.path)
	if res.Busy {
		slog.Debug("wal checkpoint could not fully truncate; a reader is active", "wal_bytes", res.WALBytesAfter)
	}
	return res, nil
}

// fileSize returns the size of a file in bytes, or 0 if it can't be stat'd
// (e.g. the -wal file doesn't exist because the DB has never been written in
// WAL mode or was already checkpointed).
func fileSize(path string) int64 {
	if fi, err := os.Stat(path); err == nil {
		return fi.Size()
	}
	return 0
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
