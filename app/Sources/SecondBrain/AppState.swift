import SwiftUI
import SecondBrainCore

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
    var selectedTagFilter: String?
    var inlineRenderingEnabled = false

    // AI state
    var aiStatus: AIStatusInfo?
    var isIndexing = false
    var indexError: String?
    var embeddingProgress: EmbeddingProgress?
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
        let vm = VaultManager(rootURL: url)
        self.vault = vm

        // Open the shared SQLite index (same DB the Go CLI writes to)
        if vm.isInitialized {
            do {
                self.database = try DatabaseManager(path: vm.indexDBPath)
            } catch {
                print("Failed to open index: \(error)")
            }
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

        // Apply tag filter if set
        if let tagFilter = selectedTagFilter, let db = database {
            if let taggedDocs = try? db.documentsWithTag(tagFilter) {
                let taggedPaths = Set(taggedDocs.map { $0.path })
                items = items.filter { taggedPaths.contains($0.relativePath) }
            }
        }

        self.files = items
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
        } catch {
            print("Failed to open: \(error)")
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
        } catch {
            print("Failed to create: \(error)")
        }
    }

    func createVault(at url: URL) {
        do {
            try VaultManager.initializeVault(at: url)
            openVault(at: url)
            UserDefaults.standard.set(url.path, forKey: "lastVaultPath")
        } catch {
            print("Failed to create vault: \(error)")
        }
    }

    func deleteDocument(at url: URL) {
        // Close tab if open
        if let idx = openDocuments.firstIndex(where: { $0.url == url }) {
            closeTab(at: idx)
        }

        // Delete file from disk
        do {
            try FileManager.default.removeItem(at: url)
            refreshFiles()
        } catch {
            print("Failed to delete: \(error)")
        }
    }

    func rebuildIndex() {
        guard let vault else { return }
        isIndexing = true
        indexError = nil
        embeddingProgress = nil

        Task {
            do {
                let process = Process()
                process.executableURL = URL(fileURLWithPath: "/usr/local/bin/2nb")
                process.arguments = ["index"]
                process.currentDirectoryURL = vault.rootURL
                let stderrPipe = Pipe()
                process.standardError = stderrPipe

                // Parse embedding progress from stderr
                stderrPipe.fileHandleForReading.readabilityHandler = { [weak self] handle in
                    let data = handle.availableData
                    guard !data.isEmpty, let line = String(data: data, encoding: .utf8) else { return }
                    // Match "embedded N/M: path" or "Embedding N/M documents..."
                    let pattern = /[Ee]mbed\w*\s+(\d+)\/(\d+)/
                    for l in line.split(separator: "\n") {
                        if let match = String(l).firstMatch(of: pattern) {
                            let current = Int(match.1) ?? 0
                            let total = Int(match.2) ?? 0
                            Task { @MainActor [weak self] in
                                self?.embeddingProgress = EmbeddingProgress(current: current, total: total)
                            }
                        }
                    }
                }

                try process.run()
                process.waitUntilExit()
                stderrPipe.fileHandleForReading.readabilityHandler = nil

                // Reopen database to pick up changes
                self.database = try DatabaseManager(path: vault.indexDBPath)
            } catch {
                errorLogger?.log("Failed to rebuild index", error: error)
                indexError = error.localizedDescription
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
        } catch {
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
                return
            }
        }
        // If original not found, create a new file
        let recoveredPath = vault.rootURL.appendingPathComponent("recovered-\(entry.documentID.prefix(8)).md")
        try? entry.content.write(to: recoveredPath, atomically: true, encoding: .utf8)
        refreshFiles()
        openDocument(at: recoveredPath)
        crashJournal?.clearSnapshot(documentID: entry.documentID)
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
        } catch {
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
        do {
            let data = try await runCLIAllowingNonZero(["lint", "--json", "--porcelain"], cwd: vault.rootURL)
            lintReport = try JSONDecoder().decode(LintReport.self, from: data)
        } catch {
            lintReport = LintReport(issues: [], filesChecked: 0, errors: 0, warnings: 0)
        }
        isLinting = false
    }

    func installSkills() async {
        guard let vault else { return }
        isInstallingSkills = true
        skillsInstallResult = nil
        do {
            let data = try await runCLI(["skills", "install", "--all", "--force"], cwd: vault.rootURL)
            let output = String(data: data, encoding: .utf8) ?? ""
            skillsInstallResult = output.isEmpty ? "Skills installed for all supported agents." : output
        } catch {
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
        try await withCheckedThrowingContinuation { continuation in
            let process = Process()
            process.executableURL = URL(fileURLWithPath: "/usr/local/bin/2nb")
            process.arguments = args
            process.currentDirectoryURL = cwd
            let stdout = Pipe()
            process.standardOutput = stdout
            process.standardError = FileHandle.nullDevice

            process.terminationHandler = { proc in
                if proc.terminationStatus == 0 {
                    let data = stdout.fileHandleForReading.readDataToEndOfFile()
                    continuation.resume(returning: data)
                } else {
                    continuation.resume(throwing: CLIError.nonZeroExit(proc.terminationStatus))
                }
            }

            do {
                try process.run()
            } catch {
                continuation.resume(throwing: error)
            }
        }
    }

    /// Like runCLI but returns stdout regardless of exit code.
    /// Needed for `2nb lint` which exits 2 on validation errors but still emits valid JSON.
    private func runCLIAllowingNonZero(_ args: [String], cwd: URL) async throws -> Data {
        try await withCheckedThrowingContinuation { continuation in
            let process = Process()
            process.executableURL = URL(fileURLWithPath: "/usr/local/bin/2nb")
            process.arguments = args
            process.currentDirectoryURL = cwd
            let stdout = Pipe()
            process.standardOutput = stdout
            process.standardError = FileHandle.nullDevice
            process.terminationHandler = { _ in
                continuation.resume(returning: stdout.fileHandleForReading.readDataToEndOfFile())
            }
            do {
                try process.run()
            } catch {
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
    let detail: String
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
