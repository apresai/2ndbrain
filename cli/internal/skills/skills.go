package skills

import (
	_ "embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

//go:embed content/2ndbrain-skill.md
var coreContent string

// Agent describes a supported AI coding agent.
type Agent struct {
	Slug        string
	Name        string
	ProjectPath string // relative to project/vault root (e.g. ".claude/skills/2nb/SKILL.md")
	UserPath    string // relative to home dir (e.g. ".claude/skills/2nb/SKILL.md")
	Note        string // advisory printed after install
}

// InstallStatus is the JSON-serializable result for skills list.
type InstallStatus struct {
	Slug             string `json:"slug"`
	Name             string `json:"name"`
	ProjectPath      string `json:"project_path"`
	UserPath         string `json:"user_path"`
	ProjectInstalled bool   `json:"project_installed"`
	UserInstalled    bool   `json:"user_installed"`
	Note             string `json:"note,omitempty"`
}

// ErrAlreadyInstalled is returned when a skill file already exists and force is false.
var ErrAlreadyInstalled = errors.New("skill already installed (use --force to overwrite)")

// Agents is the full registry of supported coding agents.
// All use the open SKILL.md standard with name/description frontmatter.
var Agents = []Agent{
	{Slug: "agents", Name: "Agents (cross-tool standard)",
		ProjectPath: ".agents/skills/2nb/SKILL.md",
		UserPath:    ".agents/skills/2nb/SKILL.md",
		Note:        "The cross-tool .agents/skills standard; read by Warp (its recommended primary) and other agents. In this repo this path is a committed mirror kept in sync by `make sync-skills` — don't `skills install agents` here, it would stamp the mirror."},
	{Slug: "claude-code", Name: "Claude Code",
		ProjectPath: ".claude/skills/2nb/SKILL.md",
		UserPath:    ".claude/skills/2nb/SKILL.md",
		Note:        "Claude Desktop reads the same ~/.claude/skills path, so this install also serves Claude Desktop."},
	{Slug: "cursor", Name: "Cursor",
		ProjectPath: ".cursor/skills/2nb/SKILL.md",
		UserPath:    ".cursor/skills/2nb/SKILL.md",
		Note:        "Cursor skills require the Nightly release channel."},
	{Slug: "windsurf", Name: "Windsurf",
		ProjectPath: ".windsurf/skills/2nb/SKILL.md",
		UserPath:    ".codeium/windsurf/skills/2nb/SKILL.md"},
	{Slug: "github-copilot", Name: "GitHub Copilot",
		ProjectPath: ".github/skills/2nb/SKILL.md",
		UserPath:    ".copilot/skills/2nb/SKILL.md"},
	{Slug: "kiro", Name: "Kiro",
		ProjectPath: ".kiro/skills/2nb/SKILL.md",
		UserPath:    ".kiro/skills/2nb/SKILL.md"},
	{Slug: "cline", Name: "Cline",
		ProjectPath: ".cline/skills/2nb/SKILL.md",
		UserPath:    ".cline/skills/2nb/SKILL.md"},
	{Slug: "roo-code", Name: "Roo Code",
		ProjectPath: ".roo/skills/2nb/SKILL.md",
		UserPath:    ".roo/skills/2nb/SKILL.md"},
	{Slug: "junie", Name: "JetBrains Junie",
		ProjectPath: ".junie/skills/2nb/SKILL.md",
		UserPath:    ".junie/skills/2nb/SKILL.md"},
	{Slug: "warp", Name: "Warp",
		ProjectPath: ".warp/skills/2nb/SKILL.md",
		UserPath:    ".warp/skills/2nb/SKILL.md",
		Note:        "Warp also reads ~/.claude/skills, so a Claude Code install is already visible in Warp."},
	{Slug: "codex", Name: "Codex",
		ProjectPath: ".codex/skills/2nb/SKILL.md",
		UserPath:    ".codex/skills/2nb/SKILL.md",
		Note:        "Codex also reads ~/.agents/skills, so an `agents` install is already visible in Codex."},
}

// AgentBySlug returns the agent with the given slug, or false if not found.
func AgentBySlug(slug string) (*Agent, bool) {
	for i := range Agents {
		if Agents[i].Slug == slug {
			return &Agents[i], true
		}
	}
	return nil, false
}

// RenderContent returns the SKILL.md content. All agents use the same
// open SKILL.md standard with name/description frontmatter.
func (a *Agent) RenderContent() string {
	return coreContent
}

// InstallPath returns the resolved install path for the given scope.
// For user scope, baseDir should be the home directory.
// For project scope, baseDir should be the vault/project root.
func (a *Agent) InstallPath(user bool, baseDir string) string {
	if user {
		return filepath.Join(baseDir, a.UserPath)
	}
	return filepath.Join(baseDir, a.ProjectPath)
}

// IsInstalled reports whether the skill file exists.
func IsInstalled(baseDir string, a Agent, user bool) bool {
	var rel string
	if user {
		rel = a.UserPath
	} else {
		rel = a.ProjectPath
	}
	_, err := os.Stat(filepath.Join(baseDir, rel))
	return err == nil
}

// Install writes the skill file for the agent under baseDir.
// Creates intermediate directories as needed. Returns ErrAlreadyInstalled
// if the file exists and force is false. On a force-overwrite of an existing,
// differing file it first writes a "<path>.bak" backup.
func Install(baseDir string, a Agent, user bool, force bool) error {
	_, err := InstallWithBackup(baseDir, a, user, force)
	return err
}

// InstallWithBackup is Install that also reports the path of any backup written.
// When force overwrites an EXISTING SKILL.md whose content differs from what we'd
// write, the old bytes are first copied to "<path>.bak" so a user's hand-edits
// (or a prior version) stay recoverable. Returns ErrAlreadyInstalled when the
// file exists and force is false.
func InstallWithBackup(baseDir string, a Agent, user bool, force bool) (backupPath string, err error) {
	abs := a.InstallPath(user, baseDir)
	if !force {
		if _, statErr := os.Stat(abs); statErr == nil {
			return "", ErrAlreadyInstalled
		}
	}
	return writeManaged(abs, true)
}

// writeManaged writes the stamped SKILL.md to abs atomically (PID-suffixed temp +
// rename, so a concurrent refresh can't clobber the other's temp). When backup is
// true and abs already exists with content different from what we're writing, the
// existing bytes are first copied to "<abs>.bak" (preserving mode). Returns the
// backup path written ("" if none). RefreshIfStale passes backup=false so a
// silent brew-upgrade auto-refresh never litters .bak files.
func writeManaged(abs string, backup bool) (backupPath string, err error) {
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return "", fmt.Errorf("create directory: %w", err)
	}
	content := StampedContent()
	if backup {
		if existing, rerr := os.ReadFile(abs); rerr == nil && string(existing) != content {
			mode := os.FileMode(0o644)
			if fi, serr := os.Stat(abs); serr == nil {
				mode = fi.Mode().Perm()
			}
			backupPath = abs + ".bak"
			if berr := os.WriteFile(backupPath, existing, mode); berr != nil {
				return "", fmt.Errorf("write backup %s: %w", backupPath, berr)
			}
		}
	}
	tmp := fmt.Sprintf("%s.tmp.%d", abs, os.Getpid())
	if err := os.WriteFile(tmp, []byte(content), 0o644); err != nil {
		return backupPath, fmt.Errorf("write: %w", err)
	}
	if err := os.Rename(tmp, abs); err != nil {
		os.Remove(tmp)
		return backupPath, fmt.Errorf("rename: %w", err)
	}
	return backupPath, nil
}

// IsSourceRepoRoot reports whether dir is the 2ndbrain source tree, detected by
// the embedded skill source that only exists there. Callers use it to refuse a
// project-scope `skills install`/`setup` that would stamp the committed
// .agents/.warp/.claude mirrors (regenerate those with `make sync-skills`).
func IsSourceRepoRoot(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, "cli", "internal", "skills", "content", "2ndbrain-skill.md"))
	return err == nil
}

// RefreshIfStale re-installs an installed skill in place when it is a stamped,
// unmodified managed copy that is out of date relative to this binary's content.
// It leaves a hand-edited copy (Modified) and an unstamped copy untouched, so it
// never clobbers a user's own edits — those surface via `skills doctor` instead.
// Returns true when it rewrote the file. A missing install is a no-op (false).
func RefreshIfStale(baseDir string, a Agent, user bool) (bool, error) {
	abs := a.InstallPath(user, baseDir)
	data, err := os.ReadFile(abs)
	if err != nil {
		return false, nil // not installed (or unreadable) — nothing to refresh
	}
	f := FreshnessOf(data)
	if !f.Stamped || f.UpToDate || f.Modified {
		return false, nil
	}
	// backup=false: an auto-refresh of an already-managed, unmodified copy
	// should never leave a .bak (it'd accumulate one per brew upgrade).
	if _, err := writeManaged(abs, false); err != nil {
		return false, err
	}
	return true, nil
}

// Uninstall removes the skill file. Returns nil if the file did not exist.
func Uninstall(baseDir string, a Agent, user bool) error {
	abs := a.InstallPath(user, baseDir)
	err := os.Remove(abs)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// ListStatuses returns the install status for every registered agent.
// projectDir may be empty if no vault is available (only user statuses will be checked).
func ListStatuses(projectDir, homeDir string) []InstallStatus {
	out := make([]InstallStatus, len(Agents))
	for i, a := range Agents {
		out[i] = InstallStatus{
			Slug:          a.Slug,
			Name:          a.Name,
			ProjectPath:   a.ProjectPath,
			UserPath:      "~/" + a.UserPath,
			UserInstalled: IsInstalled(homeDir, a, true),
			Note:          a.Note,
		}
		if projectDir != "" {
			out[i].ProjectInstalled = IsInstalled(projectDir, a, false)
		}
	}
	return out
}
