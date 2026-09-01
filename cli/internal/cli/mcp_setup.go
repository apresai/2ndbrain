package cli

import (
	"fmt"

	mcppkg "github.com/apresai/2ndbrain/internal/mcp"
	"github.com/spf13/cobra"
)

var mcpSetupCmd = &cobra.Command{
	Use:   "mcp-setup",
	Short: "Show MCP server setup instructions for AI tools",
	Long:  "Prints configuration snippets for connecting the 2ndbrain MCP server to Claude Code, Claude Desktop, Cursor, Codex, Grok, Gemini CLI, Amazon Q, and Kiro. The printed snippets include the active vault path so they're ready to paste.",
	Example: `  2nb mcp-setup                                     # print all snippets
  2nb mcp-setup | pbcopy                            # copy to clipboard (macOS)`,
	RunE: runMCPSetup,
}

func init() {
	mcpSetupCmd.GroupID = "integr"
	rootCmd.AddCommand(mcpSetupCmd)
}

func runMCPSetup(cmd *cobra.Command, args []string) error {
	vaultPath := "<vault-path>"
	v, err := openVault()
	if err == nil {
		vaultPath = v.Root
		v.Close()
	}

	catalog := mcppkg.ToolCatalog()

	fmt.Printf(`2ndbrain MCP Server Setup
=========================

Your vault: %s

The MCP server exposes %d tools for searching, reading, creating,
and asking questions about your knowledge base.

───────────────────────────────────────────────
 Claude Code  (~/.claude.json)
───────────────────────────────────────────────

{
  "mcpServers": {
    "2ndbrain": {
      "command": "2nb",
      "args": ["mcp-server"],
      "cwd": "%s"
    }
  }
}

───────────────────────────────────────────────
 Claude Desktop  (~/Library/Application Support/Claude/claude_desktop_config.json)
   Tip: run  2nb mcp install --client claude-desktop  to write this for you.
   It's a GUI app (no shell PATH), so use an ABSOLUTE 2nb path and NO "cwd"/"url"
   field (a "url" field silently corrupts the file). Restart the app to apply.
───────────────────────────────────────────────

{
  "mcpServers": {
    "2ndbrain": {
      "command": "/opt/homebrew/bin/2nb",
      "args": ["mcp-server", "--vault", "%s"]
    }
  }
}

───────────────────────────────────────────────
 Codex  (~/.codex/config.toml)
   Tip: run  2nb mcp install --client codex  (uses "codex mcp add"). Restart your Codex session.
───────────────────────────────────────────────

[mcp_servers.2ndbrain]
command = "2nb"
args = ["mcp-server", "--vault", "%s"]

───────────────────────────────────────────────
 Grok  (~/.grok/config.toml)
   Grok also imports servers from ~/.claude.json. Use the key "twonb", not
   "2ndbrain": Grok prefixes tools as <server>__<name> and keeps only names
   matching ^[a-zA-Z_][a-zA-Z0-9_-]{0,63}$, so a key that starts with a digit
   catalogs as 0 tools even when grok mcp doctor reports 22.
───────────────────────────────────────────────

[mcp_servers.twonb]
command = "2nb"
args = ["mcp-server", "--vault", "%s"]

───────────────────────────────────────────────
 Cursor  (.cursor/mcp.json)
───────────────────────────────────────────────

{
  "mcpServers": {
    "2ndbrain": {
      "command": "2nb",
      "args": ["mcp-server"],
      "cwd": "%s"
    }
  }
}

───────────────────────────────────────────────
 Gemini CLI  (~/.gemini/settings.json)
───────────────────────────────────────────────

{
  "mcpServers": {
    "2ndbrain": {
      "command": "2nb",
      "args": ["mcp-server"],
      "cwd": "%s"
    }
  }
}

───────────────────────────────────────────────
 Amazon Q CLI  (~/.aws/amazonq/mcp.json)
───────────────────────────────────────────────

{
  "mcpServers": {
    "2ndbrain": {
      "command": "2nb",
      "args": ["mcp-server"],
      "cwd": "%s",
      "transport": "stdio"
    }
  }
}

───────────────────────────────────────────────
 Kiro  (.kiro/mcp.json)
───────────────────────────────────────────────

{
  "mcpServers": {
    "2ndbrain": {
      "command": "2nb",
      "args": ["mcp-server"],
      "cwd": "%s"
    }
  }
}

`, vaultPath, len(catalog), vaultPath, vaultPath, vaultPath, vaultPath, vaultPath, vaultPath, vaultPath, vaultPath)

	fmt.Printf("───────────────────────────────────────────────\n Available Tools (%d)\n───────────────────────────────────────────────\n\n", len(catalog))
	for _, t := range catalog {
		fmt.Printf("  %-18s %s\n", t.Name, t.Description)
	}
	fmt.Printf(`
───────────────────────────────────────────────
 Example Prompts to Try
───────────────────────────────────────────────

  "What's in my knowledge base?"
  "Search for authentication patterns"
  "What authentication approach did we choose and why?"
  "Create an ADR for switching to PostgreSQL"
  "Write a PRD for the mobile app redesign"
  "Create a PR/FAQ for the new AI feature"
  "List all draft runbooks"
  "Show me the Decision section of use-jwt-for-auth.md"
  "What's related to the Stripe integration?"
  "Mark the JWT ADR as accepted"
  "Reindex the knowledge base"

`)

	return nil
}
