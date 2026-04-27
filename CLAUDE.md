# 2ndbrain

AI-native markdown knowledge base with a Go CLI, MCP server, and native macOS editor.

## Repository Layout

- `cli/` — Go CLI binary (`2nb`) + MCP server
- `app/` — Swift macOS editor (SwiftUI + AppKit)
- `reqs.md` — EARS-format requirements specification
- `press-release.md` — Product vision document
- `test-plan.md` — Requirements validation test plan

### Project docs (`docs/`)

- [`agent-teaching.md`](docs/agent-teaching.md) — MCP vs CLI decision matrix + test battery design
- [`mcp-integration.md`](docs/mcp-integration.md) — MCP setup snippets for Claude Code, Cursor, and other clients
- [`templates.md`](docs/templates.md) — Built-in document type templates (adr, runbook, prd, prfaq, note, postmortem)
- [`vault-structure.md`](docs/vault-structure.md) — On-disk vault layout reference

## Versioning

Format: `major.minor.build`. Single source of truth: `VERSION` file at repo root. The Go CLI reads it via `cli/Makefile` LDFLAGS into `internal/cli.Version`; the Swift app generates `app/Sources/SecondBrain/Version.swift` (never edit by hand).

Bump targets (root `Makefile`): `make bump-build` (`0.1.0` → `0.1.1`), `make bump-minor` (`0.1.1` → `0.2.0`), `make bump-major` (`0.2.0` → `1.0.0`). Each regenerates `Version.swift`.

## Release

Both CLI and macOS editor are published via Homebrew:

```bash
brew install apresai/tap/2nb                    # CLI only
brew install --cask apresai/tap/secondbrain     # macOS editor (depends on CLI)
```

### Pipeline

1. `make bump-build` (or `bump-minor`/`bump-major`) — increment `VERSION`, regenerate `Version.swift`.
2. `make release` — updates `CHANGELOG.md`, commits, tags `v<VERSION>`, pushes tag.
3. GitHub Actions (`.github/workflows/release.yml`) on tag push: macos-latest arm64, CGO_ENABLED=1; GoReleaser builds CLI for arm64+x86_64 and pushes formula `twonb.rb` to `apresai/homebrew-tap`; Swift builds `SecondBrain.app`, ad-hoc codesigns, packages as `SecondBrain-<VERSION>-arm64.zip`, pushes cask `secondbrain.rb`.
4. `make release-local` — same flow locally, CLI only (no app build).

Key files: `.goreleaser.yaml`, `.github/workflows/release.yml`, `casks/secondbrain.rb.tmpl` (with `CASK_VERSION`/`CASK_SHA256` tokens), `scripts/update-changelog.sh`, `CHANGELOG.md`.

The `apresai` GitHub environment provides `HOMEBREW_TAP_TOKEN` (PAT for `apresai/homebrew-tap`).

The macOS editor is distributed as an arm64 ad-hoc signed `.app` bundle (not Apple-notarized), so users must right-click > Open on first launch. The cask `depends_on formula: "apresai/tap/twonb"` because the app shells out to `2nb` for AI/indexing/lint.

## Build

```bash
make build              # Both CLI and app (regenerates Version.swift)
make build-cli          # cli/bin/2nb only
make build-app          # macOS editor
cd cli && make test     # All Go tests
cd cli && make install  # Install to /usr/local/bin/2nb
```

**Required:** `CGO_ENABLED=1` and `-tags fts5` for all Go compilation.

Launch the macOS editor via `open` on the `.app` bundle — never run the raw binary directly (it won't register with the window server):

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
make install            # Build + install CLI to /usr/local/bin + app to /Applications
```

### No Mock Tests Policy

**All tests MUST use real API endpoints — local or paid. Mocks (`httptest.NewServer`, fake responses, stub implementations) are NOT allowed.** Tests needing a provider call the real API and skip if credentials/services are unavailable. This applies to AI provider tests (Bedrock, OpenRouter, Ollama), MCP tests, and any future integration tests.

- Bedrock: real AWS credentials; skip if not configured
- OpenRouter: real `OPENROUTER_API_KEY`; skip if not set
- Ollama: real server at localhost:11434; skip if not running or model not pulled
- Pure logic tests (string classification, price parsing) that don't call any API are fine

### GUI Test Automation

GUI tests use AppleScript for app interaction and `screencapture` for verification. Run `make install` first — computer-use MCP requires apps in `/Applications`.

Test scripts live in `tests/`: `gui-helpers.sh` (shared), `gui-test-crud.sh`, `gui-test-navigation.sh`, `gui-test-editor.sh`, `gui-test-ui.sh`, `gui-test-vault.sh`, `gui-test-vault-switch.sh`, `gui-test-ai.sh`, `gui-test-polish.sh` (credential-gated).

Key patterns:
- **NSAlert dialogs** (New Note): type in text field, navigate popup via accessibility, press Return.
- **SwiftUI overlays** (Quick Open, Search, Command Palette): rely on menu shortcuts (not `.onKeyPress`) since NSTextView steals focus. `makeFirstResponder(nil)` + `@FocusState` ensures overlay TextFields get focus.
- **Sidebar clicks**: AppleScript `click at {x, y}` coordinates.
- **Screenshots**: `/tmp/sb-gui-tests/` for debugging.

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
| `internal/mcp` | MCP server with 16 tools + sidecar status files |
| `internal/git` | Read-only git wrappers (IsRepo, Activity, DiffFile, StatusFiles) |
| `internal/skills` | Skill file generation and agent registry |
| `internal/output` | JSON/CSV/YAML formatters |
| `internal/testutil` | Test helpers (NewTestVault, CreateAndIndex) |

Key types: `document.Document`, `store.DB`, `vault.Vault`, `search.Engine`, `graph.Graph`.

### CLI Commands (51)

Organized into groups: Getting Started, Documents, Search & AI, Quality, Integration, Import/Export, Configuration. Use `--help` on any command for full flag detail.

| Command | Purpose |
|---------|---------|
| `init` | **Deprecated alias** for `vault create` |
| `vault [path]` | Health report (same as `vault status`); legacy positional path acts like `vault set` |
| `vault status` | Unified health: vault info, index coverage, portability, AI reachability, stale docs |
| `vault show` | Terse summary (path, source, name, doc count); `--json` |
| `vault create <path>` | Initialize a new vault and make it active (replaces `init`) |
| `vault set <path>` | Set existing vault as active |
| `vault list` | List recently used vaults; reads `~/.2ndbrain-vaults` |
| `create` | Create document from template (`--type`, `--title`) |
| `read` | Read full document or specific section (`--chunk`) |
| `meta` | View or update frontmatter with schema validation (`--set key=value`) |
| `index` | Rebuild index. `--doc <path>` for a single doc; `--force-reembed` invalidates every stored embedding |
| `search` | Hybrid BM25 + semantic. Filters: `--type --status --tag --limit`. `--threshold` overrides cosine cutoff. `--bm25-only` |
| `list` | List documents with filters (`--type --status --tag --limit --sort`) |
| `lint [glob]` | Validate schemas, check broken wikilinks |
| `stale` | List documents not modified within N days (`--since`) |
| `related` | Find related docs via link graph (`--depth`) |
| `graph` | Output link graph as JSON adjacency list |
| `export-context` | Generate CLAUDE.md-compatible context bundle (`--types --status --limit`) |
| `delete` | Delete document from disk and index (`--force`) |
| `import-obsidian` | Import Obsidian vault (adds UUIDs, normalizes tags, builds index) |
| `export-obsidian` | Export to Obsidian format (`--strip-ids`) |
| `mcp-server` | Start MCP server on stdio transport |
| `mcp-setup` | Show MCP setup instructions for all AI tools |
| `mcp status` | List live MCP server processes and recent tool invocations (`--json`) |
| `suggest-links` | Suggest semantically related documents to link from a given document (`--limit`) |
| `polish` | AI copy-edit (`--system`, `--max-tokens`) — returns original + polished for diff preview |
| `git activity` | Recent commits touching vault files (`--since 7d`, `--json`) |
| `git show <hash>` | Full commit detail: metadata, stats, per-file diffs |
| `git diff <path>` | Unified diff of a file vs HEAD |
| `git status` | Uncommitted/untracked files in the vault |
| `ask <question>` | RAG Q&A — search vault, generate answer with sources |
| `ai status` | Provider, models, readiness, embedding count, vault portability state with one-line fix hints |
| `ai embed <text>` | Generate embedding vector (debug) |
| `ai setup` | Multi-provider setup wizard (`--provider --embedding-model --generation-model`) |
| `ai local` | Check local AI readiness (Ollama, models, disk, RAM, embeddings) |
| `models list` | Verified catalog. Flags: `--type --free --discover --status --provider --promote --scope --enabled-only`. `--discover --promote` tests unverified models concurrently and adds passing ones. `--enabled-only` drops user-disabled (dropdowns pass this; CLI use does not) |
| `models test <id>` | Smoke-test a model. `--save` writes to user catalog regardless of pass/fail (success → `tier=user_verified`, failure → `test_error`). Default `--scope vault` |
| `models add <id>` | Add/update a model. Default scope is per-vault `.2ndbrain/models.yaml`; `--scope global` writes `~/.config/2nb/models.yaml`. Updates *merge*: `Enabled`, `TestedAt`, `TestLatencyMs`, `Benchmark` are preserved unless explicitly re-set. `--similarity-threshold` is embedding-only; `--price-request` is for per-request priced models |
| `models remove <id>` | Remove from user catalog (`--provider --scope`) |
| `models enable [id]` | Mark enabled. With `--vendor <name>` (e.g. `anthropic`/`amazon`/`google`) toggles every model from that vendor — the GUI's bulk toggle. `--vendor` and `<id>` are mutually exclusive |
| `models disable [id]` | Hide from selection dropdowns (still listed by `models list`). Same `--vendor` bulk mode |
| `models enable-state <id>` | Tri-state pointer: `--state default|enabled|disabled`. `default` clears for tier defaults. Used by GUI Enable State menu |
| `models cost-preview [ids...]` | Estimate USD cost across one or more models. `--probe test|bench_embed|bench_gen|bench_rag|retrieval`. Local — no API calls |
| `models wizard` | Interactive end-to-end: providers → discover → easy-mode → cost preview → test → save. `--json` emits line-delimited events; aborts non-interactively if estimated cost > `--cost-cap` (default $0.10) |
| `models bench` | Benchmark against the vault. `--probe embed|generate|retrieval|search|rag`. `retrieval` is zero-API (scores stored embeddings). History in `.2ndbrain/bench.db`; per-model summary written at `--summary-scope` (default `global`). `--json` emits line-delimited events |
| `models calibrate` | Sample baseline cosine distribution and recommend a similarity threshold. `--samples --save --scope --seed` |
| `models bench fav/unfav/favs/history/compare` | Manage benchmark favorites and view history |
| `skills list/install/uninstall/show` | Generate SKILL.md for AI coding agents (`--user`, `--all`, `--force`) |
| `config show/get/set/set-key` | Read/write config; `set-key <provider>` stores API key in macOS Keychain |
| `completion` | Emit shell completion script (`zsh|bash|fish|powershell`) |
| `completion install` | Install zsh completion idempotently into existing dir from `.zshrc` (or `~/.zsh/completions/_2nb`, or `--dir`); compinit runs unconditionally; warns on multiple `2nb` binaries on PATH |

**Shell completion** dispatches to the built binary so it stays fresh. Homebrew installs scripts via GoReleaser; non-brew users run `completion install`.

**Global flags:** `--format` (json/csv/yaml), `--porcelain`, `--json`, `--csv`, `--yaml`, `--vault`, `--verbose` / `-v`.

**Parent-command defaults:** `2nb ai` → `ai status`, `2nb models` → `models list`, `2nb git` → `git status`, `2nb mcp` → `mcp status`, `2nb skills` → `skills list`, `2nb config` → `config show`. `--help` still works (Cobra intercepts before `RunE`).

### Similarity Threshold

Hybrid search drops vector hits below the active threshold. Resolution chain (`AIConfig.ResolveSimilarityThresholdFull`):

1. Vault `ai.similarity_threshold` (if > 0)
2. User catalog `RecommendedSimilarityThreshold` (`~/.config/2nb/models.yaml` or `.2ndbrain/models.yaml`)
3. Active model's recommendation in `BuiltinCatalog()`
4. `DefaultSimilarityThreshold` (`0.20`)

Different embedding models have very different baseline distributions. Builtin recommendations: Nova-2 `0.65` (measured on a real vault), Nemotron-VL `0.60`, nomic-embed-text/Titan-v2/Cohere-embed `0.50`, mxbai/snowflake/bge-m3 `0.55`, all-minilm `0.35`. The rest are estimates from training objectives.

Configure via `2nb config set ai.similarity_threshold 0.65`, save calibration via `2nb models calibrate --save`, or override per-query with `--threshold`. `2nb ai status` prints the active value and source. `2nb models list` shows recommendations in a THRESHOLD column. Search results display `(rrf=X.XXX, cos=Y.YYY)` so semantic relevance is judgable directly.

**Calibration** (`2nb models calibrate`) samples random doc pairs, computes cosine distribution (p50/p90/p95/p99), and recommends `p95 + 0.01` rounded up. Default 500 samples; small vaults clamp to `n*(n-1)/2`. `--save` upserts a user-catalog entry carrying only the threshold.

### Other Subsystems

**Bedrock embedding:** Beyond builtin models, supports TwelveLabs Marengo embed via Bedrock InvokeModel. Marengo 2.7 takes `{"inputType":"text","inputText":"..."}`; Marengo 3.0 wraps the text: `{"inputType":"text","text":{"inputText":"..."}}`. Both return `{"data":{"embedding":[...]}}`. Add via `2nb models add <id> --provider bedrock --type embedding --price-request <USD>`.

**Live pricing:** `models list`, `ai status`, `index` fetch pricing from OpenRouter `/models` and AWS pricing offer files with a 24h disk cache at `$XDG_CACHE_HOME/2nb/pricing` (macOS: `~/Library/Caches/2nb/pricing`). 15s timeout; air-gapped falls back to stale cache then to builtin metadata.

**Invoke strategy:** Catalog entries carry an `InvokeStrategy` naming the API dialect. Strategies (in `cli/internal/ai/invoke_strategy.go`): `bedrock_converse`, `bedrock_invoke_{anthropic,nova,nova_embed,titan_embed,cohere_embed,marengo_2_7,marengo_3_0}`, `anthropic_messages`, `openai_{chat,embeddings}`, `openrouter_{chat,embeddings}`, `ollama_{generate,embeddings}`. Empty = "use provider default". User catalog overrides builtin. Adding a model variant no longer requires dispatcher code changes — a catalog entry with the right strategy is enough.

**Retrieval-quality probe:** Scores stored embeddings by checking whether each resolved wikilink's target appears in the source's top-K semantic neighbors. Returns MRR@K and Recall@K (K=10) over the usable-pair set. Requires ≥10 resolved wikilink pairs (configurable via `MinLinksForRetrievalProbe`); below that returns `ErrTooFewLinks` so callers skip silently. Zero API cost.

**Cost estimator:** Per-probe token assumptions — `test` (20 in / 32 out / 1 req), `bench_embed` (10 in / 0 out), `bench_gen` (20 / 128 / 1), `bench_rag` (2500 / 512 / 1), `retrieval` (0). `KnownPricing` distinguishes known-free (`Local=true` or explicit $0 with `PriceSource` set) from unknown.

**Logging:** `--verbose` writes structured `slog` to stderr and `.2ndbrain/logs/cli.log`. Without `--verbose`, only the file.

### Vault Portability

A vault is self-contained: markdown files + `.2ndbrain/index.db` + `.2ndbrain/config.yaml`. `tar czf` and open elsewhere with no migration. DB paths are vault-relative (`internal/vault/indexer.go`), IDs are UUIDs from frontmatter, embeddings are self-contained `[]float32` BLOBs.

**Source of truth:** the DB's `documents.embedding_model` column and BLOB length record what produced the stored embeddings. Config is user preference only — `2nb index` never writes derived state back to `config.yaml`, so git-shared team vaults don't produce merge conflicts.

| DB state | Outcome | Fix |
|---|---|---|
| embeddings match dim D and model M, provider available | **OK** | none |
| dim D in DB, current provider produces D' ≠ D | **DIMENSION BREAK** | `2nb index --force-reembed` or switch provider back |
| model M in DB, M' ≠ M in config, same dim | **MODEL MISMATCH** | next `2nb index` auto re-embeds on content change, or `--force-reembed` now |
| provider configured but `Available()=false` | **PROVIDER UNAVAILABLE** | start/install provider; BM25 runs meanwhile |
| mixed models in DB | **MIXED** | `2nb index --force-reembed` |
| zero embeddings, docs present | **UNINDEXED** | `2nb index` (BM25 still works) |
| vault `schema_version > max` | **DB TOO NEW** | `brew upgrade apresai/tap/twonb` |
| `config.yaml` missing/corrupt | **self-heals** | regenerated; `.bak` preserved on corrupt |

**Loud degradation:** `2nb search` and `2nb ask` call `VectorCompat` (`cli/internal/cli/helpers_vector.go`) at the hybrid gate. If embeddings aren't usable, they print one stderr line, collect into a `warnings` slice, and force BM25-only. The Swift app sees the same messages via `--json` envelope and shows a yellow banner; status-bar AI dot turns yellow on any non-OK state.

**Shipping a vault:** exclude personal/local state:

```bash
tar czf vault.tar.gz \
  --exclude='.2ndbrain/logs' \
  --exclude='.2ndbrain/recovery' \
  --exclude='.2ndbrain/mcp' \
  --exclude='.2ndbrain/bench.db' \
  my-vault/
```

`.2ndbrain/config.yaml` and `.2ndbrain/index.db` *should* stay in single-user tarballs. For git-shared team vaults, `2nb vault create` writes a `.gitignore` excluding `config.yaml`, `index.db` (+ WAL), `bench.db`, `logs/`, `recovery/`, `mcp/`, `*.bak`. Only `schemas.yaml` is committable.

**Privacy caveat:** embeddings are a lossy reconstruction of source text — shipping a vault with embeddings is functionally equivalent to shipping (approximate) content. A `--strip-embeddings` export mode is future work.

**JSON envelope (breaking change from 0.1.12):** `2nb search --json` and `2nb ask --json` return `{mode, warnings, results}` / `{mode, warnings, answer, sources}` envelopes. Programmatic consumers that decoded a raw array/object need to extract `.results` / `.answer`. The Swift app decodes via `CLISearchResponse` / `CLIAskResponse` in `AppState.swift`.

### MCP Server (16 tools)

Each `2nb mcp-server` writes a sidecar status file to `.2ndbrain/mcp/<pid>.json` (PID, start time, parent PID, last 50 invocations: tool, timestamp, duration, ok/error). The editor polls `2nb mcp status --json` every 5s. mark3labs/mcp-go has no client-connected hook, so sidecar files are the only enumeration mechanism.

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
| `kb_structure` | Document heading hierarchy |
| `kb_delete` | Delete from vault and index |
| `kb_index` | Rebuild index and embeddings |
| `kb_suggest_links` | Find semantically related docs to link from a given doc |
| `kb_polish` | AI copy-editor returns original + polished for diff |
| `kb_git_activity` | Recent git commits touching vault files |
| `kb_git_diff` | Unified diff of a file vs HEAD |
| `kb_git_status` | Map of path → porcelain status for uncommitted files |

### Testing

Tests use `t.TempDir()` for isolated vaults; each creates its own SQLite DB. Run with `cd cli && make test` (`go test -race -tags fts5 ./...`).

## Swift macOS Editor (`app/`)

**Framework:** SwiftUI + AppKit, Swift 6.0, macOS 14+
**Dependencies:** GRDB.swift (SQLite), Yams (YAML), swift-markdown
**Architecture:** MVVM with `@Observable`, NSTextView for editor

The Swift app reads the same `.2ndbrain/index.db` the CLI writes (WAL mode for concurrent access).

### macOS SwiftUI Gotchas

- **Sheets are broken with NSViewRepresentable:** SwiftUI `.sheet()` modals don't receive button/keyboard events when the parent view contains an NSViewRepresentable (like our NSTextView editor). Use AppKit dialogs (`NSAlert.runModal()` or `NSOpenPanel.runModal()`) — never `beginSheetModal` (same issue).
- **Computer-use access:** The `.app` bundle must have a real binary (not symlink) and be ad-hoc codesigned (`codesign -s - --deep --force`). The Makefile handles this.
- **Troubleshooting:** When hitting SwiftUI platform bugs, use Context7 and Brave Search before guessing.

### Context7 Library IDs

| Library | ID |
|---------|----|
| SwiftUI (Apple docs) | `/websites/developer_apple_swiftui` |
| Swift language book | `/swiftlang/swift-book` |
| Swift concurrency migration | `/swiftlang/swift-migration-guide` |
| GRDB.swift (SQLite) | `/groue/grdb.swift` |

### GUI Features

| Feature | File | Notes |
|---------|------|-------|
| Vault creation | VaultManager.swift | Creates `.2ndbrain/` |
| Vault opening | SecondBrainApp.swift | Folder picker (Cmd+Shift+O) |
| Document editing | EditorArea.swift | NSTextView, debounced sync, read-only mode for >100 MB files |
| Editor mode toggle | EditorArea.swift | Source / Split / Preview segmented control |
| Live preview | EditorArea.swift | Read-only HTML via WKWebView. Removed in 0.1.15: contenteditable + Turndown.js corrupted markdown with Mermaid SVGs |
| Document templates | AppState.swift | ADR, Runbook, Note, Postmortem, PRD, PR/FAQ |
| Document duplication | SidebarView.swift | Context menu → fresh UUID + "(copy)" title |
| Drag to open | ContentView.swift | Drop `.md` from Finder into new tabs |
| Document deletion | SidebarView.swift | Context menu w/ confirmation |
| Autosave | AppState.swift + PreferencesView.swift | Toggle + 15/30/60s interval picker |
| Low disk warning | AppState.swift | NSAlert when volume < 50 MB before save |
| Filename collision | AppState.swift | `uniqueFilename` appends -1, -2, ... |
| Pre-write crash snapshot | CrashJournal.swift | Sync `.recovery.md` before every write |
| Merge conflict dialog | MergeConflictView.swift | NSHostingController window with two DiffView panes when FSEvents detects external edit to a dirty tab |
| Parse-on-open recovery | AppState.swift | Recovery dialog when FrontmatterParser throws |
| Frontmatter editing | PropertiesView.swift | Type-appropriate controls |
| Wikilink autocomplete | MentionAutocompleteController.swift | `@` and `[[` triggered popover |
| Search panel | SearchPanelView.swift | Vault-wide w/ type filters (Cmd+Shift+F) |
| Quick open | QuickOpenView.swift | Fuzzy filename (Cmd+P) |
| Command palette | CommandPaletteView.swift | All commands w/ fuzzy search (Cmd+Shift+P) |
| Graph view | GraphView.swift + Graph/ | Obsidian-style force-directed canvas, inspector (mode, filters, forces, color groups), global/local modes, zoom/pan/drag, hover/selection highlighting. Barnes-Hut quadtree O(n log n) repulsion, scales past 1K nodes |
| Backlinks panel | BacklinksView.swift | Documents linking to current |
| Outline panel | SidebarView.swift | Heading hierarchy |
| Properties panel | PropertiesView.swift | Editable frontmatter (Cmd+Option+I) |
| Tab system | TabBarView.swift | Multiple docs with dirty indicators |
| Focus mode | ContentView.swift | Distraction-free (Cmd+Shift+E); mouse top edge briefly reveals tab bar + breadcrumb |
| Status bar | StatusBarView.swift | Doc type, status, word/chunk count, token estimate, AI dot, MCP dot |
| AI status popover | StatusBarView.swift | Clickable AI dot → provider/models/staleness/rebuild |
| MCP status panel | MCPStatusView.swift | Cmd+Shift+M → per-client list w/ recent invocations |
| MCP status indicator | StatusBarView.swift | Green dot + client count, polls `2nb mcp status --json` every 5s |
| Index rebuild | IndexProgressView.swift | Confirmation → progress → stats |
| Lint validation | LintResultsView.swift | Shells out to `2nb lint --json` |
| Skills install | SkillsInstallView.swift | SKILL.md for 8 AI agents (AI menu) |
| MCP setup | MCPSetupView.swift | Config snippets for 6 AI tools |
| Vault Status panel | VaultStatusView.swift | Unified health (Vault > Vault Status…); Rebuild Index + Re-embed All |
| AI Hub | AIHubView.swift | See below |
| Window toolbar | ContentView.swift | New Note / Search / Quick Open visible whenever a vault is open |
| File→Notes menu rename | AppDelegate.swift | AppKit hook; reapplies on `NSMenu.didBeginTrackingNotification` + `applicationDidBecomeActive` |
| Obsidian import/export | SecondBrainApp.swift | Shells out to CLI w/ folder picker |
| Spotlight indexing | SpotlightIndexer | CoreSpotlight integration |
| Crash recovery | CrashJournal | Recovery dialog on launch |
| File watching | FSEventsWatcher | Auto-reload on external changes |
| Ask AI panel | AskAIView.swift | RAG overlay via `2nb ask` (Cmd+Shift+A) |
| Semantic search | SearchPanelView.swift | Toggle for AI-powered hybrid |
| Find Similar | SidebarView.swift | Context menu → semantic search |
| Tag drill-down | TagBrowserView.swift | Click tag → filtered file list → back |
| Preferences | PreferencesView.swift | Font family/size + autosave interval (Cmd+,) |
| PDF/HTML/MD export | ExportController.swift | PDF via WKWebView |
| Suggest Links | SuggestLinksView.swift | Cmd+Shift+L → click-to-insert wikilinks |
| AI Polish | PolishView.swift | Cmd+Option+P → DiffView w/ Accept/Open-as-tab/Reject |
| Diff view | DiffView.swift | Reusable Myers LCS unified diff |
| Git sidebar indicators | SidebarView.swift | Orange = modified, blue = untracked (when vault is git repo) |
| Git activity | GitActivityView.swift | Cmd+Shift+G → recent commits with 1/3/7/30 day window |
| Commit detail | CommitDetailView.swift | Click commit row → split pane: file list + per-file diff |
| Git diff viewer | GitDiffView.swift | Right-click → Show Changes vs HEAD |

**AI Hub (AIHubView.swift)** — Single merged surface (AI menu > AI… · Cmd+Shift+,) for everything AI. Three sections:

- **Providers** — Bedrock / OpenRouter / Ollama cards with live status, enable/disable. Provider disable is vault config: `ai.<provider>.disabled` hides every model from that provider.
- **Active** — current embedding + generation slot, each with `Change` button.
- **Catalog** — grouped by vendor within type (Embedding first, then Generation); each group is a collapsible disclosure with count, latest-first rows, and `Enable all` / `Disable all` bulk buttons.

Per-row action `Details` opens `ModelCatalogPickerView` (sidebar + detail; filters: type/provider/tier/enabled/tested/compatible; sort: Best/Cheapest/Fastest/Newest/Name; actions: Test, Set Active, Set Active + Re-embed, Enable State tri-state, Similarity Threshold override, Cost Preview per probe kind, Benchmark with streaming events).

Replaces the AI Setup Wizard, Test AI Connection, and Model Wizard. Observes `modelsCatalogVersion` so external CLI edits refresh live. Vendor identity (`vendor / vendor_display / family / version_sort_key`) and the `compatible` flag are computed by the Go CLI in `applyCatalogUIFields` and sent over JSON — Swift no longer mirrors that logic. `Set Active` is gated on `appState.isIndexing` and refused at the AppState layer to prevent mixed-model embeddings during a rebuild.

### Keyboard Shortcuts

| Shortcut | Action | | Shortcut | Action |
|----------|--------|-|----------|--------|
| Cmd+N | New Note | | Cmd+Option+I | Properties Panel |
| Cmd+S | Save | | Cmd+Shift+R | Reveal Note in Finder |
| Cmd+Shift+O | Open Vault | | Cmd+1 | Files Panel |
| Cmd+P | Quick Open | | Cmd+2 | Outline Panel |
| Cmd+Shift+P | Command Palette | | Cmd+3 | Links Panel |
| Cmd+Shift+A | Ask AI | | Cmd+4 | Tags Panel |
| Cmd+Shift+, | AI Hub | | Cmd+\\ | Toggle Sidebar |
| Cmd+Shift+L | Suggest Links | | Cmd+, | Preferences |
| Cmd+Option+P | Polish Document | | Cmd+= | Increase Font Size |
| Cmd+Shift+M | MCP Server Status | | Cmd+- | Decrease Font Size |
| Cmd+Shift+G | Recent Git Activity | | Cmd+0 | Reset Font Size |
| Cmd+Option+G | Graph View | | Cmd+Shift+X | Export as PDF |
| Cmd+Shift+F | Search Panel | | Cmd+Shift+T | Typewriter Mode |
| Cmd+Shift+E | Focus Mode | | Cmd+Option+R | Inline Preview |

## Vault Format

Full reference: [`docs/vault-structure.md`](docs/vault-structure.md) and [`docs/templates.md`](docs/templates.md).

**Quick reference:**

```
vault-root/
├── .2ndbrain/
│   ├── config.yaml      # Vault name, embedding settings
│   ├── schemas.yaml     # Document type schemas (committable)
│   ├── index.db         # SQLite index (shared between CLI and editor)
│   ├── bench.db         # Benchmark history + favorites
│   ├── mcp/             # <pid>.json per running mcp-server
│   ├── recovery/        # Crash recovery snapshots
│   └── logs/            # Error logs
├── document-1.md
└── subdirectory/document-2.md
```

Documents are plain `.md` with YAML frontmatter (`id` UUID, `title`, `type`, `status`, `tags`, `created`, `modified`). Wikilinks: `[[target]]`, `[[target#heading]]`, `[[target|alias]]`.

**Document type schemas** (`.2ndbrain/schemas.yaml`):

| Type | Required | Statuses |
|------|----------|----------|
| **adr** | title, status | proposed → accepted/deprecated → superseded |
| **runbook** | title, status | draft, active, archived |
| **prd** | title, status | draft → review → approved → shipped → archived (review/approved can return to draft) |
| **prfaq** | title, status | draft → review → final |
| **note** | title | draft, complete |
| **postmortem** | title, status, incident-date | draft, reviewed, published |

**SQLite tables (`index.db`):** `documents`, `chunks`, `chunks_fts` (FTS5), `links`, `tags`, `schema_version`.

**SQLite tables (`bench.db`):** `favorites` (provider, model_id, model_type, added_at), `runs` (timestamp, provider, model_id, probe, latency_ms, ok, detail, vault_doc_count), `schema_version`. Created on first `models bench`.

## Obsidian Conversion

**Import (`2nb import-obsidian`)** — generates UUID `id` for missing docs, sets `type: note` if absent, normalizes inline `#tag` to frontmatter `tags` array, preserves existing frontmatter, maps Obsidian `aliases` to wikilink resolution, preserves `.canvas` files, initializes `.2ndbrain/` and builds index.

**Export (`2nb export-obsidian`)** — copies markdown, creates `.obsidian/` with default config, converts UUID-based references to filename-based wikilinks. `--strip-ids` removes `id` and `type` fields.

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
