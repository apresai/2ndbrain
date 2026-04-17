import SwiftUI
import SecondBrainCore

struct SheetSectionHeader: View {
    let title: String
    let systemImage: String

    var body: some View {
        HStack(spacing: 6) {
            Image(systemName: systemImage)
                .foregroundStyle(.secondary)
            Text(title)
                .font(.headline)
        }
    }
}

struct VaultStatusView: View {
    @Environment(AppState.self) var appState
    @Binding var isPresented: Bool

    @State private var staleCount: Int = 0
    @State private var staleLoading: Bool = true
    @State private var showReembedConfirm: Bool = false

    var body: some View {
        VStack(spacing: 0) {
            header

            ScrollView {
                VStack(alignment: .leading, spacing: 20) {
                    vaultSection
                    Divider()
                    indexSection
                    Divider()
                    embeddingsSection
                    Divider()
                    staleSection
                    Divider()
                    providerSection
                }
                .padding(20)
            }

            footer
        }
        .frame(width: 560, height: 620)
        .task {
            await refresh()
        }
        .confirmationDialog(
            "Re-embed all documents?",
            isPresented: $showReembedConfirm,
            titleVisibility: .visible
        ) {
            Button("Re-embed All", role: .destructive) {
                appState.rebuildIndex(forceReembed: true)
                isPresented = false
            }
            Button("Cancel", role: .cancel) { }
        } message: {
            Text("This invalidates every stored embedding and regenerates them from scratch. Useful when switching embedding models or fixing dimension/model mismatches. May take several minutes.")
        }
    }

    // MARK: - Header / Footer

    private var header: some View {
        HStack {
            Image(systemName: "externaldrive")
                .font(.title2)
                .foregroundStyle(Color.accentColor)
            Text("Vault Status")
                .font(.title2)
                .fontWeight(.semibold)
            Spacer()
            Button {
                Task { await refresh() }
            } label: {
                Image(systemName: "arrow.clockwise")
            }
            .buttonStyle(.plain)
            .help("Refresh")
        }
        .padding(.horizontal, 20)
        .padding(.vertical, 14)
        .background(Color(nsColor: .windowBackgroundColor))
        .overlay(alignment: .bottom) { Divider() }
    }

    private var footer: some View {
        HStack {
            Spacer()
            Button("Close") {
                isPresented = false
            }
            .keyboardShortcut(.cancelAction)
        }
        .padding(16)
        .background(Color(nsColor: .windowBackgroundColor))
        .overlay(alignment: .top) { Divider() }
    }

    // MARK: - Sections

    private var vaultSection: some View {
        VStack(alignment: .leading, spacing: 8) {
            SheetSectionHeader(title: "Vault", systemImage: "folder")

            if let vault = appState.vault {
                LabeledContent("Name", value: vault.rootURL.lastPathComponent)
                LabeledContent("Path") {
                    Text(vault.rootURL.path)
                        .font(.caption)
                        .foregroundStyle(.secondary)
                        .textSelection(.enabled)
                        .lineLimit(1)
                        .truncationMode(.middle)
                }
                LabeledContent("Documents", value: "\(appState.aiStatus?.documentCount ?? 0)")
            } else {
                Text("No vault open")
                    .foregroundStyle(.secondary)
            }
        }
    }

    private var indexSection: some View {
        VStack(alignment: .leading, spacing: 8) {
            SheetSectionHeader(title: "Index", systemImage: "books.vertical")

            if appState.isIndexing {
                HStack(spacing: 8) {
                    ProgressView().controlSize(.small)
                    Text("Indexing...")
                        .foregroundStyle(.secondary)
                }
            } else if let err = appState.indexError {
                HStack(spacing: 6) {
                    Image(systemName: "exclamationmark.triangle.fill")
                        .foregroundStyle(.red)
                    Text("Index failed: \(err)")
                        .foregroundStyle(.red)
                        .font(.callout)
                }
            } else {
                LabeledContent("State", value: "Up to date")
            }

            HStack {
                Button("Rebuild Index") {
                    appState.rebuildIndex()
                    isPresented = false
                }
                .disabled(appState.vault == nil || appState.isIndexing)
                .help("Full re-index of all documents")
                Spacer()
            }
        }
    }

    private var embeddingsSection: some View {
        VStack(alignment: .leading, spacing: 8) {
            SheetSectionHeader(title: "Embeddings", systemImage: "square.stack.3d.up")

            if let status = appState.aiStatus {
                LabeledContent("Provider", value: status.provider.capitalized)
                LabeledContent("Model", value: status.embeddingModel.isEmpty ? "—" : status.embeddingModel)
                LabeledContent("Embedded", value: "\(status.embeddingCount) / \(status.documentCount) documents")

                if let portability = status.portabilityStatus, !portability.isEmpty {
                    HStack(spacing: 6) {
                        Circle()
                            .fill(portabilityColor(portability))
                            .frame(width: 8, height: 8)
                        Text(portabilityLabel(portability))
                            .font(.callout)
                            .fontWeight(.medium)
                    }
                    if let action = status.portabilityAction, !action.isEmpty {
                        Text(action)
                            .font(.caption)
                            .foregroundStyle(.secondary)
                            .fixedSize(horizontal: false, vertical: true)
                    }
                }
            } else {
                Text("No AI status available")
                    .foregroundStyle(.secondary)
            }

            HStack {
                Button("Re-embed All...") {
                    showReembedConfirm = true
                }
                .disabled(appState.vault == nil || appState.isIndexing)
                .help("Invalidate and regenerate all embeddings")
                Spacer()
            }
        }
    }

    private var staleSection: some View {
        VStack(alignment: .leading, spacing: 8) {
            SheetSectionHeader(title: "Stale Documents", systemImage: "clock.badge.exclamationmark")

            if staleLoading {
                HStack(spacing: 8) {
                    ProgressView().controlSize(.small)
                    Text("Loading...")
                        .foregroundStyle(.secondary)
                }
            } else if staleCount == 0 {
                Text("No documents older than 90 days.")
                    .foregroundStyle(.secondary)
            } else {
                Text("\(staleCount) document\(staleCount == 1 ? "" : "s") not modified in the last 90 days.")
                    .foregroundStyle(.secondary)
            }
        }
    }

    private var providerSection: some View {
        VStack(alignment: .leading, spacing: 8) {
            SheetSectionHeader(title: "AI Provider", systemImage: "bolt.horizontal")

            if let status = appState.aiStatus {
                LabeledContent("Embeddings") {
                    HStack(spacing: 6) {
                        Circle()
                            .fill(status.embedAvailable ? Color.green : Color.red)
                            .frame(width: 8, height: 8)
                        Text(status.embedAvailable ? "Reachable" : "Unavailable")
                            .font(.callout)
                    }
                }
                LabeledContent("Generation") {
                    HStack(spacing: 6) {
                        Circle()
                            .fill(status.genAvailable ? Color.green : Color.red)
                            .frame(width: 8, height: 8)
                        Text(status.genAvailable ? "Reachable" : "Unavailable")
                            .font(.callout)
                    }
                }
            } else {
                Text("AI not configured")
                    .foregroundStyle(.secondary)
            }

            HStack {
                Button("Test Connection...") {
                    isPresented = false
                    appState.showAITest = true
                }
                .disabled(appState.vault == nil)
                Button("AI Setup...") {
                    isPresented = false
                    appState.showAISetupWizard = true
                }
                .disabled(appState.vault == nil)
                Spacer()
            }
        }
    }

    // MARK: - Helpers

    private func portabilityColor(_ status: String) -> Color {
        switch status {
        case "ok": return .green
        case "unindexed": return .gray
        case "dimension_break", "provider_unavailable", "mixed": return .yellow
        case "db_too_new": return .red
        default: return .gray
        }
    }

    private func portabilityLabel(_ status: String) -> String {
        switch status {
        case "ok": return "Healthy"
        case "unindexed": return "Not yet indexed"
        case "dimension_break": return "Dimension mismatch"
        case "provider_unavailable": return "Provider unavailable"
        case "mixed": return "Mixed embedding models"
        case "model_mismatch": return "Model mismatch"
        case "db_too_new": return "DB schema too new"
        default: return status.replacingOccurrences(of: "_", with: " ").capitalized
        }
    }

    private func refresh() async {
        async let aiTask: Void = appState.refreshAIStatus()
        async let staleTask: Void = loadStale()
        _ = await (aiTask, staleTask)
    }

    private func loadStale() async {
        guard let vault = appState.vault else {
            staleLoading = false
            staleCount = 0
            return
        }
        staleLoading = true
        do {
            let data = try await appState.runCLI(
                ["stale", "--since", "90", "--json", "--porcelain"],
                cwd: vault.rootURL
            )
            let docs = (try? JSONDecoder().decode([StaleDocInfo].self, from: data)) ?? []
            staleCount = docs.count
        } catch {
            staleCount = 0
        }
        staleLoading = false
    }
}

private struct StaleDocInfo: Codable {
    let path: String
    let title: String
}
