package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAgentBySlug(t *testing.T) {
	tests := []struct {
		slug  string
		found bool
		name  string
	}{
		{"agents", true, "Agents (cross-tool standard)"},
		{"claude-code", true, "Claude Code"},
		{"cursor", true, "Cursor"},
		{"windsurf", true, "Windsurf"},
		{"github-copilot", true, "GitHub Copilot"},
		{"kiro", true, "Kiro"},
		{"cline", true, "Cline"},
		{"roo-code", true, "Roo Code"},
		{"junie", true, "JetBrains Junie"},
		{"warp", true, "Warp"},
		{"codex", true, "Codex"},
		{"nonexistent", false, ""},
	}

	for _, tt := range tests {
		t.Run(tt.slug, func(t *testing.T) {
			a, ok := AgentBySlug(tt.slug)
			if ok != tt.found {
				t.Fatalf("AgentBySlug(%q) found=%v, want %v", tt.slug, ok, tt.found)
			}
			if ok && a.Name != tt.name {
				t.Fatalf("AgentBySlug(%q).Name=%q, want %q", tt.slug, a.Name, tt.name)
			}
		})
	}
}

// The cross-tool "agents" entry targets .agents/skills/2nb/SKILL.md (Warp's
// recommended primary), for both project and user scope.
func TestAgentsCrossToolPath(t *testing.T) {
	a, ok := AgentBySlug("agents")
	if !ok {
		t.Fatal("AgentBySlug(\"agents\") not found")
	}
	const want = ".agents/skills/2nb/SKILL.md"
	if a.ProjectPath != want || a.UserPath != want {
		t.Fatalf("agents paths = (%q, %q), want both %q", a.ProjectPath, a.UserPath, want)
	}
}

func TestCodexSkillPath(t *testing.T) {
	a, ok := AgentBySlug("codex")
	if !ok {
		t.Fatal("AgentBySlug(\"codex\") not found")
	}
	const want = ".codex/skills/2nb/SKILL.md"
	if a.ProjectPath != want || a.UserPath != want {
		t.Fatalf("codex paths = (%q, %q), want both %q", a.ProjectPath, a.UserPath, want)
	}
}

// InstallWithBackup writes a .bak only when a force-overwrite replaces an
// existing file whose content differs from what we're writing.
func TestInstallBackupOnForce(t *testing.T) {
	base := t.TempDir()
	a, _ := AgentBySlug("claude-code")
	abs := a.InstallPath(true, base)

	// Fresh install: no backup.
	if bak, err := InstallWithBackup(base, *a, true, false); err != nil || bak != "" {
		t.Fatalf("fresh install should have no backup: bak=%q err=%v", bak, err)
	}
	// Hand-edit, then force-overwrite: a .bak with the old content.
	if err := os.WriteFile(abs, []byte("HAND EDIT\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bak, err := InstallWithBackup(base, *a, true, true)
	if err != nil {
		t.Fatalf("force install: %v", err)
	}
	if bak == "" {
		t.Fatal("force-overwrite of a differing file must write a .bak")
	}
	if data, _ := os.ReadFile(bak); string(data) != "HAND EDIT\n" {
		t.Errorf("backup should hold the old content; got %q", data)
	}
	// Re-force with now-identical content: no new backup.
	if bak2, err := InstallWithBackup(base, *a, true, true); err != nil || bak2 != "" {
		t.Errorf("force-overwrite of identical content should not back up: bak=%q err=%v", bak2, err)
	}
}

// RefreshIfStale re-installs a stale managed copy WITHOUT writing a .bak (so a
// brew-upgrade auto-refresh doesn't accumulate one per upgrade).
func TestRefreshIfStaleNoBackup(t *testing.T) {
	base := t.TempDir()
	a, _ := AgentBySlug("claude-code")
	abs := a.InstallPath(true, base)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	// Fabricate a stale install: a different body, stamped with a claimed sha that
	// matches THAT body (so it's unmodified) but differs from the current content.
	oldBody := "---\nname: 2nb\ndescription: old\n---\n\n# Old body\n"
	stale := injectStampForTest(oldBody, "0.0.1", sha256Hex(oldBody))
	if err := os.WriteFile(abs, []byte(stale), 0o644); err != nil {
		t.Fatal(err)
	}
	refreshed, err := RefreshIfStale(base, *a, true)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if !refreshed {
		t.Fatal("expected a stale refresh")
	}
	if _, err := os.Stat(abs + ".bak"); !os.IsNotExist(err) {
		t.Error("auto-refresh must not leave a .bak")
	}
	if data, _ := os.ReadFile(abs); string(data) != StampedContent() {
		t.Error("refreshed file should hold the current stamped content")
	}
}

func TestIsSourceRepoRoot(t *testing.T) {
	dir := t.TempDir()
	if IsSourceRepoRoot(dir) {
		t.Error("empty dir is not the source repo root")
	}
	marker := filepath.Join(dir, "cli", "internal", "skills", "content", "2ndbrain-skill.md")
	if err := os.MkdirAll(filepath.Dir(marker), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(marker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !IsSourceRepoRoot(dir) {
		t.Error("dir with the embedded skill source should be the source repo root")
	}
}

func TestAllAgentsUseSKILLmd(t *testing.T) {
	for _, a := range Agents {
		t.Run(a.Slug+"/project", func(t *testing.T) {
			if !strings.HasSuffix(a.ProjectPath, "/SKILL.md") {
				t.Fatalf("ProjectPath %q does not end with /SKILL.md", a.ProjectPath)
			}
		})
		t.Run(a.Slug+"/user", func(t *testing.T) {
			if !strings.HasSuffix(a.UserPath, "/SKILL.md") {
				t.Fatalf("UserPath %q does not end with /SKILL.md", a.UserPath)
			}
		})
	}
}

func TestRenderContent(t *testing.T) {
	for _, a := range Agents {
		t.Run(a.Slug, func(t *testing.T) {
			content := a.RenderContent()
			if !strings.HasPrefix(content, "---\nname: 2nb\n") {
				got := content
				if len(got) > 80 {
					got = got[:80] + "..."
				}
				t.Fatalf("content should start with SKILL.md frontmatter, got: %q", got)
			}
			if !strings.Contains(content, "## CLI Commands") {
				t.Fatal("content missing '## CLI Commands' section")
			}
			if !strings.Contains(content, "## MCP Server Tools") {
				t.Fatal("content missing '## MCP Server Tools' section")
			}
		})
	}
}

func TestInstallProject(t *testing.T) {
	root := t.TempDir()

	for _, a := range Agents {
		t.Run(a.Slug, func(t *testing.T) {
			if IsInstalled(root, a, false) {
				t.Fatal("should not be installed initially")
			}

			if err := Install(root, a, false, false); err != nil {
				t.Fatalf("Install: %v", err)
			}

			abs := filepath.Join(root, a.ProjectPath)
			data, err := os.ReadFile(abs)
			if err != nil {
				t.Fatalf("ReadFile: %v", err)
			}
			if !strings.HasPrefix(string(data), "---\nname: 2nb\n") {
				t.Fatal("installed file missing SKILL.md frontmatter")
			}

			if !IsInstalled(root, a, false) {
				t.Fatal("should be installed after Install")
			}

			// Double install
			if err := Install(root, a, false, false); err != ErrAlreadyInstalled {
				t.Fatalf("double Install got %v, want ErrAlreadyInstalled", err)
			}

			// Force
			if err := Install(root, a, false, true); err != nil {
				t.Fatalf("force Install: %v", err)
			}

			// Uninstall
			if err := Uninstall(root, a, false); err != nil {
				t.Fatalf("Uninstall: %v", err)
			}
			if IsInstalled(root, a, false) {
				t.Fatal("should not be installed after Uninstall")
			}

			// Uninstall again (no-op)
			if err := Uninstall(root, a, false); err != nil {
				t.Fatalf("double Uninstall: %v", err)
			}
		})
	}
}

func TestInstallUser(t *testing.T) {
	home := t.TempDir()

	a, _ := AgentBySlug("claude-code")

	if IsInstalled(home, *a, true) {
		t.Fatal("should not be installed initially")
	}

	if err := Install(home, *a, true, false); err != nil {
		t.Fatalf("Install: %v", err)
	}

	abs := filepath.Join(home, a.UserPath)
	if _, err := os.Stat(abs); err != nil {
		t.Fatalf("user skill file not found: %v", err)
	}

	if !IsInstalled(home, *a, true) {
		t.Fatal("should be installed after user Install")
	}

	if err := Uninstall(home, *a, true); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	if IsInstalled(home, *a, true) {
		t.Fatal("should not be installed after Uninstall")
	}
}

func TestWindsurfUserPath(t *testing.T) {
	a, _ := AgentBySlug("windsurf")
	// Windsurf global skills go to ~/.codeium/windsurf/skills/, not ~/.windsurf/
	if !strings.HasPrefix(a.UserPath, ".codeium/windsurf/") {
		t.Fatalf("Windsurf UserPath should start with .codeium/windsurf/, got %q", a.UserPath)
	}
}

func TestListStatuses(t *testing.T) {
	projectDir := t.TempDir()
	homeDir := t.TempDir()

	// All should be not-installed
	statuses := ListStatuses(projectDir, homeDir)
	if len(statuses) != len(Agents) {
		t.Fatalf("ListStatuses returned %d, want %d", len(statuses), len(Agents))
	}
	for _, s := range statuses {
		if s.ProjectInstalled || s.UserInstalled {
			t.Fatalf("%s should not be installed", s.Slug)
		}
	}

	// Install kiro at project level
	a, _ := AgentBySlug("kiro")
	Install(projectDir, *a, false, false)

	// Install claude-code at user level
	b, _ := AgentBySlug("claude-code")
	Install(homeDir, *b, true, false)

	statuses = ListStatuses(projectDir, homeDir)
	for _, s := range statuses {
		switch s.Slug {
		case "kiro":
			if !s.ProjectInstalled {
				t.Fatal("kiro should be project-installed")
			}
			if s.UserInstalled {
				t.Fatal("kiro should not be user-installed")
			}
		case "claude-code":
			if s.ProjectInstalled {
				t.Fatal("claude-code should not be project-installed")
			}
			if !s.UserInstalled {
				t.Fatal("claude-code should be user-installed")
			}
		default:
			if s.ProjectInstalled || s.UserInstalled {
				t.Fatalf("%s should not be installed", s.Slug)
			}
		}
	}
}

func TestListStatusesNoVault(t *testing.T) {
	homeDir := t.TempDir()

	// Empty projectDir — only user statuses checked
	statuses := ListStatuses("", homeDir)
	if len(statuses) != len(Agents) {
		t.Fatalf("ListStatuses returned %d, want %d", len(statuses), len(Agents))
	}
	for _, s := range statuses {
		if s.ProjectInstalled {
			t.Fatalf("%s project should not be installed with empty projectDir", s.Slug)
		}
	}
}

func TestCursorHasNightlyNote(t *testing.T) {
	a, _ := AgentBySlug("cursor")
	if !strings.Contains(a.Note, "Nightly") {
		t.Fatalf("cursor Note should mention Nightly, got: %q", a.Note)
	}
}

func TestCoreContentNotEmpty(t *testing.T) {
	if len(coreContent) < 100 {
		t.Fatalf("embedded core content too short: %d bytes", len(coreContent))
	}
}
