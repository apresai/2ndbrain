import SwiftUI

struct PolishResultInfo: Codable {
    let path: String
    let original: String
    let polished: String
    let provider: String
    let model: String
    let durationMs: Int

    enum CodingKeys: String, CodingKey {
        case path
        case original
        case polished
        case provider
        case model
        case durationMs = "duration_ms"
    }
}

enum PolishState {
    case idle
    case loading
    case loaded(PolishResultInfo)
    case error(String)
}

struct PolishView: View {
    @Environment(AppState.self) var appState
    @Binding var isPresented: Bool

    var body: some View {
        VStack(spacing: 0) {
            HStack {
                VStack(alignment: .leading, spacing: 2) {
                    Text("Polish")
                        .font(.title2.bold())
                    if let tab = appState.currentDocument {
                        Text(tab.url.lastPathComponent)
                            .font(.caption)
                            .foregroundStyle(.secondary)
                    }
                }
                Spacer()
                if case .loaded(let result) = appState.polishState {
                    Text("\(result.provider) / \(result.model)")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                    Text("\(result.durationMs) ms")
                        .font(.caption.monospacedDigit())
                        .foregroundStyle(.tertiary)
                }
            }
            .padding()

            Divider()

            content
                .frame(maxWidth: .infinity, maxHeight: .infinity)

            Divider()

            HStack {
                Button("Reject") {
                    isPresented = false
                }
                .keyboardShortcut(.cancelAction)
                Spacer()
                if case .loaded = appState.polishState {
                    Button("Open as New Tab") {
                        appState.openPolishedAsNewTab()
                        isPresented = false
                    }
                    Button("Accept") {
                        appState.acceptPolishedRevision()
                        isPresented = false
                    }
                    .keyboardShortcut(.defaultAction)
                }
            }
            .padding(12)
        }
        .frame(width: 900, height: 600)
        .onAppear {
            if case .idle = appState.polishState {
                Task { await appState.runPolish() }
            }
        }
    }

    @ViewBuilder
    private var content: some View {
        switch appState.polishState {
        case .idle:
            ProgressView().frame(maxWidth: .infinity, maxHeight: .infinity)
        case .loading:
            VStack(spacing: 12) {
                ProgressView()
                Text("Polishing with your configured AI provider…")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
            .frame(maxWidth: .infinity, maxHeight: .infinity)
        case .loaded(let result):
            DiffView(original: result.original, modified: result.polished)
        case .error(let message):
            VStack(spacing: 12) {
                Image(systemName: "exclamationmark.triangle")
                    .font(.system(size: 28))
                    .foregroundStyle(.orange)
                Text("Polish failed")
                    .font(.headline)
                Text(message)
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .multilineTextAlignment(.center)
                    .padding(.horizontal, 40)
                Button("Retry") {
                    Task { await appState.runPolish() }
                }
            }
            .frame(maxWidth: .infinity, maxHeight: .infinity)
        }
    }
}
