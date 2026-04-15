import SwiftUI

/// Full commit details modal: header + stats + split pane with file list
/// on the left and unified diff on the right. Opened by clicking a commit
/// row in GitActivityView, or via `appState.openCommitDetail(hash)`.
///
/// Data comes from `2nb git show --json <hash>` which the app shells out
/// to — the loading state lives on AppState so the view is passive.
struct CommitDetailView: View {
    @Environment(AppState.self) var appState
    @Binding var isPresented: Bool
    @State private var selectedFilePath: String?

    var body: some View {
        VStack(spacing: 0) {
            header
            Divider()
            if let detail = appState.commitDetail {
                stats(detail: detail)
                Divider()
                splitPane(detail: detail)
            } else if let err = appState.commitDetailError {
                errorState(message: err)
            } else {
                ProgressView("Loading commit…")
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
            }
        }
        .frame(width: 900, height: 640)
        .onAppear {
            if let detail = appState.commitDetail {
                selectedFilePath = detail.files.first?.path
            }
        }
        .onChange(of: appState.commitDetail?.hash) { _, _ in
            selectedFilePath = appState.commitDetail?.files.first?.path
        }
        .onKeyPress(.escape) {
            isPresented = false
            return .handled
        }
    }

    private var header: some View {
        HStack(alignment: .top, spacing: 12) {
            if let detail = appState.commitDetail {
                VStack(alignment: .leading, spacing: 4) {
                    HStack(spacing: 8) {
                        Text(String(detail.hash.prefix(7)))
                            .font(.system(.caption, design: .monospaced))
                            .foregroundStyle(.secondary)
                            .padding(.horizontal, 6)
                            .padding(.vertical, 2)
                            .background(Color.secondary.opacity(0.15))
                            .clipShape(RoundedRectangle(cornerRadius: 4))
                        Text(detail.subject.isEmpty ? "(no subject)" : detail.subject)
                            .font(.headline)
                            .lineLimit(2)
                    }
                    HStack(spacing: 8) {
                        Text(detail.author)
                            .font(.caption)
                            .foregroundStyle(.secondary)
                        Text(friendlyDate(detail.date))
                            .font(.caption)
                            .foregroundStyle(.tertiary)
                    }
                    if !detail.body.isEmpty {
                        Text(detail.body)
                            .font(.caption)
                            .foregroundStyle(.secondary)
                            .lineLimit(3)
                            .padding(.top, 2)
                    }
                }
            } else {
                Text("Commit details")
                    .font(.headline)
            }
            Spacer()
            Button("Done") { isPresented = false }
                .keyboardShortcut(.defaultAction)
        }
        .padding(12)
    }

    private func stats(detail: CommitDetail) -> some View {
        HStack(spacing: 12) {
            Label("\(detail.stats.filesChanged) file\(detail.stats.filesChanged == 1 ? "" : "s") changed",
                  systemImage: "doc.text")
                .font(.caption)
                .foregroundStyle(.secondary)
            Text("+\(detail.stats.insertions)")
                .font(.caption)
                .foregroundStyle(.green)
            Text("-\(detail.stats.deletions)")
                .font(.caption)
                .foregroundStyle(.red)
            Spacer()
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 6)
    }

    private func splitPane(detail: CommitDetail) -> some View {
        HSplitView {
            // File list
            fileList(files: detail.files)
                .frame(minWidth: 240, maxWidth: 360)

            // Diff pane
            diffPane(files: detail.files)
                .frame(minWidth: 400)
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
    }

    private func fileList(files: [CommitFile]) -> some View {
        Group {
            if files.isEmpty {
                Text("(no files changed)")
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
            } else {
                List(files, selection: $selectedFilePath) { file in
                    HStack(spacing: 6) {
                        Image(systemName: file.binary ? "photo" : "doc.text")
                            .font(.caption)
                            .foregroundStyle(.secondary)
                        VStack(alignment: .leading, spacing: 1) {
                            Text((file.path as NSString).lastPathComponent)
                                .font(.callout)
                                .lineLimit(1)
                            Text(file.path)
                                .font(.caption2)
                                .foregroundStyle(.tertiary)
                                .lineLimit(1)
                        }
                        Spacer()
                        Text("+\(file.additions)")
                            .font(.caption2)
                            .foregroundStyle(.green)
                        Text("-\(file.deletions)")
                            .font(.caption2)
                            .foregroundStyle(.red)
                    }
                    .contentShape(Rectangle())
                    .tag(file.path)
                }
                .listStyle(.sidebar)
            }
        }
    }

    private func diffPane(files: [CommitFile]) -> some View {
        Group {
            if let path = selectedFilePath, let file = files.first(where: { $0.path == path }) {
                if file.binary {
                    VStack(spacing: 8) {
                        Image(systemName: "photo.on.rectangle")
                            .font(.largeTitle)
                            .foregroundStyle(.secondary)
                        Text("Binary file")
                            .font(.headline)
                        Text(path)
                            .font(.caption)
                            .foregroundStyle(.tertiary)
                    }
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
                } else if file.diff.isEmpty {
                    Text("(no diff available)")
                        .font(.caption)
                        .foregroundStyle(.tertiary)
                        .frame(maxWidth: .infinity, maxHeight: .infinity)
                } else {
                    ScrollView([.vertical, .horizontal]) {
                        Text(file.diff)
                            .font(.system(.caption, design: .monospaced))
                            .textSelection(.enabled)
                            .padding(8)
                            .frame(maxWidth: .infinity, alignment: .leading)
                    }
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
                }
            } else {
                Text("Select a file to view its diff")
                    .font(.caption)
                    .foregroundStyle(.tertiary)
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
            }
        }
    }

    private func errorState(message: String) -> some View {
        VStack(spacing: 8) {
            Image(systemName: "exclamationmark.triangle")
                .font(.largeTitle)
                .foregroundStyle(.orange)
            Text("Couldn't load commit")
                .font(.headline)
            Text(message)
                .font(.caption)
                .foregroundStyle(.secondary)
                .multilineTextAlignment(.center)
                .padding(.horizontal, 40)
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
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

// MARK: - Data types

/// CommitDetail as returned by `2nb git show --json <hash>`. Shape mirrors
/// the Go struct in `cli/internal/git/git.go`.
struct CommitDetail: Codable, Identifiable {
    let hash: String
    let author: String
    let date: String
    let subject: String
    let body: String
    let stats: CommitStats
    let files: [CommitFile]
    var id: String { hash }

    enum CodingKeys: String, CodingKey {
        case hash, author, date, subject, body, stats, files
    }
}

struct CommitStats: Codable {
    let filesChanged: Int
    let insertions: Int
    let deletions: Int

    enum CodingKeys: String, CodingKey {
        case filesChanged = "files_changed"
        case insertions
        case deletions
    }
}

struct CommitFile: Codable, Identifiable {
    let path: String
    let additions: Int
    let deletions: Int
    let binary: Bool
    let diff: String
    var id: String { path }
}
