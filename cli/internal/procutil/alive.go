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
// WINDOWS: this always reports false, and callers must not read that as "dead".
// os.Process.Signal refuses every signal but Kill there (syscall.EWINDOWS), so
// there is no signal-0 probe to make. Answering honestly needs OpenProcess plus
// GetExitCodeProcess, which is not written here because it cannot be exercised
// on the machines this is developed and released from, and an unverified syscall
// path is worse than a documented gap. The product is macOS-only today (Homebrew
// formula and cask), and the one Windows-reachable consumer, the MCP sidecar
// reaper, deletes status files for pids this calls dead.
func Alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = proc.Signal(syscall.Signal(0))
	if err == nil {
		return true
	}
	// EPERM is an ALIVE answer, not a dead one: the kernel found the process and
	// then refused us permission to signal it. Reading it as dead fails OPEN on
	// every caller that guards on liveness, which is precisely the Obsidian
	// register-types guard. It is reachable whenever the pid belongs to another
	// user: a shared or NFS home directory, or a lock left behind by a pid that
	// has since been reused by a privileged process. Chromium's own singleton
	// code makes the same distinction, treating anything but ESRCH as alive.
	if errors.Is(err, syscall.EPERM) {
		return true
	}
	return false
}
