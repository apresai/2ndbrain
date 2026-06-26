package skills

import (
	"os"
	"strings"
	"testing"
)

// injectStampForTest mirrors StampedContent's frontmatter injection with explicit
// version/sha, to fabricate older (stale) installs.
func injectStampForTest(content, version, sha string) string {
	stamp := stampVersionKey + ": " + version + "\n" + stampSHAKey + ": " + sha + "\n"
	const open = "---\n"
	i := strings.Index(content[len(open):], "\n---")
	insertAt := len(open) + i + 1
	return content[:insertAt] + stamp + content[insertAt:]
}

func setTestVersion(t *testing.T, v string) {
	t.Helper()
	old := Version
	Version = v
	t.Cleanup(func() { Version = old })
}

func TestStampedContent_RoundTrips(t *testing.T) {
	setTestVersion(t, "9.9.9")
	s := StampedContent()
	if !strings.HasPrefix(s, "---\nname: 2nb\n") {
		t.Fatalf("stamped content lost its frontmatter prefix")
	}
	if !strings.Contains(s, stampVersionKey+": 9.9.9") || !strings.Contains(s, stampSHAKey+": ") {
		t.Fatalf("stamp keys missing from:\n%s", s[:200])
	}
	if stripStamp(s) != coreContent {
		t.Fatalf("stripStamp(StampedContent) must restore the canonical body")
	}
	f := FreshnessOf([]byte(s))
	if !f.Stamped || !f.UpToDate || f.Modified || f.InstalledVersion != "9.9.9" {
		t.Fatalf("fresh stamped content misclassified: %+v", f)
	}
}

func TestFreshnessOf_Unstamped(t *testing.T) {
	if f := FreshnessOf([]byte(coreContent)); f.Stamped {
		t.Fatalf("unstamped content should report Stamped=false: %+v", f)
	}
}

func TestFreshnessOf_StaleUnmodified(t *testing.T) {
	setTestVersion(t, "9.9.9")
	old := coreContent + "\n<!-- legacy -->\n" // an older install's body
	stale := injectStampForTest(old, "0.0.1", sha256Hex(old))
	f := FreshnessOf([]byte(stale))
	if !f.Stamped || f.UpToDate || f.Modified {
		t.Fatalf("stale-unmodified misclassified: %+v", f)
	}
}

func TestFreshnessOf_Modified(t *testing.T) {
	setTestVersion(t, "9.9.9")
	if f := FreshnessOf([]byte(StampedContent() + "\nhand edit\n")); !f.Modified {
		t.Fatalf("hand-edited content should report Modified=true: %+v", f)
	}
}

func TestDoctor_ReportsFreshness(t *testing.T) {
	setTestVersion(t, "9.9.9")
	home := t.TempDir()
	a, ok := AgentBySlug("claude-code")
	if !ok {
		t.Fatal("claude-code agent missing")
	}
	if err := Install(home, *a, true, false); err != nil {
		t.Fatal(err)
	}

	// Freshly installed → stamped + up to date.
	if f := Doctor("claude-code", "", home).Freshness; !f.Stamped || !f.UpToDate || f.Modified {
		t.Fatalf("fresh install should report up to date: %+v", f)
	}

	// Overwrite with an older (stale, unmodified) stamp → reported out of date.
	path := a.InstallPath(true, home)
	old := coreContent + "\n<!-- legacy -->\n"
	if err := os.WriteFile(path, []byte(injectStampForTest(old, "0.0.1", sha256Hex(old))), 0o644); err != nil {
		t.Fatal(err)
	}
	if f := Doctor("claude-code", "", home).Freshness; !f.Stamped || f.UpToDate {
		t.Fatalf("stale install should report not-up-to-date: %+v", f)
	}
}

func TestRefreshIfStale(t *testing.T) {
	setTestVersion(t, "9.9.9")
	home := t.TempDir()
	a := Agent{Slug: "t", Name: "T", UserPath: ".t/skills/2nb/SKILL.md", ProjectPath: ".t/skills/2nb/SKILL.md"}
	if err := Install(home, a, true, false); err != nil {
		t.Fatal(err)
	}

	// A fresh install is up to date → no refresh.
	if ok, err := RefreshIfStale(home, a, true); err != nil || ok {
		t.Fatalf("fresh install should not refresh: ok=%v err=%v", ok, err)
	}

	// Make it stale-but-unmodified, then refresh.
	path := a.InstallPath(true, home)
	old := coreContent + "\n<!-- legacy -->\n"
	if err := os.WriteFile(path, []byte(injectStampForTest(old, "0.0.1", sha256Hex(old))), 0o644); err != nil {
		t.Fatal(err)
	}
	if ok, err := RefreshIfStale(home, a, true); err != nil || !ok {
		t.Fatalf("stale install should refresh: ok=%v err=%v", ok, err)
	}
	if data, _ := os.ReadFile(path); !FreshnessOf(data).UpToDate {
		t.Fatalf("after refresh the install should be up to date")
	}

	// A hand-edited (modified) install must never be auto-clobbered.
	if err := os.WriteFile(path, []byte(StampedContent()+"\nhand edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if ok, _ := RefreshIfStale(home, a, true); ok {
		t.Fatalf("hand-edited install must not be auto-refreshed")
	}
}
