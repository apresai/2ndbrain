import SwiftUI
import SecondBrainCore

struct ContentView: View {
    @Environment(AppState.self) var appState

    var body: some View {
        NavigationSplitView {
            SidebarView()
        } detail: {
            if appState.openDocuments.isEmpty {
                WelcomeView()
            } else {
                VStack(spacing: 0) {
                    TabBarView()
                    EditorArea()
                    StatusBarView()
                }
            }
        }
        .navigationSplitViewStyle(.balanced)
    }
}

// MARK: - Welcome View

struct WelcomeView: View {
    @Environment(AppState.self) var appState

    var body: some View {
        VStack(spacing: 16) {
            Image(systemName: "brain.head.profile")
                .font(.system(size: 64))
                .foregroundStyle(.secondary)

            Text("2ndbrain")
                .font(.largeTitle)
                .fontWeight(.bold)

            Text("AI-Native Markdown Editor")
                .font(.title3)
                .foregroundStyle(.secondary)

            if appState.vault == nil {
                Button("Open Vault") {
                    openVaultPanel()
                }
                .buttonStyle(.borderedProminent)
                .controlSize(.large)
                .padding(.top, 8)
            } else {
                Text("Select a document from the sidebar or create a new one.")
                    .foregroundStyle(.tertiary)
            }
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
    }

    private func openVaultPanel() {
        let panel = NSOpenPanel()
        panel.canChooseFiles = false
        panel.canChooseDirectories = true
        panel.allowsMultipleSelection = false
        if panel.runModal() == .OK, let url = panel.url {
            appState.openVault(at: url)
            UserDefaults.standard.set(url.path, forKey: "lastVaultPath")
        }
    }
}
