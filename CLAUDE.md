# 2ndbrain

Obsidian-native AI companion. **Obsidian stays your editor**; the Go CLI (`2nb`) + MCP server are the engine that indexes, searches, and answers (RAG) over a real Obsidian vault. A thin Obsidian plugin and a macOS configuration dashboard wrap the CLI. `2nb` writes only a gitignored `.2ndbrain/` sidecar and never rewrites a note's body except via explicit, user-invoked commands (`append`, `prepend`, `replace`, and `polish --write`); frontmatter edits via `meta` have always rewritten files in place. `polish --write` additionally records a pre-write snapshot under the gitignored `.2ndbrain/recovery/polish/` so the AI edit can be reverted with `polish --undo` (a whole-file restore that refuses to clobber post-polish edits without `--force`). The one command that mutates OTHER notes is `move`/`rename` (the strongest write surface): it rewrites every `[[wikilink]]` AND markdown-style `[text](path.md)` link across the vault that points at the moved note so links stay valid. It is gated by a mandatory `--dry-run` preview, a crash-safe ordering (the target file is moved LAST, after referencing notes are rewritten, so an interruption leaves links pointing at the still-present old name), and an ambiguity guard (a non-`--force` move is refused when a bare `[[name]]` link could point at more than one note; `--force` rewrites only the unambiguous path-qualified links and leaves the bare ones). (One further explicit, user-invoked exception: `2nb plugin install` writes the plugin bundle under `.obsidian/plugins/obsidian-2ndbrain/`; never notes, never Obsidian settings.)

## Repository Layout

- `cli/` — Go CLI binary (`2nb`) + MCP server (the engine)
- `app/` — Swift macOS configuration & companion dashboard, **not an editor** (SwiftUI + AppKit)
- `plugins/obsidian-2ndbrain/` — thin Obsidian plugin that shells out to `2nb`
- `reqs.md` — EARS-format requirements specification
- `press-release.md` — Product vision document
- `test-plan.md` — Requirements validation test plan

### Self-hosted agent skill

The canonical `2nb` agent skill lives in this repo, so any agent (Warp, Claude Code, Cursor, ...) opened on the repo loads it with zero install. Source of truth: `cli/internal/skills/content/2ndbrain-skill.md` (Go-`embed`ed into the CLI; `2nb skills install`/`show` render it). For walk-up discovery it is mirrored to the repo-root paths agents look for: `.agents/skills/2nb/SKILL.md` (Warp's recommended primary), `.warp/skills/2nb/SKILL.md`, and `.claude/skills/2nb/SKILL.md` (the last is tracked via a `.gitignore` carve-out, since `.claude/` is otherwise ignored). Edit the source file, then run `make sync-skills` to regenerate the mirrors; `make check-skills-sync` runs in release CI and fails on any drift. Never edit a mirror directly.

### Project docs (`docs/`)

- [`quick-start.md`](docs/quick-start.md): end-to-end getting-started walkthrough (install, vault, AI, index, search, MCP)
- [`agent-teaching.md`](docs/agent-teaching.md) — MCP vs CLI decision matrix + test battery design
- [`obsidian-cli-mapping.md`](docs/obsidian-cli-mapping.md): Obsidian-CLI compatibility (command mapping table, accepted argument forms, intentional non-goals)
- [`mcp-integration.md`](docs/mcp-integration.md) — MCP setup snippets for Claude Code, Cursor, and other clients
- [`templates.md`](docs/templates.md) — Built-in document type templates (adr, runbook, prd, prfaq, note, postmortem)
- [`polish-prompt-eval.md`](docs/polish-prompt-eval.md) — How the `polish` copy-edit system prompt was chosen (LLM-as-judge), with the reproducible harness and rationale
- [`vault-structure.md`](docs/vault-structure.md) — On-disk vault layout reference (Superseded for 0.5.0, see [docs/obsidian/README.md](docs/obsidian/README.md))
- [`obsidian/README.md`](docs/obsidian/README.md) — Obsidian-native pivot documentation and architectural model

## Versioning

Format: `major.minor.build`. Single source of truth: `VERSION` file at repo root. The Go CLI reads it via `cli/Makefile` LDFLAGS into `internal/cli.Version`; the Swift app generates `app/Sources/SecondBrain/Version.swift` (never edit by hand); the Obsidian plugin's `manifest.json`/`package.json`/`package-lock.json` are synced from it by `make version-plugin` (aligned from 0.8.0 onward; release CI fails if the manifest drifts from `VERSION`, and the sync refuses to lower the plugin version since Obsidian/BRAT only see increases as updates).

Bump targets (root `Makefile`): `make bump-build` (`0.1.0` → `0.1.1`), `make bump-minor` (`0.1.1` → `0.2.0`), `make bump-major` (`0.2.0` → `1.0.0`). Each regenerates `Version.swift` and the plugin version files. `make set-version V=x.y.z` sets an explicit version across all products (used for the one-time 0.8.0 alignment jump).

## Release

Both the CLI and the macOS app are published via Homebrew:

```bash
brew install apresai/tap/2nb                    # CLI only
brew install --cask apresai/tap/secondbrain     # macOS dashboard app (depends on CLI)
```

The machine-readable release contract lives in [`.release.yaml`](.release.yaml) at the repo root: the front-door command, every product, and each product's install + verify command. It is what the `oss-release` skill reads to release and verify this repo (the skill encodes the invariants; `.release.yaml` encodes this project's implementation), so a packaging change updates the Makefile (and `.release.yaml` only if a channel or command changes), never the skill. Keep it in sync with the pipeline below.

### Pipeline

**`make release-all`** is the front door: one command (canonical clone only; needs gitignored `scripts/sign.env`) that runs the test gate, bumps (`BUMP=build|minor|major|none`), tags, **waits for CI**, then signs/notarizes/publishes the app + cask, and verifies every product shipped at one version (`scripts/release-all.sh`). The underlying two steps remain available individually:

A release is **two steps**: CI ships the CLI + plugin; the macOS app is signed, notarized, and published from the maintainer's machine (signing keys never leave it / never enter CI).

1. `make bump-build` (or `bump-minor`/`bump-major`) — increment `VERSION`, regenerate `Version.swift`, sync the plugin version files.
2. `make release` — updates `CHANGELOG.md`, commits, tags `v<VERSION>`, pushes tag.
3. GitHub Actions (`.github/workflows/release.yml`) on tag push: macos-latest arm64, CGO_ENABLED=1; GoReleaser builds CLI for arm64+x86_64 and pushes formula `twonb.rb` to `apresai/homebrew-tap`; builds + uploads the Obsidian plugin assets; maintains the `2nb` formula alias. **CI does NOT build the macOS app or the cask.**
4. `make release-app` — **local, after the CI release exists.** Runs `scripts/release-app-local.sh --publish`: builds `SecondBrain.app` (which bundles the freshly-built, version-matched `2nb` CLI at `Contents/Resources/2nb` via the `build-app-release` -> `build-cli` dependency; the script fails fast if the bundled `2nb --version` ≠ `VERSION`), Developer ID-signs the **nested `2nb` binary first** then the app (hardened runtime; the outer sign is not `--deep`, so the nested binary must be signed inside-out or notarization rejects it), gates on **portable load commands** (fails the release if the app exe or bundled `2nb` carries a dangling `LC_RPATH`/`LC_LOAD_DYLIB` that would resolve on the build Mac but not a clean one — `swift build` bakes in an absolute Xcode-toolchain `LC_RPATH`, the documented SPM Gatekeeper footgun, which `build-app-release` strips before signing) plus the bundled `2nb`'s hardened-runtime flag, Apple-notarizes via `notarytool` + staples, then builds a branded drag-to-Applications **`SecondBrain-<VERSION>-arm64.dmg`** (`scripts/make-dmg.sh`, via `create-dmg`), Developer ID-signs + notarizes + staples the **DMG too** (both app and DMG are stapled — Apple distribution best practice, so the app launches offline even after being dragged out and the downloaded `.dmg` passes Gatekeeper offline), uploads the DMG to release `v<VERSION>`, and updates the cask `secondbrain.rb` (version + sha256) in the tap. Signing config is read from `scripts/sign.env` (gitignored; template at `scripts/sign.env.example`); the private key stays in the keychain / cert store. Requires `create-dmg` (`brew install create-dmg`).
5. `make release-local` — local CLI-only release (no app, no notarization).

Key files: `.goreleaser.yaml`, `.github/workflows/release.yml`, `scripts/release-app-local.sh`, `scripts/make-dmg.sh` (branded DMG builder, shared with `make package-app`), `app/Resources/dmg-background.{svg,png}` (the installer window art), `scripts/sign.env.example`, `casks/secondbrain.rb.tmpl` (with `CASK_VERSION`/`CASK_SHA256` tokens), `scripts/update-changelog.sh`, `CHANGELOG.md`.

The `apresai` GitHub environment provides `HOMEBREW_TAP_TOKEN` (PAT for `apresai/homebrew-tap`). No code-signing secrets live in CI — signing is local only.

The macOS app is distributed as an arm64 **Developer ID-signed, Apple-notarized** `.dmg` (a branded drag-to-Applications installer; the enclosed `.app` is itself signed, notarized, and stapled), so it launches with no Gatekeeper prompt — both as a direct download and when Homebrew's cask install quarantines it. **The app bundles its own version-matched `2nb` CLI** at `Contents/Resources/2nb` (signed + notarized with the app), and `CLIPath.resolve()` prefers it, so the app's AI/indexing/lint calls always run a CLI that matches the app, eliminating the "0.5.8 re-embed" drift where a cask upgrade bumped the app but left an older Homebrew `2nb`. **Bundled-CLI Gatekeeper caveat:** a standalone Mach-O **cannot carry its own stapled notarization ticket** (Apple limitation), so when an install quarantines the bundle (a browser download, or `brew install --cask`, which copies via `ditto` and propagates `com.apple.quarantine` to every nested file) the quarantined `2nb` would need an *online* notarization check when the app spawns it — and a failing/offline check makes Gatekeeper deny it with "Apple could not verify '2nb' is free of malware … Move to Trash", which breaks the whole app. To prevent this the app strips `com.apple.quarantine` from its bundled `2nb` at launch via `CLIPath.prepareBundledCLI()` (in `AppDelegate.applicationDidFinishLaunching`, before the first CLI spawn; safe for the signature, which excludes `com.apple.*` xattrs). Immediate manual unblock for an already-installed copy: `xattr -dr com.apple.quarantine /Applications/SecondBrain.app`. The cask still `depends_on formula: "apresai/tap/twonb"` so the **terminal and the Obsidian plugin** have a `2nb` on PATH (a cask upgrade does not bump that formula, so the Home banner still nudges to `brew upgrade` it — but the app itself no longer depends on it).

## Build

```bash
make build              # Both CLI and app (regenerates Version.swift)
make build-cli          # cli/bin/2nb only
make build-app          # macOS app
cd cli && make test     # All Go tests
cd cli && make install  # Install to /usr/local/bin/2nb
```

**Required:** `CGO_ENABLED=1` and `-tags fts5` for all Go compilation.

Launch the macOS app via `open` on the `.app` bundle — never run the raw binary directly (it won't register with the window server):

```bash
open app/.build/arm64-apple-macosx/debug/SecondBrain.app
```

## Testing

```bash
make test               # Go unit tests
make test-battery       # Golden-path E2E battery (cli/battery_test.go)
make test-usage         # Usage suite: MCP write->query index round-trips (internal/mcp) + a runnable end-to-end battery (real binary + real mcp-server over stdio). Catches index-consistency regressions (a write tool that skips reindex). AI steps skip without creds.
make test-swift         # Swift unit tests (JSON decoding, parsing, wizard logic)
make test-gui           # GUI tests via AppleScript + screencapture
make test-all           # Everything
make install            # Build + install CLI to /usr/local/bin + app to ~/Applications
```

### No Mock Tests Policy

**All tests MUST use real API endpoints — local or paid. Mocks (`httptest.NewServer`, fake responses, stub implementations) are NOT allowed.** Tests needing a provider call the real API and skip if credentials/services are unavailable. This applies to AI provider tests (Bedrock, OpenRouter, Ollama), MCP tests, and any future integration tests.

- Bedrock: real AWS credentials; skip if not configured
- OpenRouter: real `OPENROUTER_API_KEY`; skip if not set
- Ollama: real server at localhost:11434; skip if not running or model not pulled
- Pure logic tests (string classification, price parsing) that don't call any API are fine

### GUI Test Automation

GUI tests use AppleScript for app interaction and `screencapture` for verification. Run `make install` first (the app lands in `~/Applications`).

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
| `internal/polish` | Shared copy-edit engine: the LLM-judge-selected system prompt, grounded link-candidate gathering, invented-link stripper, deterministic broken-link repair (`RepairBrokenLinks`), and snapshot/undo primitives (imported by both `cli` and `mcp`) |
| `internal/graph` | Link graph BFS traversal |
| `internal/mcp` | MCP server with 22 tools + sidecar status files |
| `internal/git` | Read-only git wrappers (IsRepo, Activity, DiffFile, StatusFiles) |
| `internal/skills` | Skill file generation and agent registry |
| `internal/output` | JSON/CSV/TSV/YAML/raw/md/text formatters |
| `internal/bench` | Benchmark history DB (`bench.db`: favorites, runs) + probes |
| `internal/testutil` | Test helpers (NewTestVault, CreateAndIndex) |

Key types: `document.Document`, `store.DB`, `vault.Vault`, `search.Engine`, `graph.Graph`.

### CLI Commands (89)

Organized into groups: Getting Started, Documents, Search & AI, Quality, Integration, Import/Export, Configuration. Use `--help` on any command for full flag detail.

| Command | Purpose |
|---------|---------|
| `init` | **Deprecated alias** for `vault create` |
| `vault [path]` | Health report (same as `vault status`); legacy positional path acts like `vault set` |
| `vault status` | Unified health: vault info, index coverage, portability, AI reachability, stale docs |
| `vault show` | Terse summary (path, source, name, doc count); `--json` |
| `vault create <path>` | Initialize a new vault and record it in recents (replaces `init`). Does NOT make it active — 2nb follows the vault Obsidian has open, so open the new folder as a vault in Obsidian (or pass `--vault`) to use it |
| `vault set <path>` | Register an existing vault in recents (for `vault list`). 2nb's active vault follows Obsidian's open vault, so this does not switch the active vault — open it in Obsidian, or pass `--vault` |
| `vault list` | List recently used vaults; reads `~/.2ndbrain-vaults` |
| `vault checkpoint` | Collapse + truncate the index WAL (`PRAGMA wal_checkpoint` PASSIVE then TRUNCATE via `store.DB.Checkpoint`). SQLite's auto-checkpoint flushes but never truncates the `-wal` file, so a busy vault's `index.db-wal` can park at its high-water mark; this shrinks it. GUI-safe: an active reader makes TRUNCATE report `busy` rather than forcing it. `--json` → `{wal_bytes_before, wal_bytes_after, db_bytes, pages_total, pages_checkpointed, busy}` |
| `create` | Create document from template (`--type`, `--title`, `--path`, `--content`). `--path <subdir>` files the doc under a vault-relative subdirectory (created if missing); default is the vault root. `--content` sets the initial body instead of the type template. `--overwrite` replaces an existing same-title note in place (reusing its id, so the index stays consistent); `--append` appends the content to an existing same-title note (else creates). `--allow-duplicate` is the orthogonal content-hash guard. Default with neither flag keeps the collision-free `<slug>-1.md` dedupe |
| `read` | Read full document or specific section (`--chunk`). Alias: `print` |
| `append` | Append content to a document's body (`--text`, `--file`, or stdin). Explicit, opt-in body write; leaves frontmatter untouched |
| `prepend` | Insert content at the start of a document's body, after the frontmatter (`--text`, `--file`, or stdin) |
| `replace` | Replace a document's body, or just one heading's section content with `--section <heading>` (`--text`, `--file`, or stdin). First match wins on duplicate headings |
| `daily` | Resolve today's daily note from Obsidian's core daily-notes plugin config (`.obsidian/daily-notes.json`: folder, format, optional template). Bare `daily` resolves, creates the note if missing, and prints the vault-relative path. `daily path` is an explicit subcommand for the same resolve+print (for the obsidian `daily:path` form). `daily read` prints its body; `daily append`/`daily prepend` (`--text`, `--file`, or stdin) add to the body via the shared body-write path. Missing/disabled plugin falls back to Obsidian defaults (root folder, `YYYY-MM-DD`); never hard-errors. The date format honors Moment's `[literal]` bracket-escaping |
| `meta` | View or update frontmatter with schema validation. Aliases: `frontmatter`, `fm`, `properties`. `--set key=value` writes (array-typed fields like `tags`, `aliases`, or any schema `list`/`tags` field are coerced to a YAML list, comma-split, with replace semantics: `--set tags=a,b` becomes `[a, b]`, `--set tags=` clears; use `tag add`/`tag remove` for incremental edits); `--get <key>` reads one field (ExitNotFound if absent); `--remove <key>` (repeatable) deletes a field in place, preserving comments/order, and refuses identity keys (id/path/title/type) and schema-required fields. Writes re-index the whole file (chunks/tags/links via `IndexSingleFile`), so a frontmatter tag change is reflected in `list --tag` immediately; re-embedding stays gated on the body content hash, so a metadata-only edit does not re-embed |
| `index` | Rebuild index. `--doc <path>` for a single doc; `--force-reembed` invalidates every stored embedding |
| `search` | Hybrid BM25 + semantic. Filters: `--type --status --tag --limit`. `--threshold` overrides cosine cutoff. `--bm25-only` |
| `list` | List documents with filters (`--type --status --tag --limit --sort`). Alias: `files`. `--total` prints only the count; `--format paths` prints one vault-relative path per line; `--format tree` prints an indented directory hierarchy |
| `lint [glob]` | Validate schemas, check broken wikilinks |
| `stale` | List documents not modified within N days (`--since`) |
| `related` | Find related docs via link graph (`--depth`) |
| `backlinks <path>` | List resolved inbound links to a document: which docs link to it, with the source path/title and the link's heading/alias/raw form |
| `links <path>` | List outbound links from a document, including unresolved ones (each carries a `resolved` bool), so it doubles as a per-file broken-link view |
| `orphans` | List documents with no resolved inbound link (nothing in the vault links to them) |
| `deadends` | List documents with no resolved outbound link (they link to nothing real in the vault) |
| `unresolved` | List every unresolved (broken) wikilink across the vault: each source doc path paired with the raw `[[target]]` that resolves to no note. Vault-wide complement to `links <path>` (which is per-file). `--total` prints only the count |
| `repair-links <path>` | Deterministically repair broken `[[wikilinks]]` in a note — the AI-free sibling of `polish --repair-links` (runs `polish.RepairBrokenLinksFiltered`, no generation provider, works offline, never touches prose). A broken target is canonicalized only when its normalized form (lower-cased, with hyphen/underscore folded to space and whitespace collapsed) maps to exactly one note (basename/title/alias; the common case is case or separator drift, e.g. a spaced `[[Claude Code Skills Reference and Index]]` link matching the kebab `claude-code-skills-reference-and-index.md` basename); ambiguous/unmatched targets are reported, never guessed. `--target <raw>` (repeatable) scopes the repair to specific authored targets (the `T` from `broken wikilink: [[T]]`), so a per-finding GUI button fixes exactly the clicked link. Previews by default; `--write` applies in place and snapshots the original so `polish <path> --undo` reverts it (shared snapshot slot). Emits the `PolishResult` shape (`provider: "repair-links"`); rejects read-only `.canvas`/`.base` |
| `relink <path>` | Repoint a broken `[[wikilink]]` to a chosen EXISTING note: rewrites every link whose authored target equals `--from` to point at `--to` instead (via `document.RewriteWikiLinks`, preserving any `#heading`/`#^block`/`\|alias` suffix and the author's bare-vs-path form). The "apply a Did-you-mean suggestion" action, paired with `suggest-target`. EXACT (case/separator-sensitive) matching, so it only touches the named link. Previews by default; `--write` applies + snapshots (reversible via `polish <path> --undo`); emits the `PolishResult` shape (`provider: "relink"`); rejects read-only `.canvas`/`.base` |
| `unlink <path>` | Remove a broken `[[wikilink]]`, keeping its visible text (`document.UnlinkWikiLink`): `[[083477d]]` → `083477d`, `[[page\|the page]]` → `the page`, `[[note#Setup]]` → `note`. The "remove the link, keep the words" resolution for a target that names no real note (a stray id, abbreviation, external ref). EXACT (case/separator-sensitive) matching scoped to `--target`; embeds (`![[...]]`) and links inside code are never touched. Previews by default; `--write` applies + snapshots (reversible via `polish <path> --undo`); emits the `PolishResult` shape (`provider: "unlink"`, `new_target` empty); rejects read-only `.canvas`/`.base` |
| `graph` | Output link graph as JSON adjacency list |
| `outline <path>` | Heading tree of a document (heading path, level, line span). Shares `document.BuildOutline` with the MCP `kb_structure` tool |
| `wordcount <path>` | Word, character, and heading counts over the indexable body (comments stripped). Alias: `wc` |
| `tasks` | List GFM checkbox tasks (`- [ ]` / `- [x]`) across the vault. Filters: `--done`, `--todo`, `--path <file\|dir>`. `--total` prints only the count. v1 = GFM open/done only (custom statuses like `[>]`/`[-]` ignored). `--json` |
| `task <path> <line>` | Toggle a single GFM checkbox at a 1-based body line. `--done`/`--todo`/`--toggle` (default toggle); errors if the line is not a checkbox. Writes the body via the shared body-write path (frontmatter untouched) |
| `folders` | List folders (directory prefixes of `documents.path`) with doc counts; root docs bucket under `(root)` |
| `tags` | List all tags vault-wide with counts. Parent command (bare `tags` lists; `tags list` is the explicit subcommand) |
| `tags rename <old> <new>` | Rename a frontmatter tag across every document that carries it: rewrites each doc's frontmatter `tags` array (dedupes when `<new>` is already present) and reindexes. FRONTMATTER-ONLY in v1 (inline body `#old` tags are not rewritten; such docs are skipped). `--dry-run` previews affected docs without writing; per-file atomic with a collected `{renamed, skipped, failed}` summary, non-zero exit on any failure with no rollback of already-written files |
| `tag add <note> <tag>...` | Add one or more frontmatter tags to a single note (the per-note counterpart to the vault-wide `tags`, mirroring the `task`/`tasks` split). Merges into the note's `tags` array (dedupe, order preserved), schema-validates each tag, and reindexes via the shared write path so the change is immediately `list --tag`-searchable. Tags may be separate args or comma-separated; resolves the note via `file=`/`path=`/bare. Frontmatter-only; rejects read-only `.canvas`/`.base` |
| `tag remove <note> <tag>...` | Remove one or more frontmatter tags from a single note (no-op if absent); same resolution, validation, and reindex behavior as `tag add` |
| `aliases` | List frontmatter aliases mapped to their document (alias to path/title) |
| `export-context` | Generate CLAUDE.md-compatible context bundle (`--types --status --limit`) |
| `delete` | Delete document from disk and index (`--force`) |
| `move <src> <dst>` | Move/rename a note to a new vault-relative path, rewriting every `[[wikilink]]` AND markdown-style `[text](path.md)` link across the vault that points at it (wikilinks preserve `#heading`/`#^block`/`\|alias`/`!`-embed suffixes; markdown links preserve the `[label]` text, any `#anchor`/`?query` suffix, and the `.md` extension; both preserve the author's bare-vs-path form. Markdown links to external URLs (http/mailto/etc.) and anchor-only targets are skipped; links inside code are never touched). `--dry-run` previews the rename, the per-note rewrites, and the ambiguous links it would skip without writing anything; without `--force` a move is refused when a bare `[[name]]` link is ambiguous (the name matches more than one note). The target file is moved LAST, after referencing notes are rewritten, so a crash leaves links pointing at the still-present old name. JSON result: `{moved, rewritten, skipped_ambiguous, failed}` |
| `rename <src> <newname>` | Thin wrapper over `move`: destination is the source's folder + `<newname>` (`.md` appended if omitted; reject path separators). Same `[[wikilink]]` + markdown-link rewriting and `--dry-run`/`--force` behavior |
| `import-obsidian` | Import Obsidian vault (adds UUIDs, normalizes tags, builds index) |
| `export-obsidian` | Export to Obsidian format (`--strip-ids`) |
| `migrate` | Migrate a legacy 2ndbrain vault to the Obsidian-native format (schema v3); `--dry-run` previews without modifying. Non-mutating: source markdown is never changed. |
| `mcp-server` | Start MCP server on stdio transport. **Self-exits after 30 min idle** (no tool activity) so a closed AI session doesn't leave an orphan holding the index open (the client respawns it on demand). Override with `--idle-timeout <dur>` or `$2NB_MCP_IDLE_TIMEOUT` (e.g. `1h`); `0` = never. The idle watchdog (`internal/mcp/idle.go`) is lock-free (atomic activity clock + in-flight counter) and wraps each tool handler |
| `mcp-setup` | Show MCP setup instructions for all AI tools |
| `mcp status` | List live MCP server processes and recent tool invocations (`--json`) |
| `mcp reap` | Terminate stale/orphaned `mcp-server` processes for this vault (SIGTERM only; the server handles it cleanly). Reaps those whose last activity is older than `--older-than` (default 6h); never the current process, never an active server, and re-verifies the sidecar's start time before signaling to dodge PID reuse. `--dry-run` previews. With idle self-exit on `mcp-server`, this is a rarely-needed backstop. JSON: `{reaped[], skipped[], threshold, dry_run}` |
| `mcp configured` | Report whether the 2ndbrain MCP server is configured in the AI client config (`~/.claude.json`) for this vault (`--json`). Durable "is it set up?" check, unlike `mcp status` which reports "is it running right now?" |
| `mcp doctor` | End-to-end self-test of the MCP engine **in-process** (`internal/mcp.Engine` over the same `mcpToolRegistrations` the stdio server serves): counts tools (22), runs real `kb_info`/`kb_list`/`kb_search` round-trips (offline → BM25), and folds in AI readiness, `mcp configured`, the `instructions` string, and reliability signals (WAL size, alive/stale server counts). Proves it *works*, not just that it's configured. Engine checks are hard (exit 2); readiness/wiring/reliability are warnings. JSON reuses `config doctor`'s `DoctorCheck` shape (`checks[]`) plus top-level `tool_count`/`configured`/`wal_bytes`/`stale_servers`/`instructions_present`/… |
| `mcp install` / `mcp uninstall` | Write/remove the 2ndbrain server entry in `~/.claude.json` (the write-side inverse of `mcp configured`). Idempotent, backup-first (`~/.claude.json.bak`), and **preserves every unrelated key** — it parses to `map[string]json.RawMessage` and mutates only the `mcpServers` sub-map (so `numStartups`, `oauthAccount`, your other servers, etc. survive byte-for-byte); a malformed config is refused, not clobbered. `--scope user\|project`, `--command <path>` (the app passes its bundled CLI), `--client claude-code\|warp\|agents` (`warp` writes `~/.warp/.mcp.json`, `agents` the cross-tool `~/.agents/.mcp.json` which Warp also auto-reads — or `<vault>/.warp\|.agents/.mcp.json` for `--scope project` — both pinning the vault via `--vault` and `working_directory`), `--dry-run`. JSON: `{client, config_path, configured, changed, backup_path, server_key, scope}` |
| `plugin status` | Installed Obsidian plugin version vs this CLI (`--json`) |
| `plugin install` | Install or update the Obsidian plugin: downloads `manifest.json`/`main.js`/`styles.css` from the latest GitHub release into `<vault>/.obsidian/plugins/obsidian-2ndbrain/` (manifest written last so a partial install never looks complete). Alias: `plugin update`. **No-downgrade guard**: refuses (no write) when the installed plugin is newer than the latest release — e.g. a prerelease/promotion lag — so install can't silently downgrade; override with `--force`. Enabling in Obsidian stays manual (no API for it) |
| `suggest-links` | Suggest semantically related documents to link from a given document (`--limit`) |
| `suggest-target <target>` | Given ONE broken `[[wikilink]]` target, return ranked existing notes it might have meant — the "did you mean?" candidates behind the GUI link-fix sheet. Three tiers, best-first: (1) **drift** — the same normalized-name index `repair-links` uses (case/hyphen/underscore/whitespace folded), INCLUDING the ambiguous matches repair refuses to guess (via `polish.SuggestRepairTargets`); (2) **semantic** — nearest notes by embedding (skipped, not errored, when no embedder); (3) **keyword** — BM25 over the target words, so word-reorder/typo misses (`models-apresai` → `apresai-*`) surface offline. Read-only; emits `[]SuggestLinkResult` (`[]`, never null). Pair with `relink --from <target> --to <pick>` to apply |
| `polish` | AI copy-edit (`--system`, `--max-tokens`) — returns original + polished for diff preview. `--write` applies the polished body in place via the shared body-write path (opt-in; never default), still emitting original + polished for audit, and first writes a snapshot of the original under `.2ndbrain/recovery/polish/` so the change is reversible. `--links` also weaves grounded `[[wikilinks]]` to existing vault notes (semantic + substring candidates, ambiguous titles dropped; a deterministic `StripInventedLinks` pass guarantees no link to a nonexistent note). `--repair-links` deterministically REPAIRS broken `[[wikilinks]]` to existing notes (`polish.RepairBrokenLinks`): a broken target is rewritten only when its normalized — lower-cased, with hyphen/underscore folded to space and whitespace collapsed — form maps to exactly one note via basename/title/alias (the common case being case or separator drift, since the resolver is case- and separator-sensitive but Obsidian isn't); ambiguous or unmatched targets are left untouched and reported (never guessed), asset embeds are skipped, and `#heading`/`|alias` suffixes are preserved. Repair runs before the copy-edit so the AI preserves the corrected links; the snapshot is the true original so `--undo` reverts repairs + edits together. `--undo` restores the latest snapshot (reindex + re-embed) and refuses if the file changed since polishing unless `--force`. The default prompt is the LLM-judge-selected `polish.DefaultPolishSystem` (shared with `kb_polish`) |
| `git activity` | Recent commits touching vault files (`--since 7d`, `--json`) |
| `git show <hash>` | Full commit detail: metadata, stats, per-file diffs |
| `git diff <path>` | Unified diff of a file vs HEAD |
| `git status` | Uncommitted/untracked files in the vault |
| `ask <question>` | RAG Q&A — search vault, generate answer with sources. `--history <path\|->` (JSON `[{role, content}]`, `-` = stdin) makes it multi-turn: the history condenses follow-ups into standalone retrieval queries (reported as `rewritten_query` in `--json`) and grounds the answer |
| `chat` | Interactive multi-turn REPL over the same pipeline as `ask --history`; conversation lives in-process only, no `--json` |
| `ai status` | Provider, models, readiness, embedding count, vault portability state with one-line fix hints |
| `ai embed <text>` | Generate embedding vector (debug) |
| `ai setup` | Multi-provider setup wizard (`--provider --embedding-model --generation-model`). A model that passes its probe is persisted to the per-vault user catalog as `tier=user_verified`, so it shows up in `2nb models list` afterward (failed probes are never persisted) |
| `ai local` | Check local AI readiness (Ollama, models, disk, RAM, embeddings) |
| `models list` | Verified catalog. Flags: `--type --free --discover --status --provider --promote --scope --enabled-only`. `--discover --promote` tests unverified models concurrently and adds passing ones. `--enabled-only` drops user-disabled (dropdowns pass this; CLI use does not) |
| `models test <id>` | Smoke-test a model. `--save` writes to user catalog regardless of pass/fail (success → `tier=user_verified`, failure → `test_error`). Default `--scope vault` |
| `models add <id>` | Add/update a model. Default scope is per-vault `.2ndbrain/models.yaml`; `--scope global` writes `~/.config/2nb/models.yaml`. Updates *merge*: `Enabled`, `TestedAt`, `TestLatencyMs`, `Benchmark` are preserved unless explicitly re-set. `--similarity-threshold` is embedding-only; `--price-request` is for per-request priced models |
| `models remove <id>` | Remove from user catalog (`--provider --scope`) |
| `models enable [id]` | Mark enabled. With `--vendor <name>` (e.g. `anthropic`/`amazon`/`google`) toggles every model from that vendor — the GUI's bulk toggle. `--vendor` and `<id>` are mutually exclusive |
| `models disable [id]` | Hide from selection dropdowns (still listed by `models list`). Same `--vendor` bulk mode |
| `models enable-state <id>` | Tri-state pointer: `--state default|enabled|disabled`. `default` clears for tier defaults. Used by GUI Enable State menu |
| `models cost-preview [ids...]` | Estimate USD cost across one or more models. `--probe test|bench_embed|bench_gen|bench_rag|retrieval`. Local — no API calls |
| `models wizard` | Interactive end-to-end: providers → discover → easy-mode → cost preview → test → save. `--json` emits line-delimited events; aborts non-interactively if estimated cost > `--cost-cap` (default $0.10). `--set-active` writes the chosen embedding + generation models (and their provider) into the vault config via the same path `config set` uses (provider validation, disabled-flag clear, `ai.dimensions` resync), emitting a `set_active` event; an interactive run without the flag offers a y/N prompt (defaults to no), a non-interactive run does nothing unless `--set-active` is passed |
| `models bench` | Benchmark against the vault. `--probe embed|generate|retrieval|search|rag`. `retrieval` is zero-API (scores stored embeddings). History in `.2ndbrain/bench.db`; per-model summary written at `--summary-scope` (default `global`). `--json` emits line-delimited events |
| `models calibrate` | Sample baseline cosine distribution and recommend a similarity threshold. `--samples --save --scope --seed` |
| `models bench fav/unfav/favs/history/compare` | Manage benchmark favorites and view history |
| `skills list/install/uninstall/show` | Generate SKILL.md for AI coding agents (`--user`, `--all`, `--force`). Supported agents include `claude-code`, `cursor`, `windsurf`, `github-copilot`, `kiro`, `cline`, `roo-code`, `junie`, `warp` (`~/.warp/skills/2nb/SKILL.md`; Warp also reads `~/.claude/skills`), and the cross-tool `agents` (`.agents/skills/2nb/SKILL.md`, Warp's recommended primary, also honored by other agents). Installs are version-stamped; `skills list` auto-refreshes a stale, unmodified managed copy so a `brew upgrade` keeps the skill current |
| `skills doctor [slug]` | Verify an agent's skill works (slug defaults to `claude-code`): the SKILL.md is installed, non-empty, and has frontmatter, and the `2nb` it shells to resolves on **PATH** (`exec.LookPath`, the way the agent's shell finds it — NOT `os.Executable`) and runs `--version`. Honestly "installed + deps resolve", not "the agent invoked it". The on-PATH check is the common real failure (a cask bump leaves a stale terminal `2nb`). Also reports SKILL.md **freshness** (`Freshness{stamped, installed_version, up_to_date, modified}`): installs are stamped with `x-2nb-version`/`x-2nb-content-sha`, so a stale managed copy is flagged "out of date"; `skills list` self-heals an unmodified, stamped, out-of-date managed install in place (never clobbers a hand-edited one). JSON embeds `InstallStatus` + `binary_ok`/`binary_version`/`parses`/`file_nonempty`/`self_path`/`freshness` + `checks[]` |
| `config show/get/set/set-key/doctor` | Read/write config; `set-key <provider>` stores API key in macOS Keychain; `get --effective` resolves `ai.similarity_threshold` through its full chain (vault > calibration > model > default); `doctor` diagnoses AI-config problems (provider known/enabled, no orphaned model slot, `ai.dimensions` matches the model, DB embeddings match the selection, threshold resolves) with fix hints. Genuine config defects fail (exit 2); an environmental condition like an unreachable provider is a non-failing warning so `doctor` stays usable offline/in CI |
| `doctor` (alias `verify`) | Verify all three products — CLI, macOS app, Obsidian plugin — are installed and in sync with the latest release, with the exact fix command for any gap. The runtime counterpart to the per-product `verify`/`expect_version` checks in `.release.yaml`. Reuses the shared 24h release cache but **refetches it when it's behind an install** (the install is proof a newer release exists), and never shows a component a "latest" below its own version — so a just-released version is never reported as "installed > latest"; the plugin is read from the open vault (or `--vault`) and degrades to "unknown" (never errors) when no vault resolves; the app is read from `SecondBrain.app`'s `Info.plist` (macOS only). `--json` → `SuiteStatus` `{latest, checked, detail, in_sync, cli, app, plugin}`, each component a `ProductState` `{name, status, installed, version, update_available, fix}` (`status`: ok/outdated/missing/unknown/n/a). Scope is presence + version parity; functional readiness stays in `config`/`mcp`/`skills doctor` |
| `update` | Check whether a newer 2ndbrain release is available: compares the installed versions against the latest published GitHub release (`api.github.com/repos/apresai/2ndbrain/releases/latest`, cached 24h under `~/Library/Caches/2nb/updates`) and lists every component that is behind (not just the CLI — a release that shipped the CLI but not the app/cask is still flagged). Never hard-errors — offline falls back to the cache then reports "couldn't check". The 24h cache is **refetched** when it's behind an install (the installed version is proof a newer release exists), so a just-released version isn't shown as stale; and a component is never displayed with a "latest" below its own version. `--json` → `{current, latest, update_available, checked, detail, app, plugin}` where `app`/`plugin` are `ProductState` objects (additive and back-compatible — `current`/`latest`/`update_available` remain the CLI's). Use `2nb doctor` for the full presence-aware breakdown |
| `completion` | Emit shell completion script (`zsh|bash|fish|powershell`) |
| `completion install` | Install zsh completion idempotently into existing dir from `.zshrc` (or `~/.zsh/completions/_2nb`, or `--dir`); compinit runs unconditionally; warns on multiple `2nb` binaries on PATH |

**Shell completion** dispatches to the built binary so it stays fresh. Homebrew installs scripts via GoReleaser; non-brew users run `completion install`.

**Global flags:** `--format` (json/csv/tsv/yaml/raw/md/text; listings also `paths`/`tree`), `--porcelain`, `--json`, `--csv`, `--yaml`, `--vault`, `--unconfigured` (permit a write to a vault Obsidian doesn't know; without it such a write is refused), `--verbose` / `-v`, `--copy`. `--format raw` (and `md`) emits a value's `Serialize()` output (or the raw string/bytes) with no JSON wrapping, for piping a document body verbatim; `tsv` is tab-separated CSV; `text` is best-effort plain text. `--copy` also writes a command's rendered output to the clipboard (macOS `pbcopy`; a clear unsupported error elsewhere): `read`/`print` (body), `meta`/`property:read` (value), and `daily`/`daily path` (path) copy in their default output, and any command run with a machine format (`--json`/`--csv`/`--format …`, including `search`/`unresolved`/`list`) copies that rendered output.

**Obsidian-CLI syntax compatibility:** an argv preprocessor (`preprocessArgs` in `root.go`) lets `2nb` accept `obsidian`-CLI-style invocations as a drop-in (full mapping table plus accepted forms in [docs/obsidian-cli-mapping.md](docs/obsidian-cli-mapping.md)): `key=value` arguments (`file=`, `path=`, `to=`, `content=`, `name=`, `value=`, `query=`, `ref=`, `vault=`, `format=`, plus `template=` for create, `old=`/`new=` for tags:rename, and `tag=` for tag:add/tag:remove), boolean tokens (`total`, `append`, `overwrite`, `done`/`todo`/`toggle`, `verbose`), and colon-commands (`daily:read`/`daily:append`/`daily:prepend`/`daily:path`, `property:read`/`property:set`/`property:remove` → `meta`, `tags:rename` → `tags rename`, `tag:add`/`tag:remove` → `tag add`/`tag remove`, `link:unresolved`/`link:orphans`/`link:deadends`, `search:context`). Target resolution: `path=` is a strict exact vault-relative path; `file=` is the fuzzy resolver (exact → shortest-unique basename/suffix → title → alias, failing loudly with candidates on ambiguity); a bare positional is auto (exact-on-disk, else fuzzy). The resolver lives in `store.ResolveTarget` (shared with wikilink resolution via `buildLookupIndex`); CLI commands route through `resolveTargetArg` (a hidden `--resolve exact|fuzzy|auto` set by the shim). Compatibility command translations: `print` → `read`; `frontmatter`/`fm`/`properties` → `meta` (also cobra aliases); `files` → `list`; `search-content` → `search --bm25-only`; `list-vaults`/`set-default-vault`/`add-vault` → `vault list`/`set`/`create`. It only rewrites recognized command + parameter shapes; a free-text `search`/`ask`/`chat`/`search-content` query is never parsed as `key=value` (so a query containing `=` is preserved), and an unrecognized `key=value` on any command passes through verbatim rather than being dropped.

**Parent-command defaults:** `2nb ai` → `ai status`, `2nb models` → `models list`, `2nb git` → `git status`, `2nb mcp` → `mcp status`, `2nb plugin` → `plugin status`, `2nb skills` → `skills list`, `2nb config` → `config show`. `--help` still works (Cobra intercepts before `RunE`).

### AI Providers

The default provider is **AWS Bedrock** (via your AWS credentials): generation = Claude Haiku 4.5 (`us.anthropic.claude-haiku-4-5-20251001-v1:0`), embeddings = Amazon Nova-2 (`amazon.nova-2-multimodal-embeddings-v1:0`, 1024 dims). Defaults live in `DefaultAIConfig()` (`cli/internal/ai/config.go`).

**Bedrock auth** uses the AWS SDK credential chain (SigV4 from env or `~/.aws`), **or** a Bedrock **API key (bearer token)**. The bearer token is normally the `AWS_BEARER_TOKEN_BEDROCK` env var, but a GUI app launched by launchd has no shell env — so `2nb config set-key bedrock` (which prompts for the token) stores it in the macOS Keychain and `loadBedrockAWSConfig` (`cli/internal/ai/bedrock.go`, `ensureBedrockBearerToken`) exports it for the SDK when the env var is unset (macOS only, env wins). The SDK **prefers a bearer token over SigV4**, so a stored key overrides `~/.aws` SigV4 creds for Bedrock — replace it by re-running `set-key`, or delete the `dev.apresai.2ndbrain`/`bedrock` item in Keychain Access to fall back to SigV4. This is how the macOS app reaches Bedrock without your shell's credentials.

**Ollama (local) and OpenRouter are opt-in**: both ship `disabled: true` in a fresh vault's `config.yaml`, so selection UIs show only Bedrock until the user enables them. `2nb ai setup` (a Bedrock-first wizard that detects AWS creds, confirms region, verifies models, and reminds you to enable Bedrock model access in the AWS console), the macOS AI Hub, or activating the provider with `2nb config set ai.provider <name>` clears the `disabled` flag. `Disabled` only hides a provider's models from dropdowns; an explicitly-chosen active provider still runs.

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

> [!IMPORTANT]
> This section is superseded for 0.5.0 by the path-based identity and non-mutating sidecar architecture detailed in [docs/obsidian/identity-model.md](docs/obsidian/identity-model.md).

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

### MCP Server (22 tools)

Each `2nb mcp-server` writes a sidecar status file to `.2ndbrain/mcp/<pid>.json` (PID, start time, parent PID, last 50 invocations: tool, timestamp, duration, ok/error). The dashboard polls `2nb mcp status --json` every 5s. mark3labs/mcp-go has no client-connected hook, so sidecar files are the only enumeration mechanism.

The server self-announces via a one-line `instructions` string in the initialize response (`mcp.ServerInstructions`, wired through `newMCPServer` — the single source of truth for server construction shared by `Start`, tests, and future in-process self-tests). Clients fold it into their session-start "MCP Server Instructions" summary, so a connected-but-idle server is not misread as absent.

| Tool | Purpose |
|------|---------|
| `kb_info` | Vault overview: name, doc types, schemas, counts, AI status |
| `kb_search` | Hybrid search with type/status/tag filters |
| `kb_ask` | RAG Q&A with source citations |
| `kb_read` | Read document or chunk by heading path |
| `kb_list` | List with filters |
| `kb_create` | Create from template type; optional `path` files it under a vault-relative subdirectory |
| `kb_update_meta` | Update frontmatter with schema validation, then re-index the whole file (chunks/tags/links via `IndexSingleFile`) so a tag/status change is reflected in `kb_list`/`2nb list --tag` immediately; re-embedding stays gated on the body content hash (a metadata-only edit does not re-embed). Matches the CLI `meta --set` path |
| `kb_related` | Traverse link graph to depth N |
| `kb_structure` | Document heading hierarchy (also covers the `outline` view via `BuildOutline`) |
| `kb_backlinks` | Resolved inbound links to a document (store `Backlinks`) |
| `kb_links` | Outbound links from a document, including unresolved/broken ones (store `OutboundLinks`) |
| `kb_tags` | Vault-wide tag list with per-tag document counts (store `TagCounts`) |
| `kb_tasks` | GFM checkbox tasks across the vault or a file/dir, with `done`/`todo` filters (`document.ExtractTasks`) |
| `kb_delete` | Delete from vault and index |
| `kb_index` | Rebuild index and embeddings |
| `kb_append` | Append text to a document body; reindex + re-embed (shared body-write path); rejects read-only `.canvas`/`.base` |
| `kb_replace_section` | Replace one heading's section content (`document.ReplaceSection`); reindex + re-embed; rejects read-only `.canvas`/`.base` |
| `kb_suggest_links` | Find semantically related docs to link from a given doc |
| `kb_polish` | AI copy-editor returns original + polished for diff. `links:true` also adds grounded `[[wikilinks]]` to existing notes (same gathering + `StripInventedLinks` backstop as the CLI). Read-only: never writes (so no snapshot); undo stays CLI-only |
| `kb_git_activity` | Recent git commits touching vault files |
| `kb_git_diff` | Unified diff of a file vs HEAD |
| `kb_git_status` | Map of path → porcelain status for uncommitted files |

`move`/`rename` (the wikilink-rewriting vault mutation) is intentionally **CLI-only**: it is the highest-blast-radius write surface, so it stays behind `2nb move`/`2nb rename` (with their mandatory `--dry-run`) rather than an MCP tool. `polish --undo` (a whole-file restore from snapshot) is CLI-only for the same blast-radius reason — `kb_polish` is preview-only and never writes. `kb_outline` is not a separate tool: `kb_structure` already returns the outline via the shared `document.BuildOutline`.

### Testing

Tests use `t.TempDir()` for isolated vaults; each creates its own SQLite DB. Run with `cd cli && make test` (`go test -race -tags fts5 ./...`).

## Swift macOS App (`app/`)

**Framework:** SwiftUI + AppKit, Swift 6.0, macOS 14+
**Dependencies:** GRDB.swift (SQLite), Yams (YAML), swift-markdown
**Architecture:** MVVM with `@Observable`

The macOS app is a **configuration and companion dashboard, not an editor**: Obsidian is the editor. It reads the same `.2ndbrain/index.db` the CLI writes (WAL mode) and shells out to `2nb` (the **bundled** `Contents/Resources/2nb`, preferred by `CLIPath.resolve()`, falling back to Homebrew/PATH for non-bundled dev builds) for all AI / index / lint / git work. An `FSEventsWatcher` on the vault keeps the index fresh: notes edited in Obsidian are incrementally re-indexed + re-embedded a moment after they settle (debounced `2nb index --doc` via `scheduleExternalReindex`, skipping the app's own writes), and on bind a one-shot incremental `2nb index` (`syncOnBindIfStale`, gated on an on-disk-vs-indexed count delta) catches up notes added or removed while the app was closed, so embeddings stay current without a manual Sync. On launch it **binds to the vault Obsidian currently has open** — read from Obsidian's own registry `~/Library/Application Support/obsidian/obsidian.json` via `ObsidianRegistry` (`SecondBrainCore/Vault/ObsidianRegistry.swift`) — so the dashboard and Obsidian stay on the same vault. The **CLI reads the same Obsidian registry as its authoritative active-vault source** (`vault.ObsidianOpenVault`, `cli/internal/vault/obsidian_registry.go`): the READ path `resolveVaultDir` resolves `--vault` → `2NB_VAULT` → the open Obsidian vault → cwd-vault (the Obsidian rung is gated off under `2NB_TEST`). There is **no 2nb-managed active-vault pointer file** — the GUI and CLI both follow Obsidian's registry, so a bare terminal `2nb ask`/`search` already targets the same vault the dashboard shows, with nothing to drift. **Writes are firmer than reads** (`openVaultAndSetActive`, `root.go`; also used by the MCP server): a write with no `--vault`/`2NB_VAULT` goes to the vault Obsidian has open (or, if closed, its most-recent), and the **current directory is never an implicit write target**: a cwd that resolves a vault only by walking up the tree (`FindVaultRoot`) to a parent is **refused** (`walkUpRefusedError`) before any open, so a write can never silently land in — or auto-mint a `.2ndbrain/` sidecar in — an unintended vault. The cwd is honored only when it IS the vault root. An explicit `--vault` to a vault Obsidian doesn't know is refused unless `--unconfigured` (or `2NB_UNCONFIGURED=1` for the flagless MCP server) acknowledges that the note won't appear in Obsidian or the 2nb index. This is the guard that prevents a mis-`cd`'d agent (e.g. Warp launched from a source repo) from splitting a vault. (`~/.2ndbrain-vaults` recents remains, but it is display-only for `vault list`, never a resolution source.) The Welcome screen offers **"Open your Obsidian vault: \<name\>"**, and the `Vault > Open Vault…` panel (Cmd+Shift+O) validates the chosen folder is a real Obsidian vault (has `.obsidian/`, via `VaultManager.isObsidianVault`) and warns when it isn't the one Obsidian has open. The window/sidebar title shows the active vault name. The window is a `NavigationSplitView` whose sidebar leads with **Home** (the default screen) and groups the six power-user tabs under an **Advanced** section (`DashboardTab` in `ContentView.swift`):

| Tab | View | Purpose |
|-----|------|---------|
| **Home** (default) | HomeView.swift | Consolidated common-case surface: Vault card (name/path + an Obsidian-match badge confirming this is the vault Obsidian has open, plus an Obsidian-plugin row showing the installed plugin version with an Install/Update button that shells `2nb plugin install`; `ObsidianPlugin`/`HomePlugin`), AI card (AWS Bedrock + Claude Haiku 4.5 + Amazon Nova-2 with a ready/not-ready dot and Save-as-default / Test buttons), a **Claude Code card** (`HomeSkill`/`HomeMCPConfigured`) with a skill-installed row (Install button shelling `2nb skills install claude-code --user`, from `2nb skills list --json`) and an MCP-server-configured row (from `2nb mcp configured --json`, with a **Configure automatically** button that shells `2nb mcp install` behind an NSAlert confirm — writing `~/.claude.json` for you — plus a fallback Show-setup snippet button; "configured" is the durable check since the server is launched on demand by the client), a **Verify** panel (`ClaudeCodeHealthView`/`ClaudeCodeHealth`) that runs the real end-to-end self-test — fanning out `skills doctor` + `mcp doctor` + `config doctor` + two real `models test` calls into a grouped pass/warn/fail checklist — and a **Reliability** row with one-click **Checkpoint WAL** (`vault checkpoint`) / **Reap stale servers** (`mcp reap`, dry-run→confirm) buttons; a cross-dependency callout warns when only one of {skill, MCP} is set up, and Index card (doc + embedding counts, a "N notes awaiting embedding" hint, and **Sync** / Re-embed All buttons; Sync runs the incremental, hash-gated `2nb index` that re-embeds only what changed and reconciles deletions via `purgeStale`). An orange banner warns when the `2nb` the app resolves is older than the app (`CLIVersion`/`refreshCLIVersion`); since the app now prefers its bundled, version-matched CLI this stays silent in a normal release (it only fires on dev builds that fall back to a stale Homebrew copy). When Homebrew is present (`BrewLocator`) the banner offers an Update CLI button that runs `brew upgrade apresai/tap/twonb` (`AppState.upgradeCLI`) to refresh the terminal/plugin's PATH `2nb`. The catalog/benchmark/MCP/git/lint depth lives under Advanced. |

Advanced section:

| Tab | View | Purpose |
|-----|------|---------|
| Vault Status | VaultStatusView.swift | Unified health: vault info, index coverage, portability, AI reachability, stale docs; Sync + Re-embed All |
| AI Settings | AIHubView.swift | AI Hub (see below) — providers, active models, full catalog |
| MCP Server | MCPStatusView.swift | A durable "Configured in ~/.claude.json" banner (from `2nb mcp configured --json`, via `HomeMCPConfigured`) above live MCP server processes + recent tool invocations; polls `2nb mcp status --json` every 5s. The banner answers "is it set up?" even when no server is running (the client launches it on demand), and the empty state distinguishes configured-but-idle from not-configured |
| Git Integration | GitActivityView.swift | Recent commits (1/3/7/30-day window); click a row → `CommitDetailView` split pane (file list + per-file diff) |
| Validation | LintResultsView.swift | Shells out to `2nb lint --json` and renders findings; each finding is actionable — **Open in Obsidian** (via an `obsidian://open` deep link built by `SecondBrainCore/Vault/ObsidianURL`, with a default-app fallback) and, for schema findings (missing required field / invalid enum classified by `LintFinding`), **Set value…** (a sheet that runs `2nb meta --set` and re-lints). A broken-wikilink finding opens **Fix link…** → `LinkResolutionSheet`, which has **no dead ends**: it concurrently loads a `repair-links` preview and `suggest-target` candidates, then offers, in priority order, **Repair** drift (one-click, with a diff), **Did you mean?** (relink to a chosen note), **Create the note**, and **Unlink** (keep the text) — Create and Unlink are always present, so every finding has a real fix. Each action is reversible with `polish --undo`; a CLI no-op (stale finding) surfaces in-sheet instead of a false success banner |
| Updates | UpdatesView.swift | Shows the **app**, **CLI**, and **Obsidian-plugin** versions against the latest published release (via `2nb update --json`, decoded into `UpdateInfo`). CLI + plugin freshness comes from the CLI's own parity computation in the payload (`update_available` on the `current` field / the `plugin` `ProductState`), the same source as `2nb doctor`, so the dashboard can't disagree with the terminal; the **app** row stays authoritative from the running bundle (`appVersion` via `CLIVersion.isOlder`, not the payload's `app` field, which reflects `/Applications`), and a row shows the "→ latest" only when it's genuinely behind, so a current-or-ahead app never reads as outdated. One-click **Update CLI** (`AppState.upgradeCLI` + `BrewLocator`) and **Update plugin** (`installObsidianPlugin`); the app row shows a copyable `brew upgrade --cask` since a running app can't replace its own bundle. "Check now" re-runs the check |

Supporting views: `MCPSetupView` (MCP config snippets for AI tools), `ModelCatalogPickerView` (per-model detail / test / benchmark, opened from the AI Hub), `IndexProgressView` (rebuild confirmation → progress → stats), `MergeConflictView` / `DiffView` (reusable Myers LCS unified diff), `PreferencesView` (Cmd+,). `AppDelegate.swift` renames the default File menu to "Notes".

### Menus & Shortcuts

- **Vault** menu: New Vault, Open Vault (Cmd+Shift+O), Reveal Vault in Finder, Vault Status, Sync Index, Validate Vault, Import Obsidian Vault, Export to Obsidian.
- **View**: Recent Activity (Cmd+Shift+G).
- **AI** menu: AI… (Cmd+Shift+, → AI Hub), MCP Server Configuration, MCP Server Status (Cmd+Shift+M).

### macOS SwiftUI Gotchas

- **Use AppKit dialogs for modals:** prefer `NSAlert.runModal()` / `NSOpenPanel.runModal()` over SwiftUI `.sheet()` / `beginSheetModal` when a modal needs reliable button/keyboard events.
- **Computer-use access:** The `.app` bundle must have a real binary (not symlink) and be ad-hoc codesigned (`codesign -s - --deep --force`). The Makefile handles this.
- **Troubleshooting:** When hitting SwiftUI platform bugs, use Context7 and Brave Search before guessing.
- **Yams traps, uncatchably:** `Yams.load` builds Swift values through a constructor that can `fatalError` (NOT throw) on malformed YAML — e.g. Obsidian template placeholders (`date: {{date}}`) or duplicate keys — so `do/catch` / `try?` won't save you (this crashed a shipped release during vault indexing). Parse untrusted frontmatter via `Yams.compose` (AST only) plus a manual, depth-bounded `Node` walk; see `FrontmatterParser`.

### Context7 Library IDs

| Library | ID |
|---------|----|
| SwiftUI (Apple docs) | `/websites/developer_apple_swiftui` |
| Swift language book | `/swiftlang/swift-book` |
| Swift concurrency migration | `/swiftlang/swift-migration-guide` |
| GRDB.swift (SQLite) | `/groue/grdb.swift` |

**AI Hub (AIHubView.swift)** — Single merged surface (AI menu > AI… · Cmd+Shift+, ; also the "AI Settings" tab) for everything AI. Three sections:

- **Providers** — Bedrock / OpenRouter / Ollama cards with live status, enable/disable. Provider disable is vault config: `ai.<provider>.disabled` hides every model from that provider.
- **Active** — current embedding + generation slot, each with `Change` button.
- **Catalog** — grouped by vendor within type (Embedding first, then Generation); each group is a collapsible disclosure with count, latest-first rows, and `Enable all` / `Disable all` bulk buttons.

Per-row action `Details` opens `ModelCatalogPickerView` (sidebar + detail; filters: type/provider/tier/enabled/tested/compatible; sort: Best/Cheapest/Fastest/Newest/Name; actions: Test, Set Active, Set Active + Re-embed, Enable State tri-state, Similarity Threshold override, Cost Preview per probe kind, Benchmark with streaming events).

Replaces the AI Setup Wizard, Test AI Connection, and Model Wizard. Observes `modelsCatalogVersion` so external CLI edits refresh live. Vendor identity (`vendor / vendor_display / family / version_sort_key`) and the `compatible` flag are computed by the Go CLI in `applyCatalogUIFields` and sent over JSON — Swift no longer mirrors that logic. `Set Active` is gated on `appState.isIndexing` and refused at the AppState layer to prevent mixed-model embeddings during a rebuild.

## Obsidian Plugin (`plugins/obsidian-2ndbrain`)

A thin wrapper that shells out to the `2nb` CLI; Obsidian remains the editor. Command-palette prefix is **"2ndbrain AI:"**. Commands: Open chat, Semantic Search, Ask AI (RAG Q&A), Find Similar Notes, Rebuild AI Index, Polish current note, and Setup wizard. **Polish** is exposed on every surface (since it acts on the open note): the command/hotkey, a **sparkle ribbon icon**, a **note-header toolbar action** (`MarkdownView.addAction`, attached per-pane on `active-leaf-change`/`file-open`, deduped via a `WeakSet`), and the **right-click editor menu**. Clicking it runs `2nb polish <path> --write --json --links --repair-links` (apply-then-review: it copy-edits, repairs broken `[[wikilinks]]` to existing notes, and adds grounded new links in place, after a `flushEditor` `vault.modify` so the CLI's external write can't clobber unsaved edits), then opens `PolishResultModal` showing a colored line diff (`computeLineDiff`) plus repaired/skipped-link summaries, with **Keep** / **Undo**; Undo shells `polish --undo` (confirming before `--force` if the note changed since). A single-flight `polishing` lock serializes the four trigger surfaces. A **ribbon icon** (custom head-with-brain mark matching the app icon, registered via `addIcon`) toggles a right-sidebar **chat panel** (`ChatView extends ItemView`, view type `2ndbrain-chat`) holding a true multi-turn conversation: each message passes prior turns to `2nb ask --json --history -` via stdin (capped client-side by `trimChatHistory`, mirroring `ai.TrimHistory`) and renders the answer, degradation `warnings`, and source chips via a renderer shared with the Ask AI modal; a pre-`--history` CLI degrades to single-shot with an upgrade hint. It can **download and manage the `2nb` binary itself** (macOS only; resolves the latest GitHub release tag at runtime, ad-hoc signs it, and strips the quarantine xattr because the release isn't notarized) and opens a **first-run setup wizard** (Download CLI → Connect AI → Index).

Install via **BRAT** (`apresai/2ndbrain`) or copy `manifest.json` / `main.js` / `styles.css` from a GitHub release, with **no npm build needed** by end users. Settings: "Download / update CLI", "2nb CLI Path" (defaults to `2nb`; resolution is **version-aware** via `resolveCliPath` — it probes Homebrew + `~/go/bin` + PATH, and a plugin-managed download wins over a system install only when it is at least as new, so a stale managed copy can never silently shadow a fresh `brew upgrade`. `ensureCliFresh` on load re-downloads a managed copy that is behind the system binary or the plugin's version floor; a custom path is honored verbatim. The Components section degrades per-row with the resolved path + `--version` and a fix when `doctor` is unavailable), a read-only **"Vault"** line (open Obsidian vault path + index state), a **"Claude Code skill"** row (installed-status with an Install button that shells `2nb skills install claude-code --user`), a **"Claude Code MCP server"** row (configured-status from `2nb mcp configured`, with a Copy-setup-snippet button; "configured" is the durable check since the server is launched on demand by the client), and a **"Components"** section (from `2nb doctor --json` via `suiteStatus()`) with CLI / macOS app / Obsidian plugin rows showing each one's installed version, whether it is in sync with the latest release, and the fix command for any gap, plus an **Update plugin** button that shells `2nb plugin install`. Every CLI call is **pinned to the open Obsidian vault via `--vault adapter.getBasePath()`** (`pinVaultArgs`), so 2nb can never resolve a different vault from the Obsidian registry or cwd, keeping the Obsidian vault and the 2nb vault joined. Source of record: `plugins/obsidian-2ndbrain/main.ts`.

## Vault Format

Full reference: [`docs/vault-structure.md`](docs/vault-structure.md) and [`docs/templates.md`](docs/templates.md).

**Quick reference:**

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

Documents are plain `.md` with YAML frontmatter (`id` UUID, `title`, `type`, `status`, `tags`, `created`, `modified`). Wikilinks: `[[target]]`, `[[target#heading]]`, `[[target|alias]]`.

Beyond `.md`, the indexer now parses and indexes `.canvas` (JSON Canvas) and `.base` (YAML Bases) files as read-only synthetic views — file-type canvas nodes become `[[wikilinks]]`, text cards and base key/value content become searchable chunks. The CLI never writes back to `.canvas`/`.base` files.

**Document type schemas** (`.2ndbrain/schemas.yaml`):

| Type | Required | Statuses |
|------|----------|----------|
| **adr** | title, status | proposed → accepted/deprecated → superseded |
| **runbook** | title, status | draft, active, archived |
| **prd** | title, status | draft → review → approved → shipped → archived (review/approved can return to draft) |
| **prfaq** | title, status | draft → review → final |
| **note** | title | draft, complete |
| **postmortem** | title, status, incident-date | draft, reviewed, published |

**SQLite tables (`index.db`):** `documents`, `chunks`, `chunks_fts` (FTS5), `links`, `tags`, `aliases`, `schema_version`. Schema v3 adds the `aliases` table (`doc_id`, `alias`) and a `block_id` column on both `chunks` and `links` for Obsidian block references (`^block-id`).

**SQLite tables (`bench.db`):** `favorites` (provider, model_id, model_type, added_at), `runs` (timestamp, provider, model_id, probe, latency_ms, ok, detail, vault_doc_count), `schema_version`. Created on first `models bench`.

## Obsidian Conversion

> [!IMPORTANT]
> The legacy conversion commands are superseded by 0.5.0 native vault operations. See [docs/obsidian/README.md](docs/obsidian/README.md) for details.

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
