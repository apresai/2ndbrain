# macOS App (SecondBrain) Reference

The reference for the Swift macOS app in `app/`: the SecondBrain configuration and companion dashboard. It covers the app's architecture, vault binding (including the CLI's active-vault resolution and write guards, which the app and CLI share), every dashboard tab, the Settings window, the AI Hub, and GUI test automation. The macOS SwiftUI platform gotchas and Context7 library IDs stay in the repo root `CLAUDE.md`.

**Framework:** SwiftUI + AppKit, Swift 6.0, macOS 14+
**Dependencies:** GRDB.swift (SQLite), Yams (YAML), swift-markdown
**Architecture:** MVVM with `@Observable`

## Role: Configuration Dashboard, Not an Editor

The macOS app is a configuration and companion dashboard, not an editor: Obsidian is the editor. It reads the same `.2ndbrain/index.db` the CLI writes (WAL mode) and shells out to `2nb` for all AI, index, lint, and git work. The `2nb` it runs is the bundled `Contents/Resources/2nb`, preferred by `CLIPath.resolve()`, falling back to Homebrew/PATH for non-bundled dev builds.

An `FSEventsWatcher` on the vault keeps the index fresh: notes edited in Obsidian are incrementally re-indexed and re-embedded a moment after they settle (a debounced `2nb index --doc` via `scheduleExternalReindex`, skipping the app's own writes), and on bind a one-shot incremental `2nb index` (`syncOnBindIfStale`, gated on an on-disk-vs-indexed count delta) catches up notes added or removed while the app was closed, so embeddings stay current without a manual Sync.

## Vault Binding

On launch the app binds to the vault Obsidian currently has open, read from Obsidian's own registry `~/Library/Application Support/obsidian/obsidian.json` via `ObsidianRegistry` (`SecondBrainCore/Vault/ObsidianRegistry.swift`), so the dashboard and Obsidian stay on the same vault.

The Welcome screen offers "Open your Obsidian vault: \<name\>", and the `Vault > Open Vault…` panel (Cmd+Shift+O) validates that the chosen folder is a real Obsidian vault (has `.obsidian/`, via `VaultManager.isObsidianVault`) and warns when it isn't the one Obsidian has open. The window and sidebar title show the active vault name.

### CLI Active-Vault Resolution and Write Guards

The CLI reads the same Obsidian registry as its authoritative active-vault source (`vault.ObsidianOpenVault`, `cli/internal/vault/obsidian_registry.go`). The READ path `resolveVaultDir` resolves `--vault` → `2NB_VAULT` → the open Obsidian vault → cwd-vault (the Obsidian rung is gated off under `2NB_TEST`). There is no 2nb-managed active-vault pointer file: the GUI and CLI both follow Obsidian's registry, so a bare terminal `2nb ask` or `2nb search` already targets the same vault the dashboard shows, with nothing to drift.

Writes are firmer than reads (`openVaultAndSetActive` in `root.go`, also used by the MCP server): a write with no `--vault`/`2NB_VAULT` goes to the vault Obsidian has open (or, if Obsidian is closed, its most-recent), and the current directory is never an implicit write target. A cwd that resolves a vault only by walking up the tree (`FindVaultRoot`) to a parent is refused (`walkUpRefusedError`) before any open, so a write can never silently land in, or auto-mint a `.2ndbrain/` sidecar in, an unintended vault. The cwd is honored only when it IS the vault root. An explicit `--vault` pointing at a vault Obsidian doesn't know is refused unless `--unconfigured` (or `2NB_UNCONFIGURED=1` for the flagless MCP server) acknowledges that the note won't appear in Obsidian or the 2nb index. Only commands that actually WRITE take that path: `meta --get` and the bare `meta` view are reads, and `daily`, `daily path` and `daily read` escalate to the write opener only when today's note has to be created. This is the guard that prevents a mis-`cd`'d agent (for example Warp launched from a source repo) from splitting a vault. (`~/.2ndbrain-vaults` recents remains, but it is display-only for `vault list`, never a resolution source.)

## Window and Sidebar

The window is a `NavigationSplitView` whose sidebar leads with Home, the default screen (`DashboardTab` in `ContentView.swift`). The sidebar is seven entries: Home first, then Models, Notes, a Testing tab (before Health) consolidating everything measurable, the Health and Activity status groups, and a final Settings tab (see the dual-host rule under the Settings window). Configuration moved to the Settings window, so everything left in the status groups is status, and it groups by the question it answers rather than by the subsystem that produced it ("is my vault healthy?" used to span three tabs and "what has been happening?" two). `HealthView` and `ActivityView` (`DashboardGroupViews.swift`) host the EXISTING inline views behind a segmented picker rather than reimplementing them, so nothing was rewritten and nothing was dropped. The group is deliberately not labelled "Advanced": with the knobs gone, that label only discouraged people from opening tabs that answer real questions.

## Home Tab (HomeView.swift, default)

The consolidated common-case surface:

- **Vault card**: name and path, an Obsidian-match badge confirming this is the vault Obsidian has open, and an Obsidian-plugin row showing the installed plugin version with an Install/Update button that shells `2nb plugin install` (`ObsidianPlugin`/`HomePlugin`).
- **AI card** (provider-generic): the header names the ACTIVE provider via `ProviderDisplay`, the Generation/Embeddings rows show the raw active model IDs, and the ready dot and status line are provider-aware via `HomeAI.statusLine`. **Test** probes the ACTIVE models, branching on each probe's `ok`, with the shared `ColdStartHint` appearing after 15s of an in-flight probe ("Still working: a cold model can take a few minutes to first respond"; the patient-probe deadlines make a working cold model look hung without it). A **Reset to recommended defaults** button appears only when `HomeAI.differsFromDefaults` detects drift from the CLI defaults and confirms via NSAlert naming exactly what it writes; it replaced the old always-visible "Save as default" that silently reverted a user's chosen provider and models. When AI is NOT ready the card shows a `SettingsLink` straight to the AI tab (it previously reported missing credentials and offered nowhere to enter them, the onboarding dead end).
- **AI Clients summary**: configured count plus a `SettingsLink`. The per-client status rows, the Configure buttons, the setup snippet, the cross-dependency callout, and the `ClaudeCodeHealthView` Verify panel all live on the Settings window's Integrations tab, so only ONE surface writes those external client files.
- **Index card**: doc and embedding counts, a "N notes awaiting embedding" hint, and **Sync** / **Re-embed All** buttons. Sync runs the incremental, hash-gated `2nb index` that re-embeds only what changed and reconciles deletions via `purgeStale`.

An orange banner warns when the `2nb` the app resolves is older than the app (`CLIVersion`/`refreshCLIVersion`); since the app prefers its bundled, version-matched CLI this stays silent in a normal release (it only fires on dev builds that fall back to a stale Homebrew copy). When Homebrew is present (`BrewLocator`) the banner offers an Update CLI button that runs `brew upgrade apresai/tap/twonb` (`AppState.upgradeCLI`) to refresh the terminal/plugin's PATH `2nb`.

## Models Tab (SimpleModelsView.swift)

Bedrock-first flow: sticky vendor checkboxes (vocabulary from `models policy show` `known_vendors`) → a discovery-nudge banner → one **Validate** (`models cost-preview` + `models verify` on the same enabled/probeable IDs from `models list --discover`; the streamed progress line carries the shared `ColdStartHint` after 15s, since one cold probe can legitimately hold the run for minutes) → Answers/Search pickers of the CLI `working` set (price, last probe latency, and why; actives stay selectable even when not working).

The discovery-nudge banner ("N new models discovered") is **dual-source** (`DiscoveryNudge.nudgeSource`):

- Once the installed CLI proves it supports `2nb models discover`, the NEW set comes from that report's `new[]`, session-accumulated in `DiscoverDiffSession` because the CLI diff is one-shot: every run advances its machine-local baseline, so NEW reports once, and a reload between the run and the user's glance would otherwise eat the banner. That set is filtered by the same probeable-and-enabled predicate Validate uses for its candidate set (`DiscoveryNudge.probeableAndEnabled`: provider match, not vendor-policy-disabled, not rerank, not statically incompatible) AND by the legacy seen snapshot, so a model dismissed under the snapshot era is never re-announced after a CLI upgrade. The CLI's own first run seeds its baseline silently (`new` empty), so a freshly seeded pool never badges.
- The capability VERDICT settles on the first classified run and is trusted for the rest of the app session (the discover command itself still re-runs on reloads), decided BY PAYLOAD SHAPE, never exit status (`DiscoverCLIProbe`): a pre-discover CLI answers the verb as `models list`, exit 0 with a top-level array (cobra parents with a RunE swallow unknown subcommands; measured live on 0.19.1), so only the object envelope carrying `sources` proves support.
- On an older CLI the snapshot path remains the fallback: the banner appears when a reload finds probeable-and-enabled models absent from a machine-local `UserDefaults` "seen" snapshot (`AppState.discoverySeenModelKeys`, `"provider|modelID"` keys; app-side and deliberately not vault-side, since a synced vault sidecar would mis-badge models on a different machine).
- First activation of a provider (no key for it in the snapshot yet, checked via `DiscoveryNudge.shouldSuppressFirstRun`, scoped per provider rather than "does any snapshot exist", so a second provider's first appearance is never badged as N new models either) seeds silently instead of badging its full catalog as new. `updateDiscoveryNudge` requires `aiStatus` to already be populated so a snapshot is never seeded or read under a guessed provider, and only the Models-tab host runs the CLI discover probe (the Testing pane's Discover card owns its own runs; checkbox reloads never walk the vendor planes).
- **Validate new models** calls `runValidate(only:)`, the same cost-preview/confirm/verify-stream body as the main Validate button but pinned to exactly the banner's IDs (the pinned-ID path `models verify` already had); **Dismiss** marks the same IDs seen without probing. Either action, or a successful full Validate, merges the probed or dismissed IDs into the snapshot and clears the provider's session-NEW keys so the banner does not re-nudge about them.

The Thinking picker (Off/Low/Medium/High) writes `ai.reasoning_effort` only when `invoke_strategy == bedrock_mantle_responses` and only on user change. The old Hub (`AIHubView`) stays behind **Full catalog** (see the AI Hub section below).

## Notes Tab (LintResultsView.swift)

Shells out to `2nb lint --json` and renders findings; each finding is actionable:

- **Open in Obsidian** via an `obsidian://open` deep link built by `SecondBrainCore/Vault/ObsidianURL`, with a default-app fallback.
- For schema findings (missing required field or invalid enum, classified by `LintFinding`), **Set value…** opens a sheet that runs `2nb meta --set` and re-lints.

Broken-wikilink findings are class-aware and verdict-driven. Bulk classification probes each non-drift finding with `suggest-target --llm --verdict` concurrently (bounded 4-wide; the CLI no-ops the model when a high-confidence candidate exists, so drift-adjacent findings never spend a generation call) and classifies from the recommendation envelope via `FixAllPlanner`: drift → auto-repairable; relink at high confidence → one-click did-you-mean; relink at medium → top picks; unlink → `.removable` (nothing at medium or above matched, or the model explicitly declined, the measured-trustworthy removal signal). A probe that ERRORS renders a neutral "check failed" badge, never a removal recommendation.

The header counts "N one-click fixable, M need a decision, K removable". Removable rows carry a "remove?" badge and an inline **Unlink** button, and a **Remove dead links (K)** button gathers them into a pre-checked confirm sheet (`RemoveAllSheet`, mirroring `FixAllSheet`) that applies `unlink --write` per checked row: removal is never silent and never part of Fix all. **Fix all** gathers only high-confidence rewrites (drift repairs plus high-confidence relinks) into the pre-checked `FixAllSheet` applying `repair-links --write` / `relink --write`. A **Fix each (N)** button steps through everything non-one-click (decisions plus removables, the exact complement of the Fix-all set, deduped) via the per-finding sheet with an "M of N" progress indicator and Skip.

Individual findings open **Fix link…** → `LinkResolutionSheet`, which reuses the bulk verdict (no second generation spend on open; a fresh `--verdict` fetch only when no bulk result exists) and has no dead ends: **Repair** drift (one-click, with a diff), **Did you mean?** (top 2-3 with confidence and reason), **Create the note** (hidden for path-qualified targets), and **Unlink** (keep the text). The recommended action is visually preselected in BOTH queue and single-finding modes, driven by the verdict (relink → top pick, unlink → removal) rather than list emptiness. Each action is reversible with `polish --undo` (per-note latest snapshot).

CLI no-ops are classified (`LinkFixOutcome`): an actionable no-op (no matching note, or ambiguous) keeps the sheet open with guidance, while a stale no-op (the link is already gone) dismisses the sheet, shows an informational banner, and re-lints so the finding clears.

## Testing Tab (TestingView.swift)

Everything measurable in one destination. Sections are segmented, with state owned by ContentView so deep links can land on a pane (`TestingView.Section`). Menu entry: AI → "Testing & Benchmarks…" (Cmd+Shift+T) via `AppState.showTesting` + `DashboardRoute.Target.testing`, which names no pane and keeps the last-used one.

### Validate pane

Hosts the Models tab's vendor checkboxes plus the Validate flow via `SimpleModelsView(validateOnly: true)` (one implementation, two hosts), plus the per-account access summary (`AccessSummary`), a working-set summary (`WorkingModelPresentation.workingSetSummary`), and a **Discover card** (`DiscoverSectionView`, rendered inside the validateOnly branch so the pane keeps one scroller):

- Per-source discovery-cache ages mirroring the terminal ("classic us-east-1: 3h ago" / "stale (26h)", via `DiscoverPresentation`).
- A **Refresh** button running `models discover --refresh --json` (free, a control-plane re-walk with no model invokes, but it shows progress since the walk takes seconds).
- The NEW list with one-click **Add** (`models discover --add <id> --json`, free, persists the row with its routing) and **Add + Validate** (cost-preview via the `--discover`-carrying `models cost-preview`, then `PaidOperationConfirm`, then `--add <id> --validate --yes --cost-cap <2x estimate + 0.01>`, with `ColdStartHint` while the probe runs).
- The GONE list, rendered informationally.

The card renders from AppState's shared latest report plus `DiscoverDiffSession`, because the server-side diff is one-shot: a run from one surface must not eat it before the other renders. It degrades to an update-the-CLI note when `DiscoverCLIProbe` proves the installed CLI predates the verb.

### Benchmarks pane

Runs one model x probe, or the favorites full battery:

- The model picker is ordered by `models list --sort best` so measured evidence ranks the choices, and `BenchProbes.benchableModels` preserves that order within type. Probe options are type-gated via the shared `BenchProbes.options`, which also drives the catalog picker's probe menu, so both pickers carry the zero-API `search` probe; the zero-API `search`/`retrieval` probes skip the spend confirm.
- The favorites full battery streams `models bench --json` via `AppState.benchmarkFavorites`, estimated by grouping the paid probes per type in `BenchProbes.batteryPreviewGroups`.
- Favorites management: add the selected model, or remove per row (`models bench fav/unfav/favs`).
- A routes x probes compare matrix over `models bench compare --json` (`BenchCompareMatrix`: the CLI compare's probe order with retrieval appended, latency/quality cells, with the retrieval quality recovered from the recorded `mrr@K=` detail string). Rows are keyed on the route `(provider, model, plane, region)` and labelled `id@plane/region`, so the classic and mantle endpoints of a dual-plane model compare as two rows instead of collapsing into one. `BenchRunInfo` carries the plane and region every run now records (pre-0.21 rows are stamped from the catalog by `BackfillMissingRoutes`), and a row with neither falls back to the bare model id.
- The embed-throughput curve from `2nb ai embed-probe`: per-level texts/sec bars, errors, and the recommended `ai.embed_concurrency`, rendering the `EmbedProbeInfo.levels` the payload always carried.
- The bench.db history feed behind a model facet (`fetchBenchHistory`/`BenchRunInfo`). Tables only; charts are a follow-up.

### Performance pane (MetricsView.swift)

Moved in from Health. The vault performance observatory: reads `2nb metrics --json` (decoded into `VaultMetrics`/`MetricOperation`/`MetricGauges`/`MetricAggregate`) via `AppState.refreshMetrics`. Shows the last index build (duration, docs/sec, embeddings/sec, doc/chunk/link and embed counts, model), live vault gauges (doc/chunk/embedded counts, coverage %, index.db and WAL size, stale count, embedding model/dims), per-operation aggregates (count/avg/p50/avg-docs-per-sec), and a recent-operations list (per-op SF Symbol, latency, key metric, and a source chip for non-`cli` rows). Refreshes on appear plus a Refresh button, with no polling (metrics change only when an op runs). MCP-sourced rows carry the same token and count detail as CLI rows (via `recordMCPDetail`); the row detail still renders `result_count` only when present, since rows recorded by older servers lack it.

### Quality pane (TestingQualityView)

Runs the `2nb eval` family: the retrieval scorecard (Recall@10 / R@1 / MRR@10 with config and QA-cache context), the LLM-jury answer grades (`eval answers`, self-judged label included), and the tune sweep (`eval tune`) rendered suggest-only: the suggested `config set` commands are shown copyable and never applied, and a BM25-only win renders as a diagnosis.

Every eval run is gated by the CLI's own `eval --estimate` preview through `PaidOperationConfirm` with a derived `--cost-cap` of 2x estimate + $0.01 (`EvalFlow`, mirroring `VerifyFlow.costCap`). A failed estimate degrades to the numberless confirm and the CLI default cap for the bare scorecard only (whose sole cost that default was sized for); tune shares that profile (QA acquisition plus embeds, one upfront gate) and degrades too; only answers REFUSES a nil estimate (`EvalFlow.mayRunWithoutEstimate`), since its jury generation bills above the default and a too-low explicit cap aborts mid-run after partial spend. A vault with no cached QA set sees the one-time generation cost spelled out before any spend.

### Shared run models

Heavy paid runs (bench batteries, eval runs, the embed probe) share the one `BenchRunModel` single-flight claim. All three verify surfaces (AI Hub, Models tab, the Validate pane) share `VerifyRunModel` (@Observable; the one `applyVerifyEvent` fold, pinned by tests against recorded `--events` sequences) with `VerifyProgress` in its own file.

## Health Group

### Vault (VaultStatusView.swift)

Unified health: vault info, index coverage, portability, AI reachability, stale docs, and one `SettingsLink` to the AI tab. It reports state and no longer runs operations: Sync and Re-embed All duplicated Home's buttons, and five entry points once wrapped those two operations, so a destructive full re-embed was reachable from wherever you happened to be standing. Home owns them now (two entry points: Home, and the catalog picker's Set Active + Re-embed). The tab used to carry "Test Connection…" and "AI Setup…", which were the same no-op: both set computed aliases of `showAIHub`, so both merely selected a tab; nothing tested a connection and no setup wizard existed. Those aliases (`showAITest`, `showAISetupWizard`, `showModelWizard`) are deleted, and both actions now exist for real on the Settings AI tab.

### Updates (UpdatesView.swift)

Shows the app, CLI, and Obsidian-plugin versions against the latest published release (via `2nb update --json`, decoded into `UpdateInfo`). CLI and plugin freshness comes from the CLI's own parity computation in the payload (`update_available` on the `current` field, and the `plugin` `ProductState`), the same source as `2nb doctor`, so the dashboard can't disagree with the terminal. The app row stays authoritative from the running bundle (`appVersion` via `CLIVersion.isOlder`, not the payload's `app` field, which reflects `/Applications`), and a row shows the "→ latest" only when it's genuinely behind, so a current-or-ahead app never reads as outdated. One-click **Update CLI** (`AppState.upgradeCLI` + `BrewLocator`) and **Update plugin** (`installObsidianPlugin`); the app row shows a copyable `brew upgrade --cask`, since a running app can't replace its own bundle. "Check now" re-runs the check.

## Activity Group

### Git (GitActivityView.swift)

Recent commits (1/3/7/30-day window); clicking a row opens `CommitDetailView`, a split pane with the file list and per-file diff.

### MCP Server (MCPStatusView.swift)

A durable "Configured in ~/.claude.json" banner (from `2nb mcp configured --json`, via `HomeMCPConfigured`) above live MCP server processes and recent tool invocations; polls `2nb mcp status --json` every 5s. The banner answers "is it set up?" even when no server is running (the client launches it on demand), and the empty state distinguishes configured-but-idle from not-configured.

## Sidebar Settings Tab

The same four-tab Settings content as Cmd+,, hosted inline as the sidebar's LAST entry (`SettingsView(isInline: true)`), so configuration is reachable without the hidden shortcut. `OpenSettingsTabButton` (all 8 open-settings call sites) lands here when a vault is bound (sets `settingsTab` + `showSettingsPane`, routed via `DashboardRoute.Target.settings`); with no vault it opens the Settings window as before, since WelcomeView has no sidebar.

## Settings Window (Cmd+,)

`SettingsView.swift` is the app's configuration surface, and the only one that works with no vault bound (the first-run state where a user's credentials are wrong). It is a `Settings` scene wrapping a four-tab `TabView`, per the macOS pattern; tabs use `.tabItem` rather than `Tab(_:systemImage:)`, which is macOS 15+ while this app deploys to 14.

**Dual-host rule:** the same `SettingsView` also renders inline as the sidebar's Settings tab (`SettingsView(isInline: true)`), one implementation with one shared `AppState.settingsTab` selection, so the two hosts can never show different tabs. `isInline` gates only host-specific behavior: the fixed 660x700 frame, and (via the `settingsHostIsInline` environment key) the AI page's Return-key `.defaultAction` shortcut, which registers window-global and therefore stays Settings-window-only.

**Single-flight reload rule:** each settings tab view's `reload()` is single-flight PER INSTANCE with pending-rerun coalescing: a reload requested while one is in flight re-runs once it finishes, so a post-write refresh racing the `.task` load is never dropped. Each host loads its own view state, and concurrent read-only status reloads across the two hosts are harmless by design (an AppState-scoped cross-host lock was tried and starved whichever host lost the race). The Settings scene remains the no-vault host.

| Tab | View | Contents |
|---|---|---|
| **General** | `SettingsGeneralView` | active vault (read-only: it follows Obsidian), Obsidian plugin install/update |
| **AI** | `SettingsAIView` | connection, region, credentials, the two active models, **Test everything** |
| **Advanced** | `SettingsAdvancedView` | wraps the existing `AIAdvancedSettingsView` unchanged, so every knob still writes through `2nb config set` and the CLI stays the single validator |
| **Integrations** | `SettingsIntegrationsView` | per-client skill / MCP / global-instructions status + Configure, plus Claude Code's setup snippet, cross-dependency callout, and the `ClaudeCodeHealthView` Verify panel |

The AI page carries seven controls against the AI Hub's ~67, and nothing was removed to get there: the tuning knobs moved into Advanced, and the model catalog stayed in the main window (a 980×700 browser does not fit a settings sheet). Three details are load-bearing:

- **The key renders masked** (`••••••••dkE9`) from `config bedrock --json`'s `token_suffix`, and entering one verifies it before accepting it: the most-cited BYOK failure is a key that is expired, mistyped, or for the wrong provider, none of which surfaces until first use.
- **Test everything** runs `2nb doctor --json` through `AppState.runDoctorSelfTest` and renders the CLI's credential verdict, so "your key was rejected" and "your key works but this account is not entitled to that model" stay different sentences with different fixes. It reads stdout via `runCLIGlobalRaw` regardless of exit status, because doctor exits non-zero exactly when it has a verdict worth showing, and passes `expectNonZeroExit: true` so an expected failure is not logged as a CLI fault. While a run is in flight the shared `ColdStartHint` appears after 15s (doctor probes both active models under the patient probe deadlines, so a cold model's first response can take minutes).
- **`BedrockRegions.risk`** gates the region picker and returns three answers, not two: `safe`, `breaks`, and `unverifiable`. The third exists because `ai status` needs a vault, so with none bound the active embedding model is unknown; an earlier `String?` version treated that as "no constraint" and saved a breaking region silently, failing OPEN in the exact first-run case it was written for. Nova-2 embeddings answer only in `us-east-1`; mantle models (OpenAI, xAI) ignore the setting entirely, and the page says so.

`HomeView`'s AI Clients card is a read-only summary (configured count plus a `SettingsLink`); the configuration itself lives on the Integrations tab, so only one surface writes those external client files.

## Supporting Views

`MCPSetupView` (MCP config snippets for AI tools), `ModelCatalogPickerView` (per-model detail / test / benchmark, opened from the AI Hub), `IndexProgressView` (rebuild confirmation → progress → stats), `MergeConflictView` / `DiffView` (reusable Myers LCS unified diff). `AppDelegate.swift` renames the default File menu to "Notes".

## Menus and Shortcuts

- **Vault** menu: New Vault, Open Vault (Cmd+Shift+O), Reveal Vault in Finder, Vault Status, Sync Index, Validate Vault, Import Obsidian Vault, Export to Obsidian.
- **View**: Recent Activity (Cmd+Shift+G).
- **AI** menu: Models… (Cmd+Shift+, → Models tab), Testing & Benchmarks… (Cmd+Shift+T → Testing tab), MCP Server Configuration, MCP Server Status (Cmd+Shift+M).

## AI Hub (AIHubView.swift)

The power-user catalog behind the Models tab's **Full catalog** disclosure (not the default path). Default setup is `SimpleModelsView`: vendors → Validate → working-set pickers. Hub sections:

### Providers

Bedrock / OpenRouter / Ollama cards with live status and a **Show models / Hide models** button (the llama-local card is hidden behind `localEngineFeatureEnabled`, see Local models below). The button writes vault config `ai.<provider>.disabled`, and its label says what that key actually does: it hides the provider's models from the selection lists and does not stop an explicitly-chosen active provider from running (`ai/config.go`). It was labelled Enable/Disable, which promised a kill switch the key does not implement: a user who "disabled" Bedrock and watched it keep answering had every reason to think the button was broken.

### Local models

Behind `localEngineFeatureEnabled`, currently OFF (the engine binary isn't provisioned, so this section is not rendered). **Download** (NSAlert confirm → `2nb ai engine pull --json` streamed via a `Process` `readabilityHandler` into an observable `LocalModelDownload` progress bar; `PullEventStream` line-buffers the JSONL) fetches the ~4 GB bundled stack. **Use these models** (`AppState.activateLocalStack`) atomically sets `ai.provider=llama-local` plus both local models plus `ai.dimensions=768`, then force-re-embeds (all-or-nothing plus a re-embed; the confirm warns it is lower quality than Nova). **Delete local models** (`ai engine rm`) frees the cache. The plumbing is retained so flipping the flag re-enables it wholesale.

### Active

Current embedding, generation, and rerank slots, each with `Change`; the rerank slot adds an on/off toggle (`ai.rerank.enabled`, disabled while the write is in flight) and ships OFF (measured to not help at this scale, so the copy never implies it improves results).

### Catalog

Curated by default (`ModelCuration`, unit-tested): opens on the trustworthy short list (recommended / shipped-verified / user-tested models of the active provider, active models always included; degrades to verified+tested on a pre-curation CLI) with the long tail behind "Show all models (N more)", where untested unverified discoveries render demoted in a collapsed per-type group and "Discover more" lives (fresh unvalidated listings can't flood the curated view).

Grouped by vendor within type (Embedding, Generation, then Reranking); the catalog is summary-first (`CatalogSummary`): each vendor group defaults to a COLLAPSED one-line summary row (vendor name, model count, validated/no-access badges, enabled state, a policy-member shield), and the whole header is the drill-in target that expands the per-model rows on demand, so the default view is compact and everything is one click away. A concise policy chip near the top ("Vendors: Anthropic, DeepSeek (14 models, 9 validated)") surfaces the active enable-only policy at a glance and links to the Manage-vendors sheet. Each collapsed group's header still carries the count, latest-first rows on expand, and a single ellipsis Menu (`Enable all` / `Disable all` / `Verify vendor…`; the enable/disable items shell `models enable|disable --vendor`, writing per-model overrides that beat the vendor policy, and note "(overrides vendor policy)" when one exists). Row badges and meta lines use the classified access-state labels (`no access`, `throttled`, `bad credentials` via `ModelAccessPresentation`). Disabled models are hidden from the curated view by default with a **Show disabled (N)** reveal (visible dimmed in show-all mode), and a fully-disabled vendor group auto-collapses with a dimmed "disabled" header, so a vendor-policy disable visibly shrinks the list.

Paid Test/Benchmark actions are gated by a cost-estimate confirm (`PaidOperationConfirm` wrapping `models cost-preview`; the zero-API retrieval bench probe skips it). A Catalog-header access summary ("N verified, M no access, ..., checked Xd ago" from `ai status --json` `model_access`) sits beside a **Validate models** button: it cost-previews, confirms via `PaidOperationConfirm`, then streams `models verify --provider <p> --scope vault --yes --events --cost-cap <2×estimate+0.01>` with the same explicit IDs as the cost-preview confirm (NDJSON `start`/`result`/`done`) into inline progress, and reloads so the per-row access badges populate; each vendor group also carries a **Verify vendor** menu action scoped to that group's IDs. The cost gate always runs before any spend (`VerifyFlow.costCap`/`classify`, unit-tested); a cost-cap-exceeded run exits non-zero with an empty stream and surfaces the stderr as a failure.

A **Manage vendors…** button opens `VendorPolicyView`, a per-provider sheet with a checkbox per vendor (grouped from the loaded catalog, pre-populated from `models policy show`): Apply shells `models policy set --provider <p> --enable-only <csv> --scope global`, then `models policy clear --scope vault` so a leftover vault policy cannot shadow (each provider section owns its Apply/Clear and effect line; a policy vendor with no catalog model yet is preserved, so a future DeepSeek discovery arrives enabled), then offers "Validate the enabled models now?" wiring to the Validate flow (never auto-spends).

Tuning knobs that used to sit in a Hub **Advanced settings** disclosure now live on **Settings → Advanced** (`AIAdvancedSettingsView`): every write still goes through `2nb config set`.

### ModelCatalogPickerView

Per-row action `Details` opens `ModelCatalogPickerView` (sidebar plus detail; filters: type/provider/tier/enabled/tested/compatible; sort: Best/Cheapest/Fastest/Newest/Name; actions: Test, Set Active (labelled **Set Active + Turn On** for a rerank model, because picking one also writes `ai.rerank.enabled=true`: a reasonable default, but the old label gave no hint it flipped a second setting), Set Active + Re-embed, Enable State tri-state, a per-model "Your similarity threshold override" control, which starts EMPTY (it used to prefill the catalog's recommendation, so Save with no edit wrote a calibration the user never chose) with the effective-threshold caption underneath, Cost Preview per probe kind, and Benchmark with streaming events plus a lazily-loaded per-model History list from `models bench history --json`; a failed test renders the full guidance callout, including an Open-AWS-console link for bedrock access denials).

The Hub replaces the AI Setup Wizard, Test AI Connection, and Model Wizard. It observes `modelsCatalogVersion` so external CLI edits refresh live. Vendor identity (`vendor / vendor_display / family / version_sort_key`) and the `compatible` flag are computed by the Go CLI in `applyCatalogUIFields` and sent over JSON: Swift no longer mirrors that logic. `Set Active` is gated on `appState.isIndexing` and refused at the AppState layer, to prevent mixed-model embeddings during a rebuild.

## GUI Test Automation

GUI tests use AppleScript for app interaction and `screencapture` for verification. Run `make install` first (the app lands in `~/Applications`); the suite runs via `make test-gui`.

Test scripts live in `tests/`: `gui-helpers.sh` (shared), `gui-test-crud.sh`, `gui-test-navigation.sh`, `gui-test-editor.sh`, `gui-test-ui.sh`, `gui-test-vault.sh`, `gui-test-vault-switch.sh`, `gui-test-ai.sh`, `gui-test-polish.sh` (credential-gated).

Key patterns:

- **NSAlert dialogs** (New Note): type in the text field, navigate the popup via accessibility, press Return.
- **SwiftUI overlays** (Quick Open, Search, Command Palette): rely on menu shortcuts (not `.onKeyPress`) since NSTextView steals focus. `makeFirstResponder(nil)` plus `@FocusState` ensures overlay TextFields get focus.
- **Sidebar clicks**: AppleScript `click at {x, y}` coordinates.
- **Screenshots**: `/tmp/sb-gui-tests/` for debugging.
