package mcp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstall_PreservesUnrelatedKeys(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, ".claude.json")
	vaultRoot := t.TempDir()
	// A realistic config: a large integer (must survive byte-for-byte, no float
	// reformat), an unrelated object, and another MCP server.
	orig := `{"numStartups":1234567890123,"oauthAccount":{"id":"abc"},"mcpServers":{"other":{"command":"foo","args":["bar"]}}}`
	if err := os.WriteFile(cfg, []byte(orig), 0o600); err != nil {
		t.Fatal(err)
	}

	res, err := installToFile(cfg, vaultRoot, "2nb", "user", false)
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if !res.Changed || res.BackupPath == "" || !res.Configured {
		t.Fatalf("expected changed+configured+backup, got %+v", res)
	}

	got := readString(t, cfg)
	if !strings.Contains(got, "1234567890123") {
		t.Errorf("large int was reformatted or lost:\n%s", got)
	}
	if !strings.Contains(got, `"oauthAccount"`) || !strings.Contains(got, `"numStartups"`) {
		t.Errorf("unrelated top-level keys dropped:\n%s", got)
	}
	if !strings.Contains(got, `"other"`) {
		t.Errorf("unrelated mcp server dropped:\n%s", got)
	}
	if !strings.Contains(got, `"2ndbrain"`) || !strings.Contains(got, vaultRoot) {
		t.Errorf("2ndbrain entry not written for this vault:\n%s", got)
	}
	// The backup is the original bytes.
	if readString(t, res.BackupPath) != orig {
		t.Errorf("backup should equal the original config")
	}
}

func TestInstall_Idempotent(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, ".claude.json")
	vaultRoot := t.TempDir()

	if _, err := installToFile(cfg, vaultRoot, "2nb", "user", false); err != nil {
		t.Fatalf("first install: %v", err)
	}
	res, err := installToFile(cfg, vaultRoot, "2nb", "user", false)
	if err != nil {
		t.Fatalf("second install: %v", err)
	}
	if res.Changed {
		t.Errorf("second identical install should be a no-op (changed=false)")
	}
}

func TestInstall_RefusesMalformedConfig(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, ".claude.json")
	bad := `{ this is not valid json `
	if err := os.WriteFile(cfg, []byte(bad), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := installToFile(cfg, t.TempDir(), "2nb", "user", false); err == nil {
		t.Fatal("install must refuse a malformed config")
	}
	if readString(t, cfg) != bad {
		t.Errorf("a refused install must not modify the file")
	}
}

func TestInstall_CreatesMissingConfig(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), ".claude.json")
	vaultRoot := t.TempDir()
	res, err := installToFile(cfg, vaultRoot, "2nb", "user", false)
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if !res.Changed || res.BackupPath != "" {
		t.Errorf("creating a fresh config: changed=true, no backup; got %+v", res)
	}
	if !strings.Contains(readString(t, cfg), `"2ndbrain"`) {
		t.Errorf("fresh config should contain the 2ndbrain entry")
	}
}

func TestInstall_DryRunDoesNotWrite(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), ".claude.json")
	res, err := installToFile(cfg, t.TempDir(), "2nb", "user", true)
	if err != nil {
		t.Fatalf("dry-run: %v", err)
	}
	if !res.Changed {
		t.Errorf("dry-run should report it WOULD change")
	}
	if _, err := os.Stat(cfg); !os.IsNotExist(err) {
		t.Errorf("dry-run must not create the config file")
	}
}

func TestInstallThenConfiguredAgree(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), ".claude.json")
	vaultRoot := t.TempDir()
	if _, err := installToFile(cfg, vaultRoot, "2nb", "user", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	st := configuredFromFile(cfg, vaultRoot)
	if !st.Configured || st.Scope != "user" || st.ServerKey != "2ndbrain" {
		t.Errorf("configured should agree with install: %+v", st)
	}
}

func TestUninstall_RemovesOnly2ndbrain(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, ".claude.json")
	vaultRoot := t.TempDir()
	orig := `{"mcpServers":{"other":{"command":"foo"}}}`
	if err := os.WriteFile(cfg, []byte(orig), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := installToFile(cfg, vaultRoot, "2nb", "user", false); err != nil {
		t.Fatalf("install: %v", err)
	}

	res, err := uninstallFromFile(cfg, vaultRoot, "user", false)
	if err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if !res.Changed {
		t.Errorf("uninstall should report a change")
	}
	got := readString(t, cfg)
	if strings.Contains(got, `"2ndbrain"`) {
		t.Errorf("2ndbrain entry should be removed:\n%s", got)
	}
	if !strings.Contains(got, `"other"`) {
		t.Errorf("the unrelated server must be preserved:\n%s", got)
	}
}

func TestInstall_ProjectScope(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), ".claude.json")
	vaultRoot := t.TempDir()
	if _, err := installToFile(cfg, vaultRoot, "2nb", "project", false); err != nil {
		t.Fatalf("install project: %v", err)
	}
	got := readString(t, cfg)
	if !strings.Contains(got, `"projects"`) || !strings.Contains(got, vaultRoot) {
		t.Errorf("project-scope entry should be keyed under projects[<vault>]:\n%s", got)
	}
	st := configuredFromFile(cfg, vaultRoot)
	if !st.Configured || st.Scope != "project" {
		t.Errorf("configured should report project scope: %+v", st)
	}
}

func readString(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}
