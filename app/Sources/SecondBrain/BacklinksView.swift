import SwiftUI
import SecondBrainCore

struct BacklinksView: View {
    @Environment(AppState.self) var appState

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack {
                Image(systemName: "link")
                Text("Backlinks")
                    .font(.headline)
                Spacer()
                Text("\(backlinks.count)")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
            .padding(.horizontal, 12)
            .padding(.top, 8)

            if backlinks.isEmpty {
                Text("No backlinks found")
                    .font(.caption)
                    .foregroundStyle(.tertiary)
                    .padding(.horizontal, 12)
                Spacer()
            } else {
                List(backlinks) { link in
                    Button {
                        openBacklink(link)
                    } label: {
                        HStack {
                            VStack(alignment: .leading, spacing: 2) {
                                Text(link.title)
                                    .font(.body)
                                Text(link.context)
                                    .font(.caption)
                                    .foregroundStyle(.secondary)
                                    .lineLimit(2)
                            }
                            Spacer(minLength: 0)
                        }
                        .contentShape(Rectangle())
                        .padding(.vertical, 2)
                    }
                    .buttonStyle(.plain)
                }
            }
        }
    }

    private var backlinks: [BacklinkItem] {
        guard let tab = appState.currentDocument,
              let db = appState.database else { return [] }

        let targetName = tab.url.deletingPathExtension().lastPathComponent

        do {
            let results = try db.backlinks(for: targetName)
            return results.map { result in
                BacklinkItem(
                    path: result.path,
                    title: result.title,
                    context: "Links via [[\(result.linkText)]]"
                )
            }
        } catch {
            return []
        }
    }

    private func openBacklink(_ link: BacklinkItem) {
        guard let vault = appState.vault else { return }
        let url = vault.rootURL.appendingPathComponent(link.path)
        appState.openDocument(at: url)
    }
}

struct BacklinkItem: Identifiable {
    let id = UUID()
    let path: String
    let title: String
    let context: String
}
