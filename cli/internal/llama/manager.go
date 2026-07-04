package llama

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"
)

const (
	// restartBackoff is the pause before restarting a crashed role process.
	restartBackoff = 2 * time.Second
	// healthTimeout bounds the initial /health wait after a role starts (a cold
	// GGUF load can take tens of seconds).
	healthTimeout = 90 * time.Second
	// healthPollInterval is how often the initial /health wait retries.
	healthPollInterval = 500 * time.Millisecond
)

// RoleStatus is the sidecar record for one running llama-server role, written to
// StateDir()/<role>.json. The registry is machine-scoped (one engine serves all
// vaults), so any 2nb process — CLI, MCP server, or the app — can discover and
// reuse a warm server by reading it and probing /health.
type RoleStatus struct {
	Role      Role      `json:"role"`
	PID       int       `json:"pid"`
	Port      int       `json:"port"`
	Model     string    `json:"model"`
	StartedAt time.Time `json:"started_at"`
	// Healthy is filled live by Status()/EndpointFor via a /health probe; it is
	// not persisted meaningfully (the on-disk value is a best-effort hint).
	Healthy bool `json:"healthy"`
}

// RoleSpec tells the supervisor which model to load for a role and any extra
// llama-server flags. The CLI builds these from vault config + the model cache.
type RoleSpec struct {
	Role      Role
	ModelPath string
	ExtraArgs []string
}

// Manager is the always-on supervisor invoked as `2nb ai engine serve` under a
// launchd user agent. It starts and monitors one llama-server process per role,
// restarts a crashed child, and maintains the sidecar registry. There is no
// idle timeout: models stay resident until the agent is stopped.
type Manager struct {
	enginePath string
	stateDir   string
}

// NewManager resolves the engine binary (honoring an override) and the state
// directory. It errors if no llama-server can be located.
func NewManager(engineOverride string) (*Manager, error) {
	enginePath := LocateEngine(engineOverride)
	if enginePath == "" {
		return nil, fmt.Errorf("llama-server engine not found (bundle it, run `2nb ai engine pull`, or install it on PATH)")
	}
	dir, err := StateDir()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &Manager{enginePath: enginePath, stateDir: dir}, nil
}

// Serve starts every role in specs and blocks until ctx is cancelled, keeping
// each role process alive across crashes. On shutdown it removes the sidecar
// files. Intended to be the foreground process launchd keeps running.
func (m *Manager) Serve(ctx context.Context, specs []RoleSpec) error {
	if len(specs) == 0 {
		return fmt.Errorf("no roles to serve")
	}
	var wg sync.WaitGroup
	for _, spec := range specs {
		wg.Add(1)
		go func(spec RoleSpec) {
			defer wg.Done()
			m.superviseRole(ctx, spec)
		}(spec)
	}
	<-ctx.Done()
	wg.Wait()
	return nil
}

// superviseRole runs one role, restarting it with a backoff until ctx is done.
func (m *Manager) superviseRole(ctx context.Context, spec RoleSpec) {
	defer m.removeSidecar(spec.Role)
	for {
		if ctx.Err() != nil {
			return
		}
		if err := m.runRole(ctx, spec); err != nil && ctx.Err() == nil {
			slog.Warn("llama role exited", "role", spec.Role, "err", err)
		}
		if ctx.Err() != nil {
			return
		}
		select {
		case <-time.After(restartBackoff):
		case <-ctx.Done():
			return
		}
	}
}

// runRole starts one llama-server process, records its sidecar, waits for it to
// become healthy (best-effort), and blocks until it exits.
func (m *Manager) runRole(ctx context.Context, spec RoleSpec) error {
	port, err := freePort()
	if err != nil {
		return fmt.Errorf("pick port: %w", err)
	}
	args := buildArgs(spec, port)

	cmd := exec.CommandContext(ctx, m.enginePath, args...)
	// Forward the child's stdout/stderr to ours so a crashing llama-server's
	// diagnostics (bad GGUF, OOM, bad flag) reach the launchd log — otherwise
	// os/exec routes them to /dev/null and only "exit status N" survives.
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	// Shut a child down with SIGTERM (graceful) rather than the default SIGKILL
	// when ctx is cancelled, giving llama-server a moment to release the GPU.
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = 5 * time.Second

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start %s: %w", spec.Role, err)
	}
	if err := m.writeSidecar(RoleStatus{
		Role:      spec.Role,
		PID:       cmd.Process.Pid,
		Port:      port,
		Model:     spec.ModelPath,
		StartedAt: time.Now().UTC(),
	}); err != nil {
		slog.Warn("llama sidecar write failed", "role", spec.Role, "err", err)
	}

	// Readiness watcher: log both outcomes so a role that starts but never
	// becomes healthy (loads forever, wrong flags, port stolen) is visible in
	// the log instead of silently sitting there while the sidecar says "running".
	go func() {
		if waitHealthy(ctx, port) {
			slog.Info("llama role healthy", "role", spec.Role, "port", port, "model", spec.ModelPath)
		} else if ctx.Err() == nil {
			slog.Warn("llama role never became healthy within the load window",
				"role", spec.Role, "port", port, "model", spec.ModelPath)
		}
	}()

	return cmd.Wait()
}

// buildArgs assembles the llama-server command line for a role.
func buildArgs(spec RoleSpec, port int) []string {
	args := []string{
		"-m", spec.ModelPath,
		"--host", "127.0.0.1",
		"--port", strconv.Itoa(port),
	}
	switch spec.Role {
	case RoleEmbed:
		args = append(args, "--embeddings", "--pooling", "mean")
	case RoleRerank:
		args = append(args, "--reranking", "--pooling", "rank")
	}
	if HostSupportsMetal() {
		args = append(args, "-ngl", "999") // offload all layers to the GPU
	}
	return append(args, spec.ExtraArgs...)
}

// ---- sidecar registry (read/write) ----

func (m *Manager) sidecarPath(role Role) string {
	return filepath.Join(m.stateDir, string(role)+".json")
}

func (m *Manager) writeSidecar(st RoleStatus) error {
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	path := m.sidecarPath(st.Role)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (m *Manager) removeSidecar(role Role) { _ = os.Remove(m.sidecarPath(role)) }

// ReadRoleStatus reads the sidecar for one role, returning ok=false when it is
// absent or its process is no longer alive (guarding against PID reuse via a
// signal-0 liveness probe). Client-side read; does not require a Manager.
func ReadRoleStatus(role Role) (RoleStatus, bool) {
	dir, err := StateDir()
	if err != nil {
		return RoleStatus{}, false
	}
	data, err := os.ReadFile(filepath.Join(dir, string(role)+".json"))
	if err != nil {
		return RoleStatus{}, false
	}
	var st RoleStatus
	if err := json.Unmarshal(data, &st); err != nil {
		return RoleStatus{}, false
	}
	if !pidAlive(st.PID) {
		return RoleStatus{}, false
	}
	return st, true
}

// EndpointFor returns the localhost base URL of a healthy role server, or
// ok=false when the role is not running or not (yet) healthy. This is what the
// llama-local provider calls to find its endpoint at request time.
func EndpointFor(ctx context.Context, role Role) (string, bool) {
	st, ok := ReadRoleStatus(role)
	if !ok {
		return "", false
	}
	base := endpointURL(st.Port)
	if !probeHealth(ctx, st.Port) {
		return "", false
	}
	return base, true
}

// EngineStatus is the aggregate health of the local engine.
type EngineStatus struct {
	EnginePath  string       `json:"engine_path"`
	AgentLoaded bool         `json:"agent_loaded"`
	Roles       []RoleStatus `json:"roles"`
}

// Status reports the engine binary path, whether the launchd agent is loaded,
// and each role's live health. Safe to call from any process.
func Status(ctx context.Context) EngineStatus {
	es := EngineStatus{
		EnginePath:  LocateEngine(""),
		AgentLoaded: AgentLoaded(),
	}
	for _, role := range AllRoles {
		st, ok := ReadRoleStatus(role)
		if !ok {
			continue
		}
		st.Healthy = probeHealth(ctx, st.Port)
		es.Roles = append(es.Roles, st)
	}
	return es
}

// ---- health + ports ----

func endpointURL(port int) string { return fmt.Sprintf("http://127.0.0.1:%d", port) }

// probeHealth returns true when llama-server's /health returns 200 (the model
// is loaded and ready).
func probeHealth(ctx context.Context, port int) bool {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpointURL(port)+"/health", nil)
	if err != nil {
		return false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// waitHealthy polls /health until ready, the deadline passes, or ctx is done.
func waitHealthy(ctx context.Context, port int) bool {
	deadline := time.Now().Add(healthTimeout)
	for time.Now().Before(deadline) {
		if probeHealth(ctx, port) {
			return true
		}
		select {
		case <-time.After(healthPollInterval):
		case <-ctx.Done():
			return false
		}
	}
	return false
}

// freePort asks the OS for an unused localhost TCP port. There is a small race
// between closing the listener and llama-server binding, acceptable because the
// server binds immediately on start.
func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// pidAlive reports whether a process with the given PID exists (signal 0 probe).
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
