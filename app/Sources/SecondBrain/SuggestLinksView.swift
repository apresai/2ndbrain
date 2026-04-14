import SwiftUI

struct SuggestLinkInfo: Codable, Identifiable {
    let path: String
    let title: String
    let score: Double
    let snippet: String

    var id: String { path }
}

struct SuggestLinksView: View {
    @Environment(AppState.self) var appState
    @Binding var isPresented: Bool

    var body: some View {
        VStack(spacing: 0) {
            HStack {
                VStack(alignment: .leading, spacing: 2) {
                    Text("Suggest Links")
                        .font(.title2.bold())
                    if let tab = appState.currentDocument {
                        Text(tab.url.lastPathComponent)
                            .font(.caption)
                            .foregroundStyle(.secondary)
                    }
                }
                Spacer()
                Button {
                    Task { await appState.loadSuggestLinks() }
                } label: {
                    Image(systemName: "arrow.clockwise")
                }
                .buttonStyle(.plain)
                .disabled(appState.suggestLinksLoading)
                Button("Done") { isPresented = false }
                    .keyboardShortcut(.defaultAction)
            }
            .padding()

            Divider()

            if appState.suggestLinksLoading {
                VStack(spacing: 12) {
                    ProgressView()
                    Text("Searching for related documents…")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
                .frame(maxWidth: .infinity, maxHeight: .infinity)
            } else if let error = appState.suggestLinksError {
                VStack(spacing: 12) {
                    Image(systemName: "exclamationmark.triangle")
                        .font(.system(size: 28))
                        .foregroundStyle(.orange)
                    Text(error)
                        .font(.caption)
                        .foregroundStyle(.secondary)
                        .multilineTextAlignment(.center)
                        .padding(.horizontal, 40)
                    Button("Set Up AI...") {
                        isPresented = false
                        appState.showAISetupWizard = true
                    }
                }
                .frame(maxWidth: .infinity, maxHeight: .infinity)
            } else if appState.suggestLinks.isEmpty {
                VStack(spacing: 8) {
                    Image(systemName: "link.badge.plus")
                        .font(.system(size: 28))
                        .foregroundStyle(.secondary)
                    Text("No link suggestions")
                        .font(.headline)
                    Text("Either this document already links to everything similar, or the vault is too small for semantic neighbors.")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                        .multilineTextAlignment(.center)
                        .padding(.horizontal, 40)
                }
                .frame(maxWidth: .infinity, maxHeight: .infinity)
            } else {
                List {
                    ForEach(appState.suggestLinks) { suggestion in
                        suggestionRow(suggestion)
                    }
                }
                .listStyle(.inset)
            }
        }
        .frame(width: 640, height: 480)
    }

    @ViewBuilder
    private func suggestionRow(_ suggestion: SuggestLinkInfo) -> some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack {
                Text(suggestion.title.isEmpty ? suggestion.path : suggestion.title)
                    .font(.headline)
                Spacer()
                Text(String(format: "%.2f", suggestion.score))
                    .font(.caption.monospacedDigit())
                    .foregroundStyle(.secondary)
                    .padding(.horizontal, 6)
                    .padding(.vertical, 2)
                    .background(.quaternary)
                    .clipShape(RoundedRectangle(cornerRadius: 3))
            }
            Text(suggestion.path)
                .font(.caption)
                .foregroundStyle(.tertiary)
            if !suggestion.snippet.isEmpty {
                Text(suggestion.snippet)
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(3)
            }
            HStack {
                Spacer()
                Button("Insert wikilink") {
                    appState.insertWikilink(for: suggestion)
                }
                .controlSize(.small)
            }
        }
        .padding(.vertical, 4)
    }
}
