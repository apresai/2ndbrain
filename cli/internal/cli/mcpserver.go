package cli

import (
	"fmt"
	"log/slog"

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
	setupFileLogging(v)

	// Register AI providers so kb_create / kb_append / kb_replace_section /
	// kb_index can embed inline, matching the CLI write path. Without this the
	// MCP server's embedder lookup fails and every embed silently skips, so
	// agent-authored docs would have no vector embedding until a later
	// `2nb index`. Idempotent; warnings go to stderr (never stdout, which
	// carries the JSON-RPC stream).
	initAIProviders(v)

	slog.Info("MCP server started", "transport", "stdio")

	return mcppkg.Start(v, Version)
}
