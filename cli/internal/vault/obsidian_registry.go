package vault

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/apresai/2ndbrain/internal/procutil"
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

// readObsidianRegistry parses Obsidian's vault registry and returns every entry
// with a non-empty path, sorted most-recently-opened first. It returns nil when
// the registry is absent, empty, unparseable, or lists none — it never errors,
// so callers can treat it as a silent rung in vault resolution.
func readObsidianRegistry() []obsidianRegistryEntry {
	path := obsidianRegistryPath()
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		// An absent registry is the normal "Obsidian not installed / never run"
		// case — silent. A present-but-unreadable one (perms, I/O) is worth a
		// trace: it's the exact "why didn't 2nb pick up my vault?" failure mode.
		if !os.IsNotExist(err) {
			slog.Warn("obsidian registry unreadable", "path", path, "error", err)
		}
		return nil
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return nil
	}
	var root struct {
		Vaults map[string]obsidianRegistryEntry `json:"vaults"`
	}
	if err := json.Unmarshal(data, &root); err != nil {
		// Present but corrupt: log so `--verbose` can diagnose it (the Swift
		// sibling logs this too), then fall through silently like every rung.
		slog.Warn("obsidian registry present but unparseable", "path", path, "error", err)
		return nil
	}

	entries := make([]obsidianRegistryEntry, 0, len(root.Vaults))
	for _, e := range root.Vaults {
		if e.Path != "" {
			entries = append(entries, e)
		}
	}
	if len(entries) == 0 {
		return nil
	}
	// Most-recently-opened first, matching the Swift registry's ordering, so the
	// "first open" pick (and the no-open fallback) are deterministic.
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].TS > entries[j].TS })
	return entries
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
	path, _ := ObsidianActiveVault()
	return path
}

// ObsidianActiveVault returns the vault Obsidian has open (or, when none is
// flagged open, the most recently opened one) and whether that pick was an entry
// actually flagged open (vs a most-recent fallback). The wasOpen distinction
// lets a write surface "Obsidian isn't open — using your most-recent vault"
// instead of silently committing. Returns ("", false) when the registry yields
// nothing. Callers must still validate the path with IsVaultRoot.
func ObsidianActiveVault() (path string, wasOpen bool) {
	entries := readObsidianRegistry()
	if len(entries) == 0 {
		return "", false
	}
	for _, e := range entries {
		if e.Open {
			return e.Path, true
		}
	}
	return entries[0].Path, false
}

// ObsidianKnownVaults returns the path of every vault Obsidian knows about
// (currently-open plus all recently-opened), most-recent first. Used by the
// firm write guard to decide whether an explicitly-targeted vault is one the
// user actually uses in Obsidian. Returns nil when the registry yields nothing.
func ObsidianKnownVaults() []string {
	entries := readObsidianRegistry()
	if len(entries) == 0 {
		return nil
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Path)
	}
	return out
}

// ObsidianVaultState is what could be established about whether Obsidian is
// holding a vault open. It is an enum rather than a pair of bools because the
// caller REFUSES on it and then has to say why, and "open" and "probably open"
// are not the same sentence. Claiming the first when only the second is known
// is the fault this whole guard was rewritten for, one layer up.
type ObsidianVaultState int

const (
	// ObsidianStateUnknown: unanswerable (no registry, or one this build cannot
	// parse). Never read as permission to write.
	ObsidianStateUnknown ObsidianVaultState = iota
	// ObsidianClosed: Obsidian is not holding this vault.
	ObsidianClosed
	// ObsidianOpen: confirmed. The registry names this vault AND the process is
	// running.
	ObsidianOpen
	// ObsidianOpenUnconfirmed: the registry names this vault, but whether
	// Obsidian is RUNNING could not be determined. Treated as open, because an
	// absence of evidence is not permission; said differently, because it is a
	// different fact.
	ObsidianOpenUnconfirmed
)

// ObsidianVaultOpenState answers from TWO facts, and needs both. The registry
// says WHICH vault Obsidian opens; only a running process says it is still
// there.
func ObsidianVaultOpenState(root string) ObsidianVaultState {
	entries := readObsidianRegistry()
	if len(entries) == 0 {
		return ObsidianStateUnknown
	}
	want := canonicalVaultPath(root)
	flagged := false
	for _, e := range entries {
		if e.Open && canonicalVaultPath(e.Path) == want {
			flagged = true
			break
		}
	}
	if !flagged {
		return ObsidianClosed
	}
	// The registry names WHICH vault; it cannot say whether Obsidian is still
	// there. Obsidian sets `open` when a vault is opened and never clears it on
	// quit: measured with Obsidian fully quit, the flag was still true and the
	// file two days untouched. On that alone this refused every user who had
	// ever opened the vault, including one who had just quit BECAUSE the command
	// asked them to, and the only way past was --force. A guard that fires
	// wrongly for nearly everyone teaches people to reach around it.
	alive, liveKnown := obsidianProcessAlive()
	if !liveKnown {
		// No liveness signal here. Keep the cautious answer rather than
		// inventing permission from an absence, but say which answer it is.
		return ObsidianOpenUnconfirmed
	}
	if alive {
		return ObsidianOpen
	}
	return ObsidianClosed
}

// obsidianProcessAlive reports whether Obsidian is RUNNING, and whether that
// question could be answered at all.
//
// It is a var so tests can substitute it. That is the repo's own pattern for a
// probe that would otherwise reach outside the test (see procCommand in
// internal/mcp/reap.go), and it matters more here than usual: the register-types
// call site has none of the 2NB_TEST isolation every registry read in root.go
// carries, so without substitution a developer who happens to have Obsidian
// open would get different test results from one who does not.
var obsidianProcessAlive = func() (alive, known bool) {
	lock := obsidianSingletonLockPath()
	if lock == "" {
		return false, false
	}
	target, err := os.Readlink(lock)
	if err != nil {
		if os.IsNotExist(err) {
			// Chromium removes the lock on a clean exit, so an absent one is a
			// real answer: nothing is holding the profile.
			return false, true
		}
		// Present but unreadable (a permission bit, or a regular file where a
		// symlink was expected). No answer, rather than a guess, and worth a
		// trace: the caller refuses on it, and a silent refusal is what made the
		// old guard hard to argue with.
		slog.Debug("obsidian singleton lock present but unreadable", "path", lock, "err", err)
		return false, false
	}
	// The target is `<hostname>-<pid>`, and the hostname itself contains dashes
	// (`apres-ai-124299.local-99064`), so the pid is what follows the LAST one.
	dash := strings.LastIndexByte(target, '-')
	if dash < 0 {
		return false, false
	}
	pid, err := strconv.Atoi(target[dash+1:])
	if err != nil {
		return false, false
	}
	// The hostname is in there for a reason, and ignoring it is how a shared or
	// networked home directory turns into a wrong answer: a lock written by a
	// DIFFERENT machine names a pid that means nothing locally and could well be
	// live here, belonging to something unrelated. Chromium encodes the host
	// precisely so this can be checked. A mismatch is UNKNOWN, never "running"
	// and never "closed".
	//
	// The two spellings do agree, which is worth recording rather than assuming,
	// because they need not: measured on macOS, os.Hostname() gave
	// "apres-ai-124299.local" and the lock read "apres-ai-124299.local-99064",
	// while `scutil --get LocalHostName` gave the bare "apres-ai-124299". Both
	// Chromium and Go take the gethostname(2) form, so they match.
	//
	// If they ever stop matching (a machine renamed since Obsidian launched, by
	// DHCP or an MDM), every answer becomes UNKNOWN and the command refuses with
	// the "could not be determined" wording plus --force. That is the same place
	// the old flag-only guard left everyone, so the failure degrades to no worse
	// than the bug this replaced, and it degrades honestly.
	host, herr := os.Hostname()
	if herr != nil || target[:dash] != host {
		slog.Debug("obsidian singleton lock names another host", "lock_host", target[:dash], "this_host", host, "err", herr)
		return false, false
	}
	// A lock left behind by a crash names a pid that is gone. That is a
	// definite "not running", not an unknown.
	//
	// One accepted race: if Obsidian writes the registry flag before it takes
	// this lock, a launch caught mid-flight reads as not-running. The write it
	// would then allow is merge-only, backed up first and atomically renamed,
	// so the cost is a change Obsidian may overwrite, and rerunning fixes it.
	return procutil.Alive(pid), true
}

// obsidianSingletonLockPath returns the Chromium singleton lock that Obsidian,
// an Electron app, keeps beside its registry while it runs. Empty when this
// platform does not use that mechanism or the config dir cannot be resolved.
//
// Windows is deliberately empty: Chromium uses a named mutex there, so an
// absent file would prove nothing and reading one as "not running" would hand
// out permission the signal never gave.
func obsidianSingletonLockPath() string {
	if runtime.GOOS == "windows" {
		return ""
	}
	reg := obsidianRegistryPath()
	if reg == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(reg), "SingletonLock")
}

// canonicalVaultPath normalizes a vault path for comparison: symlinks resolved
// where possible (macOS hands out /var and /private/var for the same directory),
// then cleaned. An unresolvable path falls back to the cleaned original rather
// than to "", so two unresolvable paths can still compare equal.
func canonicalVaultPath(p string) string {
	if p == "" {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		return filepath.Clean(resolved)
	}
	return filepath.Clean(p)
}
