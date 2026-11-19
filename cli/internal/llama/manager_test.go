package llama

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

func containsArg(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}

func containsArgPair(args []string, flag, val string) bool {
	for i := 0; i < len(args)-1; i++ {
		if args[i] == flag && args[i+1] == val {
			return true
		}
	}
	return false
}

// flagValue finds the value immediately following flag in args.
// Returns ("", false) if flag is absent or has no following argument.
func flagValue(args []string, flag string) (string, bool) {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1], true
		}
	}
	return "", false
}

func assertFlagValue(t *testing.T, args []string, flag, want string) {
	t.Helper()
	got, ok := flagValue(args, flag)
	if !ok {
		t.Errorf("args %v missing flag %q", args, flag)
		return
	}
	if got != want {
		t.Errorf("flag %q = %q, want %q (args: %v)", flag, got, want, args)
	}
}

func TestBuildArgsPerRole(t *testing.T) {
	// RoleEmbed assertions
	embed := buildArgs(RoleSpec{Role: RoleEmbed, ModelPath: "/m/e.gguf"}, 9001)
	if !containsArg(embed, "--embeddings") {
		t.Errorf("embed args missing --embeddings: %v", embed)
	}
	assertFlagValue(t, embed, "-m", "/m/e.gguf")
	assertFlagValue(t, embed, "--host", "127.0.0.1")
	assertFlagValue(t, embed, "--port", "9001")
	assertFlagValue(t, embed, "--pooling", "mean")
	assertFlagValue(t, embed, "--ctx-size", "2048")
	assertFlagValue(t, embed, "-b", "2048")
	assertFlagValue(t, embed, "-ub", "2048")
	if !containsArgPair(embed, "-ub", "2048") || !containsArgPair(embed, "-b", "2048") || !containsArgPair(embed, "--ctx-size", "2048") || !containsArgPair(embed, "--pooling", "mean") {
		t.Errorf("embed args missing flag-value adjacency for -ub, -b, --ctx-size, or --pooling: %v", embed)
	}
	if containsArg(embed, "--reranking") {
		t.Errorf("embed args must not enable reranking: %v", embed)
	}
	if HostSupportsMetal() {
		assertFlagValue(t, embed, "-ngl", "999")
	}

	// RoleRerank assertions
	rerank := buildArgs(RoleSpec{Role: RoleRerank, ModelPath: "/m/r.gguf"}, 9002)
	if !containsArg(rerank, "--reranking") {
		t.Errorf("rerank args missing --reranking: %v", rerank)
	}
	assertFlagValue(t, rerank, "-m", "/m/r.gguf")
	assertFlagValue(t, rerank, "--host", "127.0.0.1")
	assertFlagValue(t, rerank, "--port", "9002")
	assertFlagValue(t, rerank, "--pooling", "rank")
	if !containsArgPair(rerank, "--pooling", "rank") {
		t.Errorf("rerank args missing --pooling rank adjacency: %v", rerank)
	}
	if containsArg(rerank, "--embeddings") || containsArg(rerank, "--ctx-size") || containsArg(rerank, "-ub") {
		t.Errorf("rerank must not contain embed flags: %v", rerank)
	}

	// RoleGen assertions
	gen := buildArgs(RoleSpec{Role: RoleGen, ModelPath: "/m/g.gguf", ExtraArgs: []string{"--ctx-size", "8192"}}, 9003)
	if containsArg(gen, "--embeddings") || containsArg(gen, "--reranking") {
		t.Errorf("gen must not set embed/rerank flags: %v", gen)
	}
	assertFlagValue(t, gen, "-m", "/m/g.gguf")
	assertFlagValue(t, gen, "--host", "127.0.0.1")
	assertFlagValue(t, gen, "--port", "9003")
	assertFlagValue(t, gen, "--ctx-size", "8192")
	if !containsArgPair(gen, "--ctx-size", "8192") {
		t.Errorf("gen args missing --ctx-size 8192 adjacency: %v", gen)
	}
}

func TestFreePort(t *testing.T) {
	p, err := freePort()
	if err != nil {
		t.Fatal(err)
	}
	if p <= 0 || p > 65535 {
		t.Errorf("freePort = %d, want a valid TCP port", p)
	}
}

func TestPidAlive(t *testing.T) {
	if !pidAlive(os.Getpid()) {
		t.Error("current process should be alive")
	}
	if pidAlive(0) || pidAlive(-1) {
		t.Error("non-positive pids are never alive")
	}
}

func newTestManager(t *testing.T) *Manager {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	bin := filepath.Join(t.TempDir(), "llama-server")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	m, err := NewManager(bin)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestSidecarRoundTrip(t *testing.T) {
	m := newTestManager(t)
	st := RoleStatus{Role: RoleEmbed, PID: os.Getpid(), Port: 54321, Model: "/m/e.gguf", StartedAt: time.Now().UTC()}
	if err := m.writeSidecar(st); err != nil {
		t.Fatal(err)
	}

	got, ok := ReadRoleStatus(RoleEmbed)
	if !ok {
		t.Fatal("ReadRoleStatus not ok for a live-pid sidecar")
	}
	if got.PID != st.PID || got.Port != st.Port || got.Model != st.Model {
		t.Errorf("round-trip mismatch: %+v", got)
	}

	// No server is listening, so EndpointFor must report not-healthy.
	if _, ok := EndpointFor(context.Background(), RoleEmbed); ok {
		t.Error("EndpointFor should be false when no server answers /health")
	}

	m.removeSidecar(RoleEmbed)
	if _, ok := ReadRoleStatus(RoleEmbed); ok {
		t.Error("sidecar should be gone after removeSidecar")
	}
}

func TestReadRoleStatusAbsent(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	if _, ok := ReadRoleStatus(RoleGen); ok {
		t.Error("expected not-ok for a missing sidecar")
	}
}

func TestStatusReflectsSidecars(t *testing.T) {
	m := newTestManager(t)
	if err := m.writeSidecar(RoleStatus{Role: RoleGen, PID: os.Getpid(), Port: 5, Model: "/m/g.gguf", StartedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	es := Status(context.Background())
	if len(es.Roles) != 1 || es.Roles[0].Role != RoleGen {
		t.Fatalf("Status roles = %+v, want one gen role", es.Roles)
	}
	if es.Roles[0].Healthy {
		t.Error("role should be reported unhealthy (no server listening)")
	}
}

func TestServeRejectsNoRoles(t *testing.T) {
	m := newTestManager(t)
	if err := m.Serve(context.Background(), nil); err == nil {
		t.Error("Serve should reject an empty role list")
	}
}

func TestResolveModelPathAndRoleCanStart(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	// Without engine binary, RoleCanStart must be false
	origExe := executable
	executable = func() (string, error) { return filepath.Join(t.TempDir(), "nonexistent"), nil }
	t.Cleanup(func() { executable = origExe })

	if RoleCanStart(RoleEmbed, "embeddinggemma-2") {
		t.Error("RoleCanStart should be false when engine binary is absent")
	}

	// ResolveModelPath default
	p, err := ResolveModelPath(RoleEmbed, "")
	if err != nil {
		t.Fatalf("ResolveModelPath default: %v", err)
	}
	if !strings.Contains(p, "embeddinggemma-2") {
		t.Errorf("ResolveModelPath default = %q, want embeddinggemma-2", p)
	}

	// TouchRoleActivity
	TouchRoleActivity(RoleEmbed)
	stDir, _ := StateDir()
	actFile := filepath.Join(stDir, "embed.activity")
	if info, err := os.Stat(actFile); err != nil || info.Size() == 0 {
		t.Errorf("TouchRoleActivity failed to write %s", actFile)
	}
}

func TestManagerSetIdleTimeout(t *testing.T) {
	m := newTestManager(t)
	m.SetIdleTimeout(10 * time.Minute)
	if m.idleTimeout != 10*time.Minute {
		t.Errorf("m.idleTimeout = %v, want 10m", m.idleTimeout)
	}
}

func TestRenderAgentPlist(t *testing.T) {
	plist := renderAgentPlist("/Applications/SecondBrain.app/Contents/Resources/2nb", "/tmp/2nb/engine.log",
		[]string{"--gen-model", "gemma4-e4b"})
	for _, want := range []string{
		AgentLabel,
		"/Applications/SecondBrain.app/Contents/Resources/2nb",
		"<string>ai</string>",
		"<string>engine</string>",
		"<string>serve</string>",
		"<string>--gen-model</string>",
		"<string>gemma4-e4b</string>",
		"/tmp/2nb/engine.log",
		"<key>RunAtLoad</key>",
		"<key>KeepAlive</key>",
	} {
		if !strings.Contains(plist, want) {
			t.Errorf("plist missing %q\n%s", want, plist)
		}
	}
}

func TestXMLEscape(t *testing.T) {
	got := xmlEscape(`a&b<c>d"e'f`)
	want := "a&amp;b&lt;c&gt;d&quot;e&apos;f"
	if got != want {
		t.Errorf("xmlEscape = %q, want %q", got, want)
	}
}

func TestLaunchdNonDarwinInstallErrors(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("darwin: InstallAgent would modify the user's launchd; covered by manual verification")
	}
	if err := InstallAgent("/path/to/2nb", nil); err == nil {
		t.Error("InstallAgent should error on non-darwin")
	}
	if AgentLoaded() {
		t.Error("AgentLoaded should be false on non-darwin")
	}
}

func TestEnsureEndpointFor_FailsFastOnDeadSupervisor(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	stDir, err := StateDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(stDir, 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("true")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	deadPID := cmd.Process.Pid
	if pidAlive(deadPID) {
		t.Skip("spawned process unexpectedly alive")
	}

	supPath := filepath.Join(stDir, "embed.sup")
	_ = os.WriteFile(supPath, []byte(strconv.Itoa(deadPID)), 0o644)

	// With a non-existent model, EnsureEndpointFor should clean up the stale .sup file
	// and fail before spawning a new supervisor.
	_, err = EnsureEndpointFor(context.Background(), RoleEmbed, "nonexistent-model-weights")
	if err == nil {
		t.Fatal("expected error for nonexistent model")
	}

	// The stale sup file should have been removed
	if _, err := os.Stat(supPath); !os.IsNotExist(err) {
		t.Errorf("stale sup file %s should have been removed", supPath)
	}
}

func TestEnsureEndpointFor_AbortsImmediatelyWhenSupervisorPIDDies(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	stDir, err := StateDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(stDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Spawn a process that sleeps briefly then exits, and reap it in background
	cmd := exec.Command("sleep", "0.2")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	supPID := cmd.Process.Pid
	go func() {
		_ = cmd.Wait()
	}()

	supPath := filepath.Join(stDir, "embed.sup")
	_ = os.WriteFile(supPath, []byte(strconv.Itoa(supPID)), 0o644)

	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err = EnsureEndpointFor(ctx, RoleEmbed, "embeddinggemma-2")
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected error when supervisor dies")
	}
	if !strings.Contains(err.Error(), "terminated unexpectedly") {
		t.Errorf("expected 'terminated unexpectedly' error, got %v", err)
	}
	if elapsed > 3*time.Second {
		t.Errorf("expected fail fast within ~1s, took %v", elapsed)
	}
}

func TestEnsureEndpointFor_ReusesAliveSupervisor(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	stDir, err := StateDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(stDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Use our own PID as a definitely-alive supervisor PID
	supPID := os.Getpid()
	supPath := filepath.Join(stDir, "embed.sup")
	_ = os.WriteFile(supPath, []byte(strconv.Itoa(supPID)), 0o644)

	// Even if model weights or engine binary don't exist, EnsureEndpointFor should NOT attempt
	// to spawn a new supervisor (which would check engine/model existence and fail with
	// "llama-server engine binary not found" or "model weights not found").
	// Instead, it should skip spawning, enter the health check loop, and exit when the context expires.
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err = EnsureEndpointFor(ctx, RoleEmbed, "nonexistent-model")
	if err != context.DeadlineExceeded {
		t.Errorf("expected context.DeadlineExceeded while waiting for existing supervisor, got %v", err)
	}

	// Verify supPath still contains our original supPID
	data, err := os.ReadFile(supPath)
	if err != nil {
		t.Fatalf("failed to read supPath: %v", err)
	}
	if strings.TrimSpace(string(data)) != strconv.Itoa(supPID) {
		t.Errorf("supPath was overwritten: got %q, want %d", string(data), supPID)
	}
}

func TestServe_RolesDoneChannelUnblocks(t *testing.T) {
	m := newTestManager(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := m.Serve(ctx, []RoleSpec{
		{Role: RoleEmbed, ModelPath: "/nonexistent"},
	})
	if err != nil {
		t.Errorf("Serve with cancelled ctx returned error: %v", err)
	}
}

func TestRoleMatchesModel(t *testing.T) {
	tests := []struct {
		name string
		st   RoleStatus
		want string
		ok   bool
	}{
		{"empty wanted path matches anything", RoleStatus{Model: "/m/a.gguf"}, "", true},
		{"empty recorded model matches anything", RoleStatus{}, "/m/a.gguf", true},
		{"identical paths", RoleStatus{Model: "/m/a.gguf"}, "/m/a.gguf", true},
		{"different models", RoleStatus{Model: "/m/embeddinggemma-300m/a.gguf"}, "/m/embeddinggemma-2/b.gguf", false},
		{"cleaned paths normalize", RoleStatus{Model: "/m//x/../a.gguf"}, "/m/./a.gguf", true},
		{"same base, different dir", RoleStatus{Model: "/m/x/a.gguf"}, "/m/y/a.gguf", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := roleMatchesModel(tt.st, tt.want); got != tt.ok {
				t.Errorf("roleMatchesModel(%+v, %q) = %v, want %v", tt.st, tt.want, got, tt.ok)
			}
		})
	}
}
