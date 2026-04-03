# 2ndbrain

A native macOS markdown editor designed for joint AI-human use. Built with a Go CLI, MCP server, and SwiftUI editor that share a SQLite index — making your knowledge base instantly searchable by both you and your AI coding assistant.

## Features

- **14 CLI commands** — init, create, read, search, list, delete, meta, lint, stale, related, graph, export-context, index, mcp-server
- **MCP server** — 8 tools for Claude Code, Cursor, and any MCP client
- **BM25 search** — Full-text search via SQLite FTS5 with type/status/tag filters
- **Schema validation** — Typed frontmatter fields, enum constraints, status state machines
- **Wikilinks** — `[[target#heading|alias]]` with link resolution and graph traversal
- **Document templates** — ADR, runbook, postmortem, note with enforced schemas
- **Native macOS editor** — SwiftUI + AppKit with live preview, sidebar, tabs, Spotlight integration
- **Local-first** — All data on disk as plain markdown. No cloud, no API keys.

## Quick Start

```bash
# Build the CLI
cd cli && make build

# Initialize a vault
./bin/2nb init --path ~/vault
cd ~/vault

# Create documents
2nb create --type adr --title "Use JWT for Authentication"
2nb create --type runbook --title "Debug Auth Failures"

# Index and search
2nb index
2nb search "authentication" --json
2nb search --type adr --status proposed --json

# List all documents
2nb list --type adr --json

# Connect to Claude Code
# Add to ~/.claude.json:
# {"mcpServers": {"2ndbrain": {"command": "2nb", "args": ["mcp-server"]}}}
```

## CLI Commands

| Command | Description |
|---------|-------------|
| `init --path <dir>` | Initialize a new vault |
| `create --type <type> --title <title>` | Create document from template |
| `read <path> [--chunk <heading>]` | Read document or specific section |
| `meta <path> [--set key=value]` | View or update frontmatter |
| `index` | Build/rebuild search index |
| `search [query] [--type] [--status] [--tag]` | Hybrid BM25 search |
| `list [--type] [--status] [--tag] [--sort]` | List documents with filters |
| `delete <path> [--force]` | Delete document from vault and index |
| `lint [glob]` | Validate schemas and check broken wikilinks |
| `stale --since <days>` | Find stale documents |
| `related <path> --depth <n>` | Find related docs via link graph |
| `graph <path>` | Output link graph as JSON |
| `export-context --types <types>` | Generate CLAUDE.md context bundle |
| `mcp-server` | Start MCP server on stdio |

All commands support `--json`, `--yaml`, `--csv` output formats.

## MCP Integration

The MCP server exposes 8 tools for AI coding assistants:

| Tool | Description |
|------|-------------|
| `kb_search` | Hybrid search with type/status/tag filters |
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
{"mcpServers": {"2ndbrain": {"command": "2nb", "args": ["mcp-server"], "cwd": "/path/to/vault"}}}
```

**Cursor** (`.cursor/mcp.json`):
```json
{"mcpServers": {"2ndbrain": {"command": "2nb", "args": ["mcp-server"], "cwd": "/path/to/vault"}}}
```

## Document Types

| Type | Status States | Use For |
|------|--------------|---------|
| `adr` | proposed -> accepted -> deprecated/superseded | Architecture decisions |
| `runbook` | draft -> active -> archived | Operational procedures |
| `postmortem` | draft -> reviewed -> published | Incident analysis |
| `note` | draft -> complete | General knowledge |

## Architecture

```
cli/    Go CLI + MCP server (cobra, mattn/go-sqlite3, mark3labs/mcp-go)
app/    Swift macOS editor (SwiftUI, GRDB.swift, swift-markdown)
         |                    |
         +-------- shared ----+
              .2ndbrain/index.db (SQLite WAL)
```

## Development

```bash
# Go CLI
cd cli && make build    # Build binary
cd cli && make test     # Run tests (requires CGO_ENABLED=1)
cd cli && make install  # Install to /usr/local/bin

# Swift editor
cd app && swift build   # Build macOS app
```

Requires: Go 1.24+, CGO_ENABLED=1, macOS 14+ for Swift app.

## License

Private. Copyright Apres AI.
