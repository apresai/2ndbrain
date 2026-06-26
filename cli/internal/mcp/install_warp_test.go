package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/apresai/2ndbrain/internal/vault"
)

func TestInstallForClient_Warp(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	v, err := vault.Init(t.TempDir())
	if err != nil {
		t.Fatalf("init vault: %v", err)
	}
	defer v.Close()

	res, err := InstallForClient(v, "warp", "2nb", "user", false)
	if err != nil {
		t.Fatalf("InstallForClient warp: %v", err)
	}
	if !res.Changed || res.Client != "warp" || res.ConfigPath != "~/.warp/.mcp.json" {
		t.Fatalf("unexpected result: %+v", res)
	}

	cfgPath := filepath.Join(home, ".warp", ".mcp.json")
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read warp config: %v", err)
	}
	var parsed struct {
		MCPServers map[string]mcpServerEntry `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("parse warp config: %v", err)
	}
	entry, ok := parsed.MCPServers[serverKeyName]
	if !ok {
		t.Fatalf("warp config missing the %q server: %s", serverKeyName, data)
	}
	if entry.WorkingDirectory != v.Root {
		t.Errorf("working_directory = %q, want the vault root %q", entry.WorkingDirectory, v.Root)
	}
	// Vault is also pinned via --vault in args (belt-and-suspenders).
	var sawVaultFlag bool
	for i, a := range entry.Args {
		if a == "--vault" && i+1 < len(entry.Args) && entry.Args[i+1] == v.Root {
			sawVaultFlag = true
		}
	}
	if !sawVaultFlag {
		t.Errorf("args should pin --vault %q, got %v", v.Root, entry.Args)
	}

	// Idempotent: a second identical install reports no change.
	res2, err := InstallForClient(v, "warp", "2nb", "user", false)
	if err != nil {
		t.Fatalf("second install: %v", err)
	}
	if res2.Changed {
		t.Errorf("second identical warp install should be a no-op")
	}

	// Uninstall removes the entry.
	if _, err := UninstallForClient(v, "warp", "user", false); err != nil {
		t.Fatalf("UninstallForClient warp: %v", err)
	}
	data, _ = os.ReadFile(cfgPath)
	var after struct {
		MCPServers map[string]mcpServerEntry `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &after); err != nil {
		t.Fatalf("parse warp config after uninstall: %v", err)
	}
	if _, ok := after.MCPServers[serverKeyName]; ok {
		t.Errorf("entry should be removed after uninstall: %s", data)
	}
}

// An unknown client is a clear error, not a silent claude-code write.
func TestInstallForClient_UnknownClient(t *testing.T) {
	v, err := vault.Init(t.TempDir())
	if err != nil {
		t.Fatalf("init vault: %v", err)
	}
	defer v.Close()
	if _, err := InstallForClient(v, "cursor", "2nb", "user", false); err == nil {
		t.Fatalf("expected an error for an unknown client")
	}
}
