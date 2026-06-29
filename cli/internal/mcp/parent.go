package mcp

import (
	"os"
	"time"
)

// defaultParentPollInterval is how often the parent-death watchdog checks
// whether its parent (the MCP client) is still alive. Orphan cleanup is not
// latency-sensitive — a few tens of seconds is far below the multi-day runaway
// it guards against — and the poll is a single getppid() syscall, so this stays
// generous.
const defaultParentPollInterval = 30 * time.Second

// parentWatchdog exits an mcp-server process when its parent process goes away.
//
// On stdio transport the parent IS the MCP client: the editor/agent that spawned
// the server owns its stdin/stdout pipes for the life of the connection. So
// "parent gone" is the precise orphan signal. It catches the case stdin-EOF
// misses — the client process dies WITHOUT cleanly closing the pipe (a crash, a
// SIGKILL, a leaked child) — and it can never fire while a client is connected,
// so it does not interrupt a live-but-quiet session the way the old
// activity-based idle timer did. This is what now reaps the runaway orphan the
// field hit (one server ran 2d9h pinning the WAL); see [idleWatchdog] for the
// superseded approach.
//
// Detection is a portable getppid() poll: when the parent dies the OS reparents
// this process (to launchd / PID 1 on macOS, to init or a subreaper on Linux),
// so os.Getppid() returns something other than the value captured at startup. No
// platform-specific syscall, works on every GOOS.
type parentWatchdog struct {
	start    int                  // parent PID captured at startup
	interval time.Duration        // poll period; <= 0 disables the watchdog
	getppid  func() int           // injectable for tests; os.Getppid in prod
	onExpire func(start, now int) // called once when the parent changes (process exit in prod); receives the original and current parent PID for logging
}

func newParentWatchdog(interval time.Duration, onExpire func(start, now int)) *parentWatchdog {
	return &parentWatchdog{
		start:    os.Getppid(),
		interval: interval,
		getppid:  os.Getppid,
		onExpire: onExpire,
	}
}

// run polls until the parent PID differs from the one captured at construction,
// then calls onExpire exactly once and returns. Safe to launch in its own
// goroutine; onExpire typically calls os.Exit. An interval <= 0 disables the
// watchdog (returns immediately).
func (w *parentWatchdog) run() {
	if w.interval <= 0 {
		return
	}
	for {
		time.Sleep(w.interval)
		if now := w.getppid(); now != w.start {
			w.onExpire(w.start, now)
			return
		}
	}
}
