# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/).

## [Unreleased]

(empty - ready for next release)

## [0.1.13] - 2026-04-15

## [0.1.13] - 2026-04-15

### Added
- `2nb index --force-reembed` flag: invalidates all stored embeddings and re-embeds from scratch (use after switching providers)
- `2nb ai status` now reports vault portability state — dimension mismatch, model mismatch, provider unavailable, mixed embeddings, unindexed — with one-line fix hints
- `VectorCompat` helper: `search` and `ask` automatically degrade to BM25-only with a stderr warning when stored embeddings are incompatible with the current provider
- Vault `.gitignore` initialized by `2nb init` now excludes `config.yaml`, `index.db` (+ WAL), `bench.db`, `logs/`, `recovery/`, `mcp/`, and `*.bak`
- `config.yaml` self-heals: missing or corrupt config regenerates from defaults; corrupt original preserved as `.bak`
- macOS app shows a yellow warning banner over search and Ask AI results when the CLI reports degraded vector mode
- macOS app AI status dot turns yellow on any non-OK portability state

### Changed
- **Breaking:** `2nb search --json` and `2nb ask --json` now return structured envelopes — `{mode, warnings, results}` and `{mode, warnings, answer, sources}` respectively; consumers must extract `.results` / `.answer` instead of decoding a raw array/object


## [0.1.12] - 2026-04-14

### Fixed
- Remove `nonisolated` from `WKScriptMessageHandler` conformance for Xcode 16.4 compatibility


## [0.1.11] - 2026-04-14

### Added
- SecondBrain.app distributed via Homebrew Cask (`brew install --cask apresai/tap/secondbrain`)
- GitHub Actions workflow builds, packages, and publishes the macOS app bundle on release tags
- Cask template (`casks/secondbrain.rb.tmpl`) for automated formula generation


## [0.1.10] - 2026-04-14

### Added
- Search results now display RRF and cosine similarity scores (`rrf=X.XXX, cos=Y.YYY`) for transparency into hybrid ranking
- Parent-command defaults: running `2nb ai`, `2nb models`, `2nb git`, `2nb mcp`, `2nb skills`, or `2nb config` without a subcommand now invokes the most useful read-only action instead of printing help

### Changed
- Expanded MCP tool descriptions and skill file content with richer LLM-facing context for all 16 tools

### Fixed
- Wikilink resolution correctness (title/filename matching edge cases)
- Vector search threshold filtering applied consistently across hybrid and standalone semantic queries


## [0.1.9] - 2026-04-14

## [0.1.9] - 2026-04-14

### Added
- **Git integration** — `2nb git activity`, `git diff`, `git status` CLI commands; sidebar modified/untracked indicators; Git Activity panel (Cmd+Shift+G) and diff viewer in editor
- **AI Polish** — `2nb polish` CLI command with diff preview; editor panel (Cmd+Option+P) with Accept / Open-as-new-tab / Reject flow
- **Suggest Links** — `2nb suggest-links` via vector search; editor panel (Cmd+Shift+L) with click-to-insert wikilinks
- **MCP observability** — sidecar status files per server process; `2nb mcp status` command; MCP Status panel (Cmd+Shift+M) in editor with per-client tool invocation history
- **Editable preview mode** — WYSIWYG editing in preview via WKWebView ↔ Turndown.js bridge; source/split/preview segmented control in toolbar
- **Merge conflict dialog** — side-by-side diff when FSEvents detects an external edit to a dirty tab
- **Autosave** — configurable interval (Off / 15s / 30s / 60s) in Preferences
- **Safety features** — pre-write crash snapshots, low-disk warning (<50 MB), filename collision suffix (-1, -2, …)
- **Directory tree sidebar** with tag split pane, multi-tag filter, and breadcrumb bar
- New, Save, and Share toolbar buttons in editor
- Incremental re-embed on document save
- High-resolution macOS application icon set (16 px – 1024 px)


## [0.1.8] - 2026-04-11

### Added
- **Editor Preferences** (Cmd+,): font family and size picker with live preview
- **Tag drill-down**: click any tag in the sidebar to browse a filtered document list with back navigation
- **Index rebuild dialog**: confirmation step, dual progress bars (indexing + embeddings), and post-rebuild stats summary
- **Structured CLI logging**: slog-based log output to `.2ndbrain/logs/cli.log`; `--verbose` additionally routes to stderr
- **GUI test suite for index operations** (`tests/gui-test-index.sh`)

### Changed
- Export controller expanded with additional format and output path handling
- Editor area layout and rendering improvements


## [0.1.7] - 2026-04-10

## [0.1.7] - 2026-04-10

### Added
- **AI Setup Wizard** — 4-step guided wizard for configuring AI providers, credentials, models, and running a connectivity test
- **Skills Install panel** — Tools menu panel for installing SKILL.md files for 8 AI coding agents
- **MCP Setup panel** — Tools menu panel showing MCP config snippets for 6 AI tools
- **Lint Results view** — Clickable lint issue list shelled out from `2nb lint --json`
- **App icon** — Custom app icon (1024px PNG + ICNS)
- **Swift test suite** — Unit tests covering JSON decoding, frontmatter parsing, markdown rendering, and wizard logic (636 lines across 4 test files)


## [0.1.6] - 2026-04-10

## [0.1.6] - 2026-04-10

### Added
- `skills` command — discover and display vault-specific Claude skill instructions
- Easy mode option to `ai setup` wizard for simplified provider configuration
- Command grouping in CLI help output for better discoverability
- Real-API integration tests for Bedrock, OpenRouter, graph traversal, MCP tools, and output formatters

### Changed
- OpenRouter easy mode default model updated to Gemma 4 31B
- Model test and bench probes now include a system prompt for more realistic evaluation

### Fixed
- `ai status` pricing now reads from the model catalog instead of calling provider `ListModels`, correcting displayed costs


## [0.1.5] - 2026-04-09

## [0.1.5] - 2026-04-09

### Changed
- `ai setup` rewritten as an interactive multi-provider wizard supporting Bedrock, OpenRouter, and Ollama with guided configuration and validation
- README updated with model catalog reference, benchmarking workflows, and Converse API documentation


## [0.1.4] - 2026-04-09

### Added
- `models test <model-id>` command to smoke-test any model with an embed or generate probe
- `models bench` command suite for benchmarking models against your vault with persistent history
- `models bench fav` / `models bench unfav` / `models bench favs` to manage benchmark favorites
- `models bench history` to review past benchmark runs
- `models bench compare` for side-by-side latency comparison of favorited models
- Benchmark history and favorites persisted in `.2ndbrain/bench.db`

### Changed
- Bedrock provider migrated from InvokeModel API to Converse API


## [0.1.3] - 2026-04-09

### Added
- `models list` now shows a rich, status-aware model catalog indicating which models are configured, available, and ready to use
- Model catalog with merge logic to combine built-in and runtime-discovered models across providers (Bedrock, OpenRouter, Ollama)


## [0.1.2] - 2026-04-07

## [0.1.2] - 2026-04-07

### Added
- OpenRouter retry logic with exponential backoff and request throttling
- Cost awareness for OpenRouter API usage (`ai status` and `ai cost` tracking)
- GitHub Actions release workflow improvements
- `index` command now reports embedding generation progress

### Fixed
- 7 GUI crash bugs across editor, properties, tabs, status bar, autocomplete, crash recovery, and app state
- Homebrew formula renamed to `twonb` (Ruby class names cannot start with a digit)

### Changed
- `.gitignore` simplified
- Press release updated to acknowledge Obsidian inspiration


## [0.1.1] - 2026-04-06

## [0.2.0] - 2026-04-06

### Added
- **Editor**: Syntax highlighting, typewriter mode, inline Markdown preview, Mermaid diagrams, and KaTeX math rendering
- **Editor**: Slash command menu (`/`) for quick block insertion
- **Editor**: Template picker for structured document creation
- **Editor**: Tag browser panel
- **Editor**: Document export (PDF and other formats)
- **CLI**: `2nb vault` command for vault management operations
- **CLI**: `mcp-setup` command for guided MCP configuration
- **Document types**: PRD and PRFAQ with status machines and templates
- **AI**: Inline embeddings generated at index time using content hashing to skip unchanged documents
- **MCP**: Additional tools (`kb_structure`, `kb_delete`, `kb_index`)
- MIT license and contributor guide

### Fixed
- Inline rendering toggle now correctly persists state
- PDF export reliability on documents with complex content
- Offline resilience when AI provider is unreachable
- `import-obsidian` no longer modifies source vault files
- Model registry deduplication by `(provider, id)` eliminates duplicate entries in `models list`


## [0.1.0] - 2026-04-04

### Added
- Go CLI (`2nb`) with 24 commands for vault management, search, and AI
- MCP server with 9 tools for Claude Desktop integration
- Native macOS editor (SwiftUI + AppKit) with tabs, search, graph view
- Hybrid search: BM25 (FTS5) + vector search with Reciprocal Rank Fusion
- RAG Q&A via `2nb ask` with source citations
- Three AI providers: AWS Bedrock, OpenRouter, Ollama (local)
- Local AI readiness check via `2nb ai local`
- Document types with schemas: ADR, Runbook, Note, Postmortem
- Wikilink resolution and link graph traversal
- Obsidian import/export with frontmatter normalization
- Spotlight indexing, crash recovery, file watching
- GUI: Ask AI panel, semantic search toggle, AI status indicator
