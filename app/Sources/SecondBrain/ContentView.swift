import SwiftUI
import SecondBrainCore

struct ContentView: View {
    @Environment(AppState.self) var appState
    @State private var showProperties = false

    private var anyOverlayVisible: Bool {
        appState.showSearch || appState.showQuickOpen || appState.showCommandPalette || appState.showAskAI || appState.showTemplatePicker || appState.showAISetupWizard
    }

    var body: some View {
        ZStack {
            mainContent

            // Modal overlays
            if appState.showSearch {
                overlayBackground { appState.showSearch = false }
                SearchPanelView(isPresented: Binding(
                    get: { appState.showSearch },
                    set: { appState.showSearch = $0 }
                ))
            }
            if appState.showQuickOpen {
                overlayBackground { appState.showQuickOpen = false }
                QuickOpenView(isPresented: Binding(
                    get: { appState.showQuickOpen },
                    set: { appState.showQuickOpen = $0 }
                ))
            }
            if appState.showCommandPalette {
                overlayBackground { appState.showCommandPalette = false }
                CommandPaletteView(isPresented: Binding(
                    get: { appState.showCommandPalette },
                    set: { appState.showCommandPalette = $0 }
                ))
            }
            if appState.showAskAI {
                overlayBackground { appState.showAskAI = false }
                AskAIView(isPresented: Binding(
                    get: { appState.showAskAI },
                    set: { appState.showAskAI = $0 }
                ))
            }
            if appState.showTemplatePicker {
                overlayBackground { appState.showTemplatePicker = false }
                TemplatePicker(isPresented: Binding(
                    get: { appState.showTemplatePicker },
                    set: { appState.showTemplatePicker = $0 }
                ))
            }
            if appState.showAISetupWizard {
                overlayBackground { appState.showAISetupWizard = false }
                AISetupWizardView(isPresented: Binding(
                    get: { appState.showAISetupWizard },
                    set: { appState.showAISetupWizard = $0 }
                ))
            }
        }
        .onChange(of: anyOverlayVisible) { _, visible in
            if visible {
                NSApp.keyWindow?.makeFirstResponder(nil)
            }
        }
        .sheet(isPresented: Binding(
            get: { appState.showGraphView },
            set: { appState.showGraphView = $0 }
        )) {
            GraphView()
                .environment(appState)
                .frame(minWidth: 600, minHeight: 400)
        }
        .sheet(isPresented: $showProperties) {
            PropertiesView(isPresented: $showProperties)
                .environment(appState)
        }
        .sheet(isPresented: Binding(
            get: { appState.showLintResults },
            set: { appState.showLintResults = $0 }
        )) {
            LintResultsView(isPresented: Binding(
                get: { appState.showLintResults },
                set: { appState.showLintResults = $0 }
            ))
            .environment(appState)
            .onAppear { Task { await appState.runLint() } }
        }
        .sheet(isPresented: Binding(
            get: { appState.showSkillsInstall },
            set: { appState.showSkillsInstall = $0 }
        )) {
            SkillsInstallView(isPresented: Binding(
                get: { appState.showSkillsInstall },
                set: { appState.showSkillsInstall = $0 }
            ))
            .environment(appState)
        }
        .sheet(isPresented: Binding(
            get: { appState.showMCPSetup },
            set: { appState.showMCPSetup = $0 }
        )) {
            MCPSetupView(isPresented: Binding(
                get: { appState.showMCPSetup },
                set: { appState.showMCPSetup = $0 }
            ))
            .environment(appState)
        }
        .alert("Crash Recovery", isPresented: Binding(
            get: { appState.showRecoveryDialog },
            set: { appState.showRecoveryDialog = $0 }
        )) {
            Button("Recover All") {
                for entry in appState.recoveryEntries {
                    appState.recoverDocument(entry)
                }
                appState.showRecoveryDialog = false
            }
            Button("Discard", role: .destructive) {
                appState.dismissRecovery()
            }
        } message: {
            Text("\(appState.recoveryEntries.count) document(s) have unsaved changes from a previous session.")
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
