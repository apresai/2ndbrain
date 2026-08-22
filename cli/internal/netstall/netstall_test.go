package netstall

import (
	"errors"
	"io"
	"sync/atomic"
	"testing"
	"time"
)

// A transfer that keeps producing bytes must never be cancelled, no matter
// how long it takes in total: each read resets the watchdog. The writer here
// takes ~1s of wall clock against a 500ms stall window, so a total-time
// timeout of the same magnitude WOULD have fired; only per-gap silence may.
func TestGuardProgressResetsWatchdog(t *testing.T) {
	pr, pw := io.Pipe()
	var stalled atomic.Bool
	r, stop := Guard(pr, 500*time.Millisecond, func() {
		stalled.Store(true)
		_ = pw.CloseWithError(errors.New("stalled"))
	})
	defer stop()

	go func() {
		for range 10 {
			time.Sleep(100 * time.Millisecond) // each gap well inside the window
			if _, err := pw.Write([]byte("x")); err != nil {
				return
			}
		}
		_ = pw.Close()
	}()

	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}
	if stalled.Load() {
		t.Error("watchdog fired despite continuous progress; Guard must bound silence, not total time")
	}
	if len(data) != 10 {
		t.Errorf("read %d bytes, want 10", len(data))
	}
}

// A transfer that goes silent (without closing) must be cancelled by the
// watchdog rather than blocking the read forever.
func TestGuardStallCancels(t *testing.T) {
	pr, pw := io.Pipe()
	stallErr := errors.New("stalled: no data")
	// The cancel models what a context.CancelFunc does to an in-flight HTTP
	// body: the pending read returns with an error. On a pipe that is the
	// write side closing with the error.
	r, stop := Guard(pr, 150*time.Millisecond, func() {
		_ = pw.CloseWithError(stallErr)
	})
	defer stop()

	go func() {
		_, _ = pw.Write([]byte("x")) // one byte of progress, then silence
		// Deliberately never closed: the pipe now models a half-open socket.
	}()

	done := make(chan error, 1)
	go func() {
		_, err := io.ReadAll(r)
		done <- err
	}()

	select {
	case err := <-done:
		if !errors.Is(err, stallErr) {
			t.Fatalf("read ended with %v, want the watchdog's stall error", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("read blocked past the stall window; the watchdog never fired")
	}
}

// stop() disarms the watchdog: after a completed transfer, the stall window
// elapsing must not invoke cancel.
func TestGuardStopDisarms(t *testing.T) {
	pr, pw := io.Pipe()
	var fired atomic.Bool
	r, stop := Guard(pr, 100*time.Millisecond, func() { fired.Store(true) })

	go func() {
		_, _ = pw.Write([]byte("done"))
		_ = pw.Close()
	}()
	if _, err := io.ReadAll(r); err != nil {
		t.Fatalf("read failed: %v", err)
	}
	stop()
	time.Sleep(250 * time.Millisecond) // well past the window
	if fired.Load() {
		t.Error("watchdog fired after stop(); a finished transfer must not be cancelled late")
	}
}
