# 2ndbrain

[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![Release](https://img.shields.io/github/v/release/apresai/2ndbrain)](https://github.com/apresai/2ndbrain/releases)
[![Homebrew](https://img.shields.io/badge/homebrew-apresai%2Ftap%2F2nb-orange)](https://github.com/apresai/homebrew-tap)

AI-native markdown knowledge base with semantic search. A Go CLI, MCP server, and native macOS editor that share a SQLite index — making your knowledge base searchable by both you and your AI coding assistant.

## Install

```bash
brew install --cask apresai/tap/secondbrain
```

This installs both the `2nb` CLI and the SecondBrain macOS editor. If you only need the CLI:

```bash
brew install apresai/tap/2nb
```

Or download binaries from [GitHub Releases](https://github.com/apresai/2ndbrain/releases).

> **Note:** The editor is ad-hoc signed (not notarized). On first launch, right-click the app and choose "Open" to bypass the macOS Gatekeeper warning.

## Quick Start

```bash
# Initialize a vault
2nb init --path ~/vault

# Create documents
2nb create "Use JWT for Authentication" --type adr
2nb create "Debug Auth Failures" --type runbook
2nb create "My Notes on Go"

# Index with AI embeddings
2nb index

# Search (hybrid BM25 + semantic)
2nb search "authentication"
2nb search "how does auth work" --type adr

# Ask questions (RAG with source citations)
2nb ask "What authentication approach did we choose and why?"
```

## Features

- **Hybrid search** — BM25 keyword + vector semantic search with Reciprocal Rank Fusion
- **RAG Q&A** — Ask questions, get answers with source citations
- **MCP server** — 16 tools for Claude Code, Cursor, and any MCP client, with live status sidecar files and an observability panel
- **Suggest Links** — AI finds semantically related documents in your vault and proposes wikilinks to insert
- **Polish** — AI copy-editor fixes spelling, grammar, and awkward phrasing with a diff preview (Accept / Open in new tab / Reject)
- **Incremental re-embed on save** — embeddings rebuild only for the document whose content hash changed
- **Git integration (read-only)** — sidebar dots for uncommitted/untracked files, Recent Activity panel, Show Changes vs HEAD, MCP tools for AI clients
- **Merge conflict resolution** — FSEvents-backed 3-way diff dialog when a tab's file changes externally while dirty
- **Skill files** — One command to teach 8 AI coding agents about your vault
- **Three AI providers** — AWS Bedrock, OpenRouter, Ollama (fully local)
- **Schema validation** — Typed frontmatter, enum constraints, status state machines
- **Wikilinks** — `[[target#heading|alias]]` with link resolution and graph traversal
- **Document templates** — ADR, runbook, prd, prfaq, postmortem, note with enforced schemas
- **Native macOS editor** — SwiftUI + AppKit with live preview, editable preview (WYSIWYG via Turndown), tabs, search, graph view, autosave, crash recovery
- **Local-first** — All data on disk as plain markdown. Obsidian-compatible.

## AI Providers

2ndbrain supports three AI providers for embeddings and generation. Bedrock uses the [Converse API](https://docs.aws.amazon.com/bedrock/latest/userguide/conversation-inference-call.html), so any Bedrock model works — Claude, Nova, Llama, Mistral, and more.

| Provider | Embeddings | Generation | Setup |
|----------|-----------|------------|-------|
| **AWS Bedrock** | Nova Embeddings v2 | Nova Micro, Claude, Llama, any model | Uses existing AWS SSO — zero new keys |
| **OpenRouter** | Nemotron Embed (free) | Gemma 4 31B (free), GPT-4o, Claude, etc. | `OPENROUTER_API_KEY` env var |
| **Ollama** | nomic-embed-text | qwen2.5, gemma3, llama3 | `brew install ollama` — fully local |

### Quick Setup (Any Provider)

The setup wizard detects credentials and offers recommended defaults:

```bash
2nb ai setup
# → Pick provider (Bedrock / OpenRouter / Ollama)
# → Easy mode: recommended models, or Custom: pick from catalog
# → Tests connectivity, saves config, offers to index
```

### Local AI with Ollama

Run everything locally with no cloud calls:

```bash
# Check readiness (models, disk, RAM)
2nb ai local

# Guided setup (installs models, configures vault)
2nb ai setup

# Or configure manually
2nb config set ai.provider ollama
ollama pull embeddinggemma
ollama pull qwen2.5:0.5b
2nb index
```

### AWS Bedrock (Default)

Uses your existing AWS SSO credentials — no new API keys needed:

```bash
2nb ai status
2nb config set ai.bedrock.profile my-profile
2nb config set ai.bedrock.region us-west-2
```

### OpenRouter

```bash
export OPENROUTER_API_KEY=sk-or-...
2nb config set ai.provider openrouter
2nb index
```

## Model Catalog & Benchmarking

Browse verified models across all providers, test any model, and benchmark your favorites:

```bash
# See all verified models with pricing
2nb models list

# Discover vendor catalogs (Bedrock, OpenRouter, Ollama)
2nb models list --discover

# Check credentials and reachability
2nb models list --status

# Test if a model works before switching
2nb models test amazon.nova-micro-v1:0
2nb models test google/gemma-4-31b-it:free

# Benchmark your favorites
2nb models bench fav amazon.nova-micro-v1:0
2nb models bench fav us.anthropic.claude-haiku-4-5-20251001-v1:0
2nb models bench                  # runs embed/generate/search/rag probes
2nb models bench compare          # side-by-side latency leaderboard
2nb models bench history          # view past runs
```

Models are tiered as **verified** (tested with 2nb) or **unverified** (available from vendor, use `models test` to check). The benchmark suite stores results in `.2ndbrain/bench.db` for tracking performance over time.

## CLI Commands

Commands are organized into groups (`2nb --help` shows the full list).

**Global flags:** `--json`, `--csv`, `--yaml`, `--format`, `--porcelain`, `--vault`, `--verbose` (`-v` for debug logging to stderr and `.2ndbrain/logs/cli.log`)

### Getting Started

| Command | Description |
|---------|-------------|
| `init --path <dir>` | Initialize a new vault |
| `vault` | Show or set the active vault |

### Documents

| Command | Description |
|---------|-------------|
| `create <title> [--type adr\|runbook\|note\|postmortem]` | Create document from template |
| `read <path> [--chunk <heading>]` | Read document or specific section |
| `meta <path> [--set key=value]` | View or update frontmatter |
| `delete <path> [--force]` | Delete document from vault and index |
| `list [--type] [--status] [--tag] [--sort]` | List documents with filters |

### Search & AI

| Command | Description |
|---------|-------------|
| `search <query> [--type] [--status] [--tag] [--threshold]` | Hybrid BM25 + semantic search (shows `rrf` + raw `cos` scores) |
| `ask <question>` | RAG Q&A with source citations |
| `suggest-links <path> [--limit 10]` | Rank semantically related documents for wikilink insertion |
| `polish <path> [--system <prompt>]` | AI copy-edit a document (JSON with original + polished body) |
| `index [--doc <path>]` | Build search index + embeddings (full vault or a single document) |
| `ai status` | Show AI provider, models, embedding count |
| `ai setup` | Multi-provider setup wizard (easy mode or custom) |
| `ai local` | Check local AI readiness (Ollama, disk, RAM, models) |
| `ai embed <text>` | Generate embedding vector (debug) |
| `models list [--discover] [--status] [--provider]` | Verified model catalog + vendor discovery |
| `models test <model-id>` | Smoke-test any model (embed or generate probe) |
| `models bench` | Benchmark favorites with persistent history |
| `models bench fav <model-id>` | Add model to benchmark favorites |
| `models bench compare` | Side-by-side latency leaderboard |

### Git (read-only)

| Command | Description |
|---------|-------------|
| `git activity [--since 7d]` | Recent commits that touched vault files |
| `git diff <path>` | Unified diff of a file against HEAD |
| `git status` | Uncommitted/untracked files in the vault |

### Quality

| Command | Description |
|---------|-------------|
| `related <path> --depth <n>` | Find related docs via link graph |
| `graph <path>` | Output link graph as JSON |
| `lint [glob]` | Validate schemas, check broken wikilinks |
| `stale --since <days>` | Find stale documents |

### Integration

| Command | Description |
|---------|-------------|
| `mcp-server` | Start MCP server on stdio |
| `mcp-setup` | Show MCP setup instructions for all AI tools |
| `mcp status [--json]` | List live MCP server processes and their recent tool invocations |
| `export-context --types <types>` | Generate CLAUDE.md context bundle |
| `skills list` | List supported AI agents and install status |
| `skills install <agent> [--all] [--user]` | Install skill file for an AI coding agent |
| `skills uninstall <agent> [--all] [--user]` | Remove skill file for an AI coding agent |
| `skills show <agent>` | Preview skill content for an agent |

### Import / Export

| Command | Description |
|---------|-------------|
| `import-obsidian <path>` | Import Obsidian vault |
| `export-obsidian <path> [--strip-ids]` | Export to Obsidian format |

### Configuration

| Command | Description |
|---------|-------------|
| `config show` | Show full vault configuration (vault root + dir + name + all `ai.*` keys) |
| `config get <key>` | Get a config value (e.g., `ai.provider`, `ai.similarity_threshold`) |
| `config set <key> <value>` | Set a config value |
| `config set-key <provider>` | Store API key in macOS Keychain |

All commands support `--json`, `--yaml`, `--csv` for machine-readable output.

### Defaults and search scoring

- **Parent-command defaults** — running a command group without a subcommand invokes its most-useful read-only action: `2nb ai` → `ai status`, `2nb models` → `models list`, `2nb git` → `git status`, `2nb mcp` → `mcp status`, `2nb skills` → `skills list`, `2nb config` → `config show`. `--help` still works on every command.
- **Similarity threshold** — hybrid search drops vector hits whose cosine similarity is below `ai.similarity_threshold` (default `0.20`) so barely-related neighbors stop padding result lists. Configure with `2nb config set ai.similarity_threshold 0.25` or override per-query with `2nb search "foo" --threshold 0.35`.
- **Score display** — `2nb search` now shows `(rrf=X.XXX, cos=Y.YYY)` on each result. The `rrf` is the Reciprocal Rank Fusion score used for ranking; `cos` is the raw cosine similarity from the vector channel, which is what you actually want to look at when judging whether a result is relevant. If legitimate matches are being cut, lower the threshold; if noise is slipping through, raise it.

## MCP Server

The MCP server exposes 16 tools for AI coding assistants:

| Tool | Description |
|------|-------------|
| `kb_info` | Vault overview — doc types, schemas, counts, AI status |
| `kb_search` | Hybrid BM25 + semantic search with filters |
| `kb_ask` | RAG Q&A — answer questions with source citations |
| `kb_read` | Read full document or specific heading chunk |
| `kb_list` | List documents with metadata filters |
| `kb_create` | Create document from template |
| `kb_update_meta` | Update frontmatter with schema validation |
| `kb_related` | Graph traversal to find connected documents |
| `kb_structure` | Get document heading tree as JSON |
| `kb_delete` | Delete document from vault and index |
| `kb_index` | Rebuild search index and generate embeddings |
| `kb_suggest_links` | Suggest semantically related documents to wikilink from a source doc |
| `kb_polish` | AI copy-editor returns original + polished body for diff review |
| `kb_git_activity` | Recent git commits that touched vault files (read-only) |
| `kb_git_diff` | Unified diff of a file against HEAD |
| `kb_git_status` | Uncommitted/untracked files in the vault |

Each running `2nb mcp-server` writes a sidecar status file to `.2ndbrain/mcp/<pid>.json` with PID, start time, parent PID, and the last 50 tool invocations. Run `2nb mcp status` to list live servers, or use the Cmd+Shift+M status panel in the editor.

### Claude Code

Add to `~/.claude.json`:

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

### Cursor

Add to `.cursor/mcp.json`:

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

### Claude Desktop

Add to `~/Library/Application Support/Claude/claude_desktop_config.json`:

```json
{
  "mcpServers": {
    "2ndbrain": {
      "command": "/usr/local/bin/2nb",
      "args": ["mcp-server"],
      "cwd": "/path/to/your/vault"
    }
  }
}
```

Run `2nb mcp-setup` for config snippets for additional tools (Gemini CLI, Amazon Q, Kiro).

## Skill Files

Install a skill file to teach AI coding agents about your vault's CLI, MCP tools, and document format:

```bash
# See supported agents and status
2nb skills list

# Install for one agent
2nb skills install claude-code

# Install for all supported agents
2nb skills install --all

# Install globally (all projects, not just this vault)
2nb skills install claude-code --user
```

Supported agents: Claude Code, Cursor, Windsurf, GitHub Copilot, Kiro, Cline, Roo Code, JetBrains Junie.

## macOS Editor

A native SwiftUI + AppKit editor with:

- Markdown editing with Source / Split / Preview mode toggle (editable preview via Turndown.js ↔ WKWebView bridge)
- 30-second autosave (Off / 15s / 30s / 60s in Preferences), low-disk warning, filename collision suffixing, pre-write crash snapshots
- Merge conflict dialog — when a file changes externally while a tab is dirty, a 2-pane diff window shows On Disk vs Ancestor and Yours vs Ancestor
- Configurable editor font family and size (Preferences via Cmd+,, zoom with Cmd+=/Cmd+-/Cmd+0)
- Quick Open (Cmd+P), Command Palette (Cmd+Shift+P)
- Search panel with semantic search toggle (Cmd+Shift+F)
- Ask AI panel for RAG Q&A (Cmd+Shift+A)
- **Suggest Links** (Cmd+Shift+L) — click-to-insert AI-suggested wikilinks to semantically related documents
- **Polish** (Cmd+Option+P) — AI copy-edit the current document with an accept/reject diff preview
- **MCP Server Status** (Cmd+Shift+M) — see which AI clients are connected and which tools they've invoked, plus a live status-bar indicator
- **Recent Activity** (Cmd+Shift+G) — for vaults that are git repos, browse recent commits with 1/3/7/30-day window
- **Git diff viewer** — right-click any file in the sidebar → Show Changes vs HEAD
- **Git sidebar indicators** — orange dot for modified, blue dot for untracked
- AI setup wizard — guided provider/credentials/model configuration (Tools menu)
- Interactive AI status popover with staleness indicator and index rebuild
- Status bar shows document type, status, word count, chunk count, token estimate, AI dot, MCP dot
- Index rebuild dialog with confirmation, progress bars, and stats summary
- Tag drill-down navigation — click a tag to see filtered files, back button to return
- Export as PDF, HTML, or Markdown (Export menu, Cmd+Shift+X for PDF)
- Tools menu: Install AI Agent Skills, Connect AI Tools (MCP), MCP Server Status, Validate Knowledge Base, Rebuild Index
- Interactive link graph visualization
- Wikilink autocomplete, backlinks panel, outline view
- Tabs with dirty indicators, focus mode with hover-to-reveal toolbar
- Sidebar context menu: Open, Duplicate, Find Similar, Show Changes vs HEAD, Delete
- Drag `.md` files from Finder into the editor to open in new tabs
- 6 document templates: Note, ADR, Runbook, Postmortem, PRD, PR/FAQ
- Obsidian import/export
- Spotlight indexing, crash recovery (parse-on-open corruption detection), file watching

Build and install:

```bash
make install    # Installs to ~/Applications
```

## Document Types

| Type | Status States | Use For |
|------|--------------|---------|
| `adr` | proposed -> accepted -> deprecated/superseded | Architecture decisions |
| `runbook` | draft -> active -> archived | Operational procedures |
| `prd` | draft -> review -> approved -> shipped -> archived | Product requirements |
| `prfaq` | draft -> review -> final | Amazon-style press release / FAQ |
| `postmortem` | draft -> reviewed -> published | Incident analysis |
| `note` | draft -> complete | General knowledge |

Documents are plain `.md` files with YAML frontmatter:

```yaml
---
id: <UUID>
title: Use JWT for Authentication
type: adr
status: proposed
tags: [auth, security]
created: 2026-04-04T00:00:00Z
modified: 2026-04-04T00:00:00Z
---
# Use JWT for Authentication

Content with [[wikilinks]] to other documents.
```

## Obsidian Compatible

Import an existing Obsidian vault:

```bash
2nb import-obsidian ~/my-obsidian-vault
```

This adds UUIDs, normalizes tags from inline `#tag` to frontmatter, and builds the search index. Your files stay as plain markdown.

Export back to Obsidian anytime:

```bash
2nb export-obsidian ~/export-dir --strip-ids
```

## Development

```bash
make build          # CLI + macOS app
make install        # CLI to /usr/local/bin, app to ~/Applications
make test           # Go unit tests
make test-swift     # Swift unit tests (JSON decoding, parsing, wizard logic)
make test-gui       # GUI tests (AppleScript automation)
make test-all       # Go + Swift + GUI
```

Requires Go 1.24+, CGO_ENABLED=1, macOS 14+ (for Swift app).

See [CONTRIBUTING.md](CONTRIBUTING.md) for details.

## Releasing

Both the CLI and macOS editor are published to Homebrew via GitHub Actions.

```bash
# 1. Bump the version
make bump-build          # 0.1.8 → 0.1.9 (or bump-minor / bump-major)

# 2. Release — tags, pushes, and triggers CI
make release
```

`make release` updates `CHANGELOG.md`, commits, creates a git tag, and pushes it. GitHub Actions then:

1. Builds CLI binaries (arm64 + x86_64) via [GoReleaser](https://goreleaser.com)
2. Builds SecondBrain.app (arm64, `swift build -c release`)
3. Creates a [GitHub Release](https://github.com/apresai/2ndbrain/releases) with all archives
4. Pushes the Homebrew formula and cask to [`apresai/homebrew-tap`](https://github.com/apresai/homebrew-tap)

Users install with:

```bash
brew install apresai/tap/2nb                    # CLI
brew install --cask apresai/tap/secondbrain     # macOS editor (includes CLI)
```

To build a release locally (without GitHub Actions):

```bash
make release-local       # runs goreleaser locally (CLI only)
make package-app         # builds + zips SecondBrain.app locally
```

> **Note:** The editor is ad-hoc signed, not notarized with Apple Developer ID. Users must right-click > Open on first launch to bypass Gatekeeper.

## Architecture

```
cli/    Go CLI + MCP server (cobra, go-sqlite3, mcp-go, aws-sdk-go-v2)
app/    Swift macOS editor (SwiftUI, GRDB.swift, swift-markdown)
         |                    |
         +-------- shared ----+
              .2ndbrain/index.db (SQLite WAL)
```

The CLI and editor share the same SQLite database via WAL mode for concurrent access. All AI operations go through the provider interfaces in `cli/internal/ai/`.

## License

[MIT](LICENSE) - Copyright (c) 2026 Apres AI
