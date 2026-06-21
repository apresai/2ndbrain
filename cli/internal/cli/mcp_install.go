package cli

import (
	"fmt"
	"os"

	mcppkg "github.com/apresai/2ndbrain/internal/mcp"
	"github.com/apresai/2ndbrain/internal/output"
	"github.com/spf13/cobra"
)

var mcpInstallCmd = &cobra.Command{
	Use:   "install",
	Short: "Write the 2ndbrain MCP server entry into the AI client config (~/.claude.json)",
	Long: `Adds (or updates) the 2ndbrain MCP server in ~/.claude.json so Claude Code
launches it for this vault — the write-side inverse of "2nb mcp configured".

It is idempotent (no change if an equivalent entry already exists), backs up the
file first (~/.claude.json.bak), and preserves every unrelated key (it mutates
only the mcpServers entry, never the rest of your config). A malformed config is
refused rather than overwritten. --dry-run shows the plan without writing.`,
	Example: `  2nb mcp install
  2nb mcp install --scope project
  2nb mcp install --command /path/to/2nb --dry-run`,
	RunE: runMCPInstall,
}

var mcpUninstallCmd = &cobra.Command{
	Use:   "uninstall",
	Short: "Remove the 2ndbrain MCP server entry from ~/.claude.json",
	Long:  "Removes the 2ndbrain entry for this vault's scope, backing up the config first and preserving all other keys.",
	RunE:  runMCPUninstall,
}

var (
	mcpInstallScope   string
	mcpInstallCommand string
	mcpInstallDryRun  bool
	mcpUninstallScope string
	mcpUninstallDry   bool
)

func init() {
	mcpInstallCmd.Flags().StringVar(&mcpInstallScope, "scope", "user", "Where to write the entry: user (top-level) or project (cwd-keyed)")
	mcpInstallCmd.Flags().StringVar(&mcpInstallCommand, "command", "2nb", "The command Claude Code launches (default: 2nb on PATH; the app passes its bundled CLI path)")
	mcpInstallCmd.Flags().BoolVar(&mcpInstallDryRun, "dry-run", false, "Print the planned change without writing")
	mcpUninstallCmd.Flags().StringVar(&mcpUninstallScope, "scope", "user", "user or project")
	mcpUninstallCmd.Flags().BoolVar(&mcpUninstallDry, "dry-run", false, "Print the planned change without writing")
	mcpCmd.AddCommand(mcpInstallCmd)
	mcpCmd.AddCommand(mcpUninstallCmd)
}

func runMCPInstall(cmd *cobra.Command, _ []string) error {
	v, err := openVault()
	if err != nil {
		return err
	}
	defer v.Close()

	res, err := mcppkg.Install(v, mcpInstallCommand, mcpInstallScope, mcpInstallDryRun)
	if err != nil {
		return err
	}

	if format := getFormat(cmd); format != "" {
		return output.Write(os.Stdout, format, res)
	}

	switch {
	case !res.Changed:
		fmt.Printf("Already configured (%s scope); no change.\n", res.Scope)
	case mcpInstallDryRun:
		fmt.Printf("Would write the 2ndbrain MCP server entry to ~/.claude.json (%s scope).\n", res.Scope)
	default:
		fmt.Printf("Configured the 2ndbrain MCP server in ~/.claude.json (%s scope).\n", res.Scope)
		if res.BackupPath != "" {
			fmt.Printf("Backup saved to %s\n", res.BackupPath)
		}
	}
	return nil
}

func runMCPUninstall(cmd *cobra.Command, _ []string) error {
	v, err := openVault()
	if err != nil {
		return err
	}
	defer v.Close()

	res, err := mcppkg.Uninstall(v, mcpUninstallScope, mcpUninstallDry)
	if err != nil {
		return err
	}

	if format := getFormat(cmd); format != "" {
		return output.Write(os.Stdout, format, res)
	}

	switch {
	case !res.Changed:
		fmt.Printf("No 2ndbrain entry found (%s scope); nothing to remove.\n", res.Scope)
	case mcpUninstallDry:
		fmt.Printf("Would remove the 2ndbrain MCP server entry (%s scope).\n", res.Scope)
	default:
		fmt.Printf("Removed the 2ndbrain MCP server entry (%s scope).\n", res.Scope)
		if res.BackupPath != "" {
			fmt.Printf("Backup saved to %s\n", res.BackupPath)
		}
	}
	return nil
}
