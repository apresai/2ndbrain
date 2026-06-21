package store

import (
	"errors"
	"fmt"
	"testing"

	sqlite3 "github.com/mattn/go-sqlite3"
)

func TestIsBusyErr(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"busy code", sqlite3.Error{Code: sqlite3.ErrBusy}, true},
		{"locked code", sqlite3.Error{Code: sqlite3.ErrLocked}, true},
		{"wrapped busy code", fmt.Errorf("upsert: %w", sqlite3.Error{Code: sqlite3.ErrBusy}), true},
		{"message fallback", errors.New("database is locked"), true},
		{"unrelated error", errors.New("syntax error"), false},
		{"other sqlite code", sqlite3.Error{Code: sqlite3.ErrConstraint}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := IsBusyErr(c.err); got != c.want {
				t.Errorf("IsBusyErr(%v) = %v, want %v", c.err, got, c.want)
			}
		})
	}
}

func TestRetryBusy(t *testing.T) {
	busy := sqlite3.Error{Code: sqlite3.ErrBusy}

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
