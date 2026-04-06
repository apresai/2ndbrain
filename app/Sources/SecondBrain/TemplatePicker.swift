import SwiftUI

struct TemplatePicker: View {
    @Environment(AppState.self) var appState
    @Binding var isPresented: Bool
    @State private var title = ""
    @State private var selectedType: String?
    @FocusState private var titleFocused: Bool

    private let templates: [(type: String, name: String, icon: String, description: String, sections: [String])] = [
        ("note", "Note", "doc.text", "General knowledge",
         []),
        ("adr", "Architecture Decision Record", "checkmark.seal", "Record decisions with context and consequences",
         ["Status", "Context", "Decision", "Consequences"]),
        ("runbook", "Runbook", "list.clipboard", "Step-by-step operational procedure",
         ["Overview", "Prerequisites", "Steps", "Verification", "Rollback"]),
        ("prd", "Product Requirements", "doc.richtext", "Product requirements with user stories and specs",
         ["Problem Statement", "Target Users", "Goals", "User Stories", "Requirements", "Risks"]),
        ("prfaq", "Press Release / FAQ", "newspaper", "Amazon-style working backwards document",
         ["Press Release", "External FAQ", "Internal FAQ"]),
        ("postmortem", "Postmortem", "exclamationmark.triangle", "Incident analysis and lessons learned",
         ["Summary", "Timeline", "Root Cause", "Impact", "Action Items", "Lessons Learned"]),
    ]

    var body: some View {
        VStack(spacing: 0) {
            // Title field
            HStack {
                Image(systemName: "doc.badge.plus")
                    .foregroundStyle(.secondary)
                TextField("Document title...", text: $title)
                    .textFieldStyle(.plain)
                    .font(.title3)
                    .focused($titleFocused)
                    .onSubmit { createDocument() }
            }
            .padding(12)

            Divider()

            // Template grid
            ScrollView {
                LazyVGrid(columns: [GridItem(.flexible()), GridItem(.flexible())], spacing: 12) {
                    ForEach(templates, id: \.type) { template in
                        TemplateCard(
                            name: template.name,
                            icon: template.icon,
                            description: template.description,
                            sections: template.sections,
                            isSelected: selectedType == template.type
                        )
                        .onTapGesture {
                            selectedType = template.type
                        }
                    }
                }
                .padding(16)
            }

            Divider()

            // Create button
            HStack {
                Spacer()
                Button("Cancel") {
                    isPresented = false
                }
                .keyboardShortcut(.cancelAction)

                Button("Create") {
                    createDocument()
                }
                .keyboardShortcut(.defaultAction)
                .disabled(title.trimmingCharacters(in: .whitespaces).isEmpty || selectedType == nil)
            }
            .padding(12)
        }
        .frame(width: 560, height: 480)
        .background(.regularMaterial)
        .clipShape(RoundedRectangle(cornerRadius: 12))
        .shadow(radius: 20)
        .onKeyPress(.escape) { isPresented = false; return .handled }
        .onAppear {
            titleFocused = true
            selectedType = "note"
        }
    }

    private func createDocument() {
        let trimmed = title.trimmingCharacters(in: .whitespaces)
        guard !trimmed.isEmpty, let type = selectedType else { return }
        appState.createNewDocument(type: type, title: trimmed)
        isPresented = false
    }
}

struct TemplateCard: View {
    let name: String
    let icon: String
    let description: String
    let sections: [String]
    let isSelected: Bool

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack {
                Image(systemName: icon)
                    .font(.title3)
                    .foregroundStyle(isSelected ? .white : .accentColor)
                Text(name)
                    .font(.headline)
                    .foregroundStyle(isSelected ? .white : .primary)
            }

            Text(description)
                .font(.caption)
                .foregroundStyle(isSelected ? .white.opacity(0.8) : .secondary)
                .lineLimit(2)

            if !sections.isEmpty {
                HStack(spacing: 4) {
                    ForEach(sections.prefix(4), id: \.self) { section in
                        Text(section)
                            .font(.system(size: 9))
                            .padding(.horizontal, 4)
                            .padding(.vertical, 2)
                            .background(isSelected ? Color.white.opacity(0.2) : Color.secondary.opacity(0.1))
                            .clipShape(RoundedRectangle(cornerRadius: 3))
                            .foregroundStyle(isSelected ? Color.white.opacity(0.9) : Color.secondary.opacity(0.5))
                    }
                    if sections.count > 4 {
                        Text("+\(sections.count - 4)")
                            .font(.system(size: 9))
                            .foregroundStyle(isSelected ? Color.white.opacity(0.7) : Color.secondary)
                    }
                }
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .padding(12)
        .background(isSelected ? Color.accentColor : Color(nsColor: .controlBackgroundColor))
        .clipShape(RoundedRectangle(cornerRadius: 8))
        .overlay(
            RoundedRectangle(cornerRadius: 8)
                .stroke(isSelected ? Color.accentColor : Color(nsColor: .separatorColor), lineWidth: 1)
        )
    }
}
