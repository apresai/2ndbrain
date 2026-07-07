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

Or download from [GitHub Releases](https://github.com/apresai/2ndbrain/releases): the CLI as a `tar.gz`, and the app as a branded **drag-to-Applications `.dmg`**.

The app and its `.dmg` are both Developer ID-signed, Apple-notarized, and stapled, so it launches with no Gatekeeper prompt — whether installed via the cask or by downloading the disk image directly.

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
- **MCP server** — 22 tools for Claude Code, Cursor, and any MCP client, with live status sidecar files and an observability panel
- **Suggest Links** — AI finds semantically related documents in your vault and proposes wikilinks to insert
- **Polish** — AI copy-editor that fixes spelling, grammar, and clarity, repairs broken `[[wikilinks]]` to existing notes (case/separator/whitespace/alias drift, never guessing an ambiguous target), and adds grounded new links (never inventing a target). Returns the original and polished text together so any client can show a diff; the Obsidian plugin applies it in one click from the note toolbar, ribbon, command, or right-click menu, with an Undo button
- **Vault health dashboard** — unified panel showing index state, embedding portability, stale docs, and provider reachability with one-click Sync and Re-embed All. Notes edited in Obsidian are re-indexed and re-embedded automatically (a debounced incremental index), so search and AI stay current without a manual rebuild. When an upgrade improves how 2nb chunks or embeds your notes, `vault status` (and the app/plugin) prompt you to reindex or re-embed so your existing vault picks up the improvement — always a prompt, never an automatic re-embed
- **Built-in installer**: the dashboard updates the CLI (`brew upgrade` behind an Update CLI button) and installs or updates the Obsidian plugin into the bound vault (`2nb plugin install` behind an Install/Update button)
- **AI Clients card**: per-client rows for Claude Code, Warp, Claude Desktop, and Codex — each showing whether the skill is installed, whether the 2ndbrain MCP server is configured, and (for Claude Code / Claude Desktop) whether the global-instructions block is present, with a one-click **Configure** button (runs `2nb setup --client …`, backup-safe) so you can wire up any assistant at a glance
- **AI connection testing** — one-click probe of your configured embedding and generation models with live latency
- **Incremental re-embed** — `2nb index` rebuilds embeddings only for documents whose content hash changed
- **Git integration (read-only)** — Recent Activity panel with per-commit file diffs in the dashboard, plus MCP git tools for AI clients
- **Skill files** — One command to teach 8 AI coding agents about your vault
- **Three AI providers** — AWS Bedrock, OpenRouter, Ollama (fully local)
- **Schema validation** — Typed frontmatter, enum constraints, status state machines
- **Wikilinks** — `[[target#heading|alias]]` with link resolution and graph traversal
- **Broken-link resolution, no dead ends** — `2nb lint` and the dashboard's Validation tab don't just flag broken `[[wikilinks]]`; every finding is resolvable: repair case/separator drift to the right note, pick a "did you mean?" suggestion (`suggest-target` → `relink`), create the missing note, or `unlink` it (keeping the text) — with a one-click bulk **Repair drift links** for a whole batch
- **Document templates** — ADR, runbook, prd, prfaq, postmortem, note with enforced schemas
- **Native macOS dashboard** — SwiftUI + AppKit companion app for vault health, AI configuration, plugin install, MCP monitoring, and git activity; Obsidian remains the editor
- **Local-first** — All data on disk as plain markdown in your Obsidian vault. `2nb` writes only a gitignored `.2ndbrain/` sidecar and never rewrites a note's body except via explicit, user-invoked commands (`append`, `prepend`, `replace`); frontmatter edits via `meta` have always rewritten files in place.

## AI Providers

2ndbrain supports three AI providers for embeddings and generation. Bedrock uses the [Converse API](https://docs.aws.amazon.com/bedrock/latest/userguide/conversation-inference-call.html), so any Bedrock model works — Claude, Nova, Llama, Mistral, and more.

| Provider | Embeddings | Generation | Setup |
|----------|-----------|------------|-------|
| **AWS Bedrock** | Nova Embeddings v2 | Nova Micro, Claude, Llama, any model | Uses existing AWS SSO — zero new keys |
| **OpenRouter** | Nemotron Embed (free) | Gemma 4 31B (free), GPT-4o, Claude, etc. | `OPENROUTER_API_KEY` env var |
| **Ollama** | nomic-embed-text | qwen2.5, gemma3, llama3 | `brew install ollama` — fully local |
| **llama-local** _(experimental, CLI-only)_ | EmbeddingGemma 300M | Gemma 4 E2B / E4B | fully offline via llama.cpp; needs `llama-server` on PATH (`brew install llama.cpp`) since the engine isn't bundled yet. Hidden in the app until it is. |

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
2nb models wizard --set-active    # also write the chosen models into the vault config

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

**Global flags:** `--json`, `--csv`, `--yaml`, `--format` (json/csv/tsv/yaml/raw/md/text; listings also `paths`/`tree`), `--porcelain`, `--vault`, `--unconfigured` (permit a write to a vault Obsidian doesn't know — without it such a write is refused), `--copy` (also copy output to the clipboard), `--verbose` (`-v` for debug logging to stderr and `.2ndbrain/logs/cli.log`)

### Getting Started

| Command | Description |
|---------|-------------|
| `vault` | Health report for the active vault (same as `vault status`) |
| `vault create <path>` | Initialize a new vault (open it in Obsidian to use it; 2nb follows your open Obsidian vault) |
| `vault set <path>` | Register an existing vault in recents (the active vault follows Obsidian) |
| `vault list` | List recently used vaults |
| `vault show` | Terse summary: path, source, name, doc count |
| `init [path]` | Deprecated alias for `vault create` |

### Documents

| Command | Description |
|---------|-------------|
| `create <title> [--type adr\|runbook\|prd\|prfaq\|postmortem\|note] [--path <subdir>] [--content <body>] [--overwrite\|--append]` | Create document from template. `--path` files it under a vault-relative subdirectory (created if missing); default is the vault root. `--content` sets the initial body instead of the type template. `--overwrite` replaces an existing same-title note in place (keeps its id); `--append` appends to it (else creates) |
| `read <path> [--chunk <heading>]` | Read document or specific section. Alias: `print` |
| `append <path> [--text \| --file \| stdin]` | Append content to a document's body. Explicit, opt-in body write; frontmatter is left untouched |
| `prepend <path> [--text \| --file \| stdin]` | Insert content at the start of a document's body, after the frontmatter |
| `replace <path> [--section <heading>] [--text \| --file \| stdin]` | Replace the whole body, or just one heading's section content with `--section` (first match wins on duplicate headings) |
| `daily` | Resolve today's daily note from Obsidian's daily-notes config (`.obsidian/daily-notes.json`), create it if missing, and print the path (`daily path` is the explicit subcommand form). `daily read` prints its body; `daily append`/`daily prepend` `[--text \| --file \| stdin]` add to it. Falls back to Obsidian defaults (root folder, `YYYY-MM-DD`) when the plugin is disabled or unconfigured; the date format honors Moment `[literal]` escaping |
| `meta <path> [--set key=value] [--get <key>] [--remove <key>]` | View frontmatter, or `--set` to write, `--get` to read one field (exit 1 if absent), `--remove` to delete a field in place (preserves comments/order; refuses identity and schema-required keys). Array fields (`tags`, `aliases`, schema `list`/`tags` fields) are coerced to a YAML list, comma-split, replace semantics (`--set tags=a,b`); use `tag add`/`tag remove` for incremental edits. Aliases: `frontmatter`, `fm`, `properties` |
| `delete <path> [--force]` | Delete document from vault and index |
| `move <src> <dst> [--dry-run] [--force]` | Move/rename a note to a new vault-relative path, rewriting every `[[wikilink]]` AND markdown-style `[text](path.md)` link across the vault that points at it (preserving heading/block/alias/embed suffixes on wikilinks, the label text + `#anchor`/`?query` suffix + `.md` extension on markdown links, and the bare-vs-path form; external-URL and anchor-only markdown links are skipped; links inside code are untouched). `--dry-run` previews the rename, per-note rewrites, and ambiguous skips without writing; without `--force` a bare-name-ambiguous move is refused. The target file moves LAST for crash safety |
| `rename <src> <newname> [--dry-run] [--force]` | Rename a note in place (same folder, `.md` appended if omitted), delegating to `move` |
| `list [--type] [--status] [--tag] [--sort]` | List documents with filters |
| `tasks [--done] [--todo] [--path <file\|dir>]` | List GFM checkbox tasks (`- [ ]` / `- [x]`) across the vault. v1 is GFM open/done only (custom statuses like `[>]`/`[-]` are ignored) |
| `task <path> <line> [--done \| --todo \| --toggle]` | Toggle a single GFM checkbox at a 1-based body line (default toggle). Errors if the line is not a checkbox; frontmatter is left untouched |

### Search & AI

| Command | Description |
|---------|-------------|
| `search <query> [--type] [--status] [--tag] [--threshold]` | Hybrid BM25 + semantic search (shows `rrf` + raw `cos` scores) |
| `ask <question> [--history <path\|->]` | RAG Q&A with source citations; `--history` makes it multi-turn |
| `chat` | Interactive multi-turn Q&A session (REPL over the same pipeline) |
| `suggest-links <path> [--limit 10]` | Rank semantically related documents for wikilink insertion |
| `polish <path> [--system <prompt>] [--write] [--links] [--repair-links] [--undo] [--force]` | AI copy-edit a document (JSON with original + polished body). `--links` adds grounded `[[wikilinks]]` to existing notes (never invents a target). `--repair-links` repairs broken `[[wikilinks]]` to existing notes (case, separator (hyphen/underscore vs space), whitespace, and alias drift; ambiguous or unmatched targets reported, never guessed). `--write` applies the polished body in place (opt-in; default is preview only) after snapshotting the original; `--undo` reverts that snapshot (refusing if the file changed since, unless `--force`) |
| `index [--doc <path>] [--force-reembed]` | Build search index + embeddings (full vault or a single document); `--force-reembed` invalidates every stored embedding for after an intentional provider switch |
| `ai status` | Show AI provider, models, embedding count, and vault portability state |
| `ai setup` | Multi-provider setup wizard (easy mode or custom); a model that passes its probe is saved to the user catalog as `user_verified` |
| `ai local` | Check local AI readiness (Ollama, disk, RAM, models) |
| `ai embed <text>` | Generate embedding vector (debug) |
| `models list [--discover] [--status] [--provider] [--promote] [--enabled-only] [--recommended]` | Verified model catalog + user catalog + vendor discovery; `--discover --promote` tests unverified models concurrently and adds those that pass; `--enabled-only` filters out user-disabled models (dropdowns use this); `--recommended` shows only the curated short list, and the STATE column marks curation (★) plus each model's last test outcome |
| `models test <model-id> [--save] [--scope global\|vault]` | Smoke-test any model (embed or generate probe); failures are classified (`access_denied`, `bad_credentials`, `throttled`, ...) with a fix hint; `--save` records the result in your catalog, pass or fail |
| `models verify [ids...] [--provider] [--vendor] [--recommended] [--all] [--yes]` | Batch-probe models to check YOUR account can invoke them (AWS can gate newer frontier models per account even when the console shows access). Cost-gated; every result is recorded so `models list` and `ai status` reflect your real access |
| `models add <id> --provider --type [--scope global\|vault] [--price-in --price-out --dimensions --context-length --name --notes]` | Add a model to your user catalog (per-vault by default, or global with `--scope global`) |
| `models remove <id> --provider [--scope global\|vault]` | Remove a model from your user catalog |
| `models enable [id] --provider [--vendor <name>] [--scope global\|vault]` | Mark a model enabled so it appears in dropdowns; `--vendor` toggles every model from that vendor (the GUI's bulk toggle) |
| `models disable [id] --provider [--vendor <name>] [--scope global\|vault]` | Hide a model from dropdowns; still listed by bare `models list`; `--vendor` for the bulk toggle |
| `models enable-state <id> --state default\|enabled\|disabled` | Tri-state enable pointer; `default` clears the override for tier defaults (used by the GUI Enable State menu) |
| `models cost-preview [ids...] --probe <kind> [--provider] [--all]` | Estimate USD cost of running a probe (test / bench_embed / bench_gen / bench_rag / retrieval) across one or more models before committing |
| `models calibrate [--samples] [--save] [--scope] [--seed]` | Sample the vault's baseline cosine distribution (p50/p90/p95/p99) and recommend a similarity threshold; `--save` persists it to the user catalog |
| `models wizard [--scope] [--provider] [--skip-discover] [--cost-cap] [--json] [--set-active]` | Interactive discover → pick → cost preview → test → save flow; `--json` emits an event stream for GUI / automation; `--set-active` also writes the chosen embedding + generation models into the vault config (same write path as `config set`) |
| `models bench` | Benchmark favorites with persistent history |
| `models bench fav <model-id>` / `unfav <model-id>` / `favs` | Add / remove / list benchmark favorites |
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
| `unresolved` | List every broken wikilink across the vault (source doc + the raw `[[target]]` that resolves to no note) |
| `graph <path>` | Output link graph as JSON |
| `lint [glob]` | Validate schemas, check broken wikilinks |
| `repair-links <path> [--target <raw>...] [--write]` | Deterministically repair broken `[[wikilinks]]` to existing notes (AI-free) — a broken target is canonicalized only when its normalized form (lower-cased, with hyphen/underscore folded to space and whitespace collapsed) maps to exactly one note via basename/title/alias, so a spaced `[[Claude Code Skills Reference and Index]]` matches the kebab `claude-code-skills-reference-and-index.md`; ambiguous/unmatched targets are reported, never guessed. `--target` scopes to specific authored targets; `--write` applies + snapshots (revert with `polish <path> --undo`) |
| `suggest-target <target> [--limit 6] [--source <path>]` | Ranked "did you mean?" existing notes a broken `[[wikilink]]` target might have meant, from three tiers best-first: drift (the `repair-links` fuzzy index), semantic (embeddings, when configured), and BM25 keyword. `--source` names the note containing the broken link and excludes it from candidates (a note is never a fix for its own broken link). Read-only; emits `[]` not null. Pair with `relink` to apply a pick |
| `relink <path> --from <target> --to <note> [--write]` | Repoint a broken `[[wikilink]]` to a chosen existing note (preserves `#heading`/`#^block`/`\|alias`); EXACT match scoped to `--from`. `--write` applies + snapshots (revert with `polish <path> --undo`) |
| `unlink <path> --target <target> [--write]` | Remove a broken `[[wikilink]]`, keeping its visible text (`[[083477d]]` → `083477d`, `[[X\|alias]]` → `alias`); embeds and links in code are never touched. `--write` applies + snapshots (revert with `polish <path> --undo`) |
| `stale --since <days>` | Find stale documents |
| `outline <path>` | Heading tree of a document (heading path, level, line span) |
| `wordcount <path>` | Word, character, and heading counts over the indexable body (alias `wc`) |
| `folders` | List folders with document counts (root docs under `(root)`) |
| `tags` | List all tags vault-wide with counts |
| `tags rename <old> <new> [--dry-run]` | Rename a frontmatter tag across every document that carries it (frontmatter-only in v1; dedupes when `<new>` already present; `--dry-run` previews) |
| `tag add <note> <tag>...` | Add one or more frontmatter tags to a single note (per-note counterpart to `tags`). Merges + dedupes, schema-validates, and reindexes so the tag is immediately `list --tag`-searchable. Tags may be separate args or comma-separated; resolves the note via `file=`/`path=`/bare; rejects read-only `.canvas`/`.base` |
| `tag remove <note> <tag>...` | Remove one or more frontmatter tags from a single note (no-op if absent); same resolution and reindex as `tag add` |
| `aliases` | List frontmatter aliases mapped to their document |

### Integration

| Command | Description |
|---------|-------------|
| `mcp-server` | Start MCP server on stdio |
| `mcp-setup` | Show MCP setup instructions for all AI tools |
| `mcp status [--json]` | List live MCP server processes and their recent tool invocations |
| `mcp configured [--client <name>] [--all] [--json]` | Report whether the 2ndbrain MCP server is configured in the AI client config for this vault. Defaults to Claude Code; `--all` reports every client. The durable "is it set up?" check, unlike `mcp status` which reports "is it running right now?" |
| `setup [--all \| --client <name>]` | One-command setup: install the skill, the MCP server, **and** the global-instructions block for an AI client (claude-code, claude-desktop, warp, agents, codex) or all of them — idempotent and backup-safe |
| `instructions install/configured/uninstall [--client <name> \| --all]` | Manage a small managed "2ndbrain" reference block in an AI client's **global memory file** (`~/.claude/CLAUDE.md`), the always-loaded lightweight complement to the installable skill. Sentinel-delimited and version/sha-stamped, so it updates in place, is idempotent, preserves surrounding content, and removes cleanly; `install` refuses a hand-edited block without `--force`. Supported clients: claude-code, claude-desktop. Also run by `2nb setup` |
| `mcp install [--client <name> \| --all]` / `mcp uninstall` | Write/remove the 2ndbrain MCP server entry in a client config (claude-code → `~/.claude.json`, warp → `~/.warp/.mcp.json`, agents → `~/.agents/.mcp.json`, claude-desktop → its JSON, codex → via `codex mcp add`). Backs up the file first and preserves your other servers |
| `plugin status [--json]` | Installed Obsidian plugin version vs this CLI |
| `plugin install` | Install or update the Obsidian plugin in the open vault from the latest release (alias: `plugin update`). Won't downgrade a newer installed plugin unless `--force` |
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
| `config get <key>` | Get a config value (e.g., `ai.provider`, `ai.similarity_threshold`). `--effective` on `ai.similarity_threshold` resolves the full chain (vault > calibration > model > default) instead of the raw stored value |
| `config set <key> <value>` | Set a config value |
| `config set-key <provider>` | Store API key in macOS Keychain |
| `config doctor` | Diagnose AI-config problems (provider known/enabled, no orphaned model slot, `ai.dimensions` matches the model, DB embeddings match the selection, threshold resolves) with one-line fix hints. Config defects fail (exit 2); an unreachable provider is a non-failing warning, so it stays usable offline/in CI |
| `doctor` (alias `verify`) | Verify all three products — CLI, macOS app, Obsidian plugin — are installed and in sync with the latest release, with the exact fix command for any gap. The plugin is read from the open vault (or `--vault`); `--json` emits a `SuiteStatus` with a `ProductState` per component. Functional readiness stays in `config`/`mcp`/`skills doctor` |
| `update` | Check whether a newer release is available; lists every component (CLI, app, plugin) that is behind the latest release. Offline-safe (24h cache), refetched when it's behind an install so a just-released version isn't reported stale; a component never shows a "latest" below its own version. `--json` adds `app`/`plugin` states |

All commands support `--json`, `--yaml`, `--csv`, `--tsv` for machine-readable output, plus `--format raw`/`md` to emit a document body (or any `Serialize()`-able value) verbatim, `--format text` for plain text, and (on listings) `--format paths`/`tree` and `--total`. `--copy` also writes a command's rendered output to the clipboard (macOS `pbcopy`): `read`/`print`, `meta --get`, and `daily` copy in their default output; any command run with a machine format copies that output.

**Obsidian-CLI syntax compatibility.** `2nb` accepts `obsidian`-CLI-style invocations as a drop-in scripting replacement for the headless file/markdown layer. The full mapping table, accepted argument forms, and intentional non-goals live in [docs/obsidian-cli-mapping.md](docs/obsidian-cli-mapping.md). It understands `key=value` arguments (`file=`, `path=`, `content=`, `template=`, `query=`, `vault=`, `old=`/`new=`, etc.), boolean tokens (`total`, `append`, `overwrite`, `done`/`todo`), colon-commands (`daily:path`/`daily:append`, `property:set` → `meta`, `tags:rename` → `tags rename`, `tag:add`/`tag:remove` → `tag add`/`tag remove`, `link:unresolved`), and command aliases (`print` → `read`; `fm`/`frontmatter`/`properties` → `meta`; `files` → `list`; `search-content` → keyword search; `list-vaults`/`set-default-vault`/`add-vault` → the `vault` subcommands). `file=` resolves a target by exact path, then basename/title/alias/shortest-unique suffix (failing loudly on ambiguity); `path=` is a strict exact path. A free-text `search`/`ask` query that contains `=` is preserved (never parsed as a parameter), and an unrecognized `key=value` passes through unchanged.

### Defaults and search scoring

- **Parent-command defaults**: running a command group without a subcommand invokes its most-useful read-only action: `2nb ai` → `ai status`, `2nb models` → `models list`, `2nb git` → `git status`, `2nb mcp` → `mcp status`, `2nb plugin` → `plugin status`, `2nb skills` → `skills list`, `2nb config` → `config show`. `--help` still works on every command.
- **Similarity threshold** — hybrid search drops vector hits whose cosine similarity is below the active threshold so barely-related neighbors stop padding result lists. Resolution order: explicit vault config (`2nb config set ai.similarity_threshold 0.25`) > user calibration saved by `2nb models calibrate --save` > per-model recommendation from the builtin catalog > global default `0.20`. Builtin recommendations: Nova-2 `0.25` (measured; queries embed with Nova's asymmetric `GENERIC_RETRIEVAL` purpose, which collapses the cosine scale — see CLAUDE.md), Nemotron `0.60`, nomic-embed-text/Titan-v2/Cohere-embed `0.50`, mxbai/snowflake/bge-m3 `0.55`, all-minilm `0.35` (all estimated from each model's training objective — run `2nb models calibrate` to tune for your vault). Override per-query with `2nb search "foo" --threshold 0.35`. `2nb ai status` shows the active value and which tier supplied it.
- **Calibration** — `2nb models calibrate` samples random chunk pairs from your vault, reports the noise-floor cosine distribution (p50/p90/p95/p99), and recommends a threshold. Add `--save` to persist it to the per-vault user catalog (or `--save --scope global` for all vaults).
- **Score display** — `2nb search` now shows `(rrf=X.XXX, cos=Y.YYY)` on each result. The `rrf` is the Reciprocal Rank Fusion score used for ranking; `cos` is the raw cosine similarity from the vector channel, which is what you actually want to look at when judging whether a result is relevant. If legitimate matches are being cut, lower the threshold; if noise is slipping through, raise it.
- **Hybrid weighting** — `2nb config set ai.vector_weight 1.5` (or `ai.bm25_weight`) biases the RRF fusion toward semantic or keyword recall. Both default to `1.0` (equal-weight RRF); raise `ai.vector_weight` to lean on the vector channel, `ai.bm25_weight` for exact-term-heavy vaults.
- **RAG context** — `ask` feeds the **full matching note(s)** to the model (parent-document retrieval), so an answer in a section deep in a long note isn't truncated away; a note is only windowed around the matched section when it exceeds the budget. Tune with `2nb config set ai.rag_context_budget <runes>` (total, default 60000) / `ai.rag_note_budget` (per note, default 20000).

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

The MCP server exposes 22 tools for AI coding assistants:

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
| `kb_structure` | Get document heading tree as JSON (also covers the outline view) |
| `kb_backlinks` | Resolved inbound links to a document |
| `kb_links` | Outbound links from a document, including unresolved/broken ones |
| `kb_tags` | Vault-wide tag list with per-tag document counts |
| `kb_tasks` | GFM checkbox tasks across the vault or a file/dir, with `done`/`todo` filters |
| `kb_delete` | Delete document from vault and index |
| `kb_index` | Rebuild search index and generate embeddings |
| `kb_append` | Append text to a document body, then reindex + re-embed (rejects read-only `.canvas`/`.base`) |
| `kb_replace_section` | Replace one heading's section content, then reindex + re-embed (rejects read-only `.canvas`/`.base`) |
| `kb_suggest_links` | Suggest semantically related documents to wikilink from a source doc |
| `kb_polish` | AI copy-editor returns original + polished body for diff review; `links:true` also adds grounded wikilinks to existing notes (never invents a target) |
| `kb_git_activity` | Recent git commits that touched vault files (read-only) |
| `kb_git_diff` | Unified diff of a file against HEAD |
| `kb_git_status` | Uncommitted/untracked files in the vault |

`move`/`rename` (the wikilink-rewriting vault mutation) is intentionally CLI-only: the highest-blast-radius write stays behind `2nb move`/`2nb rename` with their mandatory `--dry-run` preview rather than an MCP tool.

Each running `2nb mcp-server` writes a sidecar status file to `.2ndbrain/mcp/<pid>.json` with PID, start time, parent PID, and the last 50 tool invocations. Run `2nb mcp status` to list live servers, or use the Cmd+Shift+M status panel in the dashboard.

**The easy way:** `2nb setup --client claude-code` (or `claude-desktop`, `warp`, `codex`, or `--all`) installs the skill and writes the MCP config for you, backing up any existing file and preserving your other servers. `2nb mcp install --client <name>` writes just the MCP entry. The manual snippets below are a fallback.

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

`2nb setup --client claude-desktop` (or `mcp install --client claude-desktop`) writes this for you. Claude Desktop is a GUI app launched with a minimal PATH, so the command must be an **absolute** `2nb` path and must NOT include a `cwd` or `url` field (a `url` field silently corrupts the file). Restart Claude Desktop to apply.

Add to `~/Library/Application Support/Claude/claude_desktop_config.json`:

```json
{
  "mcpServers": {
    "2ndbrain": {
      "command": "/opt/homebrew/bin/2nb",
      "args": ["mcp-server", "--vault", "/path/to/your/vault"]
    }
  }
}
```

### Codex

`2nb mcp install --client codex` (or `2nb setup --client codex`) runs `codex mcp add` so Codex manages its own `~/.codex/config.toml` — no manual edit. If the `codex` CLI isn't installed, the command prints the exact `codex mcp add` line and the `[mcp_servers.2ndbrain]` TOML to paste.

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

Supported agents: Claude Code (also serves Claude Desktop, which reads the same `~/.claude/skills`), Cursor, Windsurf, GitHub Copilot, Kiro, Cline, Roo Code, JetBrains Junie, Warp, Codex, and the cross-tool `agents` (`.agents/skills/`) standard. `2nb setup --client <name>` installs the skill **and** the MCP server in one step; a force-overwrite of an existing skill backs it up first.

## macOS Dashboard

A native SwiftUI + AppKit configuration and companion app. It is **not an editor**: Obsidian stays your editor. The dashboard binds to the vault Obsidian currently has open (read from Obsidian's own registry) and gives you one place to manage everything around it.

**Home** (the default screen) covers the common cases:

- **Vault card**: active vault name and path, a badge confirming it matches the vault Obsidian has open, and an Obsidian plugin row showing the installed plugin version with an Install/Update button (runs `2nb plugin install`)
- **AI card**: provider and models (AWS Bedrock with Claude Haiku 4.5 + Amazon Nova-2 by default) with a readiness dot, Save-as-default, and Test buttons
- **AI Clients card**: per-client rows (Claude Code, Warp, Claude Desktop, Codex) showing skill-installed, MCP-configured, and (for Claude Code / Claude Desktop) global-instructions status, each with a one-click **Configure** button (runs `2nb setup --client …`, backup-safe; Codex without its CLI shows the manual `codex mcp add` step instead of a false success)
- **Index card**: document and embedding counts, a "N notes awaiting embedding" hint, and Sync (incremental, embeds only what changed) and Re-embed All buttons. Notes edited in Obsidian sync automatically
- The app bundles its own version-matched `2nb` CLI and prefers it, so its AI/indexing calls never run a stale Homebrew copy. An orange banner only appears on dev builds that fall back to an older PATH `2nb`; when Homebrew is present it offers an Update CLI button that runs `brew upgrade apresai/tap/twonb` to refresh the terminal/plugin's copy

**Advanced** tabs for the power-user depth:

- **Vault Status**: unified health (vault info, index coverage, embedding portability, AI reachability, stale docs)
- **AI Settings**: the AI Hub (Cmd+Shift+,) with provider cards, active model slots, and the full model catalog with per-model test, benchmark, and enable/disable
- **MCP Server** (Cmd+Shift+M): live MCP server processes and recent tool invocations
- **Git Integration** (Cmd+Shift+G): recent commits with a 1/3/7/30-day window; click a commit for per-file diffs
- **Validation**: `2nb lint` findings rendered with file and line detail
- **Updates**: app, CLI, and Obsidian-plugin versions against the latest release (`2nb update`), with one-click upgrades for the CLI and plugin

Menus: **Vault** (New Vault, Open Vault Cmd+Shift+O, Reveal in Finder, Vault Status, Sync Index, Validate Vault, Import/Export Obsidian), **View** (Recent Activity Cmd+Shift+G), and **AI** (AI Hub, MCP Server Configuration, MCP Server Status).

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

Requires Go 1.26+ (the CLI is pure-Go, no CGO); macOS 14+ for the Swift app.

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

`make release-app` then runs on the maintainer's machine: it builds SecondBrain.app, signs it with a Developer ID certificate (hardened runtime), notarizes via Apple `notarytool`, and staples the ticket; then builds a branded drag-to-Applications `SecondBrain-<version>-arm64.dmg` (`scripts/make-dmg.sh`, via `create-dmg`) and Developer ID-signs, notarizes, and staples the **DMG too** (stapling both means the app launches offline even after being dragged out of the image, and the downloaded `.dmg` itself passes Gatekeeper offline); finally it uploads the DMG to the release and updates the cask in the tap with the new version and sha256. Signing config is read from `scripts/sign.env` (gitignored; template at `scripts/sign.env.example`); building the DMG needs `create-dmg` (`brew install create-dmg`).

Users install with:

```bash
brew install apresai/tap/2nb                    # CLI
brew install --cask apresai/tap/secondbrain     # macOS dashboard app (depends on the CLI formula)
```

To build locally without GitHub Actions:

```bash
make release-local       # runs goreleaser locally (CLI only)
make package-app         # builds a branded SecondBrain .dmg locally (Developer ID-signed if sign.env present, else ad-hoc; local use only)
```

## Architecture

```
cli/        Go CLI + MCP server (cobra, modernc.org/sqlite, mcp-go, aws-sdk-go-v2)
app/        Swift macOS dashboard (SwiftUI, GRDB.swift, swift-markdown)
plugins/    Obsidian plugin (thin wrapper that shells out to 2nb)
             |                    |
             +-------- shared ----+
                  .2ndbrain/index.db (SQLite WAL)
```

The CLI and dashboard share the same SQLite database via WAL mode for concurrent access. All AI operations go through the provider interfaces in `cli/internal/ai/`.

## License

[MIT](LICENSE) - Copyright (c) 2026 Apres AI
