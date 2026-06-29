package mcp

import (
	"sync/atomic"
	"testing"
	"time"
)

// newTestParentWatchdog builds a parentWatchdog with an injected getppid so the
// "parent" PID is controllable without spawning real processes.
func newTestParentWatchdog(interval time.Duration, start int, getppid func() int, onExpire func(start, now int)) *parentWatchdog {
	return &parentWatchdog{start: start, interval: interval, getppid: getppid, onExpire: onExpire}
}

func TestParentWatchdog_ExpiresWhenParentChanges(t *testing.T) {
	type expiry struct{ start, now int }
	fired := make(chan expiry, 1)
	// getppid returns the original parent (1000) until flipped, then 1 (reparented
	// to init), simulating the client process dying.
	var ppid atomic.Int64
	ppid.Store(1000)
	w := newTestParentWatchdog(10*time.Millisecond, 1000, func() int { return int(ppid.Load()) }, func(start, now int) { fired <- expiry{start, now} })
	go w.run()

	// Still the original parent: must not fire.
	select {
	case <-fired:
		t.Fatal("watchdog fired while the parent was unchanged")
	case <-time.After(80 * time.Millisecond):
	}

	// Parent dies -> reparented; watchdog must fire, reporting the original and
	// current parent PID for logging.
	ppid.Store(1)
	select {
	case e := <-fired:
		if e.start != 1000 || e.now != 1 {
			t.Fatalf("onExpire(start=%d, now=%d), want (1000, 1)", e.start, e.now)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("watchdog did not fire after the parent changed")
	}
}

func TestParentWatchdog_StableParentNeverFires(t *testing.T) {
	fired := make(chan struct{}, 1)
	w := newTestParentWatchdog(10*time.Millisecond, 1000, func() int { return 1000 }, func(_, _ int) { fired <- struct{}{} })
	go w.run()
	select {
	case <-fired:
		t.Fatal("watchdog fired despite a stable parent PID")
	case <-time.After(150 * time.Millisecond):
	}
}

func TestParentWatchdog_DisabledNeverFires(t *testing.T) {
	fired := make(chan struct{}, 1)
	// interval <= 0 disables the watchdog even though the parent has "changed".
	w := newTestParentWatchdog(0, 1000, func() int { return 1 }, func(_, _ int) { fired <- struct{}{} })
	go w.run() // returns immediately
	select {
	case <-fired:
		t.Fatal("a disabled (interval<=0) watchdog must never fire")
	case <-time.After(150 * time.Millisecond):
	}
}

// TestNewParentWatchdog_CapturesCurrentParent verifies the production
// constructor snapshots the real parent PID and wires os.Getppid, so a freshly
// constructed watchdog observes "no change" against the live parent.
func TestNewParentWatchdog_CapturesCurrentParent(t *testing.T) {
	w := newParentWatchdog(time.Second, func(_, _ int) {})
	if w.getppid == nil {
		t.Fatal("getppid must be wired to os.Getppid")
	}
	if w.start != w.getppid() {
		t.Fatalf("start (%d) should equal the current parent PID (%d) at construction", w.start, w.getppid())
	}
}
