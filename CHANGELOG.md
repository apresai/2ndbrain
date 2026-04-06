# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/).

## [Unreleased]

(empty - ready for next release)

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
