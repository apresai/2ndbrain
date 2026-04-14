---
name: 2nb
description: 2ndbrain knowledge base — CLI commands, MCP tools, document format, and workflows. Use when working with 2ndbrain vaults, markdown documents with YAML frontmatter, wikilinks, or the 2nb CLI.
---

# 2ndbrain Knowledge Base

This project uses **2ndbrain** (`2nb`), an AI-native markdown knowledge base with a Go CLI, a native macOS editor, and an MCP server. All documents live as plain `.md` files with YAML frontmatter in a **vault** directory. The CLI and the macOS app share a SQLite index at `<vault>/.2ndbrain/index.db` via WAL mode.

## First — orient yourself in the vault

**Run these before any create / write / index action.** They prevent the most common failure mode (writing to the wrong vault because the active vault wasn't checked).

```bash
2nb config show   # vault_root, vault_dir, vault_name, full ai.* config
2nb ai            # defaults to `ai status` — confirms embeddings are ready
2nb list --limit 10   # what's already here?
```

If `vault_root` isn't the directory you expected, either `cd` into the right vault or pass `--vault <path>` on every command. The "active vault" is separate from your current working directory — it's stored in `~/.2ndbrain-active-vault` and survives across sessions. Running `2nb create` from inside `~/dev/obsidian` will still write to whatever vault is active, not to `~/dev/obsidian`, unless you pass `--vault ~/dev/obsidian`.

**If the current directory isn't a 2nb vault**, you have two choices:
1. Initialize it: `2nb init --path .` (adds a `.2ndbrain/` directory so 2nb can index it).
2. Treat it as a foreign filesystem: use direct file writes with correct frontmatter (see "Document Format" below) and skip `2nb create` entirely. This is how you'd add notes to an Obsidian vault that you *don't* want to convert to 2nb.

## Parent-command defaults

Every command group has a useful default action when called without a subcommand. `--help` still works everywhere because Cobra intercepts it before the default runs.

| Shortcut | Equivalent to | Useful for |
|---|---|---|
| `2nb ai` | `2nb ai status` | "Is my embedding/generation provider ready?" |
| `2nb models` | `2nb models list` | Browse verified model catalog |
| `2nb git` | `2nb git status` | Uncommitted files in a git-backed vault |
| `2nb mcp` | `2nb mcp status` | Which MCP clients are live? |
| `2nb skills` | `2nb skills list` | Which agents have this skill installed? |
| `2nb config` | `2nb config show` | Full config including vault paths |

## CLI Commands (46)

All commands support `--json`, `--yaml`, `--csv`, `--format`, `--porcelain`, `--vault <path>`, and `--verbose`. Prefer `--json` in scripts and agent pipelines.

### Read & query

| Command | Purpose |
|---------|---------|
| `2nb list` | List documents with `--type`, `--status`, `--tag`, `--sort`, `--limit` filters |
| `2nb read <path>` | Read full document or a specific heading chunk (`--chunk "Heading"`) |
| `2nb meta <path>` | View frontmatter; update with `--set key=value` |
| `2nb search <query>` | Hybrid BM25 + vector search. Shows `(rrf=X.XXX, cos=Y.YYY)` per result. `--threshold` overrides `ai.similarity_threshold` per-query. `--bm25-only` skips vector search. |
| `2nb ask "<question>"` | RAG Q&A — searches the vault, synthesizes an answer with source citations |
| `2nb related <path>` | Find docs connected via `[[wikilink]]` graph traversal (`--depth N`) |
| `2nb graph` | Output the full link graph as JSON adjacency list |
| `2nb suggest-links <path>` | Rank semantically related documents that would make good wikilink targets (excludes docs already linked) |
| `2nb stale --since 7d` | Docs not modified within N days |

### Write

| Command | Purpose |
|---------|---------|
| `2nb create --type <type> --title "Title"` | Create document from template. Generates UUID, timestamps, and type-appropriate frontmatter. |
| `2nb delete <path> [--force]` | Delete from disk and index |
| `2nb polish <path>` | AI copy-edit — returns JSON with `original` and `polished` body for diff review. **Does not write to disk**; you apply the result manually. |

### Index & housekeeping

| Command | Purpose |
|---------|---------|
| `2nb index` | Rebuild the search index and regenerate embeddings for changed docs |
| `2nb index --doc <path>` | Re-index + re-embed only one document (fast, skips unchanged hash) |
| `2nb lint [glob]` | Validate schemas, check broken wikilinks (ignores wikilinks inside code spans) |
| `2nb export-context --types <types>` | Generate a CLAUDE.md-compatible context bundle |

### Git (read-only, vault must be a git repo)

| Command | Purpose |
|---------|---------|
| `2nb git activity --since 7d` | Recent commits that touched vault files |
| `2nb git diff <path>` | Unified diff of a file against HEAD |
| `2nb git status` | Uncommitted + untracked files in the vault |

### Config, AI, MCP, skills

| Command | Purpose |
|---------|---------|
| `2nb config show` | Full config with `vault_root`, `vault_dir`, `vault_name` |
| `2nb config get <key>` | Read one key (e.g. `ai.provider`, `ai.similarity_threshold`) |
| `2nb config set <key> <value>` | Write one key |
| `2nb config set-key <provider>` | Store a provider API key in macOS Keychain |
| `2nb ai status` / `ai setup` / `ai local` / `ai embed <text>` | Provider status, wizard, readiness check, debug embedding |
| `2nb models list` / `models test <id>` / `models bench` | Verified catalog, smoke test, benchmark favorites |
| `2nb mcp status` | List live MCP servers via `.2ndbrain/mcp/<pid>.json` sidecar files |
| `2nb mcp-server` | Start the MCP server on stdio (this is what AI clients invoke) |
| `2nb skills install <agent> [--all] [--user]` | Install this SKILL.md for Claude Code, Cursor, Windsurf, GitHub Copilot, Kiro, Cline, Roo Code, or JetBrains Junie |
| `2nb import-obsidian <path>` / `export-obsidian` | Convert between 2nb and Obsidian vault formats |

## MCP Server Tools (16)

The MCP server (`2nb mcp-server`, started as a stdio subprocess by the client) exposes these tools. Use these instead of shell-outs when working through an MCP client — they're faster, atomic, and return structured JSON.

**Orientation**

| Tool | When to call it |
|---|---|
| `kb_info` | **Call this first** when starting a session in a new vault. Returns doc types, schemas, counts, and AI status. |
| `kb_list` | Discover what documents exist with metadata filters. Follow with `kb_read` to get content. |

**Query**

| Tool | When to call it |
|---|---|
| `kb_search` | Hybrid BM25 + semantic search. **Check the `vector_score` field** on each result — it's the raw cosine similarity, which is a better relevance signal than `score` (the RRF fusion score). Above ~0.4 = strong match; 0.2–0.4 = related; below 0.2 is filtered out entirely. |
| `kb_ask` | RAG Q&A — synthesizes an answer from the top matches. **Fall back to `kb_search`** if `kb_ask` returns "no relevant documents" — the threshold might be too tight. |
| `kb_read` | Full document or a specific heading chunk. Call after `kb_search`/`kb_list` to fetch content for the paths you found. |
| `kb_structure` | Heading tree as JSON. Use to pick a chunk name before calling `kb_read` with `chunk:`. |
| `kb_related` | BFS over the `[[wikilink]]` graph to depth N. Use for "what connects to this?" questions. |
| `kb_suggest_links` | Given a source doc, returns semantically related docs that aren't already linked from it. Useful while drafting to find existing context you should cite. |

**Write**

| Tool | When to call it |
|---|---|
| `kb_create` | Create a document from a type template. Auto-generates UUID + timestamps. **Search first** (`kb_search` or `kb_list`) to avoid duplicating existing content. |
| `kb_update_meta` | Change frontmatter without touching the body. Enforces schema/state-machine rules (e.g., `adr` status must follow `proposed → accepted → deprecated`). |
| `kb_delete` | Delete from disk + index. Irreversible. Confirm the path is correct before calling. |
| `kb_polish` | AI copy-edit. Returns both `original` and `polished` — **you decide** whether to apply the changes with a follow-up edit. The server doesn't write the polished text anywhere. |
| `kb_index` | Force a full reindex + embedding rebuild. Most operations auto-index; only call this after bulk external edits or imports. |

**Git (read-only, only when the vault is a git repo)**

| Tool | When to call it |
|---|---|
| `kb_git_activity` | Recent commits that touched vault files. Use for "what's been changing?" |
| `kb_git_diff` | Unified diff of one file against HEAD. Use before suggesting edits to avoid conflicts with uncommitted changes. |
| `kb_git_status` | Porcelain map of modified/untracked files. |

All three return `{"git_repo": false}` when the vault isn't git-backed — don't retry, just skip.

## Workflow recipes

### Answer a question from the vault

1. `kb_ask` with the question → get the synthesized answer + source list.
2. If the answer cites sources, `kb_read` each one to verify the claim (RAG can hallucinate details from retrieved chunks).
3. If `kb_ask` returns "no relevant documents", drop to `kb_search` with broader terms or fewer filters.

### Create a new linked note

1. `kb_search` with the topic to check for duplicates. If something exists, maybe you want `kb_update_meta` or an edit, not a new doc.
2. `kb_list --tag <related-tag>` to find the cluster this note belongs to.
3. `kb_create` with the title and type.
4. `kb_read` the new doc to see the template body.
5. Edit the body with `[[wikilinks]]` to the docs you found in step 2. The editor/CLI will re-index automatically on save.

### Review what changed recently

1. `kb_git_activity --since_days 7` (vault must be a git repo) for commit-level view.
2. `kb_list --sort modified --limit 20` for mtime-based view (works without git).
3. For any doc that looks interesting: `kb_git_diff` for the uncommitted delta or `kb_read` for the full content.

### Suggest related documents to link

1. `kb_suggest_links` with the current doc's path → ranked candidates with `score` (RRF), `snippet`, and already-linked docs filtered out.
2. For each candidate you want to use, insert `[[Title]]` at the appropriate spot in the body.
3. On save, the incremental re-embed picks up the new links and `kb_related` will show the connection next time.

### Polish a document's prose

1. `kb_polish` with the path → get `original` and `polished`.
2. Diff the two in your head (or with a diff tool). Check that wikilinks, code blocks, and frontmatter are preserved.
3. If you like the changes, write the polished body back with a normal file edit (polish itself doesn't touch disk).

## Search scoring, explained

`2nb search` and `kb_search` display two numbers per result:

- **`rrf`** — Reciprocal Rank Fusion score. Combines BM25 rank + vector rank. Good for *ordering* results; bad as an absolute relevance signal. A doc that matched only in the vector channel at rank 1 gets `rrf ≈ 0.016` even if the cosine is 0.9.
- **`cos`** — raw cosine similarity from the vector channel. This is what you actually want to look at for "is this relevant?". Rules of thumb (tune per embedding model):
  - ≥ 0.6 — strong semantic match
  - 0.35 – 0.6 — related
  - 0.20 – 0.35 — weakly related
  - < 0.20 — filtered out entirely by `ai.similarity_threshold`

If legitimate matches are being cut, lower the threshold: `2nb config set ai.similarity_threshold 0.15`. If noise is slipping through, raise it. Per-query overrides: `2nb search "foo" --threshold 0.35`.

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
| `id` | Yes | Stable UUID (survives renames, used for graph edges) |
| `title` | Yes | Document title |
| `type` | Yes | Document type: note, adr, runbook, postmortem, prd, prfaq |
| `status` | Varies | Type-specific status (see state machines below) |
| `tags` | No | Array of tags |
| `created` | Auto | Creation timestamp (ISO 8601) |
| `modified` | Auto | Last modification timestamp (ISO 8601) |

### Document Types and Status State Machines

| Type | Valid Statuses | Legal Transitions |
|------|---------------|-------------------|
| `note` | draft, complete | any → any |
| `adr` | proposed, accepted, deprecated, superseded | proposed → accepted/deprecated; accepted → deprecated/superseded |
| `runbook` | draft, active, archived | draft → active → archived |
| `postmortem` | draft, reviewed, published | draft → reviewed → published |
| `prd` | draft, review, approved, shipped, archived | draft → review → approved → shipped → archived; review/approved can return to draft |
| `prfaq` | draft, review, final | draft → review → final; review can return to draft |

`kb_update_meta` and `2nb meta --set` enforce these transitions. `kb_create` picks the initial status for the type.

### Wikilink Syntax

- `[[target]]` — Link by title or filename stem
- `[[target#heading]]` — Link to a specific heading
- `[[target|display text]]` — Aliased link

Wikilinks inside fenced code blocks or inline backticks are ignored by the extractor, so prose about wikilink syntax won't produce lint warnings.

## Key Conventions and Pitfalls

- **Check the active vault before writing** — `2nb config show` answers "which vault?". Don't assume `cwd` is the vault.
- **Every document has a UUID `id`** — use it for stable references, and never rewrite it during an edit.
- **Don't hand-edit `modified` timestamps** — the save path does this automatically; a manual edit can desync with `content_hash` and force a spurious re-embed.
- **Search before create** — the vault accumulates duplicates fast otherwise. `kb_search` + `kb_list --tag` are cheap.
- **Paths are vault-relative** — always. `2nb read foo.md` works; `2nb read /abs/path/foo.md` does not.
- **External file edits need a re-index** — if you use `Write` directly instead of `kb_update_meta`, follow up with `2nb index --doc <path>` or expect stale search results.
- **The polish and suggest-links tools don't write to disk** — they return suggestions. Apply them with a subsequent edit.
- **`status` transitions are enforced** — if you try to jump `adr` straight from proposed to superseded, `kb_update_meta` will reject it. Go through accepted first.
- **Foreign vaults** (Obsidian dir with no `.2ndbrain/`) — `2nb create` won't touch them. Use direct file writes with the frontmatter template above, OR run `2nb init --path <dir>` to convert it into a 2nb vault first.

## Vault Structure

```
vault-root/
├── .2ndbrain/
│   ├── config.yaml          # Vault config (name, embedding, ai.*)
│   ├── schemas.yaml         # Document type schemas
│   ├── index.db             # SQLite search index (shared with macOS editor)
│   ├── bench.db             # Model benchmark history (optional)
│   ├── mcp/<pid>.json       # One sidecar status file per running mcp-server
│   ├── recovery/            # Pre-write crash snapshots
│   └── logs/cli.log         # Structured slog output
├── document-1.md
├── document-2.md
└── subdirectory/
    └── document-3.md
```

The `.2ndbrain/` directory is the signal that a directory is a 2nb vault. If it's missing, the directory is just markdown files — 2nb won't index or write to it until `2nb init` creates the directory.
