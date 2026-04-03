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

    func createNewDocument() {
        guard let vault else { return }
        let title = "Untitled"
        let id = UUID().uuidString
        let now = ISO8601DateFormatter().string(from: Date())

        let content = """
        ---
        id: \(id)
        title: \(title)
        type: note
        status: draft
        tags: []
        created: \(now)
        modified: \(now)
        ---
        # \(title)

        """

        let filename = "untitled-\(id.prefix(8)).md"
        let url = vault.rootURL.appendingPathComponent(filename)

        do {
            try content.write(to: url, atomically: true, encoding: .utf8)
            refreshFiles()
            openDocument(at: url)
        } catch {
            print("Failed to create: \(error)")
        }
    }

    func saveCurrentDocument() {
        guard let tab = currentDocument else { return }
        let idx = activeTabIndex
        do {
            try openDocuments[idx].content.write(to: tab.url, atomically: true, encoding: .utf8)
            openDocuments[idx].isDirty = false
        } catch {
            print("Failed to save: \(error)")
        }
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
