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

## Release

Both the CLI and macOS editor are published via Homebrew:

```bash
brew install apresai/tap/2nb                    # CLI only
brew install --cask apresai/tap/secondbrain     # macOS editor (installs CLI as dependency)
```

### Release Pipeline

1. `make bump-build` (or `bump-minor` / `bump-major`) — increment `VERSION`, regenerate `Version.swift`
2. `make release` — updates `CHANGELOG.md`, commits, creates git tag `v<VERSION>`, pushes tag
3. GitHub Actions (`.github/workflows/release.yml`) triggers on the tag push:
   - Runs on `macos-latest` (arm64) with CGO_ENABLED=1 and Swift toolchain
   - GoReleaser (`.goreleaser.yaml`) builds CLI for macOS arm64 + x86_64, creates GitHub Release, pushes formula `twonb.rb` to `apresai/homebrew-tap`
   - Builds SecondBrain.app (`swift build -c release`), assembles `.app` bundle, ad-hoc codesigns
   - Packages as `SecondBrain-<VERSION>-arm64.zip` via `ditto`, uploads to GitHub Release
   - Pushes cask `Casks/secondbrain.rb` to `apresai/homebrew-tap` (generated from `casks/secondbrain.rb.tmpl`)
4. `make release-local` — same flow but runs `goreleaser release --clean` locally (CLI only, no app build)

### Key Files

| File | Purpose |
|------|---------|
| `.goreleaser.yaml` | GoReleaser config: CLI builds, archives, changelog, Homebrew formula |
| `.github/workflows/release.yml` | CI workflow triggered on `v*` tags (CLI + app) |
| `casks/secondbrain.rb.tmpl` | Homebrew cask template with `CASK_VERSION`/`CASK_SHA256` tokens |
| `scripts/update-changelog.sh` | Generates CHANGELOG.md entries for a version |
| `CHANGELOG.md` | Auto-generated release history |

### Secrets

The `apresai` GitHub environment provides `HOMEBREW_TAP_TOKEN` — a PAT with write access to `apresai/homebrew-tap`.

### App Distribution Notes

The macOS editor is distributed as an arm64 ad-hoc signed `.app` bundle. It is **not** notarized with Apple Developer ID, so users must right-click > Open on first launch to bypass Gatekeeper. The cask declares `depends_on formula: "apresai/tap/twonb"` because the app shells out to `2nb` for AI, indexing, and lint features.

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
make test-swift         # Swift unit tests (app/) — JSON decoding, parsing, wizard logic
make test-gui           # GUI tests via AppleScript + screencapture
make test-all           # Everything: Go + Swift + GUI
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

- **NSAlert dialogs** (New Note): Type in text field, navigate popup via accessibility, press Return
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
| `internal/mcp` | MCP server with 16 tools + sidecar status files |
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

### CLI Commands (47)

Commands are organized into groups (Getting Started, Documents, Search & AI, Quality, Integration, Import/Export, Configuration).

| Command | Flags | Purpose |
|---------|-------|---------|
| `init` | `--path` | Initialize a new vault |
| `vault` | | Show or set the active vault |
| `create` | `--type`, `--title` | Create document from template (adr/runbook/note/postmortem) |
| `read` | `--chunk` | Read full document or specific section |
| `meta` | `--set key=value` | View or update frontmatter with schema validation |
| `index` | `--doc <path>`, `--force-reembed` | Rebuild vault search index (or a single document); `--force-reembed` invalidates every stored embedding so the next pass re-embeds from scratch (use after intentionally switching providers) |
| `search` | `--type`, `--status`, `--tag`, `--limit`, `--threshold`, `--bm25-only` | Hybrid BM25 + semantic search with configurable cosine cutoff |
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
| `mcp-setup` | | Show MCP setup instructions for all AI tools |
| `mcp status` | `--json` | List live MCP server processes and recent tool invocations |
| `suggest-links` | `<path>`, `--limit` | Suggest semantically related documents to link from a document |
| `polish` | `<path>`, `--system`, `--max-tokens` | AI copy-edit a document (returns original + polished for diff preview) |
| `git activity` | `--since 7d`, `--json` | Recent commits that touched vault files (read-only git) |
| `git show` | `<hash>`, `--json` | Full commit detail: metadata, stats, per-file unified diffs |
| `git diff` | `<path>`, `--json` | Unified diff of a file against HEAD |
| `git status` | `--json` | List uncommitted/untracked files in the vault |
| `ask` | `<question>` | RAG Q&A — search vault, generate answer with sources |
| `ai status` | | Show AI provider, models, readiness, embedding count, and vault portability state (dimension mismatch, model mismatch, provider unavailable, etc.) with one-line fix hints |
| `ai embed` | `<text>` | Generate embedding vector (debug/testing) |
| `ai setup` | `--provider`, `--embedding-model`, `--generation-model` | Multi-provider setup wizard with easy mode |
| `ai local` | | Check local AI readiness (Ollama, models, disk, RAM, embeddings) |
| `models list` | `--type`, `--free`, `--discover`, `--status`, `--provider` | List verified model catalog, optionally discover vendor models |
| `models test` | `<model-id>`, `--provider`, `--type` | Smoke-test a model (embed or generate probe) |
| `models bench` | `--model`, `--probe`, `--provider` | Benchmark models against vault with persistent history |
| `models bench fav` | `<model-id>` | Add model to benchmark favorites |
| `models bench unfav` | `<model-id>` | Remove model from benchmark favorites |
| `models bench favs` | | List benchmark favorites |
| `models bench history` | `--limit` | Show past benchmark runs |
| `models bench compare` | | Side-by-side latency comparison of favorited models |
| `skills list` | `--user` | List supported AI agents and install status |
| `skills install` | `<agent>`, `--all`, `--force`, `--user` | Install skill file for an AI coding agent |
| `skills uninstall` | `<agent>`, `--all`, `--user` | Remove skill file for an AI coding agent |
| `skills show` | `<agent>` | Preview skill content for an agent |
| `config show` | | Dump full vault configuration including vault_root, vault_dir, vault_name |
| `config get` | `<key>` | Read a config value (e.g. `ai.provider`, `ai.similarity_threshold`) |
| `config set` | `<key> <value>` | Write a config value |
| `config set-key` | `<provider>` | Store API key in macOS Keychain |

**Global flags:** `--format` (json/csv/yaml), `--porcelain`, `--json`, `--csv`, `--yaml`, `--vault`, `--verbose` / `-v`

**Parent-command defaults:** running a group without a subcommand invokes the most-useful read-only action instead of printing help. `2nb ai` → `ai status`, `2nb models` → `models list`, `2nb git` → `git status`, `2nb mcp` → `mcp status`, `2nb skills` → `skills list`, `2nb config` → `config show`. `--help` still works on every command because Cobra intercepts it before `RunE`.

**Similarity threshold:** hybrid search drops vector hits whose cosine similarity is below `ai.similarity_threshold` (default `0.20`, configurable via `2nb config set`). Pass `--threshold <float>` on `2nb search` for one-off overrides. Results display `(rrf=X.XXX, cos=Y.YYY)` so you can judge semantic relevance directly — the RRF score alone is opaque.

**Logging:** When `--verbose` is used, structured logs (via `log/slog`) are written to stderr and to `.2ndbrain/logs/cli.log`. Without `--verbose`, only the log file is written.

### Vault Portability

A vault is self-contained: markdown files plus `.2ndbrain/index.db` plus `.2ndbrain/config.yaml`. You can `tar czf` it and open it on another machine with no migration. Paths in the DB are relative to the vault root (`internal/vault/indexer.go`), IDs are UUIDs from frontmatter, and embeddings are self-contained `[]float32` BLOBs — nothing in the index refers to an absolute path or a host-specific resource.

**Source of truth:** the DB's `documents.embedding_model` column and the BLOB length itself record what produced the stored embeddings. Config is user preference only — nothing in `2nb index` writes derived state back to `config.yaml`, so team vaults shared via git don't produce merge conflicts.

**Decision table (rendered live by `2nb ai status`):**

| DB state | Current config/env | Outcome | Fix |
|---|---|---|---|
| embeddings match dim D and model M, provider available | — | **OK** | none |
| dim D in DB, current provider produces D' ≠ D | — | **DIMENSION BREAK** | `2nb index --force-reembed` or switch provider back |
| model M in DB, M' ≠ M in config, same dim | — | **MODEL MISMATCH** | next `2nb index` auto re-embeds on content change, or `--force-reembed` now |
| provider configured but `Available()=false` | — | **PROVIDER UNAVAILABLE** | start/install provider; BM25 runs meanwhile |
| mixed models in DB | — | **MIXED** | `2nb index --force-reembed` |
| zero embeddings, docs present | — | **UNINDEXED** | `2nb index` (BM25 still works) |
| vault `schema_version > max` | — | **DB TOO NEW** | `brew upgrade apresai/tap/twonb` |
| `config.yaml` missing/corrupt | — | **self-heals** | regenerated with defaults, `.bak` preserved on corrupt |

**Loud degradation:** `2nb search` and `2nb ask` call `VectorCompat` (`cli/internal/cli/helpers_vector.go`) at the hybrid gate. If the vault's embeddings aren't usable by the current provider, they print one stderr line explaining why, collect the message into a `warnings` slice, and force BM25-only for the rest of the call. The Swift macOS app sees the same messages via the `--json` envelope and shows a yellow banner over search results / Ask AI answers; the status-bar AI dot turns yellow on any non-OK portability state.

**Shipping a vault:** the recommended tarball excludes personal/local state that a receiver shouldn't need (or shouldn't see):

```bash
tar czf vault.tar.gz \
  --exclude='.2ndbrain/logs' \
  --exclude='.2ndbrain/recovery' \
  --exclude='.2ndbrain/mcp' \
  --exclude='.2ndbrain/bench.db' \
  my-vault/
```

`.2ndbrain/config.yaml` and `.2ndbrain/index.db` *should* be in the tarball for single-user sharing — config keeps the receiver's first `ai status` meaningful, and the DB carries the embeddings (without which every semantic search would re-embed from scratch). For git-shared team vaults, `2nb init` writes a `.gitignore` that excludes `config.yaml`, `index.db` (+ WAL files), `bench.db`, `logs/`, `recovery/`, `mcp/`, and `*.bak` — only `schemas.yaml` (shared doc-type definitions) is committable.

**Privacy caveat:** embeddings are a lossy representation of the source text. Shipping a vault with embeddings is functionally equivalent to shipping the (approximate) content — useful for trusted sharing, not for publishing to strangers. A `--strip-embeddings` export mode is future work.

**JSON envelope (breaking change from 0.1.12):** `2nb search --json` and `2nb ask --json` now return `{mode, warnings, results}` / `{mode, warnings, answer, sources}` envelopes. Programmatic consumers that previously decoded a raw array/object need to extract `.results` / `.answer`. The macOS app decodes the envelope as `CLISearchResponse` / `CLIAskResponse` in `AppState.swift`.

### MCP Server (16 tools)

Each `2nb mcp-server` process writes a sidecar status file to `.2ndbrain/mcp/<pid>.json` with PID, start time, parent PID, and the last 50 tool invocations (tool name, timestamp, duration, ok/error). The editor polls `2nb mcp status --json` every 5s to populate the status bar indicator and Cmd+Shift+M panel. mark3labs/mcp-go has no client-connected hook, so sidecar files are the only way to enumerate live servers.

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
| `kb_suggest_links` | Find semantically related documents to link from a given document |
| `kb_polish` | AI copy-editor returns original + polished body for diff review |
| `kb_git_activity` | Recent git commits that touched vault files (read-only) |
| `kb_git_diff` | Unified diff of a file against HEAD |
| `kb_git_status` | Map of path → git porcelain status for uncommitted files |

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
| Document editing | EditorArea.swift | NSTextView with configurable font, debounced sync, read-only mode for >100 MB files |
| Editor mode toggle | EditorArea.swift | Source / Split / Preview segmented control in toolbar |
| Live preview | EditorArea.swift | Read-only HTML preview via WKWebView (split or full-width). Removed in 0.1.15: contenteditable + Turndown.js round-trip corrupted markdown with Mermaid SVGs and WebKit artifacts |
| Document templates | AppState.swift | Create from ADR, Runbook, Note, Postmortem, PRD, PR/FAQ templates |
| Document duplication | SidebarView.swift | Context menu Duplicate with fresh UUID and "(copy)" title |
| Drag to open | ContentView.swift | Drop `.md` files from Finder to open in new tabs |
| Document deletion | SidebarView.swift | Context menu delete with confirmation |
| 30s autosave | AppState.swift + PreferencesView.swift | Toggle "Enable autosave" + 15/30/60s interval picker in Preferences |
| Low disk warning | AppState.swift | NSAlert when volume available capacity < 50 MB before save |
| Filename collision suffix | AppState.swift | `uniqueFilename` appends -1, -2, ... on create/duplicate |
| Pre-write crash snapshot | CrashJournal.swift | Sync `.recovery.md` snapshot before every write |
| Merge conflict dialog | MergeConflictView.swift | NSHostingController window with two DiffView panes when FSEvents detects external edit to a dirty tab |
| Parse-on-open recovery | AppState.swift | Shows existing recovery dialog when FrontmatterParser.loadDocument throws |
| Frontmatter editing | PropertiesView.swift | Editable properties with type-appropriate controls |
| Wikilink autocomplete | MentionAutocompleteController.swift | `@` and `[[` triggered popover with document search |
| Search panel | SearchPanelView.swift | Vault-wide search with type filters (Cmd+Shift+F) |
| Quick open | QuickOpenView.swift | Fuzzy filename search (Cmd+P) |
| Command palette | CommandPaletteView.swift | All commands with fuzzy search (Cmd+Shift+P) |
| Graph view | GraphView.swift + Graph/ | Obsidian-style force-directed graph with canvas renderer, inspector panel (mode, filters, forces, color groups), global/local modes, zoom/pan/drag, hover + selection highlighting. Simulation uses Barnes-Hut quadtree for O(n log n) repulsion, scales past 1K nodes |
| Backlinks panel | BacklinksView.swift | Documents linking to current document |
| Outline panel | SidebarView.swift | Document heading hierarchy |
| Properties panel | PropertiesView.swift | Editable frontmatter fields (Cmd+Option+I) |
| Tab system | TabBarView.swift | Multiple documents with dirty indicators |
| Focus mode | ContentView.swift | Distraction-free editing (Cmd+Shift+E); mouse top edge reveals tab bar + breadcrumb briefly |
| Status bar | StatusBarView.swift | Doc type, status, word count, chunk count, token estimate, AI dot, MCP dot |
| AI status popover | StatusBarView.swift | Clickable AI dot → provider/models/staleness/rebuild |
| MCP status panel | MCPStatusView.swift | Cmd+Shift+M → per-client list with recent tool invocations |
| MCP status indicator | StatusBarView.swift | Green dot + client count, polls `2nb mcp status --json` every 5s |
| Index rebuild | IndexProgressView.swift | Confirmation dialog → progress bars → stats summary |
| Lint validation | LintResultsView.swift | Shell out to `2nb lint --json`, clickable issues |
| Skills install | SkillsInstallView.swift | Install SKILL.md for 8 AI agents (AI menu) |
| MCP setup | MCPSetupView.swift | Show MCP config snippets for 6 AI tools (AI menu) |
| Vault Status panel | VaultStatusView.swift | Unified health panel (Vault menu > Vault Status…) — vault info, index state, embedding portability, stale docs, provider reachability. Rebuild Index + Re-embed All buttons |
| AI Test Connection | AITestView.swift | Standalone model probe (AI menu > Test AI Connection…) — shells out to `2nb models test` for embed + gen models, shows latency + ok/fail, offers Open AI Setup on failure |
| Window toolbar | ContentView.swift | New Note / Search / Quick Open buttons visible whenever a vault is open (before any doc is selected) |
| File→Notes menu rename | AppDelegate.swift | AppKit hook renames the File menu to "Notes"; observer reapplies on NSMenu.didBeginTrackingNotification + applicationDidBecomeActive |
| AI setup wizard | AISetupWizardView.swift | 4-step provider/credentials/models/test wizard |
| Obsidian import | SecondBrainApp.swift | Import via CLI with folder picker |
| Obsidian export | SecondBrainApp.swift | Export via CLI with folder picker |
| Spotlight indexing | SpotlightIndexer | CoreSpotlight integration |
| Crash recovery | CrashJournal | Recovery dialog on launch |
| File watching | FSEventsWatcher | Auto-reload on external changes |
| Ask AI panel | AskAIView.swift | RAG Q&A overlay via `2nb ask` (Cmd+Shift+A) |
| AI status indicator | StatusBarView.swift | Provider readiness + embedding progress in status bar |
| Semantic search | SearchPanelView.swift | Toggle for AI-powered hybrid search |
| Find Similar | SidebarView.swift | Context menu → semantic search for similar docs |
| Tag drill-down | TagBrowserView.swift | Click tag → filtered file list → back button |
| Preferences | PreferencesView.swift | Font family/size picker + autosave interval (Cmd+,) |
| PDF/HTML/MD export | ExportController.swift | Export menu with PDF (WKWebView), HTML, Markdown |
| Suggest Links | SuggestLinksView.swift | Cmd+Shift+L → `2nb suggest-links` → click-to-insert wikilinks |
| AI Polish | PolishView.swift | Cmd+Option+P → `2nb polish` → DiffView with Accept/Open-as-new-tab/Reject |
| Diff view | DiffView.swift | Reusable Myers LCS unified diff (polish, merge conflict, git diff) |
| Git sidebar indicators | SidebarView.swift | Orange dot for modified, blue for untracked (when vault is a git repo) |
| Git activity | GitActivityView.swift | Cmd+Shift+G → recent commits with 1/3/7/30 day window |
| Commit detail | CommitDetailView.swift | Click commit row → split pane: file list + per-file unified diff |
| Git diff viewer | GitDiffView.swift | Right-click → Show Changes vs HEAD → raw unified diff |

### Keyboard Shortcuts

| Shortcut | Action |
|----------|--------|
| Cmd+N | New Note |
| Cmd+S | Save |
| Cmd+Shift+O | Open Vault |
| Cmd+P | Quick Open |
| Cmd+Shift+P | Command Palette |
| Cmd+Shift+A | Ask AI |
| Cmd+Shift+L | Suggest Links |
| Cmd+Option+P | Polish Document |
| Cmd+Shift+M | MCP Server Status |
| Cmd+Shift+G | Recent Git Activity |
| Cmd+Option+G | Graph View |
| Cmd+Shift+F | Search Panel |
| Cmd+Shift+E | Focus Mode |
| Cmd+Option+I | Properties Panel |
| Cmd+\\ | Toggle Sidebar |
| Cmd+, | Preferences |
| Cmd+= | Increase Font Size |
| Cmd+- | Decrease Font Size |
| Cmd+0 | Reset Font Size |
| Cmd+Shift+X | Export as PDF |

## Vault Format

### Directory Structure

```
vault-root/
├── .2ndbrain/
│   ├── config.yaml      # Vault name, embedding settings
│   ├── schemas.yaml     # Document type schemas
│   ├── index.db         # SQLite index (shared between CLI and editor)
│   ├── bench.db         # Benchmark history and favorites (created on first bench)
│   ├── mcp/             # One <pid>.json per running mcp-server process
│   ├── models/          # Embedding model files
│   ├── recovery/        # Crash recovery snapshots (pre-write + failure snapshots)
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
