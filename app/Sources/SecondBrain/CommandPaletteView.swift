import SwiftUI

struct CommandPaletteView: View {
    @Environment(AppState.self) var appState
    @Binding var isPresented: Bool
    @State private var query = ""
    @State private var selectedIndex = 0
    @FocusState private var searchFocused: Bool

    var body: some View {
        VStack(spacing: 0) {
            HStack {
                Image(systemName: "command")
                    .foregroundStyle(.secondary)
                TextField("Type a command...", text: $query)
                    .textFieldStyle(.plain)
                    .font(.title3)
                    .focused($searchFocused)
                    .onSubmit { executeSelected() }
            }
            .padding(12)

            Divider()

            List(Array(filteredCommands.enumerated()), id: \.element.id) { index, command in
                Button {
                    executeCommand(command)
                } label: {
                    HStack {
                        Image(systemName: command.icon)
                            .frame(width: 20)
                            .foregroundStyle(.secondary)
                        VStack(alignment: .leading) {
                            Text(command.title)
                                .fontWeight(index == selectedIndex ? .semibold : .regular)
                            if !command.shortcut.isEmpty {
                                Text(command.shortcut)
                                    .font(.caption)
                                    .foregroundStyle(.tertiary)
                            }
                        }
                    }
                    .padding(.vertical, 2)
                }
                .buttonStyle(.plain)
                .listRowBackground(index == selectedIndex ? Color.accentColor.opacity(0.1) : Color.clear)
            }
        }
        .frame(width: 500, height: 350)
        .background(.regularMaterial)
        .clipShape(RoundedRectangle(cornerRadius: 12))
        .shadow(radius: 20)
        .onKeyPress(.upArrow) { moveSelection(-1); return .handled }
        .onKeyPress(.downArrow) { moveSelection(1); return .handled }
        .onKeyPress(.escape) { isPresented = false; return .handled }
        .onAppear { searchFocused = true }
    }

    private var filteredCommands: [PaletteCommand] {
        guard !query.isEmpty else { return allCommands }
        let lowered = query.lowercased()
        return allCommands.filter { $0.title.lowercased().contains(lowered) }
    }

    private func executeSelected() {
        let cmds = filteredCommands
        guard selectedIndex < cmds.count else { return }
        executeCommand(cmds[selectedIndex])
    }

    private func executeCommand(_ command: PaletteCommand) {
        command.action(appState)
        isPresented = false
    }

    private func moveSelection(_ delta: Int) {
        let count = filteredCommands.count
        selectedIndex = max(0, min(count - 1, selectedIndex + delta))
    }

    private var allCommands: [PaletteCommand] {
        [
            PaletteCommand(title: "New Note", icon: "doc.badge.plus", shortcut: "Cmd+N") { state in
                state.createNewDocument(type: "note")
            },
            PaletteCommand(title: "New ADR", icon: "doc.badge.plus", shortcut: "") { state in
                state.createNewDocument(type: "adr", title: "Untitled ADR")
            },
            PaletteCommand(title: "New Runbook", icon: "doc.badge.plus", shortcut: "") { state in
                state.createNewDocument(type: "runbook", title: "Untitled Runbook")
            },
            PaletteCommand(title: "New Postmortem", icon: "doc.badge.plus", shortcut: "") { state in
                state.createNewDocument(type: "postmortem", title: "Untitled Postmortem")
            },
            PaletteCommand(title: "Save", icon: "square.and.arrow.down", shortcut: "Cmd+S") { state in
                state.saveCurrentDocument()
            },
            PaletteCommand(title: "Toggle Sidebar", icon: "sidebar.left", shortcut: "Cmd+\\") { state in
                state.sidebarVisible.toggle()
            },
            PaletteCommand(title: "Toggle Focus Mode", icon: "eye", shortcut: "Cmd+Shift+E") { state in
                state.focusModeActive.toggle()
            },
            PaletteCommand(title: "Refresh File List", icon: "arrow.clockwise", shortcut: "") { state in
                state.refreshFiles()
            },
            PaletteCommand(title: "Rebuild Index", icon: "arrow.triangle.2.circlepath", shortcut: "") { state in
                state.rebuildIndex()
            },
            PaletteCommand(title: "Show Graph View", icon: "point.3.connected.trianglepath.dotted", shortcut: "") { state in
                state.showGraphView = true
            },
            PaletteCommand(title: "Reindex Spotlight", icon: "magnifyingglass", shortcut: "") { state in
                state.reindexSpotlight()
            },
        ]
    }
}

struct PaletteCommand: Identifiable {
    let id = UUID()
    let title: String
    let icon: String
    let shortcut: String
    let action: @MainActor (AppState) -> Void
}
