import SwiftUI

/// Two grouping containers that take the sidebar from eight entries to five.
///
/// The sidebar accumulated one tab per surface, so "is my vault healthy?" was
/// spread over three of them (Vault Status, Metrics, Updates) and "what has been
/// happening?" over two (Git Integration, MCP Server). With configuration moved
/// into the Settings window, what remains is all status, and status groups by the
/// question it answers rather than by which subsystem produced it.
///
/// Each group hosts the EXISTING inline views unchanged behind a segmented
/// picker. Nothing is rewritten and nothing is dropped — a rewrite would risk
/// losing detail these views already render correctly, and the point of the
/// change is fewer places to look, not less to see.

/// Health: the state of this vault and of the three installed products.
///
/// The selected section is owned by ContentView, not by @State here, for two
/// reasons. A menu item that names a specific pane ("MCP Server Status…") has to
/// be able to land on it — otherwise grouping two tabs into one silently breaks
/// the deep link, which would be an odd thing for a change about labels matching
/// behavior to do. And @State would reset the pane every time you left the tab.
struct HealthView: View {
    enum Section: String, CaseIterable, Identifiable {
        case vault = "Vault"
        case performance = "Performance"
        case updates = "Updates"
        var id: String { rawValue }
    }

    @Binding var section: Section

    var body: some View {
        VStack(spacing: 0) {
            Picker("", selection: $section) {
                ForEach(Section.allCases) { Text($0.rawValue).tag($0) }
            }
            .pickerStyle(.segmented)
            .labelsHidden()
            .padding([.horizontal, .top])
            .padding(.bottom, 8)

            switch section {
            case .vault:
                VaultStatusView(isPresented: .constant(true), isInline: true)
            case .performance:
                MetricsView(isPresented: .constant(true), isInline: true)
            case .updates:
                UpdatesView()
            }
        }
    }
}

/// Activity: what has been happening in and around the vault.
/// Section ownership matches HealthView, and for the same reasons.
struct ActivityView: View {
    enum Section: String, CaseIterable, Identifiable {
        case git = "Git"
        case mcp = "MCP Server"
        var id: String { rawValue }
    }

    @Binding var section: Section

    var body: some View {
        VStack(spacing: 0) {
            Picker("", selection: $section) {
                ForEach(Section.allCases) { Text($0.rawValue).tag($0) }
            }
            .pickerStyle(.segmented)
            .labelsHidden()
            .padding([.horizontal, .top])
            .padding(.bottom, 8)

            switch section {
            case .git:
                GitActivityView(isPresented: .constant(true), isInline: true)
            case .mcp:
                MCPStatusView(isPresented: .constant(true), isInline: true)
            }
        }
    }
}
