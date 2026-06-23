package cli

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/apresai/2ndbrain/internal/output"
	"github.com/spf13/cobra"
)

const (
	updateLatestReleaseURL = "https://api.github.com/repos/apresai/2ndbrain/releases/latest"
	updateCacheTTL         = 24 * time.Hour
	updateCacheFileName    = "latest-release.json"
	// latestRefetchCooldown bounds how often gatherSuiteStatus force-refetches
	// when an install is ahead of the cached "latest" — a successful refetch
	// rewrites the cache (resetting its mtime), so the next refetch waits at
	// least this long. Keeps the install-ahead path from hitting the network
	// every run (and offline users from eating the HTTP timeout each time).
	latestRefetchCooldown = time.Hour
)

var updateHTTPClient = &http.Client{Timeout: 15 * time.Second}

// UpdateStatus reports the installed CLI version against the latest published
// release. It is the `2nb update --json` contract (consumed by the macOS app's
// Updates tab), so keep the field names stable.
//
// Current/Latest/UpdateAvailable describe the CLI (the historical contract). App
// and Plugin carry the same parity state for the other two products, added so
// terminal, plugin, and app consumers see all three from one payload. `2nb
// doctor` is the richer, presence-aware view of the same data.
type UpdateStatus struct {
	Current         string       `json:"current"`
	Latest          string       `json:"latest,omitempty"`
	UpdateAvailable bool         `json:"update_available"`
	Checked         bool         `json:"checked"`
	Detail          string       `json:"detail,omitempty"`
	App             ProductState `json:"app"`
	Plugin          ProductState `json:"plugin"`
}

var updateCmd = &cobra.Command{
	Use:   "update",
	Short: "Check whether a newer 2ndbrain release is available",
	Long: `Compares this CLI's version against the latest published GitHub release
and, when newer, prints the commands to upgrade the CLI, the macOS app, and the
Obsidian plugin.

The latest version is cached for 24h. An offline check falls back to the cache,
then reports that it could not check — it never hard-errors, so it is safe in
scripts and air-gapped environments.`,
	RunE: runUpdate,
}

func init() {
	updateCmd.GroupID = "config"
	rootCmd.AddCommand(updateCmd)
}

func runUpdate(cmd *cobra.Command, _ []string) error {
	suite := gatherSuiteStatus(context.Background())
	status := updateStatusFromSuite(suite)

	if format := getFormat(cmd); format != "" {
		return output.Write(os.Stdout, format, status)
	}

	fmt.Printf("Current: %s\n", status.Current)
	if !status.Checked {
		fmt.Printf("Latest:  unknown\n\n%s\n", status.Detail)
		return nil
	}
	fmt.Printf("Latest:  %s\n", status.Latest)

	// Flag every component that is behind, not just the CLI: a release that
	// shipped the CLI but not the app/plugin (or vice versa) is still an update.
	behind := outdatedProducts(suite)
	switch {
	case len(behind) > 0:
		// status.Latest is the "vX.Y.Z" tag; installed versions are bare, so
		// normalize the target for a consistent "0.10.3 -> 0.10.4" line.
		target := normalizeReleaseVersion(status.Latest)
		fmt.Println("\nAn update is available:")
		for _, p := range behind {
			fmt.Printf("  %-7s %s -> %s   %s\n", p.Name, p.Version, target, p.Fix)
		}
		fmt.Println("\nRun `2nb doctor` for the full component breakdown.")
	case status.Detail != "":
		fmt.Printf("\n%s\n", status.Detail)
	default:
		fmt.Println("\nYou're up to date.")
	}
	return nil
}

// updateStatusFromSuite maps the shared SuiteStatus onto the historical
// `2nb update --json` contract (CLI fields) plus the App/Plugin states.
func updateStatusFromSuite(s SuiteStatus) UpdateStatus {
	return UpdateStatus{
		Current:         s.CLI.Version,
		Latest:          s.Latest,
		UpdateAvailable: s.CLI.UpdateAvailable,
		Checked:         s.Checked,
		Detail:          s.Detail,
		App:             s.App,
		Plugin:          s.Plugin,
	}
}

// outdatedProducts returns the components that are installed but behind latest.
func outdatedProducts(s SuiteStatus) []ProductState {
	var out []ProductState
	for _, p := range []ProductState{s.CLI, s.App, s.Plugin} {
		if p.Status == statusOutdated {
			out = append(out, p)
		}
	}
	return out
}

// normalizeReleaseVersion drops a leading "v" so a GitHub tag (v0.10.1) compares
// against the LDFLAGS-stamped Version (0.10.1).
func normalizeReleaseVersion(s string) string {
	return strings.TrimPrefix(strings.TrimSpace(s), "v")
}

// fetchLatestReleaseVersion returns the latest published release tag, cached for
// updateCacheTTL. A fresh cache short-circuits the network; a failed fetch falls
// back to a stale cache; only an empty cache + failed fetch returns an error.
//
// force=true skips the fresh-cache read and goes to the network (used when the
// cached "latest" is provably stale because a newer version is already
// installed), but still falls back to the stale cache on a failed fetch — so a
// forced refresh is never less safe offline than a normal one.
func fetchLatestReleaseVersion(ctx context.Context, force bool) (string, error) {
	cachePath := updateCachePath()

	if !force && cachePath != "" {
		if data, ok := readUpdateCache(cachePath, true); ok {
			if tag, err := parseLatestReleaseTag(data); err == nil {
				return tag, nil
			}
		}
	}

	data, err := fetchReleaseJSON(ctx, updateLatestReleaseURL)
	if err == nil {
		if cachePath != "" {
			if werr := writeUpdateCache(cachePath, data); werr != nil {
				slog.Debug("update cache write failed", "path", cachePath, "err", werr)
			}
		}
		if tag, perr := parseLatestReleaseTag(data); perr == nil {
			return tag, nil
		} else {
			err = perr
		}
	}

	// Stale cache fallback when the network or parse failed.
	if cachePath != "" {
		if data, ok := readUpdateCache(cachePath, false); ok {
			if tag, perr := parseLatestReleaseTag(data); perr == nil {
				slog.Debug("using stale update cache", "err", err)
				return tag, nil
			}
		}
	}
	return "", err
}

func fetchReleaseJSON(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "2nb/"+Version)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := updateHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		hint := ""
		if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests {
			hint = " (GitHub rate limit? the result is cached 24h, retry later)"
		}
		return nil, fmt.Errorf("GET %s: HTTP %d%s", url, resp.StatusCode, hint)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 1<<20))
}

func updateCacheDir() (string, error) {
	if xdg := os.Getenv("XDG_CACHE_HOME"); xdg != "" {
		return filepath.Join(xdg, "2nb", "updates"), nil
	}
	if dir, err := os.UserCacheDir(); err == nil && dir != "" {
		return filepath.Join(dir, "2nb", "updates"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".cache", "2nb", "updates"), nil
}

func updateCachePath() string {
	dir, err := updateCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, updateCacheFileName)
}

// updateCacheOlderThan reports whether the cached "latest" was last refreshed
// more than d ago. It returns false when the cache is missing/unreadable (the
// caller just wrote it via the non-forced fetch, so it's effectively fresh) — so
// a force refresh is gated to "we have a cache and it has aged past d". This
// bounds how often the install-is-ahead path force-refetches (see
// gatherSuiteStatus), keeping the 24h cache and offline behavior intact.
func updateCacheOlderThan(d time.Duration) bool {
	path := updateCachePath()
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return time.Since(info.ModTime()) > d
}

func readUpdateCache(path string, freshOnly bool) ([]byte, bool) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, false
	}
	if freshOnly && time.Since(info.ModTime()) > updateCacheTTL {
		return nil, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	return data, true
}

func writeUpdateCache(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
