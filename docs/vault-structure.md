# Vault Structure

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
    ├── models/                   # Embedding models (future)
    ├── recovery/                 # Crash recovery snapshots
    └── logs/                     # Error logs
```

## Configuration (`config.yaml`)

```yaml
name: my-vault
version: "1"
embedding:
  model: nomic-embed-text-v1.5.Q8_0.gguf
  dimensions: 768
  batch_size: 100
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
| `documents` | Document metadata (id, path, title, type, status, timestamps, frontmatter JSON) |
| `chunks` | Heading-based sections (heading path, level, content, content hash, line range) |
| `chunks_fts` | FTS5 virtual table for BM25 keyword search |
| `links` | Wikilink edges (source -> target, with resolution status) |
| `tags` | Document tags for filtering |

## Ignored Paths

The indexer skips:
- Hidden files and directories (starting with `.`)
- `.env` and `.env.*` files
- Files starting with `credentials`
- Files containing `secret` in the name
- Non-markdown files

## Schemas (`schemas.yaml`)

See [templates.md](templates.md) for document type schemas and status state machines.
