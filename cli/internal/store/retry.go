package store

import (
	"errors"
	"strings"
	"time"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// IsBusyErr reports whether err is a transient SQLite contention error
// (SQLITE_BUSY / SQLITE_LOCKED) — the "database is locked" class that another
// concurrent writer can cause. The busy_timeout DSN setting already makes most
// of these wait-and-succeed; this catches the rare case a writer held the lock
// longer than the timeout. It checks the typed *sqlite.Error first (masking the
// extended-result-code bits to the primary code) and falls back to the message
// for an error that lost its type through wrapping.
func IsBusyErr(err error) bool {
	if err == nil {
		return false
	}
	var se *sqlite.Error
	if errors.As(err, &se) {
		switch se.Code() & 0xFF { // primary result code; ignore extended bits
		case sqlite3.SQLITE_BUSY, sqlite3.SQLITE_LOCKED:
			return true
		}
	}
	msg := err.Error()
	return strings.Contains(msg, "database is locked") ||
		strings.Contains(msg, "database table is locked") ||
		strings.Contains(msg, "database file is locked") || // modernc SQLITE_BUSY phrasing
		strings.Contains(msg, "(SQLITE_BUSY)") ||
		strings.Contains(msg, "(SQLITE_LOCKED)")
}

// RetryBusy runs fn, retrying with exponential backoff while it returns a
// transient SQLITE_BUSY/LOCKED error. fn MUST be idempotent — the index write
// path (upserts inside one transaction that is rolled back on failure) is. Any
// non-busy error (or success) returns immediately. After the final attempt the
// last error is returned so the caller still sees the failure.
func RetryBusy(fn func() error) error {
	const maxAttempts = 4
	delay := 50 * time.Millisecond
	var err error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		err = fn()
		if !IsBusyErr(err) {
			return err
		}
		time.Sleep(delay)
		delay *= 2
	}
	return err
}
