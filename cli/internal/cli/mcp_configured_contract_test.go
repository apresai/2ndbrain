package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// The `2nb mcp configured --json` envelope is a consumer contract: the Obsidian
// plugin decodes it as a JSON array of {client, configured, scope, ...} to
// decide whether to show the "Copy MCP snippet" hint. These tests exercise the
// cobra handler + JSON output end to end (not just the configuredFromFile core
// unit-tested in internal/mcp), the way the plugin invokes it.
//
// newContractVault redirects $HOME to a temp dir, so writing .claude.json there
// drives mcp.Configured's os.UserHomeDir()-based lookup without touching the
// developer's real config.

func writeClaudeConfig(t *testing.T, content string) {
	t.Helper()
	home := os.Getenv("HOME")
	if home == "" {
		t.Fatal("HOME not set; newContractVault should have set it")
	}
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte(content), 0o644); err != nil {
		t.Fatalf("write .claude.json: %v", err)
	}
}

type configuredStatusJSON struct {
	Client     string `json:"client"`
	ConfigPath string `json:"config_path"`
	Configured bool   `json:"configured"`
	Scope      string `json:"scope"`
	ServerKey  string `json:"server_key"`
	VaultPath  string `json:"vault_path"`
}

func TestContract_MCPConfigured_JSONArrayShape(t *testing.T) {
	_, root := newContractVault(t)

	// A user-scope 2ndbrain server pinned at this vault via --vault: the real
	// `claude mcp add` shape. Must report configured for this vault.
	writeClaudeConfig(t, `{"mcpServers": {"2ndbrain": {"command": "2nb", "args": ["mcp-server", "--vault", "`+root+`"]}}}`)

	out, err := runCLIArgs(t, root, "mcp", "configured", "--json", "--porcelain")
	if err != nil {
		t.Fatalf("mcp configured: %v\n%s", err, out)
	}

	// The contract is a JSON ARRAY (slice-of-one today), not a bare object.
	var statuses []configuredStatusJSON
	if err := json.Unmarshal(out, &statuses); err != nil {
		t.Fatalf("mcp configured --json is not a JSON array: %v\n%s", err, out)
	}
	if len(statuses) != 1 {
		t.Fatalf("got %d statuses, want exactly 1: %+v", len(statuses), statuses)
	}
	st := statuses[0]
	if st.Client != "claude-code" {
		t.Errorf("client = %q, want claude-code", st.Client)
	}
	if !st.Configured {
		t.Errorf("configured = false, want true for a --vault pin to this vault: %+v", st)
	}
	if st.Scope != "user" {
		t.Errorf("scope = %q, want user", st.Scope)
	}
	if st.ServerKey != "2ndbrain" {
		t.Errorf("server_key = %q, want 2ndbrain", st.ServerKey)
	}
	if st.VaultPath != root {
		t.Errorf("vault_path = %q, want %q", st.VaultPath, root)
	}
}

func TestContract_MCPConfigured_NotConfigured(t *testing.T) {
	_, root := newContractVault(t)

	// Config present but with no 2ndbrain server: configured must be false, and
	// the array shape must still hold so the plugin can parse it.
	writeClaudeConfig(t, `{"mcpServers": {"context7": {"command": "npx", "args": ["-y", "@upstash/context7-mcp"]}}}`)

	out, err := runCLIArgs(t, root, "mcp", "configured", "--json", "--porcelain")
	if err != nil {
		t.Fatalf("mcp configured: %v\n%s", err, out)
	}
	var statuses []configuredStatusJSON
	if err := json.Unmarshal(out, &statuses); err != nil {
		t.Fatalf("not a JSON array: %v\n%s", err, out)
	}
	if len(statuses) != 1 {
		t.Fatalf("got %d statuses, want 1", len(statuses))
	}
	if statuses[0].Configured {
		t.Errorf("configured = true, want false (no 2ndbrain server): %+v", statuses[0])
	}
}

func TestContract_MCPConfigured_VaultPinnedElsewhere(t *testing.T) {
	_, root := newContractVault(t)

	// A 2ndbrain server pinned to a DIFFERENT vault via --vault must not report
	// configured for this one. This is the false-positive the hardening fixed.
	writeClaudeConfig(t, `{"mcpServers": {"2ndbrain": {"command": "2nb", "args": ["mcp-server", "--vault", "/some/other/vault"]}}}`)

	out, err := runCLIArgs(t, root, "mcp", "configured", "--json", "--porcelain")
	if err != nil {
		t.Fatalf("mcp configured: %v\n%s", err, out)
	}
	var statuses []configuredStatusJSON
	if err := json.Unmarshal(out, &statuses); err != nil {
		t.Fatalf("not a JSON array: %v\n%s", err, out)
	}
	if statuses[0].Configured {
		t.Errorf("configured = true, want false (pinned to a different vault): %+v", statuses[0])
	}
}
