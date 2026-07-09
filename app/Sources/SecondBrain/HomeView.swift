import SwiftUI
import SecondBrainCore
#if canImport(AppKit)
import AppKit
#endif

/// The default dashboard surface. Answers the three questions that matter for
/// the common case — is this the right vault, is AI set up and working, and is
/// the vault indexed — without the catalog/benchmark/MCP/git/lint depth, which
/// lives behind the sidebar's "Advanced" section. Reuses AppState's existing
/// config/test/status/index methods.
struct HomeView: View {
    @Environment(AppState.self) private var appState

    @State private var saving = false
    @State private var testing = false
    @State private var updatingCLI = false
    @State private var installingPlugin = false
    // The id of the client currently being configured (nil = none in flight), so
    // only that row's button shows "Configuring…" and all Configure buttons
    // disable while one runs.
    @State private var configuringClient: String?
    @State private var actionMessage: String?
    @State private var actionIsError = false
    // The vault Obsidian has open, loaded once in `.task` instead of read from
    // disk on every body re-render.
    @State private var obsidianOpenVault: ObsidianRegistry.Vault?
    // Installed Obsidian plugin version (nil = not installed), read from the
    // vault's plugin manifest in `.task` and re-read after an install.
    @State private var pluginVersion: String?

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 20) {
                if let warning = cliVersionWarning {
                    cliWarningBanner(warning)
                }
                if let stale = indexStaleWarning {
                    indexStaleBanner(stale)
                }
                vaultCard
                Divider()
                aiCard
                Divider()
                aiClientsCard
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
            pluginVersion = appState.vault.flatMap { ObsidianPlugin.installedVersion(vaultRoot: $0.rootURL) }
            await appState.refreshCLIVersion()
            await appState.refreshAIStatus()
            await appState.refreshSkillStatus()
            await appState.refreshMCPConfigured()
            await appState.refreshGlobalInstructions()
        }
    }

    // MARK: - CLI version drift

    /// A warning when the `2nb` the app resolves is older than this app, else
    /// nil. Since the app now bundles a version-matched CLI (Contents/Resources/2nb,
    /// preferred by `CLIPath.resolve()`), this is silent in a normal release:
    /// the stale-CLI failure mode behind the 0.5.8 re-embed bug can't happen for
    /// the app's own calls anymore. It still surfaces on dev builds that fall
    /// back to a stale Homebrew copy, and the Update CLI button below remains
    /// useful for refreshing the terminal/plugin's Homebrew `2nb`.
    private var cliVersionWarning: String? {
        guard CLIVersion.isOlder(cli: appState.cliVersion, thanApp: appVersion) else { return nil }
        return "Your 2nb CLI (\(appState.cliVersion ?? "unknown")) is older than this app (\(appVersion)). Some actions may fail until you update it."
    }

    /// A newer 2nb changed indexing/embedding logic, so this vault's index is
    /// stale (mirrors the CLI's `upgrade_reindex/reembed_recommended` portability
    /// states). The button runs the same reindex/re-embed the CLI would prompt.
    private var indexStaleWarning: (message: String, forceReembed: Bool)? {
        switch appState.aiStatus?.portabilityStatus {
        case "upgrade_reembed_recommended":
            return ("A newer 2nb improved chunking and embeddings for this vault. Re-embed to apply the improvements.", true)
        case "upgrade_reindex_recommended":
            return ("A newer 2nb improved indexing for this vault. Reindex to apply the improvements.", false)
        default:
            return nil
        }
    }

    @ViewBuilder
    private func indexStaleBanner(_ info: (message: String, forceReembed: Bool)) -> some View {
        HStack(spacing: 8) {
            Image(systemName: "exclamationmark.triangle.fill")
                .foregroundStyle(.orange)
            Text(info.message)
                .font(.callout)
            Spacer()
            Button(info.forceReembed ? "Re-embed" : "Reindex") {
                actionMessage = nil
                appState.rebuildIndex(forceReembed: info.forceReembed)
            }
            .controlSize(.small)
            .disabled(appState.isIndexing)
        }
        .padding(12)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(Color.orange.opacity(0.12), in: RoundedRectangle(cornerRadius: 8))
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
            HStack(spacing: 10) {
                // Real button when brew is present; the copyable command stays
                // either way (some users prefer the terminal, and it's the
                // fallback when Homebrew isn't installed).
                if let brew = BrewLocator.resolve() {
                    Button(updatingCLI ? "Updating…" : "Update CLI") {
                        Task { await updateCLI(brew: brew) }
                    }
                    .disabled(updatingCLI)
                    .controlSize(.small)
                }
                Text("brew upgrade apresai/tap/twonb")
                    .font(.caption.monospaced())
                    .textSelection(.enabled)
                    .foregroundStyle(.secondary)
            }
        }
        .padding(12)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(Color.orange.opacity(0.12), in: RoundedRectangle(cornerRadius: 8))
    }

    private func updateCLI(brew: String) async {
        updatingCLI = true
        actionMessage = nil
        defer { updatingCLI = false }
        let before = appState.cliVersion
        do {
            try await appState.upgradeCLI(brewPath: brew)
            actionIsError = false
            actionMessage = HomeCLIUpdate.resultMessage(before: before, after: appState.cliVersion)
        } catch {
            actionIsError = true
            actionMessage = "CLI update failed: \(error.localizedDescription)"
        }
    }

    // MARK: - Vault

    private var vaultCard: some View {
        VStack(alignment: .leading, spacing: 8) {
            SheetSectionHeader(title: "Vault", systemImage: "folder")
            if let vault = appState.vault {
                LabeledContent("Name", value: vault.rootURL.lastPathComponent)
                LabeledContent("Path", value: vault.rootURL.path)
                obsidianMatchBadge(for: vault.rootURL)
                pluginRow(for: vault.rootURL)
            } else {
                Text("No vault open").foregroundStyle(.secondary)
            }
        }
    }

    @ViewBuilder
    private func pluginRow(for vaultRoot: URL) -> some View {
        let state = HomePlugin.rowState(installed: pluginVersion, appVersion: appVersion)
        HStack(spacing: 8) {
            Text("Obsidian plugin: \(state.label)")
            if let buttonTitle = state.button {
                Button(installingPlugin ? "Installing…" : buttonTitle) {
                    Task { await installPlugin(into: vaultRoot) }
                }
                .disabled(installingPlugin)
                .controlSize(.small)
            }
        }
        .font(.callout)
    }

    private func installPlugin(into vaultRoot: URL) async {
        installingPlugin = true
        actionMessage = nil
        defer { installingPlugin = false }
        let wasInstalled = pluginVersion != nil
        do {
            try await appState.installObsidianPlugin()
            pluginVersion = ObsidianPlugin.installedVersion(vaultRoot: vaultRoot)
            actionIsError = false
            actionMessage = HomePlugin.successMessage(updated: wasInstalled)
        } catch {
            actionIsError = true
            actionMessage = "Plugin install failed: \(error.localizedDescription)"
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
        // Rerank is an optional, default-OFF stage; show its state but keep it out
        // of the core ready dot (embed+gen), so an unavailable reranker doesn't
        // read as a core-AI failure.
        let rerankValue: String
        if status?.rerankEnabled == true {
            let model = HomeAI.modelValue(status?.rerankModel)
            rerankValue = (status?.rerankAvailable == false) ? "\(model) (unavailable)" : model
        } else {
            rerankValue = "Off"
        }
        return VStack(alignment: .leading, spacing: 8) {
            SheetSectionHeader(title: HomeAI.headerTitle(status), systemImage: "bolt.horizontal")
            LabeledContent("Generation") { modelText(status?.genModel) }
            LabeledContent("Embeddings") { modelText(status?.embeddingModel) }
            LabeledContent("Rerank", value: rerankValue)
            HStack(spacing: 6) {
                Circle().fill(ready ? Color.green : Color.red).frame(width: 8, height: 8)
                Text(HomeAI.statusLine(status))
            }
            .font(.callout)
            HStack {
                // The reset path only appears when the config has drifted from
                // the recommended defaults, and always confirms first: the old
                // "Save as default" silently reverted a user's chosen
                // provider/models to the hardcoded Bedrock defaults.
                if HomeAI.differsFromDefaults(status) {
                    Button(saving ? "Resetting…" : "Reset to recommended defaults") {
                        Task { await resetToRecommendedDefaults() }
                    }
                }
                Button(testing ? "Testing…" : "Test") {
                    Task { await test() }
                }
            }
            .disabled(saving || testing || appState.vault == nil)
            .padding(.top, 4)
        }
    }

    /// Raw active model id, monospaced with middle truncation: the honest
    /// display (any friendly-name table in Swift would drift from the CLI).
    private func modelText(_ id: String?) -> some View {
        Text(HomeAI.modelValue(id))
            .font(.body.monospaced())
            .lineLimit(1)
            .truncationMode(.middle)
    }

    private func resetToRecommendedDefaults() async {
        #if canImport(AppKit)
        let alert = NSAlert()
        alert.messageText = "Reset AI configuration to the recommended defaults?"
        alert.informativeText = HomeAI.resetConfirmText(appState.aiStatus)
        alert.addButton(withTitle: "Reset")
        alert.addButton(withTitle: "Cancel")
        guard alert.runModal() == .alertFirstButtonReturn else { return }
        #endif
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
            // embeddings, nudge toward Re-embed All; otherwise confirm with
            // the values that were actually written.
            actionMessage = HomeAI.reembedHintAfterSave(appState.aiStatus)
                ?? "Reset to \(ProviderDisplay.name(HomeAI.provider)) · \(HomeAI.genModel) · \(HomeAI.embedModel)."
        } catch {
            actionIsError = true
            actionMessage = "Reset failed: \(error.localizedDescription)"
        }
    }

    /// Probe the ACTIVE models, not the shipped defaults: testing anything
    /// else answers a question the user didn't ask. Results are branched on
    /// each probe's ok flag (the CLI exits 0 for a failed probe).
    private func test() async {
        guard let status = appState.aiStatus else {
            actionIsError = true
            actionMessage = "AI status not loaded yet; try again in a moment."
            return
        }
        let ids = [status.embeddingModel, status.genModel].filter { !$0.isEmpty }
        guard await confirmPaidOperation(appState: appState, modelIDs: ids, probe: "test", operation: "Test the active models") else { return }
        testing = true
        actionMessage = nil
        defer { testing = false }
        do {
            var failures: [String] = []
            var tested = 0
            func failureLine(_ r: AIProbeResult, model: String) -> String {
                let guidance = ModelAccessPresentation.guidance(code: r.errorCode, provider: status.provider, remediation: r.remediation, strategy: r.invokeStrategy)
                if let g = guidance { return "\(model) [\(g.badge)]: \(g.advice)" }
                return "\(model): \(r.detail ?? "failed")"
            }
            if !status.embeddingModel.isEmpty {
                tested += 1
                let r = try await appState.testAndSave(modelID: status.embeddingModel, provider: status.provider, type: "embedding", scope: "vault")
                if !r.ok { failures.append(failureLine(r, model: status.embeddingModel)) }
            }
            if !status.genModel.isEmpty {
                tested += 1
                let r = try await appState.testAndSave(modelID: status.genModel, provider: status.provider, type: "generation", scope: "vault")
                if !r.ok { failures.append(failureLine(r, model: status.genModel)) }
            }
            await appState.refreshAIStatus()
            if tested == 0 {
                actionIsError = true
                actionMessage = "No active models configured; pick models in the AI Hub first."
            } else if failures.isEmpty {
                actionIsError = false
                actionMessage = "Test passed: \(status.embeddingModel) and \(status.genModel) responded."
            } else {
                actionIsError = true
                actionMessage = "Test failed: \(failures.joined(separator: " / "))"
            }
        } catch {
            actionIsError = true
            actionMessage = "Test failed: \(error.localizedDescription)"
        }
    }

    // MARK: - AI Clients

    /// "Is my AI tooling wired up?": one row per AI client (Claude Code, Warp,
    /// Claude Desktop, Codex), each showing its skill status (where the client
    /// has a skill) + MCP-configured status + a single Configure button that
    /// shells `2nb setup --client <key>`. These are AI-client artifacts, distinct
    /// from the Obsidian plugin row on the Vault card (a vault artifact), so they
    /// get their own card. The Claude Code Verify panel + cross-dependency
    /// callout live under the Claude Code row only.
    private var aiClientsCard: some View {
        VStack(alignment: .leading, spacing: 14) {
            SheetSectionHeader(title: "AI Clients", systemImage: "sparkles")
            ForEach(ClientDescriptor.all) { client in
                clientRow(client)
            }
        }
    }

    @ViewBuilder
    private func clientRow(_ client: ClientDescriptor) -> some View {
        let skill = client.skillSlug.flatMap { appState.skillStatus(forSlug: $0) }
        let mcp = appState.mcpConfigured(forClient: client.mcpClientKey)
        let mcpState = ClientConfig.mcpRow(mcp)
        VStack(alignment: .leading, spacing: 6) {
            HStack(spacing: 8) {
                Label(client.displayName, systemImage: client.systemImage)
                    .font(.callout.weight(.medium))
                Spacer()
                Button(configuringClient == client.id ? "Configuring…" : "Configure") {
                    Task { await configureClient(client) }
                }
                .controlSize(.small)
                .disabled(configuringClient != nil || appState.vault == nil)
            }
            if client.skillSlug != nil {
                let skillState = ClientConfig.skillRow(skill)
                statusLine(ok: skillState.ok, text: "Skill: \(skillState.label)")
            }
            statusLine(ok: mcpState.ok, text: "MCP server: \(mcpState.label)")
            // The config file path, once configured, so the user can find what
            // was written (e.g. ~/.claude.json, ~/.warp/.mcp.json).
            if mcpState.ok, let path = mcp?.configPath, !path.isEmpty {
                Text(path)
                    .font(.caption.monospaced())
                    .foregroundStyle(.secondary)
                    .textSelection(.enabled)
            }
            // Global-instructions row: only for clients with a memory file
            // (claude-code, claude-desktop), which are the only ones the CLI
            // returns from `instructions configured --all`.
            if let gi = appState.globalInstructions(forClient: client.mcpClientKey) {
                let giState = ClientConfig.globalInstructionsRow(gi)
                statusLine(ok: giState.ok, text: "Global instructions: \(giState.label)")
            }
            if let note = client.note, !note.isEmpty {
                Text(note)
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
            // Claude Code keeps the richer affordances: the cross-dependency
            // callout (it needs BOTH skill + MCP), a Show-setup snippet fallback,
            // and the end-to-end Verify panel.
            if client.id == ClientDescriptor.claudeCode.id {
                if let dep = crossDepMessage {
                    Label(dep, systemImage: "link")
                        .font(.caption)
                        .foregroundStyle(.orange)
                }
                Button("Show setup") {
                    Task {
                        await appState.loadMCPSetup()
                        appState.showMCPSetup = true
                    }
                }
                .controlSize(.small)
                ClaudeCodeHealthView()
            }
        }
    }

    @ViewBuilder
    private func statusLine(ok: Bool, text: String) -> some View {
        HStack(spacing: 6) {
            Image(systemName: ok ? "checkmark.circle.fill" : "circle")
                .foregroundStyle(ok ? Color.green : Color.secondary)
            Text(text)
        }
        .font(.callout)
    }

    /// Claude Code needs BOTH the skill and the MCP server; warn when only one
    /// is set up.
    private var crossDepMessage: String? {
        let skill = appState.skillStatus(forSlug: "claude-code")
        let skillInstalled = (skill?.userInstalled ?? false) || (skill?.projectInstalled ?? false)
        let mcpConfigured = appState.mcpConfigured?.configured ?? false
        return ClaudeCodeHealth.crossDependency(skillInstalled: skillInstalled, mcpConfigured: mcpConfigured)
    }

    /// Install the skill (where applicable) + configure the MCP server for one
    /// client behind a confirm (it edits an external config; a backup is saved),
    /// then re-check both statuses.
    private func configureClient(_ client: ClientDescriptor) async {
        #if canImport(AppKit)
        let confirm = ClientConfig.configureConfirm(client)
        let alert = NSAlert()
        alert.messageText = confirm.title
        alert.informativeText = confirm.info
        alert.addButton(withTitle: "Configure")
        alert.addButton(withTitle: "Cancel")
        guard alert.runModal() == .alertFirstButtonReturn else { return }
        #endif
        configuringClient = client.id
        actionMessage = nil
        defer { configuringClient = nil }
        do {
            let results = try await appState.setupClient(client.mcpClientKey)
            await appState.refreshSkillStatus()
            await appState.refreshMCPConfigured()
            await appState.refreshGlobalInstructions()
            // `2nb setup` always exits 0, so trust the per-client result, not the
            // exit code: surface a real error or a still-needed manual step
            // instead of a false "Configured" (e.g. Codex with no `codex` CLI).
            let result = results.first { $0.client == client.mcpClientKey }
            switch ClientConfig.configureOutcome(client, result: result) {
            case .success(let msg):
                actionIsError = false
                actionMessage = msg
            case .manual(let msg):
                actionIsError = false
                actionMessage = msg
            case .failure(let msg):
                actionIsError = true
                actionMessage = msg
            }
        } catch {
            actionIsError = true
            actionMessage = "Configure failed: \(error.localizedDescription)"
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
        let pending = max(0, embeddable - embedded)
        return VStack(alignment: .leading, spacing: 8) {
            SheetSectionHeader(title: "Index", systemImage: "square.stack.3d.up")
            LabeledContent("Documents", value: "\(docs)")
            LabeledContent("Embedded", value: "\(embedded) / \(embeddable)")
            if pending > 0 {
                Text("\(pending) note\(pending == 1 ? "" : "s") awaiting embedding. Sync to catch up.")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
            HStack {
                Button("Sync") { actionMessage = nil; appState.rebuildIndex() }
                    .help("Index new and changed notes and embed only what changed (reconciles notes added, edited, or deleted in Obsidian). Notes edited while the app is open sync automatically.")
                Button("Re-embed All…") { actionMessage = nil; appState.rebuildIndex(forceReembed: true) }
                    .help("Regenerate every embedding from scratch (use after switching embedding models).")
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
    /// Recommended defaults: a unit-tested mirror of the CLI's
    /// DefaultAIConfig (cli/internal/ai/config.go). The card never renders
    /// these as if they were the live config; they exist only as the target
    /// of the explicit Reset action and its drift check.
    static let provider = "bedrock"
    static let genModel = "us.anthropic.claude-haiku-4-5-20251001-v1:0"
    static let embedModel = "amazon.nova-2-multimodal-embeddings-v1:0"
    static let dims = 1024

    /// Card header reflecting the ACTIVE provider, never hardcoded copy.
    static func headerTitle(_ status: AIStatusInfo?) -> String {
        guard let status else { return "AI · checking…" }
        return "AI · " + ProviderDisplay.name(status.provider)
    }

    /// Display value for an active model slot: the raw id (the honest truth;
    /// friendly-name tables in Swift drift from the CLI), or "(not set)".
    static func modelValue(_ id: String?) -> String {
        guard let id, !id.isEmpty else { return "(not set)" }
        return id
    }

    /// True when the active config differs from the recommended defaults.
    /// Gates the Reset button so it only appears when there is real drift.
    static func differsFromDefaults(_ status: AIStatusInfo?) -> Bool {
        guard let status else { return false }
        return status.provider != provider
            || status.genModel != genModel
            || status.embeddingModel != embedModel
            || status.dimensions != dims
    }

    /// Confirm-dialog body for the Reset action: names exactly what will be
    /// written and what it replaces, so the reset can never be a surprise.
    static func resetConfirmText(_ status: AIStatusInfo?) -> String {
        var lines = [
            "This writes provider \(ProviderDisplay.name(provider)), generation model \(genModel), embedding model \(embedModel), and \(dims) dimensions to the vault config."
        ]
        if let status {
            lines.append("Current: \(ProviderDisplay.name(status.provider)) · \(modelValue(status.genModel)) · \(modelValue(status.embeddingModel)).")
        }
        lines.append("If the embedding model changes you may need Re-embed All afterwards.")
        return lines.joined(separator: "\n\n")
    }

    /// Plain-language readiness line for the AI card, provider-generic,
    /// preferring the ACTIVE provider's own `reason` (actionable) over a
    /// generic credentials hint.
    static func statusLine(_ status: AIStatusInfo?) -> String {
        guard let status else { return "Checking…" }
        let display = ProviderDisplay.name(status.provider)
        if status.embedAvailable && status.genAvailable { return "\(display) ready" }
        if let reason = status.providers?.first(where: { $0.name == status.provider })?.reason,
           !reason.isEmpty {
            return "Not ready: \(reason)"
        }
        return "Not ready: check \(display) credentials"
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

/// Pure presentation logic for the Obsidian plugin row on the Vault card,
/// extracted (like `HomeAI`) so the label/button mapping is unit-testable.
enum HomePlugin {
    /// Row label and optional action-button title for an installed plugin
    /// version (nil = not installed) vs this app's version. A plugin that is
    /// current or newer (a dev/BRAT build) gets no button: `2nb plugin
    /// install` pulls the latest release, which can't help either case.
    static func rowState(installed: String?, appVersion: String) -> (label: String, button: String?) {
        guard let installed else { return ("not installed", "Install") }
        if CLIVersion.isOlder(cli: installed, thanApp: appVersion) {
            return ("v\(installed) (update available)", "Update")
        }
        // Non-semver manifest versions (a dev/BRAT build) show raw, no "v".
        return (CLIVersion.parse(installed) != nil ? "v\(installed)" : installed, nil)
    }

    /// Post-install confirmation. A fresh install needs the two manual
    /// Obsidian steps (no API automates them); an update only needs a reload.
    static func successMessage(updated: Bool) -> String {
        if updated {
            return "Plugin updated. Reload Obsidian (Cmd+R) to pick it up."
        }
        return "Plugin installed. Reload Obsidian (Cmd+R), then enable \"2ndbrain AI\" under Settings > Community plugins."
    }
}

/// Pure message logic for the drift banner's Update CLI button, extracted so
/// the no-op case is unit-testable.
enum HomeCLIUpdate {
    /// Message after a zero-exit `brew upgrade`. Brew exits 0 even when it
    /// had nothing to do (the realistic case: the cask bumped the app before
    /// the tap shipped the matching formula), and that no-op must not read
    /// as "updated": the drift banner is still on screen contradicting it.
    static func resultMessage(before: String?, after: String?) -> String {
        if let after, after != before {
            return "CLI updated to \(after)."
        }
        let current = after ?? before ?? "unknown"
        return "CLI unchanged at \(current). Homebrew found no newer formula; the tap may not have shipped this release yet. Try again in a few minutes."
    }
}

/// Pure presentation logic for the Claude Code skill row, extracted (like
/// `HomePlugin`) so the label/button mapping is unit-testable. A nil status
/// (slug not found, or a pre-0.8.1 CLI without `skills list`) reads "unknown"
/// with no button rather than a misleading "not installed".
enum HomeSkill {
    static func rowState(_ status: SkillStatusInfo?) -> (label: String, button: String?) {
        guard let status else { return ("unknown", nil) }
        if status.userInstalled { return ("installed (user)", nil) }
        if status.projectInstalled { return ("installed (project)", nil) }
        return ("not installed", "Install")
    }

    static func successMessage() -> String {
        "Skill installed for Claude Code (user scope). It's available in your next Claude Code session."
    }
}

/// Pure presentation logic for the Claude Code MCP-server row. "Configured"
/// (wired into ~/.claude.json) is the durable signal: the server is launched
/// on demand by Claude Code, so "running" would read red whenever the client
/// is closed. A nil status (pre-0.8.1 CLI without `mcp configured`) reads
/// "unknown"; not-configured offers the setup snippet (the app never writes
/// ~/.claude.json itself).
enum HomeMCPConfigured {
    static func rowState(_ status: MCPConfiguredInfo?) -> (label: String, button: String?) {
        guard let status else { return ("unknown", nil) }
        if status.configured {
            if let scope = status.scope, !scope.isEmpty {
                return ("configured (\(scope) scope)", nil)
            }
            return ("configured", nil)
        }
        return ("not configured", "Show setup")
    }
}
