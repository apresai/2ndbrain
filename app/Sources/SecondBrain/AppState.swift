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
            let tab = DocumentTab(url: url, document: doc, content: content)
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
        let url = vault.rootURL.appendingPathComponent(filename)

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
        let tab = openDocuments[idx]
        do {
            try tab.content.write(to: tab.url, atomically: true, encoding: .utf8)
            openDocuments[idx].isDirty = false
            crashJournal?.clearSnapshot(documentID: tab.document.id)
            log.debug("Saved: \(tab.url.lastPathComponent)")
        } catch {
            log.error("Failed to save \(tab.url.lastPathComponent): \(error.localizedDescription)")
            errorLogger?.log("Failed to save document", error: error)
            crashJournal?.saveSnapshot(documentID: tab.document.id, content: tab.content)
        }
    }

    func saveSnapshotForCurrentDocument() {
        guard let tab = currentDocument else { return }
        crashJournal?.saveSnapshot(documentID: tab.document.id, content: tab.content)
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
        if let content = try? String(contentsOfFile: path, encoding: .utf8) {
            if !openDocuments[idx].isDirty {
                openDocuments[idx].content = content
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

    var title: String {
        document.title.isEmpty ? url.deletingPathExtension().lastPathComponent : document.title
    }
}
