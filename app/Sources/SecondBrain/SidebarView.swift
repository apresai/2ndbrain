import SwiftUI
import SecondBrainCore

struct SidebarView: View {
    @Environment(AppState.self) var appState
    @State private var selectedPanel: SidebarPanel = .files
    @State private var searchText = ""
    @State private var showDeleteConfirmation = false
    @State private var fileToDelete: FileItem?

    var body: some View {
        VStack(spacing: 0) {
            // Panel picker
            Picker("Panel", selection: $selectedPanel) {
                Image(systemName: "doc.text").tag(SidebarPanel.files)
                Image(systemName: "list.bullet.indent").tag(SidebarPanel.outline)
                Image(systemName: "link").tag(SidebarPanel.backlinks)
                Image(systemName: "tag").tag(SidebarPanel.tags)
            }
            .pickerStyle(.segmented)
            .padding(8)

            Divider()

            switch selectedPanel {
            case .files:
                fileList
            case .backlinks:
                BacklinksView()
            case .tags:
                TagBrowserView()
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
            .contextMenu {
                Button("Open") {
                    appState.openDocument(at: file.url)
                }
                Button("Find Similar") {
                    appState.pendingFindSimilarQuery = file.name
                    appState.showSearch = true
                }
                .disabled(appState.aiStatus?.embedAvailable != true)
                Divider()
                Button("Delete", role: .destructive) {
                    fileToDelete = file
                    showDeleteConfirmation = true
                }
            }
        }
        .searchable(text: $searchText, placement: .sidebar, prompt: "Filter files...")
        .alert("Delete Document", isPresented: $showDeleteConfirmation) {
            Button("Cancel", role: .cancel) { }
            Button("Delete", role: .destructive) {
                if let file = fileToDelete {
                    appState.deleteDocument(at: file.url)
                }
            }
        } message: {
            Text("Are you sure you want to delete \"\(fileToDelete?.name ?? "")\"? This cannot be undone.")
        }
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
    case files, outline, backlinks, tags
}
