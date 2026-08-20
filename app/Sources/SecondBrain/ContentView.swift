import SwiftUI
import SecondBrainCore
import UniformTypeIdentifiers

/// The dashboard's sidebar.
///
/// This was eight entries, one per surface, which meant "is my vault healthy?"
/// was answered across three of them and "what has been happening?" across two.
/// Now that configuration lives in the Settings window (Cmd+,), everything left
/// here is status, so it groups by the question rather than the subsystem.
///
/// `aiSettings` became `models` in name and scope: it is the model catalog, and
/// calling a catalog browser "AI Settings" is what sent people looking for their
/// API key in a 67-control grid instead of in Settings.
enum DashboardTab: String, CaseIterable, Identifiable {
    case home = "Home"
    case models = "Models"
    case notes = "Notes"
    case health = "Health"
    case activity = "Activity"

    var id: String { self.rawValue }

    /// Everything below Home. No longer labelled "Advanced": with the knobs
    /// moved out, these are ordinary status views, and calling them advanced
    /// discouraged people from opening the ones that answer real questions.
    static var secondary: [DashboardTab] { [.models, .notes, .health, .activity] }

    var systemImage: String {
        switch self {
        case .home: return "house"
        case .models: return "bolt.horizontal"
        case .notes: return "checkmark.seal"
        case .health: return "stethoscope"
        case .activity: return "clock.arrow.circlepath"
        }
    }
}

struct ContentView: View {
    @Environment(AppState.self) var appState
    @State private var selection: DashboardTab = .home
    // Owned here so a menu item can deep-link to a specific pane inside a group
    // tab, and so the pane survives leaving and returning to the tab.
    @State private var healthSection: HealthView.Section = .vault
    @State private var activitySection: ActivityView.Section = .git

    var body: some View {
        mainLayout
            // Routing lives in DashboardRoute so it is testable. Two of these
            // land on the same tab and differ only in the pane, which is exactly
            // the wiring that shipped wrong once.
            .onChange(of: appState.showAIHub) { _, show in
                if show { route(.aiHub); appState.showAIHub = false }
            }
            .onChange(of: appState.showMCPStatus) { _, show in
                if show { route(.mcpStatus); appState.showMCPStatus = false }
            }
            .onChange(of: appState.showGitActivity) { _, show in
                if show { route(.gitActivity); appState.showGitActivity = false }
            }
            .onChange(of: appState.showLintResults) { _, show in
                if show { route(.lintResults); appState.showLintResults = false }
            }
            .onChange(of: appState.showVaultStatus) { _, show in
                if show { route(.vaultStatus); appState.showVaultStatus = false }
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

    /// Select the tab a deep link names, and the pane inside it when that tab
    /// is a group. Setting the pane BEFORE the tab means the group renders with
    /// the right section already selected rather than flashing its default.
    private func route(_ target: DashboardRoute.Target) {
        if let health = DashboardRoute.healthSection(for: target) { healthSection = health }
        if let activity = DashboardRoute.activitySection(for: target) { activitySection = activity }
        selection = DashboardRoute.tab(for: target)
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
                    ForEach(DashboardTab.secondary) { tab in
                        NavigationLink(value: tab) {
                            Label(tab.rawValue, systemImage: tab.systemImage)
                        }
                    }
                }
                .navigationTitle(appState.vault?.rootURL.lastPathComponent ?? "2ndbrain")
                .listStyle(.sidebar)
            } detail: {
                Group {
                    switch selection {
                    case .home:
                        HomeView()
                    case .models:
                        SimpleModelsView()
                    case .notes:
                        LintResultsView(isPresented: .constant(true), isInline: true)
                    case .health:
                        HealthView(section: $healthSection)
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
        VStack(spacing: 16) {
            Image(systemName: "brain.head.profile")
                .font(.system(size: 64))
                .foregroundStyle(.secondary)

            Text("2ndbrain")
                .font(.largeTitle)
                .fontWeight(.bold)

            Text("AI Companion & Configuration Dashboard")
                .font(.title3)
                .foregroundStyle(.secondary)

            // Default to the vault Obsidian currently has open so the dashboard
            // binds to the same vault you're editing in Obsidian.
            if let obsidian = ObsidianRegistry.load()?.openVault, obsidian.exists {
                Button("Open your Obsidian vault: \(obsidian.name)") {
                    appState.openPickedVault(at: obsidian.url)
                }
                .buttonStyle(.borderedProminent)
                .controlSize(.large)
                .padding(.top, 8)

                Button("Open a different vault…") {
                    openVaultPanel()
                }
                .buttonStyle(.bordered)
            } else {
                Button("Open Vault") {
                    openVaultPanel()
                }
                .buttonStyle(.borderedProminent)
                .controlSize(.large)
                .padding(.top, 8)
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
            appState.openPickedVault(at: url)
        }
    }
}
