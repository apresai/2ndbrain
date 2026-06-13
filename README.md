# 2ndbrain

[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![Release](https://img.shields.io/github/v/release/apresai/2ndbrain)](https://github.com/apresai/2ndbrain/releases)
[![Homebrew](https://img.shields.io/badge/homebrew-apresai%2Ftap%2F2nb-orange)](https://github.com/apresai/homebrew-tap)

AI companion for your Obsidian vault with semantic search. A Go CLI, MCP server, and native macOS dashboard share a SQLite index, making your knowledge base searchable by both you and your AI coding assistant. Obsidian stays your editor; 2ndbrain is the engine that indexes, searches, and answers underneath it.

## Install

```bash
brew install --cask apresai/tap/secondbrain
```

This installs both the `2nb` CLI and the SecondBrain macOS dashboard app. If you only need the CLI:

```bash
brew install apresai/tap/2nb
```

Or download binaries from [GitHub Releases](https://github.com/apresai/2ndbrain/releases).

The app is Developer ID-signed and Apple-notarized, so it launches with no Gatekeeper prompt.

**New here? Follow the [Quick Start guide](docs/quick-start.md)** for the full walkthrough: app, Obsidian plugin, AI setup, and first search.

## Quick Start

The complete walkthrough (macOS app, Obsidian plugin, AI providers, MCP) lives in **[docs/quick-start.md](docs/quick-start.md)**. The CLI fast path:

```bash
# Point 2nb at your existing Obsidian vault (or scaffold a fresh one)
2nb vault set ~/path/to/your-obsidian-vault
2nb vault create ~/vault                      # only for a brand-new vault

# Configure AI for semantic search & ask
2nb ai setup

# Index with AI embeddings (safe to run repeatedly)
2nb index

# Search (hybrid BM25 + semantic)
2nb search "authentication"
2nb search "how does auth work" --type adr

# Ask questions (RAG with source citations)
2nb ask "What authentication approach did we choose and why?"

# Check vault health any time
2nb vault

# One-time: enable shell tab-completion (zsh)
2nb completion install
```

> The legacy `2nb init` command still works (it prints a deprecation notice). Prefer `2nb vault create <path>`.
>
> Homebrew installs shell completions automatically. For manual installs, `2nb completion install` detects your existing completion directory from `.zshrc` (falling back to `~/.zsh/completions/_2nb`) and updates `.zshrc` with the required `fpath` and `compinit` block. The block is placed before early-return guards and before any existing compinit/fpath setup (e.g. from gcloud or Homebrew), so completions load correctly even when other tools initialize the completion system first. Re-running is safe — the block is replaced in-place, not duplicated. If multiple `2nb` binaries are found on PATH, a warning is printed showing each binary's version and which is active.

## Features

- **Hybrid search** — BM25 keyword + vector semantic search with Reciprocal Rank Fusion
- **RAG Q&A** — Ask questions, get answers with source citations
- **MCP server** — 16 tools for Claude Code, Cursor, and any MCP client, with live status sidecar files and an observability panel
- **Suggest Links** — AI finds semantically related documents in your vault and proposes wikilinks to insert
- **Polish** — AI copy-editor returns the original and polished text together, so any client (Obsidian plugin, MCP, CLI) can show a diff before applying
- **Vault health dashboard** — unified panel showing index state, embedding portability, stale docs, and provider reachability with one-click Rebuild Index and Re-embed All
- **Built-in installer**: the dashboard updates the CLI (`brew upgrade` behind an Update CLI button) and installs or updates the Obsidian plugin into the bound vault (`2nb plugin install` behind an Install/Update button)
- **Claude Code integration card**: shows whether the Claude Code skill is installed (with an Install button) and whether the 2ndbrain MCP server is configured in `~/.claude.json` for this vault (with a Show-setup button), so you can see at a glance if your AI assistant is wired up
- **AI connection testing** — one-click probe of your configured embedding and generation models with live latency
- **Incremental re-embed** — `2nb index` rebuilds embeddings only for documents whose content hash changed
- **Git integration (read-only)** — Recent Activity panel with per-commit file diffs in the dashboard, plus MCP git tools for AI clients
- **Skill files** — One command to teach 8 AI coding agents about your vault
- **Three AI providers** — AWS Bedrock, OpenRouter, Ollama (fully local)
- **Schema validation** — Typed frontmatter, enum constraints, status state machines
- **Wikilinks** — `[[target#heading|alias]]` with link resolution and graph traversal
- **Document templates** — ADR, runbook, prd, prfaq, postmortem, note with enforced schemas
- **Native macOS dashboard** — SwiftUI + AppKit companion app for vault health, AI configuration, plugin install, MCP monitoring, and git activity; Obsidian remains the editor
- **Local-first** — All data on disk as plain markdown in your Obsidian vault. `2nb` writes only a gitignored `.2ndbrain/` sidecar and never rewrites your notes.

## AI Providers

2ndbrain supports three AI providers for embeddings and generation. Bedrock uses the [Converse API](https://docs.aws.amazon.com/bedrock/latest/userguide/conversation-inference-call.html), so any Bedrock model works — Claude, Nova, Llama, Mistral, and more.

| Provider | Embeddings | Generation | Setup |
|----------|-----------|------------|-------|
| **AWS Bedrock** | Nova Embeddings v2 | Nova Micro, Claude, Llama, any model | Uses existing AWS SSO — zero new keys |
| **OpenRouter** | Nemotron Embed (free) | Gemma 4 31B (free), GPT-4o, Claude, etc. | `OPENROUTER_API_KEY` env var |
| **Ollama** | nomic-embed-text | qwen2.5, gemma3, llama3 | `brew install ollama` — fully local |

### Quick Setup (Any Provider)

The setup wizard detects credentials and offers recommended defaults:

```bash
2nb ai setup
# → Pick provider (Bedrock / OpenRouter / Ollama)
# → Easy mode: recommended models, or Custom: pick from catalog
# → Tests connectivity, saves config, offers to index
```

### Local AI with Ollama

Run everything locally with no cloud calls:

```bash
# Check readiness (models, disk, RAM)
2nb ai local

# Guided setup (installs models, configures vault)
2nb ai setup

# Or configure manually
2nb config set ai.provider ollama
ollama pull embeddinggemma
ollama pull qwen2.5:0.5b
2nb index
```

### AWS Bedrock (Default)

Uses your existing AWS SSO credentials — no new API keys needed:

```bash
2nb ai status
2nb config set ai.bedrock.profile my-profile
2nb config set ai.bedrock.region us-west-2
```

### OpenRouter

```bash
export OPENROUTER_API_KEY=sk-or-...
2nb config set ai.provider openrouter
2nb index
```

## Model Catalog & Benchmarking

Browse verified models across all providers, test any model, and benchmark your favorites:

```bash
# See all verified models with pricing
2nb models list

# Discover vendor catalogs (Bedrock, OpenRouter, Ollama)
2nb models list --discover

# Check credentials and reachability
2nb models list --status

# Test if a model works before switching (--save adds it to your catalog)
2nb models test amazon.nova-micro-v1:0 --save
2nb models test google/gemma-4-31b-it:free

# Discover and auto-promote all passing models in one step
2nb models list --discover --promote

# Benchmark your favorites
2nb models bench fav amazon.nova-micro-v1:0
2nb models bench fav us.anthropic.claude-haiku-4-5-20251001-v1:0
2nb models bench                  # runs embed/generate/search/rag probes
2nb models bench compare          # side-by-side latency leaderboard
2nb models bench history          # view past runs

# End-to-end wizard: discover → pick → cost preview → test → save
2nb models wizard                 # TTY interactive flow
2nb models wizard --json          # JSON-event stream (GUI / automation)

# Estimate before you run
2nb models cost-preview us.anthropic.claude-opus-4-6-v1 --probe bench_rag

# Hide a model from selection dropdowns without removing it from the catalog
2nb models disable cohere.embed-multilingual-v3 --provider bedrock --scope vault
2nb models enable  cohere.embed-multilingual-v3 --provider bedrock --scope vault
```

Models are tiered as **verified** (tested with 2nb) or **unverified** (available from vendor, use `models test` to check). The benchmark suite stores results in `.2ndbrain/bench.db` for tracking performance over time.

Every catalog entry declares an `invoke_strategy` (e.g. `bedrock_converse`, `bedrock_invoke_cohere_embed`, `openrouter_chat`) so adding a new model variant doesn't require a code change — a catalog entry with the right strategy is enough. The macOS dashboard's **AI → AI…** opens the AI Hub — a single sheet with provider cards (enable / disable Bedrock, OpenRouter, Ollama), active model status, and the full catalog with inline Test / Set active / Enable / Disable / Discover actions. Catalog changes written by the CLI propagate to the running GUI via FSEvents without reopening the vault.

## CLI Commands

Commands are organized into groups (`2nb --help` shows the full list).

**Global flags:** `--json`, `--csv`, `--yaml`, `--format`, `--porcelain`, `--vault`, `--verbose` (`-v` for debug logging to stderr and `.2ndbrain/logs/cli.log`)

### Getting Started

| Command | Description |
|---------|-------------|
| `vault` | Health report for the active vault (same as `vault status`) |
| `vault create <path>` | Initialize a new vault and make it active |
| `vault set <path>` | Set an existing vault as active |
| `vault list` | List recently used vaults |
| `vault show` | Terse summary: path, source, name, doc count |
| `init [path]` | Deprecated alias for `vault create` |

### Documents

| Command | Description |
|---------|-------------|
| `create <title> [--type adr\|runbook\|prd\|prfaq\|postmortem\|note] [--path <subdir>]` | Create document from template. `--path` files it under a vault-relative subdirectory (created if missing); default is the vault root |
| `read <path> [--chunk <heading>]` | Read document or specific section |
| `meta <path> [--set key=value]` | View or update frontmatter |
| `delete <path> [--force]` | Delete document from vault and index |
| `list [--type] [--status] [--tag] [--sort]` | List documents with filters |

### Search & AI

| Command | Description |
|---------|-------------|
| `search <query> [--type] [--status] [--tag] [--threshold]` | Hybrid BM25 + semantic search (shows `rrf` + raw `cos` scores) |
| `ask <question> [--history <path\|->]` | RAG Q&A with source citations; `--history` makes it multi-turn |
| `chat` | Interactive multi-turn Q&A session (REPL over the same pipeline) |
| `suggest-links <path> [--limit 10]` | Rank semantically related documents for wikilink insertion |
| `polish <path> [--system <prompt>]` | AI copy-edit a document (JSON with original + polished body) |
| `index [--doc <path>] [--force-reembed]` | Build search index + embeddings (full vault or a single document); `--force-reembed` invalidates every stored embedding for after an intentional provider switch |
| `ai status` | Show AI provider, models, embedding count, and vault portability state |
| `ai setup` | Multi-provider setup wizard (easy mode or custom) |
| `ai local` | Check local AI readiness (Ollama, disk, RAM, models) |
| `ai embed <text>` | Generate embedding vector (debug) |
| `models list [--discover] [--status] [--provider] [--promote] [--enabled-only]` | Verified model catalog + user catalog + vendor discovery; `--discover --promote` tests unverified models concurrently and adds those that pass; `--enabled-only` filters out user-disabled models (dropdowns use this) |
| `models test <model-id> [--save] [--scope global\|vault]` | Smoke-test any model (embed or generate probe); `--save` adds the model to your catalog if it passes |
| `models add <id> --provider --type [--scope global\|vault] [--price-in --price-out --dimensions --context-length --name --notes]` | Add a model to your user catalog (per-vault by default, or global with `--scope global`) |
| `models remove <id> --provider [--scope global\|vault]` | Remove a model from your user catalog |
| `models enable <id> --provider [--scope global\|vault]` | Mark a model enabled so it appears in dropdowns |
| `models disable <id> --provider [--scope global\|vault]` | Hide a model from dropdowns; still listed by bare `models list` |
| `models cost-preview [ids...] --probe <kind> [--provider] [--all]` | Estimate USD cost of running a probe (test / bench_embed / bench_gen / bench_rag / retrieval) across one or more models before committing |
| `models wizard [--scope] [--provider] [--skip-discover] [--cost-cap] [--json]` | Interactive discover → pick → cost preview → test → save flow; `--json` emits an event stream for GUI / automation |
| `models bench` | Benchmark favorites with persistent history |
| `models bench fav <model-id>` | Add model to benchmark favorites |
| `models bench compare` | Side-by-side latency leaderboard |
| `models bench history` | View past benchmark runs |

### Git (read-only)

| Command | Description |
|---------|-------------|
| `git activity [--since 7d]` | Recent commits that touched vault files |
| `git show <hash>` | Full commit detail: metadata, stats, per-file diffs |
| `git diff <path>` | Unified diff of a file against HEAD |
| `git status` | Uncommitted/untracked files in the vault |

### Quality

| Command | Description |
|---------|-------------|
| `related <path> --depth <n>` | Find related docs via link graph |
| `backlinks <path>` | List resolved inbound links to a document (who links to it) |
| `links <path>` | List outbound links from a document, including broken ones (each carries a `resolved` flag) |
| `orphans` | List documents nothing links to (no inbound link) |
| `deadends` | List documents that link to nothing real in the vault (no outbound link) |
| `graph <path>` | Output link graph as JSON |
| `lint [glob]` | Validate schemas, check broken wikilinks |
| `stale --since <days>` | Find stale documents |
| `outline <path>` | Heading tree of a document (heading path, level, line span) |
| `wordcount <path>` | Word, character, and heading counts over the indexable body (alias `wc`) |
| `folders` | List folders with document counts (root docs under `(root)`) |
| `tags` | List all tags vault-wide with counts |
| `aliases` | List frontmatter aliases mapped to their document |

### Integration

| Command | Description |
|---------|-------------|
| `mcp-server` | Start MCP server on stdio |
| `mcp-setup` | Show MCP setup instructions for all AI tools |
| `mcp status [--json]` | List live MCP server processes and their recent tool invocations |
| `mcp configured [--json]` | Report whether the 2ndbrain MCP server is configured in the AI client config (`~/.claude.json`) for this vault. The durable "is it set up?" check, unlike `mcp status` which reports "is it running right now?" |
| `plugin status [--json]` | Installed Obsidian plugin version vs this CLI |
| `plugin install` | Install or update the Obsidian plugin in the open vault from the latest release (alias: `plugin update`) |
| `export-context --types <types>` | Generate CLAUDE.md context bundle |
| `skills list` | List supported AI agents and install status |
| `skills install <agent> [--all] [--user]` | Install skill file for an AI coding agent |
| `skills uninstall <agent> [--all] [--user]` | Remove skill file for an AI coding agent |
| `skills show <agent>` | Preview skill content for an agent |

### Import / Export

| Command | Description |
|---------|-------------|
| `import-obsidian <path>` | Import Obsidian vault |
| `export-obsidian <path> [--strip-ids]` | Export to Obsidian format |

### Configuration

| Command | Description |
|---------|-------------|
| `config show` | Show full vault configuration (vault root + dir + name + all `ai.*` keys) |
| `config get <key>` | Get a config value (e.g., `ai.provider`, `ai.similarity_threshold`) |
| `config set <key> <value>` | Set a config value |
| `config set-key <provider>` | Store API key in macOS Keychain |

All commands support `--json`, `--yaml`, `--csv` for machine-readable output.

### Defaults and search scoring

- **Parent-command defaults**: running a command group without a subcommand invokes its most-useful read-only action: `2nb ai` → `ai status`, `2nb models` → `models list`, `2nb git` → `git status`, `2nb mcp` → `mcp status`, `2nb plugin` → `plugin status`, `2nb skills` → `skills list`, `2nb config` → `config show`. `--help` still works on every command.
- **Similarity threshold** — hybrid search drops vector hits whose cosine similarity is below the active threshold so barely-related neighbors stop padding result lists. Resolution order: explicit vault config (`2nb config set ai.similarity_threshold 0.65`) > user calibration saved by `2nb models calibrate --save` > per-model recommendation from the builtin catalog > global default `0.20`. Builtin recommendations: Nova-2 `0.65` (measured), Nemotron `0.60`, nomic-embed-text/Titan-v2/Cohere-embed `0.50`, mxbai/snowflake/bge-m3 `0.55`, all-minilm `0.35` (all estimated from each model's training objective — run `2nb models calibrate` to tune for your vault). Override per-query with `2nb search "foo" --threshold 0.35`. `2nb ai status` shows the active value and which tier supplied it.
- **Calibration** — `2nb models calibrate` samples random chunk pairs from your vault, reports the noise-floor cosine distribution (p50/p90/p95/p99), and recommends a threshold. Add `--save` to persist it to the per-vault user catalog (or `--save --scope global` for all vaults).
- **Score display** — `2nb search` now shows `(rrf=X.XXX, cos=Y.YYY)` on each result. The `rrf` is the Reciprocal Rank Fusion score used for ranking; `cos` is the raw cosine similarity from the vector channel, which is what you actually want to look at when judging whether a result is relevant. If legitimate matches are being cut, lower the threshold; if noise is slipping through, raise it.

### Portable vaults

A vault is self-describing. Paths in the index are relative, IDs are UUIDs, and embeddings live in the DB as raw float32 BLOBs — you can `tar` a vault and open it on another machine without any migration step.

If the receiver's current AI provider doesn't match what produced the embeddings, `2nb ai status` tells you exactly what's wrong:

```
Vault Embedding State:
  As-embedded:    amazon.nova-2-multimodal-embeddings-v1:0 (1024d), 42 of 42 docs
  Current cfg:    ollama / nomic-embed-text (768d)
  Status:         DIMENSION BREAK
  Action:         Vault was embedded with 1024d vectors but current provider produces 768d.
                  Run `2nb index --force-reembed` or switch provider back.
```

`2nb search` and `2nb ask` fail loudly (stderr + BM25 fallback) on dimension mismatch instead of silently returning worse results. The macOS app surfaces the same warnings as a yellow banner over search results and the Ask AI panel, and the status bar AI dot turns yellow.

**Shipping a vault:**

```bash
tar czf vault.tar.gz \
  --exclude='.2ndbrain/logs' \
  --exclude='.2ndbrain/recovery' \
  --exclude='.2ndbrain/mcp' \
  --exclude='.2ndbrain/bench.db' \
  my-vault/
```

Include `.2ndbrain/config.yaml` and `.2ndbrain/index.db` — the receiver gets the vault's as-embedded state and avoids re-embedding from scratch. For git-shared team vaults, `2nb vault create` writes a `.gitignore` that excludes personal/local state (config, DBs, logs, recovery) and commits only `schemas.yaml`. Missing or corrupt `config.yaml` / `index.db` self-heal on next open with a one-line stderr warning — the vault never bricks.

> **Heads-up for scripters:** `2nb search --json` and `2nb ask --json` now return envelopes (`{mode, warnings, results}` / `{mode, warnings, answer, sources}`; multi-turn asks via `--history` add `rewritten_query`). If you were parsing a raw array/object, extract `.results` / `.answer`.

## MCP Server

The MCP server exposes 16 tools for AI coding assistants:

| Tool | Description |
|------|-------------|
| `kb_info` | Vault overview — doc types, schemas, counts, AI status |
| `kb_search` | Hybrid BM25 + semantic search with filters |
| `kb_ask` | RAG Q&A — answer questions with source citations |
| `kb_read` | Read full document or specific heading chunk |
| `kb_list` | List documents with metadata filters |
| `kb_create` | Create document from template; optional `path` files it under a vault-relative subdirectory |
| `kb_update_meta` | Update frontmatter with schema validation |
| `kb_related` | Graph traversal to find connected documents |
| `kb_structure` | Get document heading tree as JSON |
| `kb_delete` | Delete document from vault and index |
| `kb_index` | Rebuild search index and generate embeddings |
| `kb_suggest_links` | Suggest semantically related documents to wikilink from a source doc |
| `kb_polish` | AI copy-editor returns original + polished body for diff review |
| `kb_git_activity` | Recent git commits that touched vault files (read-only) |
| `kb_git_diff` | Unified diff of a file against HEAD |
| `kb_git_status` | Uncommitted/untracked files in the vault |

Each running `2nb mcp-server` writes a sidecar status file to `.2ndbrain/mcp/<pid>.json` with PID, start time, parent PID, and the last 50 tool invocations. Run `2nb mcp status` to list live servers, or use the Cmd+Shift+M status panel in the dashboard.

### Claude Code

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

### Cursor

Add to `.cursor/mcp.json`:

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

### Claude Desktop

Add to `~/Library/Application Support/Claude/claude_desktop_config.json`:

```json
{
  "mcpServers": {
    "2ndbrain": {
      "command": "/usr/local/bin/2nb",
      "args": ["mcp-server"],
      "cwd": "/path/to/your/vault"
    }
  }
}
```

Run `2nb mcp-setup` for config snippets for additional tools (Gemini CLI, Amazon Q, Kiro).

## Skill Files

Install a skill file to teach AI coding agents about your vault's CLI, MCP tools, and document format:

```bash
# See supported agents and status
2nb skills list

# Install for one agent
2nb skills install claude-code

# Install for all supported agents
2nb skills install --all

# Install globally (all projects, not just this vault)
2nb skills install claude-code --user
```

Supported agents: Claude Code, Cursor, Windsurf, GitHub Copilot, Kiro, Cline, Roo Code, JetBrains Junie.

## macOS Dashboard

A native SwiftUI + AppKit configuration and companion app. It is **not an editor**: Obsidian stays your editor. The dashboard binds to the vault Obsidian currently has open (read from Obsidian's own registry) and gives you one place to manage everything around it.

**Home** (the default screen) covers the common cases:

- **Vault card**: active vault name and path, a badge confirming it matches the vault Obsidian has open, and an Obsidian plugin row showing the installed plugin version with an Install/Update button (runs `2nb plugin install`)
- **AI card**: provider and models (AWS Bedrock with Claude Haiku 4.5 + Amazon Nova-2 by default) with a readiness dot, Save-as-default, and Test buttons
- **Claude Code card**: whether the Claude Code skill is installed (with an Install button) and whether the 2ndbrain MCP server is configured in `~/.claude.json` for this vault (with a Show-setup button), so you can see at a glance if your AI assistant is wired up
- **Index card**: document and embedding counts with Rebuild Index and Re-embed All
- An orange banner warns when the installed `2nb` CLI is older than the app (a cask upgrade does not bump the CLI formula); when Homebrew is present it offers an Update CLI button that runs `brew upgrade apresai/tap/twonb` for you

**Advanced** tabs for the power-user depth:

- **Vault Status**: unified health (vault info, index coverage, embedding portability, AI reachability, stale docs)
- **AI Settings**: the AI Hub (Cmd+Shift+,) with provider cards, active model slots, and the full model catalog with per-model test, benchmark, and enable/disable
- **MCP Server** (Cmd+Shift+M): live MCP server processes and recent tool invocations
- **Git Integration** (Cmd+Shift+G): recent commits with a 1/3/7/30-day window; click a commit for per-file diffs
- **Validation**: `2nb lint` findings rendered with file and line detail

Menus: **Vault** (New Vault, Open Vault Cmd+Shift+O, Reveal in Finder, Vault Status, Rebuild Index, Validate Vault, Import/Export Obsidian) and **AI** (AI Hub, MCP Server Configuration, MCP Server Status).

Build and install from source:

```bash
make install    # CLI to /usr/local/bin, app to ~/Applications
```

## Document Types

| Type | Status States | Use For |
|------|--------------|---------|
| `adr` | proposed -> accepted -> deprecated/superseded | Architecture decisions |
| `runbook` | draft -> active -> archived | Operational procedures |
| `prd` | draft -> review -> approved -> shipped -> archived | Product requirements |
| `prfaq` | draft -> review -> final | Amazon-style press release / FAQ |
| `postmortem` | draft -> reviewed -> published | Incident analysis |
| `note` | draft -> complete | General knowledge |

Documents are plain `.md` files with YAML frontmatter:

```yaml
---
id: <UUID>
title: Use JWT for Authentication
type: adr
status: proposed
tags: [auth, security]
created: 2026-04-04T00:00:00Z
modified: 2026-04-04T00:00:00Z
---
# Use JWT for Authentication

Content with [[wikilinks]] to other documents.
```

## Obsidian Compatible

Import an existing Obsidian vault:

```bash
2nb import-obsidian ~/my-obsidian-vault
```

This adds UUIDs, normalizes tags from inline `#tag` to frontmatter, and builds the search index. Your files stay as plain markdown.

Export back to Obsidian anytime:

```bash
2nb export-obsidian ~/export-dir --strip-ids
```

## Development

```bash
make build          # CLI + macOS app
make install        # CLI to /usr/local/bin, app to ~/Applications
make test           # Go unit tests
make test-battery   # Golden-path E2E battery (vault, CRUD, index, MCP, skills)
make test-swift     # Swift unit tests (JSON decoding, parsing, wizard logic)
make test-gui       # GUI tests (AppleScript automation)
make test-all       # Go + battery + Swift + GUI
```

Requires Go 1.25+, CGO_ENABLED=1, macOS 14+ (for Swift app).

See [CONTRIBUTING.md](CONTRIBUTING.md) for details.

## Releasing

A release ships three products at one version: the `2nb` CLI, the Obsidian plugin, and the SecondBrain macOS app. CI builds the first two; the app is signed and notarized locally, so signing keys never enter CI.

```bash
make release-all         # the front door: test gate, bump, tag, wait for CI, then sign/notarize/publish the app
```

Or step by step:

```bash
# 1. Bump the version
make bump-build          # 0.8.0 → 0.8.1 (or bump-minor / bump-major)

# 2. Release: updates CHANGELOG.md, commits, tags, pushes; CI takes it from there
make release

# 3. After CI finishes: build, sign, notarize, and publish the app + cask (local only)
make release-app
```

GitHub Actions on the tag push:

1. Builds CLI binaries (arm64 + x86_64) via [GoReleaser](https://goreleaser.com)
2. Builds and uploads the Obsidian plugin assets (`manifest.json`, `main.js`, `styles.css`, `versions.json`)
3. Creates a [GitHub Release](https://github.com/apresai/2ndbrain/releases) with the CLI archives and plugin assets
4. Pushes the Homebrew formula (`twonb`, plus the `2nb` alias) to [`apresai/homebrew-tap`](https://github.com/apresai/homebrew-tap)

`make release-app` then runs on the maintainer's machine: it builds SecondBrain.app, signs it with a Developer ID certificate (hardened runtime), notarizes via Apple `notarytool`, staples the ticket, uploads the zip to the release, and updates the cask in the tap with the new version and sha256. Signing config is read from `scripts/sign.env` (gitignored; template at `scripts/sign.env.example`).

Users install with:

```bash
brew install apresai/tap/2nb                    # CLI
brew install --cask apresai/tap/secondbrain     # macOS dashboard app (depends on the CLI formula)
```

To build locally without GitHub Actions:

```bash
make release-local       # runs goreleaser locally (CLI only)
make package-app         # builds + zips SecondBrain.app locally (ad-hoc signed, local use only)
```

## Architecture

```
cli/        Go CLI + MCP server (cobra, go-sqlite3, mcp-go, aws-sdk-go-v2)
app/        Swift macOS dashboard (SwiftUI, GRDB.swift, swift-markdown)
plugins/    Obsidian plugin (thin wrapper that shells out to 2nb)
             |                    |
             +-------- shared ----+
                  .2ndbrain/index.db (SQLite WAL)
```

The CLI and dashboard share the same SQLite database via WAL mode for concurrent access. All AI operations go through the provider interfaces in `cli/internal/ai/`.

## License

[MIT](LICENSE) - Copyright (c) 2026 Apres AI
