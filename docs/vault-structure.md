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
    ├── metrics.db                # Performance observatory (operation timings)
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
| `chunks` | Heading-based sections (heading path, level, content, content hash, line range, `block_id` for Obsidian `^block-id` references) |
| `chunks_fts` | FTS5 virtual table for BM25 keyword search |
| `vec_chunks` | sqlite-vec vec0 virtual table of per-chunk embeddings for KNN: `chunk_id` TEXT PRIMARY KEY, `embedding float[dim] distance_metric=cosine`, plus auxiliary `+doc_id`/`+content_hash`/`+model` columns so KNN hits map back to documents without a join. Created lazily on the first embed; vec0 fixes the dimension at table creation, so a dimension change (model switch, force-reembed) drops and recreates it. Derived, regenerable state, so this needs no `schema_version` bump |
| | Mixed-dimension detection: there is no dimension column; widths derive from `length(embedding)/4`. `store.DistinctEmbeddingDims` scans for mixed widths because the single-sample `SampleEmbeddingDim` can match the active provider yet miss off-dim docs, and `DocumentsNeedingEmbedding` gates on content (not dimension), so a partial Matryoshka re-embed is only normalized by `2nb index --force-reembed` |
| `links` | Wikilink edges (source -> target, raw target, heading, alias, `block_id`, resolution status) |
| `tags` | Document tags for filtering |
| `aliases` | Frontmatter aliases (`doc_id`, `alias`) for wikilink resolution (added in schema v3) |
| `meta` | Generic key/value table (`key` TEXT PRIMARY KEY, `value` TEXT), added in schema v4. Stamps the indexing/embedding LOGIC generation the index was built with (`index_generation`/`embed_generation`, written by `vault.StampAfterIndex` on a full index or force-reembed and compared by `vault.CheckIndexFreshness`), so a release that changes chunking or embedding logic at the same model and dimension can prompt a reindex, something `schema_version` (shape only) and per-row content/model hashes cannot detect |
| `schema_version` | Single-row schema version; v2 adds document embedding columns; v3 adds the `aliases` table and `block_id` columns on `chunks` and `links`; v4 adds the `meta` table |

## Benchmark Database (`bench.db`)

Created on the first `2nb models bench` run. Backs benchmark history, favorites, and the BENCH column in `2nb models list`.

| Table | Purpose |
|-------|---------|
| `favorites` | Models to benchmark regularly (`provider`, `model_id`, `model_type`, `added_at`; primary key `provider, model_id`) |
| `runs` | One row per benchmark run (`timestamp`, `provider`, `model_id`, `plane`, `region`, `probe`, `latency_ms`, `ok`, `detail`, `vault_doc_count`) |
| `schema_version` | Single-row schema version (currently 2). Schema v2 adds the `plane` and `region` columns through an idempotent `ALTER TABLE ... ADD COLUMN`, so existing history is preserved. Runs recorded before v2 carry no route and are back-filled once with the route currently resolved for that model |

## Metrics Database (`metrics.db`)

The vault performance observatory, read by `2nb metrics` and the macOS Testing tab's Performance pane. Created lazily on first open; operations are recorded automatically (best-effort, never failing the op) by `index`, `index --doc`, `--force-reembed`, `search`, and `ask`, and by the MCP server (`source=mcp`).

| Table | Purpose |
|-------|---------|
| `operations` | One row per recorded operation: `id`, `ts` (RFC3339 UTC), `operation` (`index`\|`index_doc`\|`reembed`\|`search`\|`ask`), `source` (`cli`\|`mcp`\|`app`), `duration_ms`, `ok`, `error`, the index/embed counters (`files_scanned`, `docs_indexed`, `chunks_created`, `links_found`, `embedded`, `embed_skipped`, `embed_failed`, `embed_retries`, `embed_ms`, `total_chars`, `embedding_model`, `embedding_dims`), the query fields (`result_count`, `mode`), the token fields (`input_tokens`, `output_tokens`), and `cli_version` |
| `schema_version` | Single-row schema version, currently 3. Schema v2 adds the token columns and v3 adds `embed_retries` (the provider retries an operation rode out), each via an idempotent `ALTER TABLE ADD COLUMN` migration, so an existing `metrics.db` keeps its history (old rows default to 0) rather than being dropped. The migrated columns are deliberately absent from the base `CREATE`, so a fresh install takes the same migration path an upgrade does |

After every insert the table is pruned to the newest ~200 rows per operation type (`DefaultPerTypeCap` in `cli/internal/metrics/db.go`); partitioning the cap by type means a flood of `search` rows can never evict the low-volume, high-value `index` build history. Query text is never stored (the table has no query-text column, by design, for privacy). Derived rates (docs/sec, embeddings/sec, chars/sec) are computed at read time, never stored.

## Ignored Paths

The indexer skips:
- Hidden files and directories (starting with `.`)
- `.env` and `.env.*` files
- Files starting with `credentials`
- Files containing `secret` in the name
- Non-markdown files

## Schemas (`schemas.yaml`)

See [templates.md](templates.md) for document type schemas and status state machines.
