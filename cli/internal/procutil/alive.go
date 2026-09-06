// Package procutil answers one question about a process id, in one place.
package procutil

import (
	"errors"
	"os"
	"syscall"
)

// Alive reports whether a process with the given PID is still running.
//
// On Unix, signal 0 is the standard "does this process exist" probe: the kernel
// runs its permission and existence checks and delivers nothing.
//
// It is deliberately the ONLY copy. The same twelve lines lived unexported in
// internal/mcp (for reaping orphaned MCP servers) and in internal/llama (for the
// local engine child), and a third caller in internal/vault (deciding whether
// Obsidian is holding a vault) would have made three. Nothing here is subtle
// enough for the copies to disagree today, which is exactly when consolidating
// is cheap.
//
// A true answer means the PID is live, NOT that it is the process the caller has
// in mind: PIDs are reused. A caller that would do something destructive on the
// strength of it must confirm identity separately (internal/mcp does, with
// looksLikeMCPServer). A caller that only wants to know whether some process is
// there, and fails safe either way, can use this alone.
//
// 2nb ships for macOS only, so signal 0 is always available here.
func Alive(pid int) bool {
	alive, _ := AliveState(pid)
	return alive
}

// AliveState is Alive plus how firm the answer is, for a caller that reports
// what it knows rather than only acting on it.
//
// certain is false in exactly one case: the process EXISTS but belongs to
// another user, so the kernel refused the signal with EPERM. That is an alive
// answer (reading it as dead fails OPEN on any liveness guard, which is
// precisely the Obsidian register-types guard, and it is reachable through a
// shared home directory or a pid since reused by a privileged process), but it
// is not the same fact as "this is the process I was asking about". Chromium's
// own singleton code draws the line the same way, treating anything but ESRCH
// as alive.
//
// A caller that only needs to act can use Alive and get the safe reading. A
// caller that PRINTS its conclusion should use this, so it does not assert
// "Obsidian is running" about a process it could not identify.
func AliveState(pid int) (alive, certain bool) {
	if pid <= 0 {
		return false, true
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false, true
	}
	switch err = proc.Signal(syscall.Signal(0)); {
	case err == nil:
		return true, true
	case errors.Is(err, syscall.EPERM):
		return true, false
	default:
		return false, true
	}
}
