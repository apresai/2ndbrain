import SwiftUI

struct CommandPaletteView: View {
    @Environment(AppState.self) var appState
    @Binding var isPresented: Bool
    @State private var query = ""
    @State private var selectedIndex = 0

    var body: some View {
        VStack(spacing: 0) {
            HStack {
                Image(systemName: "command")
                    .foregroundStyle(.secondary)
                TextField("Type a command...", text: $query)
                    .textFieldStyle(.plain)
                    .font(.title3)
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
            PaletteCommand(title: "New Document", icon: "doc.badge.plus", shortcut: "Cmd+N") { state in
                state.createNewDocument()
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
