import SwiftUI

struct GitChangeInfo: Codable, Identifiable {
    let hash: String
    let author: String
    let date: String
    let subject: String
    let files: [String]?

    var id: String { hash }
}

struct GitActivityView: View {
    @Environment(AppState.self) var appState
    @Binding var isPresented: Bool

    var body: some View {
        VStack(spacing: 0) {
            HStack {
                Text("Recent Activity")
                    .font(.title2.bold())
                Spacer()
                Picker("Window", selection: Binding(
                    get: { appState.gitActivityDays },
                    set: { appState.setGitActivityDays($0) }
                )) {
                    Text("24 hours").tag(1)
                    Text("3 days").tag(3)
                    Text("7 days").tag(7)
                    Text("30 days").tag(30)
                }
                .pickerStyle(.menu)
                .fixedSize()
                Button {
                    Task { await appState.refreshGitActivity() }
                } label: {
                    Image(systemName: "arrow.clockwise")
                }
                .buttonStyle(.plain)
                .disabled(appState.gitActivityLoading)
                Button("Done") { isPresented = false }
                    .keyboardShortcut(.defaultAction)
            }
            .padding()

            Divider()

            if appState.gitActivityLoading {
                ProgressView().frame(maxWidth: .infinity, maxHeight: .infinity)
            } else if !appState.vaultIsGitRepo {
                VStack(spacing: 8) {
                    Image(systemName: "externaldrive.badge.exclamationmark")
                        .font(.system(size: 28))
                        .foregroundStyle(.secondary)
                    Text("Not a git repository")
                        .font(.headline)
                    Text("Initialize git in the vault root to enable activity, diff, and uncommitted-file tracking.")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                        .multilineTextAlignment(.center)
                        .padding(.horizontal, 40)
                }
                .frame(maxWidth: .infinity, maxHeight: .infinity)
            } else if appState.gitActivity.isEmpty {
                VStack(spacing: 8) {
                    Image(systemName: "calendar.badge.clock")
                        .font(.system(size: 28))
                        .foregroundStyle(.secondary)
                    Text("No commits in the last \(appState.gitActivityDays) day\(appState.gitActivityDays == 1 ? "" : "s")")
                        .font(.headline)
                }
                .frame(maxWidth: .infinity, maxHeight: .infinity)
            } else {
                List {
                    ForEach(appState.gitActivity) { change in
                        Button {
                            appState.openCommitDetail(change.hash)
                        } label: {
                            changeRow(change)
                                .contentShape(Rectangle())
                        }
                        .buttonStyle(.plain)
                    }
                }
                .listStyle(.inset)
            }
        }
        .frame(width: 720, height: 520)
        .onAppear {
            Task { await appState.refreshGitActivity() }
        }
    }

    @ViewBuilder
    private func changeRow(_ change: GitChangeInfo) -> some View {
        VStack(alignment: .leading, spacing: 4) {
            HStack(spacing: 8) {
                Text(String(change.hash.prefix(7)))
                    .font(.system(.caption, design: .monospaced))
                    .foregroundStyle(.secondary)
                Text(change.subject.isEmpty ? "(no subject)" : change.subject)
                    .font(.body)
                    .lineLimit(1)
                Spacer()
                Text(friendlyDate(change.date))
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
            HStack(spacing: 8) {
                Text(change.author)
                    .font(.caption)
                    .foregroundStyle(.tertiary)
                if let files = change.files, !files.isEmpty {
                    Text("\(files.count) file\(files.count == 1 ? "" : "s")")
                        .font(.caption)
                        .foregroundStyle(.tertiary)
                    Text(files.prefix(3).joined(separator: ", "))
                        .font(.caption)
                        .foregroundStyle(.secondary)
                        .lineLimit(1)
                }
            }
        }
        .padding(.vertical, 2)
    }

    private func friendlyDate(_ raw: String) -> String {
        let iso = ISO8601DateFormatter()
        iso.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        if let date = iso.date(from: raw) ?? ISO8601DateFormatter().date(from: raw) {
            let fmt = RelativeDateTimeFormatter()
            fmt.unitsStyle = .short
            return fmt.localizedString(for: date, relativeTo: Date())
        }
        return raw
    }
}
