import SwiftUI
import SecondBrainCore
import os

private let log = Logger(subsystem: "dev.apresai.2ndbrain", category: "app")

@Observable @MainActor
final class AppState {
    var vault: VaultManager?
    var database: DatabaseManager?
    var fileWatcher: FSEventsWatcher?

    // Document tabs
    var openDocuments: [DocumentTab] = []
    var activeTabIndex: Int = 0

    // Sidebar
    var sidebarVisible = true
    var focusModeActive = false
    var showGraphView = false

    // Overlay panels
    var showSearch = false
    var showQuickOpen = false
    var showCommandPalette = false
    var showAskAI = false
    var typewriterModeActive = false
    var showTemplatePicker = false
    var showAISetupWizard = false
    var inlineRenderingEnabled = false
    var editorFontSize: CGFloat = UserDefaults.standard.object(forKey: "editorFontSize") as? CGFloat ?? 14
    var editorFontFamily: String = UserDefaults.standard.string(forKey: "editorFontFamily") ?? "System Mono"

    // Autosave
    var autosaveIntervalSeconds: Int = UserDefaults.standard.object(forKey: "autosaveIntervalSeconds") as? Int ?? 30
    private var autosaveTimer: Timer?
    private static let lowDiskThreshold: Int64 = 50 * 1024 * 1024

    // Merge conflict controllers (one per active dialog, retained so the NSWindow stays alive)
    private var mergeControllers: [MergeConflictController] = []

    // In-flight incremental re-embed tasks keyed by vault-relative path, so a
    // second save for the same file queues behind the first instead of
    // racing the database.
    private var reindexTasks: [String: Task<Void, Never>] = [:]

    // Suggest Links state
    var showSuggestLinks = false
    var suggestLinks: [SuggestLinkInfo] = []
    var suggestLinksLoading = false
    var suggestLinksError: String?

    // Polish state
    var showPolish = false
    var polishState: PolishState = .idle

    // AI state
    var aiStatus: AIStatusInfo?
    var isIndexing = false
    var indexError: String?
    var embeddingProgress: EmbeddingProgress?
    var indexProgress: IndexProgress?
    var showIndexProgress = false
    var pendingFindSimilarQuery: String?

    // Tools panels
    var showLintResults = false
    var isLinting = false
    var lintReport: LintReport?
    var showSkillsInstall = false
    var isInstallingSkills = false
    var skillsInstallResult: String?
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

    var validTabIndex: Int? {
        let idx = activeTabIndex
        guard idx >= 0, idx < openDocuments.count else { return nil }
        return idx
    }

    var currentDocument: DocumentTab? {
        guard let idx = validTabIndex else { return nil }
        return openDocuments[idx]
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

        // Start watching for changes
        fileWatcher?.stop()
        let watcher = FSEventsWatcher(path: url.path) { @Sendable [weak self] paths in
            Task { @MainActor in
                self?.refreshFiles()
                for path in paths {
                    self?.reloadIfOpen(path: path)
                }
            }
        }
        watcher.start()
        self.fileWatcher = watcher
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
            return
        }

        do {
            let doc = try FrontmatterParser.loadDocument(from: url)
            let content = try String(contentsOf: url, encoding: .utf8)
            var tab = DocumentTab(url: url, document: doc, content: content)
            tab.lastSavedContent = content
            openDocuments.append(tab)
            activeTabIndex = openDocuments.count - 1
            updateOutline()
            log.info("Opened document: \(url.lastPathComponent) (type=\(doc.docType), id=\(doc.id.prefix(8)))")
        } catch {
            log.error("Failed to open document \(url.lastPathComponent): \(error.localizedDescription)")
            errorLogger?.log("Failed to open document", error: error)
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

    func rebuildIndex() {
        guard vault != nil else { return }
        indexProgress = IndexProgress(phase: .ready)
        showIndexProgress = true
    }

    func startIndex() {
        guard let vault else { return }
        isIndexing = true
        indexError = nil
        embeddingProgress = nil
        indexProgress = IndexProgress(phase: .indexingFiles)
        log.info("Index rebuild started for vault: \(vault.rootURL.lastPathComponent)")
        let startTime = CFAbsoluteTimeGetCurrent()

        Task {
            do {
                let process = Process()
                process.executableURL = URL(fileURLWithPath: "/usr/local/bin/2nb")
                process.arguments = ["index"]
                process.currentDirectoryURL = vault.rootURL
                let stderrPipe = Pipe()
                let stdoutPipe = Pipe()
                process.standardError = stderrPipe
                process.standardOutput = stdoutPipe

                // Parse progress from stderr
                stderrPipe.fileHandleForReading.readabilityHandler = { [weak self] handle in
                    let data = handle.availableData
                    guard !data.isEmpty, let text = String(data: data, encoding: .utf8) else { return }

                    for line in text.split(separator: "\n") {
                        let l = String(line).trimmingCharacters(in: .whitespaces)

                        // Embedding progress: "embedded N/M: path" or "embedding N documents..."
                        let embedPattern = /[Ee]mbed\w*\s+(\d+)\/(\d+)/
                        if let match = l.firstMatch(of: embedPattern) {
                            let current = Int(match.1) ?? 0
                            let total = Int(match.2) ?? 0
                            Task { @MainActor [weak self] in
                                self?.embeddingProgress = EmbeddingProgress(current: current, total: total)
                                self?.indexProgress?.phase = .embedding
                                self?.indexProgress?.embeddingCurrent = current
                                self?.indexProgress?.embeddingTotal = total
                            }
                            continue
                        }

                        // Embedding count: "embedding N documents..."
                        let embedCountPattern = /embedding\s+(\d+)\s+documents/
                        if let match = l.firstMatch(of: embedCountPattern) {
                            let total = Int(match.1) ?? 0
                            Task { @MainActor [weak self] in
                                self?.indexProgress?.phase = .embedding
                                self?.indexProgress?.embeddingTotal = total
                            }
                            continue
                        }

                        // File indexed: "  path/to/file.md" (indented path)
                        if l.hasSuffix(".md") && !l.contains(":") {
                            let fileName = l
                            Task { @MainActor [weak self] in
                                self?.indexProgress?.currentFile = fileName
                                self?.indexProgress?.filesIndexed += 1
                            }
                        }
                    }
                }

                try process.run()
                process.waitUntilExit()
                stderrPipe.fileHandleForReading.readabilityHandler = nil

                let exitCode = process.terminationStatus
                let elapsed = CFAbsoluteTimeGetCurrent() - startTime

                // Parse stdout summary: "Indexed N files, N chunks, N links"
                let stdoutData = stdoutPipe.fileHandleForReading.readDataToEndOfFile()
                if let summary = String(data: stdoutData, encoding: .utf8) {
                    let statsPattern = /Indexed\s+(\d+)\s+files?,\s+(\d+)\s+chunks?,\s+(\d+)\s+links?/
                    if let match = summary.firstMatch(of: statsPattern) {
                        indexProgress?.docsIndexed = Int(match.1) ?? 0
                        indexProgress?.chunksCreated = Int(match.2) ?? 0
                        indexProgress?.linksFound = Int(match.3) ?? 0
                    }
                }

                indexProgress?.elapsed = elapsed
                if exitCode == 0 {
                    indexProgress?.phase = .complete
                    log.info("Index rebuild completed in \(String(format: "%.1f", elapsed))s (exit 0)")
                } else {
                    indexProgress?.phase = .failed
                    indexProgress?.error = "CLI exited with code \(exitCode)"
                    log.warning("Index rebuild finished with exit code \(exitCode) in \(String(format: "%.1f", elapsed))s")
                }

                // Reopen database to pick up changes
                self.database = try DatabaseManager(path: vault.indexDBPath)
            } catch {
                let elapsed = CFAbsoluteTimeGetCurrent() - startTime
                log.error("Index rebuild failed after \(String(format: "%.1f", elapsed))s: \(error.localizedDescription)")
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
            crashJournal?.clearSnapshotSync(documentID: tab.document.id)
            log.debug("\(isAutosave ? "Autosaved" : "Saved"): \(tab.url.lastPathComponent)")
            triggerIncrementalReindex(for: tab.url)
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
            return
        }
        // Extract body (skip frontmatter)
        let (_, body) = FrontmatterParser.parse(tab.content)
        outline = MarkdownRenderer.extractOutline(body)
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
            openDocuments[idx].hasExternalConflict = true
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
            log.debug("AI status unavailable: \(error.localizedDescription)")
            self.aiStatus = nil
        }
    }

    func askAI(question: String) async throws -> AIAskResult {
        guard let vault else { throw CLIError.noVault }
        let data = try await runCLI(["ask", "--json", "--porcelain", question], cwd: vault.rootURL)
        return try JSONDecoder().decode(AIAskResult.self, from: data)
    }

    func searchSemantic(query: String) async throws -> [SearchResultItem] {
        guard let vault else { throw CLIError.noVault }
        let data = try await runCLI(["search", "--json", "--porcelain", query], cwd: vault.rootURL)
        let results = try JSONDecoder().decode([CLISearchResult].self, from: data)
        return results.map { r in
            SearchResultItem(
                id: r.docID,
                path: r.path,
                title: r.title,
                docType: r.docType ?? "",
                headingPath: r.headingPath ?? "",
                score: r.score,
                status: r.status ?? ""
            )
        }
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

    func installSkills() async {
        guard let vault else { return }
        isInstallingSkills = true
        skillsInstallResult = nil
        log.info("Installing AI agent skills")
        do {
            let data = try await runCLI(["skills", "install", "--all", "--force"], cwd: vault.rootURL)
            let output = String(data: data, encoding: .utf8) ?? ""
            skillsInstallResult = output.isEmpty ? "Skills installed for all supported agents." : output
            log.info("Skills installed successfully")
        } catch {
            log.error("Skills installation failed: \(error.localizedDescription)")
            errorLogger?.log("Skills installation failed", error: error)
            skillsInstallResult = "Installation failed: \(error.localizedDescription)"
        }
        isInstallingSkills = false
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

    // MARK: - Suggest Links

    func openSuggestLinks() {
        suggestLinks = []
        suggestLinksError = nil
        showSuggestLinks = true
        Task { await loadSuggestLinks() }
    }

    func loadSuggestLinks() async {
        guard let vault, let tab = currentDocument else {
            suggestLinksError = "No document open."
            return
        }
        suggestLinksLoading = true
        defer { suggestLinksLoading = false }
        let relPath = vault.relativePath(for: tab.url)
        do {
            let data = try await runCLI(
                ["suggest-links", relPath, "--json", "--porcelain"],
                cwd: vault.rootURL
            )
            if data.isEmpty {
                suggestLinks = []
                return
            }
            suggestLinks = (try JSONDecoder().decode([SuggestLinkInfo]?.self, from: data)) ?? []
        } catch {
            suggestLinksError = "Could not generate suggestions: \(error.localizedDescription). Ensure an AI provider is configured."
        }
    }

    func insertWikilink(for suggestion: SuggestLinkInfo) {
        guard let idx = validTabIndex else { return }
        let linkText = "[[\(suggestion.title)]]"
        openDocuments[idx].content.append(linkText)
        openDocuments[idx].isDirty = true
        updateOutline()
    }

    // MARK: - Polish

    func openPolish() {
        polishState = .idle
        showPolish = true
    }

    func runPolish() async {
        guard let vault, let tab = currentDocument else {
            polishState = .error("No document open.")
            return
        }
        polishState = .loading
        let relPath = vault.relativePath(for: tab.url)
        do {
            let data = try await runCLI(
                ["polish", relPath, "--json", "--porcelain"],
                cwd: vault.rootURL
            )
            let result = try JSONDecoder().decode(PolishResultInfo.self, from: data)
            polishState = .loaded(result)
        } catch {
            polishState = .error("Could not polish document: \(error.localizedDescription). Ensure an AI generation provider is configured.")
        }
    }

    func acceptPolishedRevision() {
        guard case .loaded(let result) = polishState else { return }
        guard let idx = validTabIndex else { return }
        let tab = openDocuments[idx]
        // Preserve frontmatter; replace only the body.
        let (frontmatter, _) = FrontmatterParser.parse(tab.content)
        openDocuments[idx].content = FrontmatterParser.serialize(
            frontmatter: frontmatter,
            body: result.polished
        )
        openDocuments[idx].isDirty = true
        updateOutline()
        polishState = .idle
    }

    func openPolishedAsNewTab() {
        guard case .loaded(let result) = polishState else { return }
        guard let vault, let tab = currentDocument else { return }
        // Write the polished version to a sibling file with a -polished suffix
        // so the user can diff and manually merge without losing the original.
        let parent = tab.url.deletingLastPathComponent()
        let stem = tab.url.deletingPathExtension().lastPathComponent
        let ext = tab.url.pathExtension
        let candidate = "\(stem)-polished.\(ext)"
        let target = uniqueFilename(base: candidate, in: parent)
        let (frontmatter, _) = FrontmatterParser.parse(tab.content)
        let content = FrontmatterParser.serialize(frontmatter: frontmatter, body: result.polished)
        do {
            try content.write(to: target, atomically: true, encoding: .utf8)
            refreshFiles()
            openDocument(at: target)
            log.info("Polished revision opened as \(target.lastPathComponent)")
        } catch {
            log.error("Failed to open polished revision: \(error.localizedDescription)")
            errorLogger?.log("Failed to open polished revision", error: error)
        }
        _ = vault // silence unused warning; kept for future path joins
        polishState = .idle
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
            log.debug("MCP status unavailable: \(error.localizedDescription)")
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
        let data = try await runCLI(
            ["models", "list", "--json", "--porcelain", "--provider", provider],
            cwd: vault.rootURL
        )
        return try JSONDecoder().decode([CatalogModelInfo].self, from: data)
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

    private func runCLI(_ args: [String], cwd: URL) async throws -> Data {
        let cmd = "2nb " + args.joined(separator: " ")
        log.debug("CLI exec: \(cmd)")
        return try await withCheckedThrowingContinuation { continuation in
            let process = Process()
            process.executableURL = URL(fileURLWithPath: "/usr/local/bin/2nb")
            process.arguments = args
            process.currentDirectoryURL = cwd
            let stdout = Pipe()
            let stderr = Pipe()
            process.standardOutput = stdout
            process.standardError = stderr

            process.terminationHandler = { proc in
                let stderrData = stderr.fileHandleForReading.readDataToEndOfFile()
                if proc.terminationStatus == 0 {
                    let data = stdout.fileHandleForReading.readDataToEndOfFile()
                    continuation.resume(returning: data)
                } else {
                    let errMsg = String(data: stderrData, encoding: .utf8)?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
                    if !errMsg.isEmpty {
                        log.error("CLI \(cmd) failed (exit \(proc.terminationStatus)): \(errMsg)")
                    }
                    continuation.resume(throwing: CLIError.nonZeroExit(proc.terminationStatus))
                }
            }

            do {
                try process.run()
            } catch {
                log.error("CLI \(cmd) launch failed: \(error.localizedDescription)")
                continuation.resume(throwing: error)
            }
        }
    }

    /// Like runCLI but returns stdout regardless of exit code.
    /// Needed for `2nb lint` which exits 2 on validation errors but still emits valid JSON.
    private func runCLIAllowingNonZero(_ args: [String], cwd: URL) async throws -> Data {
        let cmd = "2nb " + args.joined(separator: " ")
        log.debug("CLI exec (non-zero ok): \(cmd)")
        return try await withCheckedThrowingContinuation { continuation in
            let process = Process()
            process.executableURL = URL(fileURLWithPath: "/usr/local/bin/2nb")
            process.arguments = args
            process.currentDirectoryURL = cwd
            let stdout = Pipe()
            process.standardOutput = stdout
            process.standardError = FileHandle.nullDevice
            process.terminationHandler = { proc in
                if proc.terminationStatus != 0 {
                    log.debug("CLI \(cmd) exited \(proc.terminationStatus) (allowed)")
                }
                continuation.resume(returning: stdout.fileHandleForReading.readDataToEndOfFile())
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

    enum CodingKeys: String, CodingKey {
        case provider
        case embeddingModel = "embedding_model"
        case genModel = "generation_model"
        case dimensions
        case embedAvailable = "embed_available"
        case genAvailable = "gen_available"
        case embeddingCount = "embedding_count"
        case documentCount = "document_count"
    }
}

struct AIAskResult: Codable {
    let answer: String
    let sources: [String]
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
    var id: String { modelID }
    let modelID: String
    let name: String
    let provider: String
    let modelType: String
    let dimensions: Int?
    let priceIn: Double?
    let priceOut: Double?
    let contextLen: Int?

    enum CodingKeys: String, CodingKey {
        case modelID = "id"
        case name, provider
        case modelType = "type"
        case dimensions
        case priceIn = "price_input_per_million"
        case priceOut = "price_output_per_million"
        case contextLen = "context_length"
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
    case nonZeroExit(Int32)

    var errorDescription: String? {
        switch self {
        case .noVault: return "No vault is open"
        case .nonZeroExit(let code): return "CLI exited with code \(code)"
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

    var title: String {
        document.title.isEmpty ? url.deletingPathExtension().lastPathComponent : document.title
    }
}
