package mcp

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/apresai/2ndbrain/internal/vault"
)

// procCommand returns the command line of a running process. It's the basis of
// the reap identity guard (below) and is a package var so tests can substitute
// a fake without spawning a real 2nb mcp-server. The default shells `ps`, which
// is present and accepts `-o command=` on both macOS and Linux.
var procCommand = func(pid int) (string, error) {
	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "command=").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// looksLikeMCPServer reports whether the process at pid is, by its command line,
// a 2nb mcp-server. This is the guard against PID reuse by an UNRELATED process:
// a stale sidecar (left by a server that crashed without cleanup) can have its
// PID recycled by a foreign process, which ListStatuses then resurrects as
// "alive" — but that process never rewrote our sidecar, so the StartedAt check
// alone can't catch it. Requiring both "mcp-server" and a 2nb/2ndbrain token
// biases toward false-negatives (don't reap) over false-positives (kill the
// wrong process); a missed orphan is harmless (the parent-death watchdog or a
// later reap gets it), a wrong kill is not.
func looksLikeMCPServer(pid int) bool {
	cmd, err := procCommand(pid)
	if err != nil {
		return false
	}
	lower := strings.ToLower(cmd)
	return strings.Contains(lower, "mcp-server") &&
		(strings.Contains(lower, "2nb") || strings.Contains(lower, "2ndbrain"))
}

// ReapResult is the JSON payload of `2nb mcp reap`.
type ReapResult struct {
	Reaped    []ReapedServer  `json:"reaped"`
	Skipped   []SkippedServer `json:"skipped"`
	Threshold string          `json:"threshold"`
	DryRun    bool            `json:"dry_run"`
}

type ReapedServer struct {
	PID            int       `json:"pid"`
	Age            string    `json:"age"`             // time since last activity
	LastInvocation time.Time `json:"last_invocation"` // zero if the server never served a tool
}

type SkippedServer struct {
	PID    int    `json:"pid"`
	Reason string `json:"reason"`
}

// StaleServers partitions the live mcp-server sidecars into those stale enough
// to reap (last activity — or start time, if it never served a tool — older
// than olderThan) and those to skip (the current process, and any server with
// recent activity). The age threshold is the ONLY real liveness signal: an
// active session has a recent LastInvocation, so it's never reaped. now/selfPID
// are injected for testability.
func StaleServers(v *vault.Vault, olderThan time.Duration, now time.Time, selfPID int) (stale []ServerStatus, skipped []SkippedServer, err error) {
	statuses, err := ListStatuses(v) // already drops dead PIDs + their sidecars
	if err != nil {
		return nil, nil, err
	}
	for _, st := range statuses {
		if st.PID == selfPID {
			skipped = append(skipped, SkippedServer{PID: st.PID, Reason: "current process"})
			continue
		}
		last := serverLastActivity(st)
		age := now.Sub(last)
		if age < olderThan {
			skipped = append(skipped, SkippedServer{PID: st.PID, Reason: fmt.Sprintf("active (last activity %s ago)", age.Round(time.Second))})
			continue
		}
		// Identity guard against PID reuse by an UNRELATED process: a stale
		// sidecar (left by a server that crashed without cleanup) can have its
		// PID recycled by a foreign process, which ListStatuses then resurrects
		// as "alive". Confirm the live PID is actually a 2nb mcp-server before
		// treating it as reapable, so we never signal someone else's process.
		if !looksLikeMCPServer(st.PID) {
			skipped = append(skipped, SkippedServer{PID: st.PID, Reason: "PID is not a 2nb mcp-server (likely reused since the server exited)"})
			continue
		}
		stale = append(stale, st)
	}
	return stale, skipped, nil
}

// serverLastActivity is a server's last tool invocation, or its start time if it
// has never served a tool.
func serverLastActivity(st ServerStatus) time.Time {
	if !st.LastInvocation.IsZero() {
		return st.LastInvocation
	}
	return st.StartedAt
}

// Reap terminates stale/orphaned mcp-server processes for the vault. It sends
// SIGTERM only (never SIGKILL): the 2nb mcp-server handles SIGTERM in a separate
// goroutine — removing its sidecar and exiting cleanly — even while ServeStdio
// blocks on stdin, so SIGTERM is reliable for our own servers. Avoiding SIGKILL
// sidesteps the one real hazard (a PID recycled between the liveness check and
// the kill). Before signaling, it re-reads the sidecar and confirms StartedAt is
// unchanged, so a PID reused since enumeration is not signaled. --dry-run lists
// what would be reaped without signaling anything.
func Reap(v *vault.Vault, olderThan time.Duration, dryRun bool) (ReapResult, error) {
	now := time.Now()
	stale, skipped, err := StaleServers(v, olderThan, now, os.Getpid())
	if err != nil {
		return ReapResult{}, err
	}

	res := ReapResult{Threshold: olderThan.String(), DryRun: dryRun, Skipped: skipped}
	for _, st := range stale {
		entry := ReapedServer{PID: st.PID, Age: now.Sub(serverLastActivity(st)).Round(time.Second).String(), LastInvocation: st.LastInvocation}
		if dryRun {
			res.Reaped = append(res.Reaped, entry)
			continue
		}
		if ok, reason := terminateServer(v, st); ok {
			res.Reaped = append(res.Reaped, entry)
		} else {
			res.Skipped = append(res.Skipped, SkippedServer{PID: st.PID, Reason: reason})
		}
	}
	return res, nil
}

// terminateServer SIGTERMs one server and waits up to ~3s for it to exit,
// cleaning up its sidecar on success. Two identity guards run before the signal:
// re-reading the sidecar and confirming StartedAt is unchanged catches a PID
// re-taken by ANOTHER 2nb server (which rewrites the sidecar) between
// enumeration and the signal; reuse by a FOREIGN process is already filtered out
// upstream by StaleServers' looksLikeMCPServer check.
func terminateServer(v *vault.Vault, st ServerStatus) (bool, string) {
	cur, err := readServerStatus(v, st.PID)
	if err != nil || !cur.StartedAt.Equal(st.StartedAt) {
		return false, "sidecar vanished or changed (PID re-taken by another server) before signal"
	}
	proc, err := os.FindProcess(st.PID)
	if err != nil {
		return false, "process not found"
	}
	if err := proc.Signal(syscall.SIGTERM); err != nil {
		return false, "SIGTERM failed: " + err.Error()
	}
	for i := 0; i < 30; i++ {
		time.Sleep(100 * time.Millisecond)
		if !pidAlive(st.PID) {
			_ = os.Remove(serverStatusPath(v, st.PID))
			return true, ""
		}
	}
	return false, "still running 3s after SIGTERM"
}

func serverStatusPath(v *vault.Vault, pid int) string {
	return filepath.Join(v.DotDir, "mcp", fmt.Sprintf("%d.json", pid))
}

func readServerStatus(v *vault.Vault, pid int) (ServerStatus, error) {
	var st ServerStatus
	data, err := os.ReadFile(serverStatusPath(v, pid))
	if err != nil {
		return st, err
	}
	return st, json.Unmarshal(data, &st)
}
