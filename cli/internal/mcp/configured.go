package mcp

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

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
	// omitempty so `mcp install` doesn't write a meaningless empty "cwd" into the
	// user's config for a user-scope (vault-pinned) entry. Only affects
	// marshaling (install); the detector only unmarshals this struct.
	Cwd string `json:"cwd,omitempty"`
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
		// Distinguish "no config yet" (the normal not-set-up case) from
		// "couldn't read your config" (a permission error or similar). Both
		// soft-fail to not-configured, but the latter is worth a trace for a
		// --verbose user wondering why detection says "no" on a config they
		// know exists.
		if !os.IsNotExist(err) {
			slog.Warn("mcp configured: could not read client config", "path", configPath, "err", err)
		}
		return st // missing/unreadable file → not configured, no error
	}
	var cfg claudeConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		// Malformed JSON soft-fails to not-configured (Anthropic owns this
		// file; we never block on its contents), but trace it so a --verbose
		// user can tell a corrupt config apart from a missing 2ndbrain entry.
		slog.Warn("mcp configured: client config is not valid JSON", "path", configPath, "err", err)
		return st // malformed JSON → not configured, no error
	}

	// User scope: top-level mcpServers.
	if key, ok := match2ndbrainServer(cfg.MCPServers, vaultPath); ok {
		st.Configured = true
		st.Scope = "user"
		st.ServerKey = key
		return st
	}

	// Project scope: projects[<path>].mcpServers, keyed by the absolute cwd the
	// client launched from. We can't assume that key is spelled the same way as
	// vaultPath (one may be a symlink, one resolved, or differ only by a
	// trailing slash), so compare every project key to this vault with the same
	// symlink-aware sameDir used elsewhere rather than indexing by an exact
	// string. This closes the false-negative where a genuinely-configured
	// project server read "not configured" because the key was a symlinked form.
	for projKey, proj := range cfg.Projects {
		if !sameDir(projKey, vaultPath) {
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
// server for THIS vault, and returns its key. Matching is two stages:
//
//  1. Is this entry a 2ndbrain MCP server at all? Yes if its key is the
//     conventional "2ndbrain"/"2nb", or its command basename is "2nb" and its
//     args name the "mcp-server" subcommand. (A "2nb" command without the
//     mcp-server subcommand is some other 2nb invocation, not the server.)
//  2. Does it serve THIS vault? An entry pinned to a specific vault, via a
//     "--vault <path>" arg (preferred, mirroring 2nb's own resolution order) or
//     a "cwd", counts only when that pin matches this vault. An entry with no
//     pin is vault-agnostic: the client resolves the vault at launch (the
//     active-vault file or cwd), so it counts for any vault.
//
// Stage 2 is the fix for the false positive where `2nb mcp-server --vault
// /other` (or `cwd: /other`) used to report "configured for THIS vault" purely
// because its args named mcp-server.
func match2ndbrainServer(servers map[string]mcpServerEntry, vaultPath string) (string, bool) {
	for key, entry := range servers {
		if !isSecondBrainServer(key, entry) {
			continue
		}
		if pin, pinned := pinnedVault(entry); pinned && !sameDir(pin, vaultPath) {
			continue // pinned to a different vault, not this one
		}
		return key, true
	}
	return "", false
}

// isSecondBrainServer reports whether an mcpServers entry runs the 2nb MCP
// server, recognized either by a conventional key name or by command + args.
func isSecondBrainServer(key string, entry mcpServerEntry) bool {
	if key == "2ndbrain" || key == "2nb" {
		return true
	}
	return filepath.Base(entry.Command) == "2nb" && argsContain(entry.Args, "mcp-server")
}

// pinnedVault returns the vault path an mcpServers entry is bound to, if any. A
// "--vault <path>" (or "--vault=<path>") arg wins over cwd, mirroring 2nb's own
// resolution order (--vault > 2NB_VAULT > active-vault file > cwd). An entry
// with neither is vault-agnostic and pinned is false.
func pinnedVault(entry mcpServerEntry) (path string, pinned bool) {
	if v, ok := vaultFlagValue(entry.Args); ok {
		return v, true
	}
	if entry.Cwd != "" {
		return entry.Cwd, true
	}
	return "", false
}

// vaultFlagValue extracts the value of a --vault flag from an argv slice,
// supporting both "--vault <path>" and "--vault=<path>" spellings.
func vaultFlagValue(args []string) (string, bool) {
	for i, a := range args {
		if a == "--vault" {
			if i+1 < len(args) {
				return args[i+1], true
			}
			return "", false // dangling flag, no value
		}
		if v, ok := strings.CutPrefix(a, "--vault="); ok {
			return v, true
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

// sameDir reports whether two directory paths refer to the same location. It
// first tries to canonicalize each path (absolute + symlinks resolved) so a
// symlinked spelling matches its target; if a path can't be resolved (e.g. it
// doesn't exist on this machine, common for a config written elsewhere), it
// falls back to a cleaned-absolute comparison so detection still works.
func sameDir(a, b string) bool {
	return canonicalDir(a) == canonicalDir(b)
}

// canonicalDir returns the most canonical form of a path available: absolute
// with symlinks resolved when possible, else cleaned-absolute, else cleaned.
func canonicalDir(p string) string {
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		return resolved
	}
	if abs, err := filepath.Abs(p); err == nil {
		return filepath.Clean(abs)
	}
	return filepath.Clean(p)
}
