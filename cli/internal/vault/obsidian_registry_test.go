package vault

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// writeObsidianRegistry writes obsidian.json at the per-OS location
// obsidianRegistryPath resolves (under the temp HOME), so the test exercises the
// same path ObsidianOpenVault reads on whatever platform it runs on.
func writeObsidianRegistry(t *testing.T, home, body string) {
	t.Helper()
	// On Linux obsidianRegistryPath honors XDG_CONFIG_HOME; pin it under the temp
	// HOME so the path is deterministic regardless of the real env.
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	p := obsidianRegistryPath()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("mkdir registry dir: %v", err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatalf("write registry: %v", err)
	}
}

func TestObsidianOpenVault_PrefersOpenFlagOverHigherTS(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	// The open vault has the LOWER ts: the open flag must still win, matching
	// the Swift registry (sort by ts desc, then first(open)).
	writeObsidianRegistry(t, home, `{"vaults":{
		"a":{"path":"/Users/x/archive","ts":1780434058390,"open":false},
		"b":{"path":"/Users/x/obsidian","ts":1700000000000,"open":true}
	}}`)
	if got := ObsidianOpenVault(); got != "/Users/x/obsidian" {
		t.Errorf("ObsidianOpenVault() = %q, want /Users/x/obsidian (open:true wins over higher ts)", got)
	}
}

func TestObsidianOpenVault_FallsBackToMostRecent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeObsidianRegistry(t, home, `{"vaults":{
		"a":{"path":"/Users/x/older","ts":100},
		"b":{"path":"/Users/x/newer","ts":200}
	}}`)
	if got := ObsidianOpenVault(); got != "/Users/x/newer" {
		t.Errorf("ObsidianOpenVault() = %q, want /Users/x/newer (highest ts when none open)", got)
	}
}

func TestObsidianOpenVault_AbsentEmptyMalformed(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if got := ObsidianOpenVault(); got != "" {
		t.Errorf("missing registry: got %q, want \"\"", got)
	}
	writeObsidianRegistry(t, home, "   \n")
	if got := ObsidianOpenVault(); got != "" {
		t.Errorf("empty registry: got %q, want \"\"", got)
	}
	writeObsidianRegistry(t, home, "{not json")
	if got := ObsidianOpenVault(); got != "" {
		t.Errorf("malformed registry: got %q, want \"\"", got)
	}
	writeObsidianRegistry(t, home, `{"vaults":{}}`)
	if got := ObsidianOpenVault(); got != "" {
		t.Errorf("no vaults: got %q, want \"\"", got)
	}
	writeObsidianRegistry(t, home, `{"vaults":{"a":{"path":"","ts":5,"open":true}}}`)
	if got := ObsidianOpenVault(); got != "" {
		t.Errorf("empty-path entry: got %q, want \"\"", got)
	}
}

func TestObsidianRegistryPath_PerOS(t *testing.T) {
	switch runtime.GOOS {
	case "linux":
		t.Setenv("XDG_CONFIG_HOME", "/tmp/xdgcfg")
		if got, want := obsidianRegistryPath(), "/tmp/xdgcfg/obsidian/obsidian.json"; got != want {
			t.Errorf("linux XDG path = %q, want %q", got, want)
		}
		t.Setenv("XDG_CONFIG_HOME", "")
		t.Setenv("HOME", "/home/u")
		if got, want := obsidianRegistryPath(), "/home/u/.config/obsidian/obsidian.json"; got != want {
			t.Errorf("linux default path = %q, want %q", got, want)
		}
	case "darwin":
		t.Setenv("HOME", "/Users/u")
		if got, want := obsidianRegistryPath(), "/Users/u/Library/Application Support/obsidian/obsidian.json"; got != want {
			t.Errorf("darwin path = %q, want %q", got, want)
		}
	case "windows":
		t.Setenv("APPDATA", `C:\Users\u\AppData\Roaming`)
		if got, want := obsidianRegistryPath(), filepath.Join(`C:\Users\u\AppData\Roaming`, "obsidian", "obsidian.json"); got != want {
			t.Errorf("windows path = %q, want %q", got, want)
		}
	}
	// Every supported OS returns a non-empty path ending in obsidian.json.
	if got := obsidianRegistryPath(); got == "" || filepath.Base(got) != "obsidian.json" {
		t.Errorf("obsidianRegistryPath() = %q, want a path ending in obsidian.json", got)
	}
}
