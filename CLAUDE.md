# 2ndbrain

Obsidian-native AI companion. **Obsidian stays your editor**; the Go CLI (`2nb`) + MCP server are the engine that indexes, searches, and answers (RAG) over a real Obsidian vault. A thin Obsidian plugin and a macOS configuration dashboard wrap the CLI. `2nb` writes only a gitignored `.2ndbrain/` sidecar and never rewrites your markdown.

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

Format: `major.minor.build`. Single source of truth: `VERSION` file at repo root. The Go CLI reads it via `cli/Makefile` LDFLAGS into `internal/cli.Version`; the Swift app generates `app/Sources/SecondBrain/Version.swift` (never edit by hand).

Bump targets (root `Makefile`): `make bump-build` (`0.1.0` → `0.1.1`), `make bump-minor` (`0.1.1` → `0.2.0`), `make bump-major` (`0.2.0` → `1.0.0`). Each regenerates `Version.swift`.

## Release

Both the CLI and the macOS app are published via Homebrew:

```bash
brew install apresai/tap/2nb                    # CLI only
brew install --cask apresai/tap/secondbrain     # macOS dashboard app (depends on CLI)
```

### Pipeline

1. `make bump-build` (or `bump-minor`/`bump-major`) — increment `VERSION`, regenerate `Version.swift`.
2. `make release` — updates `CHANGELOG.md`, commits, tags `v<VERSION>`, pushes tag.
3. GitHub Actions (`.github/workflows/release.yml`) on tag push: macos-latest arm64, CGO_ENABLED=1; GoReleaser builds CLI for arm64+x86_64 and pushes formula `twonb.rb` to `apresai/homebrew-tap`; Swift builds `SecondBrain.app`, signs it with the **Developer ID Application** cert + hardened runtime, **notarizes via `notarytool` and staples the ticket** (when the `MACOS_DEVID_*` / `ASC_NOTARY_*` secrets are present — ad-hoc fallback otherwise), packages as `SecondBrain-<VERSION>-arm64.zip`, pushes cask `secondbrain.rb`.
4. `make release-local` — same flow locally, CLI only (no app build).

Key files: `.goreleaser.yaml`, `.github/workflows/release.yml`, `casks/secondbrain.rb.tmpl` (with `CASK_VERSION`/`CASK_SHA256` tokens), `scripts/update-changelog.sh`, `CHANGELOG.md`.

The `apresai` GitHub environment provides `HOMEBREW_TAP_TOKEN` (PAT for `apresai/homebrew-tap`) and the macOS code-signing/notarization secrets: `MACOS_DEVID_P12_BASE64` + `MACOS_DEVID_P12_PASSWORD` (the Developer ID Application cert+key), `MACOS_KEYCHAIN_PASSWORD` (throwaway, for the ephemeral CI keychain), and `ASC_NOTARY_KEY_P8_BASE64` + `ASC_NOTARY_KEY_ID` + `ASC_NOTARY_ISSUER_ID` (App Store Connect API key for `notarytool`). If the Developer ID secret is absent the release step degrades to ad-hoc signing and skips notarization.

The macOS app is distributed as an arm64 **Developer ID-signed, Apple-notarized** `.app` bundle, so it launches with no Gatekeeper prompt even when Homebrew's cask install quarantines it. (Local debug builds via the Makefile are still ad-hoc signed; that's separate from the release pipeline.) The cask `depends_on formula: "apresai/tap/twonb"` because the app shells out to `2nb` for AI/indexing/lint; note that a cask upgrade does **not** bump the formula, so the app warns on Home when the CLI has drifted behind.

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

### CLI Commands (52)

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
| `migrate` | Migrate a legacy 2ndbrain vault to the Obsidian-native format (schema v3); `--dry-run` previews without modifying. Non-mutating: source markdown is never changed. |
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

### AI Providers

The default provider is **AWS Bedrock** (via your AWS credentials): generation = Claude Haiku 4.5 (`us.anthropic.claude-haiku-4-5-20251001-v1:0`), embeddings = Amazon Nova-2 (`amazon.nova-2-multimodal-embeddings-v1:0`, 1024 dims). Defaults live in `DefaultAIConfig()` (`cli/internal/ai/config.go`).

**Bedrock auth** uses the AWS SDK credential chain (SigV4 from env or `~/.aws`), **or** a Bedrock **API key (bearer token)**. The bearer token is normally the `AWS_BEARER_TOKEN_BEDROCK` env var, but a GUI app launched by launchd has no shell env — so `2nb config set-key bedrock <token>` stores it in the macOS Keychain and `loadBedrockAWSConfig` (`cli/internal/ai/bedrock.go`, `ensureBedrockBearerToken`) exports it for the SDK when the env var is unset (macOS only, env wins). The SDK **prefers a bearer token over SigV4**, so a stored key overrides `~/.aws` SigV4 creds for Bedrock — replace it by re-running `set-key`, or delete the `dev.apresai.2ndbrain`/`bedrock` item in Keychain Access to fall back to SigV4. This is how the macOS app reaches Bedrock without your shell's credentials.

**Ollama (local) and OpenRouter are opt-in**: both ship `disabled: true` in a fresh vault's `config.yaml`, so selection UIs show only Bedrock until the user enables them. `2nb ai setup` (a Bedrock-first wizard that detects AWS creds, confirms region, verifies models, and reminds you to enable Bedrock model access in the AWS console) or the macOS AI Hub clears the `disabled` flag. `Disabled` only hides a provider's models from dropdowns; an explicitly-chosen active provider still runs.

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

### MCP Server (16 tools)

Each `2nb mcp-server` writes a sidecar status file to `.2ndbrain/mcp/<pid>.json` (PID, start time, parent PID, last 50 invocations: tool, timestamp, duration, ok/error). The dashboard polls `2nb mcp status --json` every 5s. mark3labs/mcp-go has no client-connected hook, so sidecar files are the only enumeration mechanism.

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

## Swift macOS App (`app/`)

**Framework:** SwiftUI + AppKit, Swift 6.0, macOS 14+
**Dependencies:** GRDB.swift (SQLite), Yams (YAML), swift-markdown
**Architecture:** MVVM with `@Observable`

The macOS app is a **configuration and companion dashboard, not an editor**: Obsidian is the editor. It reads the same `.2ndbrain/index.db` the CLI writes (WAL mode) and shells out to `2nb` for all AI / index / lint / git work. On launch it **binds to the vault Obsidian currently has open** — read from Obsidian's own registry `~/Library/Application Support/obsidian/obsidian.json` via `ObsidianRegistry` (`SecondBrainCore/Vault/ObsidianRegistry.swift`) — so the dashboard and Obsidian stay on the same vault. The Welcome screen offers **"Open your Obsidian vault: \<name\>"**, and the `Vault > Open Vault…` panel (Cmd+Shift+O) validates the chosen folder is a real Obsidian vault (has `.obsidian/`, via `VaultManager.isObsidianVault`) and warns when it isn't the one Obsidian has open. The window/sidebar title shows the active vault name. The window is a `NavigationSplitView` whose sidebar leads with **Home** (the default screen) and groups the five power-user tabs under an **Advanced** section (`DashboardTab` in `ContentView.swift`):

| Tab | View | Purpose |
|-----|------|---------|
| **Home** (default) | HomeView.swift | Consolidated common-case surface: Vault card (name/path + an Obsidian-match badge confirming this is the vault Obsidian has open), AI card (AWS Bedrock + Claude Haiku 4.5 + Amazon Nova-2 with a ready/not-ready dot and Save-as-default / Test buttons), and Index card (doc + embedding counts with Rebuild Index / Re-embed All). An orange banner warns when the installed `2nb` CLI is older than the app (`CLIVersion`/`refreshCLIVersion`), since `brew upgrade --cask` bumps the app but not the `twonb` formula. The catalog/benchmark/MCP/git/lint depth lives under Advanced. |

Advanced section:

| Tab | View | Purpose |
|-----|------|---------|
| Vault Status | VaultStatusView.swift | Unified health: vault info, index coverage, portability, AI reachability, stale docs; Rebuild Index + Re-embed All |
| AI Settings | AIHubView.swift | AI Hub (see below) — providers, active models, full catalog |
| MCP Server | MCPStatusView.swift | Live MCP server processes + recent tool invocations; polls `2nb mcp status --json` every 5s |
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

A thin wrapper that shells out to the `2nb` CLI; Obsidian remains the editor. Command-palette prefix is **"2ndbrain AI:"**. Commands: Semantic Search, Ask AI (RAG Q&A), Find Similar Notes, Rebuild AI Index, and Setup wizard. It can **download and manage the `2nb` binary itself** (macOS only; resolves the latest GitHub release tag at runtime, ad-hoc signs it, and strips the quarantine xattr because the release isn't notarized) and opens a **first-run setup wizard** (Download CLI → Connect AI → Index).

Install via **BRAT** (`apresai/2ndbrain`) or copy `manifest.json` / `main.js` / `styles.css` from a GitHub release, with **no npm build needed** by end users. Settings: "Download / update CLI", "2nb CLI Path" (defaults to `2nb`; probes Homebrew + `~/go/bin` + PATH), and a read-only **"Vault"** line (open Obsidian vault path + index state). Every CLI call is **pinned to the open Obsidian vault via `--vault adapter.getBasePath()`** (`pinVaultArgs`), so 2nb can never resolve a different vault from `~/.2ndbrain-active-vault` or cwd — the Obsidian vault and the 2nb vault stay joined. Source of record: `plugins/obsidian-2ndbrain/main.ts`.

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
