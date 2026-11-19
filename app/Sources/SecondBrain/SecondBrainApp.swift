import SwiftUI
import SecondBrainCore

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
                    appDelegate.configure(appState: appState)
                    // If an explicit test or CLI override vault was passed, open it immediately
                    if let vaultOverride = LaunchOptions.current.vaultPath {
                        appState.openVault(at: URL(fileURLWithPath: vaultOverride), persistLastVault: false)
                        return
                    }
                    // Under test mode with no explicit vault override, do not fall back to live Obsidian
                    guard ProcessInfo.processInfo.environment["2NB_TEST"] != "1" else { return }

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
                .onChange(of: appState.showQuickSearch) { _, show in
                    if show {
                        appState.showQuickSearch = false
                        appDelegate.toggleQuickSearch()
                    }
                }
        }

        Settings {
            SettingsView()
                .environment(appState)
        }
        .windowResizability(.contentSize)

        .commands {
            // Suppress the default "New" (Cmd+N) and "Save" (Cmd+S) items.
            // The dashboard has no document editor, so an empty `replacing:`
            // group removes the system defaults rather than letting SwiftUI
            // restore "New Window"/"Save" in the (renamed) Notes menu.
            CommandGroup(replacing: .newItem) {}
            CommandGroup(replacing: .saveItem) {}

            // Vault menu — vault lifecycle, index sync, and Obsidian handoff.
            CommandMenu("Vault") {
                Button("New Vault...") {
                    newVaultPanel()
                }

                Button("Open Vault...") {
                    openVaultPanel()
                }
                .keyboardShortcut("o", modifiers: [.command, .shift])

                Divider()

                Button("Open in Obsidian") {
                    appState.openVaultInObsidian()
                }
                .disabled(appState.vault == nil)

                Button("Reveal Vault in Finder") {
                    if let vault = appState.vault {
                        NSWorkspace.shared.activateFileViewerSelecting([vault.rootURL])
                    }
                }
                .disabled(appState.vault == nil)

                Divider()

                Button("Sync Index") {
                    appState.rebuildIndex()
                }
                .disabled(appState.vault == nil || appState.isIndexing)

                Button("Re-embed All…") {
                    appState.rebuildIndex(forceReembed: true)
                }
                .disabled(appState.vault == nil || appState.isIndexing)

                Button("Validate Links & Schema") {
                    appState.showLintResults = true
                }
                .keyboardShortcut("l", modifiers: [.command, .shift])
                .disabled(appState.vault == nil)

                Divider()

                Button("Run Setup Guide…") {
                    appState.showOnboarding = true
                }
            }

            // View menu — merged into the system View menu via CommandGroup.
            CommandGroup(after: .toolbar) {
                Button("Home") {
                    appState.requestedTab = .home
                }
                .keyboardShortcut("1", modifiers: .command)
                .disabled(appState.vault == nil)

                Button("Ask & Search") {
                    appState.requestedTab = .askSearch
                }
                .keyboardShortcut("2", modifiers: .command)
                .disabled(appState.vault == nil)

                Button("Notes") {
                    appState.requestedTab = .notes
                }
                .keyboardShortcut("3", modifiers: .command)
                .disabled(appState.vault == nil)

                Button("Activity") {
                    appState.requestedTab = .activity
                }
                .keyboardShortcut("4", modifiers: .command)
                .disabled(appState.vault == nil)

                Divider()

                Button("GUI Actions & Status") {
                    appState.showGUIActions = true
                }
                .keyboardShortcut("j", modifiers: [.command, .shift])
                .disabled(appState.vault == nil)

                Button("Recent Git Activity") {
                    appState.openGitActivity()
                }
                .keyboardShortcut("g", modifiers: [.command, .shift])
                .disabled(appState.vault == nil)
            }

            // AI menu — Gemini capabilities, Settings, and AI client wiring.
            CommandMenu("AI") {
                Button("Ask & Search Vault…") {
                    appState.openAskGemini()
                }
                .keyboardShortcut("k", modifiers: [.command, .shift])
                .disabled(appState.vault == nil)

                Button("Search Vault…") {
                    appState.openHybridSearch()
                }
                .keyboardShortcut("f", modifiers: [.command, .shift])
                .disabled(appState.vault == nil)

                Button("Quick Ask & Search Panel") {
                    appState.showQuickSearch = true
                }

                Button("Polish Note with Gemini…") {
                    appState.showNotePolish = true
                }
                .keyboardShortcut("p", modifiers: [.command, .shift])
                .disabled(appState.vault == nil)

                Divider()

                SettingsMenuButton("Gemini Settings…", appState: appState, tab: .ai)
                    .keyboardShortcut(",", modifiers: [.command, .shift])

                Button("AI Usage & Cost…") {
                    appState.showAIUsage = true
                }
                .keyboardShortcut("u", modifiers: [.command, .shift])
                .disabled(appState.vault == nil)

                SettingsMenuButton("AI Client Integrations…", appState: appState, tab: .integrations)

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
}

/// Menu bar button that selects a Settings tab and opens the Cmd+, window.
private struct SettingsMenuButton: View {
    @Environment(\.openSettings) private var openSettings
    let title: String
    let appState: AppState
    let tab: SettingsTab

    init(_ title: String, appState: AppState, tab: SettingsTab) {
        self.title = title
        self.appState = appState
        self.tab = tab
    }

    var body: some View {
        Button(title) {
            appState.noteSettingsOpened(tab: tab)
            openSettings()
        }
    }
}
