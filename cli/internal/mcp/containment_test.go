package mcp

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// An untrusted MCP write tool must not be redirected outside the vault by an
// in-vault symlink. kb_create's `path` subdir is the reachable surface for this.
func TestUsageMCP_CreateThroughSymlinkRefused(t *testing.T) {
	h, v := makeHandlers(t)
	ctx := context.Background()

	external := t.TempDir()
	link := filepath.Join(v.Root, "escape")
	if err := os.Symlink(external, link); err != nil {
		t.Skipf("symlinks unsupported on this platform: %v", err)
	}

	res, err := h.handleKBCreate(ctx, makeRequest(map[string]any{
		"title": "Escapee",
		"type":  "note",
		"path":  "escape", // an in-vault symlink that resolves outside the vault
	}))
	if err != nil {
		t.Fatalf("unexpected hard error: %v", err)
	}
	if res == nil || !res.IsError {
		t.Fatalf("kb_create through an in-vault symlink should be refused, got: %s", resultText(t, res))
	}
	// And nothing was written into the external target.
	if entries, _ := os.ReadDir(external); len(entries) != 0 {
		t.Errorf("a file was written outside the vault via the symlink: %v", entries)
	}
}
