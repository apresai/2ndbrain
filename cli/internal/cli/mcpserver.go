package cli

import (
	"fmt"
	"log/slog"
	"os"
	"time"

	mcppkg "github.com/apresai/2ndbrain/internal/mcp"
	"github.com/spf13/cobra"
)

// defaultMCPIdleTimeout is the default activity-based self-exit. It is 0 (off):
// the server stays alive while its client is connected and relies on the
// stdin-EOF + parent-death paths to exit when the client goes away, rather than
// killing a live-but-quiet session. Set --idle-timeout / $2NB_MCP_IDLE_TIMEOUT
// to opt into an activity cap.
const defaultMCPIdleTimeout = 0

var mcpServerCmd = &cobra.Command{
	Use:   "mcp-server",
	Short: "Start an MCP server on stdio for AI tool integration",
	Long: `Starts a Model Context Protocol (MCP) server on stdio transport.

Add this to your Claude Code config:
  {"mcpServers": {"2ndbrain": {"command": "2nb", "args": ["mcp-server"]}}}

The server stays alive for as long as its client is connected. It exits
immediately when the client closes the connection, and promptly when the client
process dies (so a crashed or closed session never leaves an orphan holding the
index open). It does NOT self-exit while a client is connected.

To also cap on inactivity, set --idle-timeout or the 2NB_MCP_IDLE_TIMEOUT env
var (e.g. 30m, 1h); the default is 0 (no activity-based self-exit).

Available tools: kb_search, kb_read, kb_related, kb_create, kb_update_meta, kb_structure`,
	RunE: runMCPServer,
}

var mcpIdleTimeout time.Duration

func init() {
	mcpServerCmd.GroupID = "integr"
	mcpServerCmd.Flags().DurationVar(&mcpIdleTimeout, "idle-timeout", 0,
		"Also exit after this much inactivity (e.g. 30m, 1h; 0 = never). Default: $2NB_MCP_IDLE_TIMEOUT or 0 (stay alive while connected)")
	rootCmd.AddCommand(mcpServerCmd)
}

// resolveIdleTimeout picks the idle timeout: an explicit --idle-timeout flag
// wins (including an explicit 0 = never); else $2NB_MCP_IDLE_TIMEOUT if set and
// parseable; else the default (0 = no activity cap; the server stays alive while
// its client is connected). Pure for testability.
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
	// The MCP server serves write tools (kb_create/kb_append/…), so it resolves
	// the vault through the firm WRITE path: it binds to the vault Obsidian has
	// open by default — even when launched from a stray cwd (the Warp failure) —
	// honors a pinned --vault/2NB_VAULT, and refuses to start on a walked-up or
	// unconfigured target (acknowledge the latter with 2NB_UNCONFIGURED=1).
	v, err := openVaultAndSetActive()
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
