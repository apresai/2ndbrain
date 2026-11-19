import SwiftUI
import SecondBrainCore
import UniformTypeIdentifiers

/// The dashboard's sidebar.
///
/// Focused Gemini-first companion layout (Beta): four capability-oriented tabs
/// (`Home`, `Ask & Search`, `Notes`, `Activity`), while all configuration lives
/// in the dedicated Settings window (Cmd+,).
enum DashboardTab: String, CaseIterable, Identifiable {
    case home = "Home"
    case askSearch = "Ask & Search"
    case notes = "Notes"
    case activity = "Activity"

    var id: String { self.rawValue }

    /// Everything below Home in the sidebar Workspace section.
    static var secondary: [DashboardTab] { [.askSearch, .notes, .activity] }

    var systemImage: String {
        switch self {
        case .home: return "house"
        case .askSearch: return "sparkles"
        case .notes: return "checkmark.seal"
        case .activity: return "clock.arrow.circlepath"
        }
    }
}

/// Compact pill badge marking the focused Beta GUI surface.
struct BetaBadge: View {
    var body: some View {
        Text("BETA")
            .font(.system(size: 9, weight: .bold))
            .tracking(0.6)
            .foregroundStyle(.orange)
            .padding(.horizontal, 6)
            .padding(.vertical, 2)
            .background(Color.orange.opacity(0.14), in: Capsule())
    }
}

struct ContentView: View {
    @Environment(AppState.self) var appState
    @Environment(\.openSettings) private var openSettings
    @State private var selection: DashboardTab = .home
    // Owned here so menu items and Home quick actions can deep-link to a
    // specific pane inside a group tab, and so the pane survives switching tabs.
    @State private var notesSection: NotesView.Section = .linksAndSchema
    @State private var activitySection: ActivityView.Section = .actions
    @State private var checkedFirstRunOnboarding = false

    var body: some View {
        @Bindable var state = appState
        mainLayout
            // Routing lives in DashboardRoute so it is testable. Pairs of
            // targets land on the same tab and differ only in the section.
            .onChange(of: appState.requestedTab) { _, tab in
                if let tab {
                    selection = tab
                    appState.requestedTab = nil
                }
            }
            .onChange(of: appState.showAskSearch) { _, show in
                if show { route(.askSearch); appState.showAskSearch = false }
            }
            .onChange(of: appState.showLintResults) { _, show in
                if show { route(.lintResults); appState.showLintResults = false }
            }
            .onChange(of: appState.showNotePolish) { _, show in
                if show { route(.notePolish); appState.showNotePolish = false }
            }
            .onChange(of: appState.showGUIActions) { _, show in
                if show { route(.guiActions); appState.showGUIActions = false }
            }
            .onChange(of: appState.showGitActivity) { _, show in
                if show { route(.gitActivity); appState.showGitActivity = false }
            }
            .onChange(of: appState.showMCPStatus) { _, show in
                if show { route(.mcpStatus); appState.showMCPStatus = false }
            }
            .onChange(of: appState.showAIUsage) { _, show in
                if show { route(.aiUsage); appState.showAIUsage = false }
            }
            .onChange(of: selection) { _, newValue in
                appState.actionTracker.record(
                    category: .navigation,
                    action: "nav.tab",
                    phase: .succeeded,
                    summary: "Switched tab to \(newValue.rawValue)",
                    metadata: ["tab": newValue.rawValue]
                )
                appState.actionTracker.updateStatus { status in
                    status.activeTab = newValue.rawValue
                    status.activeSection = activeSectionName(for: newValue)
                }
            }
            .onChange(of: notesSection) { _, newValue in
                appState.actionTracker.record(
                    category: .navigation,
                    action: "nav.section",
                    phase: .succeeded,
                    summary: "Switched Notes pane to \(newValue.rawValue)",
                    metadata: ["tab": DashboardTab.notes.rawValue, "section": newValue.rawValue]
                )
                if selection == .notes {
                    appState.actionTracker.updateStatus { $0.activeSection = newValue.rawValue }
                }
            }
            .onChange(of: activitySection) { _, newValue in
                appState.actionTracker.record(
                    category: .navigation,
                    action: "nav.section",
                    phase: .succeeded,
                    summary: "Switched Activity pane to \(newValue.rawValue)",
                    metadata: ["tab": DashboardTab.activity.rawValue, "section": newValue.rawValue]
                )
                if selection == .activity {
                    appState.actionTracker.updateStatus { $0.activeSection = newValue.rawValue }
                }
            }
            .onChange(of: appState.showOnboarding) { _, visible in
                appState.actionTracker.updateStatus { $0.onboardingVisible = visible }
            }
            .onChange(of: appState.showIndexProgress) { _, visible in
                appState.actionTracker.updateStatus { $0.indexSheetVisible = visible }
            }
            .onChange(of: appState.showSettingsRequest) { _, show in
                if show {
                    appState.showSettingsRequest = false
                    NSApp.activate()
                    openSettings()
                }
            }
            .task {
                guard !checkedFirstRunOnboarding else { return }
                checkedFirstRunOnboarding = true
                let isTest = ProcessInfo.processInfo.environment["2NB_TEST"] == "1"
                let skipOnboarding = LaunchOptions.current.skipOnboarding || LaunchOptions.current.vaultPath != nil
                let completed = UserDefaults.standard.bool(forKey: "hasCompletedOnboarding")
                if !isTest && !skipOnboarding && !completed && appState.vault == nil {
                    appState.showOnboarding = true
                }
            }
            .sheet(isPresented: $state.showOnboarding) {
                OnboardingView()
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
                get: { appState.showCommitDetail },
                set: { newValue in
                    if newValue {
                        appState.showCommitDetail = true
                    } else {
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
    }

    private func route(_ target: DashboardRoute.Target) {
        if let notes = DashboardRoute.notesSection(for: target) { notesSection = notes }
        if let activity = DashboardRoute.activitySection(for: target) { activitySection = activity }
        selection = DashboardRoute.tab(for: target)
    }

    private func activeSectionName(for tab: DashboardTab) -> String? {
        switch tab {
        case .home, .askSearch:
            return nil
        case .notes:
            return notesSection.rawValue
        case .activity:
            return activitySection.rawValue
        }
    }

    @ViewBuilder
    private var mainLayout: some View {
        if appState.vault == nil {
            WelcomeView()
        } else {
            NavigationSplitView {
                List(selection: $selection) {
                    NavigationLink(value: DashboardTab.home) {
                        Label(DashboardTab.home.rawValue, systemImage: DashboardTab.home.systemImage)
                    }
                    Section("Workspace") {
                        ForEach(DashboardTab.secondary) { tab in
                            NavigationLink(value: tab) {
                                Label(tab.rawValue, systemImage: tab.systemImage)
                            }
                        }
                    }
                }
                .safeAreaInset(edge: .bottom) {
                    HStack(spacing: 8) {
                        BetaBadge()
                        Spacer()
                        Button {
                            appState.noteSettingsOpened(tab: .ai)
                            openSettings()
                        } label: {
                            Label("Settings…", systemImage: "gearshape")
                                .font(.caption)
                        }
                        .buttonStyle(.plain)
                        .foregroundStyle(.secondary)
                    }
                    .padding(.horizontal, 12)
                    .padding(.vertical, 8)
                    .background(.bar)
                }
                .navigationTitle(appState.vault?.rootURL.lastPathComponent ?? "2ndbrain")
                .listStyle(.sidebar)
                .navigationSplitViewColumnWidth(min: 195, ideal: 220, max: 255)
            } detail: {
                Group {
                    switch selection {
                    case .home:
                        HomeView()
                    case .askSearch:
                        AskSearchView()
                    case .notes:
                        NotesView(section: $notesSection)
                    case .activity:
                        ActivityView(section: $activitySection)
                    }
                }
                .navigationTitle(selection.rawValue)
                .background(Color(nsColor: .windowBackgroundColor))
            }
            .navigationSplitViewStyle(.balanced)
        }
    }
}

// MARK: - Welcome View

struct WelcomeView: View {
    @Environment(AppState.self) var appState

    var body: some View {
        VStack(spacing: 20) {
            ThemedCard(style: .prominent, padding: 28) {
                VStack(spacing: 16) {
                    Image(systemName: "brain.head.profile")
                        .font(.system(size: 56))
                        .foregroundStyle(AppTheme.geminiAccent)

                    HStack(spacing: 8) {
                        Text("2ndbrain")
                            .font(.largeTitle.weight(.bold))
                        BetaBadge()
                    }

                    Text("Gemini Companion for Obsidian")
                        .font(.title3)
                        .foregroundStyle(.secondary)

                    VStack(spacing: 10) {
                        Button {
                            appState.showOnboarding = true
                        } label: {
                            Label("Run 3-Step Setup Guide…", systemImage: "sparkles")
                                .frame(maxWidth: .infinity)
                        }
                        .buttonStyle(.borderedProminent)
                        .controlSize(.large)

                        if let obsidian = ObsidianRegistry.load()?.openVault, obsidian.exists {
                            Button {
                                appState.openPickedVault(at: obsidian.url)
                            } label: {
                                Text("Open Obsidian vault: \(obsidian.name)")
                                    .frame(maxWidth: .infinity)
                            }
                            .buttonStyle(.bordered)
                            .controlSize(.large)
                        }

                        Button {
                            openVaultPanel()
                        } label: {
                            Text("Open Vault Folder…")
                                .frame(maxWidth: .infinity)
                        }
                        .buttonStyle(.bordered)
                    }
                    .padding(.top, 6)
                }
                .frame(maxWidth: .infinity)
            }
            .frame(maxWidth: 460)
        }
        .padding(32)
        .frame(maxWidth: .infinity, maxHeight: .infinity)
    }

    private func openVaultPanel() {
        let panel = NSOpenPanel()
        panel.canChooseFiles = false
        panel.canChooseDirectories = true
        panel.allowsMultipleSelection = false
        if panel.runModal() == .OK, let url = panel.url {
            appState.openPickedVault(at: url)
        }
    }
}
