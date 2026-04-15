import SwiftUI

struct AskAIView: View {
    @Environment(AppState.self) var appState
    @Binding var isPresented: Bool
    @State private var question = ""
    @State private var answer = ""
    @State private var sources: [String] = []
    @State private var isLoading = false
    @State private var errorMessage = ""
    @FocusState private var inputFocused: Bool

    var body: some View {
        VStack(spacing: 0) {
            // Header
            HStack {
                Image(systemName: "brain.head.profile")
                    .foregroundStyle(.secondary)
                TextField("Ask your vault...", text: $question)
                    .textFieldStyle(.plain)
                    .font(.title3)
                    .focused($inputFocused)
                    .onSubmit { askQuestion() }
                    .disabled(isLoading)

                if isLoading {
                    ProgressView()
                        .controlSize(.small)
                }
            }
            .padding(12)

            Divider()

            // Portability warning banner — surfaces CLI stderr warnings
            // for the retrieval step (dimension mismatch, provider
            // unavailable, etc.). The answer still generates against
            // BM25 results; this tells the user retrieval degraded.
            if !appState.lastSemanticWarnings.isEmpty {
                ForEach(appState.lastSemanticWarnings, id: \.self) { warning in
                    HStack(alignment: .top, spacing: 8) {
                        Image(systemName: "exclamationmark.triangle.fill")
                            .foregroundStyle(.yellow)
                        Text(warning)
                            .font(.caption)
                            .foregroundStyle(.secondary)
                            .fixedSize(horizontal: false, vertical: true)
                        Spacer()
                    }
                    .padding(.horizontal, 12)
                    .padding(.vertical, 6)
                    .background(Color.yellow.opacity(0.08))
                }
                Divider()
            }

            // Content area
            if !errorMessage.isEmpty {
                VStack(spacing: 8) {
                    Image(systemName: "exclamationmark.triangle")
                        .font(.title2)
                        .foregroundStyle(.orange)
                    Text(errorMessage)
                        .font(.body)
                        .foregroundStyle(.secondary)
                        .multilineTextAlignment(.center)
                }
                .frame(maxWidth: .infinity, maxHeight: .infinity)
                .padding()
            } else if answer.isEmpty && !isLoading {
                VStack(spacing: 8) {
                    if appState.aiStatus?.genAvailable != true {
                        Image(systemName: "brain.head.profile")
                            .font(.title)
                            .foregroundStyle(.tertiary)
                        Text("AI generation not available")
                            .font(.headline)
                            .foregroundStyle(.secondary)
                        Text("Run `2nb ai status` to configure a provider.")
                            .font(.caption)
                            .foregroundStyle(.tertiary)
                    } else {
                        Image(systemName: "text.bubble")
                            .font(.title)
                            .foregroundStyle(.tertiary)
                        Text("Ask a question about your vault")
                            .foregroundStyle(.secondary)
                    }
                }
                .frame(maxWidth: .infinity, maxHeight: .infinity)
            } else {
                ScrollView {
                    VStack(alignment: .leading, spacing: 12) {
                        Text(answer)
                            .font(.body)
                            .textSelection(.enabled)
                            .frame(maxWidth: .infinity, alignment: .leading)

                        if !sources.isEmpty {
                            Divider()
                            Text("Sources")
                                .font(.caption)
                                .fontWeight(.semibold)
                                .foregroundStyle(.secondary)
                            ForEach(sources, id: \.self) { source in
                                Button {
                                    openSource(source)
                                } label: {
                                    HStack(spacing: 4) {
                                        Image(systemName: "doc.text")
                                            .font(.caption2)
                                        Text(source)
                                            .font(.caption)
                                    }
                                    .foregroundStyle(.blue)
                                }
                                .buttonStyle(.plain)
                            }
                        }
                    }
                    .padding()
                }
            }
        }
        .frame(width: 600, height: 450)
        .background(.regularMaterial)
        .clipShape(RoundedRectangle(cornerRadius: 12))
        .shadow(radius: 20)
        .onKeyPress(.escape) { isPresented = false; return .handled }
        .onAppear { inputFocused = true }
    }

    private func askQuestion() {
        guard !question.trimmingCharacters(in: .whitespaces).isEmpty else { return }
        guard appState.aiStatus?.genAvailable == true else {
            errorMessage = "AI generation not available. Configure a provider first."
            return
        }

        isLoading = true
        answer = ""
        sources = []
        errorMessage = ""

        Task {
            do {
                let result = try await appState.askAI(question: question)
                answer = result.answer
                sources = result.sources
            } catch {
                errorMessage = "Failed to get answer: \(error.localizedDescription)"
            }
            isLoading = false
        }
    }

    private func openSource(_ path: String) {
        guard let vault = appState.vault else { return }
        let url = URL(fileURLWithPath: vault.rootURL.path).appendingPathComponent(path)
        appState.openDocument(at: url)
        isPresented = false
    }
}
