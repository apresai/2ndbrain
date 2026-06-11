# Vault Structure

> [!IMPORTANT]
> The directory layout and UUID-first frontmatter format described in this document are superseded for 0.5.0 by the Obsidian-native coexistence and path-based identity models. See [docs/obsidian/vault-coexistence.md](obsidian/vault-coexistence.md) and [docs/obsidian/identity-model.md](obsidian/identity-model.md) for details.

A 2ndbrain vault is a directory containing plain markdown files and a `.2ndbrain/` configuration directory.

## Directory Layout

```
my-vault/
├── use-jwt-for-auth.md          # Documents (plain markdown)
├── debug-auth-failures.md
├── api-outage-march-2026.md
├── notes/                        # Subdirectories supported
│   └── project-ideas.md
└── .2ndbrain/                    # Vault metadata (gitignored)
    ├── config.yaml               # Vault configuration
    ├── schemas.yaml              # Document type schemas
    ├── index.db                  # SQLite search index (FTS5 + metadata)
    ├── index.db-wal              # WAL file (multi-process access)
    ├── bench.db                  # Benchmark history and favorites
    ├── mcp/                      # Runtime status files for mcp-server processes
    ├── models/                   # Embedding models (future)
    ├── recovery/                 # Crash recovery snapshots
    └── logs/                     # Error logs
```

## Configuration (`config.yaml`)

```yaml
name: my-vault
version: "1"
ai:
  provider: bedrock
  embedding_model: amazon.nova-2-multimodal-embeddings-v1:0
  generation_model: us.anthropic.claude-haiku-4-5-20251001-v1:0
  dimensions: 1024
  similarity_threshold: 0
  ollama:
    endpoint: http://localhost:11434
    disabled: true       # Ollama is opt-in; the setup wizard enables it
  bedrock:
    profile: default
    region: us-east-1
  openrouter:
    api_key_env: OPENROUTER_API_KEY
    disabled: true       # OpenRouter is opt-in
```

## Frontmatter Format

Every document should have YAML frontmatter:

```yaml
---
id: 72e35128-5e6d-48b9-bb7a-716f1109b73d
title: Use JWT for Authentication
type: adr
status: proposed
tags:
  - auth
  - jwt
created: 2026-04-03T02:21:31Z
modified: 2026-04-03T02:21:37Z
---
```

**Required fields**: `id` (auto-generated UUID), `title`, `type`

## Index Database (`index.db`)

SQLite database shared by the Go CLI and Swift app via WAL mode.

| Table | Purpose |
|-------|---------|
| `documents` | Document metadata (id, path, title, doc_type, status, timestamps, content_hash, frontmatter JSON, embedding, embedding_model, embedding_hash) |
| `chunks` | Heading-based sections (heading path, level, content, content hash, line range) |
| `chunks_fts` | FTS5 virtual table for BM25 keyword search |
| `links` | Wikilink edges (source -> target, with resolution status) |
| `tags` | Document tags for filtering |
| `schema_version` | Single-row schema version; v2 adds document embedding columns; v3 adds the `aliases` table and `block_id` columns on `chunks` and `links` |

## Ignored Paths

The indexer skips:
- Hidden files and directories (starting with `.`)
- `.env` and `.env.*` files
- Files starting with `credentials`
- Files containing `secret` in the name
- Non-markdown files

## Schemas (`schemas.yaml`)

See [templates.md](templates.md) for document type schemas and status state machines.
