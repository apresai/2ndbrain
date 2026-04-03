# 2ndbrain: The Markdown Editor That Makes Your Knowledge Base AI-Native

**The first macOS markdown editor designed from the ground up for AI-assisted development — shipping with a built-in MCP server, hybrid semantic search, and a CLI that speaks JSON.**

---

AUSTIN, TX — Today, Apres AI announced 2ndbrain, a native macOS markdown editor purpose-built for developers who work alongside AI coding assistants like Claude Code, Cursor, and Copilot. Unlike existing editors that bolt AI features onto legacy architectures, 2ndbrain treats AI integration as a first-class design constraint — making every document in your knowledge base instantly searchable, retrievable, and actionable by both humans and machines.

## The Problem

Developers maintain critical knowledge in markdown — architecture decision records, runbooks, API documentation, project notes, changelogs. But the tools that edit this knowledge and the AI tools that consume it live in completely separate worlds.

When Claude Code needs to understand your project, it reads files with `grep`. It searches by keyword, not meaning. It can't ask "what decisions did we make about authentication?" and get a ranked answer — it can only match the exact string "authentication" across thousands of files, consuming tokens on irrelevant matches. A benchmark on a 155,000-line codebase showed vanilla grep-based search spawned 5 subagents and burned massive token budgets. A semantic search layer reduced input tokens by 97% — same results, fraction of the cost.

Meanwhile, Obsidian — the most popular markdown knowledge base — runs on Electron, consuming 500MB of RAM. Its search is keyword-only. Its metadata index exists only while the app is running. It has no native CLI, no MCP server, and no structured output format for AI tools. The 20+ community-built Obsidian MCP servers are all workarounds for an integration layer that should be built in.

The gap is clear: developers need a markdown editor where the human writing experience and the AI retrieval experience are designed together, not stitched together.

## The Solution

2ndbrain is a native macOS application built with Swift and AppKit. It edits plain markdown files — no proprietary format, no lock-in. But under the surface, it maintains a persistent hybrid search index combining BM25 keyword matching with local vector embeddings, fused via Reciprocal Rank Fusion. Every document gets a stable UUID in its frontmatter that survives renames. Every heading becomes an addressable chunk that AI tools can retrieve directly.

The editor ships with three interfaces:

**A native macOS editor** with live preview, wikilinks, backlinks, graph visualization, vim mode, and a command palette. It integrates with Spotlight (search your vault from anywhere on your Mac), Quick Look (preview markdown in Finder), and the system clipboard. It launches in under 2 seconds and runs at 50MB of RAM — not 500.

**A full CLI (`2nb`)** that operates without the GUI. Every operation — search, read, create, update metadata, lint, export — outputs structured JSON by default. AI tools pipe it directly. Developers script it. CI/CD runs it. `2nb search "JWT refresh strategy" --type adr --status accepted --json` returns ranked results with chunk content, relevance scores, and frontmatter metadata in a format Claude Code can consume in a single tool call.

**A built-in MCP server** that exposes your vault as searchable resources to any MCP-compatible AI assistant. Claude Code, Claude Desktop, Cursor, Windsurf — they all connect directly. No plugin installation, no community server, no middleware. Your knowledge base becomes part of the AI's context automatically. When you save a document, subscribed AI clients get notified. When they need context, they search semantically — not by filename.

"We built 2ndbrain because we were tired of the disconnect between where developers think and where AI assistants look," said Chad Neal, founder of Apres AI. "Your best architectural decisions are sitting in markdown files that your AI assistant can't meaningfully search. 2ndbrain closes that gap. Your knowledge base becomes your AI's memory — instantly searchable, semantically indexed, and always up to date."

## How It Works

**Write naturally, index automatically.** Create documents using built-in templates for ADRs, runbooks, postmortems, and project notes. Each template enforces a frontmatter schema with typed properties — status fields with valid transitions, enumerated tags, required relationships. The editor validates as you write, so your knowledge base stays structured without extra effort.

**Search by meaning, not just keywords.** Type "how do we handle auth token refresh?" and get ranked results from across your vault — even if no document contains that exact phrase. The hybrid search engine combines keyword precision with semantic understanding, all running locally on your machine using GGUF embedding models. No API keys. No cloud. No token costs.

**Connect to your AI tools in one line.** Add `2nb mcp-server` to your Claude Code config and your vault becomes a first-class context source. Claude can search your ADRs before suggesting an architecture, read your runbooks before debugging an incident, check your conventions before writing code. The MCP server exposes `kb_search`, `kb_read`, `kb_related`, `kb_create`, and `kb_update_meta` as tools — the exact operations AI coding assistants need.

**Export context bundles for any AI workflow.** Run `2nb export-context --types adr,runbook --status accepted` and get a CLAUDE.md-compatible file containing your most relevant documents, sized to fit a context window. Use it as a pre-commit hook, a Claude Code memory source, or a prompt engineering artifact.

"Before 2ndbrain, I had 400 lines of CLAUDE.md instructions that Claude would forget halfway through a session," said a developer during the beta program. "Now I point Claude at my vault and it finds what it needs. My ADRs actually influence the code Claude writes. The CLI alone saved me hours of copy-pasting context into prompts."

## Key Features

- **Native macOS**: SwiftUI + AppKit. Spotlight search, Quick Look, Touch ID, Handoff. Sandboxed and notarized.
- **Plain markdown files**: No database, no proprietary format. Your files work with git, grep, and every other tool you already use.
- **Hybrid search**: BM25 + vector embeddings with Reciprocal Rank Fusion. Local GGUF models — no API keys.
- **Structured frontmatter**: Schema validation, typed properties, status state machines. Your metadata is always clean.
- **Stable UUIDs**: Every document gets a persistent ID that survives renames. AI-generated cross-references don't break.
- **Built-in MCP server**: stdio transport, no plugins needed. Works with Claude Code, Claude Desktop, Cursor, and any MCP client.
- **CLI-first**: `2nb` outputs JSON, YAML, CSV, or plain text. Scriptable, pipeable, AI-friendly.
- **Knowledge graph**: Wikilinks, backlinks, graph visualization. Resolve by UUID first, filename second.
- **Document templates**: ADR, runbook, postmortem, service doc — each with enforced schemas.
- **Local-first privacy**: All data on your machine. All AI features run locally. Nothing leaves your disk unless you choose to push it.

## Availability

2ndbrain is available as an open-source project at [github.com/apresai/2ndbrain](https://github.com/apresai/2ndbrain). The native macOS editor requires macOS 14 Sonoma or later on Apple Silicon. The CLI and MCP server run on macOS, Linux, and WSL.

---

*Apres AI builds developer tools that bridge the gap between human knowledge and AI capability. Learn more at github.com/apresai.*
