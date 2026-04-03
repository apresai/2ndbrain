import SwiftUI
import SecondBrainCore

struct SidebarView: View {
    @Environment(AppState.self) var appState
    @State private var selectedPanel: SidebarPanel = .files
    @State private var searchText = ""

    var body: some View {
        VStack(spacing: 0) {
            // Panel picker
            Picker("Panel", selection: $selectedPanel) {
                Image(systemName: "doc.text").tag(SidebarPanel.files)
                Image(systemName: "list.bullet.indent").tag(SidebarPanel.outline)
            }
            .pickerStyle(.segmented)
            .padding(8)

            Divider()

            switch selectedPanel {
            case .files:
                fileList
            case .outline:
                outlineList
            }
        }
        .frame(minWidth: 200)
    }

    private var fileList: some View {
        List(filteredFiles, selection: Binding<FileItem.ID?>(
            get: { nil },
            set: { _ in }
        )) { file in
            Button {
                appState.openDocument(at: file.url)
            } label: {
                VStack(alignment: .leading, spacing: 2) {
                    Text(file.name)
                        .font(.body)
                        .lineLimit(1)
                    Text(file.relativePath)
                        .font(.caption)
                        .foregroundStyle(.secondary)
                        .lineLimit(1)
                }
                .padding(.vertical, 2)
            }
            .buttonStyle(.plain)
        }
        .searchable(text: $searchText, placement: .sidebar, prompt: "Filter files...")
    }

    private var outlineList: some View {
        List(appState.outline) { heading in
            HStack {
                Text(String(repeating: "  ", count: heading.level - 1) + heading.text)
                    .font(heading.level == 1 ? .headline : heading.level == 2 ? .subheadline : .body)
                    .lineLimit(1)
            }
            .padding(.vertical, 1)
        }
    }

    private var filteredFiles: [FileItem] {
        if searchText.isEmpty {
            return appState.files
        }
        return appState.files.filter {
            $0.name.localizedCaseInsensitiveContains(searchText)
        }
    }
}

enum SidebarPanel {
    case files, outline
}
