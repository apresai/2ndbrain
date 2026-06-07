import SwiftUI
import SecondBrainCore

/// The default dashboard surface. Answers the three questions that matter for
/// the common case — is this the right vault, is AI set up and working, and is
/// the vault indexed — without the catalog/benchmark/MCP/git/lint depth, which
/// lives behind the sidebar's "Advanced" section. Reuses AppState's existing
/// config/test/status/index methods.
struct HomeView: View {
    @Environment(AppState.self) private var appState

    // The shipped defaults (DefaultAIConfig): AWS Bedrock + Haiku 4.5 + Nova-2.
    private let bedrockProvider = "bedrock"
    private let bedrockGenModel = "us.anthropic.claude-haiku-4-5-20251001-v1:0"
    private let bedrockEmbedModel = "amazon.nova-2-multimodal-embeddings-v1:0"
    private let bedrockDims = 1024

    @State private var saving = false
    @State private var testing = false
    @State private var actionMessage: String?
    @State private var actionIsError = false

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 20) {
                vaultCard
                Divider()
                aiCard
                Divider()
                indexCard
                if let actionMessage {
                    Text(actionMessage)
                        .font(.callout)
                        .foregroundStyle(actionIsError ? .red : .secondary)
                }
            }
            .padding(24)
            .frame(maxWidth: 640, alignment: .leading)
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
        .task { await appState.refreshAIStatus() }
    }

    // MARK: - Vault

    private var vaultCard: some View {
        VStack(alignment: .leading, spacing: 8) {
            SheetSectionHeader(title: "Vault", systemImage: "folder")
            if let vault = appState.vault {
                LabeledContent("Name", value: vault.rootURL.lastPathComponent)
                LabeledContent("Path", value: vault.rootURL.path)
                obsidianMatchBadge(for: vault.rootURL)
            } else {
                Text("No vault open").foregroundStyle(.secondary)
            }
        }
    }

    @ViewBuilder
    private func obsidianMatchBadge(for url: URL) -> some View {
        let openVault = ObsidianRegistry.load()?.openVault
        let matches = openVault.map {
            ObsidianRegistry.normalizedPath($0.url) == ObsidianRegistry.normalizedPath(url)
        } ?? false
        HStack(spacing: 6) {
            Image(systemName: matches ? "checkmark.seal.fill" : "exclamationmark.triangle.fill")
                .foregroundStyle(matches ? Color.green : Color.orange)
            if matches {
                Text("This is the vault Obsidian has open")
            } else if let openVault {
                Text("Obsidian has “\(openVault.name)” open, not this folder")
            } else {
                Text("Obsidian's open vault is unknown")
            }
        }
        .font(.callout)
    }

    // MARK: - AI

    private var aiCard: some View {
        let status = appState.aiStatus
        let ready = (status?.embedAvailable ?? false) && (status?.genAvailable ?? false)
        return VStack(alignment: .leading, spacing: 8) {
            SheetSectionHeader(title: "AI — AWS Bedrock", systemImage: "bolt.horizontal")
            LabeledContent("Generation", value: friendlyModel(status?.genModel) ?? "Claude Haiku 4.5")
            LabeledContent("Embeddings", value: friendlyModel(status?.embeddingModel) ?? "Amazon Nova-2")
            HStack(spacing: 6) {
                Circle().fill(ready ? Color.green : Color.red).frame(width: 8, height: 8)
                Text(statusLine(status))
            }
            .font(.callout)
            HStack {
                Button(saving ? "Saving…" : "Save as default") {
                    Task { await save() }
                }
                Button(testing ? "Testing…" : "Test") {
                    Task { await test() }
                }
            }
            .disabled(saving || testing || appState.vault == nil)
            .padding(.top, 4)
        }
    }

    private func statusLine(_ status: AIStatusInfo?) -> String {
        guard let status else { return "Checking…" }
        if status.embedAvailable && status.genAvailable { return "Bedrock ready" }
        if let reason = status.providers?.first(where: { $0.name == bedrockProvider })?.reason,
           !reason.isEmpty {
            return "Not ready — \(reason)"
        }
        return "Not ready — check AWS credentials"
    }

    /// Friendly name for the two default Bedrock models; the raw id otherwise.
    private func friendlyModel(_ id: String?) -> String? {
        switch id {
        case bedrockGenModel: return "Claude Haiku 4.5"
        case bedrockEmbedModel: return "Amazon Nova-2"
        case .some(let value) where !value.isEmpty: return value
        default: return nil
        }
    }

    private func save() async {
        saving = true
        actionMessage = nil
        defer { saving = false }
        do {
            try await appState.saveAIConfig(
                provider: bedrockProvider,
                embedModel: bedrockEmbedModel,
                genModel: bedrockGenModel,
                dims: bedrockDims,
                bedrockProfile: "",
                bedrockRegion: "",
                openrouterKey: ""
            )
            await appState.refreshAIStatus()
            actionIsError = false
            actionMessage = "Saved: AWS Bedrock · Claude Haiku 4.5 · Amazon Nova-2."
        } catch {
            actionIsError = true
            actionMessage = "Save failed: \(error.localizedDescription)"
        }
    }

    private func test() async {
        testing = true
        actionMessage = nil
        defer { testing = false }
        do {
            _ = try await appState.testAndSave(modelID: bedrockEmbedModel, provider: bedrockProvider, type: "embedding", scope: "vault")
            _ = try await appState.testAndSave(modelID: bedrockGenModel, provider: bedrockProvider, type: "generation", scope: "vault")
            await appState.refreshAIStatus()
            actionIsError = false
            actionMessage = "Test passed: both models responded."
        } catch {
            actionIsError = true
            actionMessage = "Test failed: \(error.localizedDescription)"
        }
    }

    // MARK: - Index

    private var indexCard: some View {
        let status = appState.aiStatus
        let docs = status?.documentCount ?? 0
        let embedded = status?.embeddingCount ?? 0
        return VStack(alignment: .leading, spacing: 8) {
            SheetSectionHeader(title: "Index", systemImage: "square.stack.3d.up")
            LabeledContent("Documents", value: "\(docs)")
            LabeledContent("Embedded", value: "\(embedded) / \(docs)")
            HStack {
                Button("Rebuild Index") { actionMessage = nil; appState.rebuildIndex() }
                Button("Re-embed All…") { actionMessage = nil; appState.rebuildIndex(forceReembed: true) }
            }
            .disabled(appState.vault == nil || appState.isIndexing)
            .padding(.top, 4)
        }
    }
}
