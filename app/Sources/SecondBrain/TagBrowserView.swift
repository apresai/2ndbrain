import SwiftUI
import SecondBrainCore

struct TagBrowserView: View {
    @Environment(AppState.self) var appState
    @State private var tags: [(tag: String, count: Int)] = []
    @State private var selectedTag: String?

    var body: some View {
        VStack(spacing: 0) {
            if tags.isEmpty {
                Text("No tags")
                    .foregroundStyle(.secondary)
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
            } else {
                List(tags, id: \.tag, selection: $selectedTag) { item in
                    Button {
                        if selectedTag == item.tag {
                            selectedTag = nil
                        } else {
                            selectedTag = item.tag
                        }
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
                        }
                        .padding(.vertical, 1)
                    }
                    .buttonStyle(.plain)
                    .listRowBackground(selectedTag == item.tag ? Color.accentColor.opacity(0.15) : Color.clear)
                }

                if selectedTag != nil {
                    Divider()
                    Button("Clear Filter") {
                        selectedTag = nil
                    }
                    .font(.caption)
                    .padding(6)
                }
            }
        }
        .onAppear { loadTags() }
        .onChange(of: selectedTag) { _, tag in
            appState.selectedTagFilter = tag
            appState.refreshFiles()
        }
    }

    private func loadTags() {
        guard let db = appState.database else { return }
        do {
            let rows = try db.allTags()
            tags = rows
        } catch {
            tags = []
        }
    }
}
