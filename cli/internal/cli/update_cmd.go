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
)

var updateHTTPClient = &http.Client{Timeout: 15 * time.Second}

// UpdateStatus reports the installed CLI version against the latest published
// release. It is the `2nb update --json` contract (consumed by the macOS app's
// Updates tab), so keep the field names stable.
type UpdateStatus struct {
	Current         string `json:"current"`
	Latest          string `json:"latest,omitempty"`
	UpdateAvailable bool   `json:"update_available"`
	Checked         bool   `json:"checked"`
	Detail          string `json:"detail,omitempty"`
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
	latest, err := fetchLatestReleaseVersion(context.Background())
	status := buildUpdateStatus(Version, latest, err)

	if format := getFormat(cmd); format != "" {
		return output.Write(os.Stdout, format, status)
	}

	fmt.Printf("Current: %s\n", status.Current)
	if !status.Checked {
		fmt.Printf("Latest:  unknown\n\n%s\n", status.Detail)
		return nil
	}
	fmt.Printf("Latest:  %s\n", status.Latest)
	switch {
	case status.UpdateAvailable:
		fmt.Print("\nAn update is available. Upgrade:\n" +
			"  brew upgrade apresai/tap/twonb               # CLI\n" +
			"  brew upgrade --cask apresai/tap/secondbrain  # macOS app\n" +
			"  2nb plugin install                           # Obsidian plugin\n")
	case status.Detail != "":
		fmt.Printf("\n%s\n", status.Detail)
	default:
		fmt.Println("\nYou're up to date.")
	}
	return nil
}

// buildUpdateStatus derives the user-facing status from the running version, the
// fetched latest version, and any fetch error. Pure, so the comparison logic is
// unit-tested without touching the network.
func buildUpdateStatus(current, latest string, fetchErr error) UpdateStatus {
	s := UpdateStatus{Current: current}
	if fetchErr != nil {
		s.Detail = "couldn't check for updates (offline?): " + fetchErr.Error()
		return s
	}
	s.Checked = true
	s.Latest = latest
	if cmp, ok := comparePluginVersions(normalizeReleaseVersion(current), normalizeReleaseVersion(latest)); ok {
		s.UpdateAvailable = cmp < 0
	} else {
		// A dev / non-release build (Version == "dev") isn't comparable.
		s.Detail = fmt.Sprintf("this build (%s) isn't a released version, so it can't be compared to %s", current, latest)
	}
	return s
}

// normalizeReleaseVersion drops a leading "v" so a GitHub tag (v0.10.1) compares
// against the LDFLAGS-stamped Version (0.10.1).
func normalizeReleaseVersion(s string) string {
	return strings.TrimPrefix(strings.TrimSpace(s), "v")
}

// fetchLatestReleaseVersion returns the latest published release tag, cached for
// updateCacheTTL. A fresh cache short-circuits the network; a failed fetch falls
// back to a stale cache; only an empty cache + failed fetch returns an error.
func fetchLatestReleaseVersion(ctx context.Context) (string, error) {
	cachePath := updateCachePath()

	if cachePath != "" {
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
