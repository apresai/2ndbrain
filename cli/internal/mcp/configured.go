package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/apresai/2ndbrain/internal/vault"
)

// ConfiguredStatus reports whether the 2ndbrain MCP server is wired into an AI
// client's configuration. This is the durable "is it set up?" signal, distinct
// from ServerStatus (which only knows about a server process that is running
// right now). The MCP server is launched on demand by the client as a stdio
// subprocess, so a vault dashboard checking "running" would read red whenever
// the client is closed, even when everything is configured correctly.
//
// v1 understands only Claude Code (~/.claude.json). The slice-of-one shape plus
// the Client field leave room to add Cursor/Kiro/etc. detectors later without
// breaking consumers that decode the JSON array.
type ConfiguredStatus struct {
	Client     string `json:"client"`               // e.g. "claude-code"
	ConfigPath string `json:"config_path"`          // e.g. "~/.claude.json"
	Configured bool   `json:"configured"`           // server found for this vault
	Scope      string `json:"scope,omitempty"`      // "user" | "project" when configured
	ServerKey  string `json:"server_key,omitempty"` // the matched mcpServers key
	VaultPath  string `json:"vault_path"`           // vault the check was scoped to
}

// claudeConfig is the subset of ~/.claude.json the detector reads. Anthropic
// owns this file's schema; unknown keys are ignored and a missing/garbled file
// degrades to "not configured" rather than an error.
type claudeConfig struct {
	MCPServers map[string]mcpServerEntry `json:"mcpServers"`
	Projects   map[string]struct {
		MCPServers map[string]mcpServerEntry `json:"mcpServers"`
	} `json:"projects"`
}

type mcpServerEntry struct {
	Command string   `json:"command"`
	Args    []string `json:"args"`
	Cwd     string   `json:"cwd"`
}

// Configured reports whether the 2ndbrain MCP server is configured for the
// given vault in the current user's Claude Code config. It never returns an
// error for a missing or malformed config; those resolve to Configured:false
// so a caller (e.g. the Obsidian plugin) can render "not configured" plainly.
func Configured(v *vault.Vault) ConfiguredStatus {
	home, err := os.UserHomeDir()
	configPath := "~/.claude.json"
	if err == nil {
		configPath = filepath.Join(home, ".claude.json")
	}
	st := configuredFromFile(configPath, v.Root)
	// Always present the config path as the tilde form for display stability.
	st.ConfigPath = "~/.claude.json"
	return st
}

// configuredFromFile is the testable core: it reads a specific config file and
// reports whether a 2ndbrain server is configured for vaultPath. Separated from
// Configured so tests can point it at a temp file instead of the real config.
func configuredFromFile(configPath, vaultPath string) ConfiguredStatus {
	st := ConfiguredStatus{
		Client:     "claude-code",
		ConfigPath: configPath,
		VaultPath:  vaultPath,
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		return st // missing file → not configured, no error
	}
	var cfg claudeConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return st // malformed JSON → not configured, no error
	}

	// User scope: top-level mcpServers.
	if key, ok := match2ndbrainServer(cfg.MCPServers, vaultPath); ok {
		st.Configured = true
		st.Scope = "user"
		st.ServerKey = key
		return st
	}

	// Project scope: projects[<vaultPath>].mcpServers. Match the vault path
	// both verbatim and cleaned, since either spelling may be the config key.
	for _, candidate := range projectKeyCandidates(vaultPath) {
		proj, ok := cfg.Projects[candidate]
		if !ok {
			continue
		}
		if key, ok := match2ndbrainServer(proj.MCPServers, vaultPath); ok {
			st.Configured = true
			st.Scope = "project"
			st.ServerKey = key
			return st
		}
	}

	return st
}

// match2ndbrainServer scans a mcpServers map for an entry that runs the 2nb MCP
// server. The match is deliberately liberal so a user who renamed the server
// key (or set a cwd instead of args) is still recognized:
//   - key is "2ndbrain" or "2nb", OR
//   - command basename is "2nb" AND args contains "mcp-server", OR
//   - command basename is "2nb" AND cwd is this vault.
//
// The command/args match is preferred over the key name, so a renamed key with
// the right command still counts. Returns the matched key.
func match2ndbrainServer(servers map[string]mcpServerEntry, vaultPath string) (string, bool) {
	for key, entry := range servers {
		cmdIs2nb := filepath.Base(entry.Command) == "2nb"
		if cmdIs2nb && argsContain(entry.Args, "mcp-server") {
			return key, true
		}
		if cmdIs2nb && entry.Cwd != "" && sameDir(entry.Cwd, vaultPath) {
			return key, true
		}
		if key == "2ndbrain" || key == "2nb" {
			return key, true
		}
	}
	return "", false
}

func argsContain(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}

// sameDir reports whether two directory paths refer to the same location,
// comparing cleaned absolute forms.
func sameDir(a, b string) bool {
	return filepath.Clean(a) == filepath.Clean(b)
}

// projectKeyCandidates returns the spellings of a vault path that might be used
// as a projects map key in ~/.claude.json.
func projectKeyCandidates(vaultPath string) []string {
	clean := filepath.Clean(vaultPath)
	if clean == vaultPath {
		return []string{vaultPath}
	}
	return []string{vaultPath, clean}
}
