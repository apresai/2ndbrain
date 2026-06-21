package cli

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/apresai/2ndbrain/internal/vault"
)

const (
	recentVaultsFile = ".2ndbrain-vaults"
	recentVaultsCap  = 10
)

func recentVaultsPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, recentVaultsFile)
}

// addRecentVault records a vault path in the recents file, newest first,
// deduped, capped at recentVaultsCap. Paths are canonicalized so the same
// vault reached via a symlink or /private prefix dedupes to one entry.
// Best-effort — a write failure does not block the caller.
func addRecentVault(absPath string) {
	if os.Getenv("2NB_TEST") != "" {
		return
	}
	path := recentVaultsPath()
	if path == "" || absPath == "" {
		return
	}
	absPath = canonicalVaultPath(absPath)

	entries := readRecentVaultsFile(path)
	out := make([]string, 0, len(entries)+1)
	out = append(out, absPath)
	for _, e := range entries {
		if e == absPath {
			continue
		}
		out = append(out, e)
		if len(out) >= recentVaultsCap {
			break
		}
	}

	_ = os.WriteFile(path, []byte(strings.Join(out, "\n")+"\n"), 0o644)
}

// listRecentVaults returns the recent-vaults list, filtered to paths that
// still have a .2ndbrain directory on disk. Stale entries are pruned from
// the file as a side effect.
func listRecentVaults() []string {
	path := recentVaultsPath()
	if path == "" {
		return nil
	}
	entries := readRecentVaultsFile(path)

	live := make([]string, 0, len(entries))
	for _, e := range entries {
		if _, err := os.Stat(filepath.Join(e, vault.DotDirName)); err == nil {
			live = append(live, e)
		}
	}

	if len(live) != len(entries) && os.Getenv("2NB_TEST") == "" {
		_ = os.WriteFile(path, []byte(strings.Join(live, "\n")+"\n"), 0o644)
	}
	return live
}

func readRecentVaultsFile(path string) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	lines := strings.Split(string(data), "\n")
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l != "" {
			out = append(out, l)
		}
	}
	return out
}

// canonicalVaultPath normalizes a vault path for storage and comparison:
// absolute, symlinks resolved. macOS paths arrive in multiple spellings for the
// same folder (/private/var vs /var, a symlinked home, the GUI's url.path vs a
// terminal's cwd), and the recents list is compared as strings — so every write
// and compare goes through this helper. EvalSymlinks fails on a path that no
// longer exists; fall back to the absolute form so stale entries round-trip.
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
