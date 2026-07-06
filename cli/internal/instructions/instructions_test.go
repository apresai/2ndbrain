package instructions

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// tmpMem returns a path to a not-yet-created memory file inside a temp dir, so
// Install must create the parent .claude dir itself.
func tmpMem(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), ".claude", "CLAUDE.md")
}

func TestInstall_FreshFile(t *testing.T) {
	p := tmpMem(t)
	res, err := Install(p, false, false)
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if !res.Changed || !res.Installed {
		t.Fatalf("want changed+installed, got %+v", res)
	}
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	s := string(data)
	if !strings.Contains(s, beginPrefix) || !strings.Contains(s, endMarker) {
		t.Errorf("block markers missing:\n%s", s)
	}
	if !strings.Contains(s, "2ndbrain") {
		t.Errorf("block body missing:\n%s", s)
	}

	st, _ := Configured(p)
	if !st.Installed || !st.UpToDate || st.Modified {
		t.Errorf("fresh install status: %+v", st)
	}
}

func TestInstall_Idempotent(t *testing.T) {
	p := tmpMem(t)
	if _, err := Install(p, false, false); err != nil {
		t.Fatalf("first install: %v", err)
	}
	res, err := Install(p, false, false)
	if err != nil {
		t.Fatalf("second install: %v", err)
	}
	if res.Changed {
		t.Errorf("second install should be a no-op, got Changed=true")
	}
	if _, err := os.Stat(p + ".bak"); err == nil {
		t.Errorf(".bak must not be created on an idempotent re-install")
	}
}

func TestInstall_PreservesUserContent(t *testing.T) {
	p := tmpMem(t)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	user := "# My notes\n\nimportant stuff\n"
	if err := os.WriteFile(p, []byte(user), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(p, false, false); err != nil {
		t.Fatalf("install: %v", err)
	}
	data, _ := os.ReadFile(p)
	if !strings.Contains(string(data), "important stuff") {
		t.Errorf("user content lost:\n%s", data)
	}
	bak, err := os.ReadFile(p + ".bak")
	if err != nil || string(bak) != user {
		t.Errorf("backup should equal the original user content; got %q err=%v", bak, err)
	}
}

func TestInstall_RefusesHandEditWithoutForce(t *testing.T) {
	p := tmpMem(t)
	if _, err := Install(p, false, false); err != nil {
		t.Fatalf("install: %v", err)
	}
	data, _ := os.ReadFile(p)
	edited := strings.Replace(string(data), "2ndbrain", "HAND EDITED", 1)
	if err := os.WriteFile(p, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	if st, _ := Configured(p); !st.Modified {
		t.Fatalf("hand-edited block should report Modified; got %+v", st)
	}
	if _, err := Install(p, false, false); err == nil {
		t.Errorf("expected refusal on a hand-edited block without --force")
	}
	res, err := Install(p, true, false)
	if err != nil {
		t.Fatalf("force install: %v", err)
	}
	if !res.Changed {
		t.Errorf("--force should rewrite the block")
	}
	if st, _ := Configured(p); st.Modified || !st.UpToDate {
		t.Errorf("after --force the block should be current, got %+v", st)
	}
}

func TestUninstall(t *testing.T) {
	p := tmpMem(t)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("# keep me\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(p, false, false); err != nil {
		t.Fatalf("install: %v", err)
	}
	res, err := Uninstall(p)
	if err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if !res.Changed {
		t.Errorf("uninstall should report Changed")
	}
	data, _ := os.ReadFile(p)
	if strings.Contains(string(data), beginPrefix) {
		t.Errorf("block should be gone:\n%s", data)
	}
	if !strings.Contains(string(data), "# keep me") {
		t.Errorf("user content must survive uninstall:\n%s", data)
	}
	res2, _ := Uninstall(p)
	if res2.Changed {
		t.Errorf("second uninstall should be a no-op")
	}
}

func TestConfigured_Missing(t *testing.T) {
	st, err := Configured(tmpMem(t))
	if err != nil {
		t.Fatalf("configured: %v", err)
	}
	if st.Installed {
		t.Errorf("a missing file should report not installed")
	}
}

// TestInstall_MalformedBlockRefusedThenForce verifies a dangling BEGIN (no END)
// is refused without --force and never drops trailing user content, and that
// --force installs while preserving all surrounding content.
func TestInstall_MalformedBlockRefusedThenForce(t *testing.T) {
	p := tmpMem(t)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	corrupt := "# keep me\n\n" + beginPrefix + " | version: x | sha: y -->\nstuff\n\n# trailing user content\n"
	if err := os.WriteFile(p, []byte(corrupt), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(p, false, false); err == nil {
		t.Errorf("expected refusal on a malformed (BEGIN without END) block")
	}
	if data, _ := os.ReadFile(p); string(data) != corrupt {
		t.Errorf("file must be untouched on refusal:\n%s", data)
	}

	if _, err := Install(p, true, false); err != nil {
		t.Fatalf("force install over malformed block: %v", err)
	}
	data, _ := os.ReadFile(p)
	for _, must := range []string{"# keep me", "# trailing user content"} {
		if !strings.Contains(string(data), must) {
			t.Errorf("user content %q must survive a --force install:\n%s", must, data)
		}
	}
}

// TestInstall_VersionOnlyBumpNoRewrite documents that a version-only bump (same
// body, new Version stamp) is a no-op — no rewrite, no .bak churn.
func TestInstall_VersionOnlyBumpNoRewrite(t *testing.T) {
	old := Version
	t.Cleanup(func() { Version = old })
	p := tmpMem(t)
	Version = "1.0.0"
	if _, err := Install(p, false, false); err != nil {
		t.Fatalf("install: %v", err)
	}
	Version = "1.0.1"
	res, err := Install(p, false, false)
	if err != nil {
		t.Fatalf("re-install: %v", err)
	}
	if res.Changed {
		t.Errorf("version-only bump (same body) should not rewrite; got Changed=true")
	}
	if _, err := os.Stat(p + ".bak"); err == nil {
		t.Errorf("no .bak should be written on a version-only bump")
	}
}

func TestInstall_DryRun(t *testing.T) {
	p := tmpMem(t)
	res, err := Install(p, false, true)
	if err != nil {
		t.Fatalf("dry-run install: %v", err)
	}
	if !res.Changed {
		t.Errorf("dry-run on a fresh file should report Changed=true")
	}
	if _, err := os.Stat(p); err == nil {
		t.Errorf("dry-run must not write the file")
	}
}
