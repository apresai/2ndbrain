package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

var mcpSetupCmd = &cobra.Command{
	Use:   "mcp-setup",
	Short: "Show MCP server setup instructions for AI tools",
	Long:  "Prints configuration snippets for connecting the 2ndbrain MCP server to Claude Code, Claude Desktop, Cursor, Gemini CLI, Amazon Q, and Kiro.",
	RunE:  runMCPSetup,
}

func init() {
	rootCmd.AddCommand(mcpSetupCmd)
}

func runMCPSetup(cmd *cobra.Command, args []string) error {
	vaultPath := "<vault-path>"
	v, err := openVault()
	if err == nil {
		vaultPath = v.Root
		v.Close()
	}

	fmt.Printf(`2ndbrain MCP Server Setup
=========================

Your vault: %s

The MCP server exposes 11 tools for searching, reading, creating,
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
───────────────────────────────────────────────

{
  "mcpServers": {
    "2ndbrain": {
      "command": "/usr/local/bin/2nb",
      "args": ["mcp-server"],
      "cwd": "%s"
    }
  }
}

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

───────────────────────────────────────────────
 Available Tools (11)
───────────────────────────────────────────────

  kb_info         Get vault overview (call first)
  kb_search       Hybrid keyword + semantic search
  kb_ask          Ask questions, get AI answers with sources
  kb_read         Read a document or specific section
  kb_list         List documents with filters
  kb_create       Create ADR, runbook, PRD, PR/FAQ, postmortem, or note
  kb_update_meta  Update frontmatter fields
  kb_related      Find connected documents via links
  kb_structure    Get document heading outline
  kb_delete       Delete a document
  kb_index        Rebuild search index + embeddings

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

`, vaultPath, vaultPath, vaultPath, vaultPath, vaultPath, vaultPath, vaultPath)

	return nil
}
