import SwiftUI
import SecondBrainCore

struct TagBrowserView: View {
    @Environment(AppState.self) var appState
    @State private var tags: [(tag: String, count: Int)] = []
    @State private var drillDownTag: String?

    var body: some View {
        VStack(spacing: 0) {
            if let tag = drillDownTag {
                drillDownView(tag: tag)
            } else if tags.isEmpty {
                Text("No tags")
                    .foregroundStyle(.secondary)
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
            } else {
                tagListView
            }
        }
        .onAppear { loadTags() }
    }

    private var tagListView: some View {
        List(tags, id: \.tag) { item in
            Button {
                drillDownTag = item.tag
            } label: {
                HStack {
                    Image(systemName: "tag")
                        .font(.caption)
                        .foregroundStyle(.secondary)
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
                    Image(systemName: "chevron.right")
                        .font(.caption2)
                        .foregroundStyle(.quaternary)
                }
                .padding(.vertical, 1)
            }
            .buttonStyle(.plain)
        }
    }

    private func drillDownView(tag: String) -> some View {
        VStack(spacing: 0) {
            // Back header
            HStack(spacing: 6) {
                Button {
                    drillDownTag = nil
                } label: {
                    HStack(spacing: 2) {
                        Image(systemName: "chevron.left")
                            .font(.caption.weight(.semibold))
                        Text("Tags")
                            .font(.subheadline)
                    }
                }
                .buttonStyle(.plain)
                .foregroundStyle(Color.accentColor)

                Spacer()

                Label(tag, systemImage: "tag.fill")
                    .font(.subheadline.weight(.medium))
                    .foregroundStyle(.secondary)
            }
            .padding(.horizontal, 12)
            .padding(.vertical, 8)

            Divider()

            // Filtered file list
            let files = filteredFiles(for: tag)
            if files.isEmpty {
                Text("No documents with this tag")
                    .foregroundStyle(.secondary)
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
            } else {
                List(files, id: \.id) { file in
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
            }
        }
    }

    private func filteredFiles(for tag: String) -> [FileItem] {
        guard let db = appState.database else { return [] }
        guard let taggedDocs = try? db.documentsWithTag(tag) else { return [] }
        let taggedPaths = Set(taggedDocs.map { $0.path })
        return appState.files.filter { taggedPaths.contains($0.relativePath) }
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
