package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/apresai/2ndbrain/internal/instructions"
	"github.com/apresai/2ndbrain/internal/output"
	"github.com/spf13/cobra"
)

// memoryFileRel maps a client to its user-global agent memory file, relative to
// the home dir. Only clients whose global-memory-file convention is settled are
// listed: claude-desktop shares Claude Code's ~/.claude/CLAUDE.md, and the
// Codex CLI merges ~/.codex/AGENTS.md ahead of repo-level AGENTS.md files.
// warp and agents remain documented deferrals: Warp's rules live in cloud-
// synced Warp Drive (no on-disk global rules file), and the cross-tool
// AGENTS.md standard is project-scoped with no user-global path.
var memoryFileRel = map[string]string{
	"claude-code":    filepath.Join(".claude", "CLAUDE.md"),
	"claude-desktop": filepath.Join(".claude", "CLAUDE.md"),
	"codex":          filepath.Join(".codex", "AGENTS.md"),
}

// memoryFileClients is the ordered set that `--all` iterates.
var memoryFileClients = []string{"claude-code", "claude-desktop", "codex"}

// memoryFilePath resolves a client's absolute global memory file, if it has one.
func memoryFilePath(client string) (string, bool) {
	rel, ok := memoryFileRel[client]
	if !ok {
		return "", false
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", false
	}
	return filepath.Join(home, rel), true
}

var (
	instrClient string
	instrAll    bool
	instrDryRun bool
	instrForce  bool
)

var instructionsCmd = &cobra.Command{
	Use:   "instructions",
	Short: "Manage the always-loaded 2nb block in an AI client's global memory file",
	Long: `Write, check, or remove a small managed "2ndbrain" reference block in an AI
client's global agent memory file (e.g. ~/.claude/CLAUDE.md). The block is a
lightweight, always-loaded pointer to 2nb — the complement to the fuller
installable skill (see 'skills'). It is delimited by HTML-comment markers and
version-stamped, so it updates in place and can be removed without touching your
surrounding content; writes back up the file first (<file>.bak).

Supported clients: claude-code, claude-desktop (both share ~/.claude/CLAUDE.md)
and codex (~/.codex/AGENTS.md). '2nb setup' installs this block alongside the
skill and MCP server.

Bare 'instructions' reports status (like 'instructions configured').`,
	RunE: runInstructionsConfigured,
}

var instructionsInstallCmd = &cobra.Command{
	Use:   "install",
	Short: "Write the 2nb block into an AI client's global memory file",
	RunE:  runInstructionsInstall,
}

var instructionsConfiguredCmd = &cobra.Command{
	Use:     "configured",
	Aliases: []string{"status"},
	Short:   "Report whether the 2nb block is present in a client's global memory file",
	RunE:    runInstructionsConfigured,
}

var instructionsUninstallCmd = &cobra.Command{
	Use:   "uninstall",
	Short: "Remove the 2nb block from a client's global memory file",
	RunE:  runInstructionsUninstall,
}

func init() {
	// Stamp installs with this binary's version (mirrors skills.Version).
	instructions.Version = Version

	instructionsInstallCmd.Flags().StringVar(&instrClient, "client", "claude-code", "AI client (claude-code, claude-desktop, codex)")
	instructionsInstallCmd.Flags().BoolVar(&instrDryRun, "dry-run", false, "Print the plan without writing")
	instructionsInstallCmd.Flags().BoolVar(&instrForce, "force", false, "Overwrite a hand-edited block")
	instructionsConfiguredCmd.Flags().StringVar(&instrClient, "client", "claude-code", "AI client to check")
	instructionsConfiguredCmd.Flags().BoolVar(&instrAll, "all", false, "Report every supported client")
	instructionsUninstallCmd.Flags().StringVar(&instrClient, "client", "claude-code", "AI client")

	instructionsCmd.AddCommand(instructionsInstallCmd, instructionsConfiguredCmd, instructionsUninstallCmd)
	instructionsCmd.GroupID = "integr"
	rootCmd.AddCommand(instructionsCmd)
}

func runInstructionsInstall(cmd *cobra.Command, _ []string) error {
	path, ok := memoryFilePath(instrClient)
	if !ok {
		return exitWithError(ExitValidation, fmt.Sprintf(
			"client %q has no known global memory file (supported: %s)", instrClient, strings.Join(memoryFileClients, ", ")))
	}
	res, err := instructions.Install(path, instrForce, instrDryRun)
	if err != nil {
		return err
	}
	if format := getFormat(cmd); format != "" {
		return output.Write(os.Stdout, format, res)
	}
	verb := "wrote 2nb block to"
	switch {
	case instrDryRun && res.Changed:
		verb = "would write 2nb block to"
	case !res.Changed:
		verb = "2nb block already current in"
	}
	fmt.Printf("%s %s\n", verb, res.Path)
	if res.Backup != "" {
		fmt.Fprintf(os.Stderr, "  backed up previous to %s\n", res.Backup)
	}
	return nil
}

func runInstructionsConfigured(cmd *cobra.Command, _ []string) error {
	clients := []string{instrClient}
	if instrAll {
		clients = memoryFileClients
	}
	statuses := make([]instructions.Status, 0, len(clients))
	for _, c := range clients {
		path, ok := memoryFilePath(c)
		if !ok {
			if !instrAll {
				return exitWithError(ExitValidation, fmt.Sprintf(
					"client %q has no known global memory file (supported: %s)", c, strings.Join(memoryFileClients, ", ")))
			}
			continue
		}
		st, err := instructions.Configured(path)
		if err != nil {
			return err
		}
		st.Client = c
		statuses = append(statuses, st)
	}

	if format := getFormat(cmd); format != "" {
		return output.Write(os.Stdout, format, statuses)
	}
	for _, st := range statuses {
		state := "not installed"
		if st.Installed {
			switch {
			case st.Modified:
				state = "installed (hand-edited)"
			case !st.UpToDate:
				state = "installed (out of date)"
			default:
				state = "installed"
			}
		}
		fmt.Printf("%s: %s — %s\n", st.Client, state, st.Path)
	}
	return nil
}

func runInstructionsUninstall(cmd *cobra.Command, _ []string) error {
	path, ok := memoryFilePath(instrClient)
	if !ok {
		return exitWithError(ExitValidation, fmt.Sprintf(
			"client %q has no known global memory file (supported: %s)", instrClient, strings.Join(memoryFileClients, ", ")))
	}
	res, err := instructions.Uninstall(path)
	if err != nil {
		return err
	}
	if format := getFormat(cmd); format != "" {
		return output.Write(os.Stdout, format, res)
	}
	if res.Changed {
		fmt.Printf("removed 2nb block from %s\n", res.Path)
		if res.Backup != "" {
			fmt.Fprintf(os.Stderr, "  backed up previous to %s\n", res.Backup)
		}
	} else {
		fmt.Printf("no 2nb block found in %s\n", res.Path)
	}
	return nil
}
