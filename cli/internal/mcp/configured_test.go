package mcp

import (
	"os"
	"path/filepath"
	"testing"
)

// writeConfig writes content to a temp .claude.json and returns its path.
func writeConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, ".claude.json")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func TestConfiguredFromFile(t *testing.T) {
	const vaultPath = "/Users/test/dev/obsidian"

	tests := []struct {
		name       string
		config     string
		wantConfig bool
		wantScope  string
		wantKey    string
	}{
		{
			name:       "no mcp servers at all",
			config:     `{"mcpServers": {}}`,
			wantConfig: false,
		},
		{
			name:       "other servers but no 2ndbrain",
			config:     `{"mcpServers": {"context7": {"command": "npx", "args": ["-y", "@upstash/context7-mcp"]}}}`,
			wantConfig: false,
		},
		{
			name:       "top-level 2ndbrain key (user scope)",
			config:     `{"mcpServers": {"2ndbrain": {"command": "2nb", "args": ["mcp-server"]}}}`,
			wantConfig: true,
			wantScope:  "user",
			wantKey:    "2ndbrain",
		},
		{
			name: "project scope under vault path",
			config: `{"projects": {"/Users/test/dev/obsidian": {"mcpServers": {"2ndbrain": {"command": "2nb", "args": ["mcp-server"], "cwd": "/Users/test/dev/obsidian"}}}}}`,
			wantConfig: true,
			wantScope:  "project",
			wantKey:    "2ndbrain",
		},
		{
			name:       "renamed key matched by command + args",
			config:     `{"mcpServers": {"my-notes": {"command": "2nb", "args": ["mcp-server"]}}}`,
			wantConfig: true,
			wantScope:  "user",
			wantKey:    "my-notes",
		},
		{
			name:       "renamed key matched by command + cwd to this vault",
			config:     `{"mcpServers": {"kb": {"command": "/usr/local/bin/2nb", "args": ["mcp-server"], "cwd": "/Users/test/dev/obsidian"}}}`,
			wantConfig: true,
			wantScope:  "user",
			wantKey:    "kb",
		},
		{
			// A user-scope server that runs `2nb mcp-server` matches on args
			// regardless of cwd: it's a vault-agnostic server the client points
			// at whatever directory it launches in.
			name:       "2nb mcp-server matches on args even with cwd to another vault",
			config:     `{"mcpServers": {"other": {"command": "2nb", "args": ["mcp-server"], "cwd": "/Users/test/dev/other-vault"}}}`,
			wantConfig: true,
			wantScope:  "user",
			wantKey:    "other",
		},
		{
			// The cwd branch in isolation: command is 2nb but args do NOT name
			// mcp-server and the key isn't 2ndbrain/2nb, so the only way to match
			// is cwd. With cwd pointing at a different vault, it must NOT match.
			name:       "2nb command with cwd to a different vault and no mcp-server arg is not configured",
			config:     `{"mcpServers": {"weird": {"command": "2nb", "args": ["something-else"], "cwd": "/Users/test/dev/other-vault"}}}`,
			wantConfig: false,
		},
		{
			// Same shape, but cwd matches THIS vault: the cwd branch alone makes
			// it configured.
			name:       "2nb command matched by cwd alone when cwd is this vault",
			config:     `{"mcpServers": {"weird": {"command": "2nb", "args": ["something-else"], "cwd": "/Users/test/dev/obsidian"}}}`,
			wantConfig: true,
			wantScope:  "user",
			wantKey:    "weird",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := writeConfig(t, tc.config)
			st := configuredFromFile(path, vaultPath)

			if st.Configured != tc.wantConfig {
				t.Fatalf("Configured = %v, want %v", st.Configured, tc.wantConfig)
			}
			if st.Client != "claude-code" {
				t.Errorf("Client = %q, want claude-code", st.Client)
			}
			if st.VaultPath != vaultPath {
				t.Errorf("VaultPath = %q, want %q", st.VaultPath, vaultPath)
			}
			if tc.wantConfig {
				if st.Scope != tc.wantScope {
					t.Errorf("Scope = %q, want %q", st.Scope, tc.wantScope)
				}
				if st.ServerKey != tc.wantKey {
					t.Errorf("ServerKey = %q, want %q", st.ServerKey, tc.wantKey)
				}
			}
		})
	}
}

func TestConfiguredFromFile_MissingFile(t *testing.T) {
	st := configuredFromFile(filepath.Join(t.TempDir(), "does-not-exist.json"), "/Users/test/vault")
	if st.Configured {
		t.Errorf("missing file should report not configured")
	}
	if st.Client != "claude-code" {
		t.Errorf("Client = %q, want claude-code", st.Client)
	}
}

func TestConfiguredFromFile_MalformedJSON(t *testing.T) {
	path := writeConfig(t, `{ this is not valid json `)
	st := configuredFromFile(path, "/Users/test/vault")
	if st.Configured {
		t.Errorf("malformed JSON should report not configured, not crash")
	}
}

// TestConfiguredFromFile_ProjectScopeOnlyMatchesOwnVault confirms that a
// project-scoped server under vault A does not register as configured for
// vault B.
func TestConfiguredFromFile_ProjectScopeOnlyMatchesOwnVault(t *testing.T) {
	config := `{"projects": {"/Users/test/dev/vault-a": {"mcpServers": {"2ndbrain": {"command": "2nb", "args": ["mcp-server"]}}}}}`
	path := writeConfig(t, config)

	st := configuredFromFile(path, "/Users/test/dev/vault-b")
	if st.Configured {
		t.Errorf("vault-b should not see vault-a's project-scoped server as configured")
	}
}
