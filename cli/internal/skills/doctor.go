package skills

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Verification is the result of Doctor: a strict superset of InstallStatus (the
// fields flatten into the JSON via embedding) plus the verification signals. It
// reports "installed + dependencies resolve", NOT "the agent invoked it" — final
// proof requires the agent loading the SKILL.md, which no CLI can perform.
type Verification struct {
	InstallStatus
	Installed     bool   `json:"installed"`      // project OR user
	FileNonEmpty  bool   `json:"file_nonempty"`  // the installed SKILL.md has content
	Parses        bool   `json:"parses"`         // it has a frontmatter block
	BinaryOnPath  bool   `json:"binary_on_path"` // exec.LookPath("2nb") found it
	BinaryOK      bool   `json:"binary_ok"`      // and `2nb --version` ran
	BinaryVersion string `json:"binary_version"`
	SelfPath      string `json:"self_path"` // the running binary, for a self-vs-PATH mismatch
}

// Injected for tests: the default impls shell out. Tests substitute these to
// exercise the on-PATH / missing / version cases without a real install.
var (
	skillLookPath     = exec.LookPath
	skillProbeVersion = func(binPath string) (string, bool) {
		out, err := exec.Command(binPath, "--version").Output()
		if err != nil {
			return "", false
		}
		return strings.TrimSpace(string(out)), true
	}
)

// Doctor verifies one agent's skill for the vault: it is installed, its SKILL.md
// is non-empty and has frontmatter, and the `2nb` binary it shells to resolves
// on PATH (the way the agent's shell finds it — NOT os.Executable, which proves
// nothing about PATH) and runs.
func Doctor(slug, projectDir, homeDir string) Verification {
	var ver Verification
	for _, s := range ListStatuses(projectDir, homeDir) {
		if s.Slug == slug {
			ver.InstallStatus = s
			break
		}
	}
	ver.Installed = ver.ProjectInstalled || ver.UserInstalled

	if ver.Installed {
		var path string
		if ver.UserInstalled && ver.UserPath != "" {
			// ListStatuses stores UserPath as a display form ("~/…"); strip the
			// tilde prefix before joining with the real home directory.
			path = filepath.Join(homeDir, strings.TrimPrefix(ver.UserPath, "~/"))
		} else if ver.ProjectInstalled && projectDir != "" {
			path = filepath.Join(projectDir, ver.ProjectPath)
		}
		if path != "" {
			if data, err := os.ReadFile(path); err == nil {
				trimmed := strings.TrimSpace(string(data))
				ver.FileNonEmpty = len(trimmed) > 0
				ver.Parses = hasFrontmatter(trimmed)
			}
		}
	}

	if self, err := os.Executable(); err == nil {
		ver.SelfPath = self
	}
	if binPath, err := skillLookPath("2nb"); err == nil {
		ver.BinaryOnPath = true
		if v, ok := skillProbeVersion(binPath); ok {
			ver.BinaryOK = true
			ver.BinaryVersion = v
		}
	}
	return ver
}

// hasFrontmatter reports whether content opens with a YAML frontmatter block.
func hasFrontmatter(s string) bool {
	if !strings.HasPrefix(s, "---") {
		return false
	}
	return strings.Contains(s[3:], "\n---")
}
