package skills

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func writeSkillFile(t *testing.T, home, content string) {
	t.Helper()
	dir := filepath.Join(home, ".claude", "skills", "2nb")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func swapProbes(t *testing.T, lookPath func(string) (string, error), probe func(string) (string, bool)) {
	t.Helper()
	oldLP, oldPV := skillLookPath, skillProbeVersion
	skillLookPath, skillProbeVersion = lookPath, probe
	t.Cleanup(func() { skillLookPath, skillProbeVersion = oldLP, oldPV })
}

func TestDoctor_InstalledFileValidBinaryResolves(t *testing.T) {
	home := t.TempDir()
	writeSkillFile(t, home, "---\nname: 2nb\ndescription: x\n---\n\nbody")
	swapProbes(t,
		func(string) (string, error) { return "/usr/local/bin/2nb", nil },
		func(string) (string, bool) { return "2nb version 9.9.9", true },
	)

	ver := Doctor("claude-code", "", home)
	if !ver.Installed || !ver.UserInstalled {
		t.Fatalf("should be user-installed: %+v", ver)
	}
	if !ver.FileNonEmpty || !ver.Parses {
		t.Errorf("SKILL.md should be non-empty + parse: %+v", ver)
	}
	if !ver.BinaryOnPath || !ver.BinaryOK || ver.BinaryVersion != "2nb version 9.9.9" {
		t.Errorf("2nb should resolve + report version: %+v", ver)
	}
}

func TestDoctor_BinaryNotOnPath(t *testing.T) {
	home := t.TempDir()
	writeSkillFile(t, home, "---\nname: 2nb\ndescription: x\n---\n\nbody")
	swapProbes(t,
		func(string) (string, error) { return "", exec.ErrNotFound },
		func(string) (string, bool) { return "", false },
	)

	ver := Doctor("claude-code", "", home)
	if ver.BinaryOnPath || ver.BinaryOK {
		t.Errorf("binary should not resolve when LookPath fails: %+v", ver)
	}
	if ver.SelfPath == "" {
		t.Error("self_path should still be reported for a self-vs-PATH mismatch")
	}
}

func TestDoctor_NotInstalled(t *testing.T) {
	home := t.TempDir() // no SKILL.md
	swapProbes(t,
		func(string) (string, error) { return "/usr/local/bin/2nb", nil },
		func(string) (string, bool) { return "2nb version 9.9.9", true },
	)
	ver := Doctor("claude-code", "", home)
	if ver.Installed {
		t.Errorf("should not be installed: %+v", ver)
	}
	if ver.FileNonEmpty || ver.Parses {
		t.Errorf("no file validation when not installed: %+v", ver)
	}
}

func TestDoctor_InstalledButEmptyFile(t *testing.T) {
	home := t.TempDir()
	writeSkillFile(t, home, "")
	swapProbes(t,
		func(string) (string, error) { return "/usr/local/bin/2nb", nil },
		func(string) (string, bool) { return "2nb version 9.9.9", true },
	)
	ver := Doctor("claude-code", "", home)
	if !ver.Installed {
		t.Fatal("an empty file still counts as installed (present)")
	}
	if ver.FileNonEmpty {
		t.Errorf("an empty SKILL.md must report file_nonempty=false: %+v", ver)
	}
}
