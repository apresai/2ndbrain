package vault

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"sort"
)

// obsidianRegistryEntry mirrors one entry in Obsidian's vault registry. The
// file is an internal Obsidian format with no published schema, so decoding is
// deliberately defensive: missing ts/open degrade to 0/false.
type obsidianRegistryEntry struct {
	Path string `json:"path"`
	TS   int64  `json:"ts"`   // last-opened epoch-millis (0 if absent)
	Open bool   `json:"open"` // currently-open flag (false if absent)
}

// obsidianRegistryPath returns the per-OS location of Obsidian's vault registry
// (obsidian.json). 2nb follows Obsidian's open vault, so this must resolve on
// every platform the CLI runs on, not just macOS:
//   - macOS:   ~/Library/Application Support/obsidian/obsidian.json
//   - Linux:   $XDG_CONFIG_HOME/obsidian/obsidian.json (or ~/.config/obsidian/…)
//   - Windows: %APPDATA%/obsidian/obsidian.json
//
// Returns "" when the home/config dir can't be determined; an absent file is
// handled by the caller (ObsidianOpenVault returns "").
func obsidianRegistryPath() string {
	switch runtime.GOOS {
	case "darwin":
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		return filepath.Join(home, "Library", "Application Support", "obsidian", "obsidian.json")
	case "windows":
		if appData := os.Getenv("APPDATA"); appData != "" {
			return filepath.Join(appData, "obsidian", "obsidian.json")
		}
		return ""
	default: // linux and other unixes
		if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
			return filepath.Join(xdg, "obsidian", "obsidian.json")
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		return filepath.Join(home, ".config", "obsidian", "obsidian.json")
	}
}

// ObsidianOpenVault returns the absolute path of the vault Obsidian currently
// has open, read from Obsidian's own registry. If no entry is flagged open it
// falls back to the most recently opened vault (highest ts), exactly mirroring
// the macOS app's ObsidianRegistry.openVault. Returns "" when the registry is
// absent, empty, unparseable, or lists none — it never errors, so it can serve
// as a silent rung in vault resolution.
//
// This lets a bare `2nb` invoked from outside any vault (e.g. a source repo)
// target the same vault the dashboard binds to, instead of failing to resolve.
// Callers must still validate the path with IsVaultRoot before using it.
func ObsidianOpenVault() string {
	path := obsidianRegistryPath()
	if path == "" {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		// An absent registry is the normal "Obsidian not installed / never run"
		// case — silent. A present-but-unreadable one (perms, I/O) is worth a
		// trace: it's the exact "why didn't 2nb pick up my vault?" failure mode.
		if !os.IsNotExist(err) {
			slog.Warn("obsidian registry unreadable", "path", path, "error", err)
		}
		return ""
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return ""
	}
	var root struct {
		Vaults map[string]obsidianRegistryEntry `json:"vaults"`
	}
	if err := json.Unmarshal(data, &root); err != nil {
		// Present but corrupt: log so `--verbose` can diagnose it (the Swift
		// sibling logs this too), then fall through silently like every rung.
		slog.Warn("obsidian registry present but unparseable", "path", path, "error", err)
		return ""
	}
	if len(root.Vaults) == 0 {
		return ""
	}

	entries := make([]obsidianRegistryEntry, 0, len(root.Vaults))
	for _, e := range root.Vaults {
		if e.Path != "" {
			entries = append(entries, e)
		}
	}
	if len(entries) == 0 {
		return ""
	}

	// Most-recently-opened first, matching the Swift registry's ordering, so the
	// "first open" pick (and the no-open fallback) are deterministic.
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].TS > entries[j].TS })
	for _, e := range entries {
		if e.Open {
			return e.Path
		}
	}
	return entries[0].Path
}
