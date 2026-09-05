// Package procutil answers one question about a process id, in one place.
package procutil

import (
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
func Alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}
