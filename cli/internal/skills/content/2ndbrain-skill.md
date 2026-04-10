---
name: 2nb
description: 2ndbrain knowledge base — CLI commands, MCP tools, document format, and workflows. Use when working with 2ndbrain vaults, markdown documents with YAML frontmatter, wikilinks, or the 2nb CLI.
---

# 2ndbrain Knowledge Base

This project uses **2ndbrain**, an AI-native markdown knowledge base with a CLI (`2nb`), MCP server, and structured metadata. All documents live as plain `.md` files with YAML frontmatter in a vault directory.

## CLI Commands

| Command | Purpose |
|---------|---------|
| `2nb create --type <type> --title "Title"` | Create document from template (note, adr, runbook, postmortem, prd, prfaq) |
| `2nb read <path>` | Read full document or specific chunk (`--chunk "heading"`) |
| `2nb search <query>` | Hybrid BM25 search with `--type`, `--status`, `--tag`, `--limit` filters |
| `2nb list` | List documents with `--type`, `--status`, `--tag`, `--sort`, `--limit` filters |
| `2nb meta <path>` | View frontmatter; update with `--set key=value` |
| `2nb ask "<question>"` | RAG Q&A — searches vault and generates answer with source citations |
| `2nb index` | Rebuild search index and generate embeddings |
| `2nb lint [glob]` | Validate schemas and check broken wikilinks |
| `2nb related <path>` | Find related docs via link graph traversal (`--depth N`) |
| `2nb graph` | Output link graph as JSON adjacency list |
| `2nb stale --since 30d` | List documents not modified within N days |
| `2nb delete <path>` | Delete document from disk and index (`--force` to skip confirmation) |
| `2nb export-context` | Generate context bundle for AI consumption |
| `2nb import-obsidian <path>` | Import Obsidian vault (adds UUIDs, normalizes tags) |
| `2nb export-obsidian --target <path>` | Export vault to Obsidian format |

All commands support `--format json|csv|yaml`, `--json`, `--porcelain`, and `--vault <path>`.

## MCP Server Tools

The MCP server (`2nb mcp-server`) exposes these tools for AI agent integration:

| Tool | Purpose |
|------|---------|
| `kb_info` | Vault overview: name, document types, schemas, counts, AI status |
| `kb_search` | Hybrid search with type/status/tag filters |
| `kb_ask` | RAG Q&A — answer questions with source citations |
| `kb_read` | Read document or specific chunk by heading path |
| `kb_list` | List documents with filters |
| `kb_create` | Create document from template type |
| `kb_update_meta` | Update frontmatter fields with schema validation |
| `kb_related` | Traverse link graph to depth N |
| `kb_structure` | Get document heading hierarchy |
| `kb_delete` | Delete document from vault and index |
| `kb_index` | Rebuild search index and generate embeddings |

## Document Format

Documents are plain `.md` files with YAML frontmatter:

```yaml
---
id: <UUID>
title: Document Title
type: note
status: draft
tags: [tag1, tag2]
created: 2026-01-01T00:00:00Z
modified: 2026-01-01T00:00:00Z
---
# Document Title

Body content with [[wikilinks]] to other documents.
```

### Frontmatter Fields

| Field | Required | Description |
|-------|----------|-------------|
| `id` | Yes | Stable UUID (survives renames) |
| `title` | Yes | Document title |
| `type` | Yes | Document type: note, adr, runbook, postmortem, prd, prfaq |
| `status` | Varies | Type-specific status (see below) |
| `tags` | No | Array of tags |
| `created` | Auto | Creation timestamp |
| `modified` | Auto | Last modification timestamp |

### Document Types and Status Values

| Type | Status Values | Status Flow |
|------|--------------|-------------|
| note | draft, complete | — |
| adr | proposed, accepted, deprecated, superseded | proposed → accepted/deprecated → superseded |
| runbook | draft, active, archived | — |
| postmortem | draft, reviewed, published | — |
| prd | draft, review, approved, shipped, archived | draft → review → approved → shipped → archived |
| prfaq | draft, review, final | draft → review → final |

### Wikilink Syntax

- `[[target]]` — Link by title or filename
- `[[target#heading]]` — Link to specific section
- `[[target|display text]]` — Aliased link

## Key Conventions

- Every document has a UUID `id` field — use it for stable references
- Always update the `modified` timestamp when editing a document
- Use wikilinks `[[title]]` for cross-references between documents
- Use `2nb search` or `kb_search` before creating a new document to avoid duplicates
- Use `2nb ask` or `kb_ask` for synthesizing answers across multiple documents
- Run `2nb index` after bulk imports or external edits to keep the search index current
- Run `2nb lint` to validate schemas and find broken wikilinks

## Vault Structure

```
vault-root/
├── .2ndbrain/
│   ├── config.yaml      # Vault configuration
│   ├── schemas.yaml     # Document type schemas
│   └── index.db         # SQLite search index
├── document-1.md
├── document-2.md
└── subdirectory/
    └── document-3.md
```
