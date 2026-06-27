package mcp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

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
	default:
		return InstallResult{}, fmt.Errorf("unknown client %q (want claude-code, warp, or agents)", client)
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
	default:
		return InstallResult{}, fmt.Errorf("unknown client %q (want claude-code, warp, or agents)", client)
	}
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
	res := InstallResult{Client: client, ConfigPath: display, ServerKey: serverKeyName, Scope: scope}

	root, orig, err := readConfigTree(cfgPath)
	if err != nil {
		return res, err
	}

	entryJSON, _ := json.Marshal(flatServerEntry(command, vaultRoot))
	// Flat-config clients use a flat top-level mcpServers map for both scopes (the
	// scope only selects which file), so reuse the "user"-style top-level mutation.
	changed, err := mutateServerEntry(root, "user", vaultRoot, func(servers map[string]json.RawMessage) bool {
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

func uninstallFlatClient(vaultRoot, scope, client, dotDir string, dryRun bool) (InstallResult, error) {
	if scope != "user" && scope != "project" {
		return InstallResult{}, fmt.Errorf("invalid scope %q (want user or project)", scope)
	}
	cfgPath, display := flatClientConfigPath(vaultRoot, scope, dotDir)
	res := InstallResult{Client: client, ConfigPath: display, ServerKey: serverKeyName, Scope: scope}

	root, orig, err := readConfigTree(cfgPath)
	if err != nil {
		return res, err
	}
	changed, err := mutateServerEntry(root, "user", vaultRoot, func(servers map[string]json.RawMessage) bool {
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
