import SwiftUI
import SecondBrainCore

@main
struct SecondBrainApp: App {
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
        .commands {
            CommandGroup(replacing: .newItem) {
                Button("New Vault...") {
                    newVaultPanel()
                }

                Button("Open Vault...") {
                    openVaultPanel()
                }
                .keyboardShortcut("o", modifiers: [.command, .shift])

                Divider()

                Button("New Document...") {
                    appState.showTemplatePicker = true
                }
                .keyboardShortcut("n", modifiers: .command)
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

            CommandMenu("Export") {
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

            CommandGroup(replacing: .saveItem) {
                Button("Save") {
                    appState.saveCurrentDocument()
                }
                .keyboardShortcut("s", modifiers: .command)
                .disabled(appState.currentDocument == nil)
            }

            CommandMenu("Tools") {
                Button("Set Up AI...") {
                    appState.showAISetupWizard = true
                }
                .disabled(appState.vault == nil)

                Button("Rebuild Index") {
                    appState.rebuildIndex()
                }
                .disabled(appState.vault == nil || appState.isIndexing)

                Divider()

                Button("Install AI Agent Skills...") {
                    appState.showSkillsInstall = true
                }
                .disabled(appState.vault == nil)

                Button("Connect AI Tools (MCP)...") {
                    appState.showMCPSetup = true
                }
                .disabled(appState.vault == nil)

                Divider()

                Button("Validate Knowledge Base...") {
                    appState.showLintResults = true
                }
                .disabled(appState.vault == nil)
            }

            CommandMenu("View") {
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

                Button("Ask AI") {
                    appState.showAskAI.toggle()
                }
                .keyboardShortcut("a", modifiers: [.command, .shift])

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

                Button("Toggle Sidebar") {
                    appState.sidebarVisible.toggle()
                }
                .keyboardShortcut("\\", modifiers: .command)
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
        process.executableURL = URL(fileURLWithPath: "/usr/local/bin/2nb")
        process.arguments = args
        if let cwd { process.currentDirectoryURL = cwd }

        process.terminationHandler = { proc in
            let success = proc.terminationStatus == 0
            Task { @MainActor in
                completion(success)
            }
        }

        do {
            try process.run()
        } catch {
            print("CLI command failed: \(error)")
            completion(false)
        }
    }
}

