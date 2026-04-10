import SwiftUI

struct LintResultsView: View {
    @Environment(AppState.self) var appState
    @Binding var isPresented: Bool

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
                        Text("No issues found")
                            .font(.title3)
                            .fontWeight(.medium)
                        Text("\(report.filesChecked) files checked")
                            .font(.caption)
                            .foregroundStyle(.secondary)
                    }
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
                } else {
                    List(report.issues) { issue in
                        Button {
                            openIssue(issue)
                        } label: {
                            HStack(alignment: .top, spacing: 8) {
                                Image(systemName: issue.level == "error"
                                    ? "xmark.circle.fill"
                                    : "exclamationmark.triangle.fill")
                                    .foregroundStyle(issue.level == "error" ? .red : .yellow)
                                    .frame(width: 16)
                                VStack(alignment: .leading, spacing: 2) {
                                    Text(issue.path)
                                        .font(.caption)
                                        .foregroundStyle(.secondary)
                                    Text(issue.message)
                                        .font(.body)
                                }
                            }
                            .padding(.vertical, 2)
                        }
                        .buttonStyle(.plain)
                    }
                }
            } else {
                VStack(spacing: 12) {
                    Image(systemName: "checkmark.seal")
                        .font(.system(size: 48))
                        .foregroundStyle(.tertiary)
                    Text("Click 'Check Now' to validate your vault.")
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
                Button("Close") {
                    isPresented = false
                }
                Button("Check Now") {
                    Task { await appState.runLint() }
                }
                .buttonStyle(.borderedProminent)
                .disabled(appState.isLinting)
            }
            .padding(12)
        }
        .frame(width: 580, height: 440)
    }

    private func openIssue(_ issue: LintIssue) {
        guard let vault = appState.vault else { return }
        let url = URL(fileURLWithPath: vault.rootURL.path).appendingPathComponent(issue.path)
        appState.openDocument(at: url)
        isPresented = false
    }
}
