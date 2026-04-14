package cli

import (
	"errors"
	"fmt"
	"os"
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
	Args:  cobra.MaximumNArgs(1),
	RunE:  runSkillsInstall,
}

var skillsUninstallCmd = &cobra.Command{
	Use:   "uninstall [agent]",
	Short: "Remove 2ndbrain skill for an AI coding agent",
	Args:  cobra.MaximumNArgs(1),
	RunE:  runSkillsUninstall,
}

var skillsShowCmd = &cobra.Command{
	Use:   "show <agent>",
	Short: "Preview the skill content for an agent",
	Args:  cobra.ExactArgs(1),
	RunE:  runSkillsShow,
}

func init() {
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

	for _, a := range targets {
		if err := skills.Install(baseDir, a, user, force); err != nil {
			if errors.Is(err, skills.ErrAlreadyInstalled) {
				fmt.Fprintf(os.Stderr, "skip %s: already installed (--force to overwrite)\n", a.Name)
				continue
			}
			fmt.Fprintf(os.Stderr, "error installing %s: %v\n", a.Name, err)
			continue
		}
		path := a.ProjectPath
		if user {
			path = "~/" + a.UserPath
		}
		fmt.Printf("Installed %s (%s) → %s\n", a.Name, scope, path)
		if a.Note != "" {
			fmt.Fprintf(os.Stderr, "  Note: %s\n", a.Note)
		}
	}
	return nil
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

	for _, a := range targets {
		if !skills.IsInstalled(baseDir, a, user) {
			if !all {
				fmt.Fprintf(os.Stderr, "skip %s: not installed at %s level\n", a.Name, scope)
			}
			continue
		}
		if err := skills.Uninstall(baseDir, a, user); err != nil {
			fmt.Fprintf(os.Stderr, "error uninstalling %s: %v\n", a.Name, err)
			continue
		}
		path := a.ProjectPath
		if user {
			path = "~/" + a.UserPath
		}
		fmt.Printf("Uninstalled %s (%s) ← %s\n", a.Name, scope, path)
	}
	return nil
}

func runSkillsShow(cmd *cobra.Command, args []string) error {
	a, ok := skills.AgentBySlug(args[0])
	if !ok {
		return fmt.Errorf("unknown agent %q — run `2nb skills list` to see supported agents", args[0])
	}
	fmt.Print(a.RenderContent())
	return nil
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
