import SwiftUI
import SecondBrainCore
import os

private let log = Logger(subsystem: "dev.apresai.2ndbrain", category: "app")

@Observable @MainActor
final class AppState {
    var vault: VaultManager?
    var database: DatabaseManager?
    var fileWatcher: FSEventsWatcher?
    var globalCatalogWatcher: FSEventsWatcher?

    // Bumped when models.yaml changes on disk — in the vault or globally.
    // AISetupWizardView and any future "Manage Models" panel observe this
    // counter and reload their cached model list on bump, so a CLI edit
    // (`2nb models enable/disable`, `2nb models wizard`) is reflected in
    // the running GUI without a restart.
    var modelsCatalogVersion: Int = 0

    // Document tabs
    var openDocuments: [DocumentTab] = []
    var activeTabIndex: Int = 0

    // AI Hub is the single merged sheet for provider control, active
    // model selection, and the model catalog. Replaces three earlier
    // flags (showAISetupWizard, showModelWizard, showAITest), kept
    // here as computed aliases so existing callers (Command Palette,
    // status-bar popover, Vault Status, etc.) don't all need updates
    // in this commit — they all open the same sheet regardless.
    var showAIHub = false
    var showAISetupWizard: Bool {
        get { showAIHub }
        set { showAIHub = newValue }
    }
    var showModelWizard: Bool {
        get { showAIHub }
        set { showAIHub = newValue }
    }
    var editorFontSize: CGFloat = UserDefaults.standard.object(forKey: "editorFontSize") as? CGFloat ?? 14
    var editorFontFamily: String = UserDefaults.standard.string(forKey: "editorFontFamily") ?? "System Mono"

    // Autosave
    var autosaveIntervalSeconds: Int = UserDefaults.standard.object(forKey: "autosaveIntervalSeconds") as? Int ?? 30
    private var autosaveTimer: Timer?
    private static let lowDiskThreshold: Int64 = 50 * 1024 * 1024

    // Merge conflict controllers (one per active dialog, retained so the NSWindow stays alive)
    private var mergeControllers: [MergeConflictController] = []
    private let selfWriteSuppressionInterval: TimeInterval = 2
    private var recentSelfWrites: [String: Date] = [:]

    // In-flight incremental re-embed tasks keyed by vault-relative path, so a
    // second save for the same file queues behind the first instead of
    // racing the database.
    private var reindexTasks: [String: Task<Void, Never>] = [:]

    // Current document metrics (chunk count is refreshed on open/switch/reindex;
    // token estimate is computed live from content.count / 4)
    var currentDocumentChunkCount: Int = 0

    // Git state
    var vaultIsGitRepo: Bool = false
    var gitFileStatus: [String: String] = [:]
    var gitActivity: [GitChangeInfo] = []
    var gitActivityLoading: Bool = false
    var gitActivityDays: Int = UserDefaults.standard.object(forKey: "gitActivityDays") as? Int ?? 7
    var showGitActivity: Bool = false

    // AI state
    var aiStatus: AIStatusInfo?
    // Installed `2nb` CLI version ("X.Y.Z"), or nil if it couldn't be read.
    // Home compares this against `appVersion` to warn on a stale CLI — see
    // CLIVersion and refreshCLIVersion().
    var cliVersion: String?
    // showAITest is kept as an alias for the AI Hub so the status bar
    // AI popover and Vault Status's "Test Connection" button open the
    // same unified sheet. Retiring the name entirely would force those
    // call sites to change too; the alias keeps them working.
    var showAITest: Bool {
        get { showAIHub }
        set { showAIHub = newValue }
    }

    // Portability warnings from the most recent CLI search/ask. When
    // non-empty, the vault is in a degraded state (dimension mismatch,
    // provider unavailable, model mismatch, etc.) and views should show
    // a banner explaining why. Cleared on the next successful run.
    var lastSemanticWarnings: [String] = []
    var isIndexing = false
    var indexError: String?
    var embeddingProgress: EmbeddingProgress?
    var indexProgress: IndexProgress?
    var showIndexProgress = false

    // Vault Status panel
    var showVaultStatus: Bool = false

    // Tools panels
    var showLintResults = false
    var isLinting = false
    var lintReport: LintReport?
    var showMCPSetup = false
    var mcpSetupText: String?
    var showMCPStatus = false
    var mcpStatuses: [MCPServerStatusInfo] = []
    private var mcpStatusTimer: Timer?
    var spotlightIndexer: SpotlightIndexer?
    var crashJournal: CrashJournal?
    var errorLogger: ErrorLogger?
    var recoveryEntries: [RecoveryEntry] = []
    var showRecoveryDialog = false
    var files: [FileItem] = []
    var outline: [HeadingItem] = []

    // Editor navigation — set by outline clicks, search-result opens, lint
    // issue clicks, etc. The EditorArea.updateNSView reads this on every
    // cycle, resolves it to an NSRange, scrolls + flashes, then clears it.
    var editorScrollTarget: EditorScrollTarget?

    // Request that the editor's NSTextView become first responder on the
    // next updateNSView tick. Set by openDocument so focus lands in the
    // editor after opening a file.
    var pendingFirstResponder: Bool = false

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

    // Invoked from outline row clicks. If the editor is currently in
    // preview-only mode, switching back to source mode is the caller's
    // responsibility (Phase A default: we stay in whatever mode is active
    // and scroll the NSTextView; the EditorArea auto-switches to source
    // if needed via editorMode binding).
    func jumpToHeading(_ heading: HeadingItem) {
        editorScrollTarget = .heading(heading)
        log.info("jumpToHeading: level=\(heading.level) text=\(heading.text)")
    }

    func jumpToHeadingPath(_ path: String) {
        editorScrollTarget = .headingPath(path)
        log.info("jumpToHeadingPath: \(path)")
    }

    func jumpToLine(_ line: Int) {
        editorScrollTarget = .line(line)
        log.info("jumpToLine: \(line)")
    }

    var validTabIndex: Int? {
        let idx = activeTabIndex
        guard idx >= 0, idx < openDocuments.count else { return nil }
        return idx
    }

    var currentDocument: DocumentTab? {
        guard let idx = validTabIndex else { return nil }
        return openDocuments[idx]
    }

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
        openVault(at: url)
        UserDefaults.standard.set(url.path, forKey: "lastVaultPath")
        return true
    }

    func openVault(at url: URL) {
        log.info("Opening vault at \(url.path)")
        let vm = VaultManager(rootURL: url)
        self.vault = vm

        // Open the shared SQLite index (same DB the Go CLI writes to)
        if vm.isInitialized {
            do {
                self.database = try DatabaseManager(path: vm.indexDBPath)
                log.info("Database opened: \(vm.indexDBPath)")
            } catch {
                log.error("Failed to open index: \(error.localizedDescription)")
                errorLogger?.log("Failed to open index", error: error)
            }
            // Point the CLI's shared active-vault pointer at this vault so a
            // bare `2nb ask`/`search` in the terminal resolves to the same vault
            // the dashboard is bound to. The app pins --vault on its own calls
            // and otherwise never writes the pointer, so the two could diverge.
            syncCLIActiveVault(to: url.path)
        } else {
            log.notice("Vault not initialized (no .2ndbrain dir): \(url.path)")
        }

        // Initialize Spotlight indexer and index vault
        let indexer = SpotlightIndexer()
        self.spotlightIndexer = indexer
        indexer.indexAll(vault: vm)

        // Initialize crash recovery and error logging
        let logger = ErrorLogger(vaultDotDir: vm.dotDirURL)
        let journal = CrashJournal(vaultDotDir: vm.dotDirURL) { msg in
            logger.log(msg, error: nil)
        }
        self.crashJournal = journal
        self.errorLogger = logger

        // Check for crash recovery entries
        let entries = journal.recoverableDocuments()
        if !entries.isEmpty {
            self.recoveryEntries = entries
            self.showRecoveryDialog = true
        }

        // Refresh file list
        refreshFiles()
        log.info("Vault loaded: \(self.files.count) files")

        // Refresh AI status
        Task { await refreshAIStatus() }

        // Start autosave timer for this vault
        startAutosaveTimer()

        // Start polling MCP server status (used by status bar indicator)
        startMCPStatusTimer()

        // Refresh git state (non-blocking)
        Task { await refreshGitStatus() }

        // Start watching for changes. Filter accepts .md (document changes)
        // and models.yaml (catalog changes written by the CLI wizard or
        // enable/disable commands).
        fileWatcher?.stop()
        let watcher = FSEventsWatcher(
            path: url.path,
            filter: {
                $0.hasSuffix(".md")
                    || $0.hasSuffix("/models.yaml")
                    || $0.hasSuffix("/config.yaml")
            }
        ) { @Sendable [weak self] paths in
            Task { @MainActor in
                let mdPaths = paths.filter { $0.hasSuffix(".md") }
                let catalogChanged = paths.contains { $0.hasSuffix("/models.yaml") }
                let configChanged = paths.contains { $0.hasSuffix("/config.yaml") }
                if !mdPaths.isEmpty {
                    self?.refreshFiles()
                    for path in mdPaths {
                        self?.reloadIfOpen(path: path)
                    }
                }
                if catalogChanged || configChanged {
                    // Both feed the AI Hub: catalog for model rows,
                    // config.yaml for active-model + provider-disable
                    // changes the user made via `2nb config set`.
                    self?.modelsCatalogVersion += 1
                }
                if configChanged {
                    await self?.refreshAIStatus()
                }
            }
        }
        watcher.start()
        self.fileWatcher = watcher

        // Second watcher for the user's global models catalog. Separate
        // watcher because ~/.config/2nb is outside the vault tree.
        startGlobalCatalogWatcher()
    }

    /// Best-effort: sync the CLI's shared active-vault pointer
    /// (`~/.2ndbrain-active-vault`) to the vault the dashboard just bound, so a
    /// terminal `2nb ask`/`search` with no `--vault` resolves to the same vault
    /// the GUI shows. Runs `2nb vault set`, which validates the path and also
    /// refreshes the recent-vaults list. Fire-and-forget on a background queue;
    /// failures (e.g. a vault the CLI can't open) are intentionally ignored.
    private func syncCLIActiveVault(to path: String) {
        let cliPath = CLIPath.resolve()
        DispatchQueue.global(qos: .utility).async {
            let process = Process()
            process.executableURL = URL(fileURLWithPath: cliPath)
            process.arguments = ["vault", "set", path]
            // Discard output to /dev/null rather than a Pipe — nothing reads it,
            // and an undrained pipe could (in theory) deadlock waitUntilExit on
            // large output. `vault set` prints one short line, but null is safe.
            process.standardOutput = FileHandle.nullDevice
            process.standardError = FileHandle.nullDevice
            try? process.run()
            process.waitUntilExit()
        }
    }

    /// Watches ~/.config/2nb (or $XDG_CONFIG_HOME/2nb) for models.yaml
    /// writes made by the CLI while the GUI is running. Idempotent — safe
    /// to call multiple times; the previous watcher is stopped first.
    private func startGlobalCatalogWatcher() {
        globalCatalogWatcher?.stop()

        let configRoot: String
        if let xdg = ProcessInfo.processInfo.environment["XDG_CONFIG_HOME"], !xdg.isEmpty {
            configRoot = (xdg as NSString).appendingPathComponent("2nb")
        } else {
            configRoot = (NSHomeDirectory() as NSString).appendingPathComponent(".config/2nb")
        }

        // Ensure the directory exists — FSEventStreamCreate silently
        // fails on a non-existent path, which would leave the watcher
        // inert with no warning. A missing directory is the common case
        // for users who've never run `2nb config set`.
        try? FileManager.default.createDirectory(
            atPath: configRoot,
            withIntermediateDirectories: true
        )

        let watcher = FSEventsWatcher(
            path: configRoot,
            filter: { $0.hasSuffix("/models.yaml") }
        ) { @Sendable [weak self] _ in
            Task { @MainActor in
                self?.modelsCatalogVersion += 1
            }
        }
        watcher.start()
        self.globalCatalogWatcher = watcher
    }

    func refreshFiles() {
        guard let vault else { return }
        let mdFiles = vault.listMarkdownFiles()
        var items = mdFiles.map { url in
            FileItem(
                url: url,
                name: url.deletingPathExtension().lastPathComponent,
                relativePath: vault.relativePath(for: url)
            )
        }

        self.files = items
    }

    // MARK: - Font Size

    func increaseFontSize() {
        editorFontSize = min(editorFontSize + 1, 32)
        UserDefaults.standard.set(editorFontSize, forKey: "editorFontSize")
    }

    func decreaseFontSize() {
        editorFontSize = max(editorFontSize - 1, 10)
        UserDefaults.standard.set(editorFontSize, forKey: "editorFontSize")
    }

    func resetFontSize() {
        editorFontSize = 14
        UserDefaults.standard.set(editorFontSize, forKey: "editorFontSize")
    }

    func setFontFamily(_ family: String) {
        editorFontFamily = family
        UserDefaults.standard.set(family, forKey: "editorFontFamily")
    }

    /// Resolve the stored font family name to an NSFont for the editor.
    func resolvedEditorFont(size: CGFloat? = nil) -> NSFont {
        let sz = size ?? editorFontSize
        switch editorFontFamily {
        case "System Mono":
            return NSFont.monospacedSystemFont(ofSize: sz, weight: .regular)
        default:
            return NSFont(name: editorFontFamily, size: sz)
                ?? NSFont.monospacedSystemFont(ofSize: sz, weight: .regular)
        }
    }

    func openDocument(at url: URL) {
        // Check if already open
        if let idx = openDocuments.firstIndex(where: { $0.url == url }) {
            activeTabIndex = idx
            pendingFirstResponder = true
            return
        }

        // Big-file guard: anything >100 MB gets an explicit warning + read-only
        // fallback. This sits before the FrontmatterParser load so a huge file
        // with malformed frontmatter doesn't block the user choice.
        var forceReadOnly = false
        if let attrs = try? FileManager.default.attributesOfItem(atPath: url.path),
           let size = attrs[.size] as? Int64, size > 100 * 1024 * 1024 {
            let alert = NSAlert()
            alert.messageText = "Large file"
            alert.informativeText = "\(url.lastPathComponent) is \(Self.formatBytes(size)). Opening it may be slow. It will open in read-only mode to protect your data."
            alert.alertStyle = .warning
            alert.addButton(withTitle: "Cancel")
            alert.addButton(withTitle: "Open Read-Only")
            if alert.runModal() == .alertFirstButtonReturn {
                return
            }
            forceReadOnly = true
        }

        do {
            let doc = try FrontmatterParser.loadDocument(from: url)
            let content = try String(contentsOf: url, encoding: .utf8)
            var tab = DocumentTab(url: url, document: doc, content: content)
            tab.lastSavedContent = content
            tab.readOnly = forceReadOnly
            openDocuments.append(tab)
            activeTabIndex = openDocuments.count - 1
            updateOutline()
            // Request focus into the editor so typing starts immediately
            // without an extra click. EditorArea.updateNSView reads this
            // flag on the next cycle and calls makeFirstResponder.
            pendingFirstResponder = true
            log.info("Opened document: \(url.lastPathComponent) (type=\(doc.docType), id=\(doc.id.prefix(8))\(forceReadOnly ? ", read-only" : ""))")
        } catch {
            // Parse failed — the frontmatter is likely corrupt. Surface any
            // available recovery snapshots so the user can restore rather than
            // losing work. We can't target a specific snapshot because we can't
            // recover the document ID from an unparseable file, so we show the
            // whole queue and let the user pick.
            log.error("Failed to open document \(url.lastPathComponent): \(error.localizedDescription)")
            errorLogger?.log("Failed to open document", error: error)
            if let journal = crashJournal {
                let entries = journal.recoverableDocuments()
                if !entries.isEmpty {
                    recoveryEntries = entries
                    showRecoveryDialog = true
                    return
                }
            }
            let alert = NSAlert()
            alert.messageText = "Unable to open \(url.lastPathComponent)"
            alert.informativeText = "The file's frontmatter could not be parsed: \(error.localizedDescription)"
            alert.alertStyle = .warning
            alert.addButton(withTitle: "OK")
            alert.runModal()
        }
    }

    func closeTab(at index: Int) {
        guard index >= 0, index < openDocuments.count else { return }
        openDocuments.remove(at: index)
        if activeTabIndex >= openDocuments.count {
            activeTabIndex = max(0, openDocuments.count - 1)
        }
        updateOutline()
    }

    func closeTab(id: UUID) {
        guard let index = openDocuments.firstIndex(where: { $0.id == id }) else { return }
        closeTab(at: index)
    }

    func createNewDocument(type: String = "note", title: String = "Untitled") {
        guard let vault else { return }
        let id = UUID().uuidString
        let now = ISO8601DateFormatter().string(from: Date())

        let initialStatus: String
        let extraFields: String
        let bodyTemplate: String

        switch type {
        case "adr":
            initialStatus = "proposed"
            extraFields = ""
            bodyTemplate = """
            ## Status

            proposed

            ## Context

            What is the issue that we're seeing that is motivating this decision or change?

            ## Decision

            What is the change that we're proposing and/or doing?

            ## Consequences

            What becomes easier or more difficult to do because of this change?
            """
        case "runbook":
            initialStatus = "draft"
            extraFields = "service: \nkeyword: "
            bodyTemplate = """
            ## Overview

            Brief description of what this runbook addresses.

            ## Prerequisites

            - [ ] Access to relevant systems
            - [ ] Required permissions

            ## Steps

            1. First step
            2. Second step
            3. Third step

            ## Verification

            How to verify the procedure was successful.

            ## Rollback

            Steps to undo if something goes wrong.
            """
        case "postmortem":
            initialStatus = "draft"
            extraFields = "incident-date: \(now.prefix(10))\nseverity: medium\nservices: []"
            bodyTemplate = """
            ## Summary

            Brief summary of the incident.

            ## Timeline

            | Time | Event |
            |------|-------|
            | | Incident detected |
            | | Investigation started |
            | | Root cause identified |
            | | Fix deployed |
            | | Incident resolved |

            ## Root Cause

            What caused the incident?

            ## Impact

            Who/what was affected and for how long?

            ## Action Items

            - [ ] Action item 1
            - [ ] Action item 2

            ## Lessons Learned

            What did we learn from this incident?
            """
        case "prd":
            initialStatus = "draft"
            extraFields = "owner: \npriority: p0"
            bodyTemplate = """
            ## Problem Statement

            What problem are we solving? Who has this problem? Why does it matter?

            ## Target Users

            Who are the primary, secondary, and tertiary users?

            ## Goals

            | # | Goal | Rationale |
            |---|------|-----------|
            | 1 | | |

            ## Non-Goals

            - What are we explicitly not doing?

            ## User Stories

            - **As a** [user type], **I want** [action] **so that** [benefit]

            ## Functional Requirements

            ### P0 — MVP

            | ID | Requirement |
            |----|-------------|
            | FR-1 | |

            ## Non-Functional Requirements

            | ID | Requirement | Target |
            |----|-------------|--------|
            | NFR-1 | | |

            ## Success Metrics

            | # | Metric | How to verify |
            |---|--------|---------------|
            | 1 | | |

            ## Risks

            | Risk | Likelihood | Impact | Mitigation |
            |------|-----------|--------|------------|
            | | | | |
            """
        case "prfaq":
            initialStatus = "draft"
            extraFields = "owner: "
            bodyTemplate = """
            ## Press Release

            **FOR IMMEDIATE RELEASE**

            ### [Headline: one sentence describing the product and its key benefit]

            *[Subheadline: expand on the value proposition]*

            **[City, State]** — Today, [company] announced [product], a [brief description]. [Product] enables [target user] to [key benefit] by [how it works at a high level].

            [Problem paragraph: describe the pain point this solves]

            [Quote from a leader or stakeholder about why this matters]

            **How it works:**

            1. [Step 1]
            2. [Step 2]
            3. [Step 3]

            ---

            ## Frequently Asked Questions

            ### External FAQ (Customer Questions)

            **Q: Who is this for?**
            A:

            **Q: How does it work?**
            A:

            **Q: How much does it cost?**
            A:

            ### Internal FAQ (Engineering / Business Questions)

            **Q: Why now?**
            A:

            **Q: What's the technical approach?**
            A:

            **Q: What are the main risks?**
            A:
            """
        default: // note
            initialStatus = "draft"
            extraFields = ""
            bodyTemplate = ""
        }

        var frontmatter = """
        ---
        id: \(id)
        title: \(title)
        type: \(type)
        status: \(initialStatus)
        tags: []
        created: \(now)
        modified: \(now)
        """
        if !extraFields.isEmpty {
            frontmatter += "\n\(extraFields)"
        }
        frontmatter += "\n---"

        let content = "\(frontmatter)\n# \(title)\n\n\(bodyTemplate)\n"

        let slug = title.lowercased()
            .replacingOccurrences(of: " ", with: "-")
            .filter { $0.isLetter || $0.isNumber || $0 == "-" }
        let filename = slug.isEmpty ? "\(type)-\(id.prefix(8)).md" : "\(slug).md"
        let url = uniqueFilename(base: filename, in: vault.rootURL)

        do {
            try content.write(to: url, atomically: true, encoding: .utf8)
            refreshFiles()
            openDocument(at: url)
            log.info("Created document: \(filename) (type=\(type))")
        } catch {
            log.error("Failed to create document \(filename): \(error.localizedDescription)")
            errorLogger?.log("Failed to create document", error: error)
        }
    }

    func createVault(at url: URL) {
        do {
            try VaultManager.initializeVault(at: url)
            openVault(at: url)
            UserDefaults.standard.set(url.path, forKey: "lastVaultPath")
            log.info("Created new vault at \(url.path)")
        } catch {
            log.error("Failed to create vault at \(url.path): \(error.localizedDescription)")
            errorLogger?.log("Failed to create vault", error: error)
        }
    }

    func duplicateDocument(at url: URL) {
        guard vault != nil else { return }
        do {
            let content = try String(contentsOf: url, encoding: .utf8)
            var (frontmatter, body) = FrontmatterParser.parse(content)
            // Fresh UUID so the duplicate has its own identity in the index.
            frontmatter["id"] = UUID().uuidString.lowercased()
            let now = ISO8601DateFormatter().string(from: Date())
            frontmatter["created"] = now
            frontmatter["modified"] = now
            if let oldTitle = frontmatter["title"] as? String, !oldTitle.isEmpty {
                frontmatter["title"] = "\(oldTitle) (copy)"
            }
            let newContent = FrontmatterParser.serialize(frontmatter: frontmatter, body: body)

            let stem = url.deletingPathExtension().lastPathComponent
            let ext = url.pathExtension
            let baseCandidate = ext.isEmpty ? "\(stem)-copy" : "\(stem)-copy.\(ext)"
            let parent = url.deletingLastPathComponent()
            let target = uniqueFilename(base: baseCandidate, in: parent)

            try newContent.write(to: target, atomically: true, encoding: .utf8)
            refreshFiles()
            openDocument(at: target)
            log.info("Duplicated \(url.lastPathComponent) → \(target.lastPathComponent)")
        } catch {
            log.error("Failed to duplicate \(url.lastPathComponent): \(error.localizedDescription)")
            errorLogger?.log("Failed to duplicate document", error: error)
        }
    }

    func deleteDocument(at url: URL) {
        let name = url.lastPathComponent
        // Close tab if open
        if let idx = openDocuments.firstIndex(where: { $0.url == url }) {
            closeTab(at: idx)
        }

        // Delete file from disk
        do {
            try FileManager.default.removeItem(at: url)
            refreshFiles()
            log.info("Deleted document: \(name)")
        } catch {
            log.error("Failed to delete \(name): \(error.localizedDescription)")
            errorLogger?.log("Failed to delete document", error: error)
        }
    }

    // When true, the next startIndex() pass runs `2nb index --force-reembed`
    // to invalidate all stored embeddings. Set by VaultStatusView's Re-embed
    // button and cleared once the run begins.
    var pendingForceReembed: Bool = false

    func rebuildIndex(forceReembed: Bool = false) {
        guard vault != nil else { return }
        pendingForceReembed = forceReembed
        indexProgress = IndexProgress(phase: .ready, forceReembed: forceReembed)
        showIndexProgress = true
    }

    /// True once the current index run has reached a terminal phase. Late
    /// stderr-progress updates check this so they can't flip a finished run back
    /// to `.embedding` and re-wedge the progress sheet on "Running…".
    private var indexReachedTerminalPhase: Bool {
        indexProgress?.phase == .complete || indexProgress?.phase == .failed
    }

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
        indexProgress = IndexProgress(phase: .indexingFiles, forceReembed: forceReembed)
        log.info("Index rebuild started for vault: \(vault.rootURL.lastPathComponent) forceReembed=\(forceReembed)")
        let startTime = CFAbsoluteTimeGetCurrent()

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
                    process.executableURL = URL(fileURLWithPath: CLIPath.resolve())
                    let subArgs = forceReembed ? ["index", "--force-reembed"] : ["index"]
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

                            // Embedding progress: "embedded N/M: path"
                            let embedPattern = /[Ee]mbed\w*\s+(\d+)\/(\d+)/
                            if let match = l.firstMatch(of: embedPattern) {
                                let current = Int(match.1) ?? 0
                                let total = Int(match.2) ?? 0
                                Task { @MainActor [weak self] in
                                    // Don't let a late progress update flip a
                                    // run that already finished back to
                                    // .embedding and re-wedge the sheet.
                                    guard let self, !self.indexReachedTerminalPhase else { return }
                                    self.embeddingProgress = EmbeddingProgress(current: current, total: total)
                                    self.indexProgress?.phase = .embedding
                                    self.indexProgress?.embeddingCurrent = current
                                    self.indexProgress?.embeddingTotal = total
                                }
                                continue
                            }

                            // Embedding count: "embedding N documents..."
                            let embedCountPattern = /embedding\s+(\d+)\s+documents/
                            if let match = l.firstMatch(of: embedCountPattern) {
                                let total = Int(match.1) ?? 0
                                Task { @MainActor [weak self] in
                                    guard let self, !self.indexReachedTerminalPhase else { return }
                                    self.indexProgress?.phase = .embedding
                                    self.indexProgress?.embeddingTotal = total
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
                        continuation.resume(throwing: error)
                    }
                }

                let elapsed = CFAbsoluteTimeGetCurrent() - startTime

                // Parse stdout summary: "Indexed N files, N chunks, N links"
                let statsPattern = /Indexed\s+(\d+)\s+files?,\s+(\d+)\s+chunks?,\s+(\d+)\s+links?/
                if let match = result.stdout.firstMatch(of: statsPattern) {
                    indexProgress?.docsIndexed = Int(match.1) ?? 0
                    indexProgress?.chunksCreated = Int(match.2) ?? 0
                    indexProgress?.linksFound = Int(match.3) ?? 0
                }

                indexProgress?.elapsed = elapsed
                if result.exitCode == 0 {
                    indexProgress?.phase = .complete
                    log.info("Index rebuild completed in \(String(format: "%.1f", elapsed))s (exit 0)")
                } else {
                    // Surface the actual CLI failure (last stderr line) rather
                    // than a bare exit code. Only the error line goes to the
                    // system-wide unified log as .public — the full stderr
                    // (which includes every indexed note's path) is kept in the
                    // per-vault ErrorLogger file, not broadcast system-wide.
                    let detail = result.stderr.trimmingCharacters(in: .whitespacesAndNewlines)
                    let lastLine = detail.split(separator: "\n").last.map(String.init) ?? ""
                    indexProgress?.phase = .failed
                    indexProgress?.error = lastLine.isEmpty ? "CLI exited with code \(result.exitCode)" : lastLine
                    log.warning("Index rebuild finished with exit code \(result.exitCode) in \(String(format: "%.1f", elapsed))s: \(lastLine, privacy: .public)")
                    errorLogger?.log("Index rebuild exited \(result.exitCode): \(detail)")
                }

                // Reopen the database to pick up changes. A reopen failure does
                // not undo a successful index, so log it without overwriting the
                // completion phase.
                do {
                    self.database = try DatabaseManager(path: vault.indexDBPath)
                } catch {
                    log.error("Failed to reopen index database after rebuild: \(error.localizedDescription)")
                    errorLogger?.log("Failed to reopen index database after rebuild", error: error)
                }
            } catch {
                // Launch failure (process never started).
                let elapsed = CFAbsoluteTimeGetCurrent() - startTime
                log.error("Index rebuild failed to launch after \(String(format: "%.1f", elapsed))s: \(error.localizedDescription)")
                errorLogger?.log("Failed to rebuild index", error: error)
                indexError = error.localizedDescription
                indexProgress?.phase = .failed
                indexProgress?.error = error.localizedDescription
                indexProgress?.elapsed = elapsed
            }

            isIndexing = false
            embeddingProgress = nil
            await refreshAIStatus()
        }
    }

    func saveCurrentDocument() {
        let idx = activeTabIndex
        guard idx >= 0, idx < openDocuments.count else { return }
        _ = performSave(tabIdx: idx, isAutosave: false)
    }

    func saveSnapshotForCurrentDocument() {
        guard let tab = currentDocument else { return }
        crashJournal?.saveSnapshot(documentID: tab.document.id, content: tab.content)
    }

    /// Core save path used by both manual save and autosave.
    /// Returns true on success. When isAutosave is true, skips the low-disk modal
    /// (which would block the UI unexpectedly on a background timer tick).
    @discardableResult
    private func performSave(tabIdx: Int, isAutosave: Bool) -> Bool {
        guard tabIdx >= 0, tabIdx < openDocuments.count else { return false }
        let tab = openDocuments[tabIdx]

        if tab.hasExternalConflict {
            if !isAutosave {
                let alert = NSAlert()
                alert.messageText = "Unresolved merge conflict"
                alert.informativeText = "\(tab.url.lastPathComponent) has unresolved external changes. Resolve the conflict before saving."
                alert.alertStyle = .warning
                alert.addButton(withTitle: "OK")
                alert.runModal()
            }
            return false
        }

        if !isAutosave, let avail = availableDiskSpace(at: tab.url), avail < Self.lowDiskThreshold {
            let alert = NSAlert()
            alert.messageText = "Low disk space"
            alert.informativeText = "Only \(Self.formatBytes(avail)) free on this volume. Saving may fail."
            alert.alertStyle = .warning
            alert.addButton(withTitle: "Cancel")
            alert.addButton(withTitle: "Save Anyway")
            if alert.runModal() == .alertFirstButtonReturn {
                return false
            }
        }

        do {
            try crashJournal?.savePreWriteSnapshot(documentID: tab.document.id, content: tab.content)
        } catch {
            log.warning("Pre-write snapshot failed for \(tab.url.lastPathComponent): \(error.localizedDescription)")
        }

        do {
            try tab.content.write(to: tab.url, atomically: true, encoding: .utf8)
            openDocuments[tabIdx].isDirty = false
            openDocuments[tabIdx].lastSavedContent = tab.content
            markSelfWrite(path: tab.url.path)
            crashJournal?.clearSnapshotSync(documentID: tab.document.id)
            log.debug("\(isAutosave ? "Autosaved" : "Saved"): \(tab.url.lastPathComponent)")
            triggerIncrementalReindex(for: tab.url)
            if vaultIsGitRepo {
                Task { await refreshGitStatus() }
            }
            return true
        } catch {
            log.error("\(isAutosave ? "Autosave" : "Save") failed for \(tab.url.lastPathComponent): \(error.localizedDescription)")
            errorLogger?.log("Failed to save document", error: error)
            return false
        }
    }

    // MARK: - Autosave

    func startAutosaveTimer() {
        autosaveTimer?.invalidate()
        autosaveTimer = nil
        guard autosaveIntervalSeconds > 0 else { return }
        let interval = TimeInterval(autosaveIntervalSeconds)
        autosaveTimer = Timer.scheduledTimer(withTimeInterval: interval, repeats: true) { [weak self] _ in
            Task { @MainActor in
                self?.runAutosave()
            }
        }
    }

    func setAutosaveInterval(_ seconds: Int) {
        autosaveIntervalSeconds = seconds
        UserDefaults.standard.set(seconds, forKey: "autosaveIntervalSeconds")
        startAutosaveTimer()
    }

    private func runAutosave() {
        for idx in openDocuments.indices where openDocuments[idx].isDirty {
            performSave(tabIdx: idx, isAutosave: true)
        }
    }

    // MARK: - Filesystem Helpers

    private func availableDiskSpace(at url: URL) -> Int64? {
        let dir = url.deletingLastPathComponent()
        guard let values = try? dir.resourceValues(forKeys: [.volumeAvailableCapacityForImportantUsageKey]) else {
            return nil
        }
        return values.volumeAvailableCapacityForImportantUsage
    }

    private static func formatBytes(_ bytes: Int64) -> String {
        let formatter = ByteCountFormatter()
        formatter.allowedUnits = [.useMB, .useGB]
        formatter.countStyle = .file
        return formatter.string(fromByteCount: bytes)
    }

    private func markSelfWrite(path: String) {
        recentSelfWrites[path] = Date().addingTimeInterval(selfWriteSuppressionInterval)
    }

    private func shouldSuppressSelfWrite(path: String, diskContent: String, tab: DocumentTab) -> Bool {
        let now = Date()
        recentSelfWrites = recentSelfWrites.filter { $0.value > now }
        guard let expiresAt = recentSelfWrites[path], expiresAt > now else {
            return false
        }
        if diskContent == tab.lastSavedContent || diskContent == tab.content {
            recentSelfWrites.removeValue(forKey: path)
            return true
        }
        return false
    }

    private func uniqueFilename(base: String, in dir: URL) -> URL {
        let baseURL = dir.appendingPathComponent(base)
        if !FileManager.default.fileExists(atPath: baseURL.path) {
            return baseURL
        }
        let stem = (base as NSString).deletingPathExtension
        let ext = (base as NSString).pathExtension
        for counter in 1..<1000 {
            let candidate = ext.isEmpty ? "\(stem)-\(counter)" : "\(stem)-\(counter).\(ext)"
            let candidateURL = dir.appendingPathComponent(candidate)
            if !FileManager.default.fileExists(atPath: candidateURL.path) {
                return candidateURL
            }
        }
        let suffix = String(UUID().uuidString.prefix(8))
        let fallback = ext.isEmpty ? "\(stem)-\(suffix)" : "\(stem)-\(suffix).\(ext)"
        return dir.appendingPathComponent(fallback)
    }

    func updateBodyOfCurrentDocument(_ newBody: String) {
        guard let idx = validTabIndex else { return }
        let (frontmatter, _) = FrontmatterParser.parse(openDocuments[idx].content)
        openDocuments[idx].content = FrontmatterParser.serialize(frontmatter: frontmatter, body: newBody)
        openDocuments[idx].isDirty = true
    }

    func recoverDocument(_ entry: RecoveryEntry) {
        // Write recovered content to the vault
        guard let vault else { return }
        // Try to find the original file
        let files = vault.listMarkdownFiles()
        for file in files {
            if let doc = try? FrontmatterParser.loadDocument(from: file), doc.id == entry.documentID {
                try? entry.content.write(to: file, atomically: true, encoding: .utf8)
                openDocument(at: file)
                crashJournal?.clearSnapshot(documentID: entry.documentID)
                log.info("Recovered document \(entry.documentID.prefix(8)) to \(file.lastPathComponent)")
                return
            }
        }
        // If original not found, create a new file
        let recoveredPath = vault.rootURL.appendingPathComponent("recovered-\(entry.documentID.prefix(8)).md")
        try? entry.content.write(to: recoveredPath, atomically: true, encoding: .utf8)
        refreshFiles()
        openDocument(at: recoveredPath)
        crashJournal?.clearSnapshot(documentID: entry.documentID)
        log.notice("Recovered document \(entry.documentID.prefix(8)) to new file (original not found)")
    }

    func dismissRecovery() {
        crashJournal?.clearAll()
        recoveryEntries = []
        showRecoveryDialog = false
    }

    func updateOutline() {
        guard let tab = currentDocument else {
            outline = []
            currentDocumentChunkCount = 0
            return
        }
        // Extract body (skip frontmatter)
        let (_, body) = FrontmatterParser.parse(tab.content)
        outline = MarkdownRenderer.extractOutline(body)
        refreshChunkCount()
    }

    func refreshChunkCount() {
        guard let tab = currentDocument, let db = database else {
            currentDocumentChunkCount = 0
            return
        }
        currentDocumentChunkCount = (try? db.chunkCount(forDocID: tab.document.id)) ?? 0
    }

    func reindexSpotlight() {
        guard let vault, let indexer = spotlightIndexer else { return }
        indexer.clearAll()
        indexer.indexAll(vault: vault)
    }

    private func reloadIfOpen(path: String) {
        guard let idx = openDocuments.firstIndex(where: { $0.url.path == path }) else { return }
        guard let diskContent = try? String(contentsOfFile: path, encoding: .utf8) else {
            // File vanished or unreadable — leave the tab alone for now. External
            // deletion handling is deferred; the user can still save to recreate it.
            return
        }
        let tab = openDocuments[idx]
        if shouldSuppressSelfWrite(path: path, diskContent: diskContent, tab: tab) {
            return
        }
        // No external change if disk matches what we last saw.
        if diskContent == tab.lastSavedContent {
            return
        }
        // External change detected.
        if !tab.isDirty {
            // Safe to reload silently — editor has no unsaved work.
            openDocuments[idx].content = diskContent
            openDocuments[idx].lastSavedContent = diskContent
            return
        }
        // Conflict: editor is dirty and disk changed under us.
        if tab.hasExternalConflict {
            return // already prompting the user
        }
        openDocuments[idx].hasExternalConflict = true
        let filename = tab.url.lastPathComponent
        let ancestor = tab.lastSavedContent
        let mine = tab.content
        let tabID = tab.id
        let controller = MergeConflictController()
        mergeControllers.append(controller)
        controller.present(
            filename: filename,
            theirs: diskContent,
            mine: mine,
            ancestor: ancestor
        ) { [weak self] resolution in
            guard let self else { return }
            self.handleMergeResolution(tabID: tabID, diskContent: diskContent, resolution: resolution)
            self.mergeControllers.removeAll { $0 === controller }
        }
    }

    private func handleMergeResolution(tabID: UUID, diskContent: String, resolution: MergeConflictResolution) {
        guard let idx = openDocuments.firstIndex(where: { $0.id == tabID }) else { return }
        switch resolution {
        case .keepMine:
            openDocuments[idx].hasExternalConflict = false
            // lastSavedContent must equal diskContent before performSave so the
            // next FSEvents tick from our own write isn't mistaken for another
            // external change. We overwrite the external edits with ours.
            openDocuments[idx].lastSavedContent = diskContent
            performSave(tabIdx: idx, isAutosave: false)
        case .useTheirs:
            openDocuments[idx].content = diskContent
            openDocuments[idx].lastSavedContent = diskContent
            openDocuments[idx].isDirty = false
            openDocuments[idx].hasExternalConflict = false
        case .cancel:
            // Dismiss without choosing: return to pre-conflict state so the
            // tab isn't permanently locked from saving. Next save overwrites
            // disk (same effect as keepMine); if the user wanted the disk
            // version instead they should have picked "Use Theirs".
            openDocuments[idx].hasExternalConflict = false
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
            log.info("AI status: provider=\(status.provider) embed=\(status.embedAvailable) gen=\(status.genAvailable) docs=\(status.documentCount) embeddings=\(status.embeddingCount)")
        } catch {
            log.warning("AI status unavailable: \(error.localizedDescription)")
            self.aiStatus = nil
        }
    }

    /// Reads `2nb --version` and stores the parsed version on `cliVersion`.
    /// Needs no vault (cobra resolves `--version` before vault lookup) and is
    /// best-effort: a launch failure leaves `cliVersion` nil and is logged, not
    /// surfaced. Kept off `runCLI` because that always injects `--vault`.
    func refreshCLIVersion() async {
        let logger = self.errorLogger
        let raw: String? = await withCheckedContinuation { continuation in
            let process = Process()
            process.executableURL = URL(fileURLWithPath: CLIPath.resolve())
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
                logger?.log("2nb --version failed to launch: \(error.localizedDescription)")
                continuation.resume(returning: nil)
            }
        }
        if let parsed = CLIVersion.parse(raw) {
            cliVersion = "\(parsed.0).\(parsed.1).\(parsed.2)"
        } else {
            cliVersion = nil
        }
    }

    func askAI(question: String) async throws -> AIAskResult {
        guard let vault else { throw CLIError.noVault }
        let data = try await runCLI(["ask", "--json", "--porcelain", question], cwd: vault.rootURL)
        let response = try JSONDecoder().decode(CLIAskResponse.self, from: data)
        self.lastSemanticWarnings = response.warnings ?? []
        return AIAskResult(answer: response.answer, sources: response.sources)
    }

    // MARK: - Tools Integration

    func runLint() async {
        guard let vault else { return }
        isLinting = true
        lintReport = nil
        log.info("Lint validation started for vault: \(vault.rootURL.lastPathComponent)")
        let startTime = CFAbsoluteTimeGetCurrent()
        do {
            let data = try await runCLIAllowingNonZero(["lint", "--json", "--porcelain"], cwd: vault.rootURL)
            let report = try JSONDecoder().decode(LintReport.self, from: data)
            lintReport = report
            let elapsed = CFAbsoluteTimeGetCurrent() - startTime
            log.info("Lint completed in \(String(format: "%.1f", elapsed))s: \(report.filesChecked) files, \(report.errors) errors, \(report.warnings) warnings")
        } catch {
            let elapsed = CFAbsoluteTimeGetCurrent() - startTime
            log.error("Lint failed after \(String(format: "%.1f", elapsed))s: \(error.localizedDescription)")
            errorLogger?.log("Lint validation failed", error: error)
            lintReport = LintReport(issues: [], filesChecked: 0, errors: 0, warnings: 0)
        }
        isLinting = false
    }

    func loadMCPSetup() async {
        guard let vault else { return }
        do {
            let data = try await runCLI(["mcp-setup"], cwd: vault.rootURL)
            mcpSetupText = String(data: data, encoding: .utf8) ?? ""
        } catch {
            mcpSetupText = "Failed to load MCP setup: \(error.localizedDescription)"
        }
    }

    // MARK: - Incremental Re-Embed

    private func triggerIncrementalReindex(for url: URL) {
        guard let vault else { return }
        let relPath = vault.relativePath(for: url)
        // Queue behind any in-flight task for the same path so we never race
        // the CLI writing chunks to SQLite.
        let previous = reindexTasks[relPath]
        let task = Task { [weak self] in
            if let previous {
                _ = await previous.value
            }
            guard let self else { return }
            do {
                _ = try await self.runCLI(
                    ["index", "--doc", relPath, "--json", "--porcelain"],
                    cwd: vault.rootURL
                )
            } catch {
                log.debug("incremental reindex failed for \(relPath): \(error.localizedDescription)")
            }
            self.reindexTasks.removeValue(forKey: relPath)
        }
        reindexTasks[relPath] = task
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
                let msg = error.localizedDescription
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

    /// Resolves a search result (from `2nb search --json`) to an open
    /// document + optional editor scroll to its heading path. Called by
    /// SearchPanelView and Quick Open. If `headingPath` is empty, the
    /// document opens normally with no scroll.
    func openSearchResult(path: String, headingPath: String?) {
        guard let vault else { return }
        let url = vault.rootURL.appendingPathComponent(path)
        openDocument(at: url)
        if let headingPath, !headingPath.isEmpty {
            jumpToHeadingPath(headingPath)
        }
        log.info("openSearchResult: path=\(path) headingPath=\(headingPath ?? "")")
    }

    /// Resolves a wikilink target `[[target]]`, `[[target#heading]]`, or
    /// `[[target|alias]]` to a file path in the vault and opens it. The
    /// target is matched against the existing in-memory file list by
    /// filename (case-insensitive). Heading component (after `#`), if
    /// present, sets editorScrollTarget after opening.
    func openWikilink(_ target: String) {
        guard let vault else { return }

        // Strip alias (`target|alias` → `target`).
        let withoutAlias = target.split(separator: "|").first.map(String.init) ?? target
        // Split on `#` for heading component.
        let parts = withoutAlias.split(separator: "#", maxSplits: 1).map(String.init)
        let name = parts[0].trimmingCharacters(in: .whitespaces)
        let heading = parts.count == 2 ? parts[1].trimmingCharacters(in: .whitespaces) : nil

        // Resolve by filename (without extension, case-insensitive).
        let match = files.first { file in
            let stem = (file.name as NSString).deletingPathExtension
            return stem.localizedCaseInsensitiveCompare(name) == .orderedSame
        }

        guard let file = match else {
            log.warning("wikilink target not found: \(name)")
            return
        }

        openDocument(at: file.url)
        if let heading {
            jumpToHeadingPath(heading)
        }
        log.info("openWikilink: target=\(name) heading=\(heading ?? "")")
    }

    // MARK: - MCP Observability

    func refreshMCPStatus() async {
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
                      dims: Int, bedrockProfile: String, bedrockRegion: String,
                      openrouterKey: String) async throws {
        guard let vault else { throw CLIError.noVault }
        log.info("Saving AI config: provider=\(provider) embed=\(embedModel) gen=\(genModel)")
        let cwd = vault.rootURL

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

        if provider == "bedrock" {
            if !bedrockProfile.isEmpty {
                _ = try await runCLI(["config", "set", "ai.bedrock.profile", bedrockProfile], cwd: cwd)
            }
            if !bedrockRegion.isEmpty {
                _ = try await runCLI(["config", "set", "ai.bedrock.region", bedrockRegion], cwd: cwd)
            }
        }
    }

    func testModel(provider: String, modelID: String, modelType: String) async throws -> AIProbeResult {
        guard let vault else { throw CLIError.noVault }
        let data = try await runCLIAllowingNonZero(
            ["models", "test", modelID, "--provider", provider, "--type", modelType, "--json", "--porcelain"],
            cwd: vault.rootURL
        )
        return try JSONDecoder().decode(AIProbeResult.self, from: data)
    }

    func fetchModels(provider: String) async throws -> [CatalogModelInfo] {
        guard let vault else { throw CLIError.noVault }
        // --enabled-only hides user-disabled models from the standard
        // dropdown. Power users can still see them via `2nb models list`.
        let data = try await runCLI(
            ["models", "list", "--json", "--porcelain", "--provider", provider, "--enabled-only"],
            cwd: vault.rootURL
        )
        return try JSONDecoder().decode([CatalogModelInfo].self, from: data)
    }

    /// Fetches the full merged catalog including discovered-but-unverified
    /// vendor models. Used by the Model Wizard where the user explicitly
    /// wants to see everything available, verified or not.
    func fetchModelsForWizard() async throws -> [CatalogModelInfo] {
        guard let vault else { throw CLIError.noVault }
        let data = try await runCLI(
            ["models", "list", "--json", "--porcelain", "--discover"],
            cwd: vault.rootURL
        )
        struct MergedList: Decodable {
            let verified: [CatalogModelInfo]
            let unverified: [CatalogModelInfo]?
        }
        // The CLI currently emits a flat array for `models list`, but
        // when --discover is set the response may be the MergedModelList
        // wrapper. Try the wrapper first; fall back to flat array.
        if let merged = try? JSONDecoder().decode(MergedList.self, from: data) {
            return merged.verified + (merged.unverified ?? [])
        }
        return try JSONDecoder().decode([CatalogModelInfo].self, from: data)
    }

    func costPreview(modelIDs: [String], probe: String) async throws -> CostPreviewResponse {
        guard let vault else { throw CLIError.noVault }
        var args = ["models", "cost-preview", "--json", "--porcelain", "--probe", probe]
        args.append(contentsOf: modelIDs)
        let data = try await runCLI(args, cwd: vault.rootURL)
        return try JSONDecoder().decode(CostPreviewResponse.self, from: data)
    }

    /// Tests one model and saves it on pass. Matches the wizard flow step.
    /// Returns the decoded AIProbeResult so the UI can render outcome + latency.
    func testAndSave(modelID: String, provider: String, type: String, scope: String) async throws -> AIProbeResult {
        guard let vault else { throw CLIError.noVault }
        log.info("AI Hub action: test \(modelID, privacy: .public) (provider=\(provider, privacy: .public) type=\(type, privacy: .public) scope=\(scope, privacy: .public))")
        let data = try await runCLI(
            [
                "models", "test", modelID,
                "--provider", provider,
                "--type", type,
                "--save",
                "--scope", scope,
                "--json", "--porcelain",
            ],
            cwd: vault.rootURL
        )
        return try JSONDecoder().decode(AIProbeResult.self, from: data)
    }

    /// Writes ai.provider plus ai.embedding_model or ai.generation_model via `2nb config set`
    /// so the AI Hub can swap the active model without shelling through the
    /// full setup wizard. Caller is responsible for validating type is
    /// "embedding" or "generation". Triggers a config.yaml FSEvent which
    /// rolls the Hub's displayed state automatically.
    ///
    /// Refuses while an index rebuild is running — flipping the provider
    /// mid-rebuild produces mixed-model embeddings in one DB. Snapshots the
    /// current `ai.provider`; skips the provider write if unchanged, and
    /// reverts it if the model-key write fails so the vault never sits with
    /// a new provider against an old model.
    func setActiveModel(type: String, modelID: String, provider: String) async throws {
        guard let vault else { throw CLIError.noVault }
        if isIndexing { throw CLIError.indexRebuildInProgress }
        let key: String
        switch type {
        case "embedding": key = "ai.embedding_model"
        case "generation": key = "ai.generation_model"
        default:
            throw CLIError.noVault  // fall through — callers must pass a known type
        }

        let oldProviderData = try await runCLI(
            ["config", "get", "ai.provider"],
            cwd: vault.rootURL
        )
        let oldProvider = String(data: oldProviderData, encoding: .utf8)?
            .trimmingCharacters(in: .whitespacesAndNewlines) ?? ""

        log.info("AI Hub action: set ai.provider=\(provider, privacy: .public) and \(key, privacy: .public)=\(modelID, privacy: .public) (was provider=\(oldProvider, privacy: .public))")

        let providerChanged = oldProvider != provider
        if providerChanged {
            _ = try await runCLI(
                ["config", "set", "ai.provider", provider],
                cwd: vault.rootURL
            )
        }
        do {
            _ = try await runCLI(
                ["config", "set", key, modelID],
                cwd: vault.rootURL
            )
        } catch {
            if providerChanged && !oldProvider.isEmpty {
                log.error("setActiveModel partial-write: ai.provider was set to \(provider, privacy: .public) but \(key, privacy: .public)=\(modelID, privacy: .public) failed; reverting ai.provider to \(oldProvider, privacy: .public)")
                _ = try? await runCLI(
                    ["config", "set", "ai.provider", oldProvider],
                    cwd: vault.rootURL
                )
            }
            throw error
        }
    }

    /// Flips ai.<provider>.disabled via `2nb config set`. The GUI uses
    /// this to silence a provider without removing credentials.
    func setProviderDisabled(_ provider: String, disabled: Bool) async throws {
        guard let vault else { throw CLIError.noVault }
        let key = "ai.\(provider).disabled"
        log.info("AI Hub action: set \(key, privacy: .public) = \(disabled, privacy: .public)")
        _ = try await runCLI(
            ["config", "set", key, String(disabled)],
            cwd: vault.rootURL
        )
    }

    /// Enable or disable a single model in the user catalog so it shows /
    /// hides from selection dropdowns. Mirrors `2nb models enable|disable`.
    func setModelEnabled(_ modelID: String, provider: String, scope: String, enabled: Bool) async throws {
        guard let vault else { throw CLIError.noVault }
        let verb = enabled ? "enable" : "disable"
        log.info("AI Hub action: models \(verb, privacy: .public) \(modelID, privacy: .public) (provider=\(provider, privacy: .public) scope=\(scope, privacy: .public))")
        _ = try await runCLI(
            ["models", verb, modelID, "--provider", provider, "--scope", scope],
            cwd: vault.rootURL
        )
    }

    func setModelEnableState(_ modelID: String, provider: String, scope: String, state: String) async throws {
        guard let vault else { throw CLIError.noVault }
        log.info("AI Hub action: models enable-state \(modelID, privacy: .public) state=\(state, privacy: .public) (provider=\(provider, privacy: .public) scope=\(scope, privacy: .public))")
        _ = try await runCLI(
            ["models", "enable-state", modelID, "--provider", provider, "--scope", scope, "--state", state],
            cwd: vault.rootURL
        )
    }

    /// Threshold-only update against the user catalog.
    ///
    /// Intentionally avoids passing `--price-in / --price-out / --price-request`
    /// even when the in-memory model carries those values: Go's `mergeAddCatalogEntry`
    /// derives `priceOverride` from `cmd.Flags().Changed(...)`, so any passed
    /// price flag (even with the existing value) flips `PriceSource` to
    /// `"user"` and disables future live-pricing refresh.
    func setModelSimilarityThreshold(_ model: CatalogModelInfo, threshold: Double, scope: String) async throws {
        guard let vault else { throw CLIError.noVault }
        log.info("AI Hub action: models add \(model.modelID, privacy: .public) similarity_threshold=\(threshold, privacy: .public) scope=\(scope, privacy: .public)")
        var args = [
            "models", "add", model.modelID,
            "--provider", model.provider,
            "--type", model.modelType,
            "--scope", scope,
            "--similarity-threshold", String(format: "%.4f", threshold),
        ]
        if !model.name.isEmpty {
            args.append(contentsOf: ["--name", model.name])
        }
        if let dimensions = model.dimensions, dimensions > 0 {
            args.append(contentsOf: ["--dimensions", String(dimensions)])
        }
        if let contextLen = model.contextLen, contextLen > 0 {
            args.append(contentsOf: ["--context-length", String(contextLen)])
        }
        if let notes = model.notes, !notes.isEmpty {
            args.append(contentsOf: ["--notes", notes])
        }
        _ = try await runCLI(args, cwd: vault.rootURL)
    }

    func benchmarkModel(modelID: String, provider: String, type: String, probe: String, onEvent: @escaping @Sendable @MainActor (BenchmarkEvent) -> Void) async throws {
        guard let vault else { throw CLIError.noVault }
        let fullArgs = CLIPath.args(
            ["models", "bench", "--model", modelID, "--provider", provider, "--probe", probe, "--json", "--porcelain"],
            vault: vault.rootURL
        )
        let cmd = "2nb " + fullArgs.joined(separator: " ")
        log.info("AI Hub action: \(cmd, privacy: .public)")
        let errorLogger = self.errorLogger
        try await withCheckedThrowingContinuation { (continuation: CheckedContinuation<Void, Error>) in
            let process = Process()
            process.executableURL = URL(fileURLWithPath: CLIPath.resolve())
            process.arguments = fullArgs
            process.currentDirectoryURL = vault.rootURL
            let stdout = Pipe()
            let stderr = Pipe()
            process.standardOutput = stdout
            process.standardError = stderr

            let state = LineBuffer()
            stdout.fileHandleForReading.readabilityHandler = { handle in
                let data = handle.availableData
                guard !data.isEmpty else { return }
                for line in state.append(data) {
                    if let event = try? JSONDecoder().decode(BenchmarkEvent.self, from: Data(line.utf8)) {
                        Task { @MainActor in onEvent(event) }
                    }
                }
            }

            process.terminationHandler = { proc in
                stdout.fileHandleForReading.readabilityHandler = nil
                for line in state.append(stdout.fileHandleForReading.readDataToEndOfFile()) {
                    if let event = try? JSONDecoder().decode(BenchmarkEvent.self, from: Data(line.utf8)) {
                        Task { @MainActor in onEvent(event) }
                    }
                }
                for line in state.finish() {
                    if let event = try? JSONDecoder().decode(BenchmarkEvent.self, from: Data(line.utf8)) {
                        Task { @MainActor in onEvent(event) }
                    }
                }
                if proc.terminationStatus == 0 {
                    continuation.resume()
                } else {
                    let errMsg = String(data: stderr.fileHandleForReading.readDataToEndOfFile(), encoding: .utf8)?
                        .trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
                    log.error("CLI \(cmd, privacy: .public) failed (exit \(proc.terminationStatus)): \(errMsg, privacy: .public)")
                    errorLogger?.log("CLI \(cmd) failed (exit \(proc.terminationStatus)): \(errMsg)")
                    continuation.resume(throwing: CLIError.nonZeroExit(proc.terminationStatus, message: errMsg))
                }
            }

            do {
                try process.run()
            } catch {
                log.error("CLI \(cmd) launch failed: \(error.localizedDescription)")
                errorLogger?.log("CLI \(cmd) launch failed", error: error)
                continuation.resume(throwing: error)
            }
        }
    }

    /// Enable or disable every model from a vendor within a provider in
    /// one CLI call. The caller should pass the list of model IDs the
    /// Hub has already rendered; this avoids a catalog-lookup round-
    /// trip inside the CLI and, critically, covers discovered-only
    /// models that haven't yet been tested/saved into the user catalog
    /// (which the CLI's lookup wouldn't find). --vendor is passed for
    /// logging / telemetry only.
    func setVendorEnabled(vendor: String, provider: String, scope: String, enabled: Bool, modelIDs: [String]) async throws {
        guard let vault else { throw CLIError.noVault }
        let verb = enabled ? "enable" : "disable"
        log.info("AI Hub action: models \(verb, privacy: .public) --vendor \(vendor, privacy: .public) (provider=\(provider, privacy: .public) scope=\(scope, privacy: .public) count=\(modelIDs.count))")
        guard !modelIDs.isEmpty else { return }
        var args = ["models", verb, "--vendor", vendor, "--provider", provider, "--scope", scope]
        args.append(contentsOf: modelIDs)
        _ = try await runCLI(args, cwd: vault.rootURL)
    }

    /// Fetches the full AIStatusInfo envelope including providers[] for
    /// the AI Hub's provider cards and active-model section.
    func fetchAIStatus() async throws -> AIStatusInfo {
        guard let vault else { throw CLIError.noVault }
        let data = try await runCLI(
            ["ai", "status", "--json", "--porcelain"],
            cwd: vault.rootURL
        )
        return try JSONDecoder().decode(AIStatusInfo.self, from: data)
    }

    func checkOllamaStatus() async -> OllamaReadiness {
        guard let vault else { return OllamaReadiness(installed: false, running: false) }
        do {
            let data = try await runCLIAllowingNonZero(
                ["ai", "local", "--json", "--porcelain"],
                cwd: vault.rootURL
            )
            let report = try JSONDecoder().decode(OllamaReport.self, from: data)
            return report.ollama
        } catch {
            return OllamaReadiness(installed: false, running: false)
        }
    }

    // MARK: - CLI Execution

    func runCLI(_ args: [String], cwd: URL) async throws -> Data {
        let fullArgs = CLIPath.args(args, vault: cwd)
        let cmd = "2nb " + fullArgs.joined(separator: " ")
        log.info("CLI exec: \(cmd, privacy: .public)")
        // Capture the (Sendable) error logger so the background terminationHandler
        // can record a genuine CLI failure to the per-vault `.2ndbrain/logs` file
        // — the surface the user's "read the logs to debug" workflow targets.
        let errorLogger = self.errorLogger
        return try await withCheckedThrowingContinuation { continuation in
            let process = Process()
            process.executableURL = URL(fileURLWithPath: CLIPath.resolve())
            process.arguments = fullArgs
            process.currentDirectoryURL = cwd
            let stdout = Pipe()
            let stderr = Pipe()
            process.standardOutput = stdout
            process.standardError = stderr

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

            process.terminationHandler = { proc in
                // Detach the handlers so no late reads fire after we resume.
                stdout.fileHandleForReading.readabilityHandler = nil
                stderr.fileHandleForReading.readabilityHandler = nil
                // Flush any bytes that arrived between the last handler call
                // and process exit.
                drain.appendStdout(stdout.fileHandleForReading.readDataToEndOfFile())
                drain.appendStderr(stderr.fileHandleForReading.readDataToEndOfFile())
                if proc.terminationStatus == 0 {
                    continuation.resume(returning: drain.stdoutData)
                } else {
                    let errMsg = String(data: drain.stderrData, encoding: .utf8)?
                        .trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
                    if !errMsg.isEmpty {
                        log.error("CLI \(cmd, privacy: .public) failed (exit \(proc.terminationStatus)): \(errMsg, privacy: .public)")
                    }
                    errorLogger?.log("CLI \(cmd) failed (exit \(proc.terminationStatus)): \(errMsg)")
                    continuation.resume(throwing: CLIError.nonZeroExit(proc.terminationStatus, message: errMsg))
                }
            }

            do {
                try process.run()
            } catch {
                log.error("CLI \(cmd) launch failed: \(error.localizedDescription)")
                errorLogger?.log("CLI \(cmd) launch failed", error: error)
                continuation.resume(throwing: error)
            }
        }
    }

    /// Like runCLI but returns stdout regardless of exit code.
    /// Needed for `2nb lint` which exits 2 on validation errors but still emits valid JSON.
    private func runCLIAllowingNonZero(_ args: [String], cwd: URL) async throws -> Data {
        let fullArgs = CLIPath.args(args, vault: cwd)
        let cmd = "2nb " + fullArgs.joined(separator: " ")
        log.info("CLI exec (non-zero ok): \(cmd)")
        return try await withCheckedThrowingContinuation { continuation in
            let process = Process()
            process.executableURL = URL(fileURLWithPath: CLIPath.resolve())
            process.arguments = fullArgs
            process.currentDirectoryURL = cwd
            let stdout = Pipe()
            process.standardOutput = stdout
            process.standardError = FileHandle.nullDevice
            let drain = PipeDrain()
            stdout.fileHandleForReading.readabilityHandler = { handle in
                let chunk = handle.availableData
                if chunk.isEmpty {
                    handle.readabilityHandler = nil
                } else {
                    drain.appendStdout(chunk)
                }
            }
            process.terminationHandler = { proc in
                stdout.fileHandleForReading.readabilityHandler = nil
                drain.appendStdout(stdout.fileHandleForReading.readDataToEndOfFile())
                if proc.terminationStatus != 0 {
                    log.debug("CLI \(cmd) exited \(proc.terminationStatus) (allowed)")
                }
                continuation.resume(returning: drain.stdoutData)
            }
            do {
                try process.run()
            } catch {
                log.error("CLI \(cmd) launch failed: \(error.localizedDescription)")
                continuation.resume(throwing: error)
            }
        }
    }
}

/// Thread-safe accumulator for subprocess stdout/stderr. Readability
/// handlers fire on arbitrary queues, so appends are serialized through
/// an internal lock. Used by runCLI / runCLIAllowingNonZero to drain the
/// child's pipes as data arrives — avoiding the pipe-buffer deadlock
/// where a child blocks on write() past ~64KB and terminationHandler
/// never fires.
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

    /// Denominator for embedding-coverage displays: the embeddable doc count
    /// when the CLI reports it, else the raw document count (older CLI).
    var embeddableDenominator: Int { vaultEmbeddableDocs ?? documentCount }

    enum CodingKeys: String, CodingKey {
        case provider
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

    enum CodingKeys: String, CodingKey {
        case name
        case configPresent = "config_present"
        case disabled
        case reachable
        case reason
        case detail
    }
}

struct AIAskResult: Codable {
    let answer: String
    let sources: [String]
}

// CLIAskResponse is the envelope `2nb ask --json` now returns.
// Breaking change from pre-Phase-1: used to be AIAskResult directly.
struct CLIAskResponse: Codable {
    let mode: String
    let warnings: [String]?
    let answer: String
    let sources: [String]
}

// CLISearchResponse is the envelope `2nb search --json` now returns.
// Breaking change from pre-Phase-1: used to be [CLISearchResult] directly.
struct CLISearchResponse: Codable {
    let mode: String
    let warnings: [String]?
    let results: [CLISearchResult]
}

struct CLISearchResult: Codable {
    let docID: String
    let path: String
    let title: String
    let docType: String?
    let headingPath: String?
    let score: Double
    let status: String?

    enum CodingKeys: String, CodingKey {
        case docID = "doc_id"
        case path, title
        case docType = "type"
        case headingPath = "heading_path"
        case score, status
    }
}

struct EmbeddingProgress {
    var current: Int
    var total: Int
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

// MARK: - AI Setup Wizard Types

struct AIProbeResult: Codable {
    let modelID: String
    let provider: String
    let modelType: String
    let ok: Bool
    let detail: String?
    let latency: String

    enum CodingKeys: String, CodingKey {
        case modelID = "model_id"
        case provider
        case modelType = "type"
        case ok, detail, latency
    }
}

struct CatalogModelInfo: Codable, Identifiable {
    var id: String { provider + "|" + modelID }
    let modelID: String
    let name: String
    let provider: String
    let modelType: String
    let vendor: String?
    let vendorDisplay: String?
    let family: String?
    let versionSortKey: String?
    let dimensions: Int?
    let priceIn: Double?
    let priceOut: Double?
    let priceRequest: Double?
    let priceSource: String?
    let reachable: Bool?
    let credentials: Bool?
    let rateLimitRPS: Double?
    let rateLimitTPM: Int?
    let priceOverride: Bool?
    let contextLen: Int?
    let recommendedSimilarityThreshold: Double?
    let local: Bool?
    let tier: String?
    let invokeStrategy: String?
    let enabled: Bool?
    let active: Bool?
    let configHint: String?
    let notes: String?
    let testedAt: String?
    let testLatencyMs: Int64?
    let testError: String?
    let benchmark: CatalogBenchmarkSummary?
    let compatible: Bool?
    let compatibilityReason: String?

    enum CodingKeys: String, CodingKey {
        case modelID = "id"
        case name, provider
        case modelType = "type"
        case vendor
        case vendorDisplay = "vendor_display"
        case family
        case versionSortKey = "version_sort_key"
        case dimensions
        case priceIn = "price_input_per_million"
        case priceOut = "price_output_per_million"
        case priceRequest = "price_per_request"
        case priceSource = "price_source"
        case reachable
        case credentials
        case rateLimitRPS = "rate_limit_rps"
        case rateLimitTPM = "rate_limit_tpm"
        case priceOverride = "price_override"
        case contextLen = "context_length"
        case recommendedSimilarityThreshold = "recommended_similarity_threshold"
        case local
        case tier
        case invokeStrategy = "invoke_strategy"
        case enabled
        case active
        case configHint = "config_hint"
        case notes
        case testedAt = "tested_at"
        case testLatencyMs = "test_latency_ms"
        case testError = "test_error"
        case benchmark
        case compatible
        case compatibilityReason = "compatibility_reason"
    }
}

struct CatalogBenchmarkSummary: Codable, Equatable {
    let ranAt: String?
    let avgLatencyMs: Int64?
    let qualityScore: Double?
    let vaultDocCount: Int?

    enum CodingKeys: String, CodingKey {
        case ranAt = "ran_at"
        case avgLatencyMs = "avg_latency_ms"
        case qualityScore = "quality_score"
        case vaultDocCount = "vault_doc_count"
    }
}

struct BenchmarkEvent: Codable, Identifiable {
    var id = UUID()
    let event: String
    let modelID: String?
    let provider: String?
    let modelType: String?
    let probe: String?
    let result: BenchmarkProbeResult?
    let benchmark: CatalogBenchmarkSummary?
    let message: String?

    enum CodingKeys: String, CodingKey {
        case event
        case modelID = "model_id"
        case provider
        case modelType = "type"
        case probe
        case result
        case benchmark
        case message
    }
}

struct BenchmarkProbeResult: Codable, Equatable {
    let probe: String
    let latencyMs: Int64
    let ok: Bool
    let skipped: Bool?
    let detail: String?
    let qualityScore: Double?
    let vaultDocCount: Int?

    enum CodingKeys: String, CodingKey {
        case probe
        case latencyMs = "latency_ms"
        case ok
        case skipped
        case detail
        case qualityScore = "quality_score"
        case vaultDocCount = "vault_doc_count"
    }
}

struct CostEstimate: Codable, Identifiable {
    var id: String { modelID }
    let modelID: String
    let provider: String
    let requests: Int
    let inputTokens: Int
    let outputTokens: Int
    let probe: String
    let usd: Double
    let knownPricing: Bool

    enum CodingKeys: String, CodingKey {
        case modelID = "model_id"
        case provider, requests, probe, usd
        case inputTokens = "input_tokens"
        case outputTokens = "output_tokens"
        case knownPricing = "known_pricing"
    }
}

struct CostPreviewResponse: Codable {
    let estimates: [CostEstimate]
    let totalUSD: Double

    enum CodingKeys: String, CodingKey {
        case estimates
        case totalUSD = "total_usd"
    }
}

struct OllamaReadiness: Codable {
    let installed: Bool
    let running: Bool
}

struct OllamaReport: Codable {
    let ollama: OllamaReadiness
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

enum FileTreeNode: Identifiable {
    case directory(id: String, name: String, path: String, children: [FileTreeNode])
    case file(FileItem)

    var id: String {
        switch self {
        case .directory(let id, _, _, _): return id
        case .file(let item): return item.id.uuidString
        }
    }

    var name: String {
        switch self {
        case .directory(_, let name, _, _): return name
        case .file(let item): return item.name
        }
    }

    var fileCount: Int {
        switch self {
        case .directory(_, _, _, let children):
            return children.reduce(0) { $0 + ($1.isFile ? 1 : $1.fileCount) }
        case .file: return 1
        }
    }

    var isFile: Bool {
        if case .file = self { return true }
        return false
    }
}

func buildFileTree(from files: [FileItem]) -> [FileTreeNode] {
    // Group files by their top-level directory
    var dirFiles: [String: [FileItem]] = [:]
    var rootFiles: [FileItem] = []

    for file in files {
        let parts = file.relativePath.split(separator: "/").map(String.init)
        if parts.count > 1 {
            dirFiles[parts[0], default: []].append(file)
        } else {
            rootFiles.append(file)
        }
    }

    // Recursively build subtrees
    func subtree(files: [FileItem], prefix: String) -> [FileTreeNode] {
        var subdirs: [String: [FileItem]] = [:]
        var leaves: [FileItem] = []

        for file in files {
            let remainder = String(file.relativePath.dropFirst(prefix.count))
            let parts = remainder.split(separator: "/").map(String.init)
            if parts.count > 1 {
                subdirs[parts[0], default: []].append(file)
            } else {
                leaves.append(file)
            }
        }

        var nodes: [FileTreeNode] = subdirs.keys.sorted().map { dir in
            let path = prefix + dir + "/"
            return .directory(id: "dir:" + path, name: dir, path: path, children: subtree(files: subdirs[dir]!, prefix: path))
        }
        nodes += leaves.sorted { $0.name.localizedCaseInsensitiveCompare($1.name) == .orderedAscending }.map { .file($0) }
        return nodes
    }

    // Build top-level tree: directories first, then root files
    var tree: [FileTreeNode] = dirFiles.keys.sorted().map { dir in
        let path = dir + "/"
        return .directory(id: "dir:" + path, name: dir, path: path, children: subtree(files: dirFiles[dir]!, prefix: path))
    }
    tree += rootFiles.sorted { $0.name.localizedCaseInsensitiveCompare($1.name) == .orderedAscending }.map { .file($0) }
    return tree
}

struct DocumentTab: Identifiable {
    let id = UUID()
    let url: URL
    var document: MarkdownDocument
    var content: String
    var isDirty: Bool = false
    /// Content as it was on disk at the last successful load or save.
    /// Used as the common ancestor for merge conflict detection.
    var lastSavedContent: String = ""
    /// True when external changes were detected and the user deferred resolution.
    /// Blocks further saves until resolved via the merge conflict dialog.
    var hasExternalConflict: Bool = false
    /// True when the file was opened in read-only mode (e.g. because it's huge).
    /// The editor disables text input when this is set; autosave skips the tab.
    var readOnly: Bool = false

    var title: String {
        document.title.isEmpty ? url.deletingPathExtension().lastPathComponent : document.title
    }
}
