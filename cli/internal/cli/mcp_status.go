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

func init() {
	mcpCmd.GroupID = "integr"
	mcpCmd.AddCommand(mcpStatusCmd)
	rootCmd.AddCommand(mcpCmd)
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
