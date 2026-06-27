package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"

	mcppkg "github.com/apresai/2ndbrain/internal/mcp"
	"github.com/apresai/2ndbrain/internal/output"
	"github.com/spf13/cobra"
)

var mcpCmd = &cobra.Command{
	Use:   "mcp",
	Short: "MCP server management and observability",
	// Default action when invoked without a subcommand: show live servers.
	RunE: runMCPStatus,
}

var mcpStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show live MCP server processes and their recent tool invocations",
	Long: `List all running 2nb mcp-server processes attached to this vault, along with
their parent PID, start time, and the last 50 tool invocations each has served.

Useful to confirm that Claude Code / Cursor / other clients are actually talking
to the vault, and to spot which tool is hot when the vault feels slow.`,
	RunE: runMCPStatus,
}

var mcpConfiguredCmd = &cobra.Command{
	Use:   "configured",
	Short: "Report whether the 2ndbrain MCP server is configured in the AI client config",
	Long: `Check whether the 2ndbrain MCP server is wired into the AI client's config
(currently ~/.claude.json for Claude Code) for this vault.

This is the durable "is it set up?" signal, unlike 'mcp status', which only
sees a server process that is running right now. The MCP server is launched on
demand by the client, so 'mcp status' reads empty whenever the client is closed
even when everything is configured correctly. Use 'mcp configured' to answer
"will my AI tool find this vault?" without the client running.

If it reports not configured, run '2nb mcp-setup' for the snippet to add.`,
	RunE: runMCPConfigured,
}

var (
	mcpConfiguredClient string
	mcpConfiguredAll    bool
)

func init() {
	mcpCmd.GroupID = "integr"
	mcpConfiguredCmd.Flags().StringVar(&mcpConfiguredClient, "client", "claude-code", clientFlagHelp())
	mcpConfiguredCmd.Flags().BoolVar(&mcpConfiguredAll, "all", false, "Report configured-status for every supported client")
	_ = mcpConfiguredCmd.RegisterFlagCompletionFunc("client", completeMCPClients)
	mcpCmd.AddCommand(mcpStatusCmd)
	mcpCmd.AddCommand(mcpConfiguredCmd)
	rootCmd.AddCommand(mcpCmd)
}

func runMCPConfigured(cmd *cobra.Command, args []string) error {
	v, err := openVault()
	if err != nil {
		return err
	}
	defer v.Close()

	// Always a JSON array (the plugin/app contract). Default (no --client/--all)
	// stays slice-of-one claude-code via ConfiguredFor(v, "claude-code").
	var statuses []mcppkg.ConfiguredStatus
	if mcpConfiguredAll {
		statuses = mcppkg.ConfiguredAll(v)
	} else {
		statuses = []mcppkg.ConfiguredStatus{mcppkg.ConfiguredFor(v, mcpConfiguredClient)}
	}

	if getFormat(cmd) == output.FormatJSON {
		out, err := json.MarshalIndent(statuses, "", "  ")
		if err != nil {
			return err
		}
		fmt.Println(string(out))
		return nil
	}

	anyMissing := false
	for _, st := range statuses {
		name := clientDisplayName(st.Client)
		if st.Configured {
			scope := st.Scope
			if scope == "" {
				scope = "configured"
			}
			fmt.Printf("%s MCP server: configured (%s scope) in %s\n", name, scope, st.ConfigPath)
			continue
		}
		anyMissing = true
		fmt.Printf("%s MCP server: not configured in %s\n", name, st.ConfigPath)
	}
	if anyMissing {
		fmt.Fprintln(os.Stderr, "  Configure with: 2nb mcp install --client <name>  (or '2nb setup'); see '2nb mcp-setup' for snippets")
	}
	return nil
}

// clientDisplayName maps a client slug to a human label for terminal output.
func clientDisplayName(client string) string {
	switch client {
	case "", "claude-code", "claude":
		return "Claude Code"
	case "claude-desktop":
		return "Claude Desktop"
	case "warp":
		return "Warp"
	case "agents":
		return "Agents (.agents)"
	case "codex":
		return "Codex"
	default:
		return client
	}
}

func runMCPStatus(cmd *cobra.Command, args []string) error {
	v, err := openVault()
	if err != nil {
		return err
	}
	defer v.Close()

	statuses, err := mcppkg.ListStatuses(v)
	if err != nil {
		return fmt.Errorf("list mcp statuses: %w", err)
	}

	if getFormat(cmd) == output.FormatJSON {
		out, err := json.MarshalIndent(statuses, "", "  ")
		if err != nil {
			return err
		}
		fmt.Println(string(out))
		return nil
	}

	if len(statuses) == 0 {
		fmt.Println("No MCP servers currently running against this vault.")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "PID\tSTARTED\tPARENT PID\tINVOCATIONS\tLAST TOOL")
	for _, s := range statuses {
		lastTool := "-"
		if len(s.Invocations) > 0 {
			lastTool = s.Invocations[len(s.Invocations)-1].Tool
		}
		fmt.Fprintf(w, "%d\t%s\t%d\t%d\t%s\n",
			s.PID,
			s.StartedAt.Local().Format("15:04:05"),
			s.ParentPID,
			len(s.Invocations),
			lastTool,
		)
	}
	return w.Flush()
}
