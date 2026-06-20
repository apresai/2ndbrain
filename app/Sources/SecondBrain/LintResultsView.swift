import SwiftUI
import SecondBrainCore

struct LintResultsView: View {
    @Environment(AppState.self) var appState
    @Binding var isPresented: Bool
    var isInline: Bool = false

    /// The finding currently being fixed via the "Set value…" sheet.
    @State private var activeSetValue: ActiveSetValue?
    /// Inline result banner after a fix (green) or failure (red).
    @State private var actionMessage: String?
    @State private var actionIsError = false

    var body: some View {
        VStack(spacing: 0) {
            // Header
            HStack {
                Image(systemName: "checkmark.seal")
                    .foregroundStyle(.secondary)
                Text("Validate Knowledge Base")
                    .font(.title3)
                    .fontWeight(.medium)
                Spacer()
                if let report = appState.lintReport {
                    Text("\(report.filesChecked) files checked")
                        .font(.caption)
                        .foregroundStyle(.tertiary)
                }
            }
            .padding(12)

            if let actionMessage {
                Divider()
                HStack(spacing: 6) {
                    Image(systemName: actionIsError ? "exclamationmark.triangle.fill" : "checkmark.circle.fill")
                        .foregroundStyle(actionIsError ? .orange : .green)
                    Text(actionMessage)
                        .font(.caption)
                        .foregroundStyle(.secondary)
                    Spacer()
                }
                .padding(.horizontal, 12)
                .padding(.vertical, 6)
                .background(actionIsError ? Color.orange.opacity(0.08) : Color.green.opacity(0.08))
            }

            Divider()

            // Content
            if appState.isLinting {
                VStack(spacing: 12) {
                    ProgressView()
                    Text("Checking...")
                        .foregroundStyle(.secondary)
                }
                .frame(maxWidth: .infinity, maxHeight: .infinity)
            } else if let report = appState.lintReport {
                if report.issues.isEmpty {
                    VStack(spacing: 12) {
                        Image(systemName: "checkmark.circle.fill")
                            .font(.system(size: 48))
                            .foregroundStyle(.green)
                        Text("No issues found!")
                            .font(.headline)
                        Text("Your vault structure matches all schemas and contains no broken wikilinks.")
                            .font(.caption)
                            .foregroundStyle(.secondary)
                            .multilineTextAlignment(.center)
                            .padding(.horizontal, 40)
                    }
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
                } else {
                    List(report.issues) { issue in
                        IssueRow(
                            issue: issue,
                            onOpenInObsidian: { openInObsidian(issue) },
                            onSetValue: { setValue in activeSetValue = setValue }
                        )
                    }
                    .listStyle(.inset)
                }
            } else {
                VStack(spacing: 12) {
                    Image(systemName: "questionmark.circle")
                        .font(.system(size: 48))
                        .foregroundStyle(.secondary)
                    Text("Not validated yet")
                        .font(.headline)
                    Text("Run a validation scan to check for schema errors or broken links.")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
                .frame(maxWidth: .infinity, maxHeight: .infinity)
            }

            Divider()

            // Footer
            HStack {
                if let report = appState.lintReport, !report.issues.isEmpty {
                    Text("\(report.errors) errors, \(report.warnings) warnings")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
                Spacer()
                if !isInline {
                    Button("Close") {
                        isPresented = false
                    }
                }
                Button("Check Now") {
                    Task { await appState.runLint() }
                }
                .buttonStyle(.borderedProminent)
                .disabled(appState.isLinting)
            }
            .padding(12)
        }
        .frame(width: isInline ? nil : 580, height: isInline ? nil : 480)
        .sheet(item: $activeSetValue) { item in
            SetValueSheet(item: item) { field, value in
                activeSetValue = nil
                actionIsError = false
                actionMessage = "Set \(field) = \(value). Re-checking…"
                Task { await appState.runLint() }
            }
            .environment(appState)
        }
    }

    /// Open the note in Obsidian (not Finder), falling back to the file's
    /// default handler if Obsidian isn't installed. Unlike the old behavior, the
    /// panel stays open so the user can keep working through findings.
    private func openInObsidian(_ issue: LintIssue) {
        guard let vault = appState.vault else { return }
        let absURL = vault.rootURL.appendingPathComponent(issue.path)
        // Prefer the name Obsidian itself registers for this folder (handles a
        // vault renamed inside Obsidian); fall back to the folder basename.
        let vaultName = ObsidianRegistry.load()?.vault(at: vault.rootURL)?.name
            ?? vault.rootURL.lastPathComponent
        ObsidianURL.open(vaultName: vaultName, relativePath: issue.path, absoluteFileURL: absURL)
    }
}

/// One validation finding with its remediation affordances. Pure presentation:
/// it classifies the message and surfaces the right buttons, delegating the
/// actual work to the parent via closures.
private struct IssueRow: View {
    let issue: LintIssue
    let onOpenInObsidian: () -> Void
    let onSetValue: (ActiveSetValue) -> Void

    private var finding: LintFinding { LintFinding.classify(message: issue.message) }

    var body: some View {
        HStack(alignment: .top, spacing: 8) {
            Image(systemName: issue.level == "error" ? "exclamationmark.octagon.fill" : "exclamationmark.triangle.fill")
                .foregroundStyle(issue.level == "error" ? .red : .orange)
                .font(.body)

            VStack(alignment: .leading, spacing: 4) {
                HStack {
                    Text(issue.path)
                        .font(.body)
                        .fontWeight(.medium)
                        .lineLimit(1)
                    if let line = issue.line, line > 0 {
                        Text("line \(line)")
                            .font(.caption)
                            .foregroundStyle(.secondary)
                    }
                }

                Text(issue.message)
                    .font(.caption)
                    .foregroundStyle(.secondary)

                HStack(spacing: 8) {
                    Button("Open in Obsidian", action: onOpenInObsidian)
                        .controlSize(.small)

                    switch finding {
                    case let .missingField(field, _):
                        Button("Set value…") {
                            onSetValue(ActiveSetValue(path: issue.path, field: field, allowed: [], currentValue: nil))
                        }
                        .controlSize(.small)
                    case let .invalidEnum(field, value, allowed):
                        Button("Set value…") {
                            onSetValue(ActiveSetValue(path: issue.path, field: field, allowed: allowed, currentValue: value))
                        }
                        .controlSize(.small)
                    case .brokenLink, .parseError, .other:
                        EmptyView()
                    }
                }
            }
            Spacer()
        }
        .padding(.vertical, 4)
    }
}

/// The finding being edited in the Set-value sheet. `allowed` non-empty means an
/// enum (render a picker); empty means a free-text required field.
struct ActiveSetValue: Identifiable {
    let id = UUID()
    let path: String
    let field: String
    let allowed: [String]
    let currentValue: String?
}

/// Sheet to set one frontmatter field, fixing a missing-required-field or
/// invalid-enum finding. Enum findings get a picker of valid values; missing
/// fields get a validated text field. The CLI schema-validates the write.
private struct SetValueSheet: View {
    @Environment(AppState.self) private var appState
    @Environment(\.dismiss) private var dismiss
    let item: ActiveSetValue
    /// Called with (field, value) after a successful write so the parent can
    /// dismiss, re-lint, and show a banner.
    let onSaved: (String, String) -> Void

    @State private var selected: String
    @State private var freeText: String = ""
    @State private var saving = false
    @State private var errorText: String?

    init(item: ActiveSetValue, onSaved: @escaping (String, String) -> Void) {
        self.item = item
        self.onSaved = onSaved
        _selected = State(initialValue: item.allowed.first ?? "")
    }

    private var isEnum: Bool { !item.allowed.isEmpty }

    private var resolvedValue: String {
        isEnum ? selected : freeText.trimmingCharacters(in: .whitespacesAndNewlines)
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            Text("Set ‘\(item.field)’")
                .font(.headline)
            Text(item.path)
                .font(.caption)
                .foregroundStyle(.secondary)
                .lineLimit(1)

            if isEnum {
                if let current = item.currentValue {
                    Text("Current value ‘\(current)’ is not allowed.")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
                Picker("Value", selection: $selected) {
                    ForEach(item.allowed, id: \.self) { Text($0).tag($0) }
                }
                .labelsHidden()
                .pickerStyle(.menu)
            } else {
                TextField("Value for \(item.field)", text: $freeText)
                    .textFieldStyle(.roundedBorder)
            }

            if let errorText {
                Text(errorText)
                    .font(.caption)
                    .foregroundStyle(.red)
                    .fixedSize(horizontal: false, vertical: true)
            }

            HStack {
                Spacer()
                Button("Cancel") { dismiss() }
                    .keyboardShortcut(.cancelAction)
                    .disabled(saving)
                Button(saving ? "Saving…" : "Save") { save() }
                    .keyboardShortcut(.defaultAction)
                    .buttonStyle(.borderedProminent)
                    .disabled(saving || resolvedValue.isEmpty)
            }
        }
        .padding(16)
        .frame(width: 360)
    }

    private func save() {
        let value = resolvedValue
        guard !value.isEmpty else { return }
        saving = true
        errorText = nil
        Task {
            do {
                try await appState.setMeta(path: item.path, key: item.field, value: value)
                saving = false
                onSaved(item.field, value)
            } catch {
                saving = false
                errorText = (error as? CLIError)?.errorDescription ?? error.localizedDescription
            }
        }
    }
}
