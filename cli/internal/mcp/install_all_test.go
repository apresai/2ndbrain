package mcp

import (
	"errors"
	"runtime"
	"testing"

	"github.com/apresai/2ndbrain/internal/vault"
)

// stubCodexAbsent makes codex degrade (no real `codex` exec) for these tests.
func stubCodexAbsent(t *testing.T) {
	t.Helper()
	orig := codexLookPath
	codexLookPath = func(string) (string, error) { return "", errors.New("not found") }
	t.Cleanup(func() { codexLookPath = orig })
}

func resultByClient(results []InstallResult, client string) (InstallResult, bool) {
	for _, r := range results {
		if r.Client == client {
			return r, true
		}
	}
	return InstallResult{}, false
}

func TestInstallAll_AllClients(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
		t.Skip("asserts claude-desktop configured, which requires a supported OS")
	}
	t.Setenv("HOME", t.TempDir())
	stubDesktopLookPath(t, "/abs/2nb")
	stubCodexAbsent(t)

	v, err := vault.Init(t.TempDir())
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	defer v.Close()

	results := InstallAll(v, "2nb", "user", false)
	if len(results) != len(SupportedClients()) {
		t.Fatalf("InstallAll returned %d, want %d", len(results), len(SupportedClients()))
	}
	for _, client := range []string{"claude-code", "warp", "agents", "claude-desktop"} {
		r, ok := resultByClient(results, client)
		if !ok || !r.Configured || r.Error != "" {
			t.Errorf("%s should be configured without error: %+v", client, r)
		}
	}
	// codex is absent -> degraded with Instructions, not an error.
	cx, _ := resultByClient(results, "codex")
	if cx.Error != "" || cx.Instructions == "" {
		t.Errorf("codex (absent) should carry Instructions and no error: %+v", cx)
	}
}

// One client's failure is captured in its Error and does NOT abort the rest.
func TestInstallAll_OneFailureDoesNotAbort(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	stubCodexAbsent(t)
	// Make absolute-path resolution fail so claude-desktop (and codex's pre-check)
	// error, while claude-code/warp/agents (bare "2nb") still succeed.
	origLook, origExec := desktopLookPath, osExecutable
	desktopLookPath = func(string) (string, error) { return "", errors.New("no path") }
	osExecutable = func() (string, error) { return "", errors.New("no exe") }
	t.Cleanup(func() { desktopLookPath, osExecutable = origLook, origExec })

	v, err := vault.Init(t.TempDir())
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	defer v.Close()

	results := InstallAll(v, "2nb", "user", false)
	if len(results) != len(SupportedClients()) {
		t.Fatalf("InstallAll returned %d, want all clients despite a failure", len(results))
	}
	for _, client := range []string{"claude-code", "warp", "agents"} {
		r, _ := resultByClient(results, client)
		if !r.Configured || r.Error != "" {
			t.Errorf("%s should still succeed when another client fails: %+v", client, r)
		}
	}
	cd, _ := resultByClient(results, "claude-desktop")
	if cd.Error == "" {
		t.Errorf("claude-desktop should capture its resolve error: %+v", cd)
	}
}

// UninstallAll returns every client and removes the entries that InstallAll wrote.
func TestUninstallAll(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
		t.Skip("claude-desktop step requires a supported OS")
	}
	t.Setenv("HOME", t.TempDir())
	stubDesktopLookPath(t, "/abs/2nb")
	stubCodexAbsent(t)

	v, err := vault.Init(t.TempDir())
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	defer v.Close()

	InstallAll(v, "2nb", "user", false)
	results := UninstallAll(v, "user", false)
	if len(results) != len(SupportedClients()) {
		t.Fatalf("UninstallAll returned %d, want %d", len(results), len(SupportedClients()))
	}
	// The JSON clients had an entry, so uninstall reports a change and no error.
	for _, client := range []string{"claude-code", "warp", "agents", "claude-desktop"} {
		r, _ := resultByClient(results, client)
		if r.Error != "" {
			t.Errorf("%s uninstall errored: %+v", client, r)
		}
	}
	// All entries are gone: a follow-up ConfiguredAll reports none configured.
	for _, st := range ConfiguredAll(v) {
		if st.Configured {
			t.Errorf("%s still configured after UninstallAll: %+v", st.Client, st)
		}
	}
}
