package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/apresai/2ndbrain/internal/vault"
	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const maxInvocations = 50

// ServerStatus is the persistent snapshot of a running MCP server process.
// One file per PID is written to <vault>/.2ndbrain/mcp/<pid>.json so the
// editor (and other processes) can enumerate live servers.
type ServerStatus struct {
	PID            int              `json:"pid"`
	StartedAt      time.Time        `json:"started_at"`
	ParentPID      int              `json:"parent_pid,omitempty"`
	LastInvocation time.Time        `json:"last_invocation,omitempty"`
	Invocations    []ToolInvocation `json:"invocations"`
}

type ToolInvocation struct {
	Tool       string    `json:"tool"`
	Timestamp  time.Time `json:"timestamp"`
	OK         bool      `json:"ok"`
	DurationMs int64     `json:"duration_ms"`
	Error      string    `json:"error,omitempty"`
}

// StatusWriter persists a ServerStatus to disk with atomic writes.
// mark3labs/mcp-go has no client-connected callback, so every tool invocation
// updates the file — it's the only available signal for "this server is alive".
type StatusWriter struct {
	path   string
	mu     sync.Mutex
	status ServerStatus
}

// NewStatusWriter initializes the status file for this process and cleans up
// any stale files left behind by crashed sibling processes.
func NewStatusWriter(v *vault.Vault) (*StatusWriter, error) {
	dir := filepath.Join(v.DotDir, "mcp")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}
	pid := os.Getpid()
	w := &StatusWriter{
		path: filepath.Join(dir, fmt.Sprintf("%d.json", pid)),
		status: ServerStatus{
			PID:         pid,
			StartedAt:   time.Now().UTC(),
			ParentPID:   os.Getppid(),
			Invocations: []ToolInvocation{},
		},
	}
	if err := w.flush(); err != nil {
		return nil, err
	}
	cleanupStale(dir)
	return w, nil
}

// Record appends a tool invocation and re-flushes the status file.
func (w *StatusWriter) Record(tool string, start time.Time, ok bool, errMsg string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	inv := ToolInvocation{
		Tool:       tool,
		Timestamp:  start.UTC(),
		OK:         ok,
		DurationMs: time.Since(start).Milliseconds(),
		Error:      errMsg,
	}
	w.status.Invocations = append(w.status.Invocations, inv)
	if len(w.status.Invocations) > maxInvocations {
		w.status.Invocations = w.status.Invocations[len(w.status.Invocations)-maxInvocations:]
	}
	w.status.LastInvocation = inv.Timestamp
	if err := w.flush(); err != nil {
		slog.Warn("mcp status flush failed", "err", err)
	}
}

// Remove deletes this process's status file. Call on graceful shutdown.
func (w *StatusWriter) Remove() {
	w.mu.Lock()
	defer w.mu.Unlock()
	_ = os.Remove(w.path)
}

func (w *StatusWriter) flush() error {
	data, err := json.MarshalIndent(w.status, "", "  ")
	if err != nil {
		return err
	}
	tmp := w.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, w.path)
}

// Wrap decorates a tool handler so each invocation is recorded.
func (w *StatusWriter) Wrap(name string, fn server.ToolHandlerFunc) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
		start := time.Now()
		result, err := fn(ctx, req)
		errMsg := ""
		if err != nil {
			errMsg = err.Error()
		}
		w.Record(name, start, err == nil, errMsg)
		return result, err
	}
}

// ListStatuses reads all status files in the vault, filtering stale entries
// (files whose PIDs no longer correspond to a live process).
func ListStatuses(v *vault.Vault) ([]ServerStatus, error) {
	dir := filepath.Join(v.DotDir, "mcp")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return []ServerStatus{}, nil
		}
		return []ServerStatus{}, err
	}
	result := []ServerStatus{}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		full := filepath.Join(dir, e.Name())
		data, err := os.ReadFile(full)
		if err != nil {
			continue
		}
		var st ServerStatus
		if err := json.Unmarshal(data, &st); err != nil {
			continue
		}
		if !pidAlive(st.PID) {
			_ = os.Remove(full)
			continue
		}
		result = append(result, st)
	}
	return result, nil
}

// cleanupStale removes any status files whose PIDs are no longer alive.
func cleanupStale(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		full := filepath.Join(dir, e.Name())
		data, err := os.ReadFile(full)
		if err != nil {
			continue
		}
		var st ServerStatus
		if err := json.Unmarshal(data, &st); err != nil {
			_ = os.Remove(full)
			continue
		}
		if !pidAlive(st.PID) {
			_ = os.Remove(full)
		}
	}
}

// pidAlive returns true when a process with the given PID is still running.
// On Unix, signal 0 is the standard "does this process exist" probe.
func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}
