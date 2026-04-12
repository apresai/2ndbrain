import SwiftUI
import SecondBrainCore

struct SidebarView: View {
    @Environment(AppState.self) var appState
    @State private var selectedPanel: SidebarPanel = .files
    @State private var searchText = ""
    @State private var showDeleteConfirmation = false
    @State private var fileToDelete: FileItem?
    @State private var expandedPaths: Set<String> = []

    var body: some View {
        VStack(spacing: 0) {
            // Panel picker
            Picker("Panel", selection: $selectedPanel) {
                Label("Files", systemImage: "doc.text").tag(SidebarPanel.files)
                Label("Outline", systemImage: "list.bullet.indent").tag(SidebarPanel.outline)
                Label("Links", systemImage: "link").tag(SidebarPanel.backlinks)
                Label("Tags", systemImage: "tag").tag(SidebarPanel.tags)
            }
            .pickerStyle(.segmented)
            .labelStyle(.titleAndIcon)
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
        Group {
            if searchText.isEmpty {
                // Tree view
                List {
                    ForEach(fileTree) { node in
                        treeRow(node)
                    }
                }
            } else {
                // Flat filtered list when searching
                List(filteredFiles, id: \.id) { file in
                    fileRow(file)
                }
            }
        }
        .searchable(text: $searchText, placement: .sidebar, prompt: "Filter files...")
        .onChange(of: appState.files.map(\.relativePath)) { _, _ in
            // Prune expandedPaths when files change (prevents unbounded growth as directories are removed)
            let validPaths = collectDirectoryPaths(fileTree)
            expandedPaths = expandedPaths.intersection(validPaths)
        }
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

    private func treeRow(_ node: FileTreeNode) -> AnyView {
        switch node {
        case .directory(_, let name, let path, let children):
            return AnyView(
                DisclosureGroup(isExpanded: Binding(
                    get: { expandedPaths.contains(path) },
                    set: { expanded in
                        if expanded { expandedPaths.insert(path) } else { expandedPaths.remove(path) }
                    }
                )) {
                    ForEach(children) { child in
                        treeRow(child)
                    }
                } label: {
                    HStack(spacing: 6) {
                        Image(systemName: "folder")
                            .foregroundStyle(.secondary)
                            .font(.caption)
                        Text(name)
                            .font(.body)
                            .lineLimit(1)
                        Spacer()
                        Text("\(node.fileCount)")
                            .font(.caption2)
                            .foregroundStyle(.tertiary)
                            .padding(.horizontal, 5)
                            .padding(.vertical, 1)
                            .background(Color.secondary.opacity(0.1))
                            .clipShape(RoundedRectangle(cornerRadius: 4))
                    }
                }
            )
        case .file(let item):
            return AnyView(fileRow(item))
        }
    }

    private func fileRow(_ file: FileItem) -> some View {
        Button {
            appState.openDocument(at: file.url)
        } label: {
            VStack(alignment: .leading, spacing: 2) {
                Text(file.name)
                    .font(.body)
                    .lineLimit(1)
                if !searchText.isEmpty {
                    Text(file.relativePath)
                        .font(.caption)
                        .foregroundStyle(.secondary)
                        .lineLimit(1)
                }
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

    private var fileTree: [FileTreeNode] {
        buildFileTree(from: appState.files)
    }

    private func collectDirectoryPaths(_ nodes: [FileTreeNode]) -> Set<String> {
        var paths: Set<String> = []
        for node in nodes {
            if case .directory(_, _, let path, let children) = node {
                paths.insert(path)
                paths.formUnion(collectDirectoryPaths(children))
            }
        }
        return paths
    }

    private var filteredFiles: [FileItem] {
        appState.files.filter {
            $0.name.localizedCaseInsensitiveContains(searchText)
        }
    }
}

enum SidebarPanel {
    case files, outline, backlinks, tags
}
