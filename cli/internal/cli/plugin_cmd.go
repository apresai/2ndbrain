package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/apresai/2ndbrain/internal/output"
	"github.com/spf13/cobra"
)

// The Obsidian plugin ships as three prebuilt release assets. `2nb plugin
// install` copies them into the open vault's .obsidian/plugins directory:
// the simplest install path for users who already have the CLI (no BRAT, no
// manual download). This is the one place 2nb writes outside its .2ndbrain
// sidecar; it touches only the plugin's own bundle, never notes or Obsidian
// settings, and only when explicitly invoked.
const (
	pluginRepo    = "apresai/2ndbrain"
	pluginDirName = "obsidian-2ndbrain"
	// pluginAssetLimit caps each downloaded asset; the bundle is ~20 KB
	// today, so anything near this is a wrong or corrupted asset.
	pluginAssetLimit = 16 << 20
)

// pluginAssets in install-write order: manifest.json LAST, so a partially
// written install never looks complete to Obsidian or `plugin status`.
var pluginAssets = []string{"main.js", "styles.css", "manifest.json"}

var pluginCmd = &cobra.Command{
	Use:   "plugin",
	Short: "Install and inspect the Obsidian plugin in the open vault",
	// Default action when invoked without a subcommand: show status.
	RunE: runPluginStatus,
}

var pluginStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show the installed Obsidian plugin version vs this CLI",
	RunE:  runPluginStatus,
}

var pluginInstallCmd = &cobra.Command{
	Use:     "install",
	Aliases: []string{"update"},
	Short:   "Install or update the Obsidian plugin in the open vault",
	Long: `Downloads the plugin's prebuilt assets (manifest.json, main.js,
styles.css) from the latest GitHub release into
<vault>/.obsidian/plugins/obsidian-2ndbrain/.

After a first install, one manual step remains (Obsidian has no API for it):
reload Obsidian, then enable "2ndbrain AI" under Settings > Community plugins.
Updates just need an Obsidian reload.`,
	RunE: runPluginInstall,
}

var pluginInstallForce bool

func init() {
	pluginCmd.AddCommand(pluginStatusCmd)
	pluginCmd.AddCommand(pluginInstallCmd)
	pluginInstallCmd.Flags().BoolVar(&pluginInstallForce, "force", false,
		"Install even if it would downgrade a newer installed plugin")
	pluginCmd.GroupID = "integr"
	rootCmd.AddCommand(pluginCmd)
}

// pluginDir returns the plugin bundle directory inside the vault.
func pluginDir(vaultRoot string) string {
	return filepath.Join(vaultRoot, ".obsidian", "plugins", pluginDirName)
}

// pluginManifestVersion extracts the version field from a plugin
// manifest.json payload.
func pluginManifestVersion(data []byte) (string, error) {
	var m struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return "", fmt.Errorf("parse manifest.json: %w", err)
	}
	if m.Version == "" {
		return "", fmt.Errorf("manifest.json has no version field")
	}
	return m.Version, nil
}

// installedPluginVersion reads the installed plugin's version, or "" when
// the plugin is not installed.
func installedPluginVersion(vaultRoot string) (string, error) {
	data, err := os.ReadFile(filepath.Join(pluginDir(vaultRoot), "manifest.json"))
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return pluginManifestVersion(data)
}

// pluginAssetURL builds the download URL for one release asset of a tag.
func pluginAssetURL(tag, asset string) string {
	return fmt.Sprintf("https://github.com/%s/releases/download/%s/%s", pluginRepo, tag, asset)
}

// parseLatestReleaseTag extracts tag_name from a GitHub releases/latest
// API response body.
func parseLatestReleaseTag(data []byte) (string, error) {
	var r struct {
		TagName string `json:"tag_name"`
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return "", fmt.Errorf("parse GitHub release response: %w", err)
	}
	if r.TagName == "" {
		return "", fmt.Errorf("GitHub release response has no tag_name")
	}
	return r.TagName, nil
}

var pluginHTTPClient = &http.Client{Timeout: 30 * time.Second}

// fetchPluginAsset GETs a URL and returns its body, size-capped.
func fetchPluginAsset(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "2nb/"+Version)
	resp, err := pluginHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		hint := ""
		if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests {
			hint = " (GitHub rate limit? Wait a few minutes and retry)"
		}
		return nil, fmt.Errorf("GET %s: HTTP %d%s", url, resp.StatusCode, hint)
	}
	// Read one byte past the cap so an over-limit body errors instead of
	// being silently truncated into a corrupt asset.
	data, err := io.ReadAll(io.LimitReader(resp.Body, pluginAssetLimit+1))
	if err != nil {
		return nil, err
	}
	if len(data) > pluginAssetLimit {
		return nil, fmt.Errorf("GET %s: asset exceeds %d bytes, refusing what looks like a wrong or corrupt download", url, pluginAssetLimit)
	}
	return data, nil
}

// comparePluginVersions compares two x.y.z strings. Returns -1/0/1 and
// ok=false when either side is not numeric x.y.z (e.g. a dev build).
func comparePluginVersions(a, b string) (int, bool) {
	parse := func(s string) ([3]int, bool) {
		var p [3]int
		parts := strings.Split(s, ".")
		if len(parts) != 3 {
			return p, false
		}
		for i, raw := range parts {
			n, err := strconv.Atoi(raw)
			if err != nil {
				return p, false
			}
			p[i] = n
		}
		return p, true
	}
	av, aok := parse(a)
	bv, bok := parse(b)
	if !aok || !bok {
		return 0, false
	}
	for i := 0; i < 3; i++ {
		if av[i] != bv[i] {
			if av[i] < bv[i] {
				return -1, true
			}
			return 1, true
		}
	}
	return 0, true
}

// PluginStatus is the JSON shape of `2nb plugin status --json`.
type PluginStatus struct {
	Installed        bool   `json:"installed"`
	InstalledVersion string `json:"installed_version,omitempty"`
	CLIVersion       string `json:"cli_version"`
	PluginDir        string `json:"plugin_dir"`
}

func runPluginStatus(cmd *cobra.Command, args []string) error {
	v, err := openVault()
	if err != nil {
		return err
	}
	defer v.Close()

	installed, err := installedPluginVersion(v.Root)
	if err != nil {
		return fmt.Errorf("read installed plugin: %w", err)
	}

	status := PluginStatus{
		Installed:        installed != "",
		InstalledVersion: installed,
		CLIVersion:       Version,
		PluginDir:        pluginDir(v.Root),
	}

	format := getFormat(cmd)
	if format != "" {
		return output.Write(os.Stdout, format, status)
	}

	if !status.Installed {
		fmt.Println("Obsidian plugin: not installed")
		fmt.Println("Install it with: 2nb plugin install")
		return nil
	}
	fmt.Printf("Obsidian plugin: v%s installed\n", status.InstalledVersion)
	cliDisplay := status.CLIVersion
	if _, ok := comparePluginVersions(cliDisplay, "0.0.0"); ok {
		cliDisplay = "v" + cliDisplay
	}
	fmt.Printf("CLI version:     %s\n", cliDisplay)
	// Direction-aware drift hint; silent for non-semver CLI builds (dev).
	if cmp, ok := comparePluginVersions(status.InstalledVersion, Version); ok {
		switch {
		case cmp < 0:
			fmt.Println("Update with: 2nb plugin install")
		case cmp > 0:
			fmt.Println("The plugin is newer than this CLI. Update the CLI with: brew upgrade apresai/tap/twonb")
		}
	}
	return nil
}

func runPluginInstall(cmd *cobra.Command, args []string) error {
	v, err := openVault()
	if err != nil {
		return err
	}
	defer v.Close()
	setupFileLogging(v)

	ctx := context.Background()
	dir := pluginDir(v.Root)
	previous, err := installedPluginVersion(v.Root)
	if err != nil {
		// A corrupt manifest shouldn't block reinstalling over it.
		fmt.Fprintf(os.Stderr, "warning: existing plugin manifest unreadable (%v); reinstalling\n", err)
		previous = ""
	}

	fmt.Fprintln(os.Stderr, "Resolving latest release...")
	body, err := fetchPluginAsset(ctx, "https://api.github.com/repos/"+pluginRepo+"/releases/latest")
	if err != nil {
		return fmt.Errorf("resolve latest release: %w\n\nOffline? Install manually: download manifest.json, main.js, styles.css from\nhttps://github.com/%s/releases into %s", err, pluginRepo, dir)
	}
	tag, err := parseLatestReleaseTag(body)
	if err != nil {
		return err
	}

	// No-downgrade guard: if the installed plugin is newer than the latest
	// release (a prerelease/promotion lag, or GitHub's /releases/latest lagging),
	// installing would DOWNGRADE it. Refuse unless --force. This protects every
	// caller of the install path — the plugin's "Update plugin" button, the macOS
	// app, and manual CLI use.
	if previous != "" && !pluginInstallForce {
		if cmp, ok := comparePluginVersions(normalizeReleaseVersion(previous), normalizeReleaseVersion(tag)); ok && cmp > 0 {
			fmt.Printf("Obsidian plugin v%s is newer than the latest release %s; not downgrading. Use --force to override.\n", previous, tag)
			return nil
		}
	}

	fmt.Fprintf(os.Stderr, "Installing plugin %s into %s\n", tag, dir)

	// Download everything before writing anything: a failed fetch must
	// never leave a mixed-version bundle on disk (updates would otherwise
	// run new main.js under the old manifest).
	downloaded := make(map[string][]byte, len(pluginAssets))
	for _, asset := range pluginAssets {
		data, err := fetchPluginAsset(ctx, pluginAssetURL(tag, asset))
		if err != nil {
			return fmt.Errorf("download %s: %w", asset, err)
		}
		downloaded[asset] = data
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create plugin directory: %w", err)
	}
	for _, asset := range pluginAssets {
		if err := os.WriteFile(filepath.Join(dir, asset), downloaded[asset], 0o644); err != nil {
			return fmt.Errorf("write %s: %w", asset, err)
		}
	}

	installed, err := installedPluginVersion(v.Root)
	if err != nil {
		return fmt.Errorf("verify install: %w", err)
	}

	if previous == "" {
		fmt.Printf("Installed Obsidian plugin v%s.\n\nTwo steps left in Obsidian (it has no API for them):\n  1. Reload Obsidian (Cmd+R).\n  2. Enable \"2ndbrain AI\" under Settings > Community plugins.\n", installed)
	} else if previous == installed {
		fmt.Printf("Obsidian plugin already at v%s (latest release). Reload Obsidian if it's running.\n", installed)
	} else {
		fmt.Printf("Updated Obsidian plugin v%s -> v%s. Reload Obsidian (Cmd+R) to pick it up.\n", previous, installed)
	}
	return nil
}
