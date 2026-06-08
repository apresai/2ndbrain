import SwiftUI
import SecondBrainCore

/// The default dashboard surface. Answers the three questions that matter for
/// the common case — is this the right vault, is AI set up and working, and is
/// the vault indexed — without the catalog/benchmark/MCP/git/lint depth, which
/// lives behind the sidebar's "Advanced" section. Reuses AppState's existing
/// config/test/status/index methods.
struct HomeView: View {
    @Environment(AppState.self) private var appState

    @State private var saving = false
    @State private var testing = false
    @State private var actionMessage: String?
    @State private var actionIsError = false
    // The vault Obsidian has open, loaded once in `.task` instead of read from
    // disk on every body re-render.
    @State private var obsidianOpenVault: ObsidianRegistry.Vault?

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 20) {
                if let warning = cliVersionWarning {
                    cliWarningBanner(warning)
                }
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
        // Keyed on the active vault so switching vaults re-reads status and the
        // Obsidian registry; otherwise the cached open-vault badge could go
        // stale after a vault switch while Home stays on screen.
        .task(id: appState.vault?.rootURL) {
            // Read the (cheap, local) registry first so the match badge is
            // correct immediately, rather than flashing "unknown" while the
            // slower `2nb ai status` shell-out runs.
            obsidianOpenVault = ObsidianRegistry.load()?.openVault
            await appState.refreshCLIVersion()
            await appState.refreshAIStatus()
        }
    }

    // MARK: - CLI version drift

    /// A warning when the installed `2nb` is older than this app, else nil.
    /// A stale CLI is what made the 0.5.8 re-embed fail silently, so surface it
    /// before the user hits an action that depends on a newer CLI.
    private var cliVersionWarning: String? {
        guard CLIVersion.isOlder(cli: appState.cliVersion, thanApp: appVersion) else { return nil }
        return "Your 2nb CLI (\(appState.cliVersion ?? "unknown")) is older than this app (\(appVersion)). Some actions may fail until you update it."
    }

    @ViewBuilder
    private func cliWarningBanner(_ message: String) -> some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack(spacing: 6) {
                Image(systemName: "exclamationmark.triangle.fill")
                    .foregroundStyle(.orange)
                Text(message)
                    .font(.callout)
            }
            Text("brew upgrade apresai/tap/twonb")
                .font(.caption.monospaced())
                .textSelection(.enabled)
                .foregroundStyle(.secondary)
        }
        .padding(12)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(Color.orange.opacity(0.12), in: RoundedRectangle(cornerRadius: 8))
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
        let openVault = obsidianOpenVault
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
            LabeledContent("Generation", value: HomeAI.friendlyModel(status?.genModel) ?? "Claude Haiku 4.5")
            LabeledContent("Embeddings", value: HomeAI.friendlyModel(status?.embeddingModel) ?? "Amazon Nova-2")
            HStack(spacing: 6) {
                Circle().fill(ready ? Color.green : Color.red).frame(width: 8, height: 8)
                Text(HomeAI.statusLine(status))
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

    private func save() async {
        saving = true
        actionMessage = nil
        defer { saving = false }
        do {
            try await appState.saveAIConfig(
                provider: HomeAI.provider,
                embedModel: HomeAI.embedModel,
                genModel: HomeAI.genModel,
                dims: HomeAI.dims,
                bedrockProfile: "",
                bedrockRegion: "",
                openrouterKey: ""
            )
            await appState.refreshAIStatus()
            actionIsError = false
            // If the just-saved model no longer matches the vault's stored
            // embeddings, nudge toward Re-embed All; otherwise plain confirm.
            actionMessage = HomeAI.reembedHintAfterSave(appState.aiStatus)
                ?? "Saved: AWS Bedrock · Claude Haiku 4.5 · Amazon Nova-2."
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
            _ = try await appState.testAndSave(modelID: HomeAI.embedModel, provider: HomeAI.provider, type: "embedding", scope: "vault")
            _ = try await appState.testAndSave(modelID: HomeAI.genModel, provider: HomeAI.provider, type: "generation", scope: "vault")
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
        // Embeddable docs (excludes empty notes) as the denominator, so a blank
        // Untitled.md doesn't read as a permanent "X / Y" gap.
        let embeddable = status?.embeddableDenominator ?? docs
        return VStack(alignment: .leading, spacing: 8) {
            SheetSectionHeader(title: "Index", systemImage: "square.stack.3d.up")
            LabeledContent("Documents", value: "\(docs)")
            LabeledContent("Embedded", value: "\(embedded) / \(embeddable)")
            HStack {
                Button("Rebuild Index") { actionMessage = nil; appState.rebuildIndex() }
                Button("Re-embed All…") { actionMessage = nil; appState.rebuildIndex(forceReembed: true) }
            }
            .disabled(appState.vault == nil || appState.isIndexing)
            .padding(.top, 4)
        }
    }
}

/// Pure presentation logic for the Home AI card, plus the shipped Bedrock
/// defaults (mirroring the CLI's `DefaultAIConfig`). Extracted from `HomeView`
/// so the model-name and status-line mapping are unit-testable.
enum HomeAI {
    static let provider = "bedrock"
    static let genModel = "us.anthropic.claude-haiku-4-5-20251001-v1:0"
    static let embedModel = "amazon.nova-2-multimodal-embeddings-v1:0"
    static let dims = 1024

    /// Friendly name for the two default Bedrock models; the raw id for any
    /// other non-empty model; `nil` when no model is set.
    static func friendlyModel(_ id: String?) -> String? {
        switch id {
        case genModel: return "Claude Haiku 4.5"
        case embedModel: return "Amazon Nova-2"
        case .some(let value) where !value.isEmpty: return value
        default: return nil
        }
    }

    /// Plain-language readiness line for the AI card, preferring the provider's
    /// own `reason` (actionable) over a generic credentials hint.
    static func statusLine(_ status: AIStatusInfo?) -> String {
        guard let status else { return "Checking…" }
        if status.embedAvailable && status.genAvailable { return "Bedrock ready" }
        if let reason = status.providers?.first(where: { $0.name == provider })?.reason,
           !reason.isEmpty {
            return "Not ready — \(reason)"
        }
        return "Not ready — check AWS credentials"
    }

    /// After saving the default config, the vault's stored embeddings may no
    /// longer match the active embedding model — different dimensions
    /// (`dimension_break`), a mix of models (`mixed`), or a straight model swap
    /// (`model_mismatch`). In those cases search silently falls back to keyword
    /// matching until the embeddings are regenerated, so return a gentle, fully
    /// self-contained "Saved…" message that points the user at Re-embed All.
    /// Returns `nil` when the embeddings are fine and the plain confirmation
    /// should show instead.
    static func reembedHintAfterSave(_ status: AIStatusInfo?) -> String? {
        switch status?.portabilityStatus {
        case "dimension_break", "mixed", "model_mismatch":
            // "less accurate" is the honest framing across all three: a
            // dimension break forces keyword-only search, while a same-dim
            // mismatch/mix keeps semantic search running but on vectors from a
            // different model — degraded relevance, not zero results.
            return "Saved. This vault's embeddings were made with a different model, so semantic search may be less accurate until you regenerate them. Run Re-embed All when you have a moment."
        default:
            return nil
        }
    }
}
