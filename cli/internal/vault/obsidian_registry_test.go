package vault

import (
	"os"
	"path/filepath"
	"testing"
)

// writeObsidianRegistry writes obsidian.json under the (temp) HOME so
// ObsidianOpenVault reads it via os.UserHomeDir().
func writeObsidianRegistry(t *testing.T, home, body string) {
	t.Helper()
	dir := filepath.Join(home, "Library", "Application Support", "obsidian")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir registry dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "obsidian.json"), []byte(body), 0o644); err != nil {
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
