package cli

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/apresai/2ndbrain/internal/output"
	"github.com/apresai/2ndbrain/internal/skills"
	"github.com/spf13/cobra"
)

var skillsCmd = &cobra.Command{
	Use:   "skills",
	Short: "Manage 2ndbrain skill files for AI coding agents",
	// Default action when invoked without a subcommand: list agents and status.
	RunE: runSkillsList,
}

var skillsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List supported agents and install status",
	RunE:  runSkillsList,
}

var skillsInstallCmd = &cobra.Command{
	Use:   "install [agent]",
	Short: "Install 2ndbrain skill for an AI coding agent",
	Long:  "Install a SKILL.md file that teaches an AI coding agent about this vault's CLI, MCP tools, and document format.\n\nBy default installs at project level (vault root). Use --user to install globally in your home directory.",
	Example: `  2nb skills install --all                          # install for every supported agent
  2nb skills install claude-code                    # install for Claude Code only
  2nb skills install cursor --user                  # install globally for all projects`,
	Args:              cobra.MaximumNArgs(1),
	ValidArgsFunction: completeAgentSlugs,
	RunE:              runSkillsInstall,
}

var skillsUninstallCmd = &cobra.Command{
	Use:               "uninstall [agent]",
	Short:             "Remove 2ndbrain skill for an AI coding agent",
	Args:              cobra.MaximumNArgs(1),
	ValidArgsFunction: completeAgentSlugs,
	RunE:              runSkillsUninstall,
}

var skillsShowCmd = &cobra.Command{
	Use:               "show <agent>",
	Short:             "Preview the skill content for an agent",
	Args:              cobra.ExactArgs(1),
	ValidArgsFunction: completeAgentSlugs,
	RunE:              runSkillsShow,
}

func init() {
	// Stamp installs with this binary's version so staleness is detectable later.
	skills.Version = Version

	skillsInstallCmd.Flags().Bool("all", false, "Install for all supported agents")
	skillsInstallCmd.Flags().Bool("force", false, "Overwrite existing skill files")
	skillsInstallCmd.Flags().Bool("user", false, "Install as user-level skill (home directory, all projects)")
	skillsUninstallCmd.Flags().Bool("all", false, "Uninstall from all supported agents")
	skillsUninstallCmd.Flags().Bool("user", false, "Uninstall user-level skill")
	skillsListCmd.Flags().Bool("user", false, "Only show user-level status")

	skillsCmd.AddCommand(skillsListCmd)
	skillsCmd.AddCommand(skillsInstallCmd)
	skillsCmd.AddCommand(skillsUninstallCmd)
	skillsCmd.AddCommand(skillsShowCmd)
	skillsCmd.GroupID = "integr"
	rootCmd.AddCommand(skillsCmd)
}

func runSkillsList(cmd *cobra.Command, args []string) error {
	userOnly, _ := cmd.Flags().GetBool("user")
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("home directory: %w", err)
	}

	var projectDir string
	if !userOnly {
		if v, err := openVault(); err == nil {
			projectDir = v.Root
			v.Close()
		}
		// If vault not found and not --user, still show user statuses
	}

	// Self-heal: silently refresh any stamped, unmodified, out-of-date managed
	// install (e.g. after a `brew upgrade`). Hand-edited and unstamped copies are
	// left alone. The GUI/plugin poll this command, so a release keeps the skill
	// current with no user action.
	autoRefreshStaleSkills(homeDir, projectDir, userOnly)

	statuses := skills.ListStatuses(projectDir, homeDir)

	format := getFormat(cmd)
	if format != "" {
		return output.Write(os.Stdout, format, statuses)
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	if userOnly || projectDir == "" {
		fmt.Fprintln(w, "AGENT\tUSER\tPATH")
		for _, s := range statuses {
			mark := "✗"
			if s.UserInstalled {
				mark = "✓"
			}
			fmt.Fprintf(w, "%s\t%s\t%s\n", s.Name, mark, s.UserPath)
		}
	} else {
		fmt.Fprintln(w, "AGENT\tPROJECT\tUSER\tPROJECT PATH")
		for _, s := range statuses {
			pm := "✗"
			if s.ProjectInstalled {
				pm = "✓"
			}
			um := "✗"
			if s.UserInstalled {
				um = "✓"
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", s.Name, pm, um, s.ProjectPath)
		}
	}
	return w.Flush()
}

func runSkillsInstall(cmd *cobra.Command, args []string) error {
	all, _ := cmd.Flags().GetBool("all")
	force, _ := cmd.Flags().GetBool("force")
	user, _ := cmd.Flags().GetBool("user")

	baseDir, scope, err := resolveBaseDir(user)
	if err != nil {
		return err
	}

	targets, err := resolveAgentTargets(args, all)
	if err != nil {
		return err
	}

	// Repo-root guard: a project-scope `--all` from the 2ndbrain source tree would
	// stamp the committed .agents/.warp/.claude mirrors (regenerate those with
	// `make sync-skills`). Skip the mirror slugs in that case; an explicit
	// single-agent install stays allowed (a deliberate override).
	skipMirrors := all && !user && skills.IsSourceRepoRoot(baseDir)

	// One row per target, built BEFORE anything is printed, so the structured
	// record and the human lines are the same outcome rendered twice rather
	// than two independent descriptions of it. `skills install` printed
	// "Installed ..." on stdout for every format, so --json handed a caller
	// prose.
	results := []SkillChangeResult{}
	for _, a := range targets {
		res := SkillChangeResult{Slug: a.Slug, Agent: a.Name, Scope: scope, Note: a.Note}
		res.Path = a.ProjectPath
		if user {
			res.Path = "~/" + a.UserPath
		}
		switch {
		case skipMirrors && isRepoMirrorSlug(a.Slug):
			res.Skipped = "committed mirror in the 2ndbrain source tree; run `make sync-skills` instead"
		default:
			backup, err := skills.InstallWithBackup(baseDir, a, user, force)
			switch {
			case errors.Is(err, skills.ErrAlreadyInstalled):
				res.Skipped = "already installed (--force to overwrite)"
			case err != nil:
				res.Error = err.Error()
			default:
				res.Changed = true
				res.BackupPath = backup
			}
		}
		results = append(results, res)
	}

	if done, err := emitStructured(cmd, results); done {
		return err
	}

	for _, res := range results {
		switch {
		case res.Skipped != "":
			fmt.Fprintf(os.Stderr, "skip %s: %s\n", res.Agent, res.Skipped)
		case res.Error != "":
			fmt.Fprintf(os.Stderr, "error installing %s: %s\n", res.Agent, res.Error)
		default:
			fmt.Printf("Installed %s (%s) → %s\n", res.Agent, res.Scope, res.Path)
			if res.BackupPath != "" {
				fmt.Fprintf(os.Stderr, "  Backed up previous SKILL.md to %s\n", res.BackupPath)
			}
			if res.Note != "" {
				fmt.Fprintf(os.Stderr, "  Note: %s\n", res.Note)
			}
		}
	}
	return nil
}

// SkillChangeResult is one agent's outcome from `skills install` or
// `skills uninstall`. It matches mcp.InstallResult's shape: what changed, where,
// and why nothing did. A batch (--all) returns one row per agent, so a single
// failure never hides the rest.
type SkillChangeResult struct {
	Slug  string `json:"slug"`
	Agent string `json:"agent"`
	Scope string `json:"scope"`
	Path  string `json:"path"`
	// Changed is true only when the SKILL.md was actually written or removed.
	Changed    bool   `json:"changed"`
	BackupPath string `json:"backup_path,omitempty"`
	// Skipped says why nothing happened; empty when something did.
	Skipped string `json:"skipped,omitempty"`
	Error   string `json:"error,omitempty"`
	Note    string `json:"note,omitempty"`
}

// isRepoMirrorSlug reports whether a skill slug maps to one of the repo's
// committed SKILL.md mirrors (kept in sync by `make sync-skills`): .agents,
// .warp, and .claude. (codex's .codex/skills is install-only, not a mirror.)
func isRepoMirrorSlug(slug string) bool {
	switch slug {
	case "agents", "warp", "claude-code":
		return true
	default:
		return false
	}
}

func runSkillsUninstall(cmd *cobra.Command, args []string) error {
	all, _ := cmd.Flags().GetBool("all")
	user, _ := cmd.Flags().GetBool("user")

	baseDir, scope, err := resolveBaseDir(user)
	if err != nil {
		return err
	}

	targets, err := resolveAgentTargets(args, all)
	if err != nil {
		return err
	}

	results := []SkillChangeResult{}
	for _, a := range targets {
		res := SkillChangeResult{Slug: a.Slug, Agent: a.Name, Scope: scope}
		res.Path = a.ProjectPath
		if user {
			res.Path = "~/" + a.UserPath
		}
		switch {
		case !skills.IsInstalled(baseDir, a, user):
			res.Skipped = "not installed at " + scope + " level"
		default:
			if err := skills.Uninstall(baseDir, a, user); err != nil {
				res.Error = err.Error()
			} else {
				res.Changed = true
			}
		}
		results = append(results, res)
	}

	if done, err := emitStructured(cmd, results); done {
		return err
	}

	for _, res := range results {
		switch {
		case res.Skipped != "":
			// A batch stays quiet about agents that had nothing installed;
			// a single named agent is told.
			if !all {
				fmt.Fprintf(os.Stderr, "skip %s: %s\n", res.Agent, res.Skipped)
			}
		case res.Error != "":
			fmt.Fprintf(os.Stderr, "error uninstalling %s: %s\n", res.Agent, res.Error)
		default:
			fmt.Printf("Uninstalled %s (%s) ← %s\n", res.Agent, res.Scope, res.Path)
		}
	}
	return nil
}

// SkillDocument is the `skills show --json` record: the SKILL.md body plus the
// identity a caller would otherwise have to parse out of it.
type SkillDocument struct {
	Slug        string `json:"slug"`
	Agent       string `json:"agent"`
	ProjectPath string `json:"project_path"`
	UserPath    string `json:"user_path"`
	Version     string `json:"version"`
	Content     string `json:"content"`
	Chars       int    `json:"chars"`
}

func runSkillsShow(cmd *cobra.Command, args []string) error {
	// `skills show` emits a document BODY (the SKILL.md markdown), so it has
	// the same shape as `git diff` and `export-context`: raw/md/text emit it,
	// json wraps it in a record, and the row-set formats have nothing to
	// render. It never called getFormat at all, so `--json`, `--csv` and
	// `--yaml` each printed raw markdown and exited 0, and an agent fetching a
	// skill body programmatically got a parse error on a successful command.
	// Refused up front, before the agent lookup, so the answer never depends on
	// which agent was named.
	format := getFormat(cmd)
	switch format {
	case output.FormatCSV, output.FormatTSV, output.FormatYAML:
		return exitWithError(ExitValidation, fmt.Sprintf(
			"error: a SKILL.md is a document body, not a row set; --format %s has nothing to render (use --json for a record, or raw/md/text for the markdown itself)", format))
	}

	a, ok := skills.AgentBySlug(args[0])
	if !ok {
		return fmt.Errorf("unknown agent %q — run `2nb skills list` to see supported agents", args[0])
	}
	content := a.RenderContent()
	if format == output.FormatJSON {
		return writeOut(cmd, format, SkillDocument{
			Slug:        a.Slug,
			Agent:       a.Name,
			ProjectPath: a.ProjectPath,
			UserPath:    a.UserPath,
			Version:     skills.Version,
			Content:     content,
			Chars:       len(content),
		})
	}
	// Empty, raw, md and text all emit the markdown itself; --copy goes through
	// the same writer so it copies the skill rather than nothing.
	return writeOut(cmd, output.FormatRaw, content)
}

// autoRefreshStaleSkills re-installs any stamped, unmodified, out-of-date managed
// SKILL.md (user scope always; project scope when a vault resolved). It is the
// mechanism that keeps installed skills current across releases without user
// action: hand-edited and unstamped copies are never touched, so it can't clobber
// a user's edits. Refreshes are reported on stderr (never stdout, for --json).
func autoRefreshStaleSkills(homeDir, projectDir string, userOnly bool) {
	var refreshed []string
	refresh := func(baseDir string, a skills.Agent, user bool, scope string) {
		ok, err := skills.RefreshIfStale(baseDir, a, user)
		if err != nil {
			// A stale skill that can't be rewritten (read-only FS, permissions)
			// stays stale; `skills doctor` still flags it, but don't fail silently.
			slog.Warn("could not auto-refresh stale skill", "agent", a.Slug, "scope", scope, "err", err)
			return
		}
		if ok {
			refreshed = append(refreshed, a.Name+" ("+scope+")")
		}
	}
	for _, a := range skills.Agents {
		refresh(homeDir, a, true, "user")
		if !userOnly && projectDir != "" {
			refresh(projectDir, a, false, "project")
		}
	}
	if len(refreshed) > 0 {
		fmt.Fprintf(os.Stderr, "Updated stale 2nb skill(s) to %s: %s\n", Version, strings.Join(refreshed, ", "))
	}
}

// resolveBaseDir returns the base directory and a human-readable scope label.
// For --user, returns the home directory. Otherwise opens the vault.
func resolveBaseDir(user bool) (string, string, error) {
	if user {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", "", fmt.Errorf("home directory: %w", err)
		}
		return home, "user", nil
	}
	v, err := openVault()
	if err != nil {
		return "", "", err
	}
	defer v.Close()
	return v.Root, "project", nil
}

func resolveAgentTargets(args []string, all bool) ([]skills.Agent, error) {
	if all && len(args) > 0 {
		return nil, fmt.Errorf("cannot specify both an agent name and --all")
	}
	if all {
		return skills.Agents, nil
	}
	if len(args) == 0 {
		return nil, fmt.Errorf("specify an agent slug or use --all\nRun `2nb skills list` to see supported agents")
	}
	a, ok := skills.AgentBySlug(args[0])
	if !ok {
		return nil, fmt.Errorf("unknown agent %q — run `2nb skills list` to see supported agents", args[0])
	}
	return []skills.Agent{*a}, nil
}
