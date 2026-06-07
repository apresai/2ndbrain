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
                    // Bind to the vault Obsidian currently has open (the source
                    // of truth), so the dashboard follows Obsidian. Fall back to
                    // the last-used vault when Obsidian isn't installed or has no
                    // open vault.
                    if let obsidian = ObsidianRegistry.load()?.openVault, obsidian.exists {
                        appState.openVault(at: obsidian.url)
                        UserDefaults.standard.set(obsidian.path, forKey: "lastVaultPath")
                    } else if let lastVault = UserDefaults.standard.string(forKey: "lastVaultPath") {
                        appState.openVault(at: URL(fileURLWithPath: lastVault))
                    }
                }
        }

        Settings {
            PreferencesView()
                .environment(appState)
        }

        .commands {
            // Suppress the default "New" (Cmd+N) and "Save" (Cmd+S) items.
            // The 0.5.0 dashboard has no document editor, so an empty
            // `replacing:` group removes the system defaults rather than
            // letting SwiftUI restore "New Window"/"Save" in the (renamed)
            // Notes menu.
            CommandGroup(replacing: .newItem) {}
            CommandGroup(replacing: .saveItem) {}

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
                .disabled(appState.vault == nil)

                Button("Export to Obsidian...") {
                    exportObsidianPanel()
                }
                .disabled(appState.vault == nil)
            }

            // View menu — merged into the system View menu via
            // CommandGroup(after:) rather than CommandMenu, which would create
            // a duplicate View menu in the menu bar.
            CommandGroup(after: .toolbar) {
                Button("Recent Activity") {
                    appState.openGitActivity()
                }
                .keyboardShortcut("g", modifiers: [.command, .shift])
                .disabled(appState.vault == nil)
            }

            // AI menu — all AI-related actions (ask, suggest, polish, setup,
            // test, skills, MCP). Replaces the previous Tools menu.
            CommandMenu("AI") {
                // Merged AI Hub — replaces AI Setup, Test AI Connection,
                // and Model Wizard. Single surface for providers, active
                // models, and the catalog.
                Button("AI...") {
                    appState.showAIHub = true
                }
                .keyboardShortcut(",", modifiers: [.command, .shift])
                .disabled(appState.vault == nil)

                Divider()

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
            appState.openPickedVault(at: url)
        }
    }

    private func importObsidianPanel() {
        guard let vault = appState.vault else { return }
        let panel = NSOpenPanel()
        panel.canChooseFiles = false
        panel.canChooseDirectories = true
        panel.allowsMultipleSelection = false
        panel.message = "Select an Obsidian vault to import"
        panel.prompt = "Import"

        if panel.runModal() == .OK, let url = panel.url {
            runCLICommand(["import-obsidian", url.path], cwd: vault.rootURL) { success in
                if success {
                    appState.openVault(at: vault.rootURL)
                    UserDefaults.standard.set(vault.rootURL.path, forKey: "lastVaultPath")
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
