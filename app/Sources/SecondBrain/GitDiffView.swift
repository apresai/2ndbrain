import SwiftUI

struct GitDiffView: View {
    @Environment(AppState.self) var appState
    @Binding var isPresented: Bool
    let relPath: String

    var body: some View {
        VStack(spacing: 0) {
            HStack {
                VStack(alignment: .leading, spacing: 2) {
                    Text("Git Diff")
                        .font(.title2.bold())
                    Text(relPath)
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
                Spacer()
                Button("Done") { isPresented = false }
                    .keyboardShortcut(.defaultAction)
            }
            .padding()

            Divider()

            if appState.gitDiffLoading {
                ProgressView().frame(maxWidth: .infinity, maxHeight: .infinity)
            } else if appState.gitDiffText.isEmpty {
                VStack(spacing: 8) {
                    Image(systemName: "checkmark.circle")
                        .font(.system(size: 28))
                        .foregroundStyle(.green)
                    Text("No uncommitted changes")
                        .font(.headline)
                }
                .frame(maxWidth: .infinity, maxHeight: .infinity)
            } else {
                ScrollView {
                    Text(appState.gitDiffText)
                        .font(.system(.caption, design: .monospaced))
                        .textSelection(.enabled)
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .padding(12)
                }
            }
        }
        .frame(width: 800, height: 560)
        .onAppear {
            Task { await appState.loadGitDiff(for: relPath) }
        }
    }
}
