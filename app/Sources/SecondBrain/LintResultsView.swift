import SwiftUI

struct LintResultsView: View {
    @Environment(AppState.self) var appState
    @Binding var isPresented: Bool
    var isInline: Bool = false

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
                        Button {
                            openIssue(issue)
                        } label: {
                            HStack(alignment: .top, spacing: 8) {
                                Image(systemName: issue.level == "error" ? "exclamationmark.octagon.fill" : "exclamationmark.triangle.fill")
                                    .foregroundStyle(issue.level == "error" ? .red : .orange)
                                    .font(.body)

                                VStack(alignment: .leading, spacing: 2) {
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
                                }
                                Spacer()
                            }
                            .contentShape(Rectangle())
                            .padding(.vertical, 4)
                        }
                        .buttonStyle(.plain)
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
        .frame(width: isInline ? nil : 580, height: isInline ? nil : 440)
    }

    private func openIssue(_ issue: LintIssue) {
        guard let vault = appState.vault else { return }
        let url = URL(fileURLWithPath: vault.rootURL.path).appendingPathComponent(issue.path)
        NSWorkspace.shared.open(url)
        if !isInline {
            isPresented = false
        }
    }
}
