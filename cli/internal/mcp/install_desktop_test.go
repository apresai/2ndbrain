package mcp

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/apresai/2ndbrain/internal/vault"
)

// stubDesktopLookPath makes resolveAbsCommand deterministic in tests.
func stubDesktopLookPath(t *testing.T, abs string) {
	t.Helper()
	orig := desktopLookPath
	desktopLookPath = func(string) (string, error) { return abs, nil }
	t.Cleanup(func() { desktopLookPath = orig })
}

func TestInstallClaudeDesktop(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
		t.Skip("Claude Desktop config path is only defined on macOS/Windows")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	if runtime.GOOS == "windows" {
		t.Setenv("APPDATA", filepath.Join(home, "AppData", "Roaming"))
	}
	stubDesktopLookPath(t, "/abs/2nb")

	v, err := vault.Init(t.TempDir())
	if err != nil {
		t.Fatalf("init vault: %v", err)
	}
	defer v.Close()

	res, err := InstallForClient(v, "claude-desktop", "", "user", false)
	if err != nil {
		t.Fatalf("install claude-desktop: %v", err)
	}
	if !res.Changed || res.Client != "claude-desktop" || res.Scope != "user" {
		t.Fatalf("unexpected result: %+v", res)
	}

	cfgPath, _, perr := claudeDesktopConfigPath()
	if perr != nil {
		t.Fatalf("config path: %v", perr)
	}
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read desktop config: %v", err)
	}
	// Claude Desktop supports only command/args/env: cwd/working_directory/url
	// must never appear (a url field silently corrupts the file).
	for _, forbidden := range []string{"working_directory", "cwd", "url"} {
		if strings.Contains(string(data), forbidden) {
			t.Errorf("desktop config must not contain %q: %s", forbidden, data)
		}
	}
	var parsed struct {
		MCPServers map[string]mcpServerEntry `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("parse: %v", err)
	}
	entry, ok := parsed.MCPServers[serverKeyName]
	if !ok {
		t.Fatalf("missing server: %s", data)
	}
	if entry.Command != "/abs/2nb" {
		t.Errorf("command = %q, want absolute /abs/2nb", entry.Command)
	}
	var sawVault bool
	for i, a := range entry.Args {
		if a == "--vault" && i+1 < len(entry.Args) && entry.Args[i+1] == v.Root {
			sawVault = true
		}
	}
	if !sawVault {
		t.Errorf("args should pin --vault %q: %v", v.Root, entry.Args)
	}

	// Idempotent: a second identical install is a no-op.
	res2, err := InstallForClient(v, "claude-desktop", "", "user", false)
	if err != nil {
		t.Fatalf("second install: %v", err)
	}
	if res2.Changed {
		t.Errorf("second identical desktop install should be a no-op")
	}

	// Uninstall removes the entry.
	if _, err := UninstallForClient(v, "claude-desktop", "user", false); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	data, _ = os.ReadFile(cfgPath)
	var after struct {
		MCPServers map[string]mcpServerEntry `json:"mcpServers"`
	}
	_ = json.Unmarshal(data, &after)
	if _, ok := after.MCPServers[serverKeyName]; ok {
		t.Errorf("entry should be removed after uninstall: %s", data)
	}
}

// Installing must back up the original and preserve other servers byte-for-byte.
func TestInstallClaudeDesktop_BackupAndPreserve(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
		t.Skip("Claude Desktop config path is only defined on macOS/Windows")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	if runtime.GOOS == "windows" {
		t.Setenv("APPDATA", filepath.Join(home, "AppData", "Roaming"))
	}
	stubDesktopLookPath(t, "/abs/2nb")

	cfgPath, _, _ := claudeDesktopConfigPath()
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatal(err)
	}
	orig := `{"mcpServers": {"other": {"command": "x"}}}`
	if err := os.WriteFile(cfgPath, []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}

	v, err := vault.Init(t.TempDir())
	if err != nil {
		t.Fatalf("init vault: %v", err)
	}
	defer v.Close()

	res, err := InstallForClient(v, "claude-desktop", "", "user", false)
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if res.BackupPath == "" {
		t.Fatal("expected a backup path")
	}
	bak, _ := os.ReadFile(res.BackupPath)
	if string(bak) != orig {
		t.Errorf("backup should hold the original bytes; got %s", bak)
	}
	data, _ := os.ReadFile(cfgPath)
	if !strings.Contains(string(data), `"other"`) || !strings.Contains(string(data), serverKeyName) {
		t.Errorf("config must keep the other server AND add 2ndbrain: %s", data)
	}
}

func TestResolveAbsCommand(t *testing.T) {
	// Explicit absolute path wins.
	if got, err := resolveAbsCommand("/x/2nb"); err != nil || got != "/x/2nb" {
		t.Errorf("explicit abs: got %q, %v", got, err)
	}
	// Bare "2nb" resolves via LookPath.
	origLook := desktopLookPath
	origExec := osExecutable
	t.Cleanup(func() { desktopLookPath = origLook; osExecutable = origExec })

	desktopLookPath = func(string) (string, error) { return "/abs/2nb", nil }
	if got, err := resolveAbsCommand("2nb"); err != nil || got != "/abs/2nb" {
		t.Errorf("lookpath: got %q, %v", got, err)
	}
	// LookPath fails -> fall back to os.Executable.
	desktopLookPath = func(string) (string, error) { return "", errors.New("not found") }
	osExecutable = func() (string, error) { return "/exe/2nb", nil }
	if got, err := resolveAbsCommand(""); err != nil || got != "/exe/2nb" {
		t.Errorf("executable fallback: got %q, %v", got, err)
	}
	// Both fail -> actionable error.
	osExecutable = func() (string, error) { return "", errors.New("nope") }
	if _, err := resolveAbsCommand(""); err == nil {
		t.Error("expected an error when no absolute path can be resolved")
	}
}
