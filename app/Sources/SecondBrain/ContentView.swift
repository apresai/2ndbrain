import SwiftUI
import SecondBrainCore
import UniformTypeIdentifiers

struct ContentView: View {
    @Environment(AppState.self) var appState
    @State private var showProperties = false
    @State private var focusChromeVisible = false

    private var anyOverlayVisible: Bool {
        appState.showSearch || appState.showQuickOpen || appState.showCommandPalette || appState.showAskAI || appState.showTemplatePicker || appState.showAISetupWizard
    }

    /// Handle .md files dropped onto the editor window. We load each URL
    /// asynchronously (loadObject is off the main thread) and then jump back
    /// to MainActor to mutate AppState.
    private func handleFileDrop(providers: [NSItemProvider]) -> Bool {
        var handled = false
        for provider in providers {
            guard provider.canLoadObject(ofClass: URL.self) else { continue }
            _ = provider.loadObject(ofClass: URL.self) { url, _ in
                guard let url, url.pathExtension.lowercased() == "md" else { return }
                Task { @MainActor in
                    appState.openDocument(at: url)
                }
            }
            handled = true
        }
        return handled
    }

    var body: some View {
        ZStack {
            mainContent
                .onDrop(of: [.fileURL], isTargeted: nil) { providers in
                    handleFileDrop(providers: providers)
                }

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
                .frame(minWidth: 900, minHeight: 600, idealHeight: 700)
                .overlay(alignment: .topLeading) {
                    Button {
                        appState.showGraphView = false
                    } label: {
                        Image(systemName: "xmark.circle.fill")
                            .font(.title2)
                            .foregroundStyle(.secondary)
                    }
                    .buttonStyle(.plain)
                    .keyboardShortcut(.cancelAction)
                    .padding(8)
                }
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
        .sheet(isPresented: Binding(
            get: { appState.showMCPStatus },
            set: { appState.showMCPStatus = $0 }
        )) {
            MCPStatusView(isPresented: Binding(
                get: { appState.showMCPStatus },
                set: { appState.showMCPStatus = $0 }
            ))
            .environment(appState)
        }
        .sheet(isPresented: Binding(
            get: { appState.showSuggestLinks },
            set: { appState.showSuggestLinks = $0 }
        )) {
            SuggestLinksView(isPresented: Binding(
                get: { appState.showSuggestLinks },
                set: { appState.showSuggestLinks = $0 }
            ))
            .environment(appState)
        }
        .sheet(isPresented: Binding(
            get: { appState.showPolish },
            set: { appState.showPolish = $0 }
        )) {
            PolishView(isPresented: Binding(
                get: { appState.showPolish },
                set: { appState.showPolish = $0 }
            ))
            .environment(appState)
        }
        .sheet(isPresented: Binding(
            get: { appState.showGitActivity },
            set: { appState.showGitActivity = $0 }
        )) {
            GitActivityView(isPresented: Binding(
                get: { appState.showGitActivity },
                set: { appState.showGitActivity = $0 }
            ))
            .environment(appState)
        }
        .sheet(isPresented: Binding(
            get: { appState.showGitDiff },
            set: { appState.showGitDiff = $0 }
        )) {
            GitDiffView(
                isPresented: Binding(
                    get: { appState.showGitDiff },
                    set: { appState.showGitDiff = $0 }
                ),
                relPath: appState.gitDiffPath
            )
            .environment(appState)
        }
        .sheet(isPresented: Binding(
            get: { appState.showCommitDetail },
            set: { newValue in
                if newValue {
                    appState.showCommitDetail = true
                } else {
                    // Any dismissal (Done button, Escape, SwiftUI drag,
                    // outside-click) flows through this setter. Route
                    // through closeCommitDetail so the in-flight git-show
                    // Task gets cancelled before a reopen can race it.
                    appState.closeCommitDetail()
                }
            }
        )) {
            CommitDetailView(isPresented: Binding(
                get: { appState.showCommitDetail },
                set: { appState.showCommitDetail = $0 }
            ))
            .environment(appState)
        }
        .sheet(isPresented: Binding(
            get: { appState.showIndexProgress },
            set: { appState.showIndexProgress = $0 }
        )) {
            IndexProgressView(isPresented: Binding(
                get: { appState.showIndexProgress },
                set: { appState.showIndexProgress = $0 }
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
            // Focus mode: just the editor. Chrome (tab bar + breadcrumb)
            // auto-reveals when the mouse reaches the top edge, then fades
            // out when the user moves back into the body.
            ZStack(alignment: .top) {
                Group {
                    if appState.openDocuments.isEmpty {
                        WelcomeView()
                    } else {
                        EditorArea()
                    }
                }

                if focusChromeVisible && !appState.openDocuments.isEmpty {
                    VStack(spacing: 0) {
                        TabBarView()
                        BreadcrumbBar()
                    }
                    .background(.regularMaterial)
                    .transition(.move(edge: .top).combined(with: .opacity))
                }
            }
            .onContinuousHover { phase in
                guard case .active(let location) = phase else {
                    if focusChromeVisible {
                        withAnimation(.easeInOut(duration: 0.2)) {
                            focusChromeVisible = false
                        }
                    }
                    return
                }
                let shouldShow = location.y < 50
                if shouldShow != focusChromeVisible {
                    withAnimation(.easeInOut(duration: 0.2)) {
                        focusChromeVisible = shouldShow
                    }
                }
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
                        BreadcrumbBar()
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
