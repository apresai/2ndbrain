import SwiftUI

/// Where a menu item or Home quick action lands: a sidebar tab plus the
/// segmented pane inside it when that tab is a group.
enum DashboardRoute {
    enum Target {
        case askSearch
        case lintResults
        case notePolish
        case guiActions
        case gitActivity
        case mcpStatus
        case aiUsage
    }

    static func tab(for target: Target) -> DashboardTab {
        switch target {
        case .askSearch: return .askSearch
        case .lintResults, .notePolish: return .notes
        case .guiActions, .gitActivity, .mcpStatus, .aiUsage: return .activity
        }
    }

    static func notesSection(for target: Target) -> NotesView.Section? {
        switch target {
        case .lintResults: return .linksAndSchema
        case .notePolish: return .polish
        default: return nil
        }
    }

    static func activitySection(for target: Target) -> ActivityView.Section? {
        switch target {
        case .guiActions: return .actions
        case .mcpStatus: return .mcp
        case .gitActivity: return .git
        case .aiUsage: return .aiUsage
        default: return nil
        }
    }
}

/// Notes maintenance: deterministic wikilink/frontmatter validation alongside
/// Gemini-powered note polishing.
struct NotesView: View {
    enum Section: String, CaseIterable, Identifiable {
        case linksAndSchema = "Links & Schema"
        case polish = "AI Polish"
        var id: String { rawValue }
    }

    @Binding var section: Section

    var body: some View {
        VStack(spacing: 0) {
            HStack {
                Picker("", selection: $section) {
                    ForEach(Section.allCases) { Text($0.rawValue).tag($0) }
                }
                .pickerStyle(.segmented)
                .labelsHidden()
                .frame(maxWidth: 320)
                .accessibilityIdentifier("notes.sectionPicker")
                Spacer()
            }
            .padding(.horizontal, 16)
            .padding(.vertical, 10)
            .background(.bar)

            Divider()

            switch section {
            case .linksAndSchema:
                LintResultsView(isPresented: .constant(true), isInline: true)
            case .polish:
                NotePolishView()
            }
        }
    }
}

/// Activity: live GUI action telemetry, git history, and MCP server status for this vault.
struct ActivityView: View {
    enum Section: String, CaseIterable, Identifiable {
        case actions = "Actions & Status"
        case git = "Git"
        case mcp = "MCP Server"
        case aiUsage = "AI Usage & Cost"
        var id: String { rawValue }
    }

    @Binding var section: Section

    var body: some View {
        VStack(spacing: 0) {
            HStack {
                Picker("", selection: $section) {
                    ForEach(Section.allCases) { Text($0.rawValue).tag($0) }
                }
                .pickerStyle(.segmented)
                .labelsHidden()
                .frame(maxWidth: 520)
                .accessibilityIdentifier("activity.sectionPicker")
                Spacer()
            }
            .padding(.horizontal, 16)
            .padding(.vertical, 10)
            .background(.bar)

            Divider()

            switch section {
            case .actions:
                GUIActionsView()
            case .git:
                GitActivityView(isPresented: .constant(true), isInline: true)
            case .mcp:
                MCPStatusView(isPresented: .constant(true), isInline: true)
            case .aiUsage:
                AIUsageView()
            }
        }
    }
}
