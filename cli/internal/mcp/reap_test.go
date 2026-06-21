package mcp

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/apresai/2ndbrain/internal/testutil"
	"github.com/apresai/2ndbrain/internal/vault"
)

// startSleep launches a real `sleep` child that responds to SIGTERM. Because the
// child is owned by THIS test process, it would linger as a zombie (and fool the
// signal-0 liveness probe) until reaped; a concurrent Wait() clears it promptly
// when it dies, matching production where the reaped server isn't our child and
// init/launchd reaps it. Returns the child PID.
func startSleep(t *testing.T) int {
	t.Helper()
	cmd := exec.Command("sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start sleep: %v", err)
	}
	pid := cmd.Process.Pid
	waited := make(chan struct{})
	go func() { _ = cmd.Wait(); close(waited) }()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		<-waited
	})
	return pid
}

// fakeMCPServerProc makes looksLikeMCPServer treat every PID as a 2nb
// mcp-server, so a plain `sleep` test child stands in for a real server. Tests
// run sequentially (no t.Parallel), so swapping the package var is safe.
func fakeMCPServerProc(t *testing.T) {
	t.Helper()
	old := procCommand
	procCommand = func(pid int) (string, error) { return "/opt/homebrew/bin/2nb mcp-server --vault /x", nil }
	t.Cleanup(func() { procCommand = old })
}

func writeSidecar(t *testing.T, v *vault.Vault, pid int, started, lastInv time.Time) {
	t.Helper()
	dir := filepath.Join(v.DotDir, "mcp")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir mcp: %v", err)
	}
	st := ServerStatus{PID: pid, StartedAt: started, LastInvocation: lastInv, Invocations: []ToolInvocation{}}
	data, _ := json.MarshalIndent(st, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("%d.json", pid)), data, 0o644); err != nil {
		t.Fatalf("write sidecar: %v", err)
	}
}

func TestStaleServers_Partition(t *testing.T) {
	fakeMCPServerProc(t) // treat the sleep children as real servers
	v := testutil.NewTestVault(t)
	now := time.Now()
	self := os.Getpid()

	writeSidecar(t, v, self, now.Add(-10*time.Hour), now.Add(-10*time.Hour))
	young := startSleep(t)
	writeSidecar(t, v, young, now.Add(-2*time.Hour), now.Add(-1*time.Minute))
	old := startSleep(t)
	writeSidecar(t, v, old, now.Add(-10*time.Hour), now.Add(-7*time.Hour))

	stale, skipped, err := StaleServers(v, 6*time.Hour, now, self)
	if err != nil {
		t.Fatalf("StaleServers: %v", err)
	}
	if len(stale) != 1 || stale[0].PID != old {
		t.Errorf("stale = %+v, want only PID %d (old)", stale, old)
	}
	skippedPIDs := map[int]string{}
	for _, s := range skipped {
		skippedPIDs[s.PID] = s.Reason
	}
	if _, ok := skippedPIDs[self]; !ok {
		t.Errorf("current process %d should be skipped", self)
	}
	if _, ok := skippedPIDs[young]; !ok {
		t.Errorf("young/active server %d should be skipped", young)
	}
}

func TestReap_TerminatesStaleServer(t *testing.T) {
	fakeMCPServerProc(t)
	v := testutil.NewTestVault(t)
	pid := startSleep(t)
	writeSidecar(t, v, pid, time.Now().Add(-10*time.Hour), time.Now().Add(-7*time.Hour))

	res, err := Reap(v, 6*time.Hour, false)
	if err != nil {
		t.Fatalf("Reap: %v", err)
	}
	if res.DryRun {
		t.Error("DryRun should be false")
	}
	if len(res.Reaped) != 1 || res.Reaped[0].PID != pid {
		t.Fatalf("Reaped = %+v, want PID %d", res.Reaped, pid)
	}
	if pidAlive(pid) {
		t.Errorf("server PID %d should be dead after reap", pid)
	}
	if _, err := os.Stat(serverStatusPath(v, pid)); !os.IsNotExist(err) {
		t.Errorf("sidecar for reaped PID %d should be removed", pid)
	}
}

func TestReap_DryRunDoesNotKill(t *testing.T) {
	fakeMCPServerProc(t)
	v := testutil.NewTestVault(t)
	pid := startSleep(t)
	writeSidecar(t, v, pid, time.Now().Add(-10*time.Hour), time.Now().Add(-7*time.Hour))

	res, err := Reap(v, 6*time.Hour, true)
	if err != nil {
		t.Fatalf("Reap dry-run: %v", err)
	}
	if !res.DryRun || len(res.Reaped) != 1 || res.Reaped[0].PID != pid {
		t.Fatalf("dry-run Reaped = %+v (DryRun=%v), want PID %d listed", res.Reaped, res.DryRun, pid)
	}
	if !pidAlive(pid) {
		t.Errorf("dry-run must NOT kill PID %d", pid)
	}
	if _, err := os.Stat(serverStatusPath(v, pid)); err != nil {
		t.Errorf("dry-run must not remove the sidecar: %v", err)
	}
}

// The HIGH guard: a stale sidecar whose PID is now held by an UNRELATED process
// (here a real `sleep`, which is not a 2nb mcp-server) must NOT be signaled. Uses
// the real `ps`-backed procCommand — no fake — so it exercises the actual guard.
func TestReap_SkipsForeignPIDReuse(t *testing.T) {
	v := testutil.NewTestVault(t)
	pid := startSleep(t) // a real, alive, NON-2nb process
	writeSidecar(t, v, pid, time.Now().Add(-10*time.Hour), time.Now().Add(-7*time.Hour))

	res, err := Reap(v, 6*time.Hour, false)
	if err != nil {
		t.Fatalf("Reap: %v", err)
	}
	if len(res.Reaped) != 0 {
		t.Errorf("a foreign (non-2nb) process must not be reaped: %+v", res.Reaped)
	}
	if !pidAlive(pid) {
		t.Errorf("foreign process PID %d must be left alive", pid)
	}
	found := false
	for _, s := range res.Skipped {
		if s.PID == pid {
			found = true
		}
	}
	if !found {
		t.Errorf("foreign PID %d should appear in skipped with a reason", pid)
	}
}

// terminateServer refuses to signal when the on-disk sidecar's StartedAt no
// longer matches the enumerated one (the PID was re-taken by another 2nb server).
func TestTerminateServer_RefusesOnStartedAtMismatch(t *testing.T) {
	fakeMCPServerProc(t)
	v := testutil.NewTestVault(t)
	pid := startSleep(t)
	onDisk := time.Now()
	writeSidecar(t, v, pid, onDisk, onDisk)

	// Caller's record claims a DIFFERENT StartedAt than what's on disk.
	st := ServerStatus{PID: pid, StartedAt: onDisk.Add(-time.Hour)}
	ok, reason := terminateServer(v, st)
	if ok {
		t.Error("should refuse to signal on a StartedAt mismatch")
	}
	if !pidAlive(pid) {
		t.Errorf("process PID %d must not be signaled on mismatch", pid)
	}
	if reason == "" {
		t.Error("a skip reason should be reported")
	}
}

func TestReap_SkipsActiveServer(t *testing.T) {
	fakeMCPServerProc(t)
	v := testutil.NewTestVault(t)
	pid := startSleep(t)
	writeSidecar(t, v, pid, time.Now().Add(-2*time.Hour), time.Now().Add(-1*time.Minute))

	res, err := Reap(v, 6*time.Hour, false)
	if err != nil {
		t.Fatalf("Reap: %v", err)
	}
	if len(res.Reaped) != 0 {
		t.Errorf("an active server must not be reaped: %+v", res.Reaped)
	}
	if !pidAlive(pid) {
		t.Errorf("active server PID %d must still be alive", pid)
	}
}
