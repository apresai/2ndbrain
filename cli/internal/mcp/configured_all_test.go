package mcp

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/apresai/2ndbrain/internal/vault"
)

func writeFileT(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestConfiguredAll_ReturnsAllClients(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	v, err := vault.Init(t.TempDir())
	if err != nil {
		t.Fatalf("init vault: %v", err)
	}
	defer v.Close()

	all := ConfiguredAll(v)
	if len(all) != len(SupportedClients()) {
		t.Fatalf("ConfiguredAll returned %d, want %d", len(all), len(SupportedClients()))
	}
	for i, st := range all {
		if st.Client != SupportedClients()[i] {
			t.Errorf("status[%d].Client = %q, want %q", i, st.Client, SupportedClients()[i])
		}
		if st.Configured {
			t.Errorf("%s should be not-configured on a fresh HOME: %+v", st.Client, st)
		}
	}
}

func TestConfiguredFor_FlatClients(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	v, err := vault.Init(t.TempDir())
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	defer v.Close()

	for _, tc := range []struct{ client, dotDir string }{{"warp", ".warp"}, {"agents", ".agents"}} {
		writeFileT(t, filepath.Join(home, tc.dotDir, ".mcp.json"),
			`{"mcpServers": {"2ndbrain": {"command": "2nb", "args": ["mcp-server", "--vault", "`+v.Root+`"], "working_directory": "`+v.Root+`"}}}`)
		st := ConfiguredFor(v, tc.client)
		if !st.Configured || st.Client != tc.client || st.Scope != "user" {
			t.Errorf("%s should be configured: %+v", tc.client, st)
		}
	}
}

func TestConfiguredFor_CodexHeaderScan(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	v, err := vault.Init(t.TempDir())
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	defer v.Close()
	codexPath := filepath.Join(home, ".codex", "config.toml")

	writeFileT(t, codexPath, "[mcp_servers.other]\ncommand = \"x\"\n")
	if ConfiguredFor(v, "codex").Configured {
		t.Error("codex should be not-configured without a 2ndbrain table")
	}
	writeFileT(t, codexPath, "[mcp_servers.2ndbrain]\ncommand = \"2nb\"\n")
	if !ConfiguredFor(v, "codex").Configured {
		t.Error("codex should be configured with a bare [mcp_servers.2ndbrain] header")
	}
	writeFileT(t, codexPath, "[mcp_servers.\"2ndbrain\"]\ncommand = \"2nb\"\n")
	if !ConfiguredFor(v, "codex").Configured {
		t.Error("codex should be configured with a quoted table header")
	}
}

func TestConfiguredFor_ClaudeDesktopConfigured(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
		t.Skip("Claude Desktop config path is only defined on macOS/Windows")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	if runtime.GOOS == "windows" {
		t.Setenv("APPDATA", filepath.Join(home, "AppData", "Roaming"))
	}
	v, err := vault.Init(t.TempDir())
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	defer v.Close()

	cfgPath, _, perr := claudeDesktopConfigPath()
	if perr != nil {
		t.Fatalf("config path: %v", perr)
	}
	writeFileT(t, cfgPath, `{"mcpServers": {"2ndbrain": {"command": "/abs/2nb", "args": ["mcp-server", "--vault", "`+v.Root+`"]}}}`)

	st := ConfiguredFor(v, "claude-desktop")
	if !st.Configured || st.Client != "claude-desktop" || st.Scope != "user" {
		t.Errorf("claude-desktop should be configured: %+v", st)
	}
}

func TestConfiguredFor_ClaudeDesktopUnsupportedOS(t *testing.T) {
	if runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
		t.Skip("this exercises the unsupported-OS path")
	}
	v, err := vault.Init(t.TempDir())
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	defer v.Close()
	st := ConfiguredFor(v, "claude-desktop")
	if st.Configured {
		t.Errorf("claude-desktop must report not-configured on %s, not error", runtime.GOOS)
	}
}
