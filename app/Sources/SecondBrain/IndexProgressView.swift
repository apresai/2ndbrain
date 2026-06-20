import SwiftUI

struct IndexProgressView: View {
    @Environment(AppState.self) var appState
    @Binding var isPresented: Bool

    var body: some View {
        VStack(spacing: 16) {
            // Header
            HStack {
                Image(systemName: phaseIcon)
                    .font(.title2)
                    .foregroundStyle(phaseColor)
                Text(isReembed ? "Re-embed All" : "Sync Index")
                    .font(.headline)
                Spacer()
            }

            Divider()

            if let progress = appState.indexProgress {
                switch progress.phase {
                case .ready:
                    readyView

                case .indexingFiles:
                    VStack(alignment: .leading, spacing: 12) {
                        phaseHeader(progress.phase.rawValue)
                        ProgressView()
                            .progressViewStyle(.linear)
                        HStack {
                            Text("\(progress.filesIndexed) files indexed")
                                .font(.caption)
                                .foregroundStyle(.secondary)
                            Spacer()
                            if !progress.currentFile.isEmpty {
                                Text(progress.currentFile)
                                    .font(.caption)
                                    .foregroundStyle(.tertiary)
                                    .lineLimit(1)
                                    .truncationMode(.middle)
                            }
                        }
                    }

                case .embedding:
                    VStack(alignment: .leading, spacing: 12) {
                        phaseHeader(progress.phase.rawValue)
                        if progress.embeddingTotal > 0 {
                            ProgressView(
                                value: Double(progress.embeddingCurrent),
                                total: Double(progress.embeddingTotal)
                            )
                            .progressViewStyle(.linear)
                            Text("\(progress.embeddingCurrent) / \(progress.embeddingTotal) documents")
                                .font(.caption)
                                .foregroundStyle(.secondary)
                        } else {
                            ProgressView()
                                .progressViewStyle(.linear)
                            Text("Preparing embeddings...")
                                .font(.caption)
                                .foregroundStyle(.secondary)
                        }
                    }

                case .complete:
                    statsView(progress)

                case .failed:
                    Label(progress.error ?? "Unknown error", systemImage: "exclamationmark.triangle.fill")
                        .font(.callout)
                        .foregroundStyle(.red)
                }

                // Elapsed time (after completion)
                if progress.elapsed > 0 {
                    Text("Completed in \(String(format: "%.1f", progress.elapsed))s")
                        .font(.caption)
                        .foregroundStyle(.tertiary)
                }
            }

            Spacer(minLength: 0)

            // Buttons
            HStack {
                if phase == .ready {
                    Button("Cancel") {
                        isPresented = false
                    }
                    .keyboardShortcut(.cancelAction)
                }
                Spacer()
                switch phase {
                case .ready:
                    Button(isReembed ? "Re-embed All" : "Sync Index") {
                        appState.startIndex()
                    }
                    .buttonStyle(.borderedProminent)
                    .keyboardShortcut(.defaultAction)
                case .indexingFiles, .embedding:
                    Button("Running...") {}
                        .buttonStyle(.borderedProminent)
                        .disabled(true)
                case .complete, .failed:
                    Button("Done") {
                        isPresented = false
                    }
                    .buttonStyle(.borderedProminent)
                    .keyboardShortcut(.defaultAction)
                }
            }
        }
        .padding(20)
        .frame(width: 400, height: 280)
    }

    private var phase: IndexPhase {
        appState.indexProgress?.phase ?? .ready
    }

    /// Whether the pending run is a full "Re-embed All" (clears and regenerates
    /// every embedding) rather than an incremental "Rebuild Index". Drives the
    /// header, confirm copy, and warning so the user knows it re-runs paid
    /// embedding calls. Read from the live progress struct (which keeps the flag
    /// for the whole run) and falls back to `pendingForceReembed` only before a
    /// run exists, so the title/copy stay accurate through every phase rather
    /// than flipping back to "Rebuild Index" once `pendingForceReembed` clears.
    private var isReembed: Bool {
        appState.indexProgress?.forceReembed ?? appState.pendingForceReembed
    }

    private var phaseIcon: String {
        switch phase {
        case .ready: return "arrow.triangle.2.circlepath"
        case .indexingFiles, .embedding: return "arrow.triangle.2.circlepath"
        case .complete: return "checkmark.circle.fill"
        case .failed: return "xmark.circle.fill"
        }
    }

    private var phaseColor: Color {
        switch phase {
        case .ready: return .accentColor
        case .indexingFiles, .embedding: return .accentColor
        case .complete: return .green
        case .failed: return .red
        }
    }

    @ViewBuilder
    private var readyView: some View {
        VStack(alignment: .leading, spacing: 10) {
            Text(isReembed
                 ? "Re-embed every document in this vault from scratch."
                 : "Index new and changed notes in this vault.")
                .font(.callout)
                .foregroundStyle(.secondary)

            if let fileCount = appState.files.count as Int? {
                Text(isReembed
                     ? "All \(fileCount) documents will be re-embedded."
                     : "\(fileCount) documents will be indexed.")
                    .font(.caption)
                    .foregroundStyle(.tertiary)
            }

            if isReembed, let ai = appState.aiStatus, ai.embedAvailable {
                // Re-embed clears every stored embedding and regenerates it, so
                // it re-runs a paid embedding call for every document — unlike
                // an incremental Rebuild, which only embeds changed docs.
                Label("Regenerates every embedding — re-runs paid \(ai.embeddingModel) calls for all documents.",
                      systemImage: "exclamationmark.triangle.fill")
                    .font(.caption)
                    .foregroundStyle(.orange)
            } else if let ai = appState.aiStatus, ai.embedAvailable {
                Label("Embeddings will be updated (\(ai.embeddingModel))", systemImage: "brain.head.profile")
                    .font(.caption)
                    .foregroundStyle(.tertiary)
            }
        }
    }

    private func phaseHeader(_ title: String) -> some View {
        HStack {
            Text(title)
                .font(.subheadline.weight(.medium))
            Spacer()
            ProgressView()
                .controlSize(.small)
        }
    }

    @ViewBuilder
    private func statsView(_ progress: IndexProgress) -> some View {
        VStack(alignment: .leading, spacing: 6) {
            statRow("Documents indexed", value: "\(progress.docsIndexed)")
            statRow("Chunks created", value: "\(progress.chunksCreated)")
            statRow("Links found", value: "\(progress.linksFound)")
            if progress.embeddingCurrent > 0 {
                statRow("Embeddings generated", value: "\(progress.embeddingCurrent)")
            }
        }
        .padding(.vertical, 4)
    }

    private func statRow(_ label: String, value: String) -> some View {
        HStack {
            Text(label)
                .font(.callout)
                .foregroundStyle(.secondary)
            Spacer()
            Text(value)
                .font(.callout.monospacedDigit())
        }
    }
}
