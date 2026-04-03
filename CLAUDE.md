# 2ndbrain

AI-native markdown knowledge base with a Go CLI, MCP server, and native macOS editor.

## Repository Layout

- `cli/` — Go CLI binary (`2nb`) + MCP server
- `app/` — Swift macOS editor (SwiftUI + AppKit)
- `reqs.md` — 200 EARS-format requirements
- `press-release.md` — Product vision document

## Build

```bash
cd cli && make build    # Builds cli/bin/2nb
cd cli && make test     # Runs all Go tests
cd cli && make install  # Installs to /usr/local/bin/2nb
cd app && swift build   # Builds the macOS editor
```

**Required:** CGO_ENABLED=1 and `-tags fts5` for all Go compilation (SQLite FTS5 + CGO).

## Go CLI (`cli/`)

**Module:** `github.com/apresai/2ndbrain`
**Framework:** cobra for CLI, mark3labs/mcp-go for MCP server
**Database:** mattn/go-sqlite3 with FTS5 for BM25 search

### Package Layout

| Package | Purpose |
|---------|---------|
| `internal/cli` | Cobra command definitions (one file per command) |
| `internal/vault` | Vault init/open, config, schemas, templates, indexer |
| `internal/document` | Markdown parsing, frontmatter, chunking, wikilinks |
| `internal/store` | SQLite database CRUD, migrations, link resolution |
| `internal/search` | BM25 search engine with structured filters |
| `internal/graph` | Link graph BFS traversal |
| `internal/mcp` | MCP server with 8 tools |
| `internal/output` | JSON/CSV/YAML formatters |
| `internal/testutil` | Test helpers (NewTestVault, CreateAndIndex) |

### Key Types

- `document.Document` — Parsed markdown with frontmatter, body, metadata
- `store.DB` — SQLite connection wrapper with CRUD operations
- `vault.Vault` — Root + config + schemas + DB handle
- `search.Engine` — BM25 search over FTS5 index
- `graph.Graph` — Nodes + edges from link traversal

### CLI Commands (14)

`init`, `create`, `read`, `meta`, `index`, `search`, `lint`, `stale`, `related`, `graph`, `export-context`, `list`, `delete`, `mcp-server`

### MCP Server (8 tools)

`kb_search`, `kb_read`, `kb_related`, `kb_create`, `kb_update_meta`, `kb_structure`, `kb_delete`, `kb_list`

### Testing

Tests use `t.TempDir()` for isolated vaults. Each test creates its own SQLite database.

```bash
cd cli && make test    # go test -race -tags fts5 ./...
```

## Swift macOS Editor (`app/`)

**Framework:** SwiftUI + AppKit, Swift 6.0, macOS 14+
**Dependencies:** GRDB.swift (SQLite), Yams (YAML), swift-markdown (parsing)
**Architecture:** MVVM with @Observable, NSTextView for editor

The Swift app reads the same `.2ndbrain/index.db` that the Go CLI writes to (WAL mode for concurrent access).

## Vault Format

Documents are plain `.md` files with YAML frontmatter containing `id` (UUID), `title`, `type`, `status`, `tags`, `created`, `modified`. The `.2ndbrain/` directory holds `config.yaml`, `schemas.yaml`, and `index.db`.

## Schema Validation

Document types (adr, runbook, note, postmortem) have schemas in `.2ndbrain/schemas.yaml` with typed fields, enum validation, and status state machines (e.g., ADR: proposed -> accepted -> deprecated/superseded).

## MCP Integration

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
