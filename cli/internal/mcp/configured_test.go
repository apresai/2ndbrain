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
			// An unpinned `2nb mcp-server` (no --vault, no cwd) is vault-agnostic:
			// the client resolves the vault at launch, so it counts for any vault.
			name:       "unpinned 2nb mcp-server is vault-agnostic and matches",
			config:     `{"mcpServers": {"other": {"command": "2nb", "args": ["mcp-server"]}}}`,
			wantConfig: true,
			wantScope:  "user",
			wantKey:    "other",
		},
		{
			// The false-positive fix: an entry whose cwd pins a DIFFERENT vault
			// must NOT report configured for this vault, even though its args
			// name mcp-server.
			name:       "2nb mcp-server with cwd to another vault is not configured for this vault",
			config:     `{"mcpServers": {"other": {"command": "2nb", "args": ["mcp-server"], "cwd": "/Users/test/dev/other-vault"}}}`,
			wantConfig: false,
		},
		{
			// The real-world shape (`claude mcp add`): the vault is pinned via a
			// --vault arg, not cwd. Pinned to a DIFFERENT vault → not configured
			// for this one.
			name:       "2nb mcp-server --vault pinned to another vault is not configured",
			config:     `{"mcpServers": {"2ndbrain": {"command": "2nb", "args": ["mcp-server", "--vault", "/Users/test/dev/other-vault"]}}}`,
			wantConfig: false,
		},
		{
			// Same --vault pin, but pointing at THIS vault → configured. This is
			// exactly how the shipped `~/.claude.json` entry is shaped.
			name:       "2nb mcp-server --vault pinned to this vault is configured",
			config:     `{"mcpServers": {"2ndbrain": {"command": "2nb", "args": ["mcp-server", "--vault", "/Users/test/dev/obsidian"]}}}`,
			wantConfig: true,
			wantScope:  "user",
			wantKey:    "2ndbrain",
		},
		{
			// The --vault=<path> equals spelling is honored too.
			name:       "2nb mcp-server --vault=<this> equals form is configured",
			config:     `{"mcpServers": {"kb": {"command": "2nb", "args": ["mcp-server", "--vault=/Users/test/dev/obsidian"]}}}`,
			wantConfig: true,
			wantScope:  "user",
			wantKey:    "kb",
		},
		{
			// A non-conventional key whose command never runs `mcp-server` is not
			// the MCP server, even with cwd pointing at this vault: that's some
			// other 2nb invocation (e.g. `2nb search`), not the server.
			name:       "2nb command without mcp-server arg is not the server even with matching cwd",
			config:     `{"mcpServers": {"weird": {"command": "2nb", "args": ["something-else"], "cwd": "/Users/test/dev/obsidian"}}}`,
			wantConfig: false,
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

// TestConfiguredFromFile_ProjectScopeSymlinkKey proves the symlink false-negative
// fix: when the projects key in ~/.claude.json is a symlinked spelling of the
// vault path (the client launched from the symlink) but the caller passes the
// resolved real path (or vice versa), detection must still match. Uses real
// on-disk dirs + a symlink so EvalSymlinks actually resolves both spellings to
// the same target.
func TestConfiguredFromFile_ProjectScopeSymlinkKey(t *testing.T) {
	base := t.TempDir()
	real := filepath.Join(base, "real-vault")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "linked-vault")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlink unsupported on this platform: %v", err)
	}

	// Config keys the project by the SYMLINK spelling; we query by the REAL
	// resolved path. Pre-fix this was a false negative.
	config := `{"projects": {"` + link + `": {"mcpServers": {"2ndbrain": {"command": "2nb", "args": ["mcp-server"]}}}}}`
	path := writeConfig(t, config)

	st := configuredFromFile(path, real)
	if !st.Configured {
		t.Fatalf("symlinked project key should match the resolved vault path; got not configured")
	}
	if st.Scope != "project" {
		t.Errorf("Scope = %q, want project", st.Scope)
	}

	// And the reverse: query by the symlink spelling, config keyed by the real
	// path. Also must match.
	config2 := `{"projects": {"` + real + `": {"mcpServers": {"2ndbrain": {"command": "2nb", "args": ["mcp-server"]}}}}}`
	path2 := writeConfig(t, config2)
	if st2 := configuredFromFile(path2, link); !st2.Configured {
		t.Errorf("querying by symlink spelling against a real-path config key should match")
	}
}

// TestVaultFlagValue exercises the --vault arg extractor directly: both
// spellings, a dangling flag, and absence.
func TestVaultFlagValue(t *testing.T) {
	cases := []struct {
		name      string
		args      []string
		wantVal   string
		wantFound bool
	}{
		{"space form", []string{"mcp-server", "--vault", "/v"}, "/v", true},
		{"equals form", []string{"mcp-server", "--vault=/v"}, "/v", true},
		{"no vault flag", []string{"mcp-server"}, "", false},
		{"dangling flag", []string{"mcp-server", "--vault"}, "", false},
		{"empty args", nil, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			val, found := vaultFlagValue(tc.args)
			if found != tc.wantFound || val != tc.wantVal {
				t.Errorf("vaultFlagValue(%v) = (%q, %v), want (%q, %v)", tc.args, val, found, tc.wantVal, tc.wantFound)
			}
		})
	}
}
