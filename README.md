# 2ndbrain

[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![Release](https://img.shields.io/github/v/release/apresai/2ndbrain)](https://github.com/apresai/2ndbrain/releases)
[![Homebrew](https://img.shields.io/badge/homebrew-apresai%2Ftap%2F2nb-orange)](https://github.com/apresai/homebrew-tap)

AI-native markdown knowledge base with semantic search. A Go CLI, MCP server, and native macOS editor that share a SQLite index — making your knowledge base searchable by both you and your AI coding assistant.

## Install

```bash
brew install apresai/tap/2nb
```

Or download binaries from [GitHub Releases](https://github.com/apresai/2ndbrain/releases).

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
- **MCP server** — 9 tools for Claude Code, Cursor, and any MCP client
- **Three AI providers** — AWS Bedrock, OpenRouter, Ollama (fully local)
- **Schema validation** — Typed frontmatter, enum constraints, status state machines
- **Wikilinks** — `[[target#heading|alias]]` with link resolution and graph traversal
- **Document templates** — ADR, runbook, postmortem, note with enforced schemas
- **Native macOS editor** — SwiftUI + AppKit with live preview, tabs, search, graph view
- **Local-first** — All data on disk as plain markdown. Obsidian-compatible.

## AI Providers

2ndbrain supports three AI providers for embeddings and generation:

| Provider | Embeddings | Generation | Setup |
|----------|-----------|------------|-------|
| **AWS Bedrock** | Nova Embeddings v2 | Claude Haiku 4.5 | Uses existing AWS SSO — zero new keys |
| **OpenRouter** | Nemotron Embed (free) | Qwen, Gemma, etc. | `OPENROUTER_API_KEY` env var |
| **Ollama** | embeddinggemma | qwen2.5:0.5b | `brew install ollama` — fully local |

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

## CLI Commands

### Core

| Command | Description |
|---------|-------------|
| `init --path <dir>` | Initialize a new vault |
| `create <title> [--type adr\|runbook\|note\|postmortem]` | Create document from template |
| `read <path> [--chunk <heading>]` | Read document or specific section |
| `meta <path> [--set key=value]` | View or update frontmatter |
| `delete <path> [--force]` | Delete document from vault and index |
| `list [--type] [--status] [--tag] [--sort]` | List documents with filters |

### Search & AI

| Command | Description |
|---------|-------------|
| `search <query> [--type] [--status] [--tag]` | Hybrid BM25 + semantic search |
| `ask <question>` | RAG Q&A with source citations |
| `index` | Build search index + generate embeddings |
| `ai status` | Show AI provider, models, embedding count |
| `ai local` | Check local AI readiness (Ollama, disk, RAM, models) |
| `ai setup` | Guided local AI setup with Ollama |
| `ai embed <text>` | Generate embedding vector (debug) |
| `models list [--type embed] [--free]` | List available AI models with pricing |

### Configuration

| Command | Description |
|---------|-------------|
| `config show` | Show full vault configuration |
| `config get <key>` | Get a config value (e.g., `ai.provider`) |
| `config set <key> <value>` | Set a config value |
| `config set-key <provider>` | Store API key in macOS Keychain |

### Knowledge Graph

| Command | Description |
|---------|-------------|
| `related <path> --depth <n>` | Find related docs via link graph |
| `graph <path>` | Output link graph as JSON |
| `lint [glob]` | Validate schemas, check broken wikilinks |
| `stale --since <days>` | Find stale documents |
| `export-context --types <types>` | Generate CLAUDE.md context bundle |

### Import/Export

| Command | Description |
|---------|-------------|
| `mcp-server` | Start MCP server on stdio |
| `import-obsidian <path>` | Import Obsidian vault |
| `export-obsidian <path> [--strip-ids]` | Export to Obsidian format |

All commands support `--json`, `--yaml`, `--csv` for machine-readable output.

## MCP Server

The MCP server exposes 11 tools for AI coding assistants:

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

## macOS Editor

A native SwiftUI + AppKit editor with:

- Markdown editing with live preview
- Quick Open (Cmd+P), Command Palette (Cmd+Shift+P)
- Search panel with semantic search toggle (Cmd+Shift+F)
- Ask AI panel for RAG Q&A (Cmd+Shift+A)
- Interactive link graph visualization
- Wikilink autocomplete, backlinks panel, outline view
- Tabs with dirty indicators, focus mode
- Obsidian import/export
- Spotlight indexing, crash recovery, file watching

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
make test-gui       # GUI tests (AppleScript automation)
make test-all       # Everything
make release        # Tag + push (GitHub Actions builds + publishes)
```

Requires Go 1.24+, CGO_ENABLED=1, macOS 14+ (for Swift app).

See [CONTRIBUTING.md](CONTRIBUTING.md) for details.

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
