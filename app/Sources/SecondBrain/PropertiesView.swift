import SwiftUI
import SecondBrainCore

struct PropertiesView: View {
    @Environment(AppState.self) var appState
    @Binding var isPresented: Bool

    var body: some View {
        @Bindable var state = appState

        VStack(alignment: .leading, spacing: 0) {
            HStack {
                Text("Properties")
                    .font(.headline)
                Spacer()
                Button { isPresented = false } label: {
                    Image(systemName: "xmark.circle.fill")
                        .foregroundStyle(.secondary)
                }
                .buttonStyle(.plain)
            }
            .padding(12)

            Divider()

            if let tab = appState.currentDocument {
                ScrollView {
                    VStack(alignment: .leading, spacing: 12) {
                        PropertyRow(label: "Title", value: tab.document.title)
                        PropertyRow(label: "Type", value: tab.document.docType)
                        PropertyRow(label: "Status", value: tab.document.status)
                        PropertyRow(label: "ID", value: tab.document.id)
                        PropertyRow(label: "Tags", value: tab.document.tags.joined(separator: ", "))
                        PropertyRow(label: "Created", value: formatDate(tab.document.createdAt))
                        PropertyRow(label: "Modified", value: formatDate(tab.document.modifiedAt))
                        PropertyRow(label: "Path", value: tab.url.lastPathComponent)
                    }
                    .padding(12)
                }
            } else {
                Text("No document selected")
                    .foregroundStyle(.secondary)
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
            }
        }
        .frame(width: 280)
        .background(.regularMaterial)
    }

    private func formatDate(_ date: Date) -> String {
        let formatter = DateFormatter()
        formatter.dateStyle = .medium
        formatter.timeStyle = .short
        return formatter.string(from: date)
    }
}

struct PropertyRow: View {
    let label: String
    let value: String

    var body: some View {
        VStack(alignment: .leading, spacing: 2) {
            Text(label)
                .font(.caption)
                .foregroundStyle(.secondary)
            Text(value.isEmpty ? "-" : value)
                .font(.body)
                .textSelection(.enabled)
        }
    }
}
