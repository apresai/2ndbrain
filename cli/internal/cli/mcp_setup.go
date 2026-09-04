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
  2nb mcp-setup --json                              # the same guide as a record
  2nb mcp-setup | pbcopy                            # copy to clipboard (macOS)`,
	RunE: runMCPSetup,
}

func init() {
	mcpSetupCmd.GroupID = "integr"
	rootCmd.AddCommand(mcpSetupCmd)
}

// MCPSetupClient is one client's ready-to-paste configuration.
type MCPSetupClient struct {
	Client string `json:"client"`
	// ConfigPath is where that client keeps the file the snippet goes in.
	ConfigPath string `json:"config_path"`
	// Notes are the client-specific gotchas the human output prints under the
	// heading (an absolute path for a GUI app, Grok's server-key rule).
	Notes []string `json:"notes,omitempty"`
	// Snippet is the configuration itself, in that client's own syntax.
	Snippet string `json:"snippet"`
}

// MCPSetupGuide is the `mcp-setup --json` record. mcp-setup is a REPORT, and it
// ignored --format entirely: every format printed the same human prose, so a
// caller asking for json got a wall of box-drawing characters.
type MCPSetupGuide struct {
	Vault    string            `json:"vault"`
	Clients  []MCPSetupClient  `json:"clients"`
	Tools    []mcppkg.ToolInfo `json:"tools"`
	Examples []string          `json:"example_prompts"`
}

func runMCPSetup(cmd *cobra.Command, args []string) error {
	// A setup guide is a record, not a document body. Refused before the vault
	// lookup so the answer never depends on whether one resolved.
	if err := refuseBodylessFormat(cmd, "mcp-setup"); err != nil {
		return err
	}

	vaultPath := "<vault-path>"
	v, err := openVault()
	if err == nil {
		vaultPath = v.Root
		v.Close()
	}

	guide := MCPSetupGuide{
		Vault:    vaultPath,
		Clients:  mcpSetupClients(vaultPath),
		Tools:    mcppkg.ToolCatalog(),
		Examples: mcpSetupExamples,
	}

	if done, err := emitStructured(cmd, guide); done {
		return err
	}
	printMCPSetupGuide(guide)
	return nil
}

// mcpSetupClients builds every client's snippet against one vault path. The
// human output and the structured record are rendered from this same list, so
// neither can drift from the other.
func mcpSetupClients(vaultPath string) []MCPSetupClient {
	cwdJSON := fmt.Sprintf(`{
  "mcpServers": {
    "2ndbrain": {
      "command": "2nb",
      "args": ["mcp-server"],
      "cwd": %q
    }
  }
}`, vaultPath)

	return []MCPSetupClient{
		{
			Client:     "Claude Code",
			ConfigPath: "~/.claude.json",
			Snippet:    cwdJSON,
		},
		{
			Client:     "Claude Desktop",
			ConfigPath: "~/Library/Application Support/Claude/claude_desktop_config.json",
			Notes: []string{
				"Tip: run  2nb mcp install --client claude-desktop  to write this for you.",
				`It's a GUI app (no shell PATH), so use an ABSOLUTE 2nb path and NO "cwd"/"url" field (a "url" field silently corrupts the file). Restart the app to apply.`,
			},
			Snippet: fmt.Sprintf(`{
  "mcpServers": {
    "2ndbrain": {
      "command": "/opt/homebrew/bin/2nb",
      "args": ["mcp-server", "--vault", %q]
    }
  }
}`, vaultPath),
		},
		{
			Client:     "Codex",
			ConfigPath: "~/.codex/config.toml",
			Notes: []string{
				`Tip: run  2nb mcp install --client codex  (uses "codex mcp add"). Restart your Codex session.`,
			},
			Snippet: fmt.Sprintf(`[mcp_servers.2ndbrain]
command = "2nb"
args = ["mcp-server", "--vault", %q]`, vaultPath),
		},
		{
			Client:     "Grok",
			ConfigPath: "~/.grok/config.toml",
			Notes: []string{
				`Grok also imports servers from ~/.claude.json. Use the key "twonb", not "2ndbrain": Grok prefixes tools as <server>__<name> and keeps only names matching ^[a-zA-Z_][a-zA-Z0-9_-]{0,63}$, so a key that starts with a digit catalogs as 0 tools even when grok mcp doctor reports 22.`,
			},
			Snippet: fmt.Sprintf(`[mcp_servers.twonb]
command = "2nb"
args = ["mcp-server", "--vault", %q]`, vaultPath),
		},
		{Client: "Cursor", ConfigPath: ".cursor/mcp.json", Snippet: cwdJSON},
		{Client: "Gemini CLI", ConfigPath: "~/.gemini/settings.json", Snippet: cwdJSON},
		{
			Client:     "Amazon Q CLI",
			ConfigPath: "~/.aws/amazonq/mcp.json",
			Snippet: fmt.Sprintf(`{
  "mcpServers": {
    "2ndbrain": {
      "command": "2nb",
      "args": ["mcp-server"],
      "cwd": %q,
      "transport": "stdio"
    }
  }
}`, vaultPath),
		},
		{Client: "Kiro", ConfigPath: ".kiro/mcp.json", Snippet: cwdJSON},
	}
}

// mcpSetupExamples are the prompts the human output suggests trying.
var mcpSetupExamples = []string{
	"What's in my knowledge base?",
	"Search for authentication patterns",
	"What authentication approach did we choose and why?",
	"Create an ADR for switching to PostgreSQL",
	"Write a PRD for the mobile app redesign",
	"List all draft runbooks",
	"Show me the Decision section of use-jwt-for-auth.md",
	"What's related to the Stripe integration?",
	"Mark the JWT ADR as accepted",
	"Reindex the knowledge base",
}

const mcpSetupRule = "───────────────────────────────────────────────"

func printMCPSetupGuide(g MCPSetupGuide) {
	fmt.Printf("2ndbrain MCP Server Setup\n=========================\n\n")
	fmt.Printf("Your vault: %s\n\n", g.Vault)
	fmt.Printf("The MCP server exposes %d tools for searching, reading, creating,\nand asking questions about your knowledge base.\n\n", len(g.Tools))

	for _, c := range g.Clients {
		fmt.Println(mcpSetupRule)
		fmt.Printf(" %s  (%s)\n", c.Client, c.ConfigPath)
		for _, n := range c.Notes {
			fmt.Printf("   %s\n", n)
		}
		fmt.Println(mcpSetupRule)
		fmt.Printf("\n%s\n\n", c.Snippet)
	}

	fmt.Printf("%s\n Available Tools (%d)\n%s\n\n", mcpSetupRule, len(g.Tools), mcpSetupRule)
	for _, t := range g.Tools {
		fmt.Printf("  %-18s %s\n", t.Name, t.Description)
	}

	fmt.Printf("\n%s\n Example Prompts to Try\n%s\n\n", mcpSetupRule, mcpSetupRule)
	for _, e := range g.Examples {
		fmt.Printf("  %q\n", e)
	}
	fmt.Println()
}
