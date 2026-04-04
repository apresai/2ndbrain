import SwiftUI
import SecondBrainCore

struct PropertiesView: View {
    @Environment(AppState.self) var appState
    @Binding var isPresented: Bool
    @State private var editTitle = ""
    @State private var editType = ""
    @State private var editStatus = ""
    @State private var editTagsText = ""
    @State private var hasLoadedFields = false

    private let typeOptions = ["note", "adr", "runbook", "postmortem"]
    private let statusOptions: [String: [String]] = [
        "note": ["draft", "complete"],
        "adr": ["proposed", "accepted", "deprecated", "superseded"],
        "runbook": ["draft", "active", "archived"],
        "postmortem": ["draft", "reviewed", "published"],
    ]

    var body: some View {
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
                        // Editable title
                        VStack(alignment: .leading, spacing: 2) {
                            Text("Title")
                                .font(.caption)
                                .foregroundStyle(.secondary)
                            TextField("Title", text: $editTitle)
                                .textFieldStyle(.roundedBorder)
                                .font(.body)
                        }

                        // Editable type
                        VStack(alignment: .leading, spacing: 2) {
                            Text("Type")
                                .font(.caption)
                                .foregroundStyle(.secondary)
                            Picker("Type", selection: $editType) {
                                ForEach(typeOptions, id: \.self) { type in
                                    Text(type).tag(type)
                                }
                            }
                            .labelsHidden()
                        }

                        // Editable status
                        VStack(alignment: .leading, spacing: 2) {
                            Text("Status")
                                .font(.caption)
                                .foregroundStyle(.secondary)
                            let options = statusOptions[editType] ?? ["draft"]
                            Picker("Status", selection: $editStatus) {
                                ForEach(options, id: \.self) { status in
                                    Text(status).tag(status)
                                }
                            }
                            .labelsHidden()
                        }

                        // Read-only ID
                        PropertyRow(label: "ID", value: tab.document.id)

                        // Editable tags
                        VStack(alignment: .leading, spacing: 2) {
                            Text("Tags")
                                .font(.caption)
                                .foregroundStyle(.secondary)
                            TextField("tag1, tag2, ...", text: $editTagsText)
                                .textFieldStyle(.roundedBorder)
                                .font(.body)
                        }

                        PropertyRow(label: "Created", value: formatDate(tab.document.createdAt))
                        PropertyRow(label: "Modified", value: formatDate(tab.document.modifiedAt))
                        PropertyRow(label: "Path", value: tab.url.lastPathComponent)

                        // Save button
                        Button("Save Properties") {
                            applyChanges()
                        }
                        .buttonStyle(.borderedProminent)
                        .controlSize(.small)
                    }
                    .padding(12)
                }
                .onAppear { loadFields(from: tab) }
                .onChange(of: appState.activeTabIndex) { _, _ in
                    if let tab = appState.currentDocument {
                        loadFields(from: tab)
                    }
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

    private func loadFields(from tab: DocumentTab) {
        editTitle = tab.document.title
        editType = tab.document.docType.isEmpty ? "note" : tab.document.docType
        editStatus = tab.document.status.isEmpty ? "draft" : tab.document.status
        editTagsText = tab.document.tags.joined(separator: ", ")
    }

    private func applyChanges() {
        guard let idx = validTabIndex else { return }
        var content = appState.openDocuments[idx].content

        // Update frontmatter fields in the raw content
        content = replaceFrontmatterField(content, field: "title", value: editTitle)
        content = replaceFrontmatterField(content, field: "type", value: editType)
        content = replaceFrontmatterField(content, field: "status", value: editStatus)

        let tagsArray = editTagsText
            .split(separator: ",")
            .map { $0.trimmingCharacters(in: .whitespaces) }
            .filter { !$0.isEmpty }
        let tagsYAML = tagsArray.isEmpty ? "[]" : "[\(tagsArray.joined(separator: ", "))]"
        content = replaceFrontmatterField(content, field: "tags", value: tagsYAML)

        // Update modified timestamp
        let now = ISO8601DateFormatter().string(from: Date())
        content = replaceFrontmatterField(content, field: "modified", value: now)

        appState.openDocuments[idx].content = content
        appState.openDocuments[idx].isDirty = true

        // Re-parse the document
        if let doc = try? FrontmatterParser.loadDocument(from: appState.openDocuments[idx].url) {
            appState.openDocuments[idx].document = doc
        }
    }

    private func replaceFrontmatterField(_ content: String, field: String, value: String) -> String {
        let lines = content.components(separatedBy: "\n")
        var result: [String] = []
        var inFrontmatter = false
        var replaced = false

        for (i, line) in lines.enumerated() {
            if i == 0 && line == "---" {
                inFrontmatter = true
                result.append(line)
                continue
            }
            if inFrontmatter && line == "---" {
                if !replaced {
                    result.append("\(field): \(value)")
                }
                inFrontmatter = false
                result.append(line)
                continue
            }
            if inFrontmatter && line.hasPrefix("\(field):") {
                result.append("\(field): \(value)")
                replaced = true
            } else {
                result.append(line)
            }
        }
        return result.joined(separator: "\n")
    }

    private var validTabIndex: Int? {
        let idx = appState.activeTabIndex
        guard idx >= 0, idx < appState.openDocuments.count else { return nil }
        return idx
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
