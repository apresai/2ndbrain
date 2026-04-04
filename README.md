# 2ndbrain

AI-native markdown knowledge base with semantic search. A Go CLI, MCP server, and native macOS editor that share a SQLite index — making your knowledge base searchable by both you and your AI coding assistant.

## Features

- **Hybrid search** — BM25 keyword + vector semantic search with Reciprocal Rank Fusion
- **RAG Q&A** — Ask questions about your vault, get answers with source citations
- **MCP server** — 9 tools for Claude Code, Cursor, and any MCP client
- **AI embeddings** — AWS Bedrock (Nova Embeddings v2 + Claude Haiku 4.5) with zero key setup via SSO
- **Schema validation** — Typed frontmatter, enum constraints, status state machines
- **Wikilinks** — `[[target#heading|alias]]` with link resolution and graph traversal
- **Document templates** — ADR, runbook, postmortem, note with enforced schemas
- **Native macOS editor** — SwiftUI + AppKit with live preview, sidebar, tabs, search
- **Local-first** — All data on disk as plain markdown. Obsidian-compatible.

## Quick Start

```bash
# Install
cd cli && make build && sudo make install
# Or: make install (builds both CLI and macOS app)

# Initialize a vault
2nb init --path ~/vault

# Create documents
2nb create "Use JWT for Authentication" --type adr
2nb create "Debug Auth Failures" --type runbook
2nb create "My Notes on Go" 

# Index with AI embeddings (uses AWS SSO credentials)
2nb index

# Search
2nb search "authentication"
2nb search "how does auth work" --type adr

# Ask questions (RAG)
2nb ask "What authentication approach did we choose and why?"

# Check AI status
2nb ai status
2nb models list
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
| `ask <question>` | RAG Q&A — search vault, generate answer with sources |
| `index` | Build search index + generate AI embeddings |
| `ai status` | Show AI provider, model readiness, embedding count |
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
| `lint [glob]` | Validate schemas and check broken wikilinks |
| `stale --since <days>` | Find stale documents |
| `export-context --types <types>` | Generate CLAUDE.md context bundle |

### Other

| Command | Description |
|---------|-------------|
| `mcp-server` | Start MCP server on stdio |
| `import-obsidian <path>` | Import Obsidian vault |
| `export-obsidian <path> [--strip-ids]` | Export to Obsidian format |

All commands support `--json`, `--yaml`, `--csv` for machine-readable output. Default is human-readable tables.

Commands work from any directory — the vault location is remembered in `~/.2ndbrain-active-vault`. Override with `--vault <path>` or `2NB_VAULT` env var.

## MCP Integration

The MCP server exposes 9 tools for AI coding assistants:

| Tool | Description |
|------|-------------|
| `kb_search` | Hybrid BM25 + semantic search with filters |
| `kb_ask` | RAG Q&A — answer questions with source citations |
| `kb_read` | Read full document or specific heading chunk |
| `kb_list` | List documents with metadata filters |
| `kb_related` | Graph traversal to find connected documents |
| `kb_create` | Create document from template with UUID |
| `kb_update_meta` | Update frontmatter fields with schema validation |
| `kb_structure` | Get document heading tree as JSON |
| `kb_delete` | Delete document from vault and index |

### Setup

**Claude Code** (`~/.claude.json`):
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

## AI Provider

2ndbrain uses AWS Bedrock by default (uses your existing AWS SSO credentials — no new API keys needed):

- **Embeddings**: Amazon Nova Embeddings v2 (1024 dims, $0.135/M tokens)
- **Generation**: Claude Haiku 4.5 (200K context, $0.80/$4.00 per M tokens)

```bash
# Check your AI setup
2nb ai status

# See available models
2nb models list
```

To use a different AWS profile:
```bash
2nb config set ai.bedrock.profile my-profile
2nb config set ai.bedrock.region us-west-2
```

## Document Types

| Type | Status States | Use For |
|------|--------------|---------|
| `adr` | proposed -> accepted -> deprecated/superseded | Architecture decisions |
| `runbook` | draft -> active -> archived | Operational procedures |
| `postmortem` | draft -> reviewed -> published | Incident analysis |
| `note` | draft -> complete | General knowledge |

## Development

```bash
# Build everything
make build          # CLI + macOS app

# Install
make install        # CLI to /usr/local/bin, app to ~/Applications

# Test
make test           # Go unit tests
make test-gui       # GUI tests (AppleScript automation)
make test-all       # Everything

# Version management
make bump-build     # 0.1.0 → 0.1.1
make bump-minor     # 0.1.0 → 0.2.0
make bump-major     # 0.1.0 → 1.0.0
```

Requires: Go 1.24+, CGO_ENABLED=1, macOS 14+ (Swift app).

## Architecture

```
cli/    Go CLI + MCP server (cobra, go-sqlite3, mcp-go, aws-sdk-go-v2)
app/    Swift macOS editor (SwiftUI, GRDB.swift, swift-markdown)
         |                    |
         +-------- shared ----+
              .2ndbrain/index.db (SQLite WAL)
```

## License

Private. Copyright Apres AI.
