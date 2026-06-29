package mcp

import (
	"context"
	"sync/atomic"
	"time"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// idleWatchdog exits an mcp-server process after a period of no tool activity.
// It is now an OPT-IN activity cap (default off): orphan cleanup — the original
// motivation, after a closed Claude/Kiro session left a server holding a DB
// handle open and pinning the WAL for 2d9h — is owned by [parentWatchdog], which
// keys off the client process dying rather than tool inactivity, so it never
// kills a live-but-quiet connection. Enable this only via --idle-timeout /
// $2NB_MCP_IDLE_TIMEOUT when an inactivity cap is genuinely wanted.
//
// The design is lock-free: an atomic last-activity timestamp and an in-flight
// counter, polled by run(). No timer.Reset / channel-drain races.
//
// Activity is observed by wrap()-ing each tool handler: a request increments
// inFlight on entry and, on completion, bumps lastActive then decrements
// inFlight — in that order, so the activity clock is always fresh before the
// request is considered done. A request in flight blocks expiry regardless of
// the clock.
//
// Known, accepted TOCTOU: a request that arrives in the instant between
// ServeStdio reading it and the handler's inFlight increment can be killed if
// the timeout elapses exactly then. Inherent to idle-exit; stdio clients simply
// respawn the server on the next call. Not worth guarding at 30-minute scale.
type idleWatchdog struct {
	timeout    time.Duration
	lastActive atomic.Int64 // unix nanos of the last completed activity
	inFlight   atomic.Int32
	onExpire   func() // called once when idle past timeout (process exit in prod)
}

func newIdleWatchdog(timeout time.Duration, onExpire func()) *idleWatchdog {
	w := &idleWatchdog{timeout: timeout, onExpire: onExpire}
	w.lastActive.Store(time.Now().UnixNano())
	return w
}

// wrap decorates a tool handler so the watchdog tracks in-flight requests and
// refreshes the activity clock on completion. It is applied OUTERMOST (outside
// the status-writer wrap) so inFlight is only decremented after the inner
// status flush has run — onExpire's sidecar removal can't race a flush.
func (w *idleWatchdog) wrap(fn server.ToolHandlerFunc) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
		w.inFlight.Add(1)
		defer func() {
			w.lastActive.Store(time.Now().UnixNano())
			w.inFlight.Add(-1)
		}()
		return fn(ctx, req)
	}
}

// run polls until the server has been idle (no in-flight request and no
// activity) for at least timeout, then calls onExpire exactly once. A timeout
// <= 0 disables the watchdog (returns immediately). Safe to launch in its own
// goroutine; onExpire typically calls os.Exit.
func (w *idleWatchdog) run() {
	if w.timeout <= 0 {
		return
	}
	tick := w.timeout / 4
	if tick < 10*time.Millisecond {
		tick = 10 * time.Millisecond
	}
	if tick > time.Minute {
		tick = time.Minute
	}
	for {
		time.Sleep(tick)
		if w.inFlight.Load() > 0 {
			continue
		}
		idle := time.Duration(time.Now().UnixNano() - w.lastActive.Load())
		if idle >= w.timeout {
			w.onExpire()
			return
		}
	}
}
