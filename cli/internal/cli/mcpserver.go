package cli

import (
	"fmt"
	"log/slog"
	"os"
	"time"

	mcppkg "github.com/apresai/2ndbrain/internal/mcp"
	"github.com/spf13/cobra"
)

const defaultMCPIdleTimeout = 30 * time.Minute

var mcpServerCmd = &cobra.Command{
	Use:   "mcp-server",
	Short: "Start an MCP server on stdio for AI tool integration",
	Long: `Starts a Model Context Protocol (MCP) server on stdio transport.

Add this to your Claude Code config:
  {"mcpServers": {"2ndbrain": {"command": "2nb", "args": ["mcp-server"]}}}

The server exits on its own after 30 minutes of inactivity so a closed AI
session doesn't leave an orphaned process holding the index open (your client
respawns it on the next call). Override with --idle-timeout or the
2NB_MCP_IDLE_TIMEOUT env var (e.g. 1h); set it to 0 to never self-exit.

Available tools: kb_search, kb_read, kb_related, kb_create, kb_update_meta, kb_structure`,
	RunE: runMCPServer,
}

var mcpIdleTimeout time.Duration

func init() {
	mcpServerCmd.GroupID = "integr"
	mcpServerCmd.Flags().DurationVar(&mcpIdleTimeout, "idle-timeout", 0,
		"Exit after this much inactivity (e.g. 30m, 1h; 0 = never). Default: $2NB_MCP_IDLE_TIMEOUT or 30m")
	rootCmd.AddCommand(mcpServerCmd)
}

// resolveIdleTimeout picks the idle timeout: an explicit --idle-timeout flag
// wins (including an explicit 0 = never); else $2NB_MCP_IDLE_TIMEOUT if set and
// parseable; else the 30m default. Pure for testability.
func resolveIdleTimeout(flagChanged bool, flagVal time.Duration, env string) time.Duration {
	if flagChanged {
		return flagVal
	}
	if env != "" {
		if d, err := time.ParseDuration(env); err == nil {
			return d
		}
		slog.Warn("ignoring invalid 2NB_MCP_IDLE_TIMEOUT; using default", "value", env)
	}
	return defaultMCPIdleTimeout
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

	idleTimeout := resolveIdleTimeout(cmd.Flags().Changed("idle-timeout"), mcpIdleTimeout, os.Getenv("2NB_MCP_IDLE_TIMEOUT"))
	slog.Info("MCP server started", "transport", "stdio", "idle_timeout", idleTimeout.String())

	return mcppkg.Start(v, Version, idleTimeout)
}
