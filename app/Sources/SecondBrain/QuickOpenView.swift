import SwiftUI

struct QuickOpenView: View {
    @Environment(AppState.self) var appState
    @Binding var isPresented: Bool
    @State private var query = ""
    @State private var selectedIndex = 0
    @FocusState private var searchFocused: Bool

    var body: some View {
        VStack(spacing: 0) {
            HStack {
                Image(systemName: "doc.text.magnifyingglass")
                    .foregroundStyle(.secondary)
                TextField("Open file...", text: $query)
                    .textFieldStyle(.plain)
                    .font(.title3)
                    .focused($searchFocused)
                    .onSubmit { openSelected() }
            }
            .padding(12)

            Divider()

            List(Array(filteredFiles.enumerated()), id: \.element.id) { index, file in
                Button {
                    appState.openDocument(at: file.url)
                    isPresented = false
                } label: {
                    HStack {
                        Text(file.name)
                            .fontWeight(index == selectedIndex ? .semibold : .regular)
                        Spacer()
                        Text(file.relativePath)
                            .font(.caption)
                            .foregroundStyle(.tertiary)
                    }
                    .padding(.vertical, 2)
                }
                .buttonStyle(.plain)
                .listRowBackground(index == selectedIndex ? Color.accentColor.opacity(0.1) : Color.clear)
            }
        }
        .frame(width: 500, height: 300)
        .background(.regularMaterial)
        .clipShape(RoundedRectangle(cornerRadius: 12))
        .shadow(radius: 20)
        .onKeyPress(.upArrow) { moveSelection(-1); return .handled }
        .onKeyPress(.downArrow) { moveSelection(1); return .handled }
        .onKeyPress(.escape) { isPresented = false; return .handled }
        .onAppear { searchFocused = true }
    }

    private var filteredFiles: [FileItem] {
        guard !query.isEmpty else { return appState.files }
        let lowered = query.lowercased()
        return appState.files.filter { fuzzyMatch($0.name.lowercased(), query: lowered) }
    }

    private func fuzzyMatch(_ string: String, query: String) -> Bool {
        var sIndex = string.startIndex
        for char in query {
            guard let found = string[sIndex...].firstIndex(of: char) else { return false }
            sIndex = string.index(after: found)
        }
        return true
    }

    private func openSelected() {
        let files = filteredFiles
        guard selectedIndex < files.count else { return }
        appState.openDocument(at: files[selectedIndex].url)
        isPresented = false
    }

    private func moveSelection(_ delta: Int) {
        let count = filteredFiles.count
        selectedIndex = max(0, min(count - 1, selectedIndex + delta))
    }
}
