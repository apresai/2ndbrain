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
	{Slug: "claude-code", Name: "Claude Code",
		ProjectPath: ".claude/skills/2nb/SKILL.md",
		UserPath:    ".claude/skills/2nb/SKILL.md"},
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
// if the file exists and force is false.
func Install(baseDir string, a Agent, user bool, force bool) error {
	abs := a.InstallPath(user, baseDir)

	if !force {
		if _, err := os.Stat(abs); err == nil {
			return ErrAlreadyInstalled
		}
	}

	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return fmt.Errorf("create directory: %w", err)
	}

	tmp := abs + ".tmp"
	if err := os.WriteFile(tmp, []byte(a.RenderContent()), 0o644); err != nil {
		return fmt.Errorf("write: %w", err)
	}
	if err := os.Rename(tmp, abs); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("rename: %w", err)
	}
	return nil
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
