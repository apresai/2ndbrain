package vault

import (
	"os"
	"path/filepath"
	"testing"
)

func writeRegistryForTest(t *testing.T, body string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, "Library", "Application Support", "obsidian")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "obsidian.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// An entry flagged open wins, with wasOpen=true, regardless of ts ordering.
func TestObsidianActiveVault_PrefersOpen(t *testing.T) {
	writeRegistryForTest(t, `{"vaults":{"a":{"path":"/va","ts":9,"open":false},"b":{"path":"/vb","ts":1,"open":true}}}`)
	if p, open := ObsidianActiveVault(); p != "/vb" || !open {
		t.Errorf("got (%q,%v), want (/vb,true)", p, open)
	}
	if k := ObsidianKnownVaults(); len(k) != 2 {
		t.Errorf("ObsidianKnownVaults = %v, want 2 entries", k)
	}
}

// With nothing flagged open, the most-recent (highest ts) wins, wasOpen=false.
func TestObsidianActiveVault_FallsBackToMostRecent(t *testing.T) {
	writeRegistryForTest(t, `{"vaults":{"a":{"path":"/va","ts":5,"open":false},"b":{"path":"/vb","ts":1,"open":false}}}`)
	if p, open := ObsidianActiveVault(); p != "/va" || open {
		t.Errorf("got (%q,%v), want (/va,false)", p, open)
	}
}

// No registry → empty results, never an error.
func TestObsidianActiveVault_NoRegistry(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if p, open := ObsidianActiveVault(); p != "" || open {
		t.Errorf("got (%q,%v), want empty", p, open)
	}
	if k := ObsidianKnownVaults(); k != nil {
		t.Errorf("ObsidianKnownVaults = %v, want nil", k)
	}
}
