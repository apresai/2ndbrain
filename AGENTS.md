# 2ndbrain

Obsidian-native AI companion. **Obsidian stays the editor**; the Go CLI (`2nb`) + MCP server are the engine that indexes, searches, and answers (RAG) over a real Obsidian vault. A thin Obsidian plugin and a macOS configuration dashboard wrap the CLI. `2nb` writes only a gitignored `.2ndbrain/` sidecar and never rewrites your markdown (one explicit, user-invoked exception: `2nb plugin install` writes the plugin bundle under `.obsidian/plugins/obsidian-2ndbrain/`).

> [!NOTE]
> [CLAUDE.md](CLAUDE.md) at the repo root is the full, maintained project reference (architecture, release pipeline, command catalog, app internals). This file is the condensed version for coding agents; when the two disagree, trust CLAUDE.md and fix this file.

## Repository Layout

- `cli/` — Go CLI binary (`2nb`) + MCP server (the engine)
- `app/` — Swift macOS configuration & companion dashboard, **not an editor** (SwiftUI + AppKit)
- `plugins/obsidian-2ndbrain/` — thin Obsidian plugin that shells out to `2nb`
- `docs/` — user-facing docs, including [docs/quick-start.md](docs/quick-start.md) and the Obsidian-pivot docs under `docs/obsidian/`
- `reqs.md` — EARS-format requirements specification
- `test-plan.md` — Requirements validation test plan

## Versioning

Format: `major.minor.build`. Single source of truth: `VERSION` file at repo root.

| Component | How it reads the version |
|-----------|------------------------|
| Go CLI | `cli/Makefile` reads `../VERSION` via LDFLAGS into `internal/cli.Version` |
| Swift app | `app/Sources/SecondBrain/Version.swift` (generated; never edit by hand) |
| Obsidian plugin | `manifest.json`/`package.json` synced by `make version-plugin` |

Bump targets (root `Makefile`): `make bump-build` (`0.8.0` → `0.8.1`), `make bump-minor`, `make bump-major`, `make set-version V=x.y.z`. Each regenerates `Version.swift` and syncs the plugin version files.

The release contract (front-door command, products, per-product install + verify) is declared in [`.release.yaml`](.release.yaml) at the repo root: the machine-readable file the `oss-release` skill reads to release and verify this repo. A packaging change updates the Makefile (and `.release.yaml` only if a channel or command changes), never the skill.

## Build

```bash
make build              # Both CLI and app (regenerates Version.swift)
make build-cli          # cli/bin/2nb only
make build-app          # macOS app
cd cli && make test     # All Go tests
cd cli && make install  # Install to /usr/local/bin/2nb
```

**Required:** CGO_ENABLED=1 and `-tags fts5` for all Go compilation (SQLite FTS5 + CGO).

Launch the macOS app via `open` on the `.app` bundle — never run the raw binary directly (it won't register with the window server):

```bash
open app/.build/arm64-apple-macosx/debug/SecondBrain.app
```

## Testing

```bash
make test               # Go unit tests
make test-battery       # Golden-path E2E battery (cli/battery_test.go)
make test-swift         # Swift unit tests (JSON decoding, parsing, wizard logic)
make test-gui           # GUI tests via AppleScript + screencapture
make test-all           # Everything
make install            # Build + install CLI to /usr/local/bin + app to ~/Applications
```

### No Mock Tests Policy

**All tests MUST use real API endpoints — local or paid. Mock tests (httptest.NewServer, fake responses, stub implementations) are NOT allowed.** Tests that need a provider call the real API and skip if credentials or services are unavailable. This applies to AI provider tests (Bedrock, OpenRouter, Ollama), MCP tests, and any future integration tests.

- **Bedrock tests**: real AWS credentials; skip if not configured
- **OpenRouter tests**: real `OPENROUTER_API_KEY`; skip if not set
- **Ollama tests**: real server at localhost:11434; skip if not running or model not pulled
- Pure logic tests (string classification, price parsing) that don't call any API are fine

### GUI Test Automation

GUI tests use **AppleScript** for app interaction and **screencapture** for verification. Run `make install` first (the app lands in `~/Applications`).

Test scripts live in `tests/`: `gui-helpers.sh` (shared), `gui-test-crud.sh`, `gui-test-navigation.sh`, `gui-test-editor.sh`, `gui-test-ui.sh`, `gui-test-vault.sh`, `gui-test-vault-switch.sh`, `gui-test-ai.sh`, `gui-test-polish.sh` (credential-gated). Screenshots land in `/tmp/sb-gui-tests/` for debugging.

## Go CLI (`cli/`)

**Module:** `github.com/apresai/2ndbrain` · **CLI:** cobra · **MCP:** mark3labs/mcp-go · **DB:** mattn/go-sqlite3 with FTS5

### Package Layout

| Package | Purpose |
|---------|---------|
| `internal/ai` | Provider interfaces, registry, Bedrock/OpenRouter/Ollama implementations |
| `internal/cli` | Cobra command definitions (one file per command) |
| `internal/vault` | Init/open, config, schemas, templates, indexer |
| `internal/document` | Markdown parsing, frontmatter, chunking, wikilinks |
| `internal/store` | SQLite CRUD, migrations, link resolution |
| `internal/search` | BM25 search engine with structured filters |
| `internal/graph` | Link graph BFS traversal |
| `internal/mcp` | MCP server with 22 tools + sidecar status files |
| `internal/git` | Read-only git wrappers (IsRepo, Activity, DiffFile, StatusFiles) |
| `internal/skills` | Skill file generation and agent registry |
| `internal/output` | JSON/CSV/YAML formatters |
| `internal/testutil` | Test helpers (NewTestVault, CreateAndIndex) |

### Key Types

- `document.Document` — Parsed markdown with frontmatter, body, metadata
- `store.DB` — SQLite connection wrapper with CRUD operations
- `vault.Vault` — Root + config + schemas + DB handle
- `search.Engine` — BM25 search over FTS5 index
- `graph.Graph` — Nodes + edges from link traversal

### CLI Commands (50 top-level)

Run `2nb --help` for the full list and `--help` on any command for flags. The complete annotated catalog lives in [CLAUDE.md](CLAUDE.md). Top-level commands by group:

| Group | Commands |
|-------|----------|
| Getting Started | `vault` (subcommands: `status`, `create`, `set`, `list`, `show`), `init` (deprecated alias for `vault create`), `completion` |
| Documents | `create` (`--path`/`--content`/`--overwrite`/`--append`), `read` (alias `print`), `meta` (`--get`/`--set` [array fields like tags/aliases coerced to a list]/`--remove`; aliases `frontmatter`/`fm`/`properties`), `delete`, `list` (alias `files`; `--total`, `--format paths\|tree`), `append`, `prepend`, `replace`, `move`, `rename` (both link-aware: rewrite `[[wikilinks]]` + `[text](path.md)`), `daily` (`path`/`read`/`append`/`prepend`), `tasks`, `task`, `tag` (`add`/`remove`, per-note frontmatter tags) |
| Search & AI | `search`, `ask`, `chat`, `index`, `suggest-links`, `polish` (`--write`), `ai` (`status`/`setup`/`local`/`embed`), `models` (`list`/`test`/`add`/`remove`/`enable`/`disable`/`enable-state`/`cost-preview`/`wizard` (`--set-active`)/`bench`/`calibrate`) |
| Quality & structure | `lint`, `stale`, `related`, `graph`, `backlinks`, `links`, `orphans`, `deadends`, `unresolved`, `outline`, `wordcount`, `folders`, `tags` (`list`/`rename`), `aliases` |
| Integration | `mcp-server`, `mcp-setup`, `mcp` (`status`/`configured`), `plugin` (`status`/`install`), `git` (`activity`/`show`/`diff`/`status`, read-only), `export-context`, `skills` (`list`/`install`/`uninstall`/`show`) |
| Import/Export | `import-obsidian`, `export-obsidian`, `migrate` |
| Configuration | `config` (`show`/`get` (`--effective`)/`set`/`set-key`/`doctor`) |

**Global flags:** `--format` (json/csv/tsv/yaml/raw/md/text; listings also `paths`/`tree`), `--porcelain`, `--json`, `--csv`, `--yaml`, `--vault`, `--verbose`/`-v`, `--copy`. Also accepts obsidian-CLI-style `key=value` args, boolean tokens (`total`/`append`/`overwrite`), and colon-commands; `file=` fuzzy-resolves by title/alias/suffix while `path=` is strict-exact. Full mapping in [docs/obsidian-cli-mapping.md](docs/obsidian-cli-mapping.md).

**Parent-command defaults:** `2nb ai` → `ai status`, `2nb models` → `models list`, `2nb git` → `git status`, `2nb mcp` → `mcp status`, `2nb plugin` → `plugin status`, `2nb skills` → `skills list`, `2nb config` → `config show`.

### MCP Server (22 tools)

| Tool | Purpose |
|------|---------|
| `kb_info` | Vault overview: name, doc types, schemas, counts, AI status |
| `kb_search` | Hybrid search with type/status/tag filters |
| `kb_ask` | RAG Q&A with source citations |
| `kb_read` | Read document or chunk by heading path |
| `kb_list` | List with filters |
| `kb_create` | Create from template type |
| `kb_update_meta` | Update frontmatter with validation |
| `kb_related` | Traverse link graph to depth N |
| `kb_structure` | Document heading hierarchy (also covers the outline view) |
| `kb_backlinks` | Resolved inbound links to a document |
| `kb_links` | Outbound links from a document, including unresolved/broken ones |
| `kb_tags` | Vault-wide tag list with per-tag document counts |
| `kb_tasks` | GFM checkbox tasks across the vault or a file/dir (`done`/`todo` filters) |
| `kb_delete` | Delete from vault and index |
| `kb_index` | Rebuild index and embeddings |
| `kb_append` | Append text to a document body; reindex + re-embed; rejects read-only `.canvas`/`.base` |
| `kb_replace_section` | Replace one heading's section content; reindex + re-embed; rejects read-only `.canvas`/`.base` |
| `kb_suggest_links` | Find semantically related docs to link from a given doc |
| `kb_polish` | AI copy-editor returns original + polished for diff |
| `kb_git_activity` | Recent git commits touching vault files (`since_days`) |
| `kb_git_diff` | Unified diff of a file vs HEAD |
| `kb_git_status` | Map of path → porcelain status for uncommitted files |

`move`/`rename` (the wikilink-rewriting vault mutation) is intentionally CLI-only: the highest-blast-radius write surface stays behind `2nb move`/`2nb rename` with their mandatory `--dry-run`. `kb_structure` already covers the outline, so there is no separate `kb_outline`.

Each running `2nb mcp-server` writes a sidecar status file to `.2ndbrain/mcp/<pid>.json` (PID, start time, last 50 tool invocations). `2nb mcp status --json` enumerates live servers.

### Testing

Tests use `t.TempDir()` for isolated vaults. Each test creates its own SQLite database.

```bash
cd cli && make test    # go test -race -tags fts5 ./...
```

## Swift macOS Dashboard (`app/`)

**Framework:** SwiftUI + AppKit, Swift 6.0, macOS 14+
**Dependencies:** GRDB.swift (SQLite), Yams (YAML), swift-markdown
**Architecture:** MVVM with `@Observable`

The app is a **configuration and companion dashboard, not an editor**: Obsidian is the editor. It reads the same `.2ndbrain/index.db` the CLI writes (WAL mode) and shells out to `2nb` for all AI / index / lint / git work. On launch it binds to the vault Obsidian currently has open (via `ObsidianRegistry`, which reads `~/Library/Application Support/obsidian/obsidian.json`).

### Dashboard Tabs

| Tab | View | Purpose |
|-----|------|---------|
| **Home** (default) | HomeView.swift | Vault card (Obsidian-match badge + plugin Install/Update row), AI card (ready dot, Save-as-default, Test), Index card (Rebuild / Re-embed All), CLI-drift banner with Update CLI button |
| Vault Status | VaultStatusView.swift | Unified health: index coverage, portability, AI reachability, stale docs |
| AI Settings | AIHubView.swift | AI Hub: providers, active models, full catalog (Cmd+Shift+,) |
| MCP Server | MCPStatusView.swift | Live MCP processes + recent tool invocations (Cmd+Shift+M) |
| Git Integration | GitActivityView.swift | Recent commits (Cmd+Shift+G); click a row for per-file diffs |
| Validation | LintResultsView.swift | Renders `2nb lint --json` findings |

### macOS SwiftUI Gotchas

- **Use AppKit dialogs for modals:** prefer `NSAlert.runModal()` / `NSOpenPanel.runModal()` over SwiftUI `.sheet()` / `beginSheetModal` when a modal needs reliable button/keyboard events.
- **Computer-use access:** The `.app` bundle must have a real binary (not symlink) and be ad-hoc codesigned (`codesign -s - --deep --force`). The Makefile handles this.
- **Yams traps, uncatchably:** `Yams.load` can `fatalError` (NOT throw) on malformed YAML such as Obsidian template placeholders (`date: {{date}}`); parse untrusted frontmatter via `Yams.compose` plus a manual `Node` walk. See `FrontmatterParser`.
- **Troubleshooting:** When hitting SwiftUI platform bugs, use Context7 and Brave Search before guessing.

### Context7 Library IDs (for real-time docs lookup)

| Library | Context7 ID |
|---------|-------------|
| SwiftUI (Apple docs) | `/websites/developer_apple_swiftui` |
| Swift language book | `/swiftlang/swift-book` |
| Swift concurrency migration | `/swiftlang/swift-migration-guide` |
| GRDB.swift (SQLite) | `/groue/grdb.swift` |

## Vault Format

### Directory Structure

```
vault-root/
├── .2ndbrain/
│   ├── config.yaml      # Vault name, embedding settings
│   ├── schemas.yaml     # Document type schemas (committable)
│   ├── index.db         # SQLite index (shared between CLI and app)
│   ├── bench.db         # Benchmark history + favorites
│   ├── mcp/             # <pid>.json per running mcp-server
│   ├── recovery/        # Crash recovery snapshots
│   └── logs/            # Error logs
├── document-1.md
└── subdirectory/document-2.md
```

Beyond `.md`, the indexer parses `.canvas` (JSON Canvas) and `.base` (YAML Bases) files as read-only synthetic views; the CLI never writes back to them.

### Document Format

Plain `.md` files with YAML frontmatter:

```yaml
---
id: <UUID>           # Stable unique identifier (survives renames)
title: Document Title
type: note           # adr | runbook | prd | prfaq | note | postmortem
status: draft        # Type-specific status values
tags: [tag1, tag2]
created: 2026-04-03T00:00:00Z
modified: 2026-04-03T00:00:00Z
---
# Document Title

Body content with [[wikilinks]] to other documents.
```

### Wikilink Syntax

- `[[target]]` — Link by title or filename
- `[[target#heading]]` — Link to specific section
- `[[target|display text]]` — Aliased link

### Document Type Schemas

Defined in `.2ndbrain/schemas.yaml`. Six built-in types:

| Type | Required Fields | Status Values |
|------|----------------|---------------|
| **adr** | title, status | proposed → accepted/deprecated → superseded |
| **runbook** | title, status | draft, active, archived |
| **prd** | title, status | draft → review → approved → shipped → archived |
| **prfaq** | title, status | draft → review → final |
| **note** | title | draft, complete |
| **postmortem** | title, status, incident-date | draft, reviewed, published |

### SQLite Schema (index.db)

Tables: `documents`, `chunks`, `chunks_fts` (FTS5), `links`, `tags`, `aliases`, `schema_version`. Schema v3 adds the `aliases` table and a `block_id` column on `chunks` and `links` for Obsidian block references (`^block-id`).

## Obsidian Conversion

### Import (`2nb import-obsidian`)

- Generates UUID `id` for documents missing one
- Sets `type: note` for documents without a type
- Normalizes inline `#tag` syntax to frontmatter `tags` array
- Preserves all existing frontmatter fields
- Maps Obsidian `aliases` to wikilink resolution
- Preserves `.canvas` files as-is
- Initializes `.2ndbrain/` and builds full index

### Export (`2nb export-obsidian`)

- Copies markdown files to target directory
- Creates `.obsidian/` with default config
- Converts UUID-based references to filename-based wikilinks
- Optionally strips `id` and `type` fields (`--strip-ids`)

`2nb migrate` upgrades a legacy 2ndbrain vault to the Obsidian-native format (schema v3); `--dry-run` previews without modifying. Source markdown is never changed.

## MCP Integration

For Claude Code, add to `~/.claude.json`:

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

Run `2nb mcp-setup` for config snippets for other tools (Cursor, Claude Desktop, Gemini CLI, Amazon Q, Kiro).
