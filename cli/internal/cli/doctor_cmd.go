package cli

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/apresai/2ndbrain/internal/output"
	"github.com/apresai/2ndbrain/internal/vault"
	"github.com/spf13/cobra"
)

// 2ndbrain ships three products that must move together: the CLI (`2nb`,
// Homebrew formula `twonb`), the macOS app (`SecondBrain.app`, cask
// `secondbrain`), and the Obsidian plugin (`obsidian-2ndbrain`). They bump off
// one VERSION file, but the publish step can leave them split (e.g. a release
// that ships the CLI but not the signed app DMG). `2nb doctor` is the one
// user-facing surface that answers "do I have all three, and are they in sync
// with the latest release?" — the runtime counterpart to the per-product
// `verify`/`expect_version` checks in .release.yaml. Scope is presence +
// version parity; functional readiness lives in `config`/`mcp`/`skills doctor`.

// Canonical fix commands, kept next to the products they repair so a packaging
// change updates one place.
const (
	fixCLIUpgrade    = "brew upgrade apresai/tap/twonb"
	fixAppInstall    = "brew install --cask apresai/tap/secondbrain"
	fixAppUpgrade    = "brew upgrade --cask apresai/tap/secondbrain"
	fixPluginInstall = "2nb plugin install"
)

// Product status values (also the JSON `status` field).
const (
	statusOK       = "ok"       // installed and at the latest release
	statusOutdated = "outdated" // installed but behind the latest release
	statusMissing  = "missing"  // applicable but not installed
	statusUnknown  = "unknown"  // can't determine (offline, dev build, or vault not resolved)
	statusNA       = "n/a"      // not applicable on this platform (e.g. the macOS app on Linux)
)

// ProductState is one component's install + version-parity state. Shared by
// `2nb doctor --json` (SuiteStatus) and `2nb update --json` (UpdateStatus), so
// keep the field names stable.
type ProductState struct {
	Name            string `json:"name"`             // "cli" | "app" | "plugin"
	Status          string `json:"status"`           // ok | outdated | missing | unknown | n/a
	Installed       bool   `json:"installed"`
	Version         string `json:"version,omitempty"`
	UpdateAvailable bool   `json:"update_available"`
	Fix             string `json:"fix,omitempty"` // command to run when missing or outdated
}

// SuiteStatus is the `2nb doctor --json` contract: the three products against
// the latest published release. InSync is true when nothing installed is behind
// (a not-installed or unverifiable component is reported but does not flip it).
type SuiteStatus struct {
	Latest  string       `json:"latest,omitempty"`
	Checked bool         `json:"checked"`
	Detail  string       `json:"detail,omitempty"`
	InSync  bool         `json:"in_sync"`
	CLI     ProductState `json:"cli"`
	App     ProductState `json:"app"`
	Plugin  ProductState `json:"plugin"`
}

var doctorCmd = &cobra.Command{
	Use:     "doctor",
	Aliases: []string{"verify"},
	Short:   "Verify the CLI, macOS app, and Obsidian plugin are installed and in sync",
	Long: `Reports each 2ndbrain component — the CLI, the macOS app, and the
Obsidian plugin — with its installed version, whether it is current against the
latest published release, and the command to run to fix any gap.

The plugin is read from the open Obsidian vault (or --vault); with no vault
resolvable the plugin row is reported as unknown rather than failing. The latest
release is cached for 24h and an offline check degrades gracefully, so this is
safe in scripts and air-gapped environments.`,
	RunE: runDoctor,
}

func init() {
	doctorCmd.GroupID = "config"
	rootCmd.AddCommand(doctorCmd)
}

func runDoctor(cmd *cobra.Command, _ []string) error {
	suite := gatherSuiteStatus(context.Background())

	if format := getFormat(cmd); format != "" {
		return output.Write(os.Stdout, format, suite)
	}

	renderSuiteStatus(os.Stdout, suite)
	return nil
}

// gatherSuiteStatus fetches the latest release, reads each installed version
// (vault resolved best-effort for the plugin), and derives the parity state.
// Impure wrapper around the pure deriveSuiteStatus, shared by `doctor` and the
// enriched `update --json`.
func gatherSuiteStatus(ctx context.Context) SuiteStatus {
	appVer, appApplicable := installedAppVersion()
	pluginVer, vaultKnown := resolvePluginVersion()

	latest, fetchErr := fetchLatestReleaseVersion(ctx, false)
	// Refetch-on-stale: the installed version is proof a release at least that
	// high exists, so if the (cached) "latest" is below the highest installed
	// version the cache may be stale (a just-published release) — refetch once,
	// bypassing the cache, to pick it up promptly.
	//
	// But an install that is legitimately AHEAD of the public release (a
	// source/`make install` or prerelease build) keeps `latest < maxInst` true
	// even after a successful refetch, which would otherwise force a network call
	// on every run and make offline users wait on the HTTP timeout. So the
	// refetch is gated behind a short cooldown on the cache age: a successful
	// (forced) fetch rewrites the cache (resetting its mtime), suppressing further
	// refetches for the cooldown window. Net: at most one extra fetch per
	// cooldown, and a fresh cache (incl. offline) returns immediately. A `dev`
	// build never triggers this (it isn't a parseable version in maxInst).
	if fetchErr == nil && updateCacheOlderThan(latestRefetchCooldown) {
		maxInst := maxInstalledVersion(Version, appVer, appApplicable, pluginVer, vaultKnown)
		if maxInst != "" {
			if cmp, ok := comparePluginVersions(normalizeReleaseVersion(latest), maxInst); ok && cmp < 0 {
				if fresh, ferr := fetchLatestReleaseVersion(ctx, true); ferr == nil {
					latest = fresh
				}
			}
		}
	}

	return deriveSuiteStatus(Version, appVer, appApplicable, pluginVer, vaultKnown, latest, fetchErr)
}

// maxInstalledVersion returns the highest parseable installed version across the
// applicable products, normalized (no leading "v"), or "" if none is parseable.
// Dev/unparseable versions and not-installed / not-applicable products are
// ignored, so neither a `dev` build nor a missing component skews the result.
func maxInstalledVersion(cliVer, appVer string, appApplicable bool, pluginVer string, vaultKnown bool) string {
	cands := []string{cliVer}
	if appApplicable {
		cands = append(cands, appVer)
	}
	if vaultKnown {
		cands = append(cands, pluginVer)
	}
	max := ""
	for _, c := range cands {
		n := normalizeReleaseVersion(c)
		if _, ok := comparePluginVersions(n, "0.0.0"); !ok {
			continue // empty / dev / unparseable
		}
		if max == "" {
			max = n
			continue
		}
		if cmp, ok := comparePluginVersions(n, max); ok && cmp > 0 {
			max = n
		}
	}
	return max
}

// resolvePluginVersion reads the installed Obsidian plugin version WITHOUT
// opening the vault. `doctor`/`update` are read-only status commands, so they
// must not trigger vault.Open's side effects (creating .2ndbrain/, appending to
// .gitignore, opening/creating index.db). It resolves the vault root with the
// read-only helpers (the open Obsidian vault, an explicit --vault, or a walk-up
// from cwd) and reads only the plugin manifest. Returns ("", false) when no
// vault resolves, so the plugin is reported "unknown" rather than "missing".
func resolvePluginVersion() (version string, vaultKnown bool) {
	dir, source := resolveVaultDir()
	if source == sourceCwd {
		// resolveVaultDir returns "." for the cwd case; openResolvedVault would
		// normally walk up via vault.Open. Do that walk read-only instead.
		abs, err := filepath.Abs(dir)
		if err != nil {
			return "", false
		}
		dir = vault.FindVaultRoot(abs)
	}
	if dir == "" || !vault.IsVaultRoot(dir) {
		return "", false
	}
	v, err := installedPluginVersion(dir)
	if err != nil {
		// Corrupt/unreadable manifest: vault known, version undeterminable.
		// Surfaced under --verbose, matching update_cmd.go's swallow logging.
		slog.Debug("doctor: read plugin manifest failed", "dir", dir, "err", err)
		return "", true
	}
	return v, true
}

// deriveSuiteStatus is the pure parity derivation (no network, no filesystem),
// so the behind/current/missing/unknown logic is unit-tested offline.
func deriveSuiteStatus(cliVer, appVer string, appApplicable bool, pluginVer string, vaultKnown bool, latest string, fetchErr error) SuiteStatus {
	checked := fetchErr == nil
	s := SuiteStatus{Checked: checked}
	if !checked {
		s.Detail = "couldn't check for updates (offline?): " + fetchErr.Error()
	}

	// Each component is compared against the RAW published latest — never an
	// inflated max-installed value. Clamping the comparison up to the highest
	// install would wrongly flag the OTHER products as "outdated" against a
	// version the public channels can't provide (e.g. a `make install` CLI that's
	// ahead of the published release). The "installed > latest" contradiction is
	// instead avoided at the display layer (each surface shows "(latest X)" only
	// when X is actually newer than that component) plus refetch-on-stale above.
	if checked {
		s.Latest = latest
	}

	// CLI is always installed (it is the running binary).
	s.CLI = deriveProductState("cli", cliVer, "", fixCLIUpgrade, latest, checked)
	// A dev / non-release build is comparable to nothing; surface why (the
	// message `2nb update` has always shown for dev builds).
	if checked && s.CLI.Status == statusUnknown {
		s.Detail = fmt.Sprintf("this build (%s) isn't a released version, so it can't be compared to %s", cliVer, latest)
	}

	switch {
	case !appApplicable:
		s.App = ProductState{Name: "app", Status: statusNA}
	default:
		s.App = deriveProductState("app", appVer, fixAppInstall, fixAppUpgrade, latest, checked)
	}

	switch {
	case !vaultKnown:
		s.Plugin = ProductState{Name: "plugin", Status: statusUnknown,
			Fix: "open a vault in Obsidian or pass --vault to check the plugin"}
	default:
		s.Plugin = deriveProductState("plugin", pluginVer, fixPluginInstall, fixPluginInstall, latest, checked)
	}

	// In sync = nothing installed is behind. Missing / unknown / n/a are
	// reported but do not flip it (a CLI-only user without the plugin is not
	// "out of sync").
	s.InSync = checked &&
		s.CLI.Status != statusOutdated &&
		s.App.Status != statusOutdated &&
		s.Plugin.Status != statusOutdated
	return s
}

// deriveProductState computes one component's state from its installed version
// (empty = not installed), the install/upgrade fix commands, and the fetched
// latest release. checked=false (offline) or a non-semver version yields
// "unknown" rather than a false "up to date".
func deriveProductState(name, version, installFix, upgradeFix, latest string, checked bool) ProductState {
	p := ProductState{Name: name}
	if version == "" {
		p.Status = statusMissing
		p.Fix = installFix
		return p
	}
	p.Installed = true
	p.Version = version
	if !checked {
		p.Status = statusUnknown
		return p
	}
	cmp, ok := comparePluginVersions(normalizeReleaseVersion(version), normalizeReleaseVersion(latest))
	if !ok {
		// A dev / non-release build (Version == "dev") isn't comparable.
		p.Status = statusUnknown
		return p
	}
	if cmp < 0 {
		p.Status = statusOutdated
		p.UpdateAvailable = true
		p.Fix = upgradeFix
		return p
	}
	p.Status = statusOK
	return p
}

// installedAppVersion returns the installed SecondBrain.app version and whether
// the app is applicable on this platform. On non-darwin it is not applicable
// (false). On darwin it checks the cask location then the `make install-app`
// dev location; an empty string with applicable=true means "macOS, but not
// installed".
func installedAppVersion() (version string, applicable bool) {
	if runtime.GOOS != "darwin" {
		return "", false
	}
	home, _ := os.UserHomeDir()
	candidates := []string{"/Applications/SecondBrain.app"}
	if home != "" {
		candidates = append(candidates, filepath.Join(home, "Applications", "SecondBrain.app"))
	}
	for _, app := range candidates {
		plist := filepath.Join(app, "Contents", "Info.plist")
		data, err := os.ReadFile(plist)
		if err != nil {
			// "not installed" is the common, expected miss; only a real error
			// (e.g. permission denied on an installed app) is worth a trail.
			if !os.IsNotExist(err) {
				slog.Debug("doctor: read app Info.plist failed", "path", plist, "err", err)
			}
			continue
		}
		if v, ok := cfBundleShortVersion(data); ok {
			return v, true
		}
		// Fallback for a binary plist (the Makefile writes XML, but a future
		// Xcode build could ship a binary one): PlistBuddy reads both.
		if v := plistBuddyVersion(plist); v != "" {
			return v, true
		}
	}
	return "", true
}

// cfBundleShortVersion extracts CFBundleShortVersionString from an XML plist by
// scanning for the key then its following <string> value. Pure and
// dependency-free, so it is unit-tested against a fixture.
func cfBundleShortVersion(data []byte) (string, bool) {
	const key = "CFBundleShortVersionString"
	s := string(data)
	ki := strings.Index(s, key)
	if ki < 0 {
		return "", false
	}
	rest := s[ki+len(key):]
	open := strings.Index(rest, "<string>")
	if open < 0 {
		return "", false
	}
	rest = rest[open+len("<string>"):]
	close := strings.Index(rest, "</string>")
	if close < 0 {
		return "", false
	}
	v := strings.TrimSpace(rest[:close])
	if v == "" {
		return "", false
	}
	return v, true
}

// plistBuddyVersion reads CFBundleShortVersionString via /usr/libexec/PlistBuddy
// (handles XML and binary plists). Returns "" on any failure.
func plistBuddyVersion(plistPath string) string {
	out, err := exec.Command("/usr/libexec/PlistBuddy",
		"-c", "Print :CFBundleShortVersionString", plistPath).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// renderSuiteStatus prints the human checklist.
func renderSuiteStatus(w *os.File, s SuiteStatus) {
	header := "2ndbrain components"
	if s.Checked {
		// s.Latest is the release tag, already "vX.Y.Z".
		fmt.Fprintf(w, "%s (latest release: %s)\n\n", header, s.Latest)
	} else {
		fmt.Fprintf(w, "%s\n\n", header)
	}

	rows := []struct {
		label string
		p     ProductState
	}{
		{"CLI", s.CLI},
		{"App", s.App},
		{"Plugin", s.Plugin},
	}
	for _, r := range rows {
		fmt.Fprintln(w, "  "+formatProductRow(r.label, r.p))
	}

	fmt.Fprintln(w)
	fmt.Fprintln(w, suiteVerdict(s))
}

// formatProductRow renders one aligned component line.
func formatProductRow(label string, p ProductState) string {
	ver := p.Version
	if ver == "" {
		ver = "—"
	}
	var note string
	switch p.Status {
	case statusOK:
		note = "✓ up to date"
	case statusOutdated:
		note = fmt.Sprintf("outdated → %s", p.Fix)
	case statusMissing:
		note = fmt.Sprintf("not installed → %s", p.Fix)
	case statusNA:
		note = "macOS only"
	case statusUnknown:
		if p.Fix != "" {
			note = p.Fix
		} else if !p.Installed {
			note = "unknown"
		} else {
			note = "installed (version not comparable)"
		}
	}
	return fmt.Sprintf("%-7s %-9s %s", label, ver, note)
}

// suiteVerdict is the one-line summary under the rows.
func suiteVerdict(s SuiteStatus) string {
	if !s.Checked {
		return s.Detail
	}
	var behind []string
	for _, p := range []ProductState{s.CLI, s.App, s.Plugin} {
		if p.Status == statusOutdated {
			behind = append(behind, p.Name)
		}
	}
	if len(behind) > 0 {
		return fmt.Sprintf("%d component(s) behind (%s). Run the commands above to sync.",
			len(behind), strings.Join(behind, ", "))
	}
	// Nothing behind: note anything not installed / unverified.
	var notes []string
	if s.App.Status == statusMissing {
		notes = append(notes, "app not installed ("+s.App.Fix+")")
	}
	if s.Plugin.Status == statusMissing {
		notes = append(notes, "plugin not installed ("+s.Plugin.Fix+")")
	}
	if s.Plugin.Status == statusUnknown {
		notes = append(notes, "plugin not checked ("+s.Plugin.Fix+")")
	}
	if len(notes) > 0 {
		return fmt.Sprintf("Installed components are in sync at %s. Note: %s.",
			s.Latest, strings.Join(notes, "; "))
	}
	return fmt.Sprintf("Everything is in sync at %s.", s.Latest)
}
