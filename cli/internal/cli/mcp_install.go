package cli

import (
	"fmt"
	"os"
	"strings"

	mcppkg "github.com/apresai/2ndbrain/internal/mcp"
	"github.com/apresai/2ndbrain/internal/output"
	"github.com/spf13/cobra"
)

var mcpInstallCmd = &cobra.Command{
	Use:   "install",
	Short: "Write the 2ndbrain MCP server entry into an AI client config",
	Long: `Adds (or updates) the 2ndbrain MCP server in an AI client's config so the
client launches it for this vault — the write-side inverse of "2nb mcp configured".

--client claude-code (default) writes ~/.claude.json; warp writes ~/.warp/.mcp.json;
agents writes the cross-tool ~/.agents/.mcp.json (which Warp also auto-reads);
claude-desktop writes ~/Library/Application Support/Claude/claude_desktop_config.json
(macOS) with an absolute 2nb path (a GUI app launches with a minimal PATH); codex
runs "codex mcp add" so Codex manages its own ~/.codex/config.toml (when the codex
CLI isn't installed, the exact command + TOML snippet is printed instead). --client all
configures every supported client. claude-code/warp/agents also take --scope project
(<vault>/...); claude-desktop and codex are user-scope only.

It is idempotent (no change if an equivalent entry already exists), backs up the
file first (<config>.bak; Codex manages its own file), and preserves every
unrelated key (it mutates only the mcpServers entry, never the rest of your
config). A malformed config is refused rather than overwritten. --dry-run shows
the plan without writing.`,
	Example: `  2nb mcp install
  2nb mcp install --client warp
  2nb mcp install --client claude-desktop
  2nb mcp install --client codex
  2nb mcp install --client all
  2nb mcp install --scope project --command /path/to/2nb --dry-run`,
	RunE: runMCPInstall,
}

var mcpUninstallCmd = &cobra.Command{
	Use:   "uninstall",
	Short: "Remove the 2ndbrain MCP server entry from an AI client config",
	Long:  "Removes the 2ndbrain entry for this vault's scope from the chosen client (--client; 'all' for every client), backing up the config first and preserving all other keys.",
	RunE:  runMCPUninstall,
}

var (
	mcpInstallScope    string
	mcpInstallCommand  string
	mcpInstallClient   string
	mcpInstallDryRun   bool
	mcpUninstallScope  string
	mcpUninstallClient string
	mcpUninstallDry    bool
)

func clientFlagHelp() string {
	return "AI client config to write: " + strings.Join(mcppkg.SupportedClients(), ", ") + ", or all"
}

func init() {
	mcpInstallCmd.Flags().StringVar(&mcpInstallScope, "scope", "user", "Where to write the entry: user (top-level) or project (cwd-keyed; claude-code/warp/agents only)")
	mcpInstallCmd.Flags().StringVar(&mcpInstallCommand, "command", "2nb", "The command the client launches (default: 2nb on PATH; the app passes its bundled CLI path)")
	mcpInstallCmd.Flags().StringVar(&mcpInstallClient, "client", "claude-code", clientFlagHelp())
	mcpInstallCmd.Flags().BoolVar(&mcpInstallDryRun, "dry-run", false, "Print the planned change without writing")
	mcpUninstallCmd.Flags().StringVar(&mcpUninstallScope, "scope", "user", "user or project")
	mcpUninstallCmd.Flags().StringVar(&mcpUninstallClient, "client", "claude-code", clientFlagHelp())
	mcpUninstallCmd.Flags().BoolVar(&mcpUninstallDry, "dry-run", false, "Print the planned change without writing")
	_ = mcpInstallCmd.RegisterFlagCompletionFunc("client", completeMCPClients)
	_ = mcpUninstallCmd.RegisterFlagCompletionFunc("client", completeMCPClients)
	mcpCmd.AddCommand(mcpInstallCmd)
	mcpCmd.AddCommand(mcpUninstallCmd)
}

func runMCPInstall(cmd *cobra.Command, _ []string) error {
	v, err := openVault()
	if err != nil {
		return err
	}
	defer v.Close()

	format := getFormat(cmd)
	if mcpInstallClient == "all" {
		results := mcppkg.InstallAll(v, mcpInstallCommand, mcpInstallScope, mcpInstallDryRun)
		if format != "" {
			return output.Write(os.Stdout, format, results)
		}
		for _, res := range results {
			printInstallResult(res, mcpInstallDryRun, false)
		}
		return nil
	}

	res, err := mcppkg.InstallForClient(v, mcpInstallClient, mcpInstallCommand, mcpInstallScope, mcpInstallDryRun)
	if err != nil {
		return err
	}
	if format != "" {
		return output.Write(os.Stdout, format, res)
	}
	printInstallResult(res, mcpInstallDryRun, false)
	return nil
}

func runMCPUninstall(cmd *cobra.Command, _ []string) error {
	v, err := openVault()
	if err != nil {
		return err
	}
	defer v.Close()

	format := getFormat(cmd)
	if mcpUninstallClient == "all" {
		results := mcppkg.UninstallAll(v, mcpUninstallScope, mcpUninstallDry)
		if format != "" {
			return output.Write(os.Stdout, format, results)
		}
		for _, res := range results {
			printInstallResult(res, mcpUninstallDry, true)
		}
		return nil
	}

	res, err := mcppkg.UninstallForClient(v, mcpUninstallClient, mcpUninstallScope, mcpUninstallDry)
	if err != nil {
		return err
	}
	if format != "" {
		return output.Write(os.Stdout, format, res)
	}
	printInstallResult(res, mcpUninstallDry, true)
	return nil
}

// printInstallResult renders one InstallResult as human text. uninstall picks
// the verb; dryRun reports the plan. Per-client errors print to stderr (so a
// `--client all` run surfaces a single failure without aborting the rest), and a
// codex/absent-CLI fallback prints its manual Instructions.
func printInstallResult(res mcppkg.InstallResult, dryRun, uninstall bool) {
	switch {
	case res.Error != "":
		fmt.Fprintf(os.Stderr, "%s: %s\n", res.Client, res.Error)
	case res.Instructions != "":
		fmt.Printf("%s: manual setup needed —\n%s\n", res.Client, res.Instructions)
	case !res.Changed:
		if uninstall {
			fmt.Printf("%s: no 2ndbrain entry found (%s scope); nothing to remove.\n", res.Client, res.Scope)
		} else {
			fmt.Printf("%s: already configured (%s scope); no change.\n", res.Client, res.Scope)
		}
	case dryRun && uninstall:
		fmt.Printf("%s: would remove the 2ndbrain MCP server entry (%s scope).\n", res.Client, res.Scope)
	case dryRun:
		fmt.Printf("%s: would write the 2ndbrain MCP server entry to %s (%s scope).\n", res.Client, res.ConfigPath, res.Scope)
	case uninstall:
		fmt.Printf("%s: removed the 2ndbrain MCP server entry (%s scope).\n", res.Client, res.Scope)
		if res.BackupPath != "" {
			fmt.Printf("  Backup saved to %s\n", res.BackupPath)
		}
	default:
		fmt.Printf("%s: configured the 2ndbrain MCP server in %s (%s scope).\n", res.Client, res.ConfigPath, res.Scope)
		if res.BackupPath != "" {
			fmt.Printf("  Backup saved to %s\n", res.BackupPath)
		}
		if res.Client == "claude-desktop" {
			fmt.Println("  Restart Claude Desktop to apply.")
		}
		if res.Client == "codex" {
			fmt.Println("  Restart your Codex session to apply.")
		}
	}
}
