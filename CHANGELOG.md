# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/).

## [Unreleased]

### Added
- `2nb plugin install` (alias `update`) installs or updates the Obsidian plugin into the open vault by downloading the latest release's `manifest.json`/`main.js`/`styles.css` into `.obsidian/plugins/obsidian-2ndbrain/`; `2nb plugin status` reports the installed plugin version vs the CLI (`--json`). The manifest is written last so a partial install never looks complete; enabling the plugin in Obsidian remains the one manual step

## [0.5.15] - 2026-06-10

### Added
- `2nb ask --history <path|->` enables true multi-turn conversations: prior turns (JSON `[{role, content}]`, `-` for stdin) condense follow-up questions into standalone retrieval queries (reported as `rewritten_query` in `--json`) and ground the answer (#25)
- `2nb chat` interactive multi-turn REPL over the same RAG pipeline as `ask --history`; conversation lives in-process only (#26)
- Obsidian plugin: ribbon icon (custom head-with-brain mark) toggles a right-sidebar vault-chat panel (#24)
- Obsidian plugin: chat panel holds a true multi-turn conversation, passing prior turns to `2nb ask --history -` via stdin with client-side history trimming; renders answers, degradation warnings, and source chips; degrades to single-shot with an upgrade hint on pre-`--history` CLIs (#27)


## [0.5.14] - 2026-06-09

### Added
- Semantic-search playbook and accuracy fixes in the generated 2nb SKILL.md for coding agents (#21)

### Changed
- `--vault` is now a true one-shot override: it no longer persists as the active vault for later commands (#23)
- Provider-disable and `config set` docs aligned with actual config-write behavior

### Fixed
- Unified vault resolution so all commands resolve the active vault through the same path (#23)
- AI config writes are now atomic and validated, preventing partial or corrupt `config.yaml` updates (#22)


## [0.5.13] - 2026-06-08

### Changed
- **The dashboard now keeps the terminal `2nb` pointed at the same vault.** When the app binds a vault (on launch, or via Open Vault), it now also sets that vault as the CLI's active vault, so a bare `2nb ask`/`search` in the terminal (with no `--vault`) resolves to the same vault the dashboard shows. Previously the app pinned the vault only for its own calls, so the terminal CLI could drift to a different vault.

### Fixed
- **The test suite no longer overwrites your active-vault setting.** Running `2nb`'s own test suite (a developer action) could clobber `~/.2ndbrain-active-vault`, which made a bare terminal `2nb ask` resolve to the wrong place. The tests are now fully sandboxed. No effect on normal use; included for completeness.

## [0.5.12] - 2026-06-07

### Fixed
- **Empty notes no longer show as a gap in the embedding status.** A blank (0-byte) note can't be embedded, so the dashboard and `2nb ai status` / `vault status` used to read "115 / 117" with a "2 empty notes skipped" caveat. The embedding ratio now counts only documents that have content, so a vault with blank notes reads "115 / 115" with a clean "OK," and `2nb` no longer keeps suggesting "run index" for notes it will always skip. Empty notes stay in the index, so links to empty stub notes still resolve.

### Changed
- **Release CI runs on Node 24.** Bumped the GitHub Actions in the release workflow (`actions/checkout`, `setup-go`, `setup-node`, `goreleaser-action`) to their current major versions ahead of GitHub's June 2026 removal of Node 20. No effect on the published CLI or app; build-side maintenance only.

## [0.5.11] - 2026-06-07

### Fixed
- **Embeddings status no longer reads "Stale" forever when a vault has empty notes.** A blank (0-byte) note — e.g. Obsidian's default `Untitled.md` — can't be embedded (Amazon Nova-2 rejects empty input), so it was permanently counted as "missing an embedding," leaving the dashboard stuck on "Stale" with the dead-end advice to run `2nb index` (which just skips it again). The status now treats empty notes as deliberately skipped: a vault whose only unembedded documents are empty notes reports a healthy "OK" with a one-line "N empty notes skipped" explanation instead of a false "Stale," and the "catch up" advice only appears when documents with real content are genuinely missing embeddings. `2nb ai status --json` gains a `vault_empty_docs` field.

## [0.5.10] - 2026-06-07

### Changed
- **The macOS app is now Apple-notarized — no more Gatekeeper warning on launch.** Previously the app shipped ad-hoc signed, so macOS showed an "Apple could not verify… / Move to Trash" dialog and you had to right-click → Open (or strip the quarantine attribute) to run it. The app is now Developer ID-signed and notarized by Apple, so `brew install --cask apresai/tap/secondbrain` installs an app that launches cleanly with no prompt. The project stays fully open source; signing happens on the maintainer's machine and no signing keys live in CI.

### Fixed
- **Release builds start from a clean app bundle.** `build-app-release` now removes any stale bundle before assembling, so a leftover file can't leak into a signed/notarized artifact.

## [0.5.9] - 2026-06-07

### Fixed
- **GUI now shows the real reason a `2nb` action failed, not flag-help noise.** When a command failed at runtime (e.g. a re-embed that couldn't complete), the CLI printed the error followed by its entire flag listing, and the macOS app — which scrapes the last line of stderr — displayed a stray flag description ("--yaml … Output as YAML") instead of the actual error. The CLI now sets cobra's `SilenceUsage`, so a runtime failure prints only the error message (and its "To fix" hints); genuine bad-flag mistakes still surface a clear "Error: unknown flag …" line.

### Added
- **macOS app warns when your `2nb` CLI is older than the app.** `brew upgrade --cask secondbrain` bumps the app but not the `twonb` formula, so you could silently run a new app against an old CLI — which is what made a re-embed fail with no obvious cause. Home now shows an orange banner when the installed CLI is behind, with the `brew upgrade apresai/tap/twonb` command to fix it.

## [0.5.8] - 2026-06-07

### Changed
- **macOS app: saving the Bedrock default now nudges you to re-embed when your stored embeddings no longer match.** If "Save as default" leaves the vault embedded with a different model or dimension (`dimension_break` / `mixed` / `model_mismatch`), the confirmation gently points you at **Re-embed All** instead of a plain "Saved." The wording is honest across all three cases: a dimension break drops you to keyword-only search, while a same-dimension mismatch keeps semantic search running on stale-model vectors (less accurate, not off).
- **macOS app: the index sheet title now stays "Re-embed All" for the whole run.** It previously reverted to "Rebuild Index" mid-run because the flag it read was cleared the moment the run started; the run mode is now carried on the progress state so the title, warning, and confirm copy stay accurate through every phase.

### Fixed
- **macOS app: a dashboard tab can no longer silently drop out of the sidebar.** A new parity test asserts the Home tab plus the Advanced group cover every `DashboardTab` case exactly once and that each tab has an icon, so a case added to the enum but forgotten in the sidebar is caught at test time.

## [0.5.7] - 2026-06-07

### Changed
- **macOS app: "Re-embed All" now warns it's a paid full regeneration.** The Rebuild confirmation sheet showed identical copy for an incremental Rebuild and a full Re-embed; it now reads "Re-embed All" with an orange note that it re-runs paid embedding calls for every document.
- **macOS app: every CLI failure is recorded to the per-vault `.2ndbrain/logs/` file.** Previously only index rebuilds wrote there; now any failed `2nb` action (Save, Test, etc.) logs argv + exit code + stderr to `secondbrain.log`, so the "read the logs to debug" workflow is complete.

## [0.5.6] - 2026-06-07

### Fixed
- **macOS app now shows *why* a CLI action failed.** Every `2nb` call that exits non-zero previously surfaced a useless "CLI exited with code 1" — so a failed Save/Test on the Home screen (or any AI Hub action) told you nothing. `CLIError.nonZeroExit` now carries the trimmed `2nb` stderr, so the actual reason (e.g. "bedrock not ready: AccessDeniedException…") reaches the error banner; an empty stderr still falls back to the exit code. Home also clears a stale failure message when you start a new Rebuild / Re-embed.

## [0.5.5] - 2026-06-07

### Changed
- **macOS app: a consolidated Home screen is now the default.** Home answers the three common-case questions on one surface — is this the vault Obsidian has open (a match badge), is AI set up and working (AWS Bedrock + Claude Haiku 4.5 + Amazon Nova-2, with a ready dot plus Save-as-default and Test buttons), and is the vault indexed (doc/embedding counts with Rebuild Index / Re-embed All). The five existing tabs (Vault Status, AI Settings, MCP Server, Git Integration, Validation) move under an **Advanced** sidebar section; nothing is removed.

### Fixed
- **Rebuild Index no longer hangs, and a vault with empty notes indexes cleanly.** Two bugs compounded: (1) `2nb index` tried to embed empty/whitespace-only notes (e.g. a blank `Untitled.md`), which Amazon Nova-2 rejects with a 400 `ValidationException` (`minLength: 1`) — so the embed count stayed pinned below 100% and `--force-reembed` reported "incomplete"; (2) the macOS app's `startIndex` blocked the main actor with `process.waitUntilExit()` and had no guard against overlapping runs, so the rebuild-progress sheet could freeze on "Running…" and never reach "Done". The CLI now **skips** empty documents (counted as skipped, not failed; nothing is sent to the provider), and the app runs `2nb index` without blocking the main actor, guards against concurrent rebuilds, keys the terminal phase off the process exit code, and surfaces the actual CLI error (not a bare exit code) on failure.

## [0.5.4] - 2026-06-07

### Fixed
- **macOS app AI now works.** A GUI app launched by launchd has no shell environment, so the user's AWS credentials (shell env vars, no `~/.aws/credentials`) were invisible to the `2nb` it spawned and every AI action failed with "bedrock not ready", while the CLI worked in a terminal. The Amazon Bedrock **API key** (bearer token) can now be stored env-independently with `2nb config set-key bedrock <token>` (macOS Keychain); `loadBedrockAWSConfig` exports it for the AWS SDK when `AWS_BEARER_TOKEN_BEDROCK` isn't already set (macOS only; env wins; SigV4 fallback unchanged).

### Changed
- macOS app: `SpotlightIndexer.indexAll` runs its file-read + YAML-parse loop on a background queue instead of the main thread, so opening a large vault no longer freezes the UI.
- `FrontmatterParser` bounds its YAML-AST walk with a recursion-depth guard against pathologically deep frontmatter.

## [0.5.3] - 2026-06-06

### Fixed
- macOS app no longer crashes on launch when indexing a vault that contains Obsidian **template files**. `FrontmatterParser` used `Yams.load`, whose YAML constructor traps (an uncatchable `fatalError`) on template placeholders like `date: {{date}}`; since indexing runs during vault open, a single template note crashed the whole app. The parser now walks the YAML AST (`Yams.compose`) directly, which can't trap, while preserving scalar type fidelity.

## [0.5.2] - 2026-06-06

The Obsidian vault and the 2nb vault are now joined at the hip: every client operates on the vault you have open in Obsidian, never a different one.

### Changed
- **Obsidian plugin** pins every `2nb` command to the open Obsidian vault via `--vault`, so it can no longer resolve a different vault from a stale active-vault file or the working directory. Settings and the setup wizard now show the bound vault and its index state.
- **macOS app** binds to the vault Obsidian currently has open (read from Obsidian's own `obsidian.json`) on launch, leads the Welcome screen with "Open your Obsidian vault", validates that an opened folder is a real Obsidian vault (has `.obsidian/`) and warns when it isn't the one Obsidian has open, and shows the active vault name in the sidebar.

### Removed
- The Obsidian plugin's "Custom Vault Path" setting — it was the only way the Obsidian vault and the 2nb vault could diverge.

## [0.5.1] - 2026-06-06

### Fixed
- `2nb lint` now recurses into subdirectories. The previous top-level `*.md` glob silently checked only files in the vault root and skipped every note (and every broken wikilink) in a subfolder.
- `2nb lint` no longer reports a missing frontmatter `id` as an error, consistent with the path-based identity model (a document's identity is its path; `id` is read if present but never required).
- `2nb lint` skips Obsidian template files whose frontmatter holds unresolved `{{placeholder}}` tokens, so template scaffolding no longer produces false-positive parse errors.

## [0.5.0] - 2026-06-06

Obsidian-native pivot: Obsidian stays the editor, the `2nb` CLI plus MCP server is the engine, and the macOS app becomes a configuration dashboard. Notes are never rewritten; all derived state lives in a gitignored `.2ndbrain/` sidecar.

### Added
- Read-only indexing of `.canvas` (JSON Canvas) and `.base` (YAML Bases) files as synthetic views; `meta` and `kb_update_meta` refuse to write them.
- Obsidian Flavored Markdown parsing: embeds, `[[note#^block]]` block references, `^block-id` definitions, inline `#tags`, `%% comment %%` stripping, and markdown-link extraction.
- `2nb migrate` to upgrade a legacy 2ndbrain vault to the Obsidian-native format (schema v3), with `--dry-run`; source markdown is never modified.
- Automatic `.obsidian` vault detection and sidecar creation, shortest-unique-path plus alias resolution, and YAML-AST frontmatter preservation.
- Schema v3: `aliases` table and `block_id` columns on `chunks` and `links`.
- Obsidian plugin (`obsidian-2ndbrain`): a thin wrapper over `2nb` that downloads and manages the CLI binary itself, ships a first-run setup wizard, and installs via BRAT with no npm build. Release CI now publishes plugin assets and `versions.json`.
- LLM-facing test battery (real-stdio MCP, migrate, RAG, canvas/base), JSON-envelope contract tests, and OFM unit tests.

### Changed
- AWS Bedrock is now the default provider: Claude Haiku 4.5 for generation and Amazon Nova-2 for embeddings.
- Ollama and OpenRouter are opt-in (disabled by default); the setup wizard enables them.
- Path-based identity: UUIDs in frontmatter are read for backward compatibility but never written, generated, or required.
- The macOS app is now a `NavigationSplitView` configuration and companion dashboard rather than an editor.
- Documentation (getting-started, user guide, plugin README, `CLAUDE.md`) synced to the shipped behavior.

### Removed
- Editor views from the macOS app (editor area, graph, sidebar, tabs, search panel, and related surfaces).
- The dead `embedding:` configuration block.

## [0.4.3] - 2026-05-17

### Fixed
- Release workflow now stages `VERSION` and `Version.swift` in the release commit, preventing version drift between tagged releases and source files.


## [0.4.2] - 2026-05-17

### Added
- AI Hub model catalog picker (`ModelCatalogPickerView`) with sidebar+detail layout, filters (type/provider/tier/enabled/tested/compatible), and Best/Cheapest/Fastest/Newest/Name sort modes
- Retrieval-quality probe for `models bench` that scores stored embeddings against resolved wikilinks (MRR@K, Recall@K) with zero API cost
- `SecondBrainCore.LineBuffer` utility extracted for streaming line buffering
- Extensive test coverage across CLI and app: AI Hub contract, core commands contract, catalog merge, user catalog, bench probes, MCP tools, force-reembed, embedding store, JSON decoding, wizard logic, view construction

### Changed
- Claude model defaults bumped to 4.6/4.7
- Safer `models test`/`bench`/`set-active` flows: `Set Active` gated on indexing state to prevent mixed-model embeddings; benchmark and cost-preview paths hardened
- CLAUDE.md trimmed and reorganized

### Removed
- Claude 3.5 entries dropped from default catalog


## [0.4.1] - 2026-04-24

### Fixed

- AI Hub: vendor batch enable/disable now correctly applies to discovered-only models that lack a builtin catalog entry


## [0.4.0] - 2026-04-24

### Added
- AI Hub catalog now groups models by vendor with collapsible disclosure sections showing model counts
- Search field in AI Hub filters the model catalog by model ID or vendor name
- Bulk "Enable all" / "Disable all" buttons per vendor group in AI Hub catalog
- Contract test suite verifying CLI output matches Swift decoder expectations for AI Hub


## [0.3.1] - 2026-04-24

### Fixed

- **AI Hub provider toggles**: `config set` now accepts `ai.bedrock.disabled`, `ai.openrouter.disabled`, and `ai.ollama.disabled` keys, enabling the AI Hub enable/disable provider cards to persist correctly
- **Log message redaction**: Unredacted AI Hub action logs in the macOS app (model IDs, provider names, CLI commands, error output were being suppressed by the OS privacy filter)


## [0.3.0] - 2026-04-24

## [0.3.0] - 2026-04-24

### Added
- **AI Hub** — unified AI configuration surface (AI menu > AI… · Cmd+Shift+,) combining provider setup, model wizard, and connection testing into a single panel with provider cards, active model selectors, and a full model catalog with Test/Set active/Enable/Disable/Discover actions

### Removed
- AI Setup Wizard, Model Wizard, and Test AI Connection as separate menu items/views (replaced by AI Hub)


## [0.2.18] - 2026-04-24

## [0.2.18] - 2026-04-24

### Fixed
- Model Wizard: prevent double-tap from triggering duplicate actions

### Changed
- Expanded Bedrock Converse model allowlist to support additional models


## [0.2.17] - 2026-04-24

## [0.2.17] - 2026-04-24

### Fixed
- GUI pipe-buffer deadlock that caused the app to hang when CLI commands produced large output


## [0.2.16] - 2026-04-24

## [0.2.16] - 2026-04-24

### Added
- `models wizard` CLI command — interactive end-to-end provider → discover → pick → cost preview → test → save flow with `--json` streaming events for GUI/automation
- Model Wizard panel in macOS editor (AI menu) — grouped model list with tier badges, scope picker, cost preview, and test-and-save flow
- `models cost-preview` command — estimate USD cost of running probes across one or more models without API calls
- Invoke strategy system — `InvokeStrategy` field on catalog entries routes models to the correct API dialect; adding new model variants no longer requires code changes
- Bedrock Converse strategy (`bedrock_converse`) as a first-class invoke target alongside existing provider-specific strategies
- Retrieval-quality probe — scores stored embeddings via MRR@K and Recall@K over resolved wikilink pairs at zero API cost
- Live catalog sync in macOS editor — CLI writes to the model catalog refresh the UI automatically via FSEvents without reopening the vault
- Enable/disable toggle for models — `models enable` / `models disable` commands hide models from selection dropdowns; `models list --enabled-only` filters accordingly

### Fixed
- MCP server: path traversal vulnerability in tool handlers
- Tag parsing: edge cases in frontmatter tag normalization
- Schema migration: data-integrity issue during version upgrades
- Merge conflict view: observer memory leak
- Graph traversal: duplicate node/edge deduplication
- `purgeStale`: stale document removal correctness
- OpenRouter: env variable resolution for API key
- Document create: missing transaction boundary
- Duplicate shortcut: filename collision on rapid invocation
- `import-obsidian`: now correctly honors the active vault instead of defaulting to cwd


## [0.2.15] - 2026-04-23

### Fixed

- Bedrock live pricing now correctly resolves model IDs to AWS offer file entries, fixing cases where pricing showed "unknown" for supported models (Nova, Titan, Cohere, TwelveLabs Marengo, and cross-region inference profiles)


## [0.2.14] - 2026-04-23

Based on the diff analysis, here is the CHANGELOG entry:

```markdown
## [0.2.14] - 2026-04-23

### Fixed
- Bedrock model discovery now skips legacy and lifecycle-blocked foundation models correctly
- Model type (embedding vs. generation) is now detected from `FoundationModelDetails` instead of inferred from model class, improving accuracy for text and multimodal models
- Bedrock `--discover` no longer includes non-text-input models in results
```


## [0.2.13] - 2026-04-22

### Added
- TwelveLabs Marengo embedding models via Bedrock InvokeModel (`models add --provider bedrock --type embedding --price-request`)
- Live pricing fetched from OpenRouter `/models` API and AWS pricing offer files with 24h disk cache
- Per-model pricing overrides via `models add` flags (`--price-in`, `--price-out`, `--price-request`)

### Changed
- `models list` and `ai status` now display live pricing data; falls back to stale cache then builtin metadata in air-gapped environments
- Bedrock provider expanded to support embedding model invocations

### Fixed
- Frontmatter parsing edge cases


## [0.2.12] - 2026-04-22

### Added
- Live pricing for `models list`, `ai status`, and `index` — fetched from OpenRouter and AWS pricing APIs with a 24-hour disk cache (`~/Library/Caches/2nb/pricing`); falls back to stale cache then builtin metadata when offline
- TwelveLabs Marengo embed family support via Bedrock InvokeModel (Marengo 2.7 and 3.0 request/response formats); add via `2nb models add <model-id> --provider bedrock --type embedding --price-request <USD>`
- `--price-request` flag on `models add` for per-request priced embedding models

### Fixed
- Bedrock model discovery failures for reasoning models, system prompts, variant IDs, and embedding formats


## [0.2.11] - 2026-04-22

### Fixed

- **Bedrock discovery**: context-window variant IDs (e.g. `model:0:24k`, `model:0:512`) are no longer returned as invokable models — they 404 when called directly
- **Bedrock discovery**: inference profiles are now type-classified correctly instead of being hardcoded as `generation`
- **Bedrock generation**: reasoning models (e.g. DeepSeek R1) that emit non-text content blocks first now extract the text response correctly
- **Bedrock generation**: models that reject system prompts now get a transparent retry without one, cached per process
- **Bedrock embeddings**: Cohere Embed v4's `{"embeddings": {"float": [...]}}` response shape is now parsed correctly alongside v3's flat array format
- **Bedrock embeddings**: inference profile geo-prefixed IDs (`us./eu./ap./global.`) are stripped before embed format detection; Titan image models now return a clear unsupported error instead of silently failing


## [0.2.10] - 2026-04-22

Now I have a complete picture of the changes. Here's the changelog entry:

```markdown
## [0.2.10] - 2026-04-22

### Added
- Bedrock `--discover` now merges system-defined inference profiles (`us.*`, `eu.*`, `ap.*`, `global.*`) with foundation models, returning the correct invokable IDs for newer Claude and Nova generation models
- Bedrock embedder supports multiple embedding API formats — Nova v2 (default), Titan v1, Titan v2, and Cohere v3 (batched, ≤96 texts per call) — detected automatically from the model ID
- New verified embedding models: `amazon.titan-embed-text-v2:0` (256/512/1024 configurable dims) and `cohere.embed-english-v3` / `cohere.embed-multilingual-v3`
- New verified generation models: Claude Haiku 4.5, Sonnet 4, and Opus 4 via cross-region Bedrock inference profile IDs (`us.anthropic.claude-*`)
- Bedrock generator retries automatically without temperature when a model rejects it (e.g. Claude Opus 4.7), caching the result for the process lifetime
```


## [0.2.9] - 2026-04-22

## [0.2.9] - 2026-04-22

### Added
- `models list --discover --promote` flag: discovered models that pass concurrent smoke-testing are automatically added to the user catalog


## [0.2.8] - 2026-04-22

### Changed
- `GenOpts.Temperature` is now `*float64`; pass `nil` to omit temperature from the request (model uses its default), or use `ai.Ptr(value)` to set an explicit value

### Fixed
- Bedrock generation no longer fails permanently when a model rejects the `temperature` inference parameter; the generator retries once without temperature and caches the result for the process lifetime


## [0.2.7] - 2026-04-22

### Fixed

- Bedrock provider no longer sends `Temperature` in `InferenceConfiguration` when not explicitly set, preventing API errors on models that reject a zero-value temperature field


## [0.2.6] - 2026-04-21

### Fixed
- `completion install` now works when gcloud, Homebrew, or other tools run `compinit` before 2ndbrain's completion block


## [0.2.5] - 2026-04-21

### Changed
- `completion install` is now hardened for real-world zsh setups: handles existing `fpath` entries, early-return guards in `.zshrc`, missing completion directories, and multiple `2nb` binaries on PATH

### Added
- Test suite for `completion install` covering edge cases in `.zshrc` parsing and completion directory detection


## [0.2.4] - 2026-04-21

## [0.2.4] - 2026-04-21

### Added
- `completion install` now automatically updates `~/.zshrc` with the required `fpath` entry and `compinit` block — no manual shell config edits needed after running the command
- Golden-path E2E battery test suite covering core CLI workflows (`cli/battery_test.go`)
- GUI test scripts for polish diff flow and vault-switch persistence

### Changed
- Updated `2ndbrain-skill.md` agent skill content with expanded MCP vs CLI guidance and test battery design


## [0.2.3] - 2026-04-19

### Fixed
- Frontmatter parser edge cases causing incorrect document metadata extraction
- Database migration reliability for vault upgrades
- MCP server tool responses for `kb_related`, `kb_search`, and `kb_index`
- Graph traversal returning incorrect depth results
- Swift `VaultManager` initialization sequence causing intermittent vault open failures

### Added
- AI provider availability tracking with per-provider health checks
- Rate limiting for AI provider requests to prevent throttling errors


## [0.2.2] - 2026-04-19

### Added
- `models calibrate` command: samples random document pairs from the active vault, computes cosine similarity distribution (p50/p90/p95/p99), and recommends a threshold at p95+0.01; supports `--samples`, `--save`, `--scope`, and `--seed` flags
- Per-model recommended similarity thresholds in the built-in model catalog (Nova-2: 0.65, Nemotron-VL: 0.60, nomic-embed-text: 0.50, mxbai/snowflake/bge-m3: 0.55, all-minilm: 0.35)
- `--similarity-threshold` flag on `models add` to persist a custom threshold to the user catalog
- `models list` now shows a THRESHOLD column with per-model recommendations
- `ai status` now reports the active similarity threshold and its source (vault config / user calibration / model recommendation / default)
- Search results now display RRF and cosine scores (`rrf=X.XXX, cos=Y.YYY`) inline for relevance transparency

### Changed
- Similarity threshold resolution follows a priority chain: vault `ai.similarity_threshold` → user catalog calibration → model's built-in recommendation → global default (0.20)


## [0.2.1] - 2026-04-19

## [0.2.1] - 2026-04-19

### Fixed
- Skills Install dialog now shows a persistent Close button in every installation state


## [0.2.0] - 2026-04-17

### Added
- **`vault` parent command** with five subcommands: `status`, `show`, `create`, `set`, `list`. Bare `2nb vault` prints a unified health report (docs, embedding coverage, portability, AI reachability, stale-doc count) mirroring the macOS editor's Vault Status panel.
- **`vault create`** initializes a new vault and activates it. Replaces `2nb init` (kept as a hidden deprecated alias).
- **`vault list`** shows recently-used vaults from `~/.2ndbrain-vaults`, with `*` marking the active one; stale paths are pruned on read.
- **State-aware next-step hint on bare `2nb`** — prints one of "create a note / ai setup / index / search" based on current vault state.
- **`2nb completion` command tree** — emitters for zsh / bash / fish / powershell plus a `completion install` subcommand that writes `~/.zsh/completions/_2nb` and prints the rc snippet to activate it.
- **Dynamic shell completion** on 15+ commands via Cobra `ValidArgsFunction` / `RegisterFlagCompletionFunc`: vault paths, markdown files (from the index), schema types/statuses, agent slugs, model IDs (filtered by `--provider`), AI providers, config keys, sort fields, bench probes, catalog scopes.
- **Homebrew auto-installs completions** — `brew install apresai/tap/twonb` now ships zsh/bash/fish completions via GoReleaser's `generate_completions_from_executable`.
- **Cobra `Example:` blocks** on `create`, `index`, `search`, `ask`, `list`, `read`, `ai setup`, `mcp-setup`, `skills install`, and the full `vault` subtree so `--help` shows real invocations.
- `ai.KnownProviders` and `settableConfigKeys` as canonical lists so provider names and config keys stay in sync across switch statements, error messages, and completion.
- `vault.FindVaultRoot` exported for read-only callers (e.g. completion) that need the vault root without paying a full `vault.Open`.

### Changed
- `2nb init` is a hidden deprecated alias that delegates to `vault create`; existing scripts keep working with a deprecation notice.
- Root `--help` gains a Quick Start block and an Examples section.
- `2nb` with no args uses `EmbeddingCounts()` in a single query instead of two separate `COUNT`s.
- `vault status` probes embedder and generator reachability concurrently, halving worst-case latency.
- `config get` / `config set` error message now reads from the canonical key list, so it stays accurate when keys are added.

### Fixed
- Shell completion for `config set`/`get` now suggests the keys the commands actually accept (`ai.bedrock.profile`, `ai.openrouter.api_key_env`, `ai.ollama.endpoint`, and so on) — previously the completion list had drifted from `setConfigValue`.
- Schema completers (`create --type`, `list --type`, `--status`, `meta --set`) bypass the full vault open and read `schemas.yaml` directly, so tab presses never run SQLite migrations or emit config-self-heal stderr.

## [0.1.16] - 2026-04-17

### Added
- `models add` command to add custom models to a user-maintained catalog (`~/.config/2nb/models.yaml` or per-vault `.2ndbrain/models.yaml`); entries appear in `models list` as `tier=user_verified` and in the AI setup wizard's Custom mode picker
- `models remove` command to remove models from the user catalog by model ID, provider, and scope
- User catalog layer merged into `BuildModelList`, supporting both global and per-vault scopes with conflict resolution


## [0.1.15] - 2026-04-17

### Added
- Obsidian-style force-directed document graph view with canvas renderer, inspector panel, zoom/pan/drag, hover and selection highlighting, and Barnes-Hut quadtree simulation (O(n log n) at scale)
- Graph inspector panel with mode, filter, force, and color-group controls; global/local view modes
- Vault Status panel (Vault menu) — unified health view showing index state, embedding portability, stale docs, and provider reachability with Rebuild Index and Re-embed All actions
- AI Test Connection panel (AI menu) — live model probe with latency display and link to AI Setup on failure

### Changed
- Menu bar reorganized into Notes, Vault, and AI menus; File menu renamed to Notes
- Preview mode is now read-only; removed the editable preview round-trip

### Removed
- Editable preview (Turndown.js contenteditable round-trip) — corrupted markdown containing Mermaid diagrams and produced WebKit rendering artifacts


## [0.1.14] - 2026-04-16

### Added
- Commit detail view: click any commit in Git Activity to see changed files and per-file unified diffs
- `2nb git show <hash>` CLI command with `--json` support
- Outline panel click-to-scroll navigation in the editor
- Syntax highlighting for fenced code blocks in the editor
- Wikilink parsing and location resolver with heading anchor support

### Fixed
- `git.Show()` mishandled pathological filenames and git-quoted paths
- Race condition in commit detail diff loading
- Sidebar selection reliability after document operations
- Tab bar dirty-state indicator edge cases


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
