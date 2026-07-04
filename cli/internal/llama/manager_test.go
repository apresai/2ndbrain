package llama

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
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

func TestBuildArgsPerRole(t *testing.T) {
	embed := buildArgs(RoleSpec{Role: RoleEmbed, ModelPath: "/m/e.gguf"}, 9001)
	if !containsArg(embed, "--embeddings") || !containsArg(embed, "--pooling") {
		t.Errorf("embed args missing --embeddings/--pooling: %v", embed)
	}
	if containsArg(embed, "--reranking") {
		t.Errorf("embed args must not enable reranking: %v", embed)
	}

	rerank := buildArgs(RoleSpec{Role: RoleRerank, ModelPath: "/m/r.gguf"}, 9002)
	if !containsArg(rerank, "--reranking") {
		t.Errorf("rerank args missing --reranking: %v", rerank)
	}
	if containsArg(rerank, "--embeddings") {
		t.Errorf("rerank and embeddings are mutually exclusive: %v", rerank)
	}

	gen := buildArgs(RoleSpec{Role: RoleGen, ModelPath: "/m/g.gguf", ExtraArgs: []string{"--ctx-size", "8192"}}, 9003)
	if containsArg(gen, "--embeddings") || containsArg(gen, "--reranking") {
		t.Errorf("gen must not set embed/rerank flags: %v", gen)
	}
	if !containsArg(gen, "-m") || !containsArg(gen, "/m/g.gguf") || !containsArg(gen, "9003") {
		t.Errorf("gen args missing model/port: %v", gen)
	}
	if !containsArg(gen, "--ctx-size") || !containsArg(gen, "8192") {
		t.Errorf("gen args dropped ExtraArgs: %v", gen)
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
