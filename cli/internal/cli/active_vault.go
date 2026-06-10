package cli

import (
	"os"
	"path/filepath"
	"strings"
)

const activeVaultFile = ".2ndbrain-active-vault"

// activeVaultPath returns the path to the active vault config file.
// Stored in ~/.2ndbrain-active-vault (readable by both CLI and GUI).
func activeVaultPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, activeVaultFile)
}

// getActiveVault reads the active vault path from the config file.
func getActiveVault() string {
	path := activeVaultPath()
	if path == "" {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// setActiveVault writes the active vault path to the config file.
// Skips writing when running under go test to avoid polluting the user's config.
//
// The write is atomic (temp file + rename): the GUI writes this file on
// every vault bind and CLI write commands also update it, so a plain
// truncate-then-write could expose an empty/partial file to a concurrent
// reader, which would silently fall back to cwd resolution for that command.
func setActiveVault(vaultPath string) error {
	if os.Getenv("2NB_TEST") != "" {
		return nil
	}
	path := activeVaultPath()
	if path == "" {
		return nil
	}
	// A unique temp name (not a shared "<path>.tmp") so two concurrent
	// writers (GUI bind + CLI write command) can never rename each other's
	// half-written file into place.
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	if _, err := tmp.WriteString(canonicalVaultPath(vaultPath) + "\n"); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return nil
}

// canonicalVaultPath normalizes a vault path for storage and comparison:
// absolute, symlinks resolved. macOS paths arrive in multiple spellings for
// the same folder (/private/var vs /var, a symlinked home, the GUI's
// url.path vs a terminal's cwd), and the active-vault pointer and recents
// list are compared as strings — so every write and compare goes through
// this helper. EvalSymlinks fails on a path that no longer exists; fall
// back to the absolute form so stale entries still round-trip unchanged.
func canonicalVaultPath(p string) string {
	if p == "" {
		return "" // Abs("") would resolve to the cwd, inventing a path
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolved
	}
	return abs
}
