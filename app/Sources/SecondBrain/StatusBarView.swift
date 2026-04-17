import SwiftUI

struct StatusBarView: View {
    @Environment(AppState.self) var appState
    @State private var showAIPopover = false

    var body: some View {
        HStack(spacing: 16) {
            if let tab = appState.currentDocument {
                Text(tab.document.docType.isEmpty ? "note" : tab.document.docType)
                    .font(.caption)
                    .padding(.horizontal, 6)
                    .padding(.vertical, 2)
                    .background(.quaternary)
                    .clipShape(RoundedRectangle(cornerRadius: 3))

                if !tab.document.status.isEmpty {
                    Text(tab.document.status)
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }

                Spacer()

                Text(documentMetrics)
                    .font(.caption)
                    .foregroundStyle(.secondary)

                if tab.isDirty {
                    Text("Modified")
                        .font(.caption)
                        .foregroundStyle(.orange)
                }
            } else {
                Spacer()
            }

            if let indexError = appState.indexError {
                HStack(spacing: 4) {
                    Image(systemName: "exclamationmark.triangle.fill")
                        .foregroundStyle(.red)
                        .font(.caption)
                    Text("Index failed: \(indexError)")
                        .font(.caption)
                        .foregroundStyle(.red)
                        .lineLimit(1)
                    Button { appState.indexError = nil } label: {
                        Image(systemName: "xmark.circle.fill")
                            .font(.caption)
                            .foregroundStyle(.secondary)
                    }
                    .buttonStyle(.plain)
                    .help("Dismiss")
                }
            } else if appState.isIndexing {
                HStack(spacing: 4) {
                    ProgressView()
                        .controlSize(.mini)
                    if let progress = appState.embeddingProgress {
                        Text("Embedding \(progress.current)/\(progress.total)")
                            .font(.caption)
                            .foregroundStyle(.secondary)
                    } else {
                        Text("Indexing...")
                            .font(.caption)
                            .foregroundStyle(.secondary)
                    }
                }
            } else {
                Button {
                    showAIPopover = true
                } label: {
                    HStack(spacing: 4) {
                        Circle()
                            .fill(aiDotColor)
                            .frame(width: 6, height: 6)
                        Text("AI")
                            .font(.caption)
                            .foregroundStyle(.secondary)
                    }
                }
                .buttonStyle(.plain)
                .disabled(appState.vault == nil)
                .help("AI status — click for details")
                .popover(isPresented: $showAIPopover, arrowEdge: .top) {
                    aiPopoverContent
                        .frame(width: 280)
                        .padding(12)
                }

                Button {
                    appState.showMCPStatus = true
                } label: {
                    HStack(spacing: 4) {
                        Circle()
                            .fill(mcpDotColor)
                            .frame(width: 6, height: 6)
                        Text(mcpLabel)
                            .font(.caption)
                            .foregroundStyle(.secondary)
                    }
                }
                .buttonStyle(.plain)
                .disabled(appState.vault == nil)
                .help("MCP Server Status (Cmd+Shift+M)")
            }

            if let vault = appState.vault {
                Button {
                    appState.showVaultStatus = true
                } label: {
                    Text(vault.rootURL.lastPathComponent)
                        .font(.caption)
                        .foregroundStyle(.tertiary)
                }
                .buttonStyle(.plain)
                .help("Vault Status — click for index, embeddings, and stale docs")
            }
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 4)
        .frame(height: 24)
        .background(Color(nsColor: .controlBackgroundColor))
        .overlay(alignment: .top) { Divider() }
    }

    private var documentMetrics: String {
        guard let tab = appState.currentDocument else { return "" }
        let words = tab.content.split { $0.isWhitespace || $0.isNewline }.count
        let tokens = max(tab.content.count / 4, 0)
        var parts = ["\(words) words"]
        let chunks = appState.currentDocumentChunkCount
        if chunks > 0 {
            parts.append("\(chunks) chunks")
        }
        parts.append("~\(tokens) tokens")
        return parts.joined(separator: " · ")
    }

    private var aiDotColor: Color {
        guard let status = appState.aiStatus else { return .gray }
        // Portability status beats local availability — if the vault
        // can't do semantic search at all (dimension break, mixed,
        // model not loaded), surface it loudly as yellow even when
        // the provider is technically reachable.
        if let portability = status.portabilityStatus,
           portability != "ok" && portability != "" {
            switch portability {
            case "dimension_break", "mixed", "provider_unavailable", "model_mismatch":
                return .yellow
            default:
                break
            }
        }
        // Recent search/ask returned warnings — degraded even if the
        // vault's stored state looks fine.
        if !appState.lastSemanticWarnings.isEmpty { return .yellow }
        if status.embedAvailable && status.genAvailable {
            if status.embeddingCount < status.documentCount { return .yellow }
            return .green
        }
        if status.embedAvailable || status.genAvailable { return .yellow }
        return .gray
    }

    private var mcpDotColor: Color {
        appState.mcpStatuses.isEmpty ? .gray : .green
    }

    private var mcpLabel: String {
        let count = appState.mcpStatuses.count
        return count > 0 ? "MCP \(count)" : "MCP"
    }

    @ViewBuilder
    private var aiPopoverContent: some View {
        VStack(alignment: .leading, spacing: 8) {
            if let status = appState.aiStatus {
                HStack(spacing: 6) {
                    Circle()
                        .fill(aiDotColor)
                        .frame(width: 8, height: 8)
                    Text("AI Status")
                        .font(.headline)
                }

                Divider()

                LabeledContent("Provider") {
                    Text(status.provider)
                        .font(.callout)
                }
                LabeledContent("Embedding") {
                    Text(status.embeddingModel)
                        .font(.callout)
                        .lineLimit(1)
                }
                LabeledContent("Generation") {
                    Text(status.genModel)
                        .font(.callout)
                        .lineLimit(1)
                }
                if status.embeddingCount < status.documentCount {
                    let stale = status.documentCount - status.embeddingCount
                    LabeledContent("Embeddings") {
                        Text("\(status.embeddingCount)/\(status.documentCount) — \(stale) need indexing")
                            .font(.callout)
                            .foregroundStyle(.yellow)
                    }
                } else {
                    LabeledContent("Embeddings") {
                        Text("\(status.embeddingCount)/\(status.documentCount) docs")
                            .font(.callout)
                    }
                }

                // Portability — the self-describing "is this vault
                // ready to use here?" section. Only shown when we have
                // derived fields (older 2nb binaries don't emit them).
                if let portability = status.portabilityStatus, !portability.isEmpty {
                    Divider()
                    Text("Vault Embedding State")
                        .font(.caption)
                        .fontWeight(.semibold)
                        .foregroundStyle(.secondary)

                    if let dim = status.vaultEmbeddingDim, dim > 0 {
                        let modelLabel: String = {
                            guard let models = status.vaultEmbeddingModels, !models.isEmpty else {
                                return "(no model recorded)"
                            }
                            if models.count == 1 { return models[0] }
                            return "mixed: \(models.joined(separator: ", "))"
                        }()
                        let embeddedDocs = status.vaultEmbeddedDocs ?? 0
                        let totalDocs = status.vaultTotalDocs ?? 0
                        LabeledContent("As-embedded") {
                            Text("\(modelLabel) (\(dim)d), \(embeddedDocs)/\(totalDocs)")
                                .font(.callout)
                                .lineLimit(2)
                        }
                    }

                    LabeledContent("Status") {
                        Text(portability.replacingOccurrences(of: "_", with: " ").uppercased())
                            .font(.callout)
                            .foregroundStyle(portability == "ok" ? .green : .yellow)
                    }

                    if let action = status.portabilityAction, !action.isEmpty {
                        Text(action)
                            .font(.caption)
                            .foregroundStyle(.secondary)
                            .fixedSize(horizontal: false, vertical: true)
                            .padding(.top, 2)
                    }
                }
            } else {
                HStack(spacing: 6) {
                    Circle()
                        .fill(.gray)
                        .frame(width: 8, height: 8)
                    Text("AI Not Configured")
                        .font(.headline)
                }

                Divider()

                Text("Set up an AI provider to enable semantic search, RAG Q&A, and embeddings.")
                    .font(.callout)
                    .foregroundStyle(.secondary)

                Button("Set Up AI...") {
                    showAIPopover = false
                    appState.showAISetupWizard = true
                }
                .buttonStyle(.borderedProminent)
                .frame(maxWidth: .infinity)
            }

            Divider()

            HStack {
                if let status = appState.aiStatus,
                   status.embeddingCount < status.documentCount,
                   !appState.isIndexing {
                    Button("Rebuild Index") {
                        showAIPopover = false
                        appState.rebuildIndex()
                    }
                }
                Spacer()
                Button("Refresh") {
                    showAIPopover = false
                    Task { await appState.refreshAIStatus() }
                }
            }
        }
    }
}
