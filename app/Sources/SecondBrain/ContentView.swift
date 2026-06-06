import SwiftUI
import SecondBrainCore
import UniformTypeIdentifiers

enum DashboardTab: String, CaseIterable, Identifiable {
    case status = "Vault Status"
    case aiSettings = "AI Settings"
    case mcpServer = "MCP Server"
    case gitIntegration = "Git Integration"
    case validation = "Validation"

    var id: String { self.rawValue }

    var systemImage: String {
        switch self {
        case .status: return "externaldrive"
        case .aiSettings: return "bolt.horizontal"
        case .mcpServer: return "server.rack"
        case .gitIntegration: return "sourcecontrol"
        case .validation: return "checkmark.seal"
        }
    }
}

struct ContentView: View {
    @Environment(AppState.self) var appState
    @State private var selection: DashboardTab = .status

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
                    ForEach(DashboardTab.allCases) { tab in
                        NavigationLink(value: tab) {
                            Label(tab.rawValue, systemImage: tab.systemImage)
                        }
                    }
                }
                .navigationTitle("2ndbrain")
                .listStyle(.sidebar)
            } detail: {
                Group {
                    switch selection {
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

            Button("Open Vault") {
                openVaultPanel()
            }
            .buttonStyle(.borderedProminent)
            .controlSize(.large)
            .padding(.top, 8)
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
