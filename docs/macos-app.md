# macOS App (SecondBrain) Reference

The reference for the Swift macOS app in `app/`: the SecondBrain companion dashboard, persistent menu bar agent, and global Quick Ask & Search HUD. It covers the app's architecture, vault binding (including the CLI's active-vault resolution and write guards, which the app and CLI share), the 4-tab workspace, the Settings window, two-stage hybrid search, and GUI test automation. The macOS SwiftUI platform gotchas and Context7 library IDs stay in the repo root `CLAUDE.md`.

**Framework:** SwiftUI + AppKit + Quartz (`QLPreviewPanel`), Carbon hotkeys, Swift 6.0, macOS 14+
**Dependencies:** GRDB.swift (SQLite), Yams (YAML), swift-markdown
**Architecture:** MVVM with `@Observable`

## Role: Companion Workspace & Quick HUD, Not an Editor

The macOS app is a companion dashboard, quick search HUD, and configuration surface, not an editor: Obsidian stays the editor. It reads the same `.2ndbrain/index.db` the CLI writes (WAL mode) and shells out to `2nb` for all AI, index, lint, and git work. The `2nb` it runs is the bundled `Contents/Resources/2nb`, preferred by `CLIPath.resolve()`, falling back to Homebrew/PATH for non-bundled dev builds.

An `FSEventsWatcher` on the vault keeps the index fresh (unless paused via the menu bar item or Settings > General): notes edited in Obsidian are incrementally re-indexed and re-embedded a moment after they settle (a debounced `2nb index --doc` via `scheduleExternalReindex`, skipping the app's own writes; a note that fails to index is reported as `AppState.lastIndexingProblem` and shown beside the Index state, where it used to be swallowed into a debug log, and the batch drain catches per path so one broken note no longer aborts the rest of a sync). A full index sets it too, and that is why `startIndex` runs `2nb index --json` (never `--porcelain`, which would silence the stderr progress lines the progress UI is built on) and decides from `AppState.FullIndexSummary` rather than from a regex over the old prose summary: exit 0 stopped meaning "nothing was wrong" once an unparseable note became non-fatal and had its row dropped, so a clean rebuild can hide a note that just vanished from search. `applyFullIndexOutcome` reports the first entry of `unparseable`/`unreadable` with a count of the rest, clears only when both lists are empty, and on an envelope that does not decode keeps whatever was already reported rather than dismissing it. The report is otherwise cleared when that note indexes cleanly (`clearIndexingProblem(for:)`, the single-note watcher's own path-scoped clear) and on `openVault(at:)`, since the path belongs to the vault being left behind. `openVault(at:)` also cancels the external-reindex debounce and drops `pendingExternalPaths` (and the per-path `reindexTasks`), beside the `fileWatcher?.stop()` that queued them: that queue is AppState-wide rather than vault-scoped, so a change queued against the old vault would otherwise drain 1.5s later against the new one, where `relativePath(for:)` falls back to `lastPathComponent` and could reindex a same-named note in the wrong vault. A call already executing stays pinned to the vault it started with and finishes there; cancellation is not cooperative inside `runCLI` (only read-only search opts in, via `cancellable: true`, which terminates the superseded child), so the guarantee covers what is queued, and on bind a one-shot incremental `2nb index` (`syncOnBindIfStale`, gated on an on-disk-vs-indexed count delta and `pauseAutoSync`) catches up notes added or removed while the app was closed, so embeddings stay current without a manual Sync.

## Vault Binding

On launch (skipped under `2NB_TEST=1`), the app binds to the vault Obsidian currently has open, read from Obsidian's own registry `~/Library/Application Support/obsidian/obsidian.json` via `ObsidianRegistry` (`SecondBrainCore/Vault/ObsidianRegistry.swift`), so the dashboard and Obsidian stay on the same vault.

The Welcome screen (`WelcomeView`) and first-run guide (`OnboardingView`) offer auto-detected Obsidian vaults, and the `Vault > Open Vault…` panel (`Cmd+Shift+O`) validates that the chosen folder is a real Obsidian vault (has `.obsidian/`, via `VaultManager.isObsidianVault`) and warns when it isn't the one Obsidian has open. The window and menu bar summary show the active vault name.

### CLI Active-Vault Resolution and Write Guards

The CLI reads the same Obsidian registry as its authoritative active-vault source (`vault.ObsidianOpenVault`, `cli/internal/vault/obsidian_registry.go`). The READ path `resolveVaultDir` resolves `--vault` → `2NB_VAULT` → the open Obsidian vault → cwd-vault (the Obsidian rung is gated off under `2NB_TEST`). There is no 2nb-managed active-vault pointer file: the GUI and CLI both follow Obsidian's registry, so a bare terminal `2nb ask` or `2nb search` already targets the same vault the dashboard shows, with nothing to drift.

Writes are firmer than reads (`openVaultAndSetActive` in `root.go`, also used by the MCP server): a write with no `--vault`/`2NB_VAULT` goes to the vault Obsidian has open (or, if Obsidian is closed, its most-recent), and the current directory is never an implicit write target. A cwd that resolves a vault only by walking up the tree (`FindVaultRoot`) to a parent is refused (`walkUpRefusedError`) before any open, so a write can never silently land in, or auto-mint a `.2ndbrain/` sidecar in, an unintended vault. The cwd is honored only when it IS the vault root. An explicit `--vault` pointing at a vault Obsidian doesn't know is refused unless `--unconfigured` (or `2NB_UNCONFIGURED=1` for the flagless MCP server) acknowledges that the note won't appear in Obsidian or the 2nb index. Only commands that actually WRITE take that path: `meta --get` and the bare `meta` view are reads, and `daily`, `daily path` and `daily read` escalate to the write opener only when today's note has to be created. This is the guard that prevents a mis-`cd`'d agent (for example Warp launched from a source repo) from splitting a vault. (`~/.2ndbrain-vaults` recents remains, but it is display-only for `vault list`, never a resolution source.)

## Workspace Architecture

The app uses a regular Dock workspace (`WindowGroup` + `Cmd+,` Settings) paired with a persistent menu bar icon and global floating HUD (`AppDelegate.configure(appState:)`):

- **Design Tokens (`AppTheme.swift`)**: `ThemedCard` (`.standard`, `.prominent`, `.subtle` soft material cards), `Keycaps` shortcut pills, `NoteCitation` (wiki-link formatting and frontmatter stripping via fence scanning rather than `Yams.load`), and shared `NotePreviewPane`.
- **Window lifecycle (`AppDelegate.swift`, `MainWindow` in `SecondBrainApp.swift`)**: the app outlives its dashboard window (`applicationShouldTerminateAfterLastWindowClosed` returns false), so every route back to the window must be able to CREATE one, not only front one. The `WindowGroup` carries the id `MainWindow.sceneID`; the first `ContentView` stores SwiftUI's `openWindow` on `AppState.openMainWindowAction`; and `AppDelegate.showMainWindow()` fronts the existing dashboard or calls that action. It is reached from `applicationShouldHandleReopen` (Dock click, `open` on the running app: SwiftUI's implicit reopen goes through the New Window command this app removes, so without the hook nothing happened), from the menu bar item, and from `watchPendingRoutes`, which reopens the window when a route request (`AppState.hasPendingDashboardRoute`) is set with no dashboard to consume it. `ContentView.consumePendingRoutes()` drains those requests on appear as well as on change, since `onChange` never fires for a value set before the view existed. The vault bind in `SecondBrainApp` runs once per process, not per window. `window.close` and `window.reopen` are recorded in `gui-actions.jsonl`. `MainWindow.isDashboard` identifies the window by its SwiftUI identifier prefix, which is what tells it apart from the Settings window (also main-capable).
- **Menu Bar Agent (`StatusMenuController.swift`)**: `NSStatusItem` displaying a dynamic `18×18` icon (`StatusIcon`) with clockwise indexing sweep progress (`AppState.indexFraction`), pause bars (`AppState.pauseAutoSync`), and an orange setup dot when Gemini credentials are not ready. Its menu embeds a compact `MenuStatusView` header plus actions for Quick Ask & Search, Open 2ndbrain, Open Vault in Obsidian, Sync Index Now, Pause/Resume Auto-Sync, Gemini Settings (`requestOpenSettings(tab: .ai)`), and Quit. Skipped when `2NB_TEST=1`.
- **Global Hotkey (`Hotkeys.swift`)**: Carbon `RegisterEventHotKey` (`GlobalHotkey` / `HotkeyManager.shared`), requiring no Accessibility permissions, defaulting to `⌃⇧Space` (`KeyCombo.defaultSpec = "ctrl+shift+space"`). Includes `ShortcutRecorder` for click-to-record customization and conflict detection against macOS symbolic hotkeys.
- **Floating HUD (`QuickSearchPanel.swift`)**: Non-activating borderless `NSPanel` (`QuickSearchPanelWindow`) configured with `[.canJoinAllSpaces, .fullScreenAuxiliary, .transient]` so it floats above fullscreen apps and Spaces. Collapses to `780×62` when empty and expands top-anchored to `780×480` on input. Switches between Search and Ask Gemini via `Tab` or a leading `"? "` prefix, runs two-stage search + `NotePreviewPane`, and bridges `QLPreviewPanel` for Quick Look (`⌘Y` / `Space`).

## First-Run Onboarding (`OnboardingView.swift`)

Presented as a sheet on first launch when no vault is bound (skipped when `2NB_TEST=1`), and re-openable via **Vault > Run Setup Guide…** or **Settings > General**. Backed by `OnboardingState`:

1. **Obsidian Vault**: Detects open/known Obsidian vaults via `ObsidianRegistry` or picks a folder manually. Advancing starts background indexing (`appState.startIndex()`) immediately so embeddings populate while the user finishes setup.
2. **Google Gemini (BYOK / BYOP)**: Guides users through bringing their own `GEMINI_API_KEY` (macOS Keychain; never `GOOGLE_API_KEY`) or configuring their own GCP project ID (`your-gcp-project-id`) + region (`GoogleRegions.common`) for Vertex AI with ADC, probing credentials before saving defaults (`gemini-4-hydrogen` + `gemini-embedding-2` 768d).
3. **Shortcut & AI Clients**: Tests the global Quick Search shortcut live inside the wizard (`HotkeyManager.onboardingTestHook`), shows the `Keycaps` legend, and offers one-click MCP + skill wiring (`ClientDescriptor.all` via `appState.setupClient`).

## Window and Sidebar (`ContentView.swift`)

The main window is a `NavigationSplitView` streamlined to **four capability-oriented sidebar entries** (`DashboardTab` + `BetaBadge`):

- **Home** (default)
- **Ask & Search**
- **Notes** (Links & Schema + AI Polish)
- **Activity** (Actions & Status + Git + MCP Server + AI Usage & Cost)

All configuration lives in the separate macOS `Settings` window (`Cmd+,`) rather than a duplicate sidebar tab or raw model catalog.

## Home Tab (`HomeView.swift`)

- **Disappearing Finish Setup checklist (`SetupChecklist.missingItems`)**: Shows only incomplete setup items (`vault`, `gemini`, `index`, `clients`) and disappears completely once the vault is bound, Gemini is ready, notes are embedded, and at least one AI client is connected (or the optional AI Clients item is dismissed).
- **Quick Ask & Search hero card**: Direct launch buttons for **Ask Gemini**, **Quick Search HUD** (with live `Keycaps` shortcut badge), and **Polish Note**.
- **Workspace status cards**: Responsive 2-column grid displaying **Vault & Obsidian** (bound name/path + Obsidian plugin version/install), **Gemini AI** (active provider via `ProviderDisplay`, raw Gemini 4 generation & embedding model IDs, readiness dot via `HomeAI.statusLine`, 30-day cloud cost summary + **Usage & Cost…** shortcut (`home.openAIUsageButton`), **Test** probe with `ColdStartHint`, conditional **Reset to Recommended Defaults** when drifted, and deep-link button to `Settings > Gemini`), **Search Index** (doc/embed counts, live progress + `~ETA left` when indexing, `lastIndexingProblem` banner if a note failed to parse/read, **Sync** / **Re-embed All**), and **AI Clients** (read-only configured count linking to `Settings > Integrations`).
- **CLI version banner**: Warns when the resolved `2nb` binary is older than the app and offers one-click `brew upgrade apresai/tap/twonb` via `BrewLocator` / `AppState.upgradeCLI`.

## Ask & Search Tab (`AskSearchView.swift`)

Unified destination combining conversational RAG and live two-stage hybrid search:

- **Ask Gemini**: Multi-turn conversational Q&A (`ConversationExchange`, `AskSearchPresentation.historyTurns`) rendered inside `ThemedCard` blocks with rewritten-query indicators and clickable `obsidian://open` source chips.
- **Two-Stage Hybrid Search**:
  1. Stage 1 runs immediately as you type using `appState.searchVault(query:limit:bm25Only: true)` (`2nb search --bm25-only --json --porcelain`).
  2. After a `150ms` typing debounce (with `searchGeneration` cancellation so stale responses never overwrite newer queries), Stage 2 runs `appState.searchVault(query:limit:bm25Only: false)` for fused BM25 + Gemini vector results (`SearchPhase`: `.empty`, `.words`, `.meaning`, `.wordsOnly`).
- **Weaker Match Folding & Preview**: `PartitionedSearchResults` displays high-confidence hits (`score >= 0.5 * topScore`, keeping at least 1 hit when non-empty) and folds lower-scoring hits under an expandable `"N weaker match(es)"` disclosure. Selecting any hit updates `NotePreviewPane`, with keyboard navigation (`↑`/`↓`), Quick Look (`Space` / `⌘Y` via `QLPreviewPanel`), **Open in Obsidian** (`↩`), **Reveal in Finder** (`⌘↩`), and **Copy `[[Wikilink]]`** (`⌘C`), summarized in a bottom `Keycaps` footer.

## Notes Group (`DashboardGroupViews.swift`, `LintResultsView.swift`, `NotePolishView.swift`)

- **Links & Schema (`LintResultsView.swift`)**: Auto-scans on open by running `2nb lint --json` and `2nb fix --json --porcelain` concurrently. Displays a single-button **Fix Vault** action (`notes.fixVaultButton` -> `2nb fix --write`) whenever deterministic or AI-fixable issues (`aiFixableCount > 0`) exist, surfacing *Planned AI Repairs* (`parse_error`, `schema_enum`, `missing_required`, `broken_link`, `formatting`), `aiSkippedReason` when AI is unavailable, and any CLI errors directly (no silent failures). Paired with **Undo Last Fix** (`notes.undoFixButton` -> `2nb fix --undo`) and **Rescan** (`notes.rescanButton`). Multi-candidate ambiguous links or custom manual issues remain under *Needs Your Choice* (`LinkResolutionSheet` / `SetValueSheet`). Tracked under `lint.run`, `lint.fix_vault`, and `lint.undo_fix_vault`.
- **AI Polish (`NotePolishView.swift`)**: Search-first note picker (`polish.searchField` / `polish.searchResultsList`) querying SQLite (`DatabaseManager.searchNotesForPicker` via FTS5 + `LIKE` across title, path, and content, with unindexed disk fallback excluding templates), optional goal/lens input (`polish.goalField`), schema conformance + outgoing link options, explicit incoming backlinks diff preview (`polish.backlinksPreview`) with opt-in **Apply incoming backlinks** toggle (`polish.applyBacklinksToggle`), **Preview Polish** (`polish.previewButton`), **Apply to Note** (`polish.applyButton`), and persistent batch **Undo Last Polish** (`polish.undoButton`). App-initiated writes coordinate with `FSEventsWatcher` via `suppressExternalReindex`.

## Activity Group (`DashboardGroupViews.swift`, `GUIActionsView.swift`, `GUIActionTracker.swift`, `GitActivityView.swift`, `MCPStatusView.swift`, `AIUsageView.swift`)

- **Actions & Status (`GUIActionsView.swift`, `GUIActionTracker.swift`, `Cmd+Shift+J`)**: Live telemetry observer recording all GUI actions and application status (`GUIStatusSnapshot`, including `indexEstimatedRemainingSeconds`, `indexDocsPerSec`, `aiUsage30dCostUSD`, `aiUsage30dCloudCalls`, `aiUsage30dLocalCalls`). Maintains a 300-event ring buffer with category (`App`, `Nav`, `Vault`, `Index`, `Search`, `Ask`, `Settings`, `Lint`, `Polish`, `MCP`, `Git`, `CLI`) and text filters, expandable event detail/metadata rows, **Copy Status JSON**, **Reveal Log in Finder**, and **Clear Session** controls. Streams public structured logs to `OSLog` (`dev.apresai.2ndbrain` / `gui-action`) and writes `gui-actions.jsonl` (rotated at 5 MB to `gui-actions.prev.jsonl`) + atomic `gui-state.json` to `--gui-log-dir` (default `~/Library/Logs/SecondBrain`), mirroring to `<vault>/.2ndbrain/logs/` **only** when `<vault>/.2ndbrain` already exists on disk (honoring the write-surface guard). All messages and metadata pass through `GUIActionTracker.redact(_:)` before storage/logging so API tokens are never persisted.
- **Git (`GitActivityView.swift`)**: Recent vault commits (1/3/7/30-day window); clicking a row opens `CommitDetailView`, a split pane with changed files and unified diffs (`DiffView`).
- **MCP Server (`MCPStatusView.swift`)**: Durable configuration banner (`2nb mcp configured --json` via `HomeMCPConfigured`) above live MCP server processes and recent tool invocations (`2nb mcp status --json`).
- **AI Usage & Cost (`AIUsageView.swift`, `Cmd+Shift+U`)**: Displays Today / 7d / 30d / All-Time cloud USD spend and cloud vs `$0.00` on-device call & token counts (`2nb metrics --json`), full vault **Re-embed Estimate** card (`chunks`, `~tokens`, `docs/s` history/baseline ETA, and cost), per-model & slot breakdown (`byModel`), recent operations list, and **Reset History…** (`2nb metrics clear`).

## Launch Options (`LaunchOptions.swift`)

CLI flags and `2NB_GUI_*` environment overrides support deterministic launching and isolated GUI verification without mutating `UserDefaults`:

- `--vault <path>` (`2NB_GUI_VAULT`): Bind directly to `<path>` with `persistLastVault: false`.
- `--skip-onboarding` (`2NB_GUI_SKIP_ONBOARDING=1`): Suppress the first-run setup sheet.
- `--no-startup-sync` (`2NB_GUI_NO_STARTUP_SYNC=1`): Skip `syncOnBindIfStale` on vault open.
- `--gui-log-dir <dir>` (`2NB_GUI_LOG_DIR`): Override primary directory for `gui-actions.jsonl` and `gui-state.json`.
- `--gui-control-dir <dir>` (`2NB_GUI_CONTROL_DIR`): Enable the opt-in first-party `GUIAutomationController` file-IPC loop for driving menus, shortcuts, buttons, SwiftUI fields, and window screenshots in isolation.

## Settings Window (`Cmd+,`, `SettingsView.swift`)

`SettingsView` hosts three auto-sizing tabs inside macOS `Settings { ... }` with `.windowResizability(.contentSize)` and `SettingsPane` (`GeometryReader` + `PreferenceKey` height measurement capped to screen height; the sole host for configuration and functional without a bound vault):

| Tab | View | Contents |
|---|---|---|
| **AI & Models** | `SettingsAIView` | Independent **Generation** and **Embeddings** slot switchers (`Google Gemini (Cloud)` / `Local Gemma (On-Device)`) allowing pure cloud, pure offline, or hybrid combos (e.g., on-device `embeddinggemma-2` embeddings + cloud `gemini-4-hydrogen` generation): **Gemini** configures **BYOK** (`GEMINI_API_KEY` in Keychain; never `GOOGLE_API_KEY`) or **BYOP Vertex AI** (`your-gcp-project-id` + `GoogleRegions` + ADC), curated Gemini 4 generation models (`gemini-4-hydrogen`, `gemini-4-argon`, `gemini-4-carbon`), thinking depth (`none`/`low`/`medium`/`high` + `xhigh` gated to `gemini-4-hydrogen`), and `gemini-embedding-2` / `gemini-embedding-001`; **Local Gemma (On-Device)** downloads SHA256-pinned GGUFs (`embeddinggemma-2` `310 MB` Apache-2.0, `gemma4-e2b`, `bge-reranker-v2-m3`; offers a one-click `Switch to EmbeddingGemma 2` button when a vault uses legacy `embeddinggemma-300m`), tunes Matryoshka dimensions (`256d`/`512d`/`768d`) + Task Prefix Presets (`search`/`code`/`qa`/`none`), shows pre-run re-embed ETA/cost estimates under `Re-embed Vault Now` and live `chunks`/`docs/s`/`~ETA left`, and runs the bundled `Contents/Resources/llama/` Metal engine invisibly on demand (`10m` idle shutdown); per-slot **Test Generation Model** (`settings.testGeneration`) and **Test Embedding Model** (`settings.testEmbedding`) probe individual slots via `2nb models test` with response/dimension proof, while **Test AI Connection** (`settings.testConnection`) runs vault-pinned `2nb doctor --json` across active slots; **AI Usage & Estimated Cloud Cost** section summarizes 30-day spend/calls/tokens with **View Full Usage & Cost…** (`settings.openAIUsage`) and **Reset Usage History…** (`settings.resetAIUsage`) |
| **Integrations** | `SettingsIntegrationsView` | Sole UI writer for per-client MCP + skill + global-instructions configs (`ClientDescriptor.all`: Claude Code, Warp, Claude Desktop, Codex), manual CLI snippets (`MCPSetupView`), and `ClaudeCodeHealthView` verification |
| **General** | `SettingsGeneralView` | Active vault summary, Obsidian plugin install/update, **Quick Search & Menu Bar** (`ShortcutRecorder`, Reset to `⌃⇧Space`, `Toggle("Pause automatic background indexing")`, **Run First-Time Setup Guide…**), and embedded `UpdatesView` (`2nb update --json` + Homebrew CLI upgrade) |

- **Key probe & masking (`SettingsAIView.swift`)**: Keys verify before persistence (`KeyPersistence.shouldPersistProbedKey`) and render masked via `2nb doctor --json` / Keychain lookup. `doctor --json` reads stdout regardless of non-zero exit status (`runCLIGlobalRaw(..., expectNonZeroExit: true)`) and surfaces `ColdStartHint` after 15s while probing active models.
- **Vertex AI guidance (`ModelAccessPresentation.swift`)**: Links permission/access errors (`access_denied` / `permission_denied`) to `https://console.cloud.google.com/vertex-ai/` with `your-gcp-project-id` remediation instructions.

## Supporting Views

`MCPSetupView` (MCP config snippets for AI tools), `IndexProgressView` (rebuild confirmation with chunk/token/ETA & cost estimate → chunk-weighted progress & live `docs/s` + `~ETA left` parsed via `IndexETAEstimator` → stats), `MenuStatusView` (menu bar live ETA line while indexing), `AIUsageView` (Activity > AI Usage & Cost dashboard), and `DiffView` (reusable Myers LCS unified diff). `AppDelegate.swift` renames any system `File` menu to `"Notes"` when present.

## Menus and Shortcuts

- **Vault**: New Vault…, Open Vault… (`Cmd+Shift+O`), Open in Obsidian, Reveal Vault in Finder, Sync Index, Re-embed All…, Validate Links & Schema (`Cmd+Shift+L`), Run Setup Guide….
- **View**: Home (`Cmd+1`), Ask & Search (`Cmd+2`), Notes (`Cmd+3`), Activity (`Cmd+4`), GUI Actions & Status (`Cmd+Shift+J`), Recent Git Activity (`Cmd+Shift+G`).
- **AI**: Ask & Search Vault… (`Cmd+Shift+K`), Search Vault… (`Cmd+Shift+F`), Quick Ask & Search Panel, Polish Note with Gemini… (`Cmd+Shift+P`), Gemini Settings… (`Cmd+Shift+Comma`), AI Usage & Cost… (`Cmd+Shift+U`), AI Client Integrations…, MCP Server Configuration…, MCP Server Status… (`Cmd+Shift+M`).
- **Global HUD**: `⌃⇧Space` (user-customizable in `Settings > General` or Onboarding Step 3).

## GUI Test Automation

GUI tests drive the installed `~/Applications/SecondBrain.app` (via `make install-app` / `make test-gui`) against isolated temporary vaults using `--vault`, `--skip-onboarding`, `--no-startup-sync`, `--gui-log-dir`, and `--gui-control-dir` (`GUIAutomationController`). Because the app drives its own AppKit menus, shortcuts, accessibility buttons, mounted SwiftUI inputs, and window PNG rendering on `@MainActor` over file-IPC (`req-*.json` / `resp-*.json`) while assertions read structured `gui-actions.jsonl` + `gui-state.json`, the suite runs hermetically without mutating `UserDefaults` (`lastVaultPath`) or requiring cross-process `System Events` Accessibility TCC permissions.

Test scripts live in `tests/`:

- `gui-helpers.sh`: Shared temp-vault setup, app launch, `gui_ctl` IPC helpers, `wait_gui_state` / `assert_gui_action`, Gemini capability probing, and secret-leak assertions.
- `gui-test-window-reopen.sh`: closes the dashboard (`close_main_window` op) and asserts via `window_state` that a reopen event, the menu bar action (`status_open_main_window`) and `Cmd+3` each recreate exactly one window without re-binding the vault.
- `gui-test-ui.sh`: `Cmd+1..4` 4-tab navigation, `Cmd+Shift+J` Actions & Status pane, `AI > Gemini Settings…` telemetry, and `Vault`/`View`/`AI` menu bar checks.
- `gui-test-index.sh`: `Vault > Sync Index` cancel + run, SQLite `documents` + `gemini-embedding-2` verification, `Vault > Re-embed All…`, `AI > Search Vault…` (`Cmd+Shift+F` keyword + zero-lexical-overlap Gemini hybrid search), and floating Quick Search HUD (`hud_set_text` + `Esc`).
- `gui-test-ai.sh`: `AI > Ask & Search Vault…` (`Cmd+Shift+K`) live Gemini RAG query and source citations.
- `gui-test-vault-switch.sh`: Isolated vault switching (Vault A → Vault B → Vault A) verifying `UserDefaults` `lastVaultPath` remains untouched.
- `gui-test-polish.sh`: `AI > Polish Note with Gemini…` (`Cmd+Shift+P`) preview diff, apply write, and backup-snapshot undo verification.
- `gui-test-fix-vault.sh`: `Cmd+3` (`Notes > Links & Schema`) auto-scan, single-button `Fix Vault` (`notes.fixVaultButton`) disk/lint/schema verification, and `Undo Last Fix` (`notes.undoFixButton`) byte-for-byte restore verification.
- `gui-test-local-gemma.sh`: Local Gemma provider configuration, Settings UI inspection, per-slot embedding/hybrid generation probes, Matryoshka dimension tuning, and GUI telemetry verification.
- `gui-test-model-invocation.sh`: Zero-Homebrew live model invocation suite exercising Local Gemma (`embeddinggemma-2` 256d + `gemma4-e2b`), the legacy `embeddinggemma-300m` → `embeddinggemma-2` upgrade button (`settings.upgradeEmbeddingGemma2`), Cloud Google Gemini (`gemini-embedding-2` 768d + `gemini-4-hydrogen`), Hybrid Combo (`embeddinggemma-2` 512d + `gemini-4-hydrogen` + Home `Test Connection`), Model Test under `dimension_break`/`model_mismatch`, and honest dead-endpoint failure detection.
- `gui-test-eta-and-usage.sh`: Zero-Homebrew isolated vault test verifying pre-run re-embed ETA estimation in the `Re-embed All…` confirmation sheet (`indexEstimatedRemainingSeconds`), live/completed re-embed metrics persistence in `metrics.db`, `Activity > AI Usage & Cost` navigation + $0.00 local tracking (`aiUsage30dLocalCalls`), `Settings > AI & Models` usage display, and `settings.resetAIUsage` history clearing.

Key patterns:

- **HUD & two-stage search**: `QuickSearchPanel` uses a local `NSEvent` key monitor (`Esc`, `Tab`, `↑`/`↓`, `⌘Y`, `⌘↩`, `⌘C`), while `AskSearchView` uses `@FocusState` + `.onKeyPress` so typing in `NSTextField` never collides with list shortcuts. Under `--gui-control-dir`, `QuickSearchPanelController` skips auto-hide on `didResignKeyNotification` and `GUIInputBridge` acknowledges SwiftUI text commands only when the target mode/view is mounted.
- **Screenshots**: Rendered from the frontmost HUD/sheet/window into `/tmp/sb-gui-tests/`.
