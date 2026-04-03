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
                    // Try to open last vault or show welcome
                    if let lastVault = UserDefaults.standard.string(forKey: "lastVaultPath") {
                        appState.openVault(at: URL(fileURLWithPath: lastVault))
                    }
                }
        }
        .commands {
            CommandGroup(replacing: .newItem) {
                Button("Open Vault...") {
                    openVaultPanel()
                }
                .keyboardShortcut("o", modifiers: [.command, .shift])

                Divider()

                Button("New Document") {
                    appState.createNewDocument()
                }
                .keyboardShortcut("n", modifiers: .command)
                .disabled(appState.vault == nil)
            }

            CommandGroup(replacing: .saveItem) {
                Button("Save") {
                    appState.saveCurrentDocument()
                }
                .keyboardShortcut("s", modifiers: .command)
                .disabled(appState.currentDocument == nil)
            }
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
}
