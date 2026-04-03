import SwiftUI

struct StatusBarView: View {
    @Environment(AppState.self) var appState

    var body: some View {
        HStack(spacing: 16) {
            if let tab = appState.currentDocument {
                Text(tab.document.docType.isEmpty ? "note" : tab.document.docType)
                    .font(.caption)
                    .padding(.horizontal, 6)
                    .padding(.vertical, 2)
                    .background(.quaternary)
                    .clipShape(RoundedRectangle(cornerRadius: 3))

                if !tab.document.status.isEmpty {
                    Text(tab.document.status)
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }

                Spacer()

                Text(wordCount)
                    .font(.caption)
                    .foregroundStyle(.secondary)

                if tab.isDirty {
                    Text("Modified")
                        .font(.caption)
                        .foregroundStyle(.orange)
                }
            } else {
                Spacer()
            }

            if let vault = appState.vault {
                Text(vault.rootURL.lastPathComponent)
                    .font(.caption)
                    .foregroundStyle(.tertiary)
            }
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 4)
        .frame(height: 24)
        .background(Color(nsColor: .controlBackgroundColor))
        .overlay(alignment: .top) { Divider() }
    }

    private var wordCount: String {
        guard let tab = appState.currentDocument else { return "" }
        let words = tab.content.split { $0.isWhitespace || $0.isNewline }.count
        return "\(words) words"
    }
}
