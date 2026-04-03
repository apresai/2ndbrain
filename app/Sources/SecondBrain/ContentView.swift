import SwiftUI
import SecondBrainCore

struct ContentView: View {
    @Environment(AppState.self) var appState
    @State private var showSearch = false
    @State private var showQuickOpen = false
    @State private var showCommandPalette = false
    @State private var showProperties = false

    var body: some View {
        ZStack {
            mainContent

            // Modal overlays
            if showSearch {
                overlayBackground { showSearch = false }
                SearchPanelView(isPresented: $showSearch)
            }
            if showQuickOpen {
                overlayBackground { showQuickOpen = false }
                QuickOpenView(isPresented: $showQuickOpen)
            }
            if showCommandPalette {
                overlayBackground { showCommandPalette = false }
                CommandPaletteView(isPresented: $showCommandPalette)
            }
        }
        .sheet(isPresented: $showProperties) {
            PropertiesView(isPresented: $showProperties)
                .environment(appState)
        }
        .onKeyPress(keys: [.init("f")], phases: .down) { press in
            if press.modifiers.contains([.command, .shift]) {
                showSearch.toggle()
                return .handled
            }
            return .ignored
        }
        .onKeyPress(keys: [.init("p")], phases: .down) { press in
            if press.modifiers.contains([.command, .shift]) {
                showCommandPalette.toggle()
                return .handled
            }
            if press.modifiers.contains(.command) {
                showQuickOpen.toggle()
                return .handled
            }
            return .ignored
        }
        .onKeyPress(keys: [.init("e")], phases: .down) { press in
            if press.modifiers.contains([.command, .shift]) {
                appState.focusModeActive.toggle()
                return .handled
            }
            return .ignored
        }
        .onKeyPress(keys: [.init("i")], phases: .down) { press in
            if press.modifiers.contains([.command, .option]) {
                showProperties.toggle()
                return .handled
            }
            return .ignored
        }
    }

    @ViewBuilder
    private var mainContent: some View {
        if appState.focusModeActive {
            // Focus mode: just the editor, no chrome
            if appState.openDocuments.isEmpty {
                WelcomeView()
            } else {
                EditorArea()
            }
        } else {
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

    private func overlayBackground(onTap: @escaping () -> Void) -> some View {
        Color.black.opacity(0.3)
            .ignoresSafeArea()
            .onTapGesture(perform: onTap)
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
