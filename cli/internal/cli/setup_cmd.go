package cli

import (
	"errors"
	"fmt"
	"os"

	"github.com/apresai/2ndbrain/internal/instructions"
	mcppkg "github.com/apresai/2ndbrain/internal/mcp"
	"github.com/apresai/2ndbrain/internal/output"
	"github.com/apresai/2ndbrain/internal/skills"
	"github.com/apresai/2ndbrain/internal/vault"
	"github.com/spf13/cobra"
)

// SetupClientResult is the per-client JSON payload of `2nb setup`.
type SetupClientResult struct {
	Client        string `json:"client"`
	SkillSlug     string `json:"skill_slug,omitempty"`
	SkillPath     string `json:"skill_path,omitempty"`
	SkillBackup   string `json:"skill_backup,omitempty"`
	SkillSkipped  string `json:"skill_skipped,omitempty"`
	MCPConfigPath string `json:"mcp_config_path,omitempty"`
	MCPBackup     string `json:"mcp_backup,omitempty"`
	MCPChanged    bool   `json:"mcp_changed"`
	Configured    bool   `json:"configured"`
	Instructions  string `json:"instructions,omitempty"`
	// Global-instructions block in the client's memory file (e.g. ~/.claude/CLAUDE.md).
	// InstructionsPath is empty for a client with no known memory file.
	InstructionsPath    string `json:"instructions_file_path,omitempty"`
	InstructionsBackup  string `json:"instructions_backup,omitempty"`
	InstructionsWritten bool   `json:"instructions_written"`
	InstructionsError   string `json:"instructions_error,omitempty"`
	Error               string `json:"error,omitempty"`
}

// setupSkillSlug maps a client to the skill slug it should install, if any.
// Claude Desktop shares Claude Code's ~/.claude/skills folder, so it reuses the
// claude-code skill (its own row only configures MCP).
var setupSkillSlug = map[string]string{
	"claude-code":    "claude-code",
	"claude-desktop": "claude-code",
	"warp":           "warp",
	"agents":         "agents",
	"codex":          "codex",
}

var (
	setupAll     bool
	setupClient  string
	setupScope   string
	setupCommand string
	setupDryRun  bool
	setupForce   bool
)

var setupCmd = &cobra.Command{
	Use:   "setup",
	Short: "One-command setup: install the 2nb skill + MCP server for an AI client (or all)",
	Long: `setup wires both the 2ndbrain agent skill and the MCP server for an AI client
in one step — the easy front door over 'skills install' + 'mcp install'.

--client claude-code (default) | claude-desktop | warp | agents | codex, or --all
for every supported client. Each step is idempotent and backs up any file it
overwrites. Claude Desktop shares Claude Code's skills folder (so only its MCP is
configured); Codex's MCP is wired via 'codex mcp add' (its command is printed if
the codex CLI isn't installed). claude-desktop and codex MCP are user-scope only.`,
	Example: `  2nb setup --all
  2nb setup --client warp
  2nb setup --client claude-desktop
  2nb setup --client codex --dry-run`,
	RunE: runSetup,
}

func init() {
	setupCmd.Flags().BoolVar(&setupAll, "all", false, "Set up every supported client")
	setupCmd.Flags().StringVar(&setupClient, "client", "claude-code", clientFlagHelp())
	setupCmd.Flags().StringVar(&setupScope, "scope", "user", "user or project (skills + claude-code/warp/agents MCP; claude-desktop/codex MCP are always user)")
	setupCmd.Flags().StringVar(&setupCommand, "command", "2nb", "The command the client launches (default: 2nb on PATH)")
	setupCmd.Flags().BoolVar(&setupDryRun, "dry-run", false, "Print the plan without writing")
	setupCmd.Flags().BoolVar(&setupForce, "force", false, "Overwrite an existing skill file (backs it up first)")
	_ = setupCmd.RegisterFlagCompletionFunc("client", completeMCPClients)
	setupCmd.GroupID = "integr"
	rootCmd.AddCommand(setupCmd)
}

func runSetup(cmd *cobra.Command, _ []string) error {
	v, err := openVault()
	if err != nil {
		return err
	}
	defer v.Close()

	clients := []string{setupClient}
	if setupAll {
		clients = mcppkg.SupportedClients()
	}

	user := setupScope != "project"
	skillBase, herr := os.UserHomeDir()
	if herr != nil {
		return fmt.Errorf("home directory: %w", herr)
	}
	if !user {
		skillBase = v.Root
	}
	repoMirrorGuard := !user && skills.IsSourceRepoRoot(skillBase)

	skillDone := map[string]string{} // slug -> installed path (dedupe across clients)
	results := make([]SetupClientResult, 0, len(clients))

	for _, c := range clients {
		res := SetupClientResult{Client: c}
		setupSkill(&res, c, skillBase, user, repoMirrorGuard, skillDone)
		setupMCP(&res, v, c)
		setupInstructions(&res, c)
		results = append(results, res)
	}

	if format := getFormat(cmd); format != "" {
		return output.Write(os.Stdout, format, results)
	}
	printSetupResults(results, setupDryRun)
	return nil
}

func setupSkill(res *SetupClientResult, client, skillBase string, user, repoMirrorGuard bool, skillDone map[string]string) {
	slug, ok := setupSkillSlug[client]
	if !ok {
		return // client has no skill (none expected)
	}
	res.SkillSlug = slug
	if prev, already := skillDone[slug]; already {
		res.SkillPath = prev
		res.SkillSkipped = "already installed this run"
		return
	}
	if repoMirrorGuard && isRepoMirrorSlug(slug) {
		res.SkillSkipped = "committed repo mirror (run `make sync-skills`)"
		return
	}
	a, found := skills.AgentBySlug(slug)
	if !found {
		res.Error = appendErr(res.Error, "skill: unknown agent "+slug)
		return
	}
	if setupDryRun {
		res.SkillPath = skillPathFor(a, user)
		skillDone[slug] = res.SkillPath // dedupe so --all --dry-run doesn't repeat the shared skill
		return
	}
	backup, ierr := skills.InstallWithBackup(skillBase, *a, user, setupForce)
	if ierr != nil {
		if errors.Is(ierr, skills.ErrAlreadyInstalled) {
			res.SkillSkipped = "already installed (--force to overwrite)"
			res.SkillPath = skillPathFor(a, user)
			skillDone[slug] = res.SkillPath
			return
		}
		res.Error = appendErr(res.Error, fmt.Sprintf("skill: %v", ierr))
		return
	}
	res.SkillPath = skillPathFor(a, user)
	res.SkillBackup = backup
	skillDone[slug] = res.SkillPath
}

func setupMCP(res *SetupClientResult, v *vault.Vault, client string) {
	scope := setupScope
	if client == "claude-desktop" || client == "codex" {
		scope = "user" // single global config, no project scope
	}
	mres, merr := mcppkg.InstallForClient(v, client, setupCommand, scope, setupDryRun)
	if merr != nil {
		res.Error = appendErr(res.Error, fmt.Sprintf("mcp: %v", merr))
		return
	}
	res.MCPConfigPath = mres.ConfigPath
	res.MCPBackup = mres.BackupPath
	res.MCPChanged = mres.Changed
	res.Configured = mres.Configured
	res.Instructions = mres.Instructions
	if mres.Error != "" {
		res.Error = appendErr(res.Error, "mcp: "+mres.Error)
	}
}

// setupInstructions writes the always-loaded 2nb block into the client's global
// memory file (e.g. ~/.claude/CLAUDE.md). A client with no known memory file
// (warp/agents) is silently skipped — it has no InstructionsPath.
func setupInstructions(res *SetupClientResult, client string) {
	path, ok := memoryFilePath(client)
	if !ok {
		return
	}
	r, err := instructions.Install(path, setupForce, setupDryRun)
	if err != nil {
		res.InstructionsError = err.Error()
		return
	}
	res.InstructionsPath = r.Path
	res.InstructionsBackup = r.Backup
	res.InstructionsWritten = r.Changed
}

func printSetupResults(results []SetupClientResult, dryRun bool) {
	for _, r := range results {
		fmt.Printf("%s:\n", clientDisplayName(r.Client))

		switch {
		case r.SkillSlug == "":
			// no skill expected for this client
		case r.SkillSkipped != "":
			fmt.Printf("  skill: %s\n", r.SkillSkipped)
		case r.SkillPath != "":
			verb := "installed"
			if dryRun {
				verb = "would install"
			}
			fmt.Printf("  skill: %s → %s\n", verb, r.SkillPath)
			if r.SkillBackup != "" {
				fmt.Printf("    backed up previous to %s\n", r.SkillBackup)
			}
		}

		switch {
		case r.Instructions != "":
			fmt.Printf("  mcp: manual setup needed —\n%s\n", r.Instructions)
		case r.MCPConfigPath != "":
			verb := "configured"
			switch {
			case dryRun:
				verb = "would configure"
			case !r.MCPChanged:
				verb = "already configured"
			}
			fmt.Printf("  mcp: %s → %s\n", verb, r.MCPConfigPath)
			if r.MCPBackup != "" {
				fmt.Printf("    backed up previous to %s\n", r.MCPBackup)
			}
			if r.MCPChanged && r.Client == "claude-desktop" {
				fmt.Println("    (restart Claude Desktop to apply)")
			}
			if r.MCPChanged && r.Client == "codex" {
				fmt.Println("    (restart your Codex session to apply)")
			}
		}

		if r.InstructionsPath != "" {
			verb := "up to date in"
			switch {
			case dryRun && r.InstructionsWritten:
				verb = "would write to"
			case r.InstructionsWritten:
				verb = "wrote to"
			}
			fmt.Printf("  instructions: %s %s\n", verb, r.InstructionsPath)
			if r.InstructionsBackup != "" {
				fmt.Printf("    backed up previous to %s\n", r.InstructionsBackup)
			}
		}
		if r.InstructionsError != "" {
			fmt.Fprintf(os.Stderr, "  instructions error: %s\n", r.InstructionsError)
		}

		if r.Error != "" {
			fmt.Fprintf(os.Stderr, "  error: %s\n", r.Error)
		}
	}
}

func skillPathFor(a *skills.Agent, user bool) string {
	if user {
		return "~/" + a.UserPath
	}
	return a.ProjectPath
}

func appendErr(existing, msg string) string {
	if existing == "" {
		return msg
	}
	return existing + "; " + msg
}
