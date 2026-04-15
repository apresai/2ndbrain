import SwiftUI
import SecondBrainCore

struct TagBrowserView: View {
    @Environment(AppState.self) var appState
    @State private var tags: [(tag: String, count: Int)] = []
    @State private var selectedTags: Set<String> = []

    var body: some View {
        VSplitView {
            // Top pane: tag list
            tagListPane
                .frame(minHeight: 80)

            // Bottom pane: filtered files for selected tags
            fileListPane
                .frame(minHeight: 60)
        }
        .onAppear { loadTags() }
        .onChange(of: appState.files.count) { _, _ in
            loadTags()
        }
    }

    // MARK: - Top Pane

    private var tagListPane: some View {
        Group {
            if tags.isEmpty {
                Text("No tags")
                    .foregroundStyle(.secondary)
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
            } else {
                List(tags, id: \.tag) { item in
                    Button {
                        if selectedTags.contains(item.tag) {
                            selectedTags.remove(item.tag)
                        } else {
                            selectedTags.insert(item.tag)
                        }
                    } label: {
                        HStack {
                            Image(systemName: selectedTags.contains(item.tag) ? "tag.fill" : "tag")
                                .font(.caption)
                                .foregroundStyle(selectedTags.contains(item.tag) ? Color.accentColor : .secondary)
                            Text(item.tag)
                                .font(.body)
                            Spacer()
                            Text("\(item.count)")
                                .font(.caption)
                                .foregroundStyle(.tertiary)
                                .padding(.horizontal, 6)
                                .padding(.vertical, 1)
                                .background(Color.secondary.opacity(0.1))
                                .clipShape(RoundedRectangle(cornerRadius: 4))
                        }
                        .padding(.vertical, 1)
                    }
                    .buttonStyle(.plain)
                    .listRowBackground(selectedTags.contains(item.tag) ? Color.accentColor.opacity(0.15) : Color.clear)
                }
            }
        }
    }

    // MARK: - Bottom Pane

    private var fileListPane: some View {
        VStack(spacing: 0) {
            if !selectedTags.isEmpty {
                // Header with selected tag pills
                HStack(spacing: 4) {
                    ForEach(Array(selectedTags.sorted()), id: \.self) { tag in
                        HStack(spacing: 3) {
                            Text(tag)
                                .font(.caption.weight(.medium))
                            Button {
                                selectedTags.remove(tag)
                            } label: {
                                Image(systemName: "xmark")
                                    .font(.system(size: 8, weight: .bold))
                                    .foregroundStyle(.secondary)
                            }
                            .buttonStyle(.plain)
                        }
                        .padding(.horizontal, 7)
                        .padding(.vertical, 3)
                        .background(Color.accentColor.opacity(0.15))
                        .clipShape(RoundedRectangle(cornerRadius: 6))
                    }

                    if selectedTags.count > 1 {
                        Button("Clear") {
                            selectedTags.removeAll()
                        }
                        .font(.caption)
                        .buttonStyle(.plain)
                        .foregroundStyle(.secondary)
                    }

                    Spacer()
                }
                .padding(.horizontal, 12)
                .padding(.vertical, 6)

                Divider()

                let files = filteredFiles
                if files.isEmpty {
                    Text("No documents match all selected tags")
                        .foregroundStyle(.secondary)
                        .frame(maxWidth: .infinity, maxHeight: .infinity)
                } else {
                    List(files, id: \.id) { file in
                        Button {
                            appState.openDocument(at: file.url)
                        } label: {
                            HStack {
                                VStack(alignment: .leading, spacing: 2) {
                                    Text(file.name)
                                        .font(.body)
                                        .lineLimit(1)
                                    Text(file.relativePath)
                                        .font(.caption)
                                        .foregroundStyle(.secondary)
                                        .lineLimit(1)
                                }
                                Spacer(minLength: 0)
                            }
                            .contentShape(Rectangle())
                            .padding(.vertical, 2)
                        }
                        .buttonStyle(.plain)
                    }
                }
            } else {
                Text("Select a tag to see documents")
                    .foregroundStyle(.secondary)
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
            }
        }
    }

    // MARK: - Data

    private var filteredFiles: [FileItem] {
        guard let db = appState.database else { return [] }
        let sortedTags = Array(selectedTags)
        let docs: [DocumentRecord]
        if sortedTags.count == 1 {
            docs = (try? db.documentsWithTag(sortedTags[0])) ?? []
        } else {
            docs = (try? db.documentsWithAllTags(sortedTags)) ?? []
        }
        let paths = Set(docs.map { $0.path })
        return appState.files.filter { paths.contains($0.relativePath) }
    }

    private func loadTags() {
        guard let db = appState.database else { return }
        do {
            tags = try db.allTags()
        } catch {
            tags = []
        }
    }
}
