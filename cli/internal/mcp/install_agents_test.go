package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/apresai/2ndbrain/internal/vault"
)

// The "agents" client writes the cross-tool ~/.agents/.mcp.json (which Warp also
// auto-reads), with the same flat mcpServers map + dual vault pinning as warp.
func TestInstallForClient_Agents(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	v, err := vault.Init(t.TempDir())
	if err != nil {
		t.Fatalf("init vault: %v", err)
	}
	defer v.Close()

	res, err := InstallForClient(v, "agents", "2nb", "user", false)
	if err != nil {
		t.Fatalf("InstallForClient agents: %v", err)
	}
	if !res.Changed || res.Client != "agents" || res.ConfigPath != "~/.agents/.mcp.json" {
		t.Fatalf("unexpected result: %+v", res)
	}

	cfgPath := filepath.Join(home, ".agents", ".mcp.json")
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read agents config: %v", err)
	}
	var parsed struct {
		MCPServers map[string]mcpServerEntry `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("parse agents config: %v", err)
	}
	entry, ok := parsed.MCPServers[serverKeyName]
	if !ok {
		t.Fatalf("agents config missing the %q server: %s", serverKeyName, data)
	}
	if entry.WorkingDirectory != v.Root {
		t.Errorf("working_directory = %q, want the vault root %q", entry.WorkingDirectory, v.Root)
	}
	var sawVaultFlag bool
	for i, a := range entry.Args {
		if a == "--vault" && i+1 < len(entry.Args) && entry.Args[i+1] == v.Root {
			sawVaultFlag = true
		}
	}
	if !sawVaultFlag {
		t.Errorf("args should pin --vault %q, got %v", v.Root, entry.Args)
	}

	// Idempotent.
	res2, err := InstallForClient(v, "agents", "2nb", "user", false)
	if err != nil {
		t.Fatalf("second install: %v", err)
	}
	if res2.Changed {
		t.Errorf("second identical agents install should be a no-op")
	}

	// Uninstall removes the entry.
	if _, err := UninstallForClient(v, "agents", "user", false); err != nil {
		t.Fatalf("UninstallForClient agents: %v", err)
	}
	data, _ = os.ReadFile(cfgPath)
	var after struct {
		MCPServers map[string]mcpServerEntry `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &after); err != nil {
		t.Fatalf("parse agents config after uninstall: %v", err)
	}
	if _, ok := after.MCPServers[serverKeyName]; ok {
		t.Errorf("entry should be removed after uninstall: %s", data)
	}
}

// Project scope writes <vault>/.agents/.mcp.json, not the home dir.
func TestInstallForClient_AgentsProjectScope(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	v, err := vault.Init(t.TempDir())
	if err != nil {
		t.Fatalf("init vault: %v", err)
	}
	defer v.Close()

	res, err := InstallForClient(v, "agents", "2nb", "project", false)
	if err != nil {
		t.Fatalf("InstallForClient agents project: %v", err)
	}
	if !res.Changed || res.Client != "agents" {
		t.Fatalf("unexpected result: %+v", res)
	}
	if _, err := os.Stat(filepath.Join(v.Root, ".agents", ".mcp.json")); err != nil {
		t.Fatalf("project-scope agents config not written under the vault: %v", err)
	}
}
