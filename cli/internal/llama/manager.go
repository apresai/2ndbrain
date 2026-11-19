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
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/apresai/2ndbrain/internal/procutil"
)

const (
	// restartBackoff is the pause before restarting a crashed role process.
	restartBackoff = 2 * time.Second
	// HealthTimeout bounds the initial /health wait after a role starts (a cold
	// GGUF load can take tens of seconds). Exported so internal/ai derives the
	// llama-local probe worst case (ai.LocalWorstCase) from the real bound
	// instead of a copied number.
	HealthTimeout = 90 * time.Second
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

// Manager is the supervisor invoked as `2nb ai engine serve` (under launchd or
// spawned on-demand). It starts and monitors one llama-server process per role,
// restarts a crashed child, and maintains the sidecar registry. When configured
// with an idleTimeout, it auto-terminates after inactivity to free Metal GPU RAM.
type Manager struct {
	enginePath  string
	stateDir    string
	idleTimeout time.Duration
}

// SetIdleTimeout configures an inactivity timeout after which supervised roles
// auto-terminate to free GPU memory.
func (m *Manager) SetIdleTimeout(d time.Duration) {
	m.idleTimeout = d
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

// Serve starts every role in specs and blocks until ctx is cancelled or all
// roles finish (e.g. on idle auto-shutdown), keeping each role process alive
// across crashes. On shutdown it removes the sidecar files.
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

	rolesDone := make(chan struct{})
	go func() {
		wg.Wait()
		close(rolesDone)
	}()

	select {
	case <-rolesDone:
		return nil
	case <-ctx.Done():
		wg.Wait()
		return nil
	}
}

// superviseRole runs one role, restarting it with a backoff until ctx is done or
// the role is stopped due to inactivity (idleTimeout).
func (m *Manager) superviseRole(ctx context.Context, spec RoleSpec) {
	defer m.removeSidecar(spec.Role)
	defer m.removeActivity(spec.Role)
	defer func() {
		_ = os.Remove(filepath.Join(m.stateDir, string(spec.Role)+".sup"))
	}()
	for {
		if ctx.Err() != nil {
			return
		}
		var idleTimedOut atomic.Bool
		roleCtx, cancelRole := context.WithCancel(ctx)
		if m.idleTimeout > 0 {
			TouchRoleActivity(spec.Role)
			go m.watchIdle(roleCtx, spec.Role, func() {
				idleTimedOut.Store(true)
				cancelRole()
			})
		}
		err := m.runRole(roleCtx, spec)
		cancelRole()
		if err != nil && ctx.Err() == nil && !idleTimedOut.Load() {
			slog.Warn("llama role exited", "role", spec.Role, "err", err)
		}
		// If role was stopped due to idle timeout, do not restart
		if idleTimedOut.Load() {
			slog.Info("llama role auto-shutdown complete (idle)", "role", spec.Role)
			return
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

func (m *Manager) watchIdle(ctx context.Context, role Role, onIdle func()) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			actFile := filepath.Join(m.stateDir, string(role)+".activity")
			info, err := os.Stat(actFile)
			var lastActive time.Time
			if err == nil {
				lastActive = info.ModTime()
			} else {
				lastActive = time.Now()
			}
			if time.Since(lastActive) >= m.idleTimeout {
				slog.Info("llama role idle timeout reached; terminating", "role", role, "idle", time.Since(lastActive))
				onIdle()
				return
			}
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
		args = append(args, "--embeddings", "--pooling", "mean", "--ctx-size", "2048", "-b", "2048", "-ub", "2048")
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

func (m *Manager) removeActivity(role Role) {
	_ = os.Remove(filepath.Join(m.stateDir, string(role)+".activity"))
}

// DefaultIdleTimeout is the inactivity period after which an on-demand engine shuts down.
const DefaultIdleTimeout = 10 * time.Minute

// TouchRoleActivity updates the last activity timestamp for a role.
func TouchRoleActivity(role Role) {
	dir, err := StateDir()
	if err != nil {
		return
	}
	_ = os.MkdirAll(dir, 0o755)
	p := filepath.Join(dir, string(role)+".activity")
	now := time.Now()
	if err := os.Chtimes(p, now, now); err != nil {
		_ = os.WriteFile(p, []byte(now.Format(time.RFC3339)), 0o644)
	}
}

// ResolveModelPath finds the cached GGUF model path for a role and model identifier.
func ResolveModelPath(role Role, modelID string) (string, error) {
	if modelID == "" {
		switch role {
		case RoleEmbed:
			modelID = DefaultEmbedModelID
		case RoleGen:
			modelID = "gemma4-e2b"
		case RoleRerank:
			modelID = "bge-reranker-v2-m3"
		}
	}
	if filepath.IsAbs(modelID) || strings.HasSuffix(modelID, ".gguf") {
		if info, err := os.Stat(modelID); err == nil && !info.IsDir() {
			return modelID, nil
		}
	}
	art, ok := ArtifactFor(modelID)
	if !ok {
		return "", fmt.Errorf("unknown model id %q", modelID)
	}
	p, err := ModelPath(art.ID, art.File)
	if err != nil {
		return "", err
	}
	return p, nil
}

// RoleCanStart reports whether the engine binary and required model weights exist on disk.
func RoleCanStart(role Role, modelID string) bool {
	if LocateEngine("") == "" {
		return false
	}
	path, err := ResolveModelPath(role, modelID)
	if err != nil || path == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && info.Size() > 0
}

// roleMatchesModel reports whether a running role serves the wanted model. An
// empty wanted path or an empty recorded model means "unknown", which matches
// (never kill a server we cannot compare against).
func roleMatchesModel(st RoleStatus, wantModelPath string) bool {
	if wantModelPath == "" || st.Model == "" {
		return true
	}
	return filepath.Clean(st.Model) == filepath.Clean(wantModelPath)
}

// EnsureEndpointFor guarantees a healthy endpoint for the specified role
// serving the requested model. If the role server is already running healthy
// with that model, its endpoint is returned immediately. If it is running a
// DIFFERENT model, it is stopped (hot-swap) and a new supervisor is spawned.
// Otherwise, an on-demand detached supervisor is spawned (protected by a per-role flock),
// and EnsureEndpointFor waits for /health 200 before returning.
func EnsureEndpointFor(ctx context.Context, role Role, modelID string) (string, error) {
	wantModelPath, _ := ResolveModelPath(role, modelID)
	if st, ok := ReadRoleStatus(role); ok && roleMatchesModel(st, wantModelPath) && probeHealth(ctx, st.Port) {
		TouchRoleActivity(role)
		return endpointURL(st.Port), nil
	}
	if os.Getenv("TWONB_SKIP_ENGINE_AUTOSTART") == "1" {
		return "", fmt.Errorf("local %s engine not running (autostart disabled via TWONB_SKIP_ENGINE_AUTOSTART)", role)
	}

	stateDir, err := StateDir()
	if err != nil {
		return "", fmt.Errorf("resolve state dir: %w", err)
	}
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return "", fmt.Errorf("create state dir: %w", err)
	}

	// Advisory file lock per role to prevent concurrent spawn races
	lockPath := filepath.Join(stateDir, string(role)+".lock")
	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return "", fmt.Errorf("open lock %s: %w", lockPath, err)
	}
	defer lockFile.Close()

	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX); err != nil {
		return "", fmt.Errorf("lock %s: %w", lockPath, err)
	}
	defer syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)

	// Re-check after acquiring lock in case another process already started it
	// (with the right model), or a different model is still being served.
	if st, ok := ReadRoleStatus(role); ok {
		if roleMatchesModel(st, wantModelPath) {
			if probeHealth(ctx, st.Port) {
				TouchRoleActivity(role)
				return endpointURL(st.Port), nil
			}
		} else {
			// Hot-swap: the role serves a different model. Stop it and wait for
			// the old llama-server to exit before spawning the new supervisor.
			_ = StopRole(role)
			swapDeadline := time.Now().Add(2 * time.Second)
			for pidAlive(st.PID) && time.Now().Before(swapDeadline) {
				time.Sleep(50 * time.Millisecond)
			}
		}
	}

	// Clean up stale sidecar if PID is dead
	if st, ok := ReadRoleStatus(role); ok && !pidAlive(st.PID) {
		_ = os.Remove(filepath.Join(stateDir, string(role)+".json"))
	}

	// Check if a supervisor process is already running for this role
	supPath := filepath.Join(stateDir, string(role)+".sup")
	var supPID int
	if data, err := os.ReadFile(supPath); err == nil {
		if pid, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil && pid > 0 {
			if procutil.Alive(pid) {
				supPID = pid
			} else {
				_ = os.Remove(supPath)
			}
		}
	}

	// If no live supervisor exists, spawn one
	if supPID == 0 {
		enginePath := LocateEngine("")
		if enginePath == "" {
			return "", fmt.Errorf("llama-server engine binary not found (bundle it or check PATH)")
		}

		modelPath, err := ResolveModelPath(role, modelID)
		if err != nil {
			return "", fmt.Errorf("resolve model for %s: %w", role, err)
		}
		if info, err := os.Stat(modelPath); err != nil || info.Size() == 0 {
			return "", fmt.Errorf("model weights %q not found or empty (run: 2nb ai engine pull %s)", modelPath, modelID)
		}

		// Spawn detached supervisor
		cliBin := LocateCLI()
		var cmd *exec.Cmd
		if cliBin != "" {
			var modelFlag string
			switch role {
			case RoleEmbed:
				modelFlag = "--embed-model"
			case RoleGen:
				modelFlag = "--gen-model"
			case RoleRerank:
				modelFlag = "--rerank-model"
			}
			args := []string{"ai", "engine", "serve", modelFlag, modelPath, "--idle-timeout", "10m"}
			if enginePath != "" {
				args = append(args, "--engine-path", enginePath)
			}
			cmd = exec.Command(cliBin, args...)
		} else {
			port, err := freePort()
			if err != nil {
				return "", fmt.Errorf("pick port: %w", err)
			}
			spec := RoleSpec{Role: role, ModelPath: modelPath}
			args := buildArgs(spec, port)
			cmd = exec.Command(enginePath, args...)
		}

		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

		// Redirect stdout/stderr to stateDir/engine.log
		logFile, err := os.OpenFile(filepath.Join(stateDir, "engine.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err == nil {
			cmd.Stdout = logFile
			cmd.Stderr = logFile
			defer logFile.Close()
		}

		if err := cmd.Start(); err != nil {
			return "", fmt.Errorf("spawn on-demand %s engine: %w", role, err)
		}
		supPID = cmd.Process.Pid
		_ = os.WriteFile(supPath, []byte(strconv.Itoa(supPID)), 0o644)
	}

	TouchRoleActivity(role)

	deadline := time.Now().Add(HealthTimeout)
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}

		// Fail fast if supervisor process terminated unexpectedly
		if supPID > 0 && !procutil.Alive(supPID) {
			return "", fmt.Errorf("supervisor process (PID %d) for role %s terminated unexpectedly", supPID, role)
		}

		if st, ok := ReadRoleStatus(role); ok {
			if probeHealth(ctx, st.Port) {
				TouchRoleActivity(role)
				return endpointURL(st.Port), nil
			}
		}
		select {
		case <-time.After(healthPollInterval):
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}

	return "", fmt.Errorf("timed out waiting for on-demand %s engine to become healthy", role)
}

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
	deadline := time.Now().Add(HealthTimeout)
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
// The probe lives in internal/procutil, its one home; this name stays so the
// call sites in this file read unchanged.
func pidAlive(pid int) bool {
	return procutil.Alive(pid)
}

// StopRole terminates any running supervisor and engine process for a role.
func StopRole(role Role) error {
	dir, err := StateDir()
	if err != nil {
		return err
	}
	// Check supervisor PID
	supPath := filepath.Join(dir, string(role)+".sup")
	if data, err := os.ReadFile(supPath); err == nil {
		if pid, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil && pid > 0 {
			if procutil.Alive(pid) {
				_ = syscall.Kill(pid, syscall.SIGTERM)
			}
		}
		_ = os.Remove(supPath)
	}

	// Check engine child PID from sidecar
	if st, ok := ReadRoleStatus(role); ok {
		if st.PID > 0 && procutil.Alive(st.PID) {
			_ = syscall.Kill(st.PID, syscall.SIGTERM)
		}
	}
	_ = os.Remove(filepath.Join(dir, string(role)+".json"))
	_ = os.Remove(filepath.Join(dir, string(role)+".activity"))
	return nil
}

// StopAll terminates running processes for all known roles.
func StopAll() error {
	for _, role := range AllRoles {
		_ = StopRole(role)
	}
	return nil
}
