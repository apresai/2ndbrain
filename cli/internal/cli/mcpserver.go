package cli

import (
	"fmt"

	mcppkg "github.com/apresai/2ndbrain/internal/mcp"
	"github.com/spf13/cobra"
)

var mcpServerCmd = &cobra.Command{
	Use:   "mcp-server",
	Short: "Start an MCP server on stdio for AI tool integration",
	Long: `Starts a Model Context Protocol (MCP) server on stdio transport.

Add this to your Claude Code config:
  {"mcpServers": {"2ndbrain": {"command": "2nb", "args": ["mcp-server"]}}}

Available tools: kb_search, kb_read, kb_related, kb_create, kb_update_meta, kb_structure`,
	RunE: runMCPServer,
}

func init() {
	mcpServerCmd.GroupID = "integr"
	rootCmd.AddCommand(mcpServerCmd)
}

func runMCPServer(cmd *cobra.Command, args []string) error {
	v, err := openVault()
	if err != nil {
		return fmt.Errorf("open vault: %w", err)
	}
	defer v.Close()

	return mcppkg.Start(v, Version)
}
