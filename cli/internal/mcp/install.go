package mcp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/apresai/2ndbrain/internal/vault"
)

// serverKeyName is the mcpServers key we write (and that Configured recognizes).
const serverKeyName = "2ndbrain"

// InstallResult is the JSON payload of `2nb mcp install` / `mcp uninstall`.
type InstallResult struct {
	Client     string `json:"client"`
	ConfigPath string `json:"config_path"`
	Configured bool   `json:"configured"`  // configured for this vault AFTER the op
	Changed    bool   `json:"changed"`     // the config file was modified
	BackupPath string `json:"backup_path"` // "" if no backup was written
	ServerKey  string `json:"server_key"`
	Scope      string `json:"scope"`
	// Instructions holds manual setup steps when auto-config wasn't possible
	// (e.g. the `codex` CLI isn't on PATH so we print the command to run).
	Instructions string `json:"instructions,omitempty"`
	// Error is a per-client failure message. InstallAll/UninstallAll set it so
	// one client's failure doesn't abort configuring the rest; single-client
	// callers still receive the error via the returned error value.
	Error string `json:"error,omitempty"`
}

// Install writes (or updates) the 2ndbrain MCP server entry into Claude Code's
// config (~/.claude.json) so the server is launched for this vault. scope is
// "user" (top-level mcpServers, pinned to the vault via --vault) or "project"
// (projects[<vault>].mcpServers, pinned by the project key). command is the
// binary to launch ("2nb" by default; the GUI passes the bundled CLI's path
// when no Homebrew 2nb is on PATH). dryRun reports what would change without
// writing.
func Install(v *vault.Vault, command, scope string, dryRun bool) (InstallResult, error) {
	return installToFile(claudeConfigPath(), v.Root, command, scope, dryRun)
}

// Uninstall removes the 2ndbrain entry for this vault's scope from Claude Code's config.
func Uninstall(v *vault.Vault, scope string, dryRun bool) (InstallResult, error) {
	return uninstallFromFile(claudeConfigPath(), v.Root, scope, dryRun)
}

// InstallForClient routes to the right AI-client config writer. "" and
// "claude-code" target ~/.claude.json; "warp" targets Warp's ~/.warp/.mcp.json
// and "agents" the cross-tool ~/.agents/.mcp.json (project scope:
// <vault>/.warp|.agents/.mcp.json). Warp also auto-reads ~/.agents/.mcp.json, so
// "agents" reaches Warp and any other tool that honors the cross-tool location.
// Both flat-config clients pin the vault via --vault and working_directory so the
// server can't drift off the vault.
func InstallForClient(v *vault.Vault, client, command, scope string, dryRun bool) (InstallResult, error) {
	switch client {
	case "", "claude-code", "claude":
		return Install(v, command, scope, dryRun)
	case "warp":
		return installFlatClient(v.Root, command, scope, "warp", ".warp", dryRun)
	case "agents":
		return installFlatClient(v.Root, command, scope, "agents", ".agents", dryRun)
	case "claude-desktop":
		return installClaudeDesktop(command, v.Root, dryRun)
	case "codex":
		return installCodex(command, v.Root, dryRun)
	default:
		return InstallResult{}, fmt.Errorf("unknown client %q (want %s, or all)", client, strings.Join(SupportedClients(), ", "))
	}
}

// UninstallForClient is the client-aware inverse of InstallForClient.
func UninstallForClient(v *vault.Vault, client, scope string, dryRun bool) (InstallResult, error) {
	switch client {
	case "", "claude-code", "claude":
		return Uninstall(v, scope, dryRun)
	case "warp":
		return uninstallFlatClient(v.Root, scope, "warp", ".warp", dryRun)
	case "agents":
		return uninstallFlatClient(v.Root, scope, "agents", ".agents", dryRun)
	case "claude-desktop":
		return uninstallClaudeDesktop(dryRun)
	case "codex":
		return uninstallCodex(dryRun)
	default:
		return InstallResult{}, fmt.Errorf("unknown client %q (want %s, or all)", client, strings.Join(SupportedClients(), ", "))
	}
}

// SupportedClients is the ordered set of AI clients that `mcp install`,
// `setup`, and `mcp configured` understand. Single source of truth for flag
// help, shell completion, `--client all`, and multi-client detection.
func SupportedClients() []string {
	return []string{"claude-code", "claude-desktop", "warp", "agents", "codex"}
}

// InstallAll configures every supported client, never aborting on one client's
// failure: a per-client error is captured in that result's Error field so the
// remaining clients still run. claude-desktop and codex are user-scope only, so
// their scope is coerced to "user".
func InstallAll(v *vault.Vault, command, scope string, dryRun bool) []InstallResult {
	return runAll(SupportedClients(), func(c, s string) (InstallResult, error) {
		return InstallForClient(v, c, command, s, dryRun)
	}, scope)
}

// UninstallAll is the inverse of InstallAll.
func UninstallAll(v *vault.Vault, scope string, dryRun bool) []InstallResult {
	return runAll(SupportedClients(), func(c, s string) (InstallResult, error) {
		return UninstallForClient(v, c, s, dryRun)
	}, scope)
}

func runAll(clients []string, op func(client, scope string) (InstallResult, error), scope string) []InstallResult {
	out := make([]InstallResult, 0, len(clients))
	for _, c := range clients {
		s := scope
		if c == "claude-desktop" || c == "codex" {
			s = "user" // these clients have a single global config, no project scope
		}
		res, err := op(c, s)
		if err != nil {
			if res.Client == "" {
				res.Client = c
			}
			res.Error = err.Error()
		}
		out = append(out, res)
	}
	return out
}

// flatClientConfigPath returns the on-disk path and its display form for a flat
// mcpServers client config at the given scope: user → ~/<dotDir>/.mcp.json,
// project → the vault's own <dotDir>/.mcp.json. Used by Warp (".warp") and the
// cross-tool ".agents" location; both use a flat top-level mcpServers map.
func flatClientConfigPath(vaultRoot, scope, dotDir string) (path, display string) {
	if scope == "project" {
		p := filepath.Join(vaultRoot, dotDir, ".mcp.json")
		return p, p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(dotDir, ".mcp.json"), "~/" + dotDir + "/.mcp.json"
	}
	return filepath.Join(home, dotDir, ".mcp.json"), "~/" + dotDir + "/.mcp.json"
}

// flatServerEntry pins the vault via both --vault and working_directory, for
// flat-config clients (Warp, and the cross-tool .agents location).
func flatServerEntry(command, vaultRoot string) mcpServerEntry {
	if command == "" {
		command = "2nb"
	}
	return mcpServerEntry{
		Command:          command,
		Args:             []string{"mcp-server", "--vault", vaultRoot},
		WorkingDirectory: vaultRoot,
	}
}

func installFlatClient(vaultRoot, command, scope, client, dotDir string, dryRun bool) (InstallResult, error) {
	if scope != "user" && scope != "project" {
		return InstallResult{}, fmt.Errorf("invalid scope %q (want user or project)", scope)
	}
	cfgPath, display := flatClientConfigPath(vaultRoot, scope, dotDir)
	return installFlatFile(client, cfgPath, display, scope, flatServerEntry(command, vaultRoot), dryRun)
}

func uninstallFlatClient(vaultRoot, scope, client, dotDir string, dryRun bool) (InstallResult, error) {
	if scope != "user" && scope != "project" {
		return InstallResult{}, fmt.Errorf("invalid scope %q (want user or project)", scope)
	}
	cfgPath, display := flatClientConfigPath(vaultRoot, scope, dotDir)
	return uninstallFlatFile(client, cfgPath, display, scope, dryRun)
}

// installFlatFile writes entry into a flat top-level mcpServers JSON config at
// cfgPath (backup-first, preserving every other key byte-for-byte). Shared by
// Warp, the cross-tool .agents location, and Claude Desktop — all of which use a
// flat mcpServers map. The scope argument only labels the result; the file is
// already chosen by cfgPath.
func installFlatFile(client, cfgPath, display, scope string, entry mcpServerEntry, dryRun bool) (InstallResult, error) {
	res := InstallResult{Client: client, ConfigPath: display, ServerKey: serverKeyName, Scope: scope}
	root, orig, err := readConfigTree(cfgPath)
	if err != nil {
		return res, err
	}
	entryJSON, _ := json.Marshal(entry)
	changed, err := mutateServerEntry(root, "user", "", func(servers map[string]json.RawMessage) bool {
		if existing, ok := servers[serverKeyName]; ok && jsonEqual(existing, entryJSON) {
			return false
		}
		servers[serverKeyName] = entryJSON
		return true
	})
	if err != nil {
		return res, err
	}
	res.Changed = changed
	res.Configured = true
	if changed && !dryRun {
		if mkerr := os.MkdirAll(filepath.Dir(cfgPath), 0o755); mkerr != nil {
			return res, fmt.Errorf("create %s: %w", filepath.Dir(cfgPath), mkerr)
		}
		backup, werr := writeConfigTree(cfgPath, root, orig)
		if werr != nil {
			return res, werr
		}
		res.BackupPath = backup
	}
	return res, nil
}

func uninstallFlatFile(client, cfgPath, display, scope string, dryRun bool) (InstallResult, error) {
	res := InstallResult{Client: client, ConfigPath: display, ServerKey: serverKeyName, Scope: scope}
	root, orig, err := readConfigTree(cfgPath)
	if err != nil {
		return res, err
	}
	changed, err := mutateServerEntry(root, "user", "", func(servers map[string]json.RawMessage) bool {
		if _, ok := servers[serverKeyName]; !ok {
			return false
		}
		delete(servers, serverKeyName)
		return true
	})
	if err != nil {
		return res, err
	}
	res.Changed = changed
	res.Configured = false
	if changed && !dryRun {
		backup, werr := writeConfigTree(cfgPath, root, orig)
		if werr != nil {
			return res, werr
		}
		res.BackupPath = backup
	}
	return res, nil
}

// Injectable for tests (mirrors the DI pattern in skills/doctor.go).
var (
	desktopLookPath = exec.LookPath
	osExecutable    = os.Executable
)

// claudeDesktopConfigPath returns the OS-specific Claude Desktop MCP config path
// and its display form. Claude Desktop ships only on macOS and Windows; other
// platforms return an error so callers degrade cleanly rather than panic.
func claudeDesktopConfigPath() (path, display string, err error) {
	switch runtime.GOOS {
	case "darwin":
		home, herr := os.UserHomeDir()
		if herr != nil {
			return "", "", herr
		}
		p := filepath.Join(home, "Library", "Application Support", "Claude", "claude_desktop_config.json")
		return p, "~/Library/Application Support/Claude/claude_desktop_config.json", nil
	case "windows":
		appData := os.Getenv("APPDATA")
		if appData == "" {
			return "", "", fmt.Errorf("APPDATA is not set; cannot locate the Claude Desktop config")
		}
		return filepath.Join(appData, "Claude", "claude_desktop_config.json"), `%APPDATA%\Claude\claude_desktop_config.json`, nil
	default:
		return "", "", fmt.Errorf("Claude Desktop has no supported config location on %s", runtime.GOOS)
	}
}

// resolveAbsCommand resolves an ABSOLUTE path to the 2nb binary, required for GUI
// clients (Claude Desktop) that launch MCP servers with a minimal PATH where a
// bare "2nb" won't resolve. It deliberately does NOT resolve symlinks, so the
// stable Homebrew bin symlink survives a `brew upgrade`. An explicit --command
// (absolute, or resolvable on PATH) wins.
func resolveAbsCommand(command string) (string, error) {
	if command != "" && command != "2nb" {
		if filepath.IsAbs(command) {
			return command, nil
		}
		if p, err := desktopLookPath(command); err == nil {
			return p, nil
		}
		return "", fmt.Errorf("could not resolve an absolute path for --command %q", command)
	}
	if p, err := desktopLookPath("2nb"); err == nil && filepath.IsAbs(p) {
		return p, nil
	}
	if p, err := osExecutable(); err == nil && filepath.IsAbs(p) {
		return p, nil
	}
	return "", fmt.Errorf("could not resolve an absolute 2nb path for a GUI client; pass --command /absolute/path/to/2nb")
}

// installClaudeDesktop writes the 2ndbrain server into Claude Desktop's JSON
// config (flat mcpServers). Claude Desktop supports only command/args/env (no
// cwd/working_directory, and a `url` field silently destroys the file), and it's
// a GUI app, so the command is resolved to an absolute path and the vault is
// pinned via --vault in args only. User scope only (single global config).
func installClaudeDesktop(command, vaultRoot string, dryRun bool) (InstallResult, error) {
	cfgPath, display, err := claudeDesktopConfigPath()
	if err != nil {
		return InstallResult{Client: "claude-desktop", Scope: "user", ServerKey: serverKeyName}, err
	}
	abs, err := resolveAbsCommand(command)
	if err != nil {
		return InstallResult{Client: "claude-desktop", ConfigPath: display, Scope: "user", ServerKey: serverKeyName}, err
	}
	entry := mcpServerEntry{Command: abs, Args: []string{"mcp-server", "--vault", vaultRoot}}
	return installFlatFile("claude-desktop", cfgPath, display, "user", entry, dryRun)
}

func uninstallClaudeDesktop(dryRun bool) (InstallResult, error) {
	cfgPath, display, err := claudeDesktopConfigPath()
	if err != nil {
		return InstallResult{Client: "claude-desktop", Scope: "user", ServerKey: serverKeyName}, err
	}
	return uninstallFlatFile("claude-desktop", cfgPath, display, "user", dryRun)
}

func claudeConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".claude.json"
	}
	return filepath.Join(home, ".claude.json")
}

func desiredEntry(command, scope, vaultRoot string) mcpServerEntry {
	if command == "" {
		command = "2nb"
	}
	// User scope pins the vault via --vault (the client may be launched from any
	// cwd). Project scope is keyed by the cwd, so the args stay vault-agnostic.
	if scope == "project" {
		return mcpServerEntry{Command: command, Args: []string{"mcp-server"}}
	}
	return mcpServerEntry{Command: command, Args: []string{"mcp-server", "--vault", vaultRoot}}
}

func installToFile(configPath, vaultRoot, command, scope string, dryRun bool) (InstallResult, error) {
	if scope != "user" && scope != "project" {
		return InstallResult{}, fmt.Errorf("invalid scope %q (want user or project)", scope)
	}
	res := InstallResult{Client: "claude-code", ConfigPath: "~/.claude.json", ServerKey: serverKeyName, Scope: scope}

	root, orig, err := readConfigTree(configPath)
	if err != nil {
		return res, err
	}

	entry := desiredEntry(command, scope, vaultRoot)
	entryJSON, _ := json.Marshal(entry)

	changed, err := mutateServerEntry(root, scope, vaultRoot, func(servers map[string]json.RawMessage) bool {
		if existing, ok := servers[serverKeyName]; ok && jsonEqual(existing, entryJSON) {
			return false // already present and identical → idempotent no-op
		}
		servers[serverKeyName] = entryJSON
		return true
	})
	if err != nil {
		return res, err
	}

	res.Changed = changed
	res.Configured = true // the entry is (or already was) present for this vault
	if changed && !dryRun {
		backup, werr := writeConfigTree(configPath, root, orig)
		if werr != nil {
			return res, werr
		}
		res.BackupPath = backup
	}
	return res, nil
}

func uninstallFromFile(configPath, vaultRoot, scope string, dryRun bool) (InstallResult, error) {
	if scope != "user" && scope != "project" {
		return InstallResult{}, fmt.Errorf("invalid scope %q (want user or project)", scope)
	}
	res := InstallResult{Client: "claude-code", ConfigPath: "~/.claude.json", ServerKey: serverKeyName, Scope: scope}

	root, orig, err := readConfigTree(configPath)
	if err != nil {
		return res, err
	}

	changed, err := mutateServerEntry(root, scope, vaultRoot, func(servers map[string]json.RawMessage) bool {
		if _, ok := servers[serverKeyName]; !ok {
			return false
		}
		delete(servers, serverKeyName)
		return true
	})
	if err != nil {
		return res, err
	}

	res.Changed = changed
	res.Configured = false
	if changed && !dryRun {
		backup, werr := writeConfigTree(configPath, root, orig)
		if werr != nil {
			return res, werr
		}
		res.BackupPath = backup
	}
	return res, nil
}

// readConfigTree parses ~/.claude.json into a top-level RawMessage map so every
// unrelated key is preserved byte-for-byte on re-marshal. A missing file yields
// an empty tree (to be created); a malformed file is REFUSED — a write command
// must never clobber a config it can't parse.
func readConfigTree(configPath string) (root map[string]json.RawMessage, orig []byte, err error) {
	orig, err = os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]json.RawMessage{}, nil, nil
		}
		return nil, nil, fmt.Errorf("read %s: %w", configPath, err)
	}
	if err := json.Unmarshal(orig, &root); err != nil {
		return nil, nil, fmt.Errorf("refusing to write: %s is not valid JSON (%w); fix it by hand or remove it", configPath, err)
	}
	if root == nil {
		root = map[string]json.RawMessage{}
	}
	return root, orig, nil
}

// mutateServerEntry applies mutate to the mcpServers sub-map at the requested
// scope, preserving all sibling keys at each nesting level via RawMessage. It
// returns whether mutate reported a change. A malformed mcpServers/projects
// sub-object is refused rather than dropped.
func mutateServerEntry(root map[string]json.RawMessage, scope, vaultRoot string, mutate func(servers map[string]json.RawMessage) bool) (bool, error) {
	if scope == "user" {
		servers, err := rawObject(root, "mcpServers")
		if err != nil {
			return false, err
		}
		if !mutate(servers) {
			return false, nil
		}
		root["mcpServers"] = mustMarshal(servers)
		return true, nil
	}

	// project scope: projects[<vaultRoot>].mcpServers
	projects, err := rawObject(root, "projects")
	if err != nil {
		return false, err
	}
	projObj, err := rawObject(projects, vaultRoot)
	if err != nil {
		return false, err
	}
	servers, err := rawObject(projObj, "mcpServers")
	if err != nil {
		return false, err
	}
	if !mutate(servers) {
		return false, nil
	}
	projObj["mcpServers"] = mustMarshal(servers)
	projects[vaultRoot] = mustMarshal(projObj)
	root["projects"] = mustMarshal(projects)
	return true, nil
}

// rawObject reads parent[key] as a RawMessage map, or an empty map if absent. A
// present-but-non-object value is an error (refuse rather than overwrite it).
func rawObject(parent map[string]json.RawMessage, key string) (map[string]json.RawMessage, error) {
	out := map[string]json.RawMessage{}
	raw, ok := parent[key]
	if !ok || len(bytes.TrimSpace(raw)) == 0 || string(bytes.TrimSpace(raw)) == "null" {
		return out, nil
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("refusing to write: %q in the config is not a JSON object: %w", key, err)
	}
	return out, nil
}

// writeConfigTree backs up the original (if any), then atomically writes the
// re-marshaled tree. The backup preserves the original mode; the temp+rename
// replaces the real file (symlinks resolved) so an editor symlink isn't clobbered.
func writeConfigTree(configPath string, root map[string]json.RawMessage, orig []byte) (backupPath string, err error) {
	target := configPath
	if resolved, rerr := filepath.EvalSymlinks(configPath); rerr == nil {
		target = resolved
	}

	mode := os.FileMode(0o600)
	if fi, serr := os.Stat(target); serr == nil {
		mode = fi.Mode().Perm()
	}

	if orig != nil {
		backupPath = configPath + ".bak"
		if berr := os.WriteFile(backupPath, orig, mode); berr != nil {
			return "", fmt.Errorf("write backup %s: %w", backupPath, berr)
		}
	}

	out, merr := json.MarshalIndent(root, "", "  ")
	if merr != nil {
		return backupPath, fmt.Errorf("marshal config: %w", merr)
	}
	out = append(out, '\n')

	tmp := target + ".tmp"
	if werr := os.WriteFile(tmp, out, mode); werr != nil {
		return backupPath, fmt.Errorf("write temp config: %w", werr)
	}
	if rerr := os.Rename(tmp, target); rerr != nil {
		os.Remove(tmp)
		return backupPath, fmt.Errorf("replace config: %w", rerr)
	}
	return backupPath, nil
}

func mustMarshal(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

func jsonEqual(a, b json.RawMessage) bool {
	var av, bv any
	if json.Unmarshal(a, &av) != nil || json.Unmarshal(b, &bv) != nil {
		return false
	}
	an, _ := json.Marshal(av)
	bn, _ := json.Marshal(bv)
	return bytes.Equal(an, bn)
}
