import SwiftUI
import SecondBrainCore

struct SearchPanelView: View {
    @Environment(AppState.self) var appState
    @Binding var isPresented: Bool
    @State private var query = ""
    @State private var typeFilter = ""
    @State private var results: [SearchResultItem] = []
    @State private var selectedIndex = 0
    @FocusState private var searchFocused: Bool

    var body: some View {
        VStack(spacing: 0) {
            // Search field
            HStack {
                Image(systemName: "magnifyingglass")
                    .foregroundStyle(.secondary)
                TextField("Search vault...", text: $query)
                    .textFieldStyle(.plain)
                    .font(.title3)
                    .focused($searchFocused)
                    .onSubmit { openSelected() }

                if !typeFilter.isEmpty {
                    Text(typeFilter)
                        .font(.caption)
                        .padding(.horizontal, 6)
                        .padding(.vertical, 2)
                        .background(Color.accentColor.opacity(0.2))
                        .clipShape(RoundedRectangle(cornerRadius: 4))
                    Button {
                        typeFilter = ""
                        performSearch()
                    } label: {
                        Image(systemName: "xmark.circle.fill")
                            .font(.caption)
                    }
                    .buttonStyle(.plain)
                }
            }
            .padding(12)

            Divider()

            // Results
            if results.isEmpty && !query.isEmpty {
                Text("No results found")
                    .foregroundStyle(.secondary)
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
            } else {
                List(Array(results.enumerated()), id: \.element.id) { index, result in
                    Button {
                        openResult(result)
                    } label: {
                        HStack {
                            VStack(alignment: .leading, spacing: 2) {
                                Text(result.title)
                                    .font(.body)
                                    .fontWeight(index == selectedIndex ? .semibold : .regular)
                                HStack(spacing: 8) {
                                    Text(result.docType)
                                        .font(.caption)
                                        .foregroundStyle(.secondary)
                                    if !result.headingPath.isEmpty {
                                        Text(result.headingPath)
                                            .font(.caption)
                                            .foregroundStyle(.tertiary)
                                    }
                                }
                            }
                            Spacer()
                            if result.score > 0 {
                                Text(String(format: "%.1f", result.score))
                                    .font(.caption2)
                                    .foregroundStyle(.tertiary)
                            }
                        }
                        .padding(.vertical, 2)
                    }
                    .buttonStyle(.plain)
                    .listRowBackground(index == selectedIndex ? Color.accentColor.opacity(0.1) : Color.clear)
                }
            }
        }
        .frame(width: 600, height: 400)
        .background(.regularMaterial)
        .clipShape(RoundedRectangle(cornerRadius: 12))
        .shadow(radius: 20)
        .onChange(of: query) { _, _ in performSearch() }
        .onKeyPress(.upArrow) { moveSelection(-1); return .handled }
        .onKeyPress(.downArrow) { moveSelection(1); return .handled }
        .onKeyPress(.escape) { isPresented = false; return .handled }
        .onAppear { searchFocused = true }
    }

    private func performSearch() {
        guard let db = appState.database else { return }

        // Parse inline filters
        var searchQuery = query
        var type = typeFilter

        if let range = searchQuery.range(of: #"type:(\S+)"#, options: .regularExpression) {
            let match = String(searchQuery[range])
            type = String(match.dropFirst(5))
            searchQuery.removeSubrange(range)
            searchQuery = searchQuery.trimmingCharacters(in: .whitespaces)
        }

        do {
            let docs = try db.search(query: searchQuery, type: type.isEmpty ? nil : type, limit: 20)
            results = docs.map { doc in
                SearchResultItem(
                    id: doc.id,
                    path: doc.path,
                    title: doc.title,
                    docType: doc.docType,
                    headingPath: "",
                    score: 0,
                    status: doc.status
                )
            }
            selectedIndex = 0
        } catch {
            results = []
        }
    }

    private func openResult(_ result: SearchResultItem) {
        guard let vault = appState.vault else { return }
        let url = URL(fileURLWithPath: vault.rootURL.path).appendingPathComponent(result.path)
        appState.openDocument(at: url)
        isPresented = false
    }

    private func openSelected() {
        guard selectedIndex < results.count else { return }
        openResult(results[selectedIndex])
    }

    private func moveSelection(_ delta: Int) {
        selectedIndex = max(0, min(results.count - 1, selectedIndex + delta))
    }
}

struct SearchResultItem: Identifiable {
    let id: String
    let path: String
    let title: String
    let docType: String
    let headingPath: String
    let score: Double
    let status: String
}
