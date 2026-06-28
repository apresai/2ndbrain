package store

import (
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func TestIsBusyErr(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"message busy", errors.New("database is locked"), true},
		{"message table locked", errors.New("database table is locked"), true},
		{"modernc busy phrasing", errors.New("The database file is locked (SQLITE_BUSY)"), true},
		{"modernc locked phrasing", errors.New("A table in the database is locked (SQLITE_LOCKED)"), true},
		{"wrapped busy", fmt.Errorf("upsert: %w", errors.New("database is locked")), true},
		{"unrelated error", errors.New("syntax error"), false},
		{"other sqlite phrasing", errors.New("constraint violation"), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := IsBusyErr(c.err); got != c.want {
				t.Errorf("IsBusyErr(%v) = %v, want %v", c.err, got, c.want)
			}
		})
	}
}

// TestIsBusyErr_RealContention forces a genuine modernc *sqlite.Error with
// SQLITE_BUSY via two connections contending for the write lock (busy_timeout=0),
// exercising the typed errors.As + masked-Code() path against a real driver error
// — modernc's Error has unexported fields, so this is the only way to cover it.
func TestIsBusyErr_RealContention(t *testing.T) {
	dsn := "file:" + filepath.Join(t.TempDir(), "t.db") +
		"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(0)&_txlock=immediate"
	c1, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer c1.Close()
	c1.SetMaxOpenConns(1)
	c2, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()
	c2.SetMaxOpenConns(1)

	if _, err := c1.Exec("CREATE TABLE t(x INTEGER)"); err != nil {
		t.Fatal(err)
	}
	// Hold the write lock on c1 (BEGIN IMMEDIATE via _txlock).
	tx, err := c1.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec("INSERT INTO t VALUES(1)"); err != nil {
		t.Fatal(err)
	}
	// c2's write tries to take the write lock and, with busy_timeout=0, fails fast.
	_, err = c2.Exec("INSERT INTO t VALUES(2)")
	if err == nil {
		t.Skip("environment serialized the writes; no contention error produced")
	}
	if !IsBusyErr(err) {
		t.Errorf("IsBusyErr did not recognize a real contention error: %v", err)
	}
}

func TestRetryBusy(t *testing.T) {
	busy := errors.New("database is locked")

	t.Run("success on first try", func(t *testing.T) {
		calls := 0
		err := RetryBusy(func() error { calls++; return nil })
		if err != nil || calls != 1 {
			t.Errorf("got err=%v calls=%d, want nil/1", err, calls)
		}
	})

	t.Run("non-busy error returns immediately, no retry", func(t *testing.T) {
		calls := 0
		sentinel := errors.New("boom")
		err := RetryBusy(func() error { calls++; return sentinel })
		if !errors.Is(err, sentinel) || calls != 1 {
			t.Errorf("got err=%v calls=%d, want sentinel/1", err, calls)
		}
	})

	t.Run("retries then succeeds", func(t *testing.T) {
		calls := 0
		err := RetryBusy(func() error {
			calls++
			if calls < 2 {
				return busy
			}
			return nil
		})
		if err != nil || calls != 2 {
			t.Errorf("got err=%v calls=%d, want nil/2", err, calls)
		}
	})

	t.Run("gives up after max attempts, returns last busy error", func(t *testing.T) {
		calls := 0
		err := RetryBusy(func() error { calls++; return busy })
		if !IsBusyErr(err) || calls != 4 {
			t.Errorf("got err=%v calls=%d, want busy/4", err, calls)
		}
	})
}
