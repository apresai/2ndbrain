# 2ndbrain

AI-native markdown knowledge base with a Go CLI, MCP server, and native macOS editor.

## Repository Layout

- `cli/` — Go CLI binary (`2nb`) + MCP server
- `app/` — Swift macOS editor (SwiftUI + AppKit)
- `reqs.md` — EARS-format requirements specification
- `press-release.md` — Product vision document
- `test-plan.md` — Requirements validation test plan

## Versioning

Format: `major.minor.build` (e.g., `0.1.0`). Single source of truth: `VERSION` file at repo root.

| Component | How it reads the version |
|-----------|------------------------|
| Go CLI | `cli/Makefile` reads `../VERSION` via LDFLAGS into `internal/cli.Version` |
| Swift app | `app/Sources/SecondBrain/Version.swift` (generated, exposes `appVersion`) |

Bump targets (root `Makefile`):

| Target | Effect | Example |
|--------|--------|---------|
| `make bump-build` | Increment build | `0.1.0` → `0.1.1` |
| `make bump-minor` | Increment minor, reset build | `0.1.1` → `0.2.0` |
| `make bump-major` | Increment major, reset minor+build | `0.2.0` → `1.0.0` |

Each bump target also regenerates `Version.swift`. Never edit `Version.swift` by hand.

## Build

```bash
make build              # Builds both CLI and app (generates Version.swift first)
make build-cli          # Builds cli/bin/2nb only
make build-app          # Generates Version.swift + builds macOS editor
cd cli && make build    # Builds cli/bin/2nb
cd cli && make test     # Runs all Go tests
cd cli && make install  # Installs to /usr/local/bin/2nb
```

### Running the macOS Editor

Launch via `open` on the `.app` bundle — do **not** run the raw binary directly (it won't register with the window server and no window will appear):

```bash
open app/.build/arm64-apple-macosx/debug/SecondBrain.app
```

**Required:** CGO_ENABLED=1 and `-tags fts5` for all Go compilation (SQLite FTS5 + CGO).

## Testing

```bash
make test               # Go unit tests (cli/)
make test-gui           # GUI tests via AppleScript + screencapture
make test-all           # Everything: Go + GUI
make install            # Build + install CLI to /usr/local/bin + app to /Applications
```

### No Mock Tests Policy

**All tests MUST use real API endpoints — local or paid. Mock tests (httptest.NewServer, fake responses, stub implementations) are NOT allowed.** Tests that need a provider should call the real API and skip if credentials or services are unavailable. This applies to AI provider tests (Bedrock, OpenRouter, Ollama), MCP tests, and any future integration tests. The test suite must verify actual behavior against real services, not simulated responses.

- **Bedrock tests**: Use real AWS credentials; skip if not configured
- **OpenRouter tests**: Use real OPENROUTER_API_KEY; skip if not set
- **Ollama tests**: Use real Ollama server at localhost:11434; skip if not running or model not pulled
- Pure logic tests (e.g., string classification, price parsing) that don't call any API are fine

### GUI Test Automation

GUI tests use **AppleScript** for app interaction and **screencapture** for verification. Run `make install` first to install to `/Applications` — computer-use MCP requires apps in `/Applications` to grant access.

- **Computer-use**: Use for interactive testing and visual verification after `make install`
- **AppleScript**: Automated test scripts use this for headless GUI control
- **File verification**: Tests check disk state (file exists, frontmatter correct) after GUI actions

Test scripts live in `tests/`:

| Script | What it tests |
|--------|--------------|
| `gui-helpers.sh` | Shared functions (screenshot, pass/fail, launch/kill) |
| `gui-test-crud.sh` | Create notes (Note, ADR), delete, vault reopen |
| `gui-test-navigation.sh` | Quick Open, Command Palette, Search Panel |
| `gui-test-editor.sh` | Undo/redo, bold/italic, save, preview |
| `gui-test-ui.sh` | Sidebar, focus mode, tabs, graph view |
| `gui-test-vault.sh` | FSEvents, Obsidian import/export |
| `gui-test-ai.sh` | Ask AI panel, status bar indicator, semantic search |

### Key patterns for GUI tests

- **NSAlert dialogs** (New Document): Type in text field, navigate popup via accessibility, press Return
- **SwiftUI overlays** (Quick Open, Search, Command Palette): Rely on menu shortcuts (not `.onKeyPress`) since NSTextView steals focus. `makeFirstResponder(nil)` + `@FocusState` ensures overlay TextFields get focus.
- **Sidebar clicks**: Use AppleScript `click at {x, y}` coordinates
- **Screenshots**: Saved to `/tmp/sb-gui-tests/` for debugging failures

## Go CLI (`cli/`)

**Module:** `github.com/apresai/2ndbrain`
**Framework:** cobra for CLI, mark3labs/mcp-go for MCP server
**Database:** mattn/go-sqlite3 with FTS5 for BM25 search

### Package Layout

| Package | Purpose |
|---------|---------|
| `internal/ai` | AI provider interfaces, registry, Bedrock/OpenRouter/Ollama implementations |
| `internal/cli` | Cobra command definitions (one file per command) |
| `internal/vault` | Vault init/open, config, schemas, templates, indexer |
| `internal/document` | Markdown parsing, frontmatter, chunking, wikilinks |
| `internal/store` | SQLite database CRUD, migrations, link resolution |
| `internal/search` | BM25 search engine with structured filters |
| `internal/graph` | Link graph BFS traversal |
| `internal/mcp` | MCP server with 9 tools |
| `internal/output` | JSON/CSV/YAML formatters |
| `internal/testutil` | Test helpers (NewTestVault, CreateAndIndex) |

### Key Types

- `document.Document` — Parsed markdown with frontmatter, body, metadata
- `store.DB` — SQLite connection wrapper with CRUD operations
- `vault.Vault` — Root + config + schemas + DB handle
- `search.Engine` — BM25 search over FTS5 index
- `graph.Graph` — Nodes + edges from link traversal

### CLI Commands (33)

| Command | Flags | Purpose |
|---------|-------|---------|
| `init` | `--path` | Initialize a new vault |
| `create` | `--type`, `--title` | Create document from template (adr/runbook/note/postmortem) |
| `read` | `--chunk` | Read full document or specific section |
| `meta` | `--set key=value` | View or update frontmatter with schema validation |
| `index` | | Rebuild vault search index |
| `search` | `--type`, `--status`, `--tag`, `--limit` | Hybrid BM25 search with filters |
| `list` | `--type`, `--status`, `--tag`, `--limit`, `--sort` | List documents with filters |
| `lint` | `[glob]` | Validate schemas, check broken wikilinks |
| `stale` | `--since` | List documents not modified within N days |
| `related` | `--depth` | Find related docs via link graph traversal |
| `graph` | | Output link graph as JSON adjacency list |
| `export-context` | `--types`, `--status`, `--limit` | Generate CLAUDE.md-compatible context bundle |
| `delete` | `--force` | Delete document from disk and index |
| `import-obsidian` | `--target` | Import Obsidian vault (adds UUIDs, normalizes tags, builds index) |
| `export-obsidian` | `--strip-ids` | Export vault to Obsidian format |
| `mcp-server` | | Start MCP server on stdio transport |
| `ask` | `<question>` | RAG Q&A — search vault, generate answer with sources |
| `ai status` | | Show AI provider, models, readiness, embedding count |
| `ai embed` | `<text>` | Generate embedding vector (debug/testing) |
| `ai setup` | | Guided local AI setup with Ollama (install, pull models, configure) |
| `ai local` | | Check local AI readiness (Ollama, models, disk, RAM, embeddings) |
| `models list` | `--type`, `--free`, `--discover`, `--status`, `--provider` | List verified model catalog, optionally discover vendor models |
| `models test` | `<model-id>`, `--provider`, `--type` | Smoke-test a model (embed or generate probe) |
| `models bench` | `--model`, `--probe`, `--provider` | Benchmark models against vault with persistent history |
| `models bench fav` | `<model-id>` | Add model to benchmark favorites |
| `models bench unfav` | `<model-id>` | Remove model from benchmark favorites |
| `models bench favs` | | List benchmark favorites |
| `models bench history` | `--limit` | Show past benchmark runs |
| `models bench compare` | | Side-by-side latency comparison of favorited models |
| `config show` | | Dump full vault configuration |
| `config get` | `<key>` | Read a config value |
| `config set` | `<key> <value>` | Write a config value |
| `config set-key` | `<provider>` | Store API key in macOS Keychain |

**Global flags:** `--format` (json/csv/yaml), `--porcelain`, `--json`, `--csv`, `--yaml`, `--vault`

### MCP Server (11 tools)

| Tool | Purpose |
|------|---------|
| `kb_info` | Vault overview: name, doc types, schemas, counts, AI status |
| `kb_search` | Hybrid search with type/status/tag filters |
| `kb_ask` | RAG Q&A — answer questions with source citations |
| `kb_read` | Read document or specific chunk by heading path |
| `kb_list` | List documents with filters |
| `kb_create` | Create document from template type |
| `kb_update_meta` | Update frontmatter fields with validation |
| `kb_related` | Traverse link graph to depth N |
| `kb_structure` | Get document heading hierarchy |
| `kb_delete` | Delete document from vault and index |
| `kb_index` | Rebuild search index and generate embeddings |

### Testing

Tests use `t.TempDir()` for isolated vaults. Each test creates its own SQLite database.

```bash
cd cli && make test    # go test -race -tags fts5 ./...
```

## Swift macOS Editor (`app/`)

**Framework:** SwiftUI + AppKit, Swift 6.0, macOS 14+
**Dependencies:** GRDB.swift (SQLite), Yams (YAML), swift-markdown (parsing)
**Architecture:** MVVM with @Observable, NSTextView for editor

### macOS SwiftUI Gotchas

- **Sheets are broken with NSViewRepresentable**: SwiftUI `.sheet()` modals don't receive button/keyboard events when the parent view contains an NSViewRepresentable (like our NSTextView editor). Use AppKit dialogs (`NSAlert.runModal()` or `NSOpenPanel.runModal()`) instead — never `beginSheetModal` which has the same issue.
- **Computer-use access**: The `.app` bundle must have a real binary (not symlink) and be ad-hoc codesigned (`codesign -s - --deep --force`). The Makefile handles this automatically.
- **Troubleshooting**: When hitting SwiftUI platform bugs, use Context7 and Brave Search to find current guidance before guessing at fixes.

### Context7 Library IDs (for real-time docs lookup)

| Library | Context7 ID |
|---------|-------------|
| SwiftUI (Apple docs) | `/websites/developer_apple_swiftui` |
| Swift language book | `/swiftlang/swift-book` |
| Swift concurrency migration | `/swiftlang/swift-migration-guide` |
| GRDB.swift (SQLite) | `/groue/grdb.swift` |

The Swift app reads the same `.2ndbrain/index.db` that the Go CLI writes to (WAL mode for concurrent access).

### GUI Features

| Feature | File | Description |
|---------|------|-------------|
| Vault creation | VaultManager.swift | Create new vault with .2ndbrain directory |
| Vault opening | SecondBrainApp.swift | Open existing vault via folder picker (Cmd+Shift+O) |
| Document editing | EditorArea.swift | NSTextView with monospace font, debounced sync |
| Live preview | EditorArea.swift | Side-by-side HTML preview via WKWebView |
| Document templates | AppState.swift | Create from ADR, Runbook, Note, Postmortem templates |
| Document deletion | SidebarView.swift | Context menu delete with confirmation |
| Frontmatter editing | PropertiesView.swift | Editable properties with type-appropriate controls |
| Wikilink autocomplete | MentionAutocompleteController.swift | `@` and `[[` triggered popover with document search |
| Search panel | SearchPanelView.swift | Vault-wide search with type filters (Cmd+Shift+F) |
| Quick open | QuickOpenView.swift | Fuzzy filename search (Cmd+P) |
| Command palette | CommandPaletteView.swift | All commands with fuzzy search (Cmd+Shift+P) |
| Graph view | GraphView.swift | Interactive force-directed link graph |
| Backlinks panel | BacklinksView.swift | Documents linking to current document |
| Outline panel | SidebarView.swift | Document heading hierarchy |
| Properties panel | PropertiesView.swift | Editable frontmatter fields (Cmd+Option+I) |
| Tab system | TabBarView.swift | Multiple documents with dirty indicators |
| Focus mode | ContentView.swift | Distraction-free editing (Cmd+Shift+E) |
| Status bar | StatusBarView.swift | Doc type, status, word count |
| Index rebuild | AppState.swift | Shell out to `2nb index` |
| Lint validation | LintResultsView.swift | Shell out to `2nb lint --json` |
| Obsidian import | SecondBrainApp.swift | Import via CLI with folder picker |
| Obsidian export | SecondBrainApp.swift | Export via CLI with folder picker |
| Spotlight indexing | SpotlightIndexer | CoreSpotlight integration |
| Crash recovery | CrashJournal | Recovery dialog on launch |
| File watching | FSEventsWatcher | Auto-reload on external changes |
| Ask AI panel | AskAIView.swift | RAG Q&A overlay via `2nb ask` (Cmd+Shift+A) |
| AI status indicator | StatusBarView.swift | Provider readiness + embedding progress in status bar |
| Semantic search | SearchPanelView.swift | Toggle for AI-powered hybrid search |
| Find Similar | SidebarView.swift | Context menu → semantic search for similar docs |

### Keyboard Shortcuts

| Shortcut | Action |
|----------|--------|
| Cmd+N | New Document |
| Cmd+S | Save |
| Cmd+Shift+O | Open Vault |
| Cmd+P | Quick Open |
| Cmd+Shift+P | Command Palette |
| Cmd+Shift+A | Ask AI |
| Cmd+Shift+F | Search Panel |
| Cmd+Shift+E | Focus Mode |
| Cmd+Option+I | Properties Panel |
| Cmd+\\ | Toggle Sidebar |

## Vault Format

### Directory Structure

```
vault-root/
├── .2ndbrain/
│   ├── config.yaml      # Vault name, embedding settings
│   ├── schemas.yaml     # Document type schemas
│   ├── index.db         # SQLite index (shared between CLI and editor)
│   ├── bench.db         # Benchmark history and favorites (created on first bench)
│   ├── models/          # Embedding model files
│   ├── recovery/        # Crash recovery snapshots
│   └── logs/            # Error logs
├── document-1.md
├── document-2.md
└── subdirectory/
    └── document-3.md
```

### Document Format

Plain `.md` files with YAML frontmatter:

```yaml
---
id: <UUID>           # Stable unique identifier (survives renames)
title: Document Title
type: note           # adr | runbook | note | postmortem
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

Defined in `.2ndbrain/schemas.yaml`. Four built-in types:

| Type | Required Fields | Status Values | Status Machine |
|------|----------------|---------------|----------------|
| **adr** | title, status | proposed, accepted, deprecated, superseded | proposed → accepted/deprecated; accepted → deprecated/superseded |
| **runbook** | title, status | draft, active, archived | — |
| **prd** | title, status | draft, review, approved, shipped, archived | draft → review → approved → shipped → archived; review/approved can return to draft |
| **prfaq** | title, status | draft, review, final | draft → review → final; review can return to draft |
| **note** | title | draft, complete | — |
| **postmortem** | title, status, incident-date | draft, reviewed, published | — |

### SQLite Schema (index.db)

Tables: `documents`, `chunks`, `chunks_fts` (FTS5), `links`, `tags`, `schema_version`

### SQLite Schema (bench.db)

Created on first `2nb models bench` invocation. Stores benchmark run history and model favorites.

Tables: `favorites` (provider, model_id, model_type, added_at), `runs` (timestamp, provider, model_id, probe, latency_ms, ok, detail, vault_doc_count), `schema_version`

Favorites track which models the user wants to benchmark regularly. Runs capture individual probe results with vault context (doc count at time of run) for historical comparison.

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
