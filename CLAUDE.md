# 2ndbrain

Obsidian-native AI companion. **Obsidian stays your editor**; the Go CLI (`2nb`) + MCP server are the engine that indexes, searches, and answers (RAG) over a real Obsidian vault. A thin Obsidian plugin and a macOS configuration dashboard wrap the CLI. `2nb` writes only a gitignored `.2ndbrain/` sidecar and never rewrites a note's body except via explicit, user-invoked commands (`append`, `prepend`, `replace`); frontmatter edits via `meta` have always rewritten files in place. The one command that mutates OTHER notes is `move`/`rename` (the strongest write surface): it rewrites every `[[wikilink]]` AND markdown-style `[text](path.md)` link across the vault that points at the moved note so links stay valid. It is gated by a mandatory `--dry-run` preview, a crash-safe ordering (the target file is moved LAST, after referencing notes are rewritten, so an interruption leaves links pointing at the still-present old name), and an ambiguity guard (a non-`--force` move is refused when a bare `[[name]]` link could point at more than one note; `--force` rewrites only the unambiguous path-qualified links and leaves the bare ones). (One further explicit, user-invoked exception: `2nb plugin install` writes the plugin bundle under `.obsidian/plugins/obsidian-2ndbrain/`; never notes, never Obsidian settings.)

## Repository Layout

- `cli/` — Go CLI binary (`2nb`) + MCP server (the engine)
- `app/` — Swift macOS configuration & companion dashboard, **not an editor** (SwiftUI + AppKit)
- `plugins/obsidian-2ndbrain/` — thin Obsidian plugin that shells out to `2nb`
- `reqs.md` — EARS-format requirements specification
- `press-release.md` — Product vision document
- `test-plan.md` — Requirements validation test plan

### Project docs (`docs/`)

- [`agent-teaching.md`](docs/agent-teaching.md) — MCP vs CLI decision matrix + test battery design
- [`mcp-integration.md`](docs/mcp-integration.md) — MCP setup snippets for Claude Code, Cursor, and other clients
- [`templates.md`](docs/templates.md) — Built-in document type templates (adr, runbook, prd, prfaq, note, postmortem)
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

### Pipeline

**`make release-all`** is the front door: one command (canonical clone only; needs gitignored `scripts/sign.env`) that runs the test gate, bumps (`BUMP=build|minor|major|none`), tags, **waits for CI**, then signs/notarizes/publishes the app + cask, and verifies every product shipped at one version (`scripts/release-all.sh`). The underlying two steps remain available individually:

A release is **two steps**: CI ships the CLI + plugin; the macOS app is signed, notarized, and published from the maintainer's machine (signing keys never leave it / never enter CI).

1. `make bump-build` (or `bump-minor`/`bump-major`) — increment `VERSION`, regenerate `Version.swift`, sync the plugin version files.
2. `make release` — updates `CHANGELOG.md`, commits, tags `v<VERSION>`, pushes tag.
3. GitHub Actions (`.github/workflows/release.yml`) on tag push: macos-latest arm64, CGO_ENABLED=1; GoReleaser builds CLI for arm64+x86_64 and pushes formula `twonb.rb` to `apresai/homebrew-tap`; builds + uploads the Obsidian plugin assets; maintains the `2nb` formula alias. **CI does NOT build the macOS app or the cask.**
4. `make release-app` — **local, after the CI release exists.** Runs `scripts/release-app-local.sh --publish`: builds `SecondBrain.app`, Developer ID-signs it (hardened runtime), Apple-notarizes via `notarytool` + staples, packages `SecondBrain-<VERSION>-arm64.zip`, uploads it to release `v<VERSION>`, and updates the cask `secondbrain.rb` (version + sha256) in the tap. Signing config is read from `scripts/sign.env` (gitignored; template at `scripts/sign.env.example`); the private key stays in the keychain / cert store.
5. `make release-local` — local CLI-only release (no app, no notarization).

Key files: `.goreleaser.yaml`, `.github/workflows/release.yml`, `scripts/release-app-local.sh`, `scripts/sign.env.example`, `casks/secondbrain.rb.tmpl` (with `CASK_VERSION`/`CASK_SHA256` tokens), `scripts/update-changelog.sh`, `CHANGELOG.md`.

The `apresai` GitHub environment provides `HOMEBREW_TAP_TOKEN` (PAT for `apresai/homebrew-tap`). No code-signing secrets live in CI — signing is local only.

The macOS app is distributed as an arm64 **Developer ID-signed, Apple-notarized** `.app` bundle, so it launches with no Gatekeeper prompt even when Homebrew's cask install quarantines it. The cask `depends_on formula: "apresai/tap/twonb"` because the app shells out to `2nb` for AI/indexing/lint; a cask upgrade does **not** bump the formula, so the app warns on Home when the CLI has drifted behind (and, when Homebrew is present, offers an Update CLI button that runs the `brew upgrade` itself).

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
| `internal/graph` | Link graph BFS traversal |
| `internal/mcp` | MCP server with 22 tools + sidecar status files |
| `internal/git` | Read-only git wrappers (IsRepo, Activity, DiffFile, StatusFiles) |
| `internal/skills` | Skill file generation and agent registry |
| `internal/output` | JSON/CSV/YAML formatters |
| `internal/testutil` | Test helpers (NewTestVault, CreateAndIndex) |

Key types: `document.Document`, `store.DB`, `vault.Vault`, `search.Engine`, `graph.Graph`.

### CLI Commands (73)

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
| `create` | Create document from template (`--type`, `--title`, `--path`, `--content`). `--path <subdir>` files the doc under a vault-relative subdirectory (created if missing); default is the vault root. `--content` sets the initial body instead of the type template |
| `read` | Read full document or specific section (`--chunk`) |
| `append` | Append content to a document's body (`--text`, `--file`, or stdin). Explicit, opt-in body write; leaves frontmatter untouched |
| `prepend` | Insert content at the start of a document's body, after the frontmatter (`--text`, `--file`, or stdin) |
| `replace` | Replace a document's body, or just one heading's section content with `--section <heading>` (`--text`, `--file`, or stdin). First match wins on duplicate headings |
| `daily` | Resolve today's daily note from Obsidian's core daily-notes plugin config (`.obsidian/daily-notes.json`: folder, format, optional template). Bare `daily` resolves, creates the note if missing, and prints the vault-relative path. `daily read` prints its body; `daily append`/`daily prepend` (`--text`, `--file`, or stdin) add to the body via the shared body-write path. Missing/disabled plugin falls back to Obsidian defaults (root folder, `YYYY-MM-DD`); never hard-errors. The date format honors Moment's `[literal]` bracket-escaping |
| `meta` | View or update frontmatter with schema validation. `--set key=value` writes; `--get <key>` reads one field (ExitNotFound if absent); `--remove <key>` (repeatable) deletes a field in place, preserving comments/order, and refuses identity keys (id/path/title/type) and schema-required fields. Writes now re-index the whole file (chunks/tags/links via `IndexSingleFile`), so a frontmatter tag change is reflected in `list --tag` immediately; re-embedding stays gated on the body content hash, so a metadata-only edit does not re-embed |
| `index` | Rebuild index. `--doc <path>` for a single doc; `--force-reembed` invalidates every stored embedding |
| `search` | Hybrid BM25 + semantic. Filters: `--type --status --tag --limit`. `--threshold` overrides cosine cutoff. `--bm25-only` |
| `list` | List documents with filters (`--type --status --tag --limit --sort`) |
| `lint [glob]` | Validate schemas, check broken wikilinks |
| `stale` | List documents not modified within N days (`--since`) |
| `related` | Find related docs via link graph (`--depth`) |
| `backlinks <path>` | List resolved inbound links to a document: which docs link to it, with the source path/title and the link's heading/alias/raw form |
| `links <path>` | List outbound links from a document, including unresolved ones (each carries a `resolved` bool), so it doubles as a per-file broken-link view |
| `orphans` | List documents with no resolved inbound link (nothing in the vault links to them) |
| `deadends` | List documents with no resolved outbound link (they link to nothing real in the vault) |
| `unresolved` | List every unresolved (broken) wikilink across the vault: each source doc path paired with the raw `[[target]]` that resolves to no note. Vault-wide complement to `links <path>` (which is per-file) |
| `graph` | Output link graph as JSON adjacency list |
| `outline <path>` | Heading tree of a document (heading path, level, line span). Shares `document.BuildOutline` with the MCP `kb_structure` tool |
| `wordcount <path>` | Word, character, and heading counts over the indexable body (comments stripped). Alias: `wc` |
| `tasks` | List GFM checkbox tasks (`- [ ]` / `- [x]`) across the vault. Filters: `--done`, `--todo`, `--path <file\|dir>`. v1 = GFM open/done only (custom statuses like `[>]`/`[-]` ignored). `--json` |
| `task <path> <line>` | Toggle a single GFM checkbox at a 1-based body line. `--done`/`--todo`/`--toggle` (default toggle); errors if the line is not a checkbox. Writes the body via the shared body-write path (frontmatter untouched) |
| `folders` | List folders (directory prefixes of `documents.path`) with doc counts; root docs bucket under `(root)` |
| `tags` | List all tags vault-wide with counts. Parent command (bare `tags` lists; `tags list` is the explicit subcommand) |
| `tags rename <old> <new>` | Rename a frontmatter tag across every document that carries it: rewrites each doc's frontmatter `tags` array (dedupes when `<new>` is already present) and reindexes. FRONTMATTER-ONLY in v1 (inline body `#old` tags are not rewritten; such docs are skipped). `--dry-run` previews affected docs without writing; per-file atomic with a collected `{renamed, skipped, failed}` summary, non-zero exit on any failure with no rollback of already-written files |
| `aliases` | List frontmatter aliases mapped to their document (alias to path/title) |
| `export-context` | Generate CLAUDE.md-compatible context bundle (`--types --status --limit`) |
| `delete` | Delete document from disk and index (`--force`) |
| `move <src> <dst>` | Move/rename a note to a new vault-relative path, rewriting every `[[wikilink]]` AND markdown-style `[text](path.md)` link across the vault that points at it (wikilinks preserve `#heading`/`#^block`/`\|alias`/`!`-embed suffixes; markdown links preserve the `[label]` text, any `#anchor`/`?query` suffix, and the `.md` extension; both preserve the author's bare-vs-path form. Markdown links to external URLs (http/mailto/etc.) and anchor-only targets are skipped; links inside code are never touched). `--dry-run` previews the rename, the per-note rewrites, and the ambiguous links it would skip without writing anything; without `--force` a move is refused when a bare `[[name]]` link is ambiguous (the name matches more than one note). The target file is moved LAST, after referencing notes are rewritten, so a crash leaves links pointing at the still-present old name. JSON result: `{moved, rewritten, skipped_ambiguous, failed}` |
| `rename <src> <newname>` | Thin wrapper over `move`: destination is the source's folder + `<newname>` (`.md` appended if omitted; reject path separators). Same `[[wikilink]]` + markdown-link rewriting and `--dry-run`/`--force` behavior |
| `import-obsidian` | Import Obsidian vault (adds UUIDs, normalizes tags, builds index) |
| `export-obsidian` | Export to Obsidian format (`--strip-ids`) |
| `migrate` | Migrate a legacy 2ndbrain vault to the Obsidian-native format (schema v3); `--dry-run` previews without modifying. Non-mutating: source markdown is never changed. |
| `mcp-server` | Start MCP server on stdio transport |
| `mcp-setup` | Show MCP setup instructions for all AI tools |
| `mcp status` | List live MCP server processes and recent tool invocations (`--json`) |
| `mcp configured` | Report whether the 2ndbrain MCP server is configured in the AI client config (`~/.claude.json`) for this vault (`--json`). Durable "is it set up?" check, unlike `mcp status` which reports "is it running right now?" |
| `plugin status` | Installed Obsidian plugin version vs this CLI (`--json`) |
| `plugin install` | Install or update the Obsidian plugin: downloads `manifest.json`/`main.js`/`styles.css` from the latest GitHub release into `<vault>/.obsidian/plugins/obsidian-2ndbrain/` (manifest written last so a partial install never looks complete). Alias: `plugin update`. Enabling in Obsidian stays manual (no API for it) |
| `suggest-links` | Suggest semantically related documents to link from a given document (`--limit`) |
| `polish` | AI copy-edit (`--system`, `--max-tokens`) — returns original + polished for diff preview. `--write` applies the polished body to the document in place via the shared body-write path (opt-in; never default), still emitting original + polished for audit |
| `git activity` | Recent commits touching vault files (`--since 7d`, `--json`) |
| `git show <hash>` | Full commit detail: metadata, stats, per-file diffs |
| `git diff <path>` | Unified diff of a file vs HEAD |
| `git status` | Uncommitted/untracked files in the vault |
| `ask <question>` | RAG Q&A — search vault, generate answer with sources. `--history <path\|->` (JSON `[{role, content}]`, `-` = stdin) makes it multi-turn: the history condenses follow-ups into standalone retrieval queries (reported as `rewritten_query` in `--json`) and grounds the answer |
| `chat` | Interactive multi-turn REPL over the same pipeline as `ask --history`; conversation lives in-process only, no `--json` |
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
| `config show/get/set/set-key/doctor` | Read/write config; `set-key <provider>` stores API key in macOS Keychain; `get --effective` resolves `ai.similarity_threshold` through its full chain (vault > calibration > model > default); `doctor` diagnoses AI-config problems (provider known/enabled, no orphaned model slot, `ai.dimensions` matches the model, DB embeddings match the selection, threshold resolves) with fix hints. Genuine config defects fail (exit 2); an environmental condition like an unreachable provider is a non-failing warning so `doctor` stays usable offline/in CI |
| `completion` | Emit shell completion script (`zsh|bash|fish|powershell`) |
| `completion install` | Install zsh completion idempotently into existing dir from `.zshrc` (or `~/.zsh/completions/_2nb`, or `--dir`); compinit runs unconditionally; warns on multiple `2nb` binaries on PATH |

**Shell completion** dispatches to the built binary so it stays fresh. Homebrew installs scripts via GoReleaser; non-brew users run `completion install`.

**Global flags:** `--format` (json/csv/yaml/raw), `--porcelain`, `--json`, `--csv`, `--yaml`, `--vault`, `--verbose` / `-v`. `--format raw` emits a value's `Serialize()` output (or the raw string/bytes) with no JSON wrapping, for piping a document body verbatim.

**Obsidian-CLI syntax compatibility:** an argv preprocessor (`preprocessArgs` in `root.go`) lets `2nb` accept `obsidian`-CLI-style invocations as a drop-in: `key=value` arguments (`file=`, `path=`, `to=`, `content=`, `name=`, `value=`, `query=`, `ref=`, `vault=`, `format=`) and colon-commands (`daily:read`/`daily:append`, `property:read`/`property:set`/`property:remove` → `meta`, `link:unresolved`/`link:orphans`/`link:deadends`, `search:context`). It only rewrites recognized command + parameter shapes; a free-text `search`/`ask`/`chat` query is never parsed as `key=value` (so a query containing `=` is preserved), and an unrecognized `key=value` on any command passes through verbatim rather than being dropped.

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

| Tool | Purpose |
|------|---------|
| `kb_info` | Vault overview: name, doc types, schemas, counts, AI status |
| `kb_search` | Hybrid search with type/status/tag filters |
| `kb_ask` | RAG Q&A with source citations |
| `kb_read` | Read document or chunk by heading path |
| `kb_list` | List with filters |
| `kb_create` | Create from template type; optional `path` files it under a vault-relative subdirectory |
| `kb_update_meta` | Update frontmatter with validation |
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
| `kb_polish` | AI copy-editor returns original + polished for diff |
| `kb_git_activity` | Recent git commits touching vault files |
| `kb_git_diff` | Unified diff of a file vs HEAD |
| `kb_git_status` | Map of path → porcelain status for uncommitted files |

`move`/`rename` (the wikilink-rewriting vault mutation) is intentionally **CLI-only**: it is the highest-blast-radius write surface, so it stays behind `2nb move`/`2nb rename` (with their mandatory `--dry-run`) rather than an MCP tool. `kb_outline` is not a separate tool: `kb_structure` already returns the outline via the shared `document.BuildOutline`.

### Testing

Tests use `t.TempDir()` for isolated vaults; each creates its own SQLite DB. Run with `cd cli && make test` (`go test -race -tags fts5 ./...`).

## Swift macOS App (`app/`)

**Framework:** SwiftUI + AppKit, Swift 6.0, macOS 14+
**Dependencies:** GRDB.swift (SQLite), Yams (YAML), swift-markdown
**Architecture:** MVVM with `@Observable`

The macOS app is a **configuration and companion dashboard, not an editor**: Obsidian is the editor. It reads the same `.2ndbrain/index.db` the CLI writes (WAL mode) and shells out to `2nb` for all AI / index / lint / git work. On launch it **binds to the vault Obsidian currently has open** — read from Obsidian's own registry `~/Library/Application Support/obsidian/obsidian.json` via `ObsidianRegistry` (`SecondBrainCore/Vault/ObsidianRegistry.swift`) — so the dashboard and Obsidian stay on the same vault. When it binds an initialized vault, `AppState.openVault` also runs `2nb vault set <path>` (best-effort, background) to point the CLI's shared `~/.2ndbrain-active-vault` at it, so a bare terminal `2nb ask`/`search` (which the app's own calls bypass by pinning `--vault`) resolves to the same vault the dashboard shows. The Welcome screen offers **"Open your Obsidian vault: \<name\>"**, and the `Vault > Open Vault…` panel (Cmd+Shift+O) validates the chosen folder is a real Obsidian vault (has `.obsidian/`, via `VaultManager.isObsidianVault`) and warns when it isn't the one Obsidian has open. The window/sidebar title shows the active vault name. The window is a `NavigationSplitView` whose sidebar leads with **Home** (the default screen) and groups the five power-user tabs under an **Advanced** section (`DashboardTab` in `ContentView.swift`):

| Tab | View | Purpose |
|-----|------|---------|
| **Home** (default) | HomeView.swift | Consolidated common-case surface: Vault card (name/path + an Obsidian-match badge confirming this is the vault Obsidian has open, plus an Obsidian-plugin row showing the installed plugin version with an Install/Update button that shells `2nb plugin install`; `ObsidianPlugin`/`HomePlugin`), AI card (AWS Bedrock + Claude Haiku 4.5 + Amazon Nova-2 with a ready/not-ready dot and Save-as-default / Test buttons), a **Claude Code card** (`HomeSkill`/`HomeMCPConfigured`) with a skill-installed row (Install button shelling `2nb skills install claude-code --user`, from `2nb skills list --json`) and an MCP-server-configured row (from `2nb mcp configured --json`, with a Show-setup button that opens the snippet sheet; "configured" is the durable check since the server is launched on demand by the client), and Index card (doc + embedding counts with Rebuild Index / Re-embed All). An orange banner warns when the installed `2nb` CLI is older than the app (`CLIVersion`/`refreshCLIVersion`), since `brew upgrade --cask` bumps the app but not the `twonb` formula; when Homebrew is present (`BrewLocator`) the banner offers an Update CLI button that runs `brew upgrade apresai/tap/twonb` (`AppState.upgradeCLI`). The catalog/benchmark/MCP/git/lint depth lives under Advanced. |

Advanced section:

| Tab | View | Purpose |
|-----|------|---------|
| Vault Status | VaultStatusView.swift | Unified health: vault info, index coverage, portability, AI reachability, stale docs; Rebuild Index + Re-embed All |
| AI Settings | AIHubView.swift | AI Hub (see below) — providers, active models, full catalog |
| MCP Server | MCPStatusView.swift | A durable "Configured in ~/.claude.json" banner (from `2nb mcp configured --json`, via `HomeMCPConfigured`) above live MCP server processes + recent tool invocations; polls `2nb mcp status --json` every 5s. The banner answers "is it set up?" even when no server is running (the client launches it on demand), and the empty state distinguishes configured-but-idle from not-configured |
| Git Integration | GitActivityView.swift | Recent commits (1/3/7/30-day window); click a row → `CommitDetailView` split pane (file list + per-file diff) |
| Validation | LintResultsView.swift | Shells out to `2nb lint --json` and renders findings |

Supporting views: `MCPSetupView` (MCP config snippets for AI tools), `ModelCatalogPickerView` (per-model detail / test / benchmark, opened from the AI Hub), `IndexProgressView` (rebuild confirmation → progress → stats), `MergeConflictView` / `DiffView` (reusable Myers LCS unified diff), `PreferencesView` (Cmd+,). `AppDelegate.swift` renames the default File menu to "Notes".

### Menus & Shortcuts

- **Vault** menu: New Vault, Open Vault (Cmd+Shift+O), Reveal Vault in Finder, Vault Status, Rebuild Index, Validate Vault, Import Obsidian Vault, Export to Obsidian.
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

A thin wrapper that shells out to the `2nb` CLI; Obsidian remains the editor. Command-palette prefix is **"2ndbrain AI:"**. Commands: Open chat, Semantic Search, Ask AI (RAG Q&A), Find Similar Notes, Rebuild AI Index, and Setup wizard. A **ribbon icon** (custom head-with-brain mark matching the app icon, registered via `addIcon`) toggles a right-sidebar **chat panel** (`ChatView extends ItemView`, view type `2ndbrain-chat`) holding a true multi-turn conversation: each message passes prior turns to `2nb ask --json --history -` via stdin (capped client-side by `trimChatHistory`, mirroring `ai.TrimHistory`) and renders the answer, degradation `warnings`, and source chips via a renderer shared with the Ask AI modal; a pre-`--history` CLI degrades to single-shot with an upgrade hint. It can **download and manage the `2nb` binary itself** (macOS only; resolves the latest GitHub release tag at runtime, ad-hoc signs it, and strips the quarantine xattr because the release isn't notarized) and opens a **first-run setup wizard** (Download CLI → Connect AI → Index).

Install via **BRAT** (`apresai/2ndbrain`) or copy `manifest.json` / `main.js` / `styles.css` from a GitHub release, with **no npm build needed** by end users. Settings: "Download / update CLI", "2nb CLI Path" (defaults to `2nb`; probes Homebrew + `~/go/bin` + PATH), a read-only **"Vault"** line (open Obsidian vault path + index state), a **"Claude Code skill"** row (installed-status with an Install button that shells `2nb skills install claude-code --user`), and a **"Claude Code MCP server"** row (configured-status from `2nb mcp configured`, with a Copy-setup-snippet button; "configured" is the durable check since the server is launched on demand by the client). Every CLI call is **pinned to the open Obsidian vault via `--vault adapter.getBasePath()`** (`pinVaultArgs`), so 2nb can never resolve a different vault from `~/.2ndbrain-active-vault` or cwd, keeping the Obsidian vault and the 2nb vault joined. Source of record: `plugins/obsidian-2ndbrain/main.ts`.

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
