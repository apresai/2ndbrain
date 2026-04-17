import SwiftUI
import SecondBrainCore
import os

@main
struct SecondBrainApp: App {
    @NSApplicationDelegateAdaptor(AppDelegate.self) private var appDelegate
    @State private var appState = AppState()
    var body: some Scene {
        WindowGroup {
            ContentView()
                .environment(appState)
                .frame(minWidth: 800, minHeight: 500)
                .onAppear {
                    if let lastVault = UserDefaults.standard.string(forKey: "lastVaultPath") {
                        appState.openVault(at: URL(fileURLWithPath: lastVault))
                    }
                }
        }

        Settings {
            PreferencesView()
                .environment(appState)
        }

        .commands {
            // Notes menu (renamed from File via AppDelegate). Only note-scoped
            // operations live here; vault operations moved to the Vault menu.
            CommandGroup(replacing: .newItem) {
                Button("New Note...") {
                    appState.showTemplatePicker = true
                }
                .keyboardShortcut("n", modifiers: .command)
                .disabled(appState.vault == nil)

                Button("Duplicate Note") {
                    if let url = appState.currentDocument?.url {
                        appState.duplicateDocument(at: url)
                    }
                }
                .disabled(appState.currentDocument == nil)

                Divider()

                Menu("Export") {
                    Button("Export as PDF...") {
                        exportCurrentDocument(format: .pdf)
                    }
                    .keyboardShortcut("x", modifiers: [.command, .shift])
                    .disabled(appState.currentDocument == nil)

                    Button("Export as HTML...") {
                        exportCurrentDocument(format: .html)
                    }
                    .disabled(appState.currentDocument == nil)

                    Button("Export as Markdown...") {
                        exportCurrentDocument(format: .markdown)
                    }
                    .disabled(appState.currentDocument == nil)
                }

                Divider()

                Button("Reveal Note in Finder") {
                    if let url = appState.currentDocument?.url {
                        NSWorkspace.shared.activateFileViewerSelecting([url])
                    }
                }
                .keyboardShortcut("r", modifiers: [.command, .shift])
                .disabled(appState.currentDocument == nil)
            }

            CommandGroup(replacing: .saveItem) {
                Button("Save") {
                    appState.saveCurrentDocument()
                }
                .keyboardShortcut("s", modifiers: .command)
                .disabled(appState.currentDocument == nil)
            }

            // Vault menu — all vault-scoped operations (create, open, health,
            // import/export) in one place. Split out from File/Tools.
            CommandMenu("Vault") {
                Button("New Vault...") {
                    newVaultPanel()
                }

                Button("Open Vault...") {
                    openVaultPanel()
                }
                .keyboardShortcut("o", modifiers: [.command, .shift])

                Divider()

                Button("Reveal Vault in Finder") {
                    if let vault = appState.vault {
                        NSWorkspace.shared.activateFileViewerSelecting([vault.rootURL])
                    }
                }
                .disabled(appState.vault == nil)

                Button("Vault Status...") {
                    appState.showVaultStatus = true
                }
                .disabled(appState.vault == nil)

                Divider()

                Button("Rebuild Index") {
                    appState.rebuildIndex()
                }
                .disabled(appState.vault == nil || appState.isIndexing)

                Button("Validate Vault...") {
                    appState.showLintResults = true
                }
                .disabled(appState.vault == nil)

                Divider()

                Button("Import Obsidian Vault...") {
                    importObsidianPanel()
                }

                Button("Export to Obsidian...") {
                    exportObsidianPanel()
                }
                .disabled(appState.vault == nil)
            }

            // View menu — merged into the system View menu via
            // CommandGroup(after:) rather than CommandMenu, which would create
            // a duplicate View menu in the menu bar.
            CommandGroup(after: .toolbar) {
                Button("Toggle Sidebar") {
                    appState.sidebarVisible.toggle()
                }
                .keyboardShortcut("\\", modifiers: .command)

                // Cmd+1/2/3/4 switch sidebar panels. Auto-unhide the
                // sidebar if it's currently hidden — otherwise the
                // shortcut would silently no-op.
                Button("Show Files Panel") {
                    appState.sidebarVisible = true
                    appState.requestedSidebarPanel = .files
                }
                .keyboardShortcut("1", modifiers: .command)

                Button("Show Outline Panel") {
                    appState.sidebarVisible = true
                    appState.requestedSidebarPanel = .outline
                }
                .keyboardShortcut("2", modifiers: .command)

                Button("Show Links Panel") {
                    appState.sidebarVisible = true
                    appState.requestedSidebarPanel = .backlinks
                }
                .keyboardShortcut("3", modifiers: .command)

                Button("Show Tags Panel") {
                    appState.sidebarVisible = true
                    appState.requestedSidebarPanel = .tags
                }
                .keyboardShortcut("4", modifiers: .command)

                Divider()

                Button("Search Vault") {
                    appState.showSearch.toggle()
                }
                .keyboardShortcut("f", modifiers: [.command, .shift])

                Button("Quick Open") {
                    appState.showQuickOpen.toggle()
                }
                .keyboardShortcut("p", modifiers: .command)

                Button("Command Palette") {
                    appState.showCommandPalette.toggle()
                }
                .keyboardShortcut("p", modifiers: [.command, .shift])

                Divider()

                Button("Recent Activity") {
                    appState.openGitActivity()
                }
                .keyboardShortcut("g", modifiers: [.command, .shift])
                .disabled(appState.vault == nil)

                Button("Graph View") {
                    appState.showGraphView = true
                }
                .keyboardShortcut("g", modifiers: [.command, .option])
                .disabled(appState.vault == nil)

                Divider()

                Button("Focus Mode") {
                    appState.focusModeActive.toggle()
                }
                .keyboardShortcut("e", modifiers: [.command, .shift])

                Button("Typewriter Mode") {
                    appState.typewriterModeActive.toggle()
                }
                .keyboardShortcut("t", modifiers: [.command, .shift])

                Button("Inline Preview") {
                    appState.inlineRenderingEnabled.toggle()
                }
                .keyboardShortcut("r", modifiers: [.command, .option])

                Divider()

                Button("Zoom In") {
                    appState.increaseFontSize()
                }
                .keyboardShortcut("=", modifiers: .command)

                Button("Zoom Out") {
                    appState.decreaseFontSize()
                }
                .keyboardShortcut("-", modifiers: .command)

                Button("Actual Size") {
                    appState.resetFontSize()
                }
                .keyboardShortcut("0", modifiers: .command)
            }

            // AI menu — all AI-related actions (ask, suggest, polish, setup,
            // test, skills, MCP). Replaces the previous Tools menu.
            CommandMenu("AI") {
                Button("Ask AI...") {
                    appState.showAskAI.toggle()
                }
                .keyboardShortcut("a", modifiers: [.command, .shift])
                .disabled(appState.vault == nil)

                Button("Suggest Links") {
                    appState.openSuggestLinks()
                }
                .keyboardShortcut("l", modifiers: [.command, .shift])
                .disabled(appState.currentDocument == nil)

                Button("Polish Document") {
                    appState.openPolish()
                }
                .keyboardShortcut("p", modifiers: [.command, .option])
                .disabled(appState.currentDocument == nil)

                Divider()

                Button("AI Setup...") {
                    appState.showAISetupWizard = true
                }
                .disabled(appState.vault == nil)

                Button("Test AI Connection...") {
                    appState.showAITest = true
                }
                .disabled(appState.vault == nil)

                Divider()

                Button("AI Agent Skills...") {
                    appState.showSkillsInstall = true
                }
                .disabled(appState.vault == nil)

                Button("MCP Server Configuration...") {
                    appState.showMCPSetup = true
                }
                .disabled(appState.vault == nil)

                Button("MCP Server Status...") {
                    appState.showMCPStatus = true
                }
                .keyboardShortcut("m", modifiers: [.command, .shift])
                .disabled(appState.vault == nil)
            }
        }
    }

    private func newVaultPanel() {
        let panel = NSOpenPanel()
        panel.canChooseFiles = false
        panel.canChooseDirectories = true
        panel.allowsMultipleSelection = false
        panel.canCreateDirectories = true
        panel.message = "Select or create a directory for the new vault"
        panel.prompt = "Create Vault"

        if panel.runModal() == .OK, let url = panel.url {
            appState.createVault(at: url)
        }
    }

    private func openVaultPanel() {
        let panel = NSOpenPanel()
        panel.canChooseFiles = false
        panel.canChooseDirectories = true
        panel.allowsMultipleSelection = false
        panel.message = "Select a 2ndbrain vault directory"
        panel.prompt = "Open Vault"

        if panel.runModal() == .OK, let url = panel.url {
            appState.openVault(at: url)
            UserDefaults.standard.set(url.path, forKey: "lastVaultPath")
        }
    }

    private func importObsidianPanel() {
        let panel = NSOpenPanel()
        panel.canChooseFiles = false
        panel.canChooseDirectories = true
        panel.allowsMultipleSelection = false
        panel.message = "Select an Obsidian vault to import"
        panel.prompt = "Import"

        if panel.runModal() == .OK, let url = panel.url {
            runCLICommand(["import-obsidian", url.path]) { success in
                if success {
                    appState.openVault(at: url)
                    UserDefaults.standard.set(url.path, forKey: "lastVaultPath")
                }
            }
        }
    }

    private func exportObsidianPanel() {
        let panel = NSOpenPanel()
        panel.canChooseFiles = false
        panel.canChooseDirectories = true
        panel.canCreateDirectories = true
        panel.allowsMultipleSelection = false
        panel.message = "Select a directory to export the vault as Obsidian format"
        panel.prompt = "Export"

        if panel.runModal() == .OK, let url = panel.url {
            guard let vault = appState.vault else { return }
            runCLICommand(["export-obsidian", url.path], cwd: vault.rootURL) { _ in }
        }
    }

    private enum ExportFormat { case pdf, html, markdown }

    private func exportCurrentDocument(format: ExportFormat) {
        guard let tab = appState.currentDocument else { return }
        let name = tab.url.lastPathComponent
        let (_, body) = FrontmatterParser.parse(tab.content)
        let html = MarkdownRenderer.renderHTML(body)

        switch format {
        case .pdf:
            ExportController.exportPDF(html: html, suggestedName: name)
        case .html:
            ExportController.exportHTML(html: html, suggestedName: name)
        case .markdown:
            ExportController.exportMarkdown(content: tab.content, suggestedName: name)
        }
    }

    @MainActor
    private func runCLICommand(_ args: [String], cwd: URL? = nil, completion: @escaping @MainActor (Bool) -> Void) {
        let process = Process()
        process.executableURL = URL(fileURLWithPath: CLIPath.resolve())
        if let cwd {
            process.arguments = CLIPath.args(args, vault: cwd)
            process.currentDirectoryURL = cwd
        } else {
            process.arguments = args
        }

        process.terminationHandler = { proc in
            let success = proc.terminationStatus == 0
            Task { @MainActor in
                completion(success)
            }
        }

        do {
            try process.run()
        } catch {
            Logger(subsystem: "dev.apresai.2ndbrain", category: "app").error("CLI command failed: \(error.localizedDescription)")
            completion(false)
        }
    }
}

