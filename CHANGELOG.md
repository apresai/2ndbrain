# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/).

## [Unreleased]

(empty - ready for next release)

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
