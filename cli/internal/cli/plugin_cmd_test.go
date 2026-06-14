package cli

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPluginAssetURL(t *testing.T) {
	got := pluginAssetURL("v0.8.0", "main.js")
	want := "https://github.com/apresai/2ndbrain/releases/download/v0.8.0/main.js"
	if got != want {
		t.Errorf("pluginAssetURL = %q, want %q", got, want)
	}
}

func TestParseLatestReleaseTag(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		want    string
		wantErr bool
	}{
		{"valid", `{"tag_name":"v0.8.0","name":"v0.8.0"}`, "v0.8.0", false},
		{"missing tag", `{"name":"x"}`, "", true},
		{"garbage", `not json`, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseLatestReleaseTag([]byte(tt.body))
			if (err != nil) != tt.wantErr || got != tt.want {
				t.Errorf("parseLatestReleaseTag(%q) = (%q, %v), want (%q, err=%v)", tt.body, got, err, tt.want, tt.wantErr)
			}
		})
	}
}

func TestPluginManifestVersion(t *testing.T) {
	if v, err := pluginManifestVersion([]byte(`{"id":"obsidian-2ndbrain","version":"0.7.0"}`)); err != nil || v != "0.7.0" {
		t.Errorf("got (%q, %v), want (0.7.0, nil)", v, err)
	}
	if _, err := pluginManifestVersion([]byte(`{"id":"x"}`)); err == nil {
		t.Error("manifest without version should error")
	}
	if _, err := pluginManifestVersion([]byte(`broken`)); err == nil {
		t.Error("broken manifest should error")
	}
}

func TestInstalledPluginVersion(t *testing.T) {
	root := t.TempDir()

	// Not installed: empty version, no error.
	if v, err := installedPluginVersion(root); err != nil || v != "" {
		t.Errorf("uninstalled = (%q, %v), want (\"\", nil)", v, err)
	}

	// Installed: version read from the manifest.
	dir := pluginDir(root)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(`{"version":"0.7.0"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if v, err := installedPluginVersion(root); err != nil || v != "0.7.0" {
		t.Errorf("installed = (%q, %v), want (0.7.0, nil)", v, err)
	}
}

func TestComparePluginVersions(t *testing.T) {
	tests := []struct {
		a, b string
		want int
		ok   bool
	}{
		{"0.7.0", "0.8.0", -1, true},
		{"0.8.0", "0.8.0", 0, true},
		{"1.0.0", "0.9.9", 1, true},
		{"0.10.0", "0.9.0", 1, true},
		{"dev", "0.8.0", 0, false},
		{"0.8.0", "dev", 0, false},
		{"0.8", "0.8.0", 0, false},
	}
	for _, tt := range tests {
		got, ok := comparePluginVersions(tt.a, tt.b)
		if got != tt.want || ok != tt.ok {
			t.Errorf("comparePluginVersions(%q, %q) = (%d, %v), want (%d, %v)", tt.a, tt.b, got, ok, tt.want, tt.ok)
		}
	}
}

func TestPluginStatus_NotInstalledJSON(t *testing.T) {
	_, root := newContractVault(t)
	out, err := runCLIArgs(t, root, "plugin", "status", "--json")
	if err != nil {
		t.Fatalf("plugin status: %v", err)
	}
	var st PluginStatus
	if err := json.Unmarshal(out, &st); err != nil {
		t.Fatalf("decode %q: %v", out, err)
	}
	if st.Installed || st.InstalledVersion != "" {
		t.Errorf("fresh vault must report not installed, got %+v", st)
	}
	if !strings.Contains(st.PluginDir, filepath.Join(".obsidian", "plugins", "obsidian-2ndbrain")) {
		t.Errorf("plugin_dir = %q, want the vault plugin path", st.PluginDir)
	}
}

// seedPluginManifest writes a manifest with the given version into the
// vault's plugin directory.
func seedPluginManifest(t *testing.T, vaultRoot, version string) {
	t.Helper()
	dir := pluginDir(vaultRoot)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(`{"version":"`+version+`"}`), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestPluginStatus_InstalledJSON(t *testing.T) {
	_, root := newContractVault(t)
	seedPluginManifest(t, root, "0.0.1")

	out, err := runCLIArgs(t, root, "plugin", "status", "--json")
	if err != nil {
		t.Fatalf("plugin status: %v", err)
	}
	var st PluginStatus
	if err := json.Unmarshal(out, &st); err != nil {
		t.Fatalf("decode %q: %v", out, err)
	}
	if !st.Installed || st.InstalledVersion != "0.0.1" {
		t.Errorf("installed status = %+v, want installed at 0.0.1", st)
	}
	if st.CLIVersion != Version {
		t.Errorf("cli_version = %q, want %q", st.CLIVersion, Version)
	}
}

// TestPluginStatus_DriftHints pins the direction-aware human output: an
// older plugin nags toward plugin install, a newer plugin nags toward a
// CLI upgrade, and a dev (non-semver) CLI build stays silent.
func TestPluginStatus_DriftHints(t *testing.T) {
	_, root := newContractVault(t)
	seedPluginManifest(t, root, "0.0.1")
	origVersion := Version
	defer func() { Version = origVersion }()

	Version = "9.9.9" // plugin older than CLI
	out, err := runCLIArgs(t, root, "plugin", "status")
	if err != nil {
		t.Fatalf("plugin status: %v", err)
	}
	if !strings.Contains(string(out), "Update with: 2nb plugin install") {
		t.Errorf("older plugin should hint plugin install, got:\n%s", out)
	}

	Version = "0.0.0" // plugin newer than CLI
	out, err = runCLIArgs(t, root, "plugin", "status")
	if err != nil {
		t.Fatalf("plugin status: %v", err)
	}
	if !strings.Contains(string(out), "brew upgrade") {
		t.Errorf("newer plugin should hint a CLI upgrade, got:\n%s", out)
	}

	Version = "dev" // non-semver build: no nag either way
	out, err = runCLIArgs(t, root, "plugin", "status")
	if err != nil {
		t.Fatalf("plugin status: %v", err)
	}
	if strings.Contains(string(out), "Update with") || strings.Contains(string(out), "brew upgrade") {
		t.Errorf("dev build must not nag, got:\n%s", out)
	}
}

// TestPluginStatus_CorruptManifest pins current behavior: status reports
// the error loudly (install, by contrast, warns and reinstalls over it).
func TestPluginStatus_CorruptManifest(t *testing.T) {
	_, root := newContractVault(t)
	dir := pluginDir(root)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runCLIArgs(t, root, "plugin", "status"); err == nil {
		t.Error("corrupt manifest should surface as an error from plugin status")
	}
}

// isTransientGitHubErr reports whether a live-GitHub error is a transient
// infrastructure failure rather than a real bug: the unauthenticated rate limit
// (403/429, the connectivity probe and install share the same 60/hr bucket), a
// server-side 5xx (500/502/503/504, e.g. the GitHub 502 that blocked release
// v0.8.3), or a network-layer error (DNS, dial, TLS, reset, timeout) raised
// before any HTTP status is reached. A genuine defect is NOT transient: a 404
// from a wrong release/asset URL, a parse error, or an over-cap asset produces
// an error string that is intentionally not matched here, so it still fails.
// Kept as a pure predicate (no *testing.T) so the matching logic is table-testable.
func isTransientGitHubErr(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	// Rate-limit + server-side statuses carry the "HTTP <code>" shape that
	// fetchPluginAsset formats. 4xx other than 403/429 (notably 404) are real
	// bugs and intentionally absent.
	for _, code := range []string{"HTTP 403", "HTTP 429", "HTTP 500", "HTTP 502", "HTTP 503", "HTTP 504"} {
		if strings.Contains(msg, code) {
			return true
		}
	}
	// Network-layer failures (no HTTP status reached): http.Client.Do returns
	// these before any response. In a live-GitHub install test these are always
	// connectivity, not a logic bug.
	for _, sig := range []string{
		"no such host", "connection refused", "connection reset",
		"network is unreachable", "i/o timeout", "context deadline exceeded",
		"TLS handshake", "EOF", "timeout",
	} {
		if strings.Contains(msg, sig) {
			return true
		}
	}
	return false
}

// skipIfTransientGitHub skips (rather than fails) a live-GitHub step when the
// error is transient per isTransientGitHubErr.
func skipIfTransientGitHub(t *testing.T, err error) {
	t.Helper()
	if isTransientGitHubErr(err) {
		t.Skipf("GitHub transient failure mid-test: %v", err)
	}
}

func TestIsTransientGitHubErr(t *testing.T) {
	tests := []struct {
		name string
		msg  string
		want bool
	}{
		{"nil", "", false}, // handled via the nil-error case below
		{"rate limit 403", "GET https://api.github.com/x: HTTP 403 (GitHub rate limit?)", true},
		{"rate limit 429", "GET x: HTTP 429", true},
		{"server 500", "GET x: HTTP 500", true},
		{"server 502", "GET x: HTTP 502", true},
		{"server 503", "GET x: HTTP 503", true},
		{"server 504", "GET x: HTTP 504", true},
		{"dns", "Get \"https://github.com\": dial tcp: lookup github.com: no such host", true},
		{"refused", "dial tcp 1.2.3.4:443: connect: connection refused", true},
		{"reset", "read tcp: connection reset by peer", true},
		{"client timeout", "Get \"https://...\": context deadline exceeded (Client.Timeout exceeded while awaiting headers)", true},
		{"tls", "remote error: TLS handshake failure", true},
		{"truncated body", "unexpected EOF", true},
		// Real bugs: must NOT be treated as transient (so they fail loudly).
		{"not found 404", "GET https://github.com/.../main.js: HTTP 404", false},
		{"over-cap asset", "GET x: asset exceeds 16777216 bytes, refusing what looks like a wrong or corrupt download", false},
		{"bad manifest", "parse manifest.json: unexpected end of JSON input", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var err error
			if tt.msg != "" {
				err = errors.New(tt.msg)
			}
			if got := isTransientGitHubErr(err); got != tt.want {
				t.Errorf("isTransientGitHubErr(%q) = %v, want %v", tt.msg, got, tt.want)
			}
		})
	}
}

// TestPluginInstall_E2E_GitHub downloads the real release assets from GitHub
// into a temp vault (no-mock policy: real API, skip when unreachable).
func TestPluginInstall_E2E_GitHub(t *testing.T) {
	// Probe connectivity first so a network-less environment skips rather
	// than fails, while a real bug in the command still fails loudly.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/repos/apresai/2ndbrain/releases/latest", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Skipf("GitHub unreachable, skipping live install test: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Skipf("GitHub releases API returned %d, skipping", resp.StatusCode)
	}

	_, root := newContractVault(t)
	out, err := runCLIArgs(t, root, "plugin", "install")
	skipIfTransientGitHub(t, err)
	if err != nil {
		t.Fatalf("plugin install: %v", err)
	}
	if !strings.Contains(string(out), "Installed Obsidian plugin v") {
		t.Errorf("fresh install should report the installed version, got:\n%s", out)
	}

	for _, asset := range pluginAssets {
		if _, err := os.Stat(filepath.Join(pluginDir(root), asset)); err != nil {
			t.Errorf("asset %s missing after install: %v", asset, err)
		}
	}
	v, err := installedPluginVersion(root)
	if err != nil || v == "" {
		t.Errorf("installed version unreadable after install: (%q, %v)", v, err)
	}

	// Seed an older manifest to deterministically hit the updated branch.
	seedPluginManifest(t, root, "0.0.1")
	out, err = runCLIArgs(t, root, "plugin", "install")
	skipIfTransientGitHub(t, err)
	if err != nil {
		t.Fatalf("plugin install (update path): %v", err)
	}
	if !strings.Contains(string(out), "Updated Obsidian plugin v0.0.1 -> v") {
		t.Errorf("update should report old -> new, got:\n%s", out)
	}

	// Third run: already at the latest release.
	out, err = runCLIArgs(t, root, "plugin", "install")
	skipIfTransientGitHub(t, err)
	if err != nil {
		t.Fatalf("plugin install (already-latest path): %v", err)
	}
	if !strings.Contains(string(out), "already at v") {
		t.Errorf("repeat install should report already-at-latest, got:\n%s", out)
	}
}
