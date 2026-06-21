import SwiftUI
import SecondBrainCore
import UniformTypeIdentifiers

enum DashboardTab: String, CaseIterable, Identifiable {
    case home = "Home"
    case status = "Vault Status"
    case aiSettings = "AI Settings"
    case mcpServer = "MCP Server"
    case gitIntegration = "Git Integration"
    case validation = "Validation"
    case updates = "Updates"

    var id: String { self.rawValue }

    /// The power-user tabs, demoted under an "Advanced" sidebar section. Home
    /// surfaces the common-case essentials (vault, AI, index); everything else
    /// lives here.
    static var advanced: [DashboardTab] { [.status, .aiSettings, .mcpServer, .gitIntegration, .validation, .updates] }

    var systemImage: String {
        switch self {
        case .home: return "house"
        case .status: return "externaldrive"
        case .aiSettings: return "bolt.horizontal"
        case .mcpServer: return "server.rack"
        case .gitIntegration: return "sourcecontrol"
        case .validation: return "checkmark.seal"
        case .updates: return "arrow.down.circle"
        }
    }
}

struct ContentView: View {
    @Environment(AppState.self) var appState
    @State private var selection: DashboardTab = .home

    var body: some View {
        mainLayout
            .onChange(of: appState.showAIHub) { _, show in
                if show {
                    selection = .aiSettings
                    appState.showAIHub = false
                }
            }
            .onChange(of: appState.showMCPStatus) { _, show in
                if show {
                    selection = .mcpServer
                    appState.showMCPStatus = false
                }
            }
            .onChange(of: appState.showGitActivity) { _, show in
                if show {
                    selection = .gitIntegration
                    appState.showGitActivity = false
                }
            }
            .onChange(of: appState.showLintResults) { _, show in
                if show {
                    selection = .validation
                    appState.showLintResults = false
                }
            }
            .onChange(of: appState.showVaultStatus) { _, show in
                if show {
                    selection = .status
                    appState.showVaultStatus = false
                }
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
                    Section("Advanced") {
                        ForEach(DashboardTab.advanced) { tab in
                            NavigationLink(value: tab) {
                                Label(tab.rawValue, systemImage: tab.systemImage)
                            }
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
                    case .status:
                        VaultStatusView(isPresented: .constant(true), isInline: true)
                    case .aiSettings:
                        AIHubView(onClose: {}, isInline: true)
                    case .mcpServer:
                        MCPStatusView(isPresented: .constant(true), isInline: true)
                    case .gitIntegration:
                        GitActivityView(isPresented: .constant(true), isInline: true)
                    case .validation:
                        LintResultsView(isPresented: .constant(true), isInline: true)
                    case .updates:
                        UpdatesView()
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
