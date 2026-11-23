import SwiftUI
import SecondBrainCore
import os

private let log = Logger(subsystem: "dev.apresai.2ndbrain", category: "app")

/// Gating logic for the startup catch-up index.
enum StartupSync {
    /// Whether to run a catch-up `2nb index` when a vault binds: only when no
    /// index is already running AND the vault has on-disk changes the index
    /// hasn't seen.
    static func shouldSync(vaultChanged: Bool, isIndexing: Bool) -> Bool {
        !isIndexing && vaultChanged
    }

    /// True if any indexable file (.md/.canvas/.base) under `vaultRoot` is newer
    /// than the index db, or the db doesn't exist yet (never indexed). Stat-only
    /// (no content read), so it stays cheap even for a large vault. Catches notes
    /// added OR modified while the app was closed. Unlike a doc-count compare it
    /// doesn't drift when the index also counts .canvas/.base or skips ignored
    /// files. A pure delete with no other change isn't caught here (the file is
    /// gone, so there's no newer mtime); that reconciles on the next manual Sync.
    static func vaultChangedSinceIndex(vaultRoot: URL) -> Bool {
        let fm = FileManager.default
        let dbPath = vaultRoot.appendingPathComponent(".2ndbrain/index.db").path
        guard let dbMod = (try? fm.attributesOfItem(atPath: dbPath))?[.modificationDate] as? Date else {
            return true // never indexed
        }
        // .skipsHiddenFiles excludes .2ndbrain (the db itself) and .obsidian.
        guard let enumerator = fm.enumerator(
            at: vaultRoot,
            includingPropertiesForKeys: [.contentModificationDateKey],
            options: [.skipsHiddenFiles]
        ) else {
            return true
        }
        for case let url as URL in enumerator {
            switch url.pathExtension.lowercased() {
            case "md", "canvas", "base":
                if let mod = try? url.resourceValues(forKeys: [.contentModificationDateKey]).contentModificationDate,
                   mod > dbMod {
                    return true
                }
            default:
                continue
            }
        }
        return false
    }
}

@Observable @MainActor
final class AppState {
    var vault: VaultManager?
    var database: DatabaseManager?
    var fileWatcher: FSEventsWatcher?

    /// Structured GUI action and status telemetry (in-memory feed + OSLog +
    /// JSONL/JSON files under ~/Library/Logs/SecondBrain or `--gui-log-dir`).
    let actionTracker: GUIActionTracker

    /// Mounted-view input bridge used when `--gui-control-dir` is enabled.
    let guiInputBridge = GUIInputBridge()

    /// Optional per-instance CLI executable override for isolated tests;
    /// when nil, falls back to `CLIPath.resolve()`.
    @ObservationIgnored var cliExecutablePathOverride: String?

    private func resolvedCLIPath() -> String {
        cliExecutablePathOverride ?? CLIPath.resolve()
    }

    /// Which Settings tab the window shows. Selection-bound so a link
    /// anywhere in the app can land the user on the exact Settings tab.
    var settingsTab: SettingsTab = .ai

    /// Deep-link flags for main Beta navigation tabs, observed by ContentView.
    var requestedTab: DashboardTab?
    var showAskSearch = false
    var requestedAskSearchMode: AskSearchMode?
    var showNotePolish = false
    var showGUIActions = false
    var showSettingsRequest = false
    var showOnboarding = false
    var showQuickSearch = false

    /// True while a route request is waiting for a dashboard window to
    /// consume it. ContentView drains these the moment they are set, so this
    /// stays true only when no dashboard window exists; AppDelegate watches
    /// it and reopens the window so the command is not silently dropped.
    var hasPendingDashboardRoute: Bool {
        requestedTab != nil || showAskSearch || showLintResults || showNotePolish
            || showGUIActions || showGitActivity || showMCPStatus || showAIUsage
            || showSettingsRequest || showMCPSetup || showOnboarding
    }

    /// Creates the dashboard window. Captured from SwiftUI's `openWindow`
    /// by the first ContentView, because AppKit alone cannot instantiate a
    /// SwiftUI scene once its window has been closed and destroyed.
    @ObservationIgnored var openMainWindowAction: (() -> Void)?

    init() {
        let tracker = GUIActionTracker(overrideLogDir: LaunchOptions.current.guiLogDir)
        self.actionTracker = tracker
        tracker.record(
            category: .app,
            action: "app.launch",
            phase: .succeeded,
            summary: "SecondBrain launched (v\(appVersion))",
            metadata: [
                "version": appVersion,
                "vault_override": LaunchOptions.current.vaultPath != nil ? "true" : "false",
                "skip_onboarding": LaunchOptions.current.skipOnboarding ? "true" : "false",
                "no_startup_sync": LaunchOptions.current.noStartupSync ? "true" : "false",
            ]
        )
    }

    func openAskGemini() {
        requestedAskSearchMode = .ask
        showAskSearch = true
    }

    func openHybridSearch() {
        requestedAskSearchMode = .search
        showAskSearch = true
    }

    /// User toggle to pause background incremental indexing from the menu bar
    /// or Settings → General. Queued paths stay collected and flush on resume.
    var pauseAutoSync: Bool = ProcessInfo.processInfo.environment["2NB_TEST"] == "1"
        ? false
        : UserDefaults.standard.bool(forKey: "pauseAutoSync")

    func noteSettingsOpened(tab: SettingsTab) {
        settingsTab = tab
        actionTracker.record(
            category: .navigation,
            action: "settings.open",
            phase: .succeeded,
            summary: "Opened Settings (\(tab.rawValue))",
            metadata: ["tab": tab.rawValue]
        )
    }

    func requestOpenSettings(tab: SettingsTab = .ai) {
        noteSettingsOpened(tab: tab)
        showSettingsRequest = true
    }

    func setAutoSyncPaused(_ paused: Bool) {
        guard pauseAutoSync != paused else { return }
        pauseAutoSync = paused
        if ProcessInfo.processInfo.environment["2NB_TEST"] != "1" {
            UserDefaults.standard.set(paused, forKey: "pauseAutoSync")
        }
        actionTracker.record(
            category: .settings,
            action: "settings.auto_sync_toggle",
            phase: .succeeded,
            summary: paused ? "Paused background auto-sync" : "Resumed background auto-sync",
            metadata: ["paused": String(paused)]
        )
        if paused {
            externalReindexTask?.cancel()
            externalReindexTask = nil
        } else if !pendingExternalPaths.isEmpty {
            externalReindexTask = Task { @MainActor [weak self] in
                await self?.drainExternalReindex()
            }
        }
    }

    var indexFraction: Double? {
        guard isIndexing else { return nil }
        if let ep = embeddingProgress {
            if let ct = ep.chunksTotal, ct > 0, let cd = ep.chunksDone {
                return max(0.04, min(0.98, Double(cd) / Double(ct)))
            }
            if ep.total > 0 {
                return max(0.04, min(0.98, Double(ep.current) / Double(ep.total)))
            }
        }
        return 0.06
    }

    // Coalesced incremental index for notes changed OUTSIDE the app (edited in
    // Obsidian, synced, git-pulled). Every path that changes within a debounce
    // window is collected here and drained through ONE serial pass, so we never
    // run two `2nb index --doc` at once (they would contend on the single
    // SQLite writer and spawn a parallel embed storm). A bulk change collapses
    // to a single full `2nb index`.
    // internal, not private, so IndexingProblemTests can drive the real
    // schedule-then-switch path instead of waiting out the 1.5s debounce.
    // @testable import keeps them inside the module regardless.
    var pendingExternalPaths: Set<String> = []
    var suppressedExternalPaths: [String: Date] = [:]
    var externalReindexTask: Task<Void, Never>?
    private var externalReindexInFlight = false
    private let externalReindexDebounce: Duration = .seconds(1.5)
    private let bulkExternalReindexThreshold = 8

    // Git state
    var vaultIsGitRepo: Bool = false
    var gitFileStatus: [String: String] = [:]
    var gitActivity: [GitChangeInfo] = []
    var gitActivityLoading: Bool = false
    var gitActivityDays: Int = UserDefaults.standard.object(forKey: "gitActivityDays") as? Int ?? 7
    var showGitActivity: Bool = false

    // AI state
    var aiStatus: AIStatusInfo?
    var metricsReport: MetricsReport?
    var metricsLoading = false
    var showAIUsage = false
    var localModelDownload: LocalModelDownload?
    var localEngineStatus: EngineStatus?
    // Installed `2nb` CLI version ("X.Y.Z"), or nil if it couldn't be read.
    // Home compares this against `appVersion` to warn on a stale CLI — see
    // CLIVersion and refreshCLIVersion().
    var cliVersion: String?
    // Portability warnings from the most recent CLI search/ask. When
    // non-empty, the vault is in a degraded state (dimension mismatch,
    // provider unavailable, model mismatch, etc.) and views should show
    // a banner explaining why. Cleared on the next successful run.
    var lastSemanticWarnings: [String] = []
    var isIndexing = false
    var indexError: String?
    /// The most recent per-note reindex failure, from the watcher's
    /// `index --doc`. Distinct from `indexError`, which belongs to a full index
    /// run, so a broken note cannot erase the reason a whole rebuild failed (or
    /// the other way round). Both call sites used to log.debug this and show
    /// nothing, so a note whose frontmatter broke on save silently vanished from
    /// search until the next full index reported it.
    var lastIndexingProblem: IndexingProblem?
    var embeddingProgress: EmbeddingProgress?
    var indexProgress: IndexProgress?
    var showIndexProgress = false

    // Tools panels
    var showLintResults = false
    var isLinting = false
    var isFixingVault = false
    var lintReport: LintReport?
    var fixReport: FixVaultReport?
    var lintError: String?
    var fixPreviewError: String?
    private var lastFixWrittenPaths: [String] = []
    var canUndoPolishPath: String?
    var canUndoPolishBacklinkPaths: [String] = []
    var showMCPSetup = false
    var mcpSetupText: String?
    var showMCPStatus = false
    var mcpStatuses: [MCPServerStatusInfo] = []
    private var mcpStatusTimer: Timer?
    /// In-flight guard for the 5s MCP-status poll: a tick that runs slow (a
    /// wedged CLI riding out the CLIWatchdog bound) must not stack a second
    /// `mcp status` process on top of the first, tick after tick. Overlapping
    /// ticks are skipped instead; the next idle tick refreshes normally.
    /// Main-actor confined (AppState is @MainActor), and the guard-and-set
    /// happens synchronously before the first await, so it cannot race.
    private var mcpStatusRefreshInFlight = false
    // AI-client integration status for the Home "AI Clients" card: which 2nb
    // skills are installed (skillStatuses, across all client slugs) and which
    // clients have the MCP server configured (mcpConfiguredAll, one entry per
    // client). Refreshed on vault switch; empty when the CLI is unreachable or
    // too old.
    var skillStatuses: [SkillStatusInfo] = []
    var mcpConfiguredAll: [MCPConfiguredInfo] = []
    var globalInstructionsAll: [GlobalInstructionsInfo] = []

    /// MCP-configured status for a specific client (claude-code, warp,
    /// claude-desktop, codex), or nil if that client isn't in the last refresh.
    func mcpConfigured(forClient key: String) -> MCPConfiguredInfo? {
        mcpConfiguredAll.first { $0.client == key }
    }

    /// Global-instructions status for a specific client, or nil if that client
    /// has no known memory file (warp/codex) or isn't in the last refresh.
    func globalInstructions(forClient key: String) -> GlobalInstructionsInfo? {
        globalInstructionsAll.first { $0.client == key }
    }

    /// Back-compat accessor: the Claude Code entry, the historical meaning of
    /// `mcpConfigured`. Computed from `mcpConfiguredAll`, so callers and the
    /// cross-dependency callout keep working unchanged.
    var mcpConfigured: MCPConfiguredInfo? {
        mcpConfigured(forClient: "claude-code")
    }

    /// Skill install status for a specific slug (claude-code, warp, codex, …),
    /// or nil if that slug isn't in the last refresh.
    func skillStatus(forSlug slug: String) -> SkillStatusInfo? {
        skillStatuses.first { $0.slug == slug }
    }

    // Claude Code "Verify" health panel state.
    var verifying = false
    var healthChecks: [HealthCheck] = []
    var errorLogger: ErrorLogger?
    var files: [FileItem] = []

    // Commit detail modal — loaded lazily via `2nb git show --json <hash>`
    // when the user clicks a commit in GitActivityView. The modal presents
    // on showCommitDetail and displays commitDetail (or commitDetailError
    // if the CLI returned non-zero).
    var showCommitDetail: Bool = false
    var commitDetail: CommitDetail?
    var commitDetailError: String?
    // Guards openCommitDetail against last-write-wins races. The running
    // task is cancelled when another openCommitDetail arrives or when
    // closeCommitDetail runs; its continuation checks Task.isCancelled
    // after each await and drops the stale result instead of publishing.
    // Cancellation prevents stale writes, not the subprocess itself.
    private var commitDetailTask: Task<Void, Never>?

    /// Opens a user-picked folder as a vault after validating it is a real
    /// Obsidian vault and warning when it isn't the vault Obsidian currently has
    /// open. This is the single entry point for the "Open Vault" panels, so the
    /// dashboard stays joined to the correct Obsidian vault rather than silently
    /// operating on an arbitrary folder. Returns whether the vault was opened.
    @discardableResult
    func openPickedVault(at url: URL) -> Bool {
        let vm = VaultManager(rootURL: url)
        if !vm.isObsidianVault {
            let alert = NSAlert()
            alert.messageText = "Not an Obsidian vault"
            alert.informativeText = "“\(url.lastPathComponent)” has no .obsidian folder, so Obsidian doesn't manage it as a vault. Open it anyway?"
            alert.addButton(withTitle: "Open Anyway")
            alert.addButton(withTitle: "Cancel")
            guard alert.runModal() == .alertFirstButtonReturn else { return false }
        } else if let open = ObsidianRegistry.load()?.openVault,
                  ObsidianRegistry.normalizedPath(open.url) != ObsidianRegistry.normalizedPath(url) {
            let alert = NSAlert()
            alert.messageText = "Different vault than Obsidian has open"
            alert.informativeText = "Obsidian currently has “\(open.name)” open, but you're opening “\(url.lastPathComponent)”. 2ndbrain works best on the vault you're editing in Obsidian. Open this one anyway?"
            alert.addButton(withTitle: "Open This Vault")
            alert.addButton(withTitle: "Cancel")
            guard alert.runModal() == .alertFirstButtonReturn else { return false }
        }
        openVault(at: url, persistLastVault: true)
        return true
    }

    func openVault(at url: URL, persistLastVault: Bool = true) {
        log.info("Opening vault at \(url.path)")
        // A recorded problem names a path inside the vault being left behind, so
        // carrying it across a switch shows a banner about a note the new vault
        // does not even have.
        lastIndexingProblem = nil
        lintReport = nil
        fixReport = nil
        lintError = nil
        fixPreviewError = nil
        lastFixWrittenPaths = []
        canUndoPolishPath = nil
        canUndoPolishBacklinkPaths = []
        metricsReport = nil
        let vm = VaultManager(rootURL: url)
        self.vault = vm
        actionTracker.setVaultDotDir(vm.dotDirURL)

        if persistLastVault,
           ProcessInfo.processInfo.environment["2NB_TEST"] != "1",
           LaunchOptions.current.vaultPath == nil {
            UserDefaults.standard.set(url.path, forKey: "lastVaultPath")
        }

        // Open the shared SQLite index (same DB the Go CLI writes to)
        if vm.isInitialized {
            do {
                self.database = try DatabaseManager(path: vm.indexDBPath)
                log.info("Database opened: \(vm.indexDBPath)")
            } catch {
                let safeErr = Self.boundedRedacted(error.localizedDescription)
                log.error("Failed to open index: \(safeErr)")
                errorLogger?.log("Failed to open index: \(safeErr)")
                actionTracker.record(
                    category: .vault,
                    action: "vault.open_db",
                    phase: .failed,
                    summary: "Failed to open SQLite index for \(vm.rootURL.lastPathComponent)",
                    detail: error.localizedDescription
                )
            }
            // No CLI sync needed: 2nb reads Obsidian's own registry to resolve
            // the active vault, the same source this app binds to, so a bare
            // terminal `2nb` already follows the open Obsidian vault.
        } else {
            self.database = nil
            log.notice("Vault not initialized (no .2ndbrain dir): \(url.path)")
        }

        self.errorLogger = ErrorLogger(vaultDotDir: vm.dotDirURL)

        // Refresh file list
        refreshFiles()
        log.info("Vault loaded: \(self.files.count) files")

        actionTracker.updateStatus { status in
            status.vaultPath = vm.rootURL.path
            status.vaultName = vm.rootURL.lastPathComponent
            status.fileCount = self.files.count
            status.lastError = nil
            status.lastIndexError = nil
            status.lastIndexingProblemPath = nil
            status.lastIndexingProblemMessage = nil
            status.indexEstimatedRemainingSeconds = nil
            status.indexDocsPerSec = nil
            status.aiUsage30dCostUSD = nil
            status.aiUsage30dCloudCalls = nil
            status.aiUsage30dLocalCalls = nil
        }
        actionTracker.record(
            category: .vault,
            action: "vault.open",
            phase: .succeeded,
            summary: "Opened vault \(vm.rootURL.lastPathComponent) (\(self.files.count) notes)",
            metadata: [
                "vault": vm.rootURL.lastPathComponent,
                "path": vm.rootURL.path,
                "initialized": String(vm.isInitialized),
                "files": "\(self.files.count)",
            ]
        )

        // Refresh AI status, then catch up the index if notes were added or
        // removed in Obsidian while the app was closed (the file watcher only
        // sees changes from now on).
        Task {
            await refreshAIStatus()
            await refreshMetrics()
            if !LaunchOptions.current.noStartupSync {
                syncOnBindIfStale()
            }
        }

        // Start polling MCP server status (used by status bar indicator)
        startMCPStatusTimer()

        // Refresh git state (non-blocking)
        Task { await refreshGitStatus() }

        // Start watching for external changes (.md notes and config.yaml).
        fileWatcher?.stop()
        // The debounce queue is AppState-wide, not vault-scoped, so a change
        // queued against the vault being closed would drain 1.5s later against
        // the one just opened.
        externalReindexTask?.cancel()
        externalReindexTask = nil
        pendingExternalPaths.removeAll()
        suppressedExternalPaths.removeAll()
        let watcher = FSEventsWatcher(
            path: url.path,
            filter: {
                $0.hasSuffix(".md") || $0.hasSuffix("/config.yaml")
            }
        ) { @Sendable [weak self] paths in
            Task { @MainActor in
                let mdPaths = paths.filter { path in
                    guard path.hasSuffix(".md"), let vault = self?.vault else { return false }
                    return !Self.shouldExcludeExternalIndexPath(path, vault: vault)
                }
                let configChanged = paths.contains { $0.hasSuffix("/config.yaml") }
                if !mdPaths.isEmpty {
                    self?.refreshFiles()
                    for path in mdPaths {
                        self?.scheduleExternalReindex(path: path)
                    }
                }
                if configChanged {
                    await self?.refreshAIStatus()
                    await self?.refreshMetrics()
                }
            }
        }
        watcher.start()
        self.fileWatcher = watcher
    }

    func refreshFiles() {
        guard let vault else { return }
        let mdFiles = vault.listMarkdownFiles()
        let items = mdFiles.map { url in
            FileItem(
                url: url,
                name: url.deletingPathExtension().lastPathComponent,
                relativePath: vault.relativePath(for: url)
            )
        }

        self.files = items
    }

    func createVault(at url: URL) {
        do {
            try VaultManager.initializeVault(at: url)
            openVault(at: url, persistLastVault: true)
            actionTracker.record(
                category: .vault,
                action: "vault.create",
                phase: .succeeded,
                summary: "Initialized vault \(url.lastPathComponent)",
                metadata: ["path": url.path]
            )
            log.info("Created new vault at \(url.path)")
        } catch {
            let safeErr = Self.boundedRedacted(error.localizedDescription)
            log.error("Failed to create vault at \(url.path): \(safeErr)")
            errorLogger?.log("Failed to create vault: \(safeErr)")
            actionTracker.record(
                category: .vault,
                action: "vault.create",
                phase: .failed,
                summary: "Failed to initialize vault \(url.lastPathComponent)",
                detail: error.localizedDescription,
                metadata: ["path": url.path]
            )
        }
    }

    // When true, the next startIndex() pass runs `2nb index --force-reembed`
    // to invalidate all stored embeddings. Set by Home's Re-embed All button
    // and by the catalog picker's Set Active + Re-embed — the two remaining
    // entry points — and cleared once the run begins.
    var pendingForceReembed: Bool = false

    private func makeSeededIndexProgress(phase: IndexPhase, forceReembed: Bool) -> IndexProgress {
        var progress = IndexProgress(phase: phase, forceReembed: forceReembed)
        let unindexedVault = (aiStatus?.embeddingCount == 0) || (aiStatus?.portabilityStatus == "unindexed")
        if let est = metricsReport?.reembedEstimate, forceReembed || unindexedVault {
            progress.embeddingTotal = est.embeddableDocs
            progress.chunksTotal = est.totalChunks
            if est.estimatedSeconds > 0 {
                progress.initialEstimatedSeconds = est.estimatedSeconds
                progress.estimatedRemainingSeconds = est.estimatedSeconds
            }
            progress.estimatedCostUSD = est.estimatedCostUSD
            progress.estimateIsCloud = est.isCloud
            progress.estimateSpeedSource = est.speedSource
        }
        return progress
    }

    func rebuildIndex(forceReembed: Bool = false) {
        guard vault != nil else { return }
        pendingForceReembed = forceReembed
        let initialProgress = makeSeededIndexProgress(phase: .ready, forceReembed: forceReembed)
        indexProgress = initialProgress
        showIndexProgress = true
        actionTracker.updateStatus { status in
            status.indexSheetVisible = true
            status.indexPhase = IndexPhase.ready.rawValue
            status.indexForceReembed = forceReembed
            status.indexEstimatedRemainingSeconds = initialProgress.estimatedRemainingSeconds
        }
        if metricsReport == nil {
            Task { @MainActor [weak self] in
                await self?.refreshMetrics()
                guard let self, let cur = self.indexProgress, cur.phase == .ready else { return }
                let updated = self.makeSeededIndexProgress(phase: .ready, forceReembed: cur.forceReembed)
                self.indexProgress = updated
                self.actionTracker.updateStatus { status in
                    status.indexEstimatedRemainingSeconds = updated.estimatedRemainingSeconds
                }
            }
        }
        actionTracker.record(
            category: .index,
            action: "index.open_sheet",
            phase: .succeeded,
            summary: forceReembed ? "Opened Re-embed All confirmation" : "Opened Sync Index confirmation",
            metadata: ["force_reembed": String(forceReembed)]
        )
    }

    /// Starts `2nb index --force-reembed` directly in the background without opening
    /// the main-window modal sheet (used by the Settings window and Menu Bar).
    func startForceReembed() {
        guard vault != nil, !isIndexing else { return }
        pendingForceReembed = true
        startIndex()
    }

    /// On vault bind, run ONE quiet incremental index if any note changed on
    /// disk since the last index, i.e. notes were added or edited in Obsidian
    /// while the app was closed (the file watcher only sees changes from now
    /// on). Uses `startIndex()` directly (no progress sheet), which is hash-gated
    /// so it re-embeds only what changed and reconciles deletions via purgeStale,
    /// and serializes with manual rebuilds via the `isIndexing` guard. The
    /// mtime scan runs off the main actor and is skipped entirely when the vault
    /// is unchanged, so a stable vault pays nothing on launch.
    func syncOnBindIfStale() {
        guard let vault, !isIndexing else { return }
        let root = vault.rootURL
        Task { @MainActor [weak self] in
            let changed = await Task.detached { StartupSync.vaultChangedSinceIndex(vaultRoot: root) }.value
            guard let self, StartupSync.shouldSync(vaultChanged: changed, isIndexing: self.isIndexing) else { return }
            log.info("Startup sync: vault changed since last index; running incremental index")
            self.startIndex()
        }
    }

    /// True once the current index run has reached a terminal phase. Late
    /// stderr-progress updates check this so they can't flip a finished run back
    /// to `.embedding` and re-wedge the progress sheet on "Running…".
    private var indexReachedTerminalPhase: Bool {
        indexProgress?.phase == .complete || indexProgress?.phase == .failed
    }

    /// Streaming runner: deliberately NOT under the CLIWatchdog absolute
    /// bound. A full index/re-embed legitimately runs for many minutes on a
    /// large vault, and an app-side kill would leave the index partially
    /// embedded (the same reason the Obsidian plugin gives `index` no
    /// timeout). Progress streams on stderr, so a live run is visibly alive.
    func startIndex() {
        guard let vault else { return }
        // One index at a time. A second Rebuild fired while one is still
        // running (e.g. a stray double-click) would spawn an overlapping
        // `2nb index` whose late progress and terminal-phase updates race the
        // first run's — the exact cause of the progress sheet getting stuck on
        // "Running…" and never reaching .complete.
        guard !isIndexing else { return }
        isIndexing = true
        indexError = nil
        embeddingProgress = nil
        let forceReembed = pendingForceReembed
        pendingForceReembed = false
        let seededProgress = makeSeededIndexProgress(phase: .indexingFiles, forceReembed: forceReembed)
        indexProgress = seededProgress
        log.info("Index rebuild started for vault: \(vault.rootURL.lastPathComponent) forceReembed=\(forceReembed)")
        let startTime = CFAbsoluteTimeGetCurrent()

        actionTracker.updateStatus { status in
            status.isIndexing = true
            status.indexPhase = IndexPhase.indexingFiles.rawValue
            status.indexForceReembed = forceReembed
            status.indexFraction = 0.06
            status.filesIndexed = 0
            status.docsIndexed = 0
            status.chunksCreated = 0
            status.embeddingCurrent = 0
            status.embeddingTotal = seededProgress.embeddingTotal
            status.indexEstimatedRemainingSeconds = seededProgress.estimatedRemainingSeconds
            status.indexDocsPerSec = nil
            status.lastIndexError = nil
        }
        actionTracker.record(
            category: .index,
            action: "index.start",
            phase: .started,
            summary: forceReembed ? "Started full index & re-embed" : "Started index sync",
            metadata: [
                "vault": vault.rootURL.lastPathComponent,
                "force_reembed": String(forceReembed),
            ]
        )

        Task {
            // Run the CLI without blocking the main actor. `AppState` is
            // @MainActor, so the old `process.waitUntilExit()` froze the UI for
            // the whole index and starved the @MainActor progress updates. This
            // mirrors `runCLI`: stream stderr for progress, resume on the
            // process's terminationHandler, and always land on a terminal phase
            // keyed off the *exit code* (never off progress reaching total —
            // after the CLI skips empty notes the embed legitimately ends below
            // total with no final "embedded N/N" line).
            do {
                let result = try await withCheckedThrowingContinuation {
                    (continuation: CheckedContinuation<(exitCode: Int32, stdout: String, stderr: String), Error>) in
                    let process = Process()
                    process.executableURL = URL(fileURLWithPath: self.resolvedCLIPath())
                    // --json, so the run's outcome is a parsed contract rather
                    // than a scrape of prose. NOT --porcelain, which the
                    // single-doc path does pass: --porcelain silences the
                    // per-file and per-embedding stderr lines the progress sheet
                    // below is built on, and a full index is the one call that
                    // has a progress sheet. Verified against the built CLI:
                    // --json leaves every stderr progress line in place.
                    var subArgs = forceReembed ? ["index", "--force-reembed"] : ["index"]
                    subArgs.append("--json")
                    process.arguments = CLIPath.args(subArgs, vault: vault.rootURL)
                    process.currentDirectoryURL = vault.rootURL
                    let stderrPipe = Pipe()
                    let stdoutPipe = Pipe()
                    process.standardError = stderrPipe
                    process.standardOutput = stdoutPipe
                    let drain = PipeDrain()

                    // Drain stdout so a large summary can't fill the pipe buffer
                    // and deadlock the child before it exits.
                    stdoutPipe.fileHandleForReading.readabilityHandler = { handle in
                        let chunk = handle.availableData
                        if chunk.isEmpty {
                            handle.readabilityHandler = nil
                        } else {
                            drain.appendStdout(chunk)
                        }
                    }

                    // Parse progress from stderr (and retain it so a failed run
                    // can surface the real CLI error instead of a bare code).
                    stderrPipe.fileHandleForReading.readabilityHandler = { [weak self] handle in
                        let data = handle.availableData
                        if data.isEmpty {
                            handle.readabilityHandler = nil
                            return
                        }
                        drain.appendStderr(data)
                        guard let text = String(data: data, encoding: .utf8) else { return }

                        for line in text.split(separator: "\n") {
                            let l = String(line).trimmingCharacters(in: .whitespaces)

                            if let parsed = IndexETAEstimator.parseProgressLine(l) {
                                Task { @MainActor [weak self] in
                                    // Don't let a late progress update flip a
                                    // run that already finished back to
                                    // .embedding and re-wedge the sheet.
                                    guard let self, !self.indexReachedTerminalPhase else { return }
                                    let enteringEmbedding = self.indexProgress?.phase != .embedding
                                    let elapsedNow = CFAbsoluteTimeGetCurrent() - startTime
                                    let resolvedTotalChunks = parsed.chunksTotal ?? self.indexProgress?.chunksTotal
                                    let resolvedRemaining = parsed.remainingSeconds ?? IndexETAEstimator.liveRemainingSeconds(
                                        current: parsed.current,
                                        total: parsed.total,
                                        elapsedSeconds: elapsedNow,
                                        initialEstimateSeconds: self.indexProgress?.initialEstimatedSeconds
                                    )
                                    let resolvedDPS = parsed.docsPerSec ?? (elapsedNow > 0.25 && parsed.current > 0 ? Double(parsed.current) / elapsedNow : nil)

                                    self.embeddingProgress = EmbeddingProgress(
                                        current: parsed.current,
                                        total: parsed.total,
                                        chunksDone: parsed.chunksDone,
                                        chunksTotal: resolvedTotalChunks,
                                        docsPerSec: resolvedDPS,
                                        remainingSeconds: resolvedRemaining,
                                        estimatedTotalSeconds: self.indexProgress?.initialEstimatedSeconds
                                    )
                                    self.indexProgress?.phase = .embedding
                                    self.indexProgress?.embeddingCurrent = parsed.current
                                    self.indexProgress?.embeddingTotal = parsed.total
                                    if let cd = parsed.chunksDone { self.indexProgress?.chunksDone = cd }
                                    if let ct = resolvedTotalChunks { self.indexProgress?.chunksTotal = ct }
                                    self.indexProgress?.docsPerSec = resolvedDPS
                                    self.indexProgress?.estimatedRemainingSeconds = resolvedRemaining
                                    if enteringEmbedding {
                                        self.actionTracker.record(
                                            category: .index,
                                            action: "index.phase",
                                            phase: .progress,
                                            summary: "Generating embeddings (\(parsed.total) document(s))",
                                            metadata: ["phase": "embedding", "total": "\(parsed.total)"]
                                        )
                                    }
                                    if enteringEmbedding || parsed.current == parsed.total || parsed.current % 5 == 0 {
                                        self.actionTracker.updateStatus { status in
                                            status.indexPhase = IndexPhase.embedding.rawValue
                                            status.indexFraction = self.indexFraction
                                            status.embeddingCurrent = parsed.current
                                            status.embeddingTotal = parsed.total
                                            status.indexEstimatedRemainingSeconds = resolvedRemaining
                                            status.indexDocsPerSec = resolvedDPS
                                        }
                                    }
                                }
                                continue
                            }

                            if let startInfo = IndexETAEstimator.parseStartLine(l) {
                                Task { @MainActor [weak self] in
                                    guard let self, !self.indexReachedTerminalPhase else { return }
                                    let enteringEmbedding = self.indexProgress?.phase != .embedding
                                    self.indexProgress?.phase = .embedding
                                    self.indexProgress?.embeddingTotal = startInfo.totalDocs
                                    if let ct = startInfo.totalChunks { self.indexProgress?.chunksTotal = ct }
                                    if let est = startInfo.estimatedSeconds, est > 0 {
                                        self.indexProgress?.initialEstimatedSeconds = est
                                        self.indexProgress?.estimatedRemainingSeconds = est
                                    }
                                    let totalChunks = startInfo.totalChunks ?? self.indexProgress?.chunksTotal
                                    let initEst = startInfo.estimatedSeconds ?? self.indexProgress?.initialEstimatedSeconds
                                    self.embeddingProgress = EmbeddingProgress(
                                        current: 0,
                                        total: startInfo.totalDocs,
                                        chunksDone: 0,
                                        chunksTotal: totalChunks,
                                        docsPerSec: nil,
                                        remainingSeconds: initEst,
                                        estimatedTotalSeconds: initEst
                                    )
                                    if enteringEmbedding {
                                        self.actionTracker.record(
                                            category: .index,
                                            action: "index.phase",
                                            phase: .progress,
                                            summary: "Generating embeddings (\(startInfo.totalDocs) document(s))",
                                            metadata: ["phase": "embedding", "total": "\(startInfo.totalDocs)"]
                                        )
                                    }
                                    self.actionTracker.updateStatus { status in
                                        status.indexPhase = IndexPhase.embedding.rawValue
                                        status.embeddingTotal = startInfo.totalDocs
                                        status.indexEstimatedRemainingSeconds = initEst
                                    }
                                }
                                continue
                            }

                            // File indexed: "  path/to/file.md" (indented path)
                            if l.hasSuffix(".md") && !l.contains(":") {
                                let fileName = l
                                Task { @MainActor [weak self] in
                                    guard let self, !self.indexReachedTerminalPhase else { return }
                                    self.indexProgress?.currentFile = fileName
                                    self.indexProgress?.filesIndexed += 1
                                }
                            }
                        }
                    }

                    process.terminationHandler = { proc in
                        stdoutPipe.fileHandleForReading.readabilityHandler = nil
                        stderrPipe.fileHandleForReading.readabilityHandler = nil
                        drain.appendStdout(stdoutPipe.fileHandleForReading.readDataToEndOfFile())
                        drain.appendStderr(stderrPipe.fileHandleForReading.readDataToEndOfFile())
                        let out = String(data: drain.stdoutData, encoding: .utf8) ?? ""
                        let err = String(data: drain.stderrData, encoding: .utf8) ?? ""
                        continuation.resume(returning: (proc.terminationStatus, out, err))
                    }

                    do {
                        try process.run()
                    } catch {
                        Self.releaseFailedLaunch(process, pipes: [stdoutPipe, stderrPipe])
                        continuation.resume(throwing: error)
                    }
                }

                let elapsed = CFAbsoluteTimeGetCurrent() - startTime
                let durationMs = Int((elapsed * 1000).rounded())

                // Counters come from the parsed envelope, not from a regex over
                // prose. The keys are contractual (always present, lists never
                // null), so there is nothing left for a scrape to add.
                let summary = AppState.parseFullIndexSummary(result.stdout)
                if let summary {
                    indexProgress?.docsIndexed = summary.docsIndexed
                    indexProgress?.chunksCreated = summary.chunksCreated
                    indexProgress?.linksFound = summary.linksFound
                }

                indexProgress?.elapsed = elapsed

                // Reopen the database to pick up changes before flipping isIndexing.
                do {
                    self.database = try DatabaseManager(path: vault.indexDBPath)
                } catch {
                    let safeErr = Self.boundedRedacted(error.localizedDescription)
                    log.error("Failed to reopen index database after rebuild: \(safeErr)")
                    errorLogger?.log("Failed to reopen index database after rebuild: \(safeErr)")
                }

                isIndexing = false
                embeddingProgress = nil
                await refreshAIStatus()
                await refreshMetrics()

                if result.exitCode == 0 {
                    indexProgress?.phase = .complete
                    applyFullIndexOutcome(summary)
                    let docsCount = summary?.docsIndexed ?? (indexProgress?.docsIndexed ?? 0)
                    let chunksCount = summary?.chunksCreated ?? (indexProgress?.chunksCreated ?? 0)
                    let linksCount = summary?.linksFound ?? (indexProgress?.linksFound ?? 0)
                    let skippedCount = (summary?.unparseable.count ?? 0) + (summary?.unreadable.count ?? 0)
                    actionTracker.updateStatus { status in
                        status.isIndexing = false
                        status.indexPhase = IndexPhase.complete.rawValue
                        status.indexForceReembed = forceReembed
                        status.indexFraction = nil
                        status.indexEstimatedRemainingSeconds = nil
                        status.indexDocsPerSec = nil
                        status.filesIndexed = indexProgress?.filesIndexed ?? docsCount
                        status.docsIndexed = docsCount
                        status.chunksCreated = chunksCount
                        status.lastIndexError = nil
                        status.lastIndexingProblemPath = self.lastIndexingProblem?.path
                        status.lastIndexingProblemMessage = self.lastIndexingProblem?.message
                    }
                    actionTracker.record(
                        category: .index,
                        action: "index.complete",
                        phase: .succeeded,
                        durationMs: durationMs,
                        summary: "\(forceReembed ? "Re-embed" : "Index sync") complete: \(docsCount) docs, \(chunksCount) chunks, \(linksCount) links",
                        metadata: [
                            "docs_indexed": "\(docsCount)",
                            "chunks_created": "\(chunksCount)",
                            "links_found": "\(linksCount)",
                            "force_reembed": String(forceReembed),
                            "skipped_count": "\(skippedCount)",
                        ]
                    )
                    log.info("Index rebuild completed in \(String(format: "%.1f", elapsed))s (exit 0)")
                } else {
                    // Surface the actionable CLI failure rather than a bare exit
                    // code or a raw 120KB stderr progress dump. Keep the full
                    // stderr in the per-vault ErrorLogger file, and cap the
                    // action telemetry detail to the trailing lines.
                    let detail = result.stderr.trimmingCharacters(in: .whitespacesAndNewlines)
                    let rawErrMessage = AppState.extractIndexFailureMessage(
                        stderr: detail,
                        exitCode: result.exitCode
                    )
                    let errMessage = Self.boundedRedacted(rawErrMessage)
                    let cappedDetail = detail
                        .split(separator: "\n", omittingEmptySubsequences: false)
                        .suffix(40)
                        .joined(separator: "\n")
                    indexError = errMessage
                    indexProgress?.phase = .failed
                    indexProgress?.error = errMessage
                    actionTracker.updateStatus { status in
                        status.isIndexing = false
                        status.indexPhase = IndexPhase.failed.rawValue
                        status.indexFraction = nil
                        status.indexEstimatedRemainingSeconds = nil
                        status.indexDocsPerSec = nil
                        status.lastIndexError = errMessage
                    }
                    actionTracker.record(
                        category: .index,
                        action: "index.failed",
                        phase: .failed,
                        durationMs: durationMs,
                        summary: "Index rebuild failed: \(errMessage)",
                        detail: cappedDetail.isEmpty ? rawErrMessage : cappedDetail,
                        metadata: [
                            "exit_code": "\(result.exitCode)",
                            "force_reembed": String(forceReembed),
                        ]
                    )
                    log.warning("Index rebuild finished with exit code \(result.exitCode) in \(String(format: "%.1f", elapsed))s: \(errMessage, privacy: .public)")
                    errorLogger?.log("Index rebuild exited \(result.exitCode): \(Self.boundedRedacted(detail))")
                }
            } catch {
                // Launch failure (process never started).
                let elapsed = CFAbsoluteTimeGetCurrent() - startTime
                let durationMs = Int((elapsed * 1000).rounded())
                let safeErr = Self.boundedRedacted(error.localizedDescription)
                log.error("Index rebuild failed to launch after \(String(format: "%.1f", elapsed))s: \(safeErr)")
                errorLogger?.log("Failed to rebuild index: \(safeErr)")
                indexError = safeErr
                indexProgress?.phase = .failed
                indexProgress?.error = safeErr
                indexProgress?.elapsed = elapsed
                isIndexing = false
                embeddingProgress = nil
                await refreshAIStatus()
                await refreshMetrics()
                actionTracker.updateStatus { status in
                    status.isIndexing = false
                    status.indexPhase = IndexPhase.failed.rawValue
                    status.indexFraction = nil
                    status.indexEstimatedRemainingSeconds = nil
                    status.indexDocsPerSec = nil
                    status.lastIndexError = safeErr
                }
                actionTracker.record(
                    category: .index,
                    action: "index.failed",
                    phase: .failed,
                    durationMs: durationMs,
                    summary: "Index rebuild failed to launch",
                    detail: error.localizedDescription,
                    metadata: ["force_reembed": String(forceReembed)]
                )
            }
        }
    }

    // MARK: - AI Integration

    func refreshAIStatus() async {
        guard let vault else {
            aiStatus = nil
            return
        }
        do {
            let data = try await runCLI(["ai", "status", "--json", "--porcelain"], cwd: vault.rootURL)
            let status = try JSONDecoder().decode(AIStatusInfo.self, from: data)
            self.aiStatus = status
            actionTracker.updateStatus { snapshot in
                snapshot.aiProvider = status.provider
                snapshot.embedProvider = status.effectiveEmbeddingProvider
                snapshot.genProvider = status.effectiveGenerationProvider
                snapshot.genModel = status.genModel
                snapshot.embedModel = status.embeddingModel
                snapshot.dimensions = status.dimensions
                snapshot.embedAvailable = status.embedAvailable
                snapshot.genAvailable = status.genAvailable
                snapshot.documentCount = status.documentCount
                snapshot.embeddingCount = status.embeddingCount
                snapshot.portabilityStatus = status.portabilityStatus
            }
            log.info("AI status: provider=\(status.provider) embed=\(status.embedAvailable) gen=\(status.genAvailable) docs=\(status.documentCount) embeddings=\(status.embeddingCount)")
        } catch {
            log.warning("AI status unavailable: \(error.localizedDescription)")
            self.aiStatus = nil
        }
    }

    func refreshMetrics(limit: Int = 20) async {
        guard let vault else {
            metricsReport = nil
            return
        }
        metricsLoading = true
        defer { metricsLoading = false }
        do {
            let data = try await runCLI(["metrics", "--limit", String(limit), "--json", "--porcelain"], cwd: vault.rootURL)
            let report = try JSONDecoder().decode(MetricsReport.self, from: data)
            self.metricsReport = report
            let summary30d = report.aiUsage?.last30d
            actionTracker.updateStatus { snapshot in
                snapshot.aiUsage30dCostUSD = summary30d?.cloudCostUSD
                snapshot.aiUsage30dCloudCalls = summary30d?.cloudCalls
                snapshot.aiUsage30dLocalCalls = summary30d?.localCalls
            }
        } catch {
            log.debug("Metrics refresh unavailable: \(error.localizedDescription)")
        }
    }

    func clearMetricsHistory() async throws {
        guard let vault else { throw CLIError.noVault }
        _ = try await trackAsyncAction(
            category: .settings,
            action: "settings.clear_metrics",
            startSummary: "Clear AI usage and metrics history"
        ) {
            try await runCLI(["metrics", "clear", "--porcelain"], cwd: vault.rootURL)
        }
        await refreshMetrics()
    }

    func refreshCLIVersion() async {
        let logger = self.errorLogger
        let cliExecutable = resolvedCLIPath()
        let raw: String? = await withCheckedContinuation { continuation in
            let process = Process()
            process.executableURL = URL(fileURLWithPath: cliExecutable)
            process.arguments = ["--version"]
            let stdout = Pipe()
            process.standardOutput = stdout
            process.standardError = Pipe()
            // `--version` prints a single short line, so there's no pipe-buffer
            // deadlock risk; read to end once the process has exited. If run()
            // throws the process never launches, so terminationHandler never
            // fires — exactly one resume either way.
            process.terminationHandler = { _ in
                let data = stdout.fileHandleForReading.readDataToEndOfFile()
                continuation.resume(returning: String(data: data, encoding: .utf8))
            }
            do {
                try process.run()
            } catch {
                Self.releaseFailedLaunch(process, pipes: [stdout, process.standardError as? Pipe])
                logger?.log("2nb --version failed to launch: \(Self.boundedRedacted(error.localizedDescription))")
                continuation.resume(returning: nil)
            }
        }
        if let parsed = CLIVersion.parse(raw) {
            let formatted = "\(parsed.0).\(parsed.1).\(parsed.2)"
            cliVersion = formatted
            actionTracker.updateStatus { $0.cliVersion = formatted }
        } else {
            cliVersion = nil
            actionTracker.updateStatus { $0.cliVersion = nil }
        }
    }

    /// Runs `brew upgrade apresai/tap/twonb` to close the CLI/app version
    /// drift Home warns about, then re-reads the CLI version and AI status.
    /// Kept off `runCLI` because the executable is brew, not 2nb, and no
    /// vault is involved. Throws with brew's stderr on a non-zero exit.
    func upgradeCLI(brewPath: String) async throws {
        try await trackAsyncAction(
            category: .cli,
            action: "cli.upgrade",
            startSummary: "Upgrade 2nb CLI via Homebrew",
            metadata: ["brewPath": brewPath]
        ) {
            let logger = self.errorLogger
            try await withCheckedThrowingContinuation { (continuation: CheckedContinuation<Void, Error>) in
                let process = Process()
                process.executableURL = URL(fileURLWithPath: brewPath)
                process.arguments = ["upgrade", "apresai/tap/twonb"]
                let stdout = Pipe()
                let stderr = Pipe()
                process.standardOutput = stdout
                process.standardError = stderr
                // Drain both pipes as data arrives; brew's download/pour chatter
                // can exceed the pipe buffer and deadlock an undrained child.
                let drain = PipeDrain()
                stdout.fileHandleForReading.readabilityHandler = { handle in
                    let chunk = handle.availableData
                    if chunk.isEmpty { handle.readabilityHandler = nil } else { drain.appendStdout(chunk) }
                }
                stderr.fileHandleForReading.readabilityHandler = { handle in
                    let chunk = handle.availableData
                    if chunk.isEmpty { handle.readabilityHandler = nil } else { drain.appendStderr(chunk) }
                }
                process.terminationHandler = { proc in
                    stdout.fileHandleForReading.readabilityHandler = nil
                    stderr.fileHandleForReading.readabilityHandler = nil
                    drain.appendStdout(stdout.fileHandleForReading.readDataToEndOfFile())
                    drain.appendStderr(stderr.fileHandleForReading.readDataToEndOfFile())
                    if proc.terminationStatus == 0 {
                        continuation.resume()
                    } else {
                        let errMsg = String(data: drain.stderrData, encoding: .utf8)?
                            .trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
                        let safeErrMsg = Self.boundedRedacted(errMsg)
                        logger?.log("brew upgrade twonb failed (exit \(proc.terminationStatus)): \(safeErrMsg)")
                        continuation.resume(throwing: CLIError.nonZeroExit(proc.terminationStatus, message: errMsg))
                    }
                }
                do {
                    try process.run()
                } catch {
                    Self.releaseFailedLaunch(process, pipes: [stdout, stderr])
                    let safeErr = Self.boundedRedacted(error.localizedDescription)
                    logger?.log("brew upgrade twonb launch failed: \(safeErr)")
                    continuation.resume(throwing: error)
                }
            }
        }
        await refreshCLIVersion()
        await refreshAIStatus()
    }

    /// Installs or updates the Obsidian plugin in the bound vault via
    /// `2nb plugin install` (runCLI pins `--vault`, so the bundle lands in
    /// the vault this dashboard shows).
    func installObsidianPlugin() async throws {
        guard let vault else { throw CLIError.noVault }
        _ = try await trackAsyncAction(
            category: .vault,
            action: "plugin.install",
            startSummary: "Install Obsidian plugin in \(vault.rootURL.lastPathComponent)",
            metadata: ["vault": vault.rootURL.lastPathComponent]
        ) {
            try await runCLI(["plugin", "install"], cwd: vault.rootURL)
        }
    }

    /// Checks for a newer 2ndbrain release via `2nb update --json` (the latest
    /// release tag is cached by the CLI for 24h). Best-effort: an unreachable
    /// network, an older CLI without the `update` command, or empty/unparseable
    /// output returns nil so the Updates tab shows "couldn't check". The pinned
    /// `--vault` is ignored by `update`, which is vault-agnostic.
    func checkForUpdates() async -> UpdateInfo? {
        guard let vault else { return nil }
        do {
            let data = try await runCLI(["update", "--json"], cwd: vault.rootURL)
            if data.isEmpty { return nil }
            return try JSONDecoder().decode(UpdateInfo.self, from: data)
        } catch {
            log.warning("update check unavailable: \(error.localizedDescription)")
            return nil
        }
    }

    /// Refreshes the Claude Code skill install status via `2nb skills list
    /// --json`. Best-effort: an unreachable or pre-0.8.1 CLI (no `skills list`)
    /// leaves `skillStatuses` empty and is logged, not surfaced (the Home row
    /// then reads "unknown").
    func refreshSkillStatus() async {
        guard let vault else {
            skillStatuses = []
            return
        }
        do {
            let data = try await runCLI(["skills", "list", "--json"], cwd: vault.rootURL)
            if data.isEmpty {
                skillStatuses = []
                return
            }
            skillStatuses = try JSONDecoder().decode([SkillStatusInfo].self, from: data)
        } catch {
            log.warning("skills list unavailable: \(error.localizedDescription)")
            skillStatuses = []
        }
    }

    /// Refreshes which clients have the 2ndbrain MCP server configured for this
    /// vault via `2nb mcp configured --all --json` (one entry per client).
    /// Best-effort, same as `refreshSkillStatus`: empty/unparseable/older-CLI →
    /// empty → each row reads "unknown".
    func refreshMCPConfigured() async {
        guard let vault else {
            mcpConfiguredAll = []
            return
        }
        do {
            let data = try await runCLI(["mcp", "configured", "--all", "--json"], cwd: vault.rootURL)
            if data.isEmpty {
                mcpConfiguredAll = []
                return
            }
            mcpConfiguredAll = try JSONDecoder().decode([MCPConfiguredInfo].self, from: data)
        } catch {
            log.warning("mcp configured unavailable: \(error.localizedDescription)")
            mcpConfiguredAll = []
        }
    }

    /// Refreshes whether the always-loaded 2nb block is present in each client's
    /// global memory file via `2nb instructions configured --all --json`.
    /// Best-effort, same as `refreshMCPConfigured`: empty/unparseable/older-CLI
    /// (no `instructions` command) → empty → the Global-instructions row is hidden.
    func refreshGlobalInstructions() async {
        guard let vault else {
            globalInstructionsAll = []
            return
        }
        do {
            let data = try await runCLI(["instructions", "configured", "--all", "--json"], cwd: vault.rootURL)
            if data.isEmpty {
                globalInstructionsAll = []
                return
            }
            globalInstructionsAll = try JSONDecoder().decode([GlobalInstructionsInfo].self, from: data)
        } catch {
            log.warning("instructions configured unavailable: \(error.localizedDescription)")
            globalInstructionsAll = []
        }
    }

    // MARK: - Claude Code health (Verify) + one-click MCP setup

    /// Run the real end-to-end self-test: fan out the doctors + a real model test
    /// concurrently and assemble the checklist. User-initiated only (the model
    /// tests make real provider calls) — never call this on a passive refresh.
    func verifyClaudeCode() async {
        guard vault != nil else { return }
        verifying = true
        defer { verifying = false }
        healthChecks = []
        actionTracker.record(
            category: .mcp,
            action: "mcp.verify",
            phase: .started,
            summary: "Run full MCP & AI verification"
        )
        let start = ContinuousClock.now

        async let skill = skillsDoctorReport()
        async let mcp = mcpDoctorReport()
        async let cfg = configDoctorReport()
        async let embed = probeActiveModel(type: "embedding")
        async let gen = probeActiveModel(type: "generation")
        let (s, m, c, e, g) = await (skill, mcp, cfg, embed, gen)

        var checks: [HealthCheck] = []
        checks += ClaudeCodeHealth.checks(group: "Skill", from: s?.checks)
        checks += ClaudeCodeHealth.checks(group: "MCP engine", from: m?.checks)
        checks += ClaudeCodeHealth.checks(group: "AI", from: c?.checks)
        checks.append(ClaudeCodeHealth.modelCheck(e, label: "Embedding"))
        checks.append(ClaudeCodeHealth.modelCheck(g, label: "Generation"))
        healthChecks = checks
        actionTracker.record(
            category: .mcp,
            action: "mcp.verify",
            phase: .succeeded,
            durationMs: elapsedMilliseconds(since: start),
            summary: "Completed \(checks.count) MCP & AI verification checks",
            metadata: ["checks": String(checks.count)]
        )
    }

    func mcpDoctorReport() async -> MCPDoctorInfo? {
        await decodeDoctor(["mcp", "doctor", "--json"])
    }

    func skillsDoctorReport() async -> SkillDoctorInfo? {
        await decodeDoctor(["skills", "doctor", "claude-code", "--json"])
    }

    func configDoctorReport() async -> ConfigDoctorInfo? {
        await decodeDoctor(["config", "doctor", "--json"])
    }

    /// Test the active embedding/generation model with a real provider call.
    func probeActiveModel(type: String) async -> AIProbeResult? {
        guard let vault, let status = aiStatus else { return nil }
        let modelID = type == "embedding" ? status.embeddingModel : status.genModel
        guard !modelID.isEmpty else { return nil }
        do {
            let data = try await runCLI(["models", "test", modelID, "--type", type, "--json", "--porcelain"], cwd: vault.rootURL)
            return try JSONDecoder().decode(AIProbeResult.self, from: data)
        } catch {
            log.warning("models test \(modelID) failed: \(error.localizedDescription)")
            return nil
        }
    }

    /// One-command setup for a single AI client: installs the skill (where the
    /// client has one) plus the MCP server via `2nb setup --client <key> --json`,
    /// returning the decoded per-client results.
    ///
    /// `2nb setup` exits 0 even when a step fails or only emits manual
    /// instructions (e.g. Codex with no `codex` CLI: `configured` false +
    /// `instructions` set), so a zero exit is NOT proof of success — the caller
    /// must inspect the returned `SetupClientResult` (`error`/`instructions`/
    /// `configured`). Passes `--command <resolved CLI path>` so the absolute path
    /// written into a client config (notably Claude Desktop, which launchd can't
    /// PATH-resolve) is this app's version-matched binary, not a bare `2nb`.
    func setupClient(_ client: String) async throws -> [SetupClientResult] {
        guard let vault else { throw CLIError.noVault }
        return try await trackAsyncAction(
            category: .mcp,
            action: "mcp.setup_client",
            startSummary: "Configure MCP client \(client)",
            metadata: ["client": client],
            successDetail: { results in
                ("Configured MCP client \(client) (\(results.count) step(s))", [
                    "client": client,
                    "steps": String(results.count),
                ])
            }
        ) {
            let data = try await runCLI(
                ["setup", "--client", client, "--command", resolvedCLIPath(), "--json", "--porcelain"],
                cwd: vault.rootURL
            )
            return try JSONDecoder().decode([SetupClientResult].self, from: data)
        }
    }

    /// Collapse the index WAL (`vault checkpoint`).
    func checkpointWAL() async throws -> VaultCheckpointResult {
        guard let vault else { throw CLIError.noVault }
        return try await trackAsyncAction(
            category: .vault,
            action: "vault.checkpoint",
            startSummary: "Checkpoint SQLite WAL"
        ) {
            let data = try await runCLI(["vault", "checkpoint", "--json"], cwd: vault.rootURL)
            return try JSONDecoder().decode(VaultCheckpointResult.self, from: data)
        }
    }

    /// Reap stale mcp-server processes (`mcp reap`). `dryRun` previews.
    func reapServers(dryRun: Bool) async throws -> MCPReapResult {
        guard let vault else { throw CLIError.noVault }
        return try await trackAsyncAction(
            category: .mcp,
            action: "mcp.reap",
            startSummary: dryRun ? "Preview stale MCP servers" : "Reap stale MCP servers",
            metadata: ["dryRun": dryRun ? "true" : "false"]
        ) {
            var args = ["mcp", "reap", "--json"]
            if dryRun { args.append("--dry-run") }
            let data = try await runCLI(args, cwd: vault.rootURL)
            return try JSONDecoder().decode(MCPReapResult.self, from: data)
        }
    }

    /// Decode a `* doctor --json` report, tolerating the exit-2 a doctor returns
    /// when it finds problems (the JSON is still emitted).
    private func decodeDoctor<T: Decodable>(_ args: [String]) async -> T? {
        guard let vault else { return nil }
        do {
            let data = try await runCLIAllowingNonZero(args, cwd: vault.rootURL)
            if data.isEmpty { return nil }
            return try JSONDecoder().decode(T.self, from: data)
        } catch {
            log.warning("doctor \(args.joined(separator: " ")) unavailable: \(error.localizedDescription)")
            return nil
        }
    }

    /// Set a single frontmatter field on a note via `2nb meta <path> --set key=value`.
    /// Used by the Validation tab to fix a missing required field or an invalid
    /// enum value in place. The CLI schema-validates the write, so an out-of-range
    /// value surfaces as a `CLIError.nonZeroExit` the caller can show.
    func setMeta(path: String, key: String, value: String) async throws {
        guard let vault else { throw CLIError.noVault }
        suppressExternalReindex(paths: [path], duration: 15.0)
        _ = try await trackAsyncAction(
            category: .lint,
            action: "note.set_meta",
            startSummary: "Set metadata \(key) on \(path)",
            metadata: ["path": path, "key": key]
        ) {
            let data = try await runCLI(["meta", path, "--set", "\(key)=\(value)"], cwd: vault.rootURL)
            suppressExternalReindex(paths: [path], duration: 5.0)
            refreshFiles()
            return data
        }
    }

    /// Deterministically repair the broken wikilink `target` in `path` via
    /// `2nb repair-links`. `preview: true` (default) computes the proposed change
    /// without writing; `preview: false` applies it (`--write`) and snapshots the
    /// original so it's reversible with `polish --undo`. Scoped to `target` so a
    /// single finding's button fixes only the link the user clicked.
    func repairLinks(path: String, target: String, preview: Bool = true) async throws -> PolishResult {
        guard let vault else { throw CLIError.noVault }
        if !preview {
            suppressExternalReindex(paths: [path], duration: 15.0)
        }
        return try await trackAsyncAction(
            category: .lint,
            action: "lint.repair_links",
            startSummary: preview ? "Preview link repair for \(target)" : "Repair link \(target) in \(path)",
            metadata: ["path": path, "target": target, "preview": preview ? "true" : "false"]
        ) {
            var args = ["repair-links", path, "--target", target, "--json", "--porcelain"]
            if !preview { args.append("--write") }
            let data = try await runCLI(args, cwd: vault.rootURL)
            let result = try JSONDecoder().decode(PolishResult.self, from: data)
            if !preview {
                suppressExternalReindex(paths: [path, result.path], duration: 5.0)
                refreshFiles()
            }
            return result
        }
    }

    /// Repair every confident drift link (case / separator / whitespace) across
    /// the given note `paths` in one pass — `2nb repair-links <path> --write`
    /// with NO `--target`, so each file's confidently-resolvable links are all
    /// fixed while ambiguous/unmatched ones are left for a manual fix. Each file
    /// is snapshotted, reversible with `polish <path> --undo`. Powers the
    /// Validation tab's bulk "Repair drift links" button.
    ///
    /// Resilient: a per-file failure (e.g. a note deleted out from under us mid
    /// pass) is logged and counted, not fatal — the remaining files still get
    /// repaired. Returns `(repaired:` total links fixed, `failed:` files skipped`)`
    /// so the caller can report partial success instead of losing it.
    func repairAllDrift(paths: [String]) async throws -> (repaired: Int, failed: Int) {
        guard let vault else { throw CLIError.noVault }
        suppressExternalReindex(paths: paths, duration: 30.0)
        return try await trackAsyncAction(
            category: .lint,
            action: "lint.repair_all_drift",
            startSummary: "Repair drift links across \(paths.count) note(s)",
            metadata: ["count": String(paths.count)],
            successDetail: { outcome in
                ("Repaired \(outcome.repaired) link(s) (\(outcome.failed) failed)", [
                    "repaired": String(outcome.repaired),
                    "failed": String(outcome.failed),
                ])
            }
        ) {
            var repaired = 0
            var failed = 0
            for path in paths {
                do {
                    let data = try await runCLI(
                        ["repair-links", path, "--write", "--json", "--porcelain"], cwd: vault.rootURL)
                    let res = try JSONDecoder().decode(PolishResult.self, from: data)
                    repaired += res.linksRepaired?.count ?? 0
                    suppressExternalReindex(paths: [path, res.path], duration: 5.0)
                } catch {
                    failed += 1
                    log.error("bulk repair-links failed for \(path): \(error.localizedDescription)")
                }
            }
            refreshFiles()
            return (repaired, failed)
        }
    }

    /// Ranked "did you mean?" existing-note candidates for one broken wikilink
    /// `target`, via `2nb suggest-target` (drift / semantic / keyword tiers).
    /// `sourcePath` is the note containing the broken link; the CLI excludes it
    /// from candidates (a note is never a fix for its own broken link).
    /// Read-only. Returns [] rather than throwing on a CLI miss so the
    /// resolution sheet still offers Create/Unlink — no dead end.
    /// Argument construction for `2nb suggest-target`, extracted so the
    /// conditional --source / --llm / --verdict append (the contract the
    /// Validation flow relies on) is unit-testable without spawning the CLI.
    ///
    /// Default limit is 3 (the top recommendations the Validation UI shows).
    /// Bulk classification passes `llm: true, verdict: true` so every row gets
    /// the model-informed recommendation (the CLI still no-ops the model when a
    /// high-confidence candidate already exists, so drift-adjacent findings
    /// never spend a generation call); the sheet reuses the bulk result.
    nonisolated static func suggestTargetArgs(target: String, sourcePath: String?, limit: Int = 3, llm: Bool = false, verdict: Bool = false) -> [String] {
        var args = ["suggest-target", target, "--limit", "\(limit)", "--json", "--porcelain"]
        if let sourcePath { args += ["--source", sourcePath] }
        if llm { args.append("--llm") }
        if verdict { args.append("--verdict") }
        return args
    }

    func suggestTarget(target: String, sourcePath: String? = nil, limit: Int = 3, llm: Bool = false) async throws -> [SuggestTargetResult] {
        guard let vault else { throw CLIError.noVault }
        let args = Self.suggestTargetArgs(target: target, sourcePath: sourcePath, limit: limit, llm: llm)
        let data = try await runCLIAllowingNonZero(args, cwd: vault.rootURL)
        return (try? JSONDecoder().decode([SuggestTargetResult].self, from: data)) ?? []
    }

    /// `2nb suggest-target --llm --verdict`: the recommendation envelope behind
    /// the class-aware Validation flow — one machine recommendation per broken
    /// link (relink or unlink) plus the candidate list and the LLM outcome.
    /// THROWS on a CLI or decode failure (unlike `suggestTarget`, which
    /// degrades to []): the caller must distinguish "the CLI said nothing
    /// matches" from "the check itself failed" so a transient error is never
    /// misclassified as a dead link.
    func suggestTargetVerdict(target: String, sourcePath: String? = nil, limit: Int = 3) async throws -> SuggestVerdictEnvelope {
        guard let vault else { throw CLIError.noVault }
        let args = Self.suggestTargetArgs(target: target, sourcePath: sourcePath, limit: limit, llm: true, verdict: true)
        do {
            let data = try await runCLIAllowingNonZero(args, cwd: vault.rootURL)
            return try JSONDecoder().decode(SuggestVerdictEnvelope.self, from: data)
        } catch {
            // The bulk classifier swallows this throw into a "check failed"
            // badge; persist the cause to the vault error log so a failing
            // probe (throttling, stale CLI, bad JSON) is debuggable from
            // .2ndbrain/logs rather than invisible.
            let safeErr = Self.boundedRedacted((error as? CLIError)?.errorDescription ?? error.localizedDescription)
            errorLogger?.log("suggest-target --verdict probe failed for [[\(target)]]: \(safeErr)")
            throw error
        }
    }

    /// Repoint the broken wikilink `from` in `path` to the chosen existing note
    /// `to` via `2nb relink`. `preview: false` writes + snapshots, reversible
    /// with `polish --undo`.
    func relink(path: String, from: String, to: String, preview: Bool = true) async throws -> PolishResult {
        guard let vault else { throw CLIError.noVault }
        if !preview {
            suppressExternalReindex(paths: [path], duration: 15.0)
        }
        return try await trackAsyncAction(
            category: .lint,
            action: "lint.relink",
            startSummary: preview ? "Preview relink \(from) -> \(to) in \(path)" : "Relink \(from) -> \(to) in \(path)",
            metadata: ["path": path, "from": from, "to": to, "preview": preview ? "true" : "false"]
        ) {
            var args = ["relink", path, "--from", from, "--to", to, "--json", "--porcelain"]
            if !preview { args.append("--write") }
            let data = try await runCLI(args, cwd: vault.rootURL)
            let result = try JSONDecoder().decode(PolishResult.self, from: data)
            if !preview {
                suppressExternalReindex(paths: [path, result.path], duration: 5.0)
                refreshFiles()
            }
            return result
        }
    }

    /// Remove the broken wikilink `target` in `path`, keeping its visible text,
    /// via `2nb unlink`. `preview: false` writes + snapshots, reversible with
    /// `polish --undo`.
    func unlink(path: String, target: String, preview: Bool = true) async throws -> PolishResult {
        guard let vault else { throw CLIError.noVault }
        if !preview {
            suppressExternalReindex(paths: [path], duration: 15.0)
        }
        return try await trackAsyncAction(
            category: .lint,
            action: "lint.unlink",
            startSummary: preview ? "Preview unlink \(target) in \(path)" : "Unlink \(target) in \(path)",
            metadata: ["path": path, "target": target, "preview": preview ? "true" : "false"]
        ) {
            var args = ["unlink", path, "--target", target, "--json", "--porcelain"]
            if !preview { args.append("--write") }
            let data = try await runCLI(args, cwd: vault.rootURL)
            let result = try JSONDecoder().decode(PolishResult.self, from: data)
            if !preview {
                suppressExternalReindex(paths: [path, result.path], duration: 5.0)
                refreshFiles()
            }
            return result
        }
    }

    /// Create a stub note titled `title` (so a broken `[[title]]` link resolves)
    /// via `2nb create`; `subdir` files it under a vault-relative folder.
    @discardableResult
    func createStub(title: String, subdir: String? = nil) async throws -> CreateResult {
        guard let vault else { throw CLIError.noVault }
        return try await trackAsyncAction(
            category: .lint,
            action: "note.create_stub",
            startSummary: "Create stub note: \(title)",
            metadata: ["title": title, "subdir": subdir ?? ""]
        ) {
            var args = ["create", "--title", title, "--type", "note", "--json", "--porcelain"]
            if let subdir, !subdir.isEmpty { args.append(contentsOf: ["--path", subdir]) }
            let data = try await runCLI(args, cwd: vault.rootURL)
            let result = try JSONDecoder().decode(CreateResult.self, from: data)
            suppressExternalReindex(paths: [result.path], duration: 5.0)
            refreshFiles()
            return result
        }
    }

    func askAI(question: String) async throws -> AIAskResult {
        let response = try await askVault(question: question)
        return AIAskResult(answer: response.answer, sources: response.sources)
    }

    // MARK: - Gemini Vault Capabilities (Ask, Search, Polish, Obsidian Deep Links)

    nonisolated static func askArgs(question: String, hasHistory: Bool) -> [String] {
        var args = ["ask", "--json", "--porcelain"]
        if hasHistory {
            args.append(contentsOf: ["--history", "-"])
        }
        args.append(question)
        return args
    }

    nonisolated static func searchArgs(query: String, limit: Int = 20, bm25Only: Bool = false) -> [String] {
        var args = ["search", "--limit", String(limit), "--json", "--porcelain"]
        if bm25Only { args.append("--bm25-only") }
        args.append(query)
        return args
    }

    nonisolated static func polishArgs(
        path: String,
        goal: String? = nil,
        schema: Bool? = nil,
        links: Bool,
        repairLinks: Bool,
        backlinks: Bool = false,
        applyBacklinks: Bool = false,
        write: Bool
    ) -> [String] {
        var args = ["polish", path, "--json", "--porcelain"]
        if let trimmedGoal = goal?.trimmingCharacters(in: .whitespacesAndNewlines), !trimmedGoal.isEmpty {
            args.append(contentsOf: ["--goal", trimmedGoal])
        }
        if let schema {
            args.append(schema ? "--schema" : "--no-schema")
        }
        if links { args.append("--links") }
        if repairLinks { args.append("--repair-links") }
        if backlinks || (write && applyBacklinks) {
            args.append("--backlinks")
        }
        if write { args.append("--write") }
        if write && applyBacklinks {
            args.append("--apply-backlinks")
        }
        return args
    }

    nonisolated static func polishUndoArgs(path: String) -> [String] {
        ["polish", path, "--undo", "--json", "--porcelain"]
    }

    func askVault(question: String, history: [AIChatTurn] = []) async throws -> CLIAskResponse {
        guard let vault else { throw CLIError.noVault }
        let previewQuestion = String(question.prefix(120))
        actionTracker.updateStatus {
            $0.lastAskQuestion = previewQuestion
            $0.lastAskStatus = "asking"
            $0.lastAskSourcesCount = nil
        }
        do {
            let response = try await trackAsyncAction(
                category: .ask,
                action: "ask.query",
                startSummary: "Ask Gemini: \(previewQuestion)",
                metadata: [
                    "question": previewQuestion,
                    "history_turns": String(history.count),
                ],
                successDetail: { (res: CLIAskResponse) in
                    (summary: "Answered query (\(res.sources.count) source(s))", metadata: [
                        "sources": String(res.sources.count),
                        "mode": res.mode,
                    ])
                }
            ) {
                let stdinData = history.isEmpty ? nil : try JSONEncoder().encode(history)
                let args = Self.askArgs(question: question, hasHistory: !history.isEmpty)
                let data = try await runCLI(args, cwd: vault.rootURL, stdin: stdinData)
                return try JSONDecoder().decode(CLIAskResponse.self, from: data)
            }
            self.lastSemanticWarnings = response.warnings ?? []
            actionTracker.updateStatus {
                $0.lastAskStatus = "done"
                $0.lastAskSourcesCount = response.sources.count
                $0.semanticWarnings = response.warnings ?? []
            }
            Task { @MainActor [weak self] in await self?.refreshMetrics() }
            return response
        } catch is CancellationError {
            throw CancellationError()
        } catch {
            actionTracker.updateStatus { $0.lastAskStatus = "failed" }
            throw error
        }
    }

    func searchVault(query: String, limit: Int = 20, bm25Only: Bool = false) async throws -> CLISearchResponse {
        guard let vault else { throw CLIError.noVault }
        let mode = bm25Only ? "bm25" : "hybrid"
        do {
            let response = try await trackAsyncAction(
                category: .search,
                action: "search.query",
                startSummary: "\(bm25Only ? "BM25" : "Hybrid") search: \(query)",
                metadata: ["query": query, "mode": mode, "limit": String(limit)],
                successDetail: { (res: CLISearchResponse) in
                    (summary: "\(res.mode) search found \(res.results.count) hit(s) for \"\(query)\"", metadata: [
                        "query": query,
                        "mode": res.mode,
                        "hits": String(res.results.count),
                        "top_path": res.results.first?.path ?? "",
                    ])
                }
            ) {
                let args = Self.searchArgs(query: query, limit: limit, bm25Only: bm25Only)
                let data = try await runCLI(args, cwd: vault.rootURL, cancellable: true)
                return try JSONDecoder().decode(CLISearchResponse.self, from: data)
            }
            // A superseded search that finished anyway must not overwrite
            // the newer query's status or spawn a metrics refresh.
            try Task.checkCancellation()
            if !bm25Only {
                self.lastSemanticWarnings = response.warnings ?? []
            }
            actionTracker.updateStatus {
                $0.lastSearchQuery = query
                $0.lastSearchMode = response.mode
                $0.lastSearchHitCount = response.results.count
                $0.lastSearchTopPath = response.results.first?.path
                if !bm25Only {
                    $0.semanticWarnings = response.warnings ?? []
                }
            }
            if !bm25Only {
                Task { @MainActor [weak self] in await self?.refreshMetrics() }
            }
            return response
        } catch is CancellationError {
            throw CancellationError()
        } catch {
            actionTracker.updateStatus { $0.lastSearchPhase = "failed" }
            throw error
        }
    }

    func listVaultNotes(limit: Int = 200) async throws -> [VaultNoteItem] {
        guard let vault else { throw CLIError.noVault }
        let data = try await runCLI(["list", "--limit", String(limit), "--sort", "modified", "--json", "--porcelain"], cwd: vault.rootURL)
        return (try? JSONDecoder().decode([VaultNoteItem].self, from: data)) ?? []
    }

    /// Search-first note picker query across `DatabaseManager` (title, path,
    /// and FTS5/chunk content), falling back off-main-actor to scanning disk
    /// `.md` files only when the vault has not been indexed yet (`database == nil`
    /// or `documentCount() == 0`).
    func searchNotesForPicker(query: String, limit: Int = 50) async -> [NotePickerItem] {
        guard let vault else { return [] }
        let db = self.database
        let trimmed = query.trimmingCharacters(in: .whitespacesAndNewlines)
        return await Task.detached(priority: .userInitiated) {
            if let db {
                let docCount = (try? db.documentCount()) ?? 0
                if docCount > 0 {
                    return (try? db.searchNotes(query: trimmed, limit: limit)) ?? []
                }
            }
            return Self.searchNotesOnDisk(vault: vault, query: trimmed, limit: limit)
        }.value
    }

    nonisolated static func searchNotesOnDisk(vault: VaultManager, query: String, limit: Int = 50) -> [NotePickerItem] {
        let boundedLimit = max(1, min(limit, 500))
        let trimmed = query.trimmingCharacters(in: .whitespacesAndNewlines)
        let excludedFolders = vault.obsidianTemplateFolders()
        let files = vault.listMarkdownFiles().filter { url in
            let rel = vault.relativePath(for: url)
            return !VaultManager.isExcludedFolderPath(rel, excludedFolders: excludedFolders)
        }
        let iso = ISO8601DateFormatter()

        if trimmed.isEmpty {
            let withDates: [(url: URL, rel: String, modified: Date)] = files.map { url in
                let vals = try? url.resourceValues(forKeys: [.contentModificationDateKey])
                return (url, vault.relativePath(for: url), vals?.contentModificationDate ?? .distantPast)
            }
            let sorted = withDates.sorted {
                if $0.modified != $1.modified { return $0.modified > $1.modified }
                return $0.rel < $1.rel
            }
            var items: [NotePickerItem] = []
            items.reserveCapacity(min(boundedLimit, sorted.count))
            for entry in sorted {
                if Task.isCancelled { break }
                let text = (try? String(contentsOf: entry.url, encoding: .utf8)) ?? ""
                if VaultManager.hasTemplatePlaceholders(text) {
                    continue
                }
                let (fm, _) = FrontmatterParser.parse(text)
                let title = (fm["title"] as? String) ?? ""
                let docType = (fm["type"] as? String) ?? ""
                let status = (fm["status"] as? String) ?? ""
                items.append(NotePickerItem(
                    id: entry.rel,
                    path: entry.rel,
                    title: title,
                    docType: docType,
                    status: status,
                    modifiedAt: entry.modified == .distantPast ? "" : iso.string(from: entry.modified),
                    snippet: nil,
                    matchSource: "recent"
                ))
                if items.count >= boundedLimit { break }
            }
            return items
        }

        var scored: [(score: Int, modified: Date, item: NotePickerItem)] = []
        for url in files {
            if Task.isCancelled { break }
            let rel = vault.relativePath(for: url)
            let text = (try? String(contentsOf: url, encoding: .utf8)) ?? ""
            if VaultManager.hasTemplatePlaceholders(text) {
                continue
            }
            let pathExact = rel.compare(trimmed, options: [.caseInsensitive, .diacriticInsensitive]) == .orderedSame
            let pathMatch = rel.localizedCaseInsensitiveContains(trimmed)
            let (fm, body) = FrontmatterParser.parse(text)
            let title = (fm["title"] as? String) ?? ""
            let titleExact = !title.isEmpty && title.compare(trimmed, options: [.caseInsensitive, .diacriticInsensitive]) == .orderedSame
            let titleMatch = !title.isEmpty && title.localizedCaseInsensitiveContains(trimmed)
            let bodyMatch = text.localizedCaseInsensitiveContains(trimmed)

            guard pathMatch || titleMatch || bodyMatch else { continue }

            let rank: Int
            let source: String
            if pathExact || titleExact {
                rank = 0
                source = titleExact ? "title" : "path"
            } else if titleMatch {
                rank = 1
                source = "title"
            } else if pathMatch {
                rank = 2
                source = "path"
            } else {
                rank = 3
                source = "content"
            }

            let vals = try? url.resourceValues(forKeys: [.contentModificationDateKey])
            let modified = vals?.contentModificationDate ?? .distantPast
            let snippet = bodyMatch ? Self.extractDiskSnippet(from: body.isEmpty ? text : body, matching: trimmed) : nil
            let item = NotePickerItem(
                id: rel,
                path: rel,
                title: title,
                docType: (fm["type"] as? String) ?? "",
                status: (fm["status"] as? String) ?? "",
                modifiedAt: modified == .distantPast ? "" : iso.string(from: modified),
                snippet: snippet,
                matchSource: source
            )
            scored.append((rank, modified, item))
        }

        scored.sort {
            if $0.score != $1.score { return $0.score < $1.score }
            if $0.modified != $1.modified { return $0.modified > $1.modified }
            return $0.item.path < $1.item.path
        }
        return Array(scored.prefix(boundedLimit).map(\.item))
    }

    nonisolated private static func extractDiskSnippet(from text: String, matching query: String) -> String? {
        let collapsed = text
            .replacingOccurrences(of: "\r\n", with: " ")
            .replacingOccurrences(of: "\n", with: " ")
            .trimmingCharacters(in: .whitespacesAndNewlines)
        guard !collapsed.isEmpty else { return nil }
        guard let range = collapsed.range(of: query, options: .caseInsensitive) else {
            return String(collapsed.prefix(140))
        }
        let start = collapsed.index(range.lowerBound, offsetBy: -40, limitedBy: collapsed.startIndex) ?? collapsed.startIndex
        let end = collapsed.index(range.upperBound, offsetBy: 100, limitedBy: collapsed.endIndex) ?? collapsed.endIndex
        return String(collapsed[start..<end])
    }

    func polishNote(
        path: String,
        goal: String? = nil,
        schema: Bool = true,
        links: Bool = true,
        repairLinks: Bool = true,
        backlinks: Bool = false,
        applyBacklinks: Bool = false,
        write: Bool = false,
        expectedBacklinkPaths: [String] = []
    ) async throws -> PolishResult {
        guard let vault else { throw CLIError.noVault }
        let trimmedGoal = goal?.trimmingCharacters(in: .whitespacesAndNewlines)
        if write {
            suppressExternalReindex(paths: [path] + expectedBacklinkPaths, duration: 60.0)
        }
        var meta: [String: String] = [
            "path": path,
            "write": write ? "true" : "false",
            "links": links ? "true" : "false",
            "repair_links": repairLinks ? "true" : "false",
            "schema": schema ? "true" : "false",
            "backlinks": backlinks ? "true" : "false",
            "apply_backlinks": (write && applyBacklinks) ? "true" : "false",
        ]
        if let trimmedGoal, !trimmedGoal.isEmpty {
            meta["goal"] = String(trimmedGoal.prefix(120))
        }
        let result: PolishResult
        do {
            result = try await trackAsyncAction(
                category: .polish,
                action: "polish.run",
                startSummary: write ? "Polish and write \(path)" : "Preview polish for \(path)",
                metadata: meta
            ) {
                let args = Self.polishArgs(
                    path: path,
                    goal: trimmedGoal,
                    schema: schema,
                    links: links,
                    repairLinks: repairLinks,
                    backlinks: backlinks,
                    applyBacklinks: applyBacklinks,
                    write: write
                )
                let data = try await runCLI(args, cwd: vault.rootURL)
                let decoded = try JSONDecoder().decode(PolishResult.self, from: data)
                if write {
                    let appliedBacklinks = decoded.backlinkPreviews?
                        .filter(\.applied)
                        .map(\.path) ?? []
                    suppressExternalReindex(
                        paths: Array(Set([path, decoded.path] + expectedBacklinkPaths + appliedBacklinks)),
                        duration: 5.0,
                        clampExisting: true
                    )
                    refreshFiles()
                }
                return decoded
            }
        } catch {
            if write {
                suppressExternalReindex(
                    paths: Array(Set([path] + expectedBacklinkPaths)),
                    duration: 5.0,
                    clampExisting: true
                )
            }
            throw error
        }
        Task { @MainActor [weak self] in await self?.refreshMetrics() }
        return result
    }

    func undoPolishNote(path: String, expectedBacklinkPaths: [String] = []) async throws -> PolishUndoResult {
        guard let vault else { throw CLIError.noVault }
        suppressExternalReindex(paths: [path] + expectedBacklinkPaths, duration: 15.0)
        do {
            return try await trackAsyncAction(
                category: .polish,
                action: "polish.undo",
                startSummary: "Undo last polish for \(path)",
                metadata: ["path": path],
                successDetail: { res in
                    let count = res.restoredPaths?.count ?? (res.reverted ? 1 : 0)
                    return ("Restored \(count) file(s)", [
                        "path": res.path,
                        "restored_count": String(count),
                    ])
                }
            ) {
                let args = Self.polishUndoArgs(path: path)
                let data = try await runCLI(args, cwd: vault.rootURL)
                let res = try JSONDecoder().decode(PolishUndoResult.self, from: data)
                let restored = (res.restoredPaths?.isEmpty == false) ? (res.restoredPaths ?? [res.path]) : [res.path]
                suppressExternalReindex(
                    paths: Array(Set([path] + expectedBacklinkPaths + restored)),
                    duration: 5.0,
                    clampExisting: true
                )
                refreshFiles()
                return res
            }
        } catch {
            suppressExternalReindex(
                paths: Array(Set([path] + expectedBacklinkPaths)),
                duration: 5.0,
                clampExisting: true
            )
            throw error
        }
    }

    func fetchStaleCount(sinceDays: Int = 90) async -> Int {
        guard let vault else { return 0 }
        guard let data = try? await runCLI(["stale", "--since", String(sinceDays), "--json", "--porcelain"], cwd: vault.rootURL) else {
            return 0
        }
        return (try? JSONDecoder().decode([StaleDocInfo].self, from: data))?.count ?? 0
    }

    func openNoteInObsidian(relativePath: String) {
        guard let vault else { return }
        let fileURL = vault.rootURL.appendingPathComponent(relativePath)
        let opened = ObsidianURL.open(
            vaultName: vault.rootURL.lastPathComponent,
            relativePath: relativePath,
            absoluteFileURL: fileURL
        )
        actionTracker.record(
            category: .navigation,
            action: "obsidian.open_note",
            phase: opened ? .succeeded : .failed,
            summary: opened ? "Opened \(relativePath) in Obsidian" : "Failed to open \(relativePath) in Obsidian",
            metadata: ["path": relativePath]
        )
    }

    func openVaultInObsidian() {
        guard let vault else { return }
        let name = vault.rootURL.lastPathComponent
        var opened = false
        if let encoded = name.addingPercentEncoding(withAllowedCharacters: .urlQueryAllowed),
           let url = URL(string: "obsidian://open?vault=\(encoded)"),
           NSWorkspace.shared.open(url) {
            opened = true
        } else {
            opened = NSWorkspace.shared.open(vault.rootURL)
        }
        actionTracker.record(
            category: .navigation,
            action: "obsidian.open_vault",
            phase: opened ? .succeeded : .failed,
            summary: opened ? "Opened vault \(name) in Obsidian" : "Failed to open vault \(name)",
            metadata: ["vault": name]
        )
    }

    // MARK: - Tools Integration

    nonisolated static func fixVaultArgs(write: Bool, ai: Bool = false) -> [String] {
        var args = ["fix"]
        if write { args.append("--write") }
        if ai { args.append("--ai") }
        args.append(contentsOf: ["--json", "--porcelain"])
        return args
    }

    nonisolated static func undoFixVaultArgs(force: Bool = false) -> [String] {
        force ? ["fix", "--undo", "--force", "--json", "--porcelain"] : ["fix", "--undo", "--json", "--porcelain"]
    }

    private func fetchLintReport(vaultRoot: URL) async -> Result<LintReport, Error> {
        do {
            let result = try await runCLIAllowingNonZeroWithOutput(["lint", "--json", "--porcelain"], cwd: vaultRoot)
            do {
                let report = try JSONDecoder().decode(LintReport.self, from: result.stdout)
                return .success(report)
            } catch {
                let errText = result.stderr.trimmingCharacters(in: .whitespacesAndNewlines)
                if !errText.isEmpty {
                    let code = result.exitCode == 0 ? Int32(1) : result.exitCode
                    return .failure(CLIError.nonZeroExit(code, message: errText))
                }
                return .failure(error)
            }
        } catch {
            return .failure(error)
        }
    }

    private func fetchFixPreviewReport(vaultRoot: URL) async -> Result<FixVaultReport, Error> {
        do {
            let data = try await runCLI(Self.fixVaultArgs(write: false), cwd: vaultRoot)
            let report = try JSONDecoder().decode(FixVaultReport.self, from: data)
            return .success(report)
        } catch {
            return .failure(error)
        }
    }

    func runLint() async {
        guard let vault else { return }
        isLinting = true
        lintReport = nil
        fixReport = nil
        lintError = nil
        fixPreviewError = nil
        log.info("Lint validation started for vault: \(vault.rootURL.lastPathComponent)")
        actionTracker.record(
            category: .lint,
            action: "lint.run",
            phase: .started,
            summary: "Run vault lint validation"
        )
        let start = ContinuousClock.now
        async let lintOutcome = fetchLintReport(vaultRoot: vault.rootURL)
        async let fixOutcome = fetchFixPreviewReport(vaultRoot: vault.rootURL)
        let (lintResult, fixResult) = await (lintOutcome, fixOutcome)

        let elapsedMs = elapsedMilliseconds(since: start)

        switch fixResult {
        case .success(let preview):
            fixReport = preview
        case .failure(let error):
            let rawMsg = (error as? CLIError)?.errorDescription ?? error.localizedDescription
            let msg = Self.boundedRedacted(rawMsg)
            fixPreviewError = msg
            log.warning("Fix preview failed: \(msg, privacy: .public)")
            errorLogger?.log("Fix preview failed: \(msg)")
        }

        switch lintResult {
        case .success(let report):
            lintReport = report
            log.info("Lint completed in \(Formatters.duration(elapsedMs)): \(report.filesChecked) files, \(report.errors) errors, \(report.warnings) warnings")
            actionTracker.record(
                category: .lint,
                action: "lint.run",
                phase: .succeeded,
                durationMs: elapsedMs,
                summary: "Lint checked \(report.filesChecked) files (\(report.errors) error(s), \(report.warnings) warning(s))",
                metadata: [
                    "files": String(report.filesChecked),
                    "errors": String(report.errors),
                    "warnings": String(report.warnings),
                ]
            )
        case .failure(let error):
            let rawMsg = (error as? CLIError)?.errorDescription ?? error.localizedDescription
            let msg = Self.boundedRedacted(rawMsg)
            lintError = msg
            lintReport = nil
            log.error("Lint failed after \(Formatters.duration(elapsedMs)): \(msg, privacy: .public)")
            errorLogger?.log("Lint validation failed: \(msg)")
            actionTracker.record(
                category: .lint,
                action: "lint.run",
                phase: .failed,
                durationMs: elapsedMs,
                summary: "Vault lint failed",
                detail: rawMsg
            )
        }
        isLinting = false
    }

    nonisolated static func validateAppliedFixReport(_ report: FixVaultReport) throws {
        guard !report.written else { return }
        if let skipReason = report.aiSkippedReason, !skipReason.isEmpty {
            throw CLIError.nonZeroExit(1, message: "AI repair skipped: \(skipReason)")
        }
        if let firstWarning = report.warnings.first(where: {
            !$0.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
        }) {
            throw CLIError.nonZeroExit(1, message: firstWarning)
        }
    }

    @discardableResult
    func applyVaultFix() async throws -> FixVaultReport {
        guard let vault else { throw CLIError.noVault }
        isFixingVault = true
        defer { isFixingVault = false }
        let predictedPaths = (fixReport?.notes.map(\.path) ?? [])
            + (fixReport?.aiFixableIssues.map(\.path) ?? [])
        suppressExternalReindex(paths: predictedPaths, duration: 60.0)
        log.info("Fix Vault started for vault: \(vault.rootURL.lastPathComponent)")
        actionTracker.record(
            category: .lint,
            action: "lint.fix_vault",
            phase: .started,
            summary: "Fix vault links, formatting, and schemas"
        )
        let start = ContinuousClock.now
        do {
            let data = try await runCLI(Self.fixVaultArgs(write: true, ai: false), cwd: vault.rootURL)
            let report = try JSONDecoder().decode(FixVaultReport.self, from: data)
            let elapsedMs = elapsedMilliseconds(since: start)

            try Self.validateAppliedFixReport(report)

            let writtenPaths = report.notes.map(\.path)
            lastFixWrittenPaths = writtenPaths
            suppressExternalReindex(
                paths: Array(Set(predictedPaths + writtenPaths)),
                duration: 5.0,
                clampExisting: true
            )
            fixReport = report

            var meta: [String: String] = [
                "total_fixes": String(report.totalFixes),
                "notes_changed": String(report.notesChanged),
                "links_updated": String(report.linksUpdated),
                "links_unlinked": String(report.linksUnlinked),
                "formatting_fixed": String(report.formattingFixed),
                "schema_updated": report.schemaUpdated ? "true" : "false",
                "ai_applied": report.aiApplied ? "true" : "false",
                "ai_fixes_applied": String(report.aiFixesApplied),
            ]
            if let provider = report.aiProvider, !provider.isEmpty {
                meta["ai_provider"] = provider
            }
            if let model = report.aiModel, !model.isEmpty {
                meta["ai_model"] = model
            }
            if let skip = report.aiSkippedReason, !skip.isEmpty {
                meta["ai_skipped_reason"] = skip
            }

            actionTracker.record(
                category: .lint,
                action: "lint.fix_vault",
                phase: .succeeded,
                durationMs: elapsedMs,
                summary: "Fixed \(report.totalFixes) item(s) across \(report.notesChanged) note(s)",
                metadata: meta
            )
            refreshFiles()
            Task { @MainActor [weak self] in await self?.refreshMetrics() }
            await runLint()
            return report
        } catch {
            suppressExternalReindex(paths: predictedPaths, duration: 5.0, clampExisting: true)
            let elapsedMs = elapsedMilliseconds(since: start)
            let rawMsg = (error as? CLIError)?.errorDescription ?? error.localizedDescription
            let safeMsg = Self.boundedRedacted(rawMsg)
            errorLogger?.log("Fix Vault failed: \(safeMsg)")
            actionTracker.record(
                category: .lint,
                action: "lint.fix_vault",
                phase: .failed,
                durationMs: elapsedMs,
                summary: "Fix Vault failed",
                detail: rawMsg
            )
            throw error
        }
    }

    @discardableResult
    func undoVaultFix(force: Bool = false) async throws -> FixUndoResult {
        guard let vault else { throw CLIError.noVault }
        isFixingVault = true
        defer { isFixingVault = false }
        let priorWrittenPaths = lastFixWrittenPaths
        suppressExternalReindex(paths: priorWrittenPaths, duration: 15.0)
        log.info("Undo Last Fix started for vault: \(vault.rootURL.lastPathComponent)")
        actionTracker.record(
            category: .lint,
            action: "lint.undo_fix_vault",
            phase: .started,
            summary: "Undo last Fix Vault batch"
        )
        let start = ContinuousClock.now
        do {
            let data = try await runCLI(Self.undoFixVaultArgs(force: force), cwd: vault.rootURL)
            let result = try JSONDecoder().decode(FixUndoResult.self, from: data)
            let elapsedMs = elapsedMilliseconds(since: start)
            suppressExternalReindex(
                paths: Array(Set(priorWrittenPaths + result.paths)),
                duration: 5.0,
                clampExisting: true
            )
            lastFixWrittenPaths = []
            actionTracker.record(
                category: .lint,
                action: "lint.undo_fix_vault",
                phase: .succeeded,
                durationMs: elapsedMs,
                summary: "Restored \(result.filesRestored) file(s)\(result.schemaRestored ? " and schema" : "")",
                metadata: [
                    "files_restored": String(result.filesRestored),
                    "schema_restored": result.schemaRestored ? "true" : "false",
                ]
            )
            refreshFiles()
            await runLint()
            return result
        } catch {
            suppressExternalReindex(paths: priorWrittenPaths, duration: 5.0, clampExisting: true)
            let elapsedMs = elapsedMilliseconds(since: start)
            let rawMsg = (error as? CLIError)?.errorDescription ?? error.localizedDescription
            let safeMsg = Self.boundedRedacted(rawMsg)
            errorLogger?.log("Undo Last Fix failed: \(safeMsg)")
            actionTracker.record(
                category: .lint,
                action: "lint.undo_fix_vault",
                phase: .failed,
                durationMs: elapsedMs,
                summary: "Undo Last Fix failed",
                detail: rawMsg
            )
            throw error
        }
    }

    func loadMCPSetup() async {
        guard let vault else { return }
        do {
            let data = try await runCLI(["mcp-setup"], cwd: vault.rootURL)
            mcpSetupText = String(data: data, encoding: .utf8) ?? ""
        } catch {
            let safeErr = Self.boundedRedacted((error as? CLIError)?.errorDescription ?? error.localizedDescription)
            mcpSetupText = "Failed to load MCP setup: \(safeErr)"
        }
    }

    // MARK: - Incremental Re-Embed

    nonisolated static func canonicalWatcherPath(_ path: String) -> String {
        URL(fileURLWithPath: path).resolvingSymlinksInPath().standardizedFileURL.path
    }

    private func resolvedWatcherPath(for path: String) -> String {
        if path.hasPrefix("/") {
            return Self.canonicalWatcherPath(path)
        }
        if let root = vault?.rootURL {
            return Self.canonicalWatcherPath(root.appendingPathComponent(path).path)
        }
        return Self.canonicalWatcherPath(path)
    }

    /// Suppress FSEventsWatcher reindexing for app-initiated writes (`fix`,
    /// `polish`, `meta`, `repair-links`, etc.) that already updated `index.db`.
    /// Evicts any matching entries from `pendingExternalPaths` while keeping
    /// non-suppressed paths untouched. When `clampExisting` is true, replaces
    /// any longer pre-write lease with `now + duration`.
    func suppressExternalReindex(
        paths: [String],
        duration: TimeInterval = 5.0,
        now: Date = Date(),
        clampExisting: Bool = false
    ) {
        guard !paths.isEmpty else { return }
        let expiry = now.addingTimeInterval(duration)
        var suppressedCanon = Set<String>()
        for rawPath in paths where !rawPath.isEmpty {
            let canon = resolvedWatcherPath(for: rawPath)
            if clampExisting {
                suppressedExternalPaths[canon] = expiry
            } else {
                let existing = suppressedExternalPaths[canon] ?? .distantPast
                suppressedExternalPaths[canon] = max(existing, expiry)
            }
            suppressedCanon.insert(canon)
        }
        if !suppressedCanon.isEmpty && !pendingExternalPaths.isEmpty {
            pendingExternalPaths = pendingExternalPaths.filter {
                !suppressedCanon.contains(Self.canonicalWatcherPath($0))
            }
            if pendingExternalPaths.isEmpty {
                externalReindexTask?.cancel()
                externalReindexTask = nil
            }
        }
    }

    nonisolated static func shouldExcludeExternalIndexPath(_ path: String, vault: VaultManager) -> Bool {
        let rel = vault.relativePath(for: URL(fileURLWithPath: path))
            .replacingOccurrences(of: "\\", with: "/")
        let segments = rel.split(separator: "/").map(String.init)
        if segments.contains(where: { $0.hasPrefix(".") || $0 == "node_modules" }) {
            return true
        }
        let excludedFolders = vault.obsidianTemplateFolders()
        if VaultManager.isExcludedFolderPath(rel, excludedFolders: excludedFolders) {
            return true
        }
        if let text = try? String(contentsOfFile: path, encoding: .utf8),
           VaultManager.hasTemplatePlaceholders(text) {
            return true
        }
        return false
    }

    /// Queue a note changed outside the app (saved in Obsidian, synced, git)
    /// for incremental indexing. Collects the path and (re)arms a single debounce
    /// timer; when it fires, `drainExternalReindex` processes every queued path
    /// in one serial pass, skipping deletes (the next full Sync purges them).
    func scheduleExternalReindex(path: String, now: Date = Date()) {
        guard let vault else { return }
        if Self.shouldExcludeExternalIndexPath(path, vault: vault) {
            return
        }
        suppressedExternalPaths = suppressedExternalPaths.filter { $0.value > now }
        let canon = Self.canonicalWatcherPath(path)
        if let expiry = suppressedExternalPaths[canon], expiry > now {
            return
        }
        // A delete or rename-away leaves no file; `index --doc` would just error
        // on the missing path. Leave it for the next full Sync to purge.
        guard FileManager.default.fileExists(atPath: path) else { return }

        pendingExternalPaths.insert(path)
        guard !pauseAutoSync else { return }
        externalReindexTask?.cancel()
        externalReindexTask = Task { @MainActor [weak self] in
            try? await Task.sleep(for: self?.externalReindexDebounce ?? .seconds(1.5))
            guard let self, !Task.isCancelled else { return }
            await self.drainExternalReindex()
        }
    }

    /// Drain every queued external change in ONE serial pass. Single-flighted
    /// (`externalReindexInFlight`) so a debounce that fires mid-drain doesn't
    /// start a second, concurrent drain — its newly-queued paths are picked up
    /// by this loop instead. A burst above `bulkExternalReindexThreshold`
    /// (Obsidian sync, git pull, folder paste) collapses to a single throttled
    /// `2nb index` rather than spawning one subprocess per file.
    private func drainExternalReindex() async {
        guard !pauseAutoSync, !externalReindexInFlight, let vault else { return }
        // A full index (manual Sync or the startup catch-up) is already running
        // and will pick up every current file, so don't start a second, racing
        // `2nb index` on the same SQLite DB. Drop the queued paths — the running
        // full scan covers them; anything written after it passes is re-caught by
        // the watcher once isIndexing clears.
        guard !isIndexing else { pendingExternalPaths.removeAll(); return }
        externalReindexInFlight = true
        defer { externalReindexInFlight = false }

        while !pendingExternalPaths.isEmpty {
            let paths = pendingExternalPaths
            pendingExternalPaths.removeAll()

            if paths.count > bulkExternalReindexThreshold {
                // Bulk change (Obsidian sync, git pull, folder paste): hand off
                // to one full, serialized `2nb index` via startIndex() — it sets
                // isIndexing (so a manual Sync can't run a second, racing index),
                // throttles embeds in a single process, and refreshes status. It
                // covers the whole burst, so stop draining; the watcher re-catches
                // anything written after the full scan passes it.
                actionTracker.record(
                    category: .index,
                    action: "index.external_burst",
                    phase: .progress,
                    summary: "Coalesced \(paths.count) external change(s) into full sync",
                    metadata: ["count": String(paths.count)]
                )
                startIndex()
                break
            }

            // Sequential: never two `index --doc` writing the DB at once. The
            // do/catch is PER PATH: one note that will not parse used to abort
            // the rest of the batch, so a single broken file could leave a
            // whole sync's worth of edits unindexed.
            for path in paths.sorted() {
                guard FileManager.default.fileExists(atPath: path) else { continue }
                if Self.shouldExcludeExternalIndexPath(path, vault: vault) { continue }
                let rel = vault.relativePath(for: URL(fileURLWithPath: path))
                let noteStart = ContinuousClock.now
                do {
                    _ = try await runCLI(["index", "--doc", rel, "--json", "--porcelain"], cwd: vault.rootURL)
                    clearIndexingProblem(for: rel)
                    actionTracker.record(
                        category: .index,
                        action: "index.external_file",
                        phase: .succeeded,
                        durationMs: elapsedMilliseconds(since: noteStart),
                        summary: "Reindexed external edit: \(rel)",
                        metadata: ["path": rel]
                    )
                } catch {
                    noteIndexingProblem(path: rel, message: error.localizedDescription)
                    actionTracker.record(
                        category: .index,
                        action: "index.external_file",
                        phase: .failed,
                        durationMs: elapsedMilliseconds(since: noteStart),
                        summary: "External reindex failed: \(rel)",
                        detail: error.localizedDescription,
                        metadata: ["path": rel]
                    )
                }
            }
            // Reflect the new/updated embeddings in the Index card's count.
            // Inside the loop so a path queued during this pass is still
            // drained before we clear the in-flight flag.
            await refreshAIStatus()
        }
    }

    /// Records a per-note reindex failure. Logged at warning, not debug: a note
    /// dropping out of the index is a user-visible outcome, and the previous
    /// debug line meant nobody ever saw it.
    func noteIndexingProblem(path: String, message: String) {
        let trimmed = Self.boundedRedacted(message.trimmingCharacters(in: .whitespacesAndNewlines))
        log.warning("index --doc failed for \(path, privacy: .public): \(trimmed, privacy: .public)")
        lastIndexingProblem = IndexingProblem(path: path, message: trimmed)
        actionTracker.updateStatus {
            $0.lastIndexingProblemPath = path
            $0.lastIndexingProblemMessage = trimmed
        }
    }

    /// Clears the problem when THAT path indexes cleanly. Scoped to the path so
    /// a different note succeeding cannot hide a failure the user has not fixed.
    func clearIndexingProblem(for path: String) {
        if lastIndexingProblem?.path == path {
            lastIndexingProblem = nil
            actionTracker.updateStatus {
                $0.lastIndexingProblemPath = nil
                $0.lastIndexingProblemMessage = nil
            }
        }
    }

    /// One full-index envelope, decoded. `index --json` guarantees every key is
    /// present and both lists are emitted empty rather than omitted, so a decode
    /// failure means the output was not the envelope at all (an older CLI on
    /// PATH, say), never "nothing to report". Optional fields with defaults so a
    /// CLI predating `unreadable` still decodes.
    struct FullIndexSummary: Decodable {
        struct SkippedNote: Decodable, Equatable {
            let path: String
            let error: String
        }
        var docsIndexed: Int = 0
        var chunksCreated: Int = 0
        var linksFound: Int = 0
        var unparseable: [SkippedNote] = []
        var unreadable: [SkippedNote] = []

        enum CodingKeys: String, CodingKey {
            case docsIndexed = "docs_indexed"
            case chunksCreated = "chunks_created"
            case linksFound = "links_found"
            case unparseable, unreadable
        }
    }

    /// Decodes an `index --json` envelope, or nil when stdout is not one.
    static func parseFullIndexSummary(_ stdout: String) -> FullIndexSummary? {
        guard let data = stdout.data(using: .utf8) else { return nil }
        return try? JSONDecoder().decode(FullIndexSummary.self, from: data)
    }

    /// Extracts an actionable single-line failure reason from multi-line `2nb index` stderr.
    nonisolated static func extractIndexFailureMessage(stderr: String, exitCode: Int32) -> String {
        let trimmed = stderr.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty else {
            return "CLI exited with code \(exitCode)"
        }
        let lines = trimmed
            .split(separator: "\n", omittingEmptySubsequences: true)
            .map { $0.trimmingCharacters(in: .whitespaces) }
            .filter { !$0.isEmpty }
        if let errorIdx = lines.lastIndex(where: { $0.hasPrefix("Error: ") }) {
            let joined = lines[errorIdx...].joined(separator: " ")
            let body = String(joined.dropFirst("Error: ".count)).trimmingCharacters(in: .whitespaces)
            if !body.isEmpty {
                return body
            }
        }
        if let embedErr = lines.last(where: { $0.hasPrefix("embed error ") }) {
            return embedErr
        }
        return lines.last ?? "CLI exited with code \(exitCode)"
    }

    /// Decides what a FULL index that exited cleanly should say about skipped
    /// notes. Exit 0 stopped meaning "nothing was wrong" the moment an
    /// unparseable note became non-fatal AND had its index row dropped: a clean
    /// exit can now hide a note that just vanished from search, and this banner
    /// is the app's only surface naming it. So a successful run REPORTS the
    /// notes it skipped and clears only when it skipped none.
    ///
    /// A nil summary means stdout was not the envelope, which is no evidence
    /// either way, so the existing report is kept rather than dismissed: going
    /// quiet about a broken note is the failure this exists to prevent, and a
    /// stale banner is the cheaper mistake.
    ///
    /// The path-scoped `clearIndexingProblem(for:)` still belongs to the
    /// single-note watcher; the vault-switch reset lives in `openVault(at:)`.
    func applyFullIndexOutcome(_ summary: FullIndexSummary?) {
        guard let summary else {
            log.warning("Index rebuild exited 0 but its --json envelope did not decode; keeping any existing indexing problem")
            return
        }
        let skipped = summary.unparseable + summary.unreadable
        guard let first = skipped.first else {
            lastIndexingProblem = nil
            return
        }
        // The lists are separate because the remedies differ, and the message
        // says which one this is: an unparseable note has LOST its index entry
        // and is gone from search until it is fixed, while an unreadable one
        // keeps whatever it had.
        var message = summary.unparseable.isEmpty
            ? "could not be read, so its existing index entry was kept: \(first.error)"
            : "\(first.error)"
        if skipped.count > 1 {
            message += " (and \(skipped.count - 1) more)"
        }
        let safeMessage = Self.boundedRedacted(message)
        log.warning("Index rebuild skipped \(skipped.count, privacy: .public) note(s); reporting \(first.path, privacy: .public)")
        lastIndexingProblem = IndexingProblem(path: first.path, message: safeMessage)
    }

    // MARK: - Git Integration

    func refreshGitStatus() async {
        guard let vault else {
            vaultIsGitRepo = false
            gitFileStatus = [:]
            return
        }
        do {
            let data = try await runCLI(
                ["git", "status", "--json", "--porcelain"],
                cwd: vault.rootURL
            )
            if data.isEmpty {
                gitFileStatus = [:]
                vaultIsGitRepo = false
                return
            }
            // When not a git repo, the CLI emits {"git_repo": false}
            if let envelope = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
               let isRepo = envelope["git_repo"] as? Bool, !isRepo {
                vaultIsGitRepo = false
                gitFileStatus = [:]
                return
            }
            let decoded = try JSONDecoder().decode([String: String].self, from: data)
            vaultIsGitRepo = true
            gitFileStatus = decoded
        } catch {
            log.warning("git status unavailable: \(error.localizedDescription)")
            vaultIsGitRepo = false
            gitFileStatus = [:]
        }
    }

    func setGitActivityDays(_ days: Int) {
        gitActivityDays = days
        UserDefaults.standard.set(days, forKey: "gitActivityDays")
        Task { await refreshGitActivity() }
    }

    func refreshGitActivity() async {
        guard let vault else { return }
        gitActivityLoading = true
        defer { gitActivityLoading = false }
        do {
            let data = try await runCLI(
                ["git", "activity", "--since", "\(gitActivityDays)d", "--json", "--porcelain"],
                cwd: vault.rootURL
            )
            if data.isEmpty {
                gitActivity = []
                return
            }
            // When not a git repo, CLI emits {"git_repo": false}
            if let envelope = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
               envelope["git_repo"] as? Bool == false {
                vaultIsGitRepo = false
                gitActivity = []
                return
            }
            vaultIsGitRepo = true
            gitActivity = (try JSONDecoder().decode([GitChangeInfo]?.self, from: data)) ?? []
        } catch {
            log.warning("git activity unavailable: \(error.localizedDescription)")
            gitActivity = []
        }
    }

    func openGitActivity() {
        showGitActivity = true
    }

    /// Opens the commit detail modal for `hash`. Loads the commit lazily
    /// via `2nb git show --json <hash>`. The modal shows a progress
    /// indicator while loading; on failure it shows commitDetailError.
    ///
    /// Any in-flight commit-detail task is cancelled before the new one
    /// starts, so rapid clicks on different commits (or close+reopen of
    /// the same commit) never race. The cancelled task drops its result
    /// via Task.isCancelled after the runCLI await resumes.
    func openCommitDetail(_ hash: String) {
        guard let vault else { return }
        commitDetailTask?.cancel()
        commitDetail = nil
        commitDetailError = nil
        showCommitDetail = true
        log.info("openCommitDetail: hash=\(hash)")

        commitDetailTask = Task { @MainActor [weak self] in
            guard let self else { return }
            do {
                let data = try await self.runCLI(
                    ["git", "show", hash, "--json", "--porcelain"],
                    cwd: vault.rootURL
                )
                if Task.isCancelled {
                    log.debug("commitDetail: dropping stale result for \(hash)")
                    return
                }
                let detail = try JSONDecoder().decode(CommitDetail.self, from: data)
                if Task.isCancelled { return }
                self.commitDetail = detail
                log.info("commitDetail: loaded \(detail.files.count) files, +\(detail.stats.insertions)/-\(detail.stats.deletions)")
            } catch {
                if Task.isCancelled {
                    log.debug("commitDetail: dropping stale error for \(hash)")
                    return
                }
                let rawMsg = (error as? CLIError)?.errorDescription ?? error.localizedDescription
                let msg = Self.boundedRedacted(rawMsg)
                self.commitDetailError = msg
                log.warning("commitDetail load failed: \(msg)")
            }
        }
    }

    /// Dismisses the commit detail modal and cancels any in-flight
    /// git-show request. Called from the sheet's isPresented setter so
    /// every dismiss path (Done button, Escape, outside-click) routes
    /// through here. Safe to call when the modal is already closed.
    func closeCommitDetail() {
        commitDetailTask?.cancel()
        commitDetailTask = nil
        showCommitDetail = false
    }

    // MARK: - MCP Observability

    func refreshMCPStatus() async {
        // Skip the tick when the previous refresh is still running; see
        // mcpStatusRefreshInFlight.
        guard !mcpStatusRefreshInFlight else { return }
        mcpStatusRefreshInFlight = true
        defer { mcpStatusRefreshInFlight = false }
        guard let vault else {
            mcpStatuses = []
            return
        }
        do {
            let data = try await runCLI(["mcp", "status", "--json"], cwd: vault.rootURL)
            // Empty stdout means "no servers" — not an error, just an empty array.
            if data.isEmpty {
                mcpStatuses = []
                return
            }
            let statuses = try JSONDecoder().decode([MCPServerStatusInfo]?.self, from: data) ?? []
            mcpStatuses = statuses
        } catch {
            log.warning("MCP status unavailable: \(error.localizedDescription)")
            mcpStatuses = []
        }
    }

    func startMCPStatusTimer() {
        mcpStatusTimer?.invalidate()
        mcpStatusTimer = Timer.scheduledTimer(withTimeInterval: 5, repeats: true) { [weak self] _ in
            Task { @MainActor in
                await self?.refreshMCPStatus()
            }
        }
        // Fire once immediately so the status bar doesn't lag for 5s after vault open
        Task { @MainActor in
            await refreshMCPStatus()
        }
    }

    // MARK: - AI Setup Wizard

    func saveAIConfig(provider: String, embedModel: String, genModel: String,
                      dims: Int, bedrockProfile: String = "", bedrockRegion: String = "",
                      openrouterKey: String = "",
                      geminiProject: String = "", geminiLocation: String = "",
                      geminiKey: String = "") async throws {
        guard let vault else { throw CLIError.noVault }
        log.info("Saving AI config: provider=\(provider) embed=\(embedModel) gen=\(genModel)")
        let cwd = vault.rootURL
        _ = try await trackAsyncAction(
            category: .settings,
            action: "settings.save_ai",
            startSummary: "Save AI config (\(provider): \(genModel) / \(embedModel))",
            metadata: [
                "provider": provider,
                "embedModel": embedModel,
                "genModel": genModel,
                "dims": String(dims),
                "hasGeminiKey": geminiKey.isEmpty ? "false" : "true",
                "hasProject": geminiProject.isEmpty ? "false" : "true",
            ]
        ) {
            // For Gemini, store API key if provided (Keychain and CLI keystore)
            if (provider == "gemini" || provider == "google") && !geminiKey.isEmpty {
                let sec = Process()
                sec.executableURL = URL(fileURLWithPath: "/usr/bin/security")
                sec.arguments = ["add-generic-password", "-s", "dev.apresai.2ndbrain",
                                 "-a", "gemini", "-w", geminiKey, "-U"]
                sec.standardOutput = FileHandle.nullDevice
                sec.standardError = FileHandle.nullDevice
                try? sec.run()
                sec.waitUntilExit()

                _ = try? await runCLIGlobal(["config", "set-key", "gemini"], stdin: Self.setKeyStdinData(geminiKey))
            }

            // For OpenRouter, store API key in Keychain first
            if provider == "openrouter" && !openrouterKey.isEmpty {
                let sec = Process()
                sec.executableURL = URL(fileURLWithPath: "/usr/bin/security")
                sec.arguments = ["add-generic-password", "-s", "dev.apresai.2ndbrain",
                                 "-a", "openrouter", "-w", openrouterKey, "-U"]
                sec.standardOutput = FileHandle.nullDevice
                sec.standardError = FileHandle.nullDevice
                try sec.run()
                sec.waitUntilExit()
            }

            // Write config values sequentially
            _ = try await runCLI(["config", "set", "ai.provider", provider], cwd: cwd)
            _ = try await runCLI(["config", "set", "ai.embedding_model", embedModel], cwd: cwd)
            _ = try await runCLI(["config", "set", "ai.generation_model", genModel], cwd: cwd)
            _ = try await runCLI(["config", "set", "ai.dimensions", String(dims)], cwd: cwd)

            if provider == "gemini" || provider == "google" {
                if !geminiProject.isEmpty {
                    _ = try await runCLI(["config", "set", "ai.gemini.project", geminiProject], cwd: cwd)
                }
                if !geminiLocation.isEmpty {
                    _ = try await runCLI(["config", "set", "ai.gemini.location", geminiLocation], cwd: cwd)
                }
            }

            if provider == "bedrock" {
                if !bedrockProfile.isEmpty {
                    _ = try await runCLI(["config", "set", "ai.bedrock.profile", bedrockProfile], cwd: cwd)
                }
                if !bedrockRegion.isEmpty {
                    _ = try await runCLI(["config", "set", "ai.bedrock.region", bedrockRegion], cwd: cwd)
                }
            }
        }
    }

    nonisolated static func boundedRedacted(_ raw: String) -> String {
        GUIActionTracker.boundedRedactedTail(raw, limit: GUIActionTracker.maxStatusErrorChars)
    }

    func testModel(provider: String, modelID: String, modelType: String) async throws -> AIProbeResult {
        return try await trackAsyncAction(
            category: .settings,
            action: "settings.test_model",
            startSummary: "Probe \(modelType) model \(modelID) (\(provider))",
            metadata: ["provider": provider, "model": modelID, "type": modelType],
            successDetail: { result in
                (
                    result.ok
                        ? "Probe passed: \(modelID) (\(result.latency))"
                        : "Probe failed: \(modelID) (\(result.errorCode ?? "error"))",
                    [
                        "provider": provider,
                        "model": modelID,
                        "type": modelType,
                        "ok": result.ok ? "true" : "false",
                        "latency": result.latency,
                        "detail": result.detail ?? "",
                    ]
                )
            }
        ) {
            let args = ["models", "test", modelID, "--provider", provider, "--type", modelType, "--json", "--porcelain"]
            let data: Data
            if let vault {
                data = try await runCLIAllowingNonZero(args, cwd: vault.rootURL)
            } else {
                let raw = try await runCLIGlobalRaw(args, expectNonZeroExit: true)
                if raw.stdout.isEmpty && raw.status != 0 && !raw.stderrText.isEmpty {
                    throw CLIError.nonZeroExit(raw.status, message: raw.stderrText)
                }
                data = raw.stdout
            }
            let result = try JSONDecoder().decode(AIProbeResult.self, from: data)
            let boundedDetail = result.detail.map { Self.boundedRedacted($0) }
            let resolvedModel = result.modelID.isEmpty ? modelID : result.modelID
            let resolvedProvider = result.provider.isEmpty ? provider : result.provider
            actionTracker.updateStatus { status in
                if modelType == "generation" {
                    status.lastGenProbeModel = resolvedModel
                    status.lastGenProbeProvider = resolvedProvider
                    status.lastGenProbeOK = result.ok
                    status.lastGenProbeDetail = boundedDetail
                    status.lastGenProbeLatency = result.latency
                } else if modelType == "embedding" {
                    status.lastEmbedProbeModel = resolvedModel
                    status.lastEmbedProbeProvider = resolvedProvider
                    status.lastEmbedProbeOK = result.ok
                    status.lastEmbedProbeDetail = boundedDetail
                    status.lastEmbedProbeLatency = result.latency
                }
            }
            if vault != nil {
                Task { @MainActor [weak self] in await self?.refreshMetrics() }
            }
            return result
        }
    }

    func setConfigValue(_ key: String, _ value: String) async throws {
        guard let vault else { throw CLIError.noVault }
        let lowerKey = key.lowercased()
        let isSensitive = lowerKey.contains("key") || lowerKey.contains("token") || lowerKey.contains("secret")
        let safeMetadata: [String: String] = [
            "key": key,
            "value": isSensitive ? "<REDACTED>" : value,
        ]
        _ = try await trackAsyncAction(
            category: .settings,
            action: "settings.set_config",
            startSummary: isSensitive ? "Set config \(key)" : "Set config \(key)=\(value)",
            metadata: safeMetadata
        ) {
            try await runCLI(["config", "set", key, value, "--porcelain"], cwd: vault.rootURL)
        }
    }

    /// Reads one raw config value (`2nb config get --porcelain`), trimmed.
    func getConfigValue(_ key: String) async throws -> String {
        guard let vault else { throw CLIError.noVault }
        let data = try await runCLI(["config", "get", key, "--porcelain"], cwd: vault.rootURL)
        return String(decoding: data, as: UTF8.self).trimmingCharacters(in: .whitespacesAndNewlines)
    }

    nonisolated static func doctorSelfTestArgs(vaultRoot: URL?) -> [String] {
        vaultRoot.map { CLIPath.args(["doctor", "--json", "--porcelain"], vault: $0) }
            ?? ["doctor", "--json", "--porcelain"]
    }

    nonisolated static func resolvedSlotProvider(
        from report: SelfTestReport,
        slot: ModelSlotTestResult.Slot,
        fallback: String?
    ) -> String {
        if let providerCheck = report.checks.first(where: { $0.name == "provider known" && $0.ok }) {
            let detail = providerCheck.detail.trimmingCharacters(in: .whitespacesAndNewlines)
            if detail.hasPrefix("embed: "), let genRange = detail.range(of: ", generation: ") {
                let embedPart = String(detail["embed: ".endIndex..<genRange.lowerBound])
                    .trimmingCharacters(in: .whitespaces)
                let genPart = String(detail[genRange.upperBound...])
                    .trimmingCharacters(in: .whitespaces)
                let picked = slot == .embedding ? embedPart : genPart
                if !picked.isEmpty { return picked }
            } else if !detail.isEmpty && !detail.contains(",") {
                return detail
            }
        }
        if let fallback, !fallback.isEmpty {
            return fallback
        }
        return report.provider
    }

    func runDoctorSelfTest(extraEnvironment: [String: String]? = nil) async throws -> SelfTestReport {
        try await trackAsyncAction(
            category: .settings,
            action: "settings.doctor_selftest",
            startSummary: "Run doctor credential self-test",
            successDetail: { report in
                let modelsOK = SelfTestPresentation.modelsTestPassed(report)
                return (
                    "AI model test \(modelsOK ? "passed" : "failed") (\(report.credentials))",
                    [
                        "ok": modelsOK ? "true" : "false",
                        "doctor_ok": report.ok ? "true" : "false",
                        "provider": report.provider,
                        "credentials": report.credentials,
                    ]
                )
            }
        ) {
            let result = try await runCLIGlobalRaw(
                Self.doctorSelfTestArgs(vaultRoot: vault?.rootURL),
                expectNonZeroExit: true,
                extraEnvironment: extraEnvironment
            )
            guard let report = try? JSONDecoder().decode(DoctorReport.self, from: result.stdout),
                  let selftest = report.selftest else {
                let detail = result.stderrText.isEmpty
                    ? "the installed 2nb may predate `doctor --json`'s selftest block; run `brew upgrade apresai/tap/twonb`"
                    : result.stderrText
                throw CLIError.nonZeroExit(result.status, message: detail)
            }
            let modelsOK = SelfTestPresentation.modelsTestPassed(selftest)
            let genProvider = extraEnvironment == nil
                ? Self.resolvedSlotProvider(from: selftest, slot: .generation, fallback: aiStatus?.effectiveGenerationProvider)
                : selftest.provider
            let embedProvider = extraEnvironment == nil
                ? Self.resolvedSlotProvider(from: selftest, slot: .embedding, fallback: aiStatus?.effectiveEmbeddingProvider)
                : selftest.provider
            let summary = Self.boundedRedacted(
                SelfTestPresentation.modelsHeadline(selftest, genProvider: genProvider, embedProvider: embedProvider)
            )
            actionTracker.updateStatus { status in
                status.lastModelsTestOK = modelsOK
                status.lastDoctorOK = modelsOK
                status.lastDoctorCredentials = selftest.credentials
                status.lastDoctorSummary = summary
                if extraEnvironment == nil {
                    let slots = SelfTestPresentation.extractSlotResults(
                        selftest,
                        genProvider: genProvider,
                        embedProvider: embedProvider
                    )
                    if let genSlot = slots.first(where: { $0.slot == .generation }) {
                        if !genSlot.modelID.isEmpty {
                            status.lastGenProbeModel = genSlot.modelID
                        }
                        if !genSlot.provider.isEmpty {
                            status.lastGenProbeProvider = genSlot.provider
                        }
                        status.lastGenProbeOK = genSlot.ok
                        let rawDetail = selftest.checks.first(where: { $0.name == "answer model" || $0.name.hasPrefix("answer model (") })?.detail ?? ""
                        status.lastGenProbeDetail = Self.boundedRedacted(genSlot.responseProof.isEmpty ? rawDetail : genSlot.responseProof)
                        status.lastGenProbeLatency = genSlot.latency
                    }
                    if let embedSlot = slots.first(where: { $0.slot == .embedding }) {
                        if !embedSlot.modelID.isEmpty {
                            status.lastEmbedProbeModel = embedSlot.modelID
                        }
                        if !embedSlot.provider.isEmpty {
                            status.lastEmbedProbeProvider = embedSlot.provider
                        }
                        status.lastEmbedProbeOK = embedSlot.ok
                        let rawDetail = selftest.checks.first(where: { $0.name == "search model" || $0.name.hasPrefix("search model (") })?.detail ?? ""
                        status.lastEmbedProbeDetail = Self.boundedRedacted(embedSlot.responseProof.isEmpty ? rawDetail : embedSlot.responseProof)
                        status.lastEmbedProbeLatency = embedSlot.latency
                    }
                }
            }
            if vault != nil {
                Task { @MainActor [weak self] in await self?.refreshMetrics() }
            }
            return selftest
        }
    }

    nonisolated static func credentialProbeEnvironment(
        provider: String = "gemini",
        apiKey: String? = nil,
        project: String? = nil,
        location: String? = nil
    ) -> [String: String] {
        var env: [String: String] = [:]
        if !provider.isEmpty {
            env["2NB_DOCTOR_PROBE_PROVIDER"] = (provider == "google" ? "gemini" : provider)
        }
        if provider == "gemini" || provider == "google" {
            if let apiKey, !apiKey.isEmpty {
                env["GEMINI_API_KEY"] = apiKey
                env["2NB_DOCTOR_PROBE_GEMINI_MODE"] = "apikey"
            } else if let project, !project.isEmpty {
                env["GOOGLE_CLOUD_PROJECT"] = project
                env["2NB_DOCTOR_PROBE_GEMINI_MODE"] = "vertex"
                env["2NB_DOCTOR_PROBE_GCP_PROJECT"] = project
                if let location, !location.isEmpty {
                    env["GOOGLE_CLOUD_LOCATION"] = location
                    env["2NB_DOCTOR_PROBE_GCP_LOCATION"] = location
                }
            }
        } else if provider == "bedrock" {
            if let apiKey, !apiKey.isEmpty {
                env["AWS_BEARER_TOKEN_BEDROCK"] = apiKey
                env["2NB_BEDROCK_IGNORE_PREFER_STORED"] = "1"
            }
        }
        return env
    }

    /// Runs the self-test against candidate credentials without permanently storing them.
    /// Supports Gemini (GEMINI_API_KEY / Google ADC / GCP project) and Bedrock.
    func probeCredentials(provider: String = "gemini", apiKey: String? = nil, project: String? = nil, location: String? = nil) async throws -> SelfTestReport {
        let env = Self.credentialProbeEnvironment(
            provider: provider,
            apiKey: apiKey,
            project: project,
            location: location
        )
        return try await runDoctorSelfTest(extraEnvironment: env.isEmpty ? nil : env)
    }

    /// Read stored Gemini API key from macOS Keychain if available.
    func getStoredGeminiKey() -> String? {
        let sec = Process()
        sec.executableURL = URL(fileURLWithPath: "/usr/bin/security")
        sec.arguments = ["find-generic-password", "-s", "dev.apresai.2ndbrain", "-a", "gemini", "-w"]
        let pipe = Pipe()
        sec.standardOutput = pipe
        sec.standardError = FileHandle.nullDevice
        do {
            try sec.run()
            sec.waitUntilExit()
            if sec.terminationStatus == 0 {
                let data = pipe.fileHandleForReading.readDataToEndOfFile()
                let str = String(data: data, encoding: .utf8)?.trimmingCharacters(in: .whitespacesAndNewlines)
                if let str, !str.isEmpty { return str }
            }
        } catch {}
        return nil
    }

    static func setKeyStdinData(_ key: String) -> Data {
        Data((key.trimmingCharacters(in: .whitespacesAndNewlines) + "\n").utf8)
    }

    /// Save Gemini API key to macOS Keychain and 2nb keystore.
    func saveGeminiKey(_ key: String) async throws {
        let trimmed = key.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty else { return }
        _ = try await trackAsyncAction(
            category: .settings,
            action: "settings.save_gemini_key",
            startSummary: "Save Gemini API key to Keychain & CLI keystore"
        ) {
            let sec = Process()
            sec.executableURL = URL(fileURLWithPath: "/usr/bin/security")
            sec.arguments = ["add-generic-password", "-s", "dev.apresai.2ndbrain", "-a", "gemini", "-w", trimmed, "-U"]
            sec.standardOutput = FileHandle.nullDevice
            sec.standardError = FileHandle.nullDevice
            try? sec.run()
            sec.waitUntilExit()

            return try await runCLIGlobal(["config", "set-key", "gemini"], stdin: Self.setKeyStdinData(trimmed))
        }
    }

    /// Clear Gemini API key from macOS Keychain.
    func clearGeminiKey() async throws {
        actionTracker.record(
            category: .settings,
            action: "settings.clear_gemini_key",
            phase: .started,
            summary: "Clear Gemini API key from Keychain"
        )
        let start = ContinuousClock.now
        let sec = Process()
        sec.executableURL = URL(fileURLWithPath: "/usr/bin/security")
        sec.arguments = ["delete-generic-password", "-s", "dev.apresai.2ndbrain", "-a", "gemini"]
        sec.standardOutput = FileHandle.nullDevice
        sec.standardError = FileHandle.nullDevice
        try? sec.run()
        sec.waitUntilExit()
        actionTracker.record(
            category: .settings,
            action: "settings.clear_gemini_key",
            phase: .succeeded,
            durationMs: elapsedMilliseconds(since: start),
            summary: "Cleared Gemini API key from Keychain"
        )
    }

    /// The full effective configuration (`2nb config show --json`), returned
    /// raw for the generic read-only viewer (no schema coupling in Swift).
    func fetchConfigShow() async throws -> Data {
        guard let vault else { throw CLIError.noVault }
        return try await runCLI(["config", "show", "--json", "--porcelain"], cwd: vault.rootURL)
    }

    func setGenerationSlot(provider: String, modelID: String) async throws {
        guard let vault else { throw CLIError.noVault }
        if isIndexing { throw CLIError.indexRebuildInProgress }

        _ = try await trackAsyncAction(
            category: .settings,
            action: "settings.set_generation_slot",
            startSummary: "Set generation slot to \(modelID) (\(provider))",
            metadata: ["type": "generation", "model": modelID, "provider": provider]
        ) {
            let embedProvData = try? await runCLI(["config", "get", "ai.embedding_provider"], cwd: vault.rootURL)
            let currentEmbedProvider = embedProvData.flatMap {
                String(data: $0, encoding: .utf8)?.trimmingCharacters(in: .whitespacesAndNewlines)
            } ?? ""

            _ = try await runCLI(["config", "set", "ai.generation_provider", provider], cwd: vault.rootURL)
            _ = try await runCLI(["config", "set", "ai.generation_model", modelID], cwd: vault.rootURL)
            if currentEmbedProvider == provider {
                _ = try await runCLI(["config", "set", "ai.provider", provider], cwd: vault.rootURL)
            }
            await refreshAIStatus()
        }
    }

    func setEmbeddingSlot(provider: String, modelID: String, dimensions: Int? = nil) async throws {
        guard let vault else { throw CLIError.noVault }
        if isIndexing { throw CLIError.indexRebuildInProgress }

        var meta: [String: String] = ["type": "embedding", "model": modelID, "provider": provider]
        if let dimensions {
            meta["dimensions"] = String(dimensions)
        }

        _ = try await trackAsyncAction(
            category: .settings,
            action: "settings.set_embedding_slot",
            startSummary: "Set embedding slot to \(modelID) (\(provider))",
            metadata: meta
        ) {
            let genProvData = try? await runCLI(["config", "get", "ai.generation_provider"], cwd: vault.rootURL)
            let currentGenProvider = genProvData.flatMap {
                String(data: $0, encoding: .utf8)?.trimmingCharacters(in: .whitespacesAndNewlines)
            } ?? ""

            _ = try await runCLI(["config", "set", "ai.embedding_provider", provider], cwd: vault.rootURL)
            _ = try await runCLI(["config", "set", "ai.embedding_model", modelID], cwd: vault.rootURL)
            if let dimensions, dimensions > 0 {
                _ = try await runCLI(["config", "set", "ai.dimensions", String(dimensions)], cwd: vault.rootURL)
            }
            if currentGenProvider == provider {
                _ = try await runCLI(["config", "set", "ai.provider", provider], cwd: vault.rootURL)
            }
            await refreshAIStatus()
        }
    }

    func setActiveModel(type: String, modelID: String, provider: String) async throws {
        guard let vault else { throw CLIError.noVault }
        if isIndexing { throw CLIError.indexRebuildInProgress }

        // Rerank is a separate slot (ai.rerank.*) resolved from the active
        // provider. Set its model + enable it without touching ai.provider or
        // the embed/gen model, which would disrupt those slots.
        if type == "rerank" {
            _ = try await trackAsyncAction(
                category: .settings,
                action: "settings.set_active_model",
                startSummary: "Set active rerank model to \(modelID) (\(provider))",
                metadata: ["type": type, "model": modelID, "provider": provider]
            ) {
                log.info("AI Hub action: set ai.rerank.model=\(modelID, privacy: .public) and enable rerank")
                _ = try await runCLI(["config", "set", "ai.rerank.model", modelID], cwd: vault.rootURL)
                _ = try await runCLI(["config", "set", "ai.rerank.enabled", "true"], cwd: vault.rootURL)
            }
            return
        }

        switch type {
        case "embedding":
            let dims: Int? = ((provider == "gemini" || provider == "google") && (modelID == "gemini-embedding-2" || modelID == "gemini-embedding-001")) ? 768 : nil
            try await setEmbeddingSlot(provider: provider, modelID: modelID, dimensions: dims)
        case "generation":
            try await setGenerationSlot(provider: provider, modelID: modelID)
        default:
            throw CLIError.noVault
        }
    }

    /// Fetches the active vault AI status snapshot (`2nb ai status --json`).
    func fetchAIStatus() async throws -> AIStatusInfo {
        guard let vault else { throw CLIError.noVault }
        let data = try await runCLI(
            ["ai", "status", "--json", "--porcelain"],
            cwd: vault.rootURL
        )
        return try JSONDecoder().decode(AIStatusInfo.self, from: data)
    }

    // MARK: - Local Gemma & Engine Methods

    /// Downloads the bundled local model weights via `ai engine pull --json`,
    /// streaming JSONL progress into `localModelDownload` for a progress bar.
    func downloadLocalModels(_ ids: [String]) async {
        guard !ids.isEmpty, localModelDownload?.finished != false else { return }
        localModelDownload = LocalModelDownload(models: ids)
        log.info("Local model download started: \(ids.joined(separator: ","), privacy: .public)")

        let start = ContinuousClock.now
        actionTracker.record(
            category: .engine,
            action: "local_model.download",
            phase: .started,
            summary: "Starting download of \(ids.count) local model(s): \(ids.joined(separator: ", "))",
            metadata: ["models": ids.joined(separator: ",")]
        )
        actionTracker.updateStatus { status in
            status.localModelDownloading = true
            status.localModelTotalCount = ids.count
            status.localModelCompletedCount = 0
            status.localModelDownloadError = nil
            status.localModelDownloadFraction = 0.0
        }

        let cliExecutable = resolvedCLIPath()
        do {
            let result: (exitCode: Int32, stderr: String, tailEvents: [PullEvent]) = try await withCheckedThrowingContinuation { continuation in
                let process = Process()
                process.executableURL = URL(fileURLWithPath: cliExecutable)
                let sub = ["ai", "engine", "pull", "--json"] + ids
                process.arguments = vault.map { CLIPath.args(sub, vault: $0.rootURL) } ?? sub
                let stdoutPipe = Pipe()
                let stderrPipe = Pipe()
                process.standardOutput = stdoutPipe
                process.standardError = stderrPipe
                let drain = PipeDrain()
                let stream = PullEventStream()

                stdoutPipe.fileHandleForReading.readabilityHandler = { [weak self] handle in
                    let chunk = handle.availableData
                    if chunk.isEmpty { handle.readabilityHandler = nil; return }
                    let events = stream.ingest(chunk)
                    guard !events.isEmpty else { return }
                    Task { @MainActor [weak self] in
                        for ev in events { self?.applyPullEvent(ev, totalModels: ids.count) }
                    }
                }
                stderrPipe.fileHandleForReading.readabilityHandler = { handle in
                    let d = handle.availableData
                    if d.isEmpty { handle.readabilityHandler = nil } else { drain.appendStderr(d) }
                }
                process.terminationHandler = { proc in
                    stdoutPipe.fileHandleForReading.readabilityHandler = nil
                    stderrPipe.fileHandleForReading.readabilityHandler = nil
                    var tailEvents = stream.ingest(stdoutPipe.fileHandleForReading.readDataToEndOfFile())
                    tailEvents.append(contentsOf: stream.flush())
                    drain.appendStderr(stderrPipe.fileHandleForReading.readDataToEndOfFile())
                    let err = String(data: drain.stderrData, encoding: .utf8) ?? ""
                    continuation.resume(returning: (proc.terminationStatus, err, tailEvents))
                }
                do {
                    try process.run()
                } catch {
                    Self.releaseFailedLaunch(process, pipes: [stdoutPipe, stderrPipe])
                    continuation.resume(throwing: error)
                }
            }

            for ev in result.tailEvents {
                self.applyPullEvent(ev, totalModels: ids.count)
            }

            let elapsedMs = elapsedMilliseconds(since: start)
            localModelDownload?.finished = true
            if result.exitCode == 0 && !(localModelDownload?.failed ?? false) {
                actionTracker.record(
                    category: .engine,
                    action: "local_model.download",
                    phase: .succeeded,
                    durationMs: elapsedMs,
                    summary: "Successfully downloaded \(ids.count) local model(s)",
                    metadata: ["models": ids.joined(separator: ",")]
                )
                actionTracker.updateStatus { status in
                    status.localModelDownloading = false
                    status.localModelDownloadFraction = 1.0
                    status.localModelCompletedCount = ids.count
                    status.localModelDownloadError = nil
                }
            } else {
                localModelDownload?.failed = true
                let rawErr = localModelDownload?.error ?? (result.stderr.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
                    ? "download failed (exit \(result.exitCode))"
                    : result.stderr.trimmingCharacters(in: .whitespacesAndNewlines))
                let err = Self.boundedRedacted(rawErr)
                localModelDownload?.error = err

                actionTracker.record(
                    category: .engine,
                    action: "local_model.download",
                    phase: .failed,
                    durationMs: elapsedMs,
                    summary: "Local model download failed: \(ids.joined(separator: ", "))",
                    detail: rawErr,
                    metadata: ["models": ids.joined(separator: ",")]
                )
                actionTracker.updateStatus { status in
                    status.localModelDownloading = false
                    status.localModelDownloadError = err
                }
            }
            await refreshAIStatus()
        } catch {
            let elapsedMs = elapsedMilliseconds(since: start)
            let safeErr = Self.boundedRedacted(error.localizedDescription)
            localModelDownload?.finished = true
            localModelDownload?.failed = true
            localModelDownload?.error = safeErr

            actionTracker.record(
                category: .engine,
                action: "local_model.download",
                phase: .failed,
                durationMs: elapsedMs,
                summary: "Local model download launch failed",
                detail: error.localizedDescription,
                metadata: ["models": ids.joined(separator: ",")]
            )
            actionTracker.updateStatus { status in
                status.localModelDownloading = false
                status.localModelDownloadError = safeErr
            }
        }
    }

    private func applyPullEvent(_ ev: PullEvent, totalModels: Int) {
        guard localModelDownload != nil else { return }
        let previousModel = localModelDownload?.currentModel ?? ""
        localModelDownload?.currentModel = ev.model

        // Record model progress action on boundary transition
        if !ev.model.isEmpty && ev.model != previousModel {
            actionTracker.record(
                category: .engine,
                action: "local_model.download",
                phase: .progress,
                summary: "Downloading local model: \(ev.model)",
                metadata: [
                    "model": ev.model,
                    "completed": String(localModelDownload?.completed.count ?? 0),
                    "total": String(totalModels)
                ]
            )
        }

        switch ev.status {
        case "progress":
            localModelDownload?.done = ev.done ?? 0
            localModelDownload?.total = ev.total ?? 0
            let fraction = localModelDownload?.fraction ?? 0
            actionTracker.updateStatus { status in
                status.localModelDownloadCurrent = ev.model
                status.localModelDownloadDone = ev.done
                status.localModelDownloadTotal = ev.total
                status.localModelDownloadFraction = fraction
            }
        case "done":
            if !(localModelDownload?.completed.contains(ev.model) ?? false) {
                localModelDownload?.completed.append(ev.model)
            }
            actionTracker.updateStatus { status in
                status.localModelCompletedCount = localModelDownload?.completed.count
            }
        case "error":
            localModelDownload?.failed = true
            localModelDownload?.error = ev.error.map { Self.boundedRedacted($0) }
        default:
            break
        }
    }

    func deleteLocalModels(_ ids: [String]) async throws -> Int64 {
        try await trackAsyncAction(
            category: .engine,
            action: "local_model.delete",
            startSummary: "Delete \(ids.count) local model weight(s)",
            metadata: ["models": ids.joined(separator: ",")],
            successDetail: { freedBytes in
                (
                    summary: "Deleted \(ids.count) local model weight(s), freed \(ByteCountFormatter.string(fromByteCount: freedBytes, countStyle: .file))",
                    metadata: [
                        "models": ids.joined(separator: ","),
                        "freedBytes": String(freedBytes)
                    ]
                )
            }
        ) {
            let sub = ["ai", "engine", "rm", "--json"] + ids
            let data: Data
            if let vault {
                data = try await runCLI(sub, cwd: vault.rootURL)
            } else {
                data = try await runCLIGlobal(sub)
            }
            struct RmResponse: Decodable {
                let removed: [RmItem]?
                let freedBytes: Int64
                enum CodingKeys: String, CodingKey {
                    case removed
                    case freedBytes = "freed_bytes"
                }
            }
            struct RmItem: Decodable {
                let model: String
                let freedBytes: Int64?
                let ok: Bool
                enum CodingKeys: String, CodingKey {
                    case model
                    case freedBytes = "freed_bytes"
                    case ok
                }
            }
            let resp = try JSONDecoder().decode(RmResponse.self, from: data)
            actionTracker.updateStatus { status in
                status.localModelCompletedCount = 0
                status.localModelDownloadFraction = nil
            }
            await refreshAIStatus()
            return resp.freedBytes
        }
    }

    func activateLocalStack(embedID: String, genID: String, dimensions: Int) async throws {
        guard let vault else { throw CLIError.noVault }
        if isIndexing { throw CLIError.indexRebuildInProgress }
        try await trackAsyncAction(
            category: .engine,
            action: "local_model.activate",
            startSummary: "Activate local Gemma models (\(embedID), \(genID), \(dimensions)d)",
            metadata: [
                "embedModel": embedID,
                "genModel": genID,
                "dimensions": String(dimensions),
                "provider": "llama-local"
            ],
            successDetail: { _ in
                (
                    summary: "Activated local Gemma models (\(embedID), \(genID), \(dimensions)d)",
                    metadata: [
                        "embedModel": embedID,
                        "genModel": genID,
                        "dimensions": String(dimensions),
                        "provider": "llama-local"
                    ]
                )
            }
        ) {
            _ = try await runCLI(["config", "set", "ai.provider", "llama-local"], cwd: vault.rootURL)
            _ = try await runCLI(["config", "set", "ai.embedding_model", embedID], cwd: vault.rootURL)
            _ = try await runCLI(["config", "set", "ai.dimensions", String(dimensions)], cwd: vault.rootURL)
            _ = try await runCLI(["config", "set", "ai.generation_model", genID], cwd: vault.rootURL)

            await refreshAIStatus()
            actionTracker.updateStatus { status in
                status.aiProvider = "llama-local"
                status.embedModel = embedID
                status.genModel = genID
                status.dimensions = dimensions
                status.localMatryoshkaDim = dimensions
            }
            startForceReembed()
        }
    }

    func startEngine() async throws {
        try await trackAsyncAction(
            category: .engine,
            action: "engine.start",
            startSummary: "Start local llama-server engine daemon"
        ) {
            if let vault {
                _ = try await runCLI(["ai", "engine", "start"], cwd: vault.rootURL)
            } else {
                _ = try await runCLIGlobal(["ai", "engine", "start"])
            }
            for _ in 0..<20 {
                let poll = await fetchEngineStatus(recordTelemetry: false)
                if poll?.isHealthy == true {
                    break
                }
                try? await Task.sleep(for: .milliseconds(500))
            }
            let st = await fetchEngineStatus(recordTelemetry: true)
            actionTracker.updateStatus { status in
                status.localEngineRunning = st?.isRunning ?? true
                status.localEnginePid = st?.primaryPid
                status.localEngineHealthy = st?.isHealthy ?? false
                status.localEngineError = nil
            }
        }
    }

    func stopEngine() async throws {
        try await trackAsyncAction(
            category: .engine,
            action: "engine.stop",
            startSummary: "Stop local llama-server engine daemon"
        ) {
            if let vault {
                _ = try await runCLI(["ai", "engine", "stop"], cwd: vault.rootURL)
            } else {
                _ = try await runCLIGlobal(["ai", "engine", "stop"])
            }
            actionTracker.updateStatus { status in
                status.localEngineRunning = false
                status.localEnginePid = nil
                status.localEngineHealthy = false
                status.localEngineRoles = nil
            }
            _ = await fetchEngineStatus(recordTelemetry: false)
        }
    }

    @discardableResult
    func fetchEngineStatus(recordTelemetry: Bool = false) async -> EngineStatus? {
        let start = ContinuousClock.now
        do {
            let data: Data
            if let vault {
                data = try await runCLIAllowingNonZero(["ai", "engine", "status", "--json"], cwd: vault.rootURL)
            } else {
                let res = try await runCLIGlobalRaw(["ai", "engine", "status", "--json"], expectNonZeroExit: true)
                data = res.stdout
            }
            let statusInfo = try JSONDecoder().decode(EngineStatus.self, from: data)
            self.localEngineStatus = statusInfo
            let elapsedMs = elapsedMilliseconds(since: start)

            let prevRunning = actionTracker.status.localEngineRunning
            let prevHealthy = actionTracker.status.localEngineHealthy
            let stateChanged = (prevRunning != statusInfo.isRunning) || (prevHealthy != statusInfo.isHealthy)

            if recordTelemetry || stateChanged {
                actionTracker.record(
                    category: .engine,
                    action: "engine.status",
                    phase: .succeeded,
                    durationMs: elapsedMs,
                    summary: "Engine status: running=\(statusInfo.isRunning), healthy=\(statusInfo.isHealthy)",
                    metadata: [
                        "running": statusInfo.isRunning ? "true" : "false",
                        "healthy": statusInfo.isHealthy ? "true" : "false",
                        "enginePath": statusInfo.enginePath,
                        "roles": statusInfo.roles?.map { "\($0.role):\($0.port)" }.joined(separator: ",") ?? ""
                    ]
                )
            }

            actionTracker.updateStatus { s in
                s.localEngineRunning = statusInfo.isRunning
                s.localEnginePid = statusInfo.primaryPid
                s.localEngineHealthy = statusInfo.isHealthy
                s.localEngineRoles = statusInfo.roles?.map { "\($0.role):\($0.port)" }
                s.localEngineError = nil
            }
            return statusInfo
        } catch {
            let elapsedMs = elapsedMilliseconds(since: start)
            if recordTelemetry {
                actionTracker.record(
                    category: .engine,
                    action: "engine.status",
                    phase: .failed,
                    durationMs: elapsedMs,
                    summary: "Engine status check failed",
                    detail: error.localizedDescription
                )
            }
            actionTracker.updateStatus { s in
                s.localEngineRunning = false
                s.localEnginePid = nil
                s.localEngineHealthy = false
                s.localEngineError = GUIActionTracker.boundedRedactedTail(error.localizedDescription, limit: GUIActionTracker.maxStatusErrorChars)
            }
            return nil
        }
    }

    func setMatryoshkaDimension(_ dim: Int) async throws {
        try await setConfigValue("ai.dimensions", String(dim))
        actionTracker.updateStatus { status in
            status.dimensions = dim
            status.localMatryoshkaDim = dim
        }
    }

    func setTaskPrefixPreset(_ preset: String) async throws {
        try await setConfigValue("ai.llama.task_prefix", preset)
        actionTracker.updateStatus { status in
            status.localTaskPrefixPreset = preset
        }
    }

    // MARK: - CLI Execution

    /// Vault-independent CLI spawn. Never logs stdin.
    /// Runs a vault-independent `2nb` command, throwing on a non-zero exit.
    /// Hang-guarded via runCLIGlobalRaw's CLIWatchdog.
    func runCLIGlobal(_ args: [String], stdin: Data? = nil) async throws -> Data {
        let result = try await runCLIGlobalRaw(args, stdin: stdin)
        guard result.status == 0 else {
            throw CLIError.nonZeroExit(result.status, message: result.stderrText)
        }
        return result.stdout
    }

    /// stdout, stderr, and the exit status of a `2nb` invocation.
    struct CLIRawResult {
        let stdout: Data
        let stderr: Data
        let status: Int32

        var stderrText: String {
            String(decoding: stderr, as: UTF8.self).trimmingCharacters(in: .whitespacesAndNewlines)
        }
    }

    /// runCLIGlobal without the throw-on-failure policy, for the callers where
    /// a non-zero exit is a RESULT rather than an error.
    ///
    /// `2nb doctor` is the reason this exists: it exits non-zero precisely when
    /// the self-test finds something wrong, and its JSON verdict — the thing
    /// the user needs to read — is on stdout. Throwing away stdout on failure
    /// would discard the payload in exactly the case it matters most.
    /// - Parameter expectNonZeroExit: suppresses the error logging for commands
    ///   where a non-zero exit is a normal verdict. `2nb doctor` returning 2
    ///   because your key is wrong is the tool working, not the tool failing;
    ///   logging it as "CLI failed" would fill the vault's error log with
    ///   entries that are not faults, and bury the ones that are.
    /// - Parameter extraEnvironment: overlaid onto the inherited environment, so
    ///   a caller can hand the CLI a credential for one invocation without
    ///   writing it anywhere. Never logged.
    func runCLIGlobalRaw(_ args: [String], stdin: Data? = nil, expectNonZeroExit: Bool = false, extraEnvironment: [String: String]? = nil) async throws -> CLIRawResult {
        let cmd = "2nb " + args.joined(separator: " ")
        log.info("CLI exec (global): \(cmd, privacy: .public)")
        let errorLogger = self.errorLogger
        let actionTracker = self.actionTracker
        let cliExecutable = resolvedCLIPath()
        return try await withCheckedThrowingContinuation { continuation in
            let process = Process()
            process.executableURL = URL(fileURLWithPath: cliExecutable)
            process.arguments = args
            if let extraEnvironment, !extraEnvironment.isEmpty {
                var env = ProcessInfo.processInfo.environment
                for (key, value) in extraEnvironment { env[key] = value }
                process.environment = env
            }
            let stdout = Pipe()
            let stderr = Pipe()
            process.standardOutput = stdout
            process.standardError = stderr
            // The payload is written AFTER launch and off the main actor
            // (feedStdin). Writing it here, before the child exists, blocks
            // forever once it exceeds the 64 KB pipe buffer, because nothing
            // can drain the pipe yet: a long Ask history froze the app.
            let input: Pipe? = stdin == nil ? nil : Pipe()
            if let input { process.standardInput = input }

            let drain = PipeDrain()
            stdout.fileHandleForReading.readabilityHandler = { handle in
                let chunk = handle.availableData
                if chunk.isEmpty {
                    handle.readabilityHandler = nil
                } else {
                    drain.appendStdout(chunk)
                }
            }
            stderr.fileHandleForReading.readabilityHandler = { handle in
                let chunk = handle.availableData
                if chunk.isEmpty {
                    handle.readabilityHandler = nil
                } else {
                    drain.appendStderr(chunk)
                }
            }

            // Absolute hang guard: SIGTERM at CLIWatchdog.timeout, SIGKILL
            // after a grace. The CLI self-bounds every provider call, so this
            // only ever fires on a genuine wedge, never on a slow working
            // model (CLIWatchdog).
            let watchdog = CLIWatchdogTimer(process: process, command: cmd) { message in
                log.error("\(message, privacy: .public)")
                errorLogger?.log(message)
            }

            process.terminationHandler = { [actionTracker] proc in
                watchdog.cancel()
                stdout.fileHandleForReading.readabilityHandler = nil
                stderr.fileHandleForReading.readabilityHandler = nil
                drain.appendStdout(stdout.fileHandleForReading.readDataToEndOfFile())
                drain.appendStderr(stderr.fileHandleForReading.readDataToEndOfFile())
                if proc.terminationStatus != 0 && !expectNonZeroExit {
                    let errMsg = String(data: drain.stderrData, encoding: .utf8)?
                        .trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
                    if !errMsg.isEmpty {
                        let safeCmd = GUIActionTracker.redact(cmd)
                        let safeErrMsg = Self.boundedRedacted(errMsg)
                        log.error("CLI \(safeCmd, privacy: .public) failed (exit \(proc.terminationStatus)): \(safeErrMsg, privacy: .public)")
                        errorLogger?.log("CLI \(safeCmd) failed (exit \(proc.terminationStatus)): \(safeErrMsg)")
                    }
                    let summary = "CLI failed (exit \(proc.terminationStatus)): \(cmd)"
                    let detail = errMsg.isEmpty ? nil : errMsg
                    Task { @MainActor in
                        actionTracker.record(
                            category: .cli,
                            action: "cli.exec",
                            phase: .failed,
                            summary: summary,
                            detail: detail,
                            metadata: ["exitCode": String(proc.terminationStatus)]
                        )
                    }
                }
                continuation.resume(returning: CLIRawResult(
                    stdout: drain.stdoutData,
                    stderr: drain.stderrData,
                    status: proc.terminationStatus
                ))
            }

            do {
                try process.run()
                watchdog.arm()
                Self.feedStdin(input, stdin)
            } catch {
                Self.releaseFailedLaunch(process, pipes: [stdout, stderr, input])
                let safeCmd = GUIActionTracker.redact(cmd)
                let errText = error.localizedDescription
                let safeErrText = Self.boundedRedacted(errText)
                log.error("CLI \(safeCmd) launch failed: \(safeErrText)")
                errorLogger?.log("CLI \(safeCmd) launch failed: \(safeErrText)")
                Task { @MainActor [actionTracker] in
                    actionTracker.record(
                        category: .cli,
                        action: "cli.exec",
                        phase: .failed,
                        summary: "CLI launch failed: \(safeCmd)",
                        detail: errText
                    )
                }
                continuation.resume(throwing: error)
            }
        }
    }

    /// - Parameter hangWatchdog: opts one invocation out of the CLIWatchdog
    ///   absolute bound. Reserved for a command that deliberately runs longer
    ///   than the watchdog's request/response ceiling while still making
    ///   progress (only `ai embed-probe` today; see `embedProbe` for the
    ///   rationale, which mirrors the streaming runners'). Every other call
    ///   keeps the default `true`.
    /// - Parameter cancellable: cancelling the calling Task terminates the
    ///   child and throws CancellationError. Only for READ-ONLY commands
    ///   whose result is worthless once superseded (per-keystroke search):
    ///   never for index or any write, which must run to completion.
    func runCLI(_ args: [String], cwd: URL, stdin: Data? = nil, hangWatchdog: Bool = true, cancellable: Bool = false) async throws -> Data {
        guard cancellable else {
            return try await runCLIProcess(args, cwd: cwd, stdin: stdin, hangWatchdog: hangWatchdog, cancelBox: nil)
        }
        let box = ProcessCancelBox()
        return try await withTaskCancellationHandler {
            try await runCLIProcess(args, cwd: cwd, stdin: stdin, hangWatchdog: hangWatchdog, cancelBox: box)
        } onCancel: {
            box.cancel()
        }
    }

    private func runCLIProcess(_ args: [String], cwd: URL, stdin: Data?, hangWatchdog: Bool, cancelBox: ProcessCancelBox?) async throws -> Data {
        let fullArgs = CLIPath.args(args, vault: cwd)
        let cmd = "2nb " + fullArgs.joined(separator: " ")
        log.info("CLI exec: \(cmd, privacy: .public)")
        // Capture the (Sendable) error logger so the background terminationHandler
        // can record a genuine CLI failure to the per-vault `.2ndbrain/logs` file
        // — the surface the user's "read the logs to debug" workflow targets.
        let errorLogger = self.errorLogger
        let actionTracker = self.actionTracker
        let cliExecutable = resolvedCLIPath()
        return try await withCheckedThrowingContinuation { continuation in
            let process = Process()
            process.executableURL = URL(fileURLWithPath: cliExecutable)
            process.arguments = fullArgs
            process.currentDirectoryURL = cwd
            let stdout = Pipe()
            let stderr = Pipe()
            process.standardOutput = stdout
            process.standardError = stderr
            // The payload is written AFTER launch and off the main actor
            // (feedStdin). Writing it here, before the child exists, blocks
            // forever once it exceeds the 64 KB pipe buffer, because nothing
            // can drain the pipe yet: a long Ask history froze the app.
            let input: Pipe? = stdin == nil ? nil : Pipe()
            if let input { process.standardInput = input }

            // Drain both pipes as data arrives. Without this the child blocks
            // on write() once its stdout exceeds the ~16-64KB pipe buffer
            // (e.g. `models list --discover` returns ~180KB of JSON),
            // deadlocking because terminationHandler never fires.
            let drain = PipeDrain()
            stdout.fileHandleForReading.readabilityHandler = { handle in
                let chunk = handle.availableData
                if chunk.isEmpty {
                    handle.readabilityHandler = nil
                } else {
                    drain.appendStdout(chunk)
                }
            }
            stderr.fileHandleForReading.readabilityHandler = { handle in
                let chunk = handle.availableData
                if chunk.isEmpty {
                    handle.readabilityHandler = nil
                } else {
                    drain.appendStderr(chunk)
                }
            }

            // Absolute hang guard (see CLIWatchdog): fires only on a genuine
            // wedge, never on a slow working model. nil when the caller
            // opted out via hangWatchdog: false.
            let watchdog: CLIWatchdogTimer? = hangWatchdog
                ? CLIWatchdogTimer(process: process, command: cmd) { message in
                    log.error("\(message, privacy: .public)")
                    errorLogger?.log(message)
                }
                : nil

            process.terminationHandler = { [actionTracker] proc in
                watchdog?.cancel()
                // Detach the handlers so no late reads fire after we resume.
                stdout.fileHandleForReading.readabilityHandler = nil
                stderr.fileHandleForReading.readabilityHandler = nil
                // Flush any bytes that arrived between the last handler call
                // and process exit.
                drain.appendStdout(stdout.fileHandleForReading.readDataToEndOfFile())
                drain.appendStderr(stderr.fileHandleForReading.readDataToEndOfFile())
                if proc.terminationStatus == 0 {
                    continuation.resume(returning: drain.stdoutData)
                } else if cancelBox?.isCancelled == true {
                    // Terminated on purpose because the caller was
                    // cancelled: not a CLI failure, so nothing is logged.
                    continuation.resume(throwing: CancellationError())
                } else {
                    let errMsg = String(data: drain.stderrData, encoding: .utf8)?
                        .trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
                    let safeCmd = GUIActionTracker.redact(cmd)
                    let safeErrMsg = Self.boundedRedacted(errMsg)
                    if !safeErrMsg.isEmpty {
                        log.error("CLI \(safeCmd, privacy: .public) failed (exit \(proc.terminationStatus)): \(safeErrMsg, privacy: .public)")
                    }
                    errorLogger?.log("CLI \(safeCmd) failed (exit \(proc.terminationStatus)): \(safeErrMsg)")
                    let summary = "CLI failed (exit \(proc.terminationStatus)): \(cmd)"
                    let detail = errMsg.isEmpty ? nil : errMsg
                    Task { @MainActor in
                        actionTracker.record(
                            category: .cli,
                            action: "cli.exec",
                            phase: .failed,
                            summary: summary,
                            detail: detail,
                            metadata: ["exitCode": String(proc.terminationStatus)]
                        )
                    }
                    continuation.resume(throwing: CLIError.nonZeroExit(proc.terminationStatus, message: errMsg))
                }
            }

            do {
                try process.run()
                watchdog?.arm()
                Self.feedStdin(input, stdin)
                cancelBox?.attach(process)
            } catch {
                Self.releaseFailedLaunch(process, pipes: [stdout, stderr, input])
                let safeCmd = GUIActionTracker.redact(cmd)
                let errText = error.localizedDescription
                let safeErrText = Self.boundedRedacted(errText)
                log.error("CLI \(safeCmd) launch failed: \(safeErrText)")
                errorLogger?.log("CLI \(safeCmd) launch failed: \(safeErrText)")
                Task { @MainActor [actionTracker] in
                    actionTracker.record(
                        category: .cli,
                        action: "cli.exec",
                        phase: .failed,
                        summary: "CLI launch failed: \(safeCmd)",
                        detail: errText
                    )
                }
                continuation.resume(throwing: error)
            }
        }
    }

    /// Writes a stdin payload to a child that has ALREADY launched, off the
    /// main actor, then closes the pipe so the child sees EOF.
    nonisolated static func feedStdin(_ pipe: Pipe?, _ data: Data?) {
        guard let pipe else { return }
        let handle = pipe.fileHandleForWriting
        DispatchQueue.global(qos: .userInitiated).async {
            // The throwing form: a child that exits before reading gives
            // EPIPE as an error, where write(_:) raises an ObjC exception.
            if let data { try? handle.write(contentsOf: data) }
            try? handle.close()
        }
    }

    /// Releases what a Process that never launched still holds. Its
    /// terminationHandler (which captures the pipes) never fires and the
    /// readability sources never see EOF, so each failed launch leaked six
    /// file descriptors; a launch failure repeated by the 5 s MCP status
    /// poll then exhausted the 256-descriptor limit within minutes, after
    /// which every CLI-backed action in the app failed.
    nonisolated static func releaseFailedLaunch(_ process: Process, pipes: [Pipe?]) {
        process.terminationHandler = nil
        for case let pipe? in pipes {
            pipe.fileHandleForReading.readabilityHandler = nil
            try? pipe.fileHandleForReading.close()
            try? pipe.fileHandleForWriting.close()
        }
    }

    /// Like runCLI but returns stdout regardless of exit code.
    /// Needed for `2nb lint` which exits 2 on validation errors but still emits valid JSON.
    private func runCLIAllowingNonZero(_ args: [String], cwd: URL) async throws -> Data {
        try await runCLIAllowingNonZeroWithOutput(args, cwd: cwd).stdout
    }

    private func runCLIAllowingNonZeroWithOutput(
        _ args: [String],
        cwd: URL
    ) async throws -> (stdout: Data, stderr: String, exitCode: Int32) {
        let fullArgs = CLIPath.args(args, vault: cwd)
        let cmd = "2nb " + fullArgs.joined(separator: " ")
        log.info("CLI exec (non-zero ok): \(cmd)")
        let cliExecutable = resolvedCLIPath()
        return try await withCheckedThrowingContinuation { continuation in
            let process = Process()
            process.executableURL = URL(fileURLWithPath: cliExecutable)
            process.arguments = fullArgs
            process.currentDirectoryURL = cwd
            let stdout = Pipe()
            let stderr = Pipe()
            process.standardOutput = stdout
            process.standardError = stderr
            let drain = PipeDrain()
            stdout.fileHandleForReading.readabilityHandler = { handle in
                let chunk = handle.availableData
                if chunk.isEmpty {
                    handle.readabilityHandler = nil
                } else {
                    drain.appendStdout(chunk)
                }
            }
            stderr.fileHandleForReading.readabilityHandler = { handle in
                let chunk = handle.availableData
                if chunk.isEmpty {
                    handle.readabilityHandler = nil
                } else {
                    drain.appendStderr(chunk)
                }
            }
            // Absolute hang guard (see CLIWatchdog): fires only on a genuine
            // wedge, never on a slow working model.
            let watchdog = CLIWatchdogTimer(process: process, command: cmd) { message in
                log.error("\(message, privacy: .public)")
            }
            process.terminationHandler = { proc in
                watchdog.cancel()
                stdout.fileHandleForReading.readabilityHandler = nil
                stderr.fileHandleForReading.readabilityHandler = nil
                drain.appendStdout(stdout.fileHandleForReading.readDataToEndOfFile())
                drain.appendStderr(stderr.fileHandleForReading.readDataToEndOfFile())
                let errMsg = String(data: drain.stderrData, encoding: .utf8)?
                    .trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
                if proc.terminationStatus != 0 {
                    log.debug("CLI \(cmd) exited \(proc.terminationStatus) (allowed)")
                }
                continuation.resume(returning: (drain.stdoutData, errMsg, proc.terminationStatus))
            }
            do {
                try process.run()
                watchdog.arm()
            } catch {
                Self.releaseFailedLaunch(process, pipes: [stdout, stderr])
                log.error("CLI \(cmd) launch failed: \(error.localizedDescription)")
                continuation.resume(throwing: error)
            }
        }
    }

    // MARK: - Action Instrumentation Helpers

    private func elapsedMilliseconds(since start: ContinuousClock.Instant) -> Int {
        let duration = ContinuousClock.now - start
        return Int(duration.components.seconds * 1000) + Int(duration.components.attoseconds / 1_000_000_000_000_000)
    }

    private func trackAsyncAction<T>(
        category: GUIActionCategory,
        action: String,
        startSummary: String,
        metadata: [String: String] = [:],
        successDetail: ((T) -> (summary: String, metadata: [String: String]))? = nil,
        operation: () async throws -> T
    ) async throws -> T {
        actionTracker.record(
            category: category,
            action: action,
            phase: .started,
            summary: startSummary,
            metadata: metadata
        )
        let start = ContinuousClock.now
        do {
            let result = try await operation()
            let elapsedMs = elapsedMilliseconds(since: start)
            let resolved: (summary: String, metadata: [String: String]) = successDetail?(result)
                ?? (summary: startSummary, metadata: metadata)
            actionTracker.record(
                category: category,
                action: action,
                phase: .succeeded,
                durationMs: elapsedMs,
                summary: resolved.summary,
                metadata: resolved.metadata
            )
            return result
        } catch is CancellationError {
            throw CancellationError()
        } catch {
            let elapsedMs = elapsedMilliseconds(since: start)
            actionTracker.record(
                category: category,
                action: action,
                phase: .failed,
                durationMs: elapsedMs,
                summary: "\(startSummary) failed",
                detail: error.localizedDescription,
                metadata: metadata
            )
            throw error
        }
    }
}

/// Thread-safe accumulator for subprocess stdout/stderr. Readability
/// handlers fire on arbitrary queues, so appends are serialized through
/// an internal lock. Used by runCLI / runCLIAllowingNonZero to drain the
/// child's pipes as data arrives — avoiding the pipe-buffer deadlock
/// where a child blocks on write() past ~64KB and terminationHandler
/// never fires.
/// Lets a cancelled Task terminate the child it is waiting on. `cancel` can
/// arrive before the process launches, so the flag is remembered and
/// `attach` terminates a process that was cancelled before it started.
final class ProcessCancelBox: @unchecked Sendable {
    private let lock = NSLock()
    private var process: Process?
    private var cancelled = false

    var isCancelled: Bool {
        lock.lock(); defer { lock.unlock() }
        return cancelled
    }

    func attach(_ process: Process) {
        lock.lock()
        self.process = process
        let terminateNow = cancelled
        lock.unlock()
        if terminateNow, process.isRunning { process.terminate() }
    }

    func cancel() {
        lock.lock()
        cancelled = true
        let process = self.process
        lock.unlock()
        if let process, process.isRunning { process.terminate() }
    }
}

final class PipeDrain: @unchecked Sendable {
    private let lock = NSLock()
    private var _stdout = Data()
    private var _stderr = Data()

    func appendStdout(_ data: Data) {
        guard !data.isEmpty else { return }
        lock.lock(); defer { lock.unlock() }
        _stdout.append(data)
    }

    func appendStderr(_ data: Data) {
        guard !data.isEmpty else { return }
        lock.lock(); defer { lock.unlock() }
        _stderr.append(data)
    }

    var stdoutData: Data {
        lock.lock(); defer { lock.unlock() }
        return _stdout
    }

    var stderrData: Data {
        lock.lock(); defer { lock.unlock() }
        return _stderr
    }
}

// MARK: - AI Types

struct AIStatusInfo: Codable {
    let provider: String
    let embeddingProvider: String?
    let generationProvider: String?
    let embeddingModel: String
    let genModel: String
    let dimensions: Int
    let embedAvailable: Bool
    let genAvailable: Bool
    let embeddingCount: Int
    let documentCount: Int
    let similarityThreshold: Double?
    let similarityThresholdSource: String?

    // Portability — the vault's self-reported embedding state from the
    // DB (source of truth), plus a derived status label. Optional for
    // decoder compatibility with older 2nb binaries that don't emit
    // these fields yet.
    let vaultEmbeddingModels: [String]?
    let vaultEmbeddingDim: Int?
    let vaultTotalDocs: Int?
    let vaultEmbeddedDocs: Int?
    // Content-bearing docs (embedded + awaiting); excludes empty notes that
    // can't be embedded. The denominator for "Embedded X / Y" so blank notes
    // don't show as a permanent gap. Optional — older 2nb binaries omit it.
    let vaultEmbeddableDocs: Int?
    let portabilityStatus: String?
    let portabilityAction: String?

    // Per-provider readiness surfaced for the AI Hub. Optional because
    // binaries before 0.3.0 don't emit this field.
    let providers: [ProviderStatusInfo]?

    // Rerank slot: an optional cross-encoder stage that ships default OFF.
    // Optional because binaries before the rerank-status fields don't emit them.
    let rerankEnabled: Bool?
    let rerankModel: String?
    let rerankAvailable: Bool?

    // Per-account model-verification summary for the active provider
    // (`model_access` in `2nb ai status --json`, from persisted `models verify`
    // / `models test --save` results). Nil when nothing was ever validated, or
    // on a CLI predating the summary.
    let modelAccess: ModelAccessSummary?

    /// Denominator for embedding-coverage displays: the embeddable doc count
    /// when the CLI reports it, else the raw document count (older CLI).
    var embeddableDenominator: Int { vaultEmbeddableDocs ?? documentCount }

    var effectiveEmbeddingProvider: String {
        if let embeddingProvider, !embeddingProvider.isEmpty {
            return embeddingProvider
        }
        return provider
    }

    var effectiveGenerationProvider: String {
        if let generationProvider, !generationProvider.isEmpty {
            return generationProvider
        }
        return provider
    }

    var isMixedProviders: Bool {
        !effectiveEmbeddingProvider.isEmpty && !effectiveGenerationProvider.isEmpty && effectiveEmbeddingProvider != effectiveGenerationProvider
    }

    enum CodingKeys: String, CodingKey {
        case provider
        case embeddingProvider = "embedding_provider"
        case generationProvider = "generation_provider"
        case embeddingModel = "embedding_model"
        case genModel = "generation_model"
        case dimensions
        case embedAvailable = "embed_available"
        case genAvailable = "gen_available"
        case embeddingCount = "embedding_count"
        case documentCount = "document_count"
        case similarityThreshold = "similarity_threshold"
        case similarityThresholdSource = "similarity_threshold_source"
        case vaultEmbeddingModels = "vault_embedding_models"
        case vaultEmbeddingDim = "vault_embedding_dim"
        case vaultTotalDocs = "vault_total_docs"
        case vaultEmbeddedDocs = "vault_embedded_docs"
        case vaultEmbeddableDocs = "vault_embeddable_docs"
        case portabilityStatus = "portability_status"
        case portabilityAction = "portability_action"
        case providers
        case rerankEnabled = "rerank_enabled"
        case rerankModel = "rerank_model"
        case rerankAvailable = "rerank_available"
        case modelAccess = "model_access"
    }
}

/// Aggregated per-account model-verification state for the active provider,
/// mirroring the Go `cli.ModelAccessSummary` from `2nb ai status --json`
/// (`model_access`). `verified` counts models that passed a probe; `accessDenied`
/// counts the AWS staged-rollout gate (a runtime 403 on a listed model);
/// `otherFailures` counts every other classified failure. The parent field is
/// nil when nothing was ever validated.
struct ModelAccessSummary: Codable, Equatable {
    let verified: Int
    let accessDenied: Int
    let otherFailures: Int
    let lastVerifiedAt: String?

    enum CodingKeys: String, CodingKey {
        case verified
        case accessDenied = "access_denied"
        case otherFailures = "other_failures"
        case lastVerifiedAt = "last_verified_at"
    }
}

struct ProviderStatusInfo: Codable, Identifiable {
    var id: String { name }
    let name: String
    let configPresent: Bool
    let disabled: Bool
    let reachable: Bool
    let reason: String?
    let detail: String?
    let tokenSource: String?

    enum CodingKeys: String, CodingKey {
        case name
        case configPresent = "config_present"
        case disabled
        case reachable
        case reason
        case detail
        case tokenSource = "token_source"
    }
}

/// One entry of `2nb skills list --json`, mirroring the Go `skills.InstallStatus`
/// struct. Used to show whether the Claude Code skill is installed for this user.
struct SkillStatusInfo: Codable, Identifiable {
    var id: String { slug }
    let slug: String
    let name: String
    let projectPath: String?
    let userPath: String?
    let projectInstalled: Bool
    let userInstalled: Bool
    let note: String?

    enum CodingKeys: String, CodingKey {
        case slug
        case name
        case projectPath = "project_path"
        case userPath = "user_path"
        case projectInstalled = "project_installed"
        case userInstalled = "user_installed"
        case note
    }
}

/// One entry of `2nb mcp configured --json`, mirroring the Go
/// `mcp.ConfiguredStatus` struct. Reports whether the 2ndbrain MCP server is
/// wired into the AI client config (durable), distinct from `mcp status` which
/// only sees servers running right now.
struct MCPConfiguredInfo: Codable, Identifiable {
    var id: String { client }
    let client: String
    let configPath: String
    let configured: Bool
    let scope: String?
    let serverKey: String?
    let vaultPath: String

    enum CodingKeys: String, CodingKey {
        case client
        case configPath = "config_path"
        case configured
        case scope
        case serverKey = "server_key"
        case vaultPath = "vault_path"
    }
}

/// One entry of `2nb instructions configured --all --json`, mirroring the Go
/// `instructions.Status`. Reports whether the always-loaded 2nb reference block
/// is present in the client's global agent memory file (`~/.claude/CLAUDE.md`).
/// Only clients with a known memory file (claude-code, claude-desktop) appear.
struct GlobalInstructionsInfo: Codable, Identifiable {
    var id: String { client }
    let client: String
    let filePath: String
    let installed: Bool
    let upToDate: Bool
    let modified: Bool
    let installedVersion: String?

    enum CodingKeys: String, CodingKey {
        case client
        case filePath = "file_path"
        case installed
        case upToDate = "up_to_date"
        case modified
        case installedVersion = "installed_version"
    }
}

struct AIAskResult: Codable {
    let answer: String
    let sources: [String]
}

struct AIChatTurn: Codable, Equatable {
    let role: String
    let content: String
}

// CLIAskResponse is the envelope `2nb ask --json` now returns.
struct CLIAskResponse: Codable, Equatable {
    let mode: String
    let warnings: [String]?
    let answer: String
    let sources: [String]
    let rewrittenQuery: String?

    enum CodingKeys: String, CodingKey {
        case mode, warnings, answer, sources
        case rewrittenQuery = "rewritten_query"
    }
}

// CLISearchResponse is the envelope `2nb search --json` now returns.
struct CLISearchResponse: Codable {
    let mode: String
    let warnings: [String]?
    let results: [CLISearchResult]
}

struct CLISearchResult: Codable, Identifiable, Equatable {
    let docID: String
    let path: String
    let title: String
    let docType: String?
    let chunkID: String?
    let headingPath: String?
    let content: String?
    let score: Double
    let vectorScore: Double?
    let status: String?

    var id: String { chunkID ?? "\(docID)|\(path)|\(headingPath ?? "")" }

    enum CodingKeys: String, CodingKey {
        case docID = "doc_id"
        case path, title
        case docType = "type"
        case chunkID = "chunk_id"
        case headingPath = "heading_path"
        case content
        case score
        case vectorScore = "vector_score"
        case status
    }
}

struct VaultNoteItem: Codable, Identifiable, Equatable {
    let id: String
    let path: String
    let title: String
    let docType: String?
    let status: String?
    let modifiedAt: String?

    var displayTitle: String {
        title.isEmpty ? (path as NSString).lastPathComponent : title
    }

    enum CodingKeys: String, CodingKey {
        case id, path, title, status
        case docType = "type"
        case modifiedAt = "modified_at"
    }
}

struct PolishUndoResult: Codable, Equatable {
    let path: String
    let reverted: Bool
    let restoredPaths: [String]?

    enum CodingKeys: String, CodingKey {
        case path, reverted
        case restoredPaths = "restored_paths"
    }

    init(path: String, reverted: Bool, restoredPaths: [String]? = nil) {
        self.path = path
        self.reverted = reverted
        self.restoredPaths = restoredPaths
    }
}

struct StaleDocInfo: Codable, Identifiable, Equatable {
    var id: String { path }
    let path: String
    let title: String
    let daysStale: Int

    enum CodingKeys: String, CodingKey {
        case path, title
        case daysStale = "days_stale"
    }
}

struct EmbeddingProgress {
    var current: Int
    var total: Int
    var chunksDone: Int?
    var chunksTotal: Int?
    var docsPerSec: Double?
    var remainingSeconds: Double?
    var estimatedTotalSeconds: Double?
}

enum IndexPhase: String {
    case ready = "Ready"
    case indexingFiles = "Indexing files"
    case embedding = "Generating embeddings"
    case complete = "Complete"
    case failed = "Failed"
}

struct IndexProgress {
    var phase: IndexPhase = .indexingFiles
    // True when this run is a full "Re-embed All" (`2nb index --force-reembed`)
    // rather than an incremental rebuild. Carried on the progress struct — not
    // read from `pendingForceReembed`, which is cleared the moment the run
    // begins — so the sheet's title and copy stay accurate through every phase.
    var forceReembed: Bool = false
    var currentFile: String = ""
    var filesIndexed: Int = 0
    var embeddingCurrent: Int = 0
    var embeddingTotal: Int = 0
    var chunksDone: Int = 0
    var chunksTotal: Int = 0
    var docsPerSec: Double?
    var initialEstimatedSeconds: Double?
    var estimatedRemainingSeconds: Double?
    var estimatedCostUSD: Double?
    var estimateIsCloud: Bool = false
    var estimateSpeedSource: String?
    var docsIndexed: Int = 0
    var chunksCreated: Int = 0
    var linksFound: Int = 0
    var error: String?
    var elapsed: TimeInterval = 0
}

// MARK: - Lint Types

struct LintIssue: Codable, Identifiable {
    var id: String { "\(path):\(line ?? 0):\(message)" }
    let path: String
    let line: Int?
    let level: String
    let message: String

    // Additive broken-wikilink classification (nil on every other issue kind,
    // and on a pre-classification CLI). `target` is the raw authored [[target]];
    // `fix` is "drift" | "ambiguous" | "missing"; `driftTarget` is the canonical
    // target the link would repair to when `fix == "drift"`. The `message`
    // string is unchanged, so an older CLI (all three nil) still classifies via
    // `LintFinding.classify(message:)`.
    let target: String?
    let fix: String?
    let driftTarget: String?
    let kind: String?
    let field: String?
    let candidates: [String]?

    enum CodingKeys: String, CodingKey {
        case path, line, level, message, target, fix, kind, field, candidates
        case driftTarget = "drift_target"
    }

    init(
        path: String,
        line: Int?,
        level: String,
        message: String,
        target: String?,
        fix: String?,
        driftTarget: String?,
        kind: String? = nil,
        field: String? = nil,
        candidates: [String]? = nil
    ) {
        self.path = path
        self.line = line
        self.level = level
        self.message = message
        self.target = target
        self.fix = fix
        self.driftTarget = driftTarget
        self.kind = kind
        self.field = field
        self.candidates = candidates
    }
}

struct LintReport: Codable {
    let issues: [LintIssue]
    let filesChecked: Int
    let errors: Int
    let warnings: Int

    enum CodingKeys: String, CodingKey {
        case issues, errors, warnings
        case filesChecked = "files_checked"
    }
}

struct FixLinkAction: Codable, Equatable, Identifiable {
    var id: String { "\(action)|\(from)|\(to ?? "")|\(reason)" }
    let action: String
    let from: String
    let to: String?
    let count: Int
    let reason: String

    enum CodingKeys: String, CodingKey {
        case action, from, to, count, reason
    }
}

struct FixFieldChange: Codable, Equatable, Identifiable {
    var id: String { "\(field)|\(to)|\(reason)" }
    let field: String
    let from: String?
    let to: String
    let reason: String

    enum CodingKeys: String, CodingKey {
        case field, from, to, reason
    }
}

struct FixNoteChange: Codable, Equatable, Identifiable {
    var id: String { path }
    let path: String
    let linkActions: [FixLinkAction]
    let frontmatterFixes: [FixFieldChange]

    enum CodingKeys: String, CodingKey {
        case path
        case linkActions = "link_actions"
        case frontmatterFixes = "frontmatter_fixes"
    }

    init(path: String, linkActions: [FixLinkAction], frontmatterFixes: [FixFieldChange]) {
        self.path = path
        self.linkActions = linkActions
        self.frontmatterFixes = frontmatterFixes
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        path = try c.decode(String.self, forKey: .path)
        linkActions = try c.decodeIfPresent([FixLinkAction].self, forKey: .linkActions) ?? []
        frontmatterFixes = try c.decodeIfPresent([FixFieldChange].self, forKey: .frontmatterFixes) ?? []
    }
}

struct FixAmbiguousLink: Codable, Equatable, Identifiable {
    var id: String { "\(path)|\(target)" }
    let path: String
    let target: String
    let candidates: [String]

    enum CodingKeys: String, CodingKey {
        case path, target, candidates
    }

    init(path: String, target: String, candidates: [String]) {
        self.path = path
        self.target = target
        self.candidates = candidates
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        path = try c.decode(String.self, forKey: .path)
        target = try c.decode(String.self, forKey: .target)
        candidates = try c.decodeIfPresent([String].self, forKey: .candidates) ?? []
    }
}

struct AIFixableIssue: Codable, Equatable, Identifiable {
    var id: String { "\(path)|\(kind)|\(target ?? "")|\(message)" }
    let path: String
    let kind: String
    let message: String
    let target: String?

    init(path: String, kind: String, message: String, target: String? = nil) {
        self.path = path
        self.kind = kind
        self.message = message
        self.target = target
    }
}

struct FixVaultReport: Codable, Equatable {
    let filesScanned: Int
    let notesChanged: Int
    let totalFixes: Int
    let schemaUpdated: Bool
    let schemaStatusesAdded: [String]
    let schemaIssuesResolved: Int
    let linksUpdated: Int
    let linksUnlinked: Int
    let formattingFixed: Int
    let written: Bool
    let undoAvailable: Bool
    let notes: [FixNoteChange]
    let ambiguous: [FixAmbiguousLink]
    let aiFixableCount: Int
    let aiFixableIssues: [AIFixableIssue]
    let aiApplied: Bool
    let aiFixesApplied: Int
    let aiProvider: String?
    let aiModel: String?
    let aiSkippedReason: String?
    let warnings: [String]

    enum CodingKeys: String, CodingKey {
        case filesScanned = "files_scanned"
        case notesChanged = "notes_changed"
        case totalFixes = "total_fixes"
        case schemaUpdated = "schema_updated"
        case schemaStatusesAdded = "schema_statuses_added"
        case schemaIssuesResolved = "schema_issues_resolved"
        case linksUpdated = "links_updated"
        case linksUnlinked = "links_unlinked"
        case formattingFixed = "formatting_fixed"
        case written
        case undoAvailable = "undo_available"
        case notes, ambiguous
        case aiFixableCount = "ai_fixable_count"
        case aiFixableIssues = "ai_fixable_issues"
        case aiApplied = "ai_applied"
        case aiFixesApplied = "ai_fixes_applied"
        case aiProvider = "ai_provider"
        case aiModel = "ai_model"
        case aiSkippedReason = "ai_skipped_reason"
        case warnings
    }

    init(
        filesScanned: Int,
        notesChanged: Int,
        totalFixes: Int,
        schemaUpdated: Bool,
        schemaStatusesAdded: [String],
        schemaIssuesResolved: Int,
        linksUpdated: Int,
        linksUnlinked: Int,
        formattingFixed: Int,
        written: Bool,
        undoAvailable: Bool,
        notes: [FixNoteChange],
        ambiguous: [FixAmbiguousLink],
        aiFixableCount: Int = 0,
        aiFixableIssues: [AIFixableIssue] = [],
        aiApplied: Bool = false,
        aiFixesApplied: Int = 0,
        aiProvider: String? = nil,
        aiModel: String? = nil,
        aiSkippedReason: String? = nil,
        warnings: [String] = []
    ) {
        self.filesScanned = filesScanned
        self.notesChanged = notesChanged
        self.totalFixes = totalFixes
        self.schemaUpdated = schemaUpdated
        self.schemaStatusesAdded = schemaStatusesAdded
        self.schemaIssuesResolved = schemaIssuesResolved
        self.linksUpdated = linksUpdated
        self.linksUnlinked = linksUnlinked
        self.formattingFixed = formattingFixed
        self.written = written
        self.undoAvailable = undoAvailable
        self.notes = notes
        self.ambiguous = ambiguous
        self.aiFixableCount = aiFixableCount
        self.aiFixableIssues = aiFixableIssues
        self.aiApplied = aiApplied
        self.aiFixesApplied = aiFixesApplied
        self.aiProvider = aiProvider
        self.aiModel = aiModel
        self.aiSkippedReason = aiSkippedReason
        self.warnings = warnings
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        filesScanned = try c.decodeIfPresent(Int.self, forKey: .filesScanned) ?? 0
        notesChanged = try c.decodeIfPresent(Int.self, forKey: .notesChanged) ?? 0
        totalFixes = try c.decodeIfPresent(Int.self, forKey: .totalFixes) ?? 0
        schemaUpdated = try c.decodeIfPresent(Bool.self, forKey: .schemaUpdated) ?? false
        schemaStatusesAdded = try c.decodeIfPresent([String].self, forKey: .schemaStatusesAdded) ?? []
        schemaIssuesResolved = try c.decodeIfPresent(Int.self, forKey: .schemaIssuesResolved) ?? 0
        linksUpdated = try c.decodeIfPresent(Int.self, forKey: .linksUpdated) ?? 0
        linksUnlinked = try c.decodeIfPresent(Int.self, forKey: .linksUnlinked) ?? 0
        formattingFixed = try c.decodeIfPresent(Int.self, forKey: .formattingFixed) ?? 0
        written = try c.decodeIfPresent(Bool.self, forKey: .written) ?? false
        undoAvailable = try c.decodeIfPresent(Bool.self, forKey: .undoAvailable) ?? false
        notes = try c.decodeIfPresent([FixNoteChange].self, forKey: .notes) ?? []
        ambiguous = try c.decodeIfPresent([FixAmbiguousLink].self, forKey: .ambiguous) ?? []
        aiFixableCount = try c.decodeIfPresent(Int.self, forKey: .aiFixableCount) ?? 0
        aiFixableIssues = try c.decodeIfPresent([AIFixableIssue].self, forKey: .aiFixableIssues) ?? []
        aiApplied = try c.decodeIfPresent(Bool.self, forKey: .aiApplied) ?? false
        aiFixesApplied = try c.decodeIfPresent(Int.self, forKey: .aiFixesApplied) ?? 0
        aiProvider = try c.decodeIfPresent(String.self, forKey: .aiProvider)
        aiModel = try c.decodeIfPresent(String.self, forKey: .aiModel)
        aiSkippedReason = try c.decodeIfPresent(String.self, forKey: .aiSkippedReason)
        warnings = try c.decodeIfPresent([String].self, forKey: .warnings) ?? []
    }
}

struct FixUndoResult: Codable, Equatable {
    let reverted: Bool
    let filesRestored: Int
    let schemaRestored: Bool
    let paths: [String]

    enum CodingKeys: String, CodingKey {
        case reverted
        case filesRestored = "files_restored"
        case schemaRestored = "schema_restored"
        case paths
    }

    init(reverted: Bool, filesRestored: Int, schemaRestored: Bool, paths: [String]) {
        self.reverted = reverted
        self.filesRestored = filesRestored
        self.schemaRestored = schemaRestored
        self.paths = paths
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        reverted = try c.decodeIfPresent(Bool.self, forKey: .reverted) ?? false
        filesRestored = try c.decodeIfPresent(Int.self, forKey: .filesRestored) ?? 0
        schemaRestored = try c.decodeIfPresent(Bool.self, forKey: .schemaRestored) ?? false
        paths = try c.decodeIfPresent([String].self, forKey: .paths) ?? []
    }
}

struct PolishBacklinkChange: Codable, Identifiable, Equatable {
    var id: String { path }
    let path: String
    let original: String
    let polished: String
    let changes: [String]
    let applied: Bool

    enum CodingKeys: String, CodingKey {
        case path, original, polished, changes, applied
    }

    init(path: String, original: String, polished: String, changes: [String] = [], applied: Bool = false) {
        self.path = path
        self.original = original
        self.polished = polished
        self.changes = changes
        self.applied = applied
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        path = try c.decode(String.self, forKey: .path)
        original = try c.decodeIfPresent(String.self, forKey: .original) ?? ""
        polished = try c.decodeIfPresent(String.self, forKey: .polished) ?? ""
        changes = try c.decodeIfPresent([String].self, forKey: .changes) ?? []
        applied = try c.decodeIfPresent(Bool.self, forKey: .applied) ?? false
    }
}

/// Decodes the JSON emitted by `2nb repair-links` (and `2nb polish`), which
/// share the Go `cli.PolishResult` wire type. `original`/`polished` drive the
/// diff preview; `linksRepaired`/`linksSkipped` summarize what happened.
struct PolishResult: Codable {
    let path: String
    let original: String
    let polished: String
    let provider: String
    let model: String
    let durationMs: Int
    let linksAdded: [String]?
    let linksRepaired: [RepairLinkRepair]?
    let linksSkipped: [RepairLinkRepair]?
    let warning: String?
    let goal: String?
    let frontmatterFixes: [FixFieldChange]?
    let schemaRepaired: Bool?
    let backlinkPreviews: [PolishBacklinkChange]?
    let backlinksApplied: Int?
    let undoBatchAvailable: Bool?

    enum CodingKeys: String, CodingKey {
        case path, original, polished, provider, model, warning, goal
        case durationMs = "duration_ms"
        case linksAdded = "links_added"
        case linksRepaired = "links_repaired"
        case linksSkipped = "links_skipped"
        case frontmatterFixes = "frontmatter_fixes"
        case schemaRepaired = "schema_repaired"
        case backlinkPreviews = "backlink_previews"
        case backlinksApplied = "backlinks_applied"
        case undoBatchAvailable = "undo_batch_available"
    }

    init(
        path: String,
        original: String,
        polished: String,
        provider: String,
        model: String,
        durationMs: Int,
        linksAdded: [String]? = nil,
        linksRepaired: [RepairLinkRepair]? = nil,
        linksSkipped: [RepairLinkRepair]? = nil,
        warning: String? = nil,
        goal: String? = nil,
        frontmatterFixes: [FixFieldChange]? = nil,
        schemaRepaired: Bool? = nil,
        backlinkPreviews: [PolishBacklinkChange]? = nil,
        backlinksApplied: Int? = nil,
        undoBatchAvailable: Bool? = nil
    ) {
        self.path = path
        self.original = original
        self.polished = polished
        self.provider = provider
        self.model = model
        self.durationMs = durationMs
        self.linksAdded = linksAdded
        self.linksRepaired = linksRepaired
        self.linksSkipped = linksSkipped
        self.warning = warning
        self.goal = goal
        self.frontmatterFixes = frontmatterFixes
        self.schemaRepaired = schemaRepaired
        self.backlinkPreviews = backlinkPreviews
        self.backlinksApplied = backlinksApplied
        self.undoBatchAvailable = undoBatchAvailable
    }
}

/// One broken `[[wikilink]]` the repair acted on (`newTarget` set) or left alone
/// (`reason` = "no_match" | "ambiguous"). Mirrors Go `polish.LinkRepair`.
struct RepairLinkRepair: Codable, Identifiable {
    var id: String { "\(raw)|\(newTarget ?? "")|\(reason ?? "")" }
    let raw: String
    let newTarget: String?
    let reason: String?

    enum CodingKeys: String, CodingKey {
        case raw
        case newTarget = "new_target"
        case reason
    }
}

/// One ranked "did you mean?" candidate from `2nb suggest-target`. Mirrors the
/// Go `cli.SuggestLinkResult`.
struct SuggestTargetResult: Codable, Identifiable, Equatable {
    var id: String { path }
    let path: String
    let title: String
    let score: Double
    let snippet: String
    /// How likely this candidate is THE note the broken target meant:
    /// "high" | "medium" | "low". Set only by `suggest-target` (nil on a
    /// pre-confidence CLI and on `suggest-links`). Only "high" is safe for
    /// one-click / Fix-all; medium/low are recommendations (top 2-3).
    let confidence: String?
    /// Short explanation from `suggest-target --llm` re-rank (nil when the
    /// deterministic tiers alone produced the candidate).
    let reason: String?

    /// Display name: the frontmatter title if set, else the basename (a note
    /// can lack a title, in which case the Go side sends "").
    var displayTitle: String {
        if !title.isEmpty { return title }
        return (path as NSString).lastPathComponent
    }

    init(path: String, title: String, score: Double, snippet: String, confidence: String? = nil, reason: String? = nil) {
        self.path = path
        self.title = title
        self.score = score
        self.snippet = snippet
        self.confidence = confidence
        self.reason = reason
    }
}

/// The `2nb suggest-target --verdict` envelope: one machine recommendation per
/// broken link plus the ranked candidates and the LLM tier's outcome. Mirrors
/// the Go `cli.SuggestTargetEnvelope`.
struct SuggestVerdictEnvelope: Codable, Equatable {
    let recommendation: SuggestRecommendation
    /// What the LLM tier did: "off" | "skipped" | "promoted" | "declined" |
    /// "error". "declined" is the model's explicit "no candidate is plausible"
    /// — the strongest removal signal (measured trustworthy, see
    /// docs/link-prompt-eval.md).
    let llm: String
    let candidates: [SuggestTargetResult]
}

/// The single machine recommendation for one broken link. "relink" repoints it
/// at `to`; "unlink" removes the link and keeps the text. Create-the-note is
/// never machine-recommended (a user choice the sheet offers alongside).
struct SuggestRecommendation: Codable, Equatable {
    let action: String // "relink" | "unlink"
    let to: String?
    let title: String?
    let confidence: String?
    let reason: String?
}

/// The note created by `2nb create --json` (`{id, path, title, type}`).
struct CreateResult: Codable {
    let id: String
    let path: String
    let title: String
    let type: String
}

// MARK: - AI Setup Wizard Types

struct AIProbeResult: Codable, Sendable {
    let modelID: String
    let provider: String
    let modelType: String
    let ok: Bool
    let detail: String?
    let latency: String
    // Classified failure code + remediation hint from the CLI's probe
    // taxonomy (models test --json: code/remediation). Optional: absent on
    // passes and on pre-taxonomy CLIs.
    let errorCode: String?
    let remediation: String?
    // The model's resolved invoke strategy (e.g. bedrock_mantle_responses).
    // Lets access guidance suppress the Bedrock console link for mantle
    // models. Optional: absent on pre-strategy CLIs.
    let invokeStrategy: String?

    enum CodingKeys: String, CodingKey {
        case modelID = "model_id"
        case provider
        case modelType = "type"
        case ok, detail, latency, remediation
        case errorCode = "code"
        case invokeStrategy = "invoke_strategy"
    }
}

/// One note the incremental reindex could not index, with the CLI's own reason.
/// Equatable so a view can diff it and a test can compare it.
struct IndexingProblem: Equatable {
    let path: String
    let message: String
}

enum CLIError: LocalizedError {
    case noVault
    /// A `2nb` invocation exited non-zero. Carries the trimmed stderr so the
    /// real reason (e.g. "bedrock not ready: AccessDeniedException…") reaches
    /// the user instead of a bare exit code.
    case nonZeroExit(Int32, message: String)
    case indexRebuildInProgress

    var errorDescription: String? {
        switch self {
        case .noVault: return "No vault is open"
        case .nonZeroExit(let code, let message):
            let trimmed = message.trimmingCharacters(in: .whitespacesAndNewlines)
            return trimmed.isEmpty ? "CLI exited with code \(code)" : trimmed
        case .indexRebuildInProgress: return "Index rebuild is in progress; wait for it to finish before changing the active model"
        }
    }
}

struct FileItem: Identifiable {
    let id = UUID()
    let url: URL
    let name: String
    let relativePath: String
}

// MARK: - Local Gemma & Engine Types

/// Progress state for a local-model download (`ai engine pull --json`).
struct LocalModelDownload {
    let models: [String]
    var currentModel: String = ""
    var done: Int64 = 0
    var total: Int64 = 0
    var completed: [String] = []
    var failed = false
    var error: String?
    var finished = false
    /// Fraction of the CURRENT model (the CLI reports per-file, not aggregate).
    var fraction: Double { total > 0 ? min(1, max(0, Double(done) / Double(total))) : 0 }
}

/// One line-delimited event from `ai engine pull --json`.
struct PullEvent: Codable, Sendable {
    let model: String
    let status: String
    let done: Int64?
    let total: Int64?
    let path: String?
    let error: String?
}

/// Buffers a byte stream and yields decoded `PullEvent`s per complete JSONL line.
/// Thread-safe accumulator with NSLock protecting the Data buffer.
final class PullEventStream: @unchecked Sendable {
    private let lock = NSLock()
    private var data = Data()
    private let decoder = JSONDecoder()

    func ingest(_ chunk: Data) -> [PullEvent] {
        lock.lock()
        defer { lock.unlock() }
        data.append(chunk)
        var events: [PullEvent] = []
        while let nl = data.firstIndex(of: 0x0A) {
            let line = data.subdata(in: data.startIndex..<nl)
            data.removeSubrange(data.startIndex...nl)
            if let ev = try? decoder.decode(PullEvent.self, from: line) {
                events.append(ev)
            }
        }
        return events
    }

    /// Flushes any remaining un-newline-terminated JSON line in buffer upon stream completion.
    func flush() -> [PullEvent] {
        lock.lock()
        defer { lock.unlock() }
        var events: [PullEvent] = []
        while let nl = data.firstIndex(of: 0x0A) {
            let line = data.subdata(in: data.startIndex..<nl)
            data.removeSubrange(data.startIndex...nl)
            if let ev = try? decoder.decode(PullEvent.self, from: line) {
                events.append(ev)
            }
        }
        if !data.isEmpty {
            let remaining = data
            data.removeAll()
            if let str = String(data: remaining, encoding: .utf8)?.trimmingCharacters(in: .whitespacesAndNewlines),
               !str.isEmpty,
               let trimmedData = str.data(using: .utf8),
               let ev = try? decoder.decode(PullEvent.self, from: trimmedData) {
                events.append(ev)
            }
        }
        return events
    }
}

/// Status of an individual supervised `llama-server` role.
struct RoleStatus: Codable, Sendable, Equatable, Identifiable {
    var id: String { "\(role)-\(port)-\(pid)" }
    let role: String
    let pid: Int
    let port: Int
    let model: String
    let startedAt: String?
    let healthy: Bool

    enum CodingKeys: String, CodingKey {
        case role, pid, port, model, healthy
        case startedAt = "started_at"
    }
}

/// Aggregated status of the local `llama-server` engine daemon (`2nb ai engine status --json`).
struct EngineStatus: Codable, Sendable, Equatable {
    let enginePath: String
    let agentLoaded: Bool
    let roles: [RoleStatus]?

    enum CodingKeys: String, CodingKey {
        case enginePath = "engine_path"
        case agentLoaded = "agent_loaded"
        case roles
    }

    var isRunning: Bool {
        agentLoaded || (roles?.contains(where: { $0.pid > 0 }) ?? false)
    }

    var isHealthy: Bool {
        guard let roles, !roles.isEmpty else { return false }
        return roles.allSatisfy { $0.healthy }
    }

    var primaryPid: Int32? {
        if let pid = roles?.first?.pid {
            return Int32(pid)
        }
        return nil
    }
}
