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
    var spotlightIndexer: SpotlightIndexer?
    var crashJournal: CrashJournal?
    var errorLogger: ErrorLogger?
    var recoveryEntries: [RecoveryEntry] = []
    var showRecoveryDialog = false
    var files: [FileItem] = []
    var outline: [HeadingItem] = []

    var currentDocument: DocumentTab? {
        guard activeTabIndex >= 0, activeTabIndex < openDocuments.count else { return nil }
        return openDocuments[activeTabIndex]
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
        let journal = CrashJournal(vaultDotDir: vm.dotDirURL)
        self.crashJournal = journal
        self.errorLogger = ErrorLogger(vaultDotDir: vm.dotDirURL)

        // Check for crash recovery entries
        let entries = journal.recoverableDocuments()
        if !entries.isEmpty {
            self.recoveryEntries = entries
            self.showRecoveryDialog = true
        }

        // Refresh file list
        refreshFiles()

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
        self.files = mdFiles.map { url in
            FileItem(
                url: url,
                name: url.deletingPathExtension().lastPathComponent,
                relativePath: vault.relativePath(for: url)
            )
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
        do {
            try vault.runIndex()
            // Reopen database to pick up changes
            self.database = try DatabaseManager(path: vault.indexDBPath)
        } catch {
            print("Failed to rebuild index: \(error)")
        }
    }

    func saveCurrentDocument() {
        guard let tab = currentDocument else { return }
        let idx = activeTabIndex
        do {
            try openDocuments[idx].content.write(to: tab.url, atomically: true, encoding: .utf8)
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
