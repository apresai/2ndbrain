# Quick Start

Get from zero to asking your Obsidian vault questions in about ten minutes. This guide covers the full setup: the `2nb` CLI, the SecondBrain macOS dashboard, the Obsidian plugin, AI configuration, and hooking your AI coding assistant up over MCP.

**What you end up with:** Obsidian stays your editor. The `2nb` CLI indexes your vault into a gitignored `.2ndbrain/` sidecar (your markdown is never modified) and gives you hybrid keyword + semantic search and RAG Q&A, from the terminal, from a chat panel inside Obsidian, and from any MCP client like Claude Code or Cursor.

## Prerequisites

- macOS 14+ with [Homebrew](https://brew.sh)
- [Obsidian](https://obsidian.md) with a vault you already use (a brand-new vault works too)
- For AI features, one of:
  - AWS credentials with Bedrock model access (the default provider), or
  - [Ollama](https://ollama.com) for fully local AI, or
  - an OpenRouter API key

You can skip the AI provider at first: indexing and BM25 keyword search work without one.

## 1. Install the app and CLI

```bash
brew install --cask apresai/tap/secondbrain
```

This installs the SecondBrain dashboard app to `/Applications` and pulls in the `2nb` CLI as a Homebrew formula dependency. The app is Developer ID-signed and Apple-notarized, so it launches without a Gatekeeper prompt.

If you only want the CLI (no dashboard):

```bash
brew install apresai/tap/2nb
```

Verify:

```bash
2nb --version
```

## 2. Launch the dashboard

```bash
open /Applications/SecondBrain.app
```

The dashboard reads Obsidian's own registry and binds to the vault Obsidian currently has open. On first launch the Welcome screen offers "Open your Obsidian vault: \<name\>"; accept it. The Home screen then shows four cards: Vault (with a badge confirming it matches Obsidian's open vault), AI, Claude Code (skill + MCP-server setup status), and Index.

Opening a vault in the dashboard also points the CLI's active vault at it, so a bare `2nb` command in your terminal resolves to the same vault. To do that from the terminal instead:

```bash
2nb vault set ~/path/to/your-obsidian-vault
```

You do not need `2nb init` or `2nb vault create` for an existing Obsidian vault; those are only for scaffolding a brand-new vault from scratch.

## 3. Install the Obsidian plugin

The plugin adds a chat panel and "2ndbrain AI:" commands inside Obsidian. Pick one install path:

**From the dashboard (easiest):** on the Home screen, the Vault card has an Obsidian plugin row. Click **Install** (or **Update** when a newer version ships).

**From the CLI:**

```bash
2nb plugin install
```

Both paths download the latest release assets into `<vault>/.obsidian/plugins/obsidian-2ndbrain/`.

**Via BRAT (auto-updating):** install the [BRAT](https://github.com/TfTHacker/obsidian42-brat) community plugin, then add the beta plugin `apresai/2ndbrain`.

**Then enable it (manual, always required):** in Obsidian go to **Settings → Community plugins**, reload if prompted, and toggle on **2ndbrain AI**. Obsidian has no API for enabling plugins, so this step is yours.

On first run the plugin opens a setup wizard (Download CLI → Connect AI → Index). Since you already installed the CLI via Homebrew, it detects the binary and skips ahead.

## 4. Connect AI

```bash
2nb ai setup
```

The wizard detects your credentials and walks you through provider and model choice. The default is AWS Bedrock with Claude Haiku 4.5 for generation and Amazon Nova-2 for embeddings; it verifies both models respond and reminds you to enable model access in the AWS console if needed.

Notes:

- The dashboard's AI card does the same thing graphically, with a readiness dot and a Test button.
- The dashboard runs without your shell's environment. If you authenticate Bedrock with a bearer token rather than `~/.aws` credentials, store it in the Keychain so the app can use it: run `2nb config set-key bedrock` and paste the token at the prompt.
- For fully local AI: `2nb ai local` checks Ollama readiness, then `2nb ai setup` and pick Ollama.
- Ollama and OpenRouter are opt-in; selection UIs show only Bedrock until you enable another provider.

Check status any time:

```bash
2nb ai status
```

## 5. Index your vault

```bash
2nb index
```

The first run creates the gitignored `.2ndbrain/` sidecar, parses every Markdown, Canvas, and Base file, and generates embeddings. Safe to run repeatedly: re-runs only re-embed documents whose content changed. The dashboard's Index card shows document and embedding counts with Sync and Re-embed All buttons, and notes you edit in Obsidian re-index automatically.

## 6. Search and ask

From the terminal:

```bash
# Hybrid BM25 + semantic search
2nb search "authentication"
2nb search "how does auth work" --type adr

# RAG Q&A with source citations
2nb ask "What authentication approach did we choose and why?"

# Interactive multi-turn chat
2nb chat
```

Inside Obsidian: click the 2ndbrain ribbon icon to open the chat panel for multi-turn Q&A over your vault, or use the command palette commands prefixed **"2ndbrain AI:"** (Open chat, Semantic Search, Ask AI, Find Similar Notes, Rebuild AI Index).

## 7. Connect your AI coding assistant (optional)

The MCP server exposes 22 tools (`kb_search`, `kb_ask`, `kb_read`, ...) to any MCP client. For Claude Code, add to `~/.claude.json`:

```json
{
  "mcpServers": {
    "2ndbrain": {
      "command": "2nb",
      "args": ["mcp-server"],
      "cwd": "/path/to/your/vault"
    }
  }
}
```

Run `2nb mcp-setup` for ready-to-paste snippets for Cursor, Claude Desktop, Gemini CLI, Amazon Q, and Kiro. See [mcp-integration.md](mcp-integration.md) for details, and `2nb skills install --all` to teach coding agents your vault's conventions.

## 8. Finishing touches

```bash
# Shell tab-completion (zsh; Homebrew installs handle this automatically)
2nb completion install

# Vault health report: index coverage, AI reachability, stale docs
2nb vault
```

## Keeping things up to date

A cask upgrade does not bump the CLI formula, so upgrade both:

```bash
brew upgrade --cask apresai/tap/secondbrain   # app
brew upgrade apresai/tap/twonb                # CLI
```

The dashboard's Home screen warns when the CLI is older than the app and offers an Update CLI button that runs the brew upgrade for you. Update the Obsidian plugin with `2nb plugin install` (or the dashboard's Update button); BRAT installs update themselves.

## Troubleshooting

- `2nb ai status` prints provider readiness and the active embedding state with one-line fix hints.
- If search warns about an embedding dimension or model mismatch (for example after switching providers), run `2nb index --force-reembed`.
- The dashboard's Vault Status tab (under Advanced) shows the same diagnostics graphically.
- Full user manual: [obsidian/user-guide.md](obsidian/user-guide.md). Plugin details: [the plugin README](../plugins/obsidian-2ndbrain/README.md).
