import SwiftUI
import SecondBrainCore
#if canImport(AppKit)
import AppKit
#endif

/// Disappearing first-run checklist logic, extracted for
/// unit testing. Renders only incomplete items and vanishes completely once
/// all items are satisfied (or the optional AI Clients item is dismissed).
enum SetupChecklist {
    enum Kind: String, Equatable {
        case vault, gemini, index, clients
    }

    struct Item: Identifiable, Equatable {
        var id: Kind { kind }
        let kind: Kind
        let title: String
        let subtitle: String
        let actionTitle: String
    }

    static func missingItems(
        hasVault: Bool,
        aiStatus: AIStatusInfo?,
        configuredClients: Int,
        clientsDismissed: Bool = false
    ) -> [Item] {
        var items: [Item] = []
        if !hasVault {
            items.append(Item(
                kind: .vault,
                title: "Bind an Obsidian vault",
                subtitle: "Open your vault so 2ndbrain can index and search your notes.",
                actionTitle: "Choose Vault…"
            ))
        }
        let geminiReady = (aiStatus?.embedAvailable == true) && (aiStatus?.genAvailable == true)
        if !geminiReady {
            items.append(Item(
                kind: .gemini,
                title: "Connect Google Gemini",
                subtitle: "Add your GEMINI_API_KEY or Vertex AI project in Settings.",
                actionTitle: "Configure Gemini…"
            ))
        }
        if hasVault, let status = aiStatus,
           (status.documentCount > 0 && status.embeddingCount == 0) || status.portabilityStatus == "unindexed" {
            items.append(Item(
                kind: .index,
                title: "Embed your vault notes",
                subtitle: "Run your first sync so semantic search and Ask Gemini have embeddings.",
                actionTitle: "Sync Now"
            ))
        } else if hasVault, let status = aiStatus, status.portabilityStatus == "dimension_break" {
            items.append(Item(
                kind: .index,
                title: "Re-embed vault for \(ProviderDisplay.name(status.effectiveEmbeddingProvider))",
                subtitle: "Stored vectors are \(status.vaultEmbeddingDim.map { "\($0)d" } ?? "incompatible"), while \(status.embeddingModel) produces \(status.dimensions)d.",
                actionTitle: "Re-embed All"
            ))
        }
        if !clientsDismissed && configuredClients == 0 {
            items.append(Item(
                kind: .clients,
                title: "Wire an AI coding client",
                subtitle: "Optional: connect Claude Code, Warp, Claude Desktop, or Codex via MCP.",
                actionTitle: "Set Up Clients…"
            ))
        }
        return items
    }
}

/// The workspace Home screen: soft material cards, a
/// disappearing Finish Setup checklist, and one-click Gemini capabilities.
struct HomeView: View {
    @Environment(AppState.self) private var appState
    @AppStorage("dismissedClientsSetupItem") private var clientsDismissed = false

    @State private var saving = false
    @State private var testing = false
    @State private var updatingCLI = false
    @State private var installingPlugin = false
    @State private var actionMessage: String?
    @State private var actionIsError = false
    @State private var homeTestResults: [ModelSlotTestResult] = []
    @State private var homeTestSummary: String?
    @State private var homeTestFailed = false
    @State private var obsidianOpenVault: ObsidianRegistry.Vault?
    @State private var pluginVersion: String?
    @State private var staleCount: Int = 0
    @State private var hotkeyManager = HotkeyManager.shared

    private var missingChecklistItems: [SetupChecklist.Item] {
        SetupChecklist.missingItems(
            hasVault: appState.vault != nil,
            aiStatus: appState.aiStatus,
            configuredClients: configuredClientCount,
            clientsDismissed: clientsDismissed
        )
    }

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 18) {
                headerBar

                if let warning = cliVersionWarning {
                    cliWarningBanner(warning)
                }
                if let stale = indexStaleWarning {
                    indexStaleBanner(stale)
                }
                if let problem = appState.lastIndexingProblem {
                    indexingProblemBanner(problem)
                }

                if !missingChecklistItems.isEmpty {
                    finishSetupCard
                }

                ViewThatFits(in: .horizontal) {
                    HStack(alignment: .top, spacing: 16) {
                        mainColumn
                            .frame(minWidth: 380, maxWidth: .infinity, alignment: .topLeading)
                        capabilitiesRail
                            .frame(width: 248, alignment: .topLeading)
                    }
                    .frame(minWidth: 644)

                    VStack(alignment: .leading, spacing: 16) {
                        mainColumn
                        capabilitiesRail
                    }
                }

                if let actionMessage {
                    ThemedCard(style: .subtle, padding: 12) {
                        HStack(spacing: 8) {
                            Image(systemName: actionIsError ? "exclamationmark.triangle.fill" : "checkmark.circle.fill")
                                .foregroundStyle(actionIsError ? .red : .green)
                            Text(actionMessage)
                                .font(.callout)
                                .textSelection(.enabled)
                        }
                    }
                }
            }
            .padding(24)
            .frame(maxWidth: 920, alignment: .leading)
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
        .task(id: appState.vault?.rootURL) {
            homeTestResults = []
            homeTestSummary = nil
            homeTestFailed = false
            obsidianOpenVault = ObsidianRegistry.load()?.openVault
            pluginVersion = appState.vault.flatMap { ObsidianPlugin.installedVersion(vaultRoot: $0.rootURL) }
            await appState.refreshCLIVersion()
            await appState.refreshAIStatus()
            await appState.refreshMetrics()
            await appState.refreshSkillStatus()
            await appState.refreshMCPConfigured()
            await appState.refreshGlobalInstructions()
            staleCount = await appState.fetchStaleCount()
            applyButtonCommand(appState.guiInputBridge.pendingButtonCommand)
        }
        .onChange(of: appState.guiInputBridge.pendingButtonCommand) { _, cmd in
            applyButtonCommand(cmd)
        }
    }

    // MARK: - Header

    private var headerBar: some View {
        HStack(alignment: .center, spacing: 12) {
            VStack(alignment: .leading, spacing: 4) {
                HStack(spacing: 8) {
                    Text(appState.vault?.rootURL.lastPathComponent ?? "2ndbrain Workspace")
                        .font(.title2.weight(.bold))
                    BetaBadge()
                }
                if let vault = appState.vault {
                    obsidianMatchBadge(for: vault.rootURL)
                } else {
                    Text("Gemini-First Obsidian Companion")
                        .font(.callout)
                        .foregroundStyle(.secondary)
                }
            }

            Spacer(minLength: 12)

            if appState.vault != nil {
                HStack(spacing: 8) {
                    Button {
                        appState.showQuickSearch = true
                    } label: {
                        HStack(spacing: 6) {
                            Label("Quick Search", systemImage: "sparkle.magnifyingglass")
                            Keycaps(caps: hotkeyManager.combo?.caps ?? ["⌃", "⇧", "Space"], size: 10)
                        }
                    }
                    .controlSize(.regular)

                    Button {
                        appState.openVaultInObsidian()
                    } label: {
                        Label("Open in Obsidian", systemImage: "arrow.up.forward.app")
                    }
                    .buttonStyle(.borderedProminent)
                    .controlSize(.regular)
                }
            }
        }
    }

    // MARK: - Finish Setup (Disappearing Checklist)

    private var finishSetupCard: some View {
        ThemedCard(style: .prominent, padding: 16) {
            VStack(alignment: .leading, spacing: 12) {
                HStack(spacing: 10) {
                    SheetSectionHeader(title: "Finish Setup", systemImage: "checklist")
                    Spacer()
                    Button("Setup Guide…") {
                        appState.showOnboarding = true
                    }
                    .controlSize(.small)
                }

                VStack(spacing: 8) {
                    ForEach(missingChecklistItems) { item in
                        HStack(spacing: 12) {
                            Image(systemName: checklistSymbol(for: item.kind))
                                .foregroundStyle(Color.accentColor)
                                .frame(width: 20)

                            VStack(alignment: .leading, spacing: 2) {
                                Text(item.title)
                                    .font(.callout.weight(.semibold))
                                Text(item.subtitle)
                                    .font(.caption)
                                    .foregroundStyle(.secondary)
                            }

                            Spacer(minLength: 8)

                            if item.kind == .clients {
                                Button("Dismiss") {
                                    clientsDismissed = true
                                }
                                .buttonStyle(.plain)
                                .font(.caption)
                                .foregroundStyle(.secondary)
                            }

                            Button(item.actionTitle) {
                                handleChecklistAction(item.kind)
                            }
                            .buttonStyle(.bordered)
                            .controlSize(.small)
                        }
                        .padding(.horizontal, 10)
                        .padding(.vertical, 8)
                        .background(Color.primary.opacity(0.04), in: RoundedRectangle(cornerRadius: 8, style: .continuous))
                    }
                }
            }
        }
    }

    private func checklistSymbol(for kind: SetupChecklist.Kind) -> String {
        switch kind {
        case .vault: return "folder.badge.plus"
        case .gemini: return "sparkles"
        case .index: return "square.stack.3d.up"
        case .clients: return "cpu"
        }
    }

    private func handleChecklistAction(_ kind: SetupChecklist.Kind) {
        switch kind {
        case .vault:
            openVaultPanel()
        case .gemini:
            appState.requestOpenSettings(tab: .ai)
        case .index:
            actionMessage = nil
            appState.rebuildIndex(forceReembed: appState.aiStatus?.portabilityStatus == "dimension_break")
        case .clients:
            appState.requestOpenSettings(tab: .integrations)
        }
    }

    private func openVaultPanel() {
        #if canImport(AppKit)
        let panel = NSOpenPanel()
        panel.canChooseFiles = false
        panel.canChooseDirectories = true
        panel.allowsMultipleSelection = false
        panel.message = "Select your Obsidian vault directory"
        panel.prompt = "Open Vault"
        if panel.runModal() == .OK, let url = panel.url {
            appState.openPickedVault(at: url)
        }
        #endif
    }

    // MARK: - Main Column Cards

    private var mainColumn: some View {
        VStack(alignment: .leading, spacing: 16) {
            ThemedCard {
                aiCard
            }
            ThemedCard {
                vaultAndIndexCard
            }
        }
    }

    private var capabilitiesRail: some View {
        VStack(alignment: .leading, spacing: 16) {
            ThemedCard {
                VStack(alignment: .leading, spacing: 10) {
                    SheetSectionHeader(title: "Capabilities", systemImage: "wand.and.stars")

                    capabilityRow(
                        title: "Ask Gemini",
                        subtitle: "RAG answers with citations",
                        systemImage: "sparkles"
                    ) {
                        appState.openAskGemini()
                    }

                    capabilityRow(
                        title: "Hybrid Search",
                        subtitle: "Instant BM25 + semantic",
                        systemImage: "magnifyingglass"
                    ) {
                        appState.openHybridSearch()
                    }

                    capabilityRow(
                        title: "Polish a Note",
                        subtitle: "Copy-edit & wikilinks",
                        systemImage: "pencil.and.outline"
                    ) {
                        appState.showNotePolish = true
                    }

                    capabilityRow(
                        title: "Links & Schema",
                        subtitle: "Fix broken links, formatting & schema",
                        systemImage: "checkmark.seal"
                    ) {
                        appState.showLintResults = true
                    }
                }
                .disabled(appState.vault == nil)
            }

            ThemedCard(style: .subtle) {
                aiClientsSummary
            }
        }
    }

    private func capabilityRow(
        title: String,
        subtitle: String,
        systemImage: String,
        action: @escaping () -> Void
    ) -> some View {
        Button(action: action) {
            HStack(spacing: 10) {
                Image(systemName: systemImage)
                    .font(.system(size: 14, weight: .semibold))
                    .foregroundStyle(Color.accentColor)
                    .frame(width: 28, height: 28)
                    .background(Color.accentColor.opacity(0.12), in: RoundedRectangle(cornerRadius: 7, style: .continuous))
                VStack(alignment: .leading, spacing: 1) {
                    Text(title)
                        .font(.callout.weight(.semibold))
                    Text(subtitle)
                        .font(.caption)
                        .foregroundStyle(.secondary)
                        .lineLimit(1)
                }
                Spacer()
                Image(systemName: "chevron.right")
                    .font(.caption.weight(.semibold))
                    .foregroundStyle(.tertiary)
            }
            .padding(8)
            .contentShape(Rectangle())
            .background(Color.primary.opacity(0.035), in: RoundedRectangle(cornerRadius: 8, style: .continuous))
        }
        .buttonStyle(.plain)
    }

    // MARK: - Warning Banners

    private var cliVersionWarning: String? {
        guard CLIVersion.isOlder(cli: appState.cliVersion, thanApp: appVersion) else { return nil }
        return "Your 2nb CLI (\(appState.cliVersion ?? "unknown")) is older than this app (\(appVersion)). Some actions may fail until you update it."
    }

    private var indexStaleWarning: (message: String, buttonTitle: String, forceReembed: Bool)? {
        HomeAI.portabilityBanner(appState.aiStatus)
    }

    @ViewBuilder
    private func indexStaleBanner(_ info: (message: String, buttonTitle: String, forceReembed: Bool)) -> some View {
        ThemedCard(style: .subtle, padding: 12) {
            HStack(spacing: 8) {
                Image(systemName: "exclamationmark.triangle.fill")
                    .foregroundStyle(.orange)
                Text(info.message)
                    .font(.callout)
                Spacer()
                Button(info.buttonTitle) {
                    actionMessage = nil
                    appState.rebuildIndex(forceReembed: info.forceReembed)
                }
                .controlSize(.small)
                .disabled(appState.isIndexing)
            }
        }
    }

    @ViewBuilder
    private func indexingProblemBanner(_ problem: IndexingProblem) -> some View {
        ThemedCard(style: .subtle, padding: 12) {
            HStack(spacing: 8) {
                Image(systemName: "exclamationmark.circle.fill")
                    .foregroundStyle(.orange)
                VStack(alignment: .leading, spacing: 2) {
                    Text("Indexing issue in \(problem.path)")
                        .font(.callout.weight(.medium))
                    Text(problem.message)
                        .font(.caption)
                        .foregroundStyle(.secondary)
                        .lineLimit(2)
                }
                Spacer()
                Button("Sync") {
                    appState.rebuildIndex()
                }
                .controlSize(.small)
                .disabled(appState.isIndexing)
            }
        }
    }

    @ViewBuilder
    private func cliWarningBanner(_ message: String) -> some View {
        ThemedCard(style: .subtle, padding: 12) {
            VStack(alignment: .leading, spacing: 6) {
                HStack(spacing: 6) {
                    Image(systemName: "exclamationmark.triangle.fill")
                        .foregroundStyle(.orange)
                    Text(message)
                        .font(.callout)
                }
                HStack(spacing: 10) {
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
        }
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

    // MARK: - AI Card

    private var aiCard: some View {
        let status = appState.aiStatus
        let ready = (status?.embedAvailable ?? false) && (status?.genAvailable ?? false)
        let hasDimensionBreak = status?.portabilityStatus == "dimension_break"
        return VStack(alignment: .leading, spacing: 10) {
            HStack {
                SheetSectionHeader(title: HomeAI.headerTitle(status), systemImage: "bolt.horizontal")
                Spacer()
                HStack(spacing: 6) {
                    Circle().fill((ready && !hasDimensionBreak) ? Color.green : Color.orange).frame(width: 8, height: 8)
                    Text((ready && hasDimensionBreak) ? "Re-embed required" : HomeAI.statusLine(status))
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
            }

            LabeledContent("Generation") { modelText(status?.genModel) }
            LabeledContent("Embeddings") { modelText(status?.embeddingModel) }

            if let u30 = appState.metricsReport?.aiUsage?.last30d {
                LabeledContent("30-day Cloud Cost") {
                    Text("\(Formatters.usd(u30.cloudCostUSD, knownPricing: u30.knownPricing)) (\(u30.cloudCalls) cloud · \(u30.localCalls) local)")
                        .font(.caption.monospacedDigit())
                        .foregroundStyle(.secondary)
                }
            }

            HStack(spacing: 10) {
                OpenSettingsTabButton(HomeAI.settingsLinkTitle(ready: ready), tab: .ai)
                    .controlSize(.small)

                if HomeAI.differsFromDefaults(status) {
                    Button(saving ? "Resetting…" : "Reset to defaults") {
                        Task { await resetToRecommendedDefaults() }
                    }
                    .controlSize(.small)
                }

                Button("Usage & Cost…") {
                    appState.showAIUsage = true
                }
                .controlSize(.small)
                .accessibilityIdentifier("home.openAIUsageButton")
                .disabled(appState.vault == nil)

                Spacer()

                Button(testing ? "Testing…" : "Test Connection") {
                    Task { await test() }
                }
                .controlSize(.small)
                .accessibilityIdentifier("home.testConnectionButton")
                .disabled(saving || testing || appState.vault == nil)
            }
            .padding(.top, 2)

            if testing {
                ColdStartHint()
            }

            if homeTestSummary != nil || !homeTestResults.isEmpty {
                Divider()
                VStack(alignment: .leading, spacing: 8) {
                    if let homeTestSummary {
                        Label(
                            homeTestSummary,
                            systemImage: homeTestFailed ? "xmark.circle.fill" : "checkmark.circle.fill"
                        )
                        .font(.callout.weight(.semibold))
                        .foregroundStyle(homeTestFailed ? .red : .green)
                    }

                    ForEach(homeTestResults) { slot in
                        homeSlotResultRow(slot)
                    }
                }
            }
        }
    }

    @ViewBuilder
    private func homeSlotResultRow(_ slot: ModelSlotTestResult) -> some View {
        VStack(alignment: .leading, spacing: 3) {
            HStack(spacing: 6) {
                Image(systemName: slot.ok ? "checkmark.circle.fill" : "xmark.circle.fill")
                    .foregroundStyle(slot.ok ? .green : .red)
                Text(slot.slot == .generation ? "Generation" : "Embeddings")
                    .font(.caption.weight(.semibold))
                Text(slot.modelID)
                    .font(.caption.monospaced())
                    .lineLimit(1)
                    .truncationMode(.middle)
                Text("· \(ProviderDisplay.name(slot.provider))")
                    .font(.caption2)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
                Spacer()
                Text(slot.ok ? (slot.latency.map { "PASS · \($0)" } ?? "PASS") : "FAIL")
                    .font(.caption2.weight(.semibold).monospacedDigit())
                    .padding(.horizontal, 6)
                    .padding(.vertical, 2)
                    .background((slot.ok ? Color.green : Color.red).opacity(0.14), in: Capsule())
                    .foregroundStyle(slot.ok ? .green : .red)
            }
            if let proof = slot.proofLabel {
                Text(proof)
                    .font(.caption2.monospaced())
                    .foregroundStyle(.secondary)
                    .textSelection(.enabled)
                    .padding(.leading, 20)
            } else if !slot.ok {
                Text(slot.responseProof)
                    .font(.caption2)
                    .foregroundStyle(.red)
                    .textSelection(.enabled)
                    .padding(.leading, 20)
            }
        }
        .padding(7)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(Color.primary.opacity(0.035), in: RoundedRectangle(cornerRadius: 7, style: .continuous))
    }

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
        homeTestResults = []
        homeTestSummary = nil
        homeTestFailed = false
        defer { saving = false }
        do {
            try await appState.saveAIConfig(
                provider: HomeAI.provider,
                embedModel: HomeAI.embedModel,
                genModel: HomeAI.genModel,
                dims: HomeAI.dims,
                bedrockProfile: "",
                bedrockRegion: "",
                openrouterKey: "",
                geminiProject: "",
                geminiLocation: "",
                geminiKey: ""
            )
            await appState.refreshAIStatus()
            actionIsError = false
            actionMessage = HomeAI.reembedHintAfterSave(appState.aiStatus)
                ?? "Reset to \(ProviderDisplay.name(HomeAI.provider)) · \(HomeAI.genModel) · \(HomeAI.embedModel)."
        } catch {
            actionIsError = true
            actionMessage = "Reset failed: \(error.localizedDescription)"
        }
    }

    private func applyButtonCommand(_ cmd: GUIButtonCommand?) {
        guard let cmd else { return }
        switch cmd.targetIdentifier {
        case "home.testConnectionButton":
            if saving || testing || appState.vault == nil {
                appState.guiInputBridge.acknowledgeButtonDisabled(cmd.id, label: cmd.targetIdentifier)
            } else {
                appState.guiInputBridge.acknowledgeButtonClicked(cmd.id, label: "Test Connection")
                Task { await test() }
            }
        case "home.openAIUsageButton":
            if appState.vault == nil {
                appState.guiInputBridge.acknowledgeButtonDisabled(cmd.id, label: cmd.targetIdentifier)
            } else {
                appState.guiInputBridge.acknowledgeButtonClicked(cmd.id, label: "Usage & Cost…")
                appState.showAIUsage = true
            }
        default:
            break
        }
    }

    private func test() async {
        guard let status = appState.aiStatus else {
            actionIsError = true
            actionMessage = "AI status not loaded yet; try again in a moment."
            homeTestResults = []
            homeTestFailed = true
            homeTestSummary = "AI status not loaded yet; try again in a moment."
            return
        }
        testing = true
        actionMessage = nil
        homeTestResults = []
        homeTestSummary = nil
        homeTestFailed = false
        defer { testing = false }
        do {
            var results: [ModelSlotTestResult] = []
            var failures: [String] = []
            var passes: [String] = []
            var tested = 0
            func failureLine(_ r: AIProbeResult, model: String, provider: String) -> String {
                let guidance = ModelAccessPresentation.guidance(
                    code: r.errorCode,
                    provider: provider,
                    remediation: provider == "llama-local" ? nil : r.remediation,
                    strategy: r.invokeStrategy
                )
                if let g = guidance { return "\(model) [\(g.badge)]: \(g.advice)" }
                return "\(model): \(r.detail ?? "failed")"
            }
            if !status.genModel.isEmpty {
                tested += 1
                let gp = status.effectiveGenerationProvider
                let r = try await appState.testModel(provider: gp, modelID: status.genModel, modelType: "generation")
                results.append(ModelSlotTestResult.fromProbe(r, slot: .generation))
                if r.ok {
                    let snippet = (r.detail?.isEmpty == false) ? " — \"\(r.detail!)\"" : ""
                    passes.append("\(status.genModel) (\(r.latency)\(snippet))")
                } else {
                    failures.append(failureLine(r, model: status.genModel, provider: gp))
                }
            }
            if !status.embeddingModel.isEmpty {
                tested += 1
                let ep = status.effectiveEmbeddingProvider
                let r = try await appState.testModel(provider: ep, modelID: status.embeddingModel, modelType: "embedding")
                results.append(ModelSlotTestResult.fromProbe(r, slot: .embedding))
                if r.ok {
                    let snippet = (r.detail?.isEmpty == false) ? " — \(r.detail!)" : ""
                    passes.append("\(status.embeddingModel) (\(r.latency)\(snippet))")
                } else {
                    failures.append(failureLine(r, model: status.embeddingModel, provider: ep))
                }
            }
            homeTestResults = results
            if tested == 0 {
                homeTestFailed = true
                homeTestSummary = "No active AI models configured"
                actionIsError = true
                actionMessage = "No active AI models configured; open Settings → AI & Models first."
            } else if failures.isEmpty {
                homeTestFailed = false
                homeTestSummary = tested == 2 ? "Both AI models responded and passed" : "AI model responded and passed"
                actionIsError = false
                actionMessage = "Test passed: \(passes.joined(separator: " · "))"
            } else {
                homeTestFailed = true
                homeTestSummary = failures.count == tested ? "Model test failed" : "One AI model failed"
                actionIsError = true
                actionMessage = "Test failed: \(failures.joined(separator: " / "))"
            }
            testing = false
            await appState.refreshAIStatus()
        } catch {
            homeTestResults = []
            homeTestFailed = true
            homeTestSummary = "Test failed: \(error.localizedDescription)"
            actionIsError = true
            actionMessage = "Test failed: \(error.localizedDescription)"
            testing = false
        }
    }

    // MARK: - Vault & Index Card

    private var vaultAndIndexCard: some View {
        let status = appState.aiStatus
        let docs = status?.documentCount ?? 0
        let embedded = status?.embeddingCount ?? 0
        let embeddable = status?.embeddableDenominator ?? docs
        let pending = max(0, embeddable - embedded)

        return VStack(alignment: .leading, spacing: 10) {
            HStack {
                SheetSectionHeader(title: "Vault & Index", systemImage: "square.stack.3d.up")
                Spacer()
                if appState.pauseAutoSync {
                    Button {
                        appState.setAutoSyncPaused(false)
                    } label: {
                        Label("Auto-sync paused · Resume", systemImage: "pause.circle.fill")
                            .font(.caption)
                            .foregroundStyle(.orange)
                    }
                    .buttonStyle(.plain)
                }
            }

            if let vault = appState.vault {
                LabeledContent("Path") {
                    Text(vault.rootURL.path)
                        .font(.caption.monospaced())
                        .foregroundStyle(.secondary)
                        .lineLimit(1)
                        .truncationMode(.middle)
                        .textSelection(.enabled)
                }
                pluginRow(for: vault.rootURL)
            }

            Divider()

            LabeledContent("Indexed notes", value: "\(docs)")
            LabeledContent("Embedded", value: "\(embedded) / \(embeddable)")
            if staleCount > 0 {
                LabeledContent("Stale (90d+)", value: "\(staleCount)")
            }

            if let fraction = appState.indexFraction {
                VStack(alignment: .leading, spacing: 4) {
                    ProgressView(value: fraction)
                        .controlSize(.small)
                    if let ep = appState.embeddingProgress, ep.total > 0 {
                        let etaSuffix = ep.remainingSeconds.map { " · ~\(Formatters.eta($0)) left" } ?? ""
                        Text("Embedding \(ep.current)/\(ep.total)\(etaSuffix)")
                            .font(.caption2.monospacedDigit())
                            .foregroundStyle(.secondary)
                    }
                }
            } else if pending > 0 {
                Text("\(pending) note\(pending == 1 ? "" : "s") awaiting embedding. Sync to catch up.")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }

            HStack(spacing: 8) {
                Button("Sync Index") {
                    actionMessage = nil
                    appState.rebuildIndex()
                }
                .controlSize(.small)
                .help("Index new and changed notes and embed only what changed.")
                .accessibilityIdentifier("home.syncButton")

                Button("Re-embed All…") {
                    actionMessage = nil
                    appState.rebuildIndex(forceReembed: true)
                }
                .controlSize(.small)
                .help("Regenerate every embedding from scratch (use after switching embedding models).")
                .accessibilityIdentifier("home.reembedButton")
            }
            .disabled(appState.vault == nil || appState.isIndexing)
            .padding(.top, 2)
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
                Text("Synced with open Obsidian vault")
            } else if let openVault {
                Text("Obsidian has “\(openVault.name)” open, not this folder")
            } else {
                Text("Obsidian's open vault is unknown")
            }
        }
        .font(.callout)
        .foregroundStyle(.secondary)
    }

    // MARK: - AI Clients Summary

    private var aiClientsSummary: some View {
        VStack(alignment: .leading, spacing: 8) {
            SheetSectionHeader(title: "AI Clients", systemImage: "cpu")
            HStack(spacing: 6) {
                Image(systemName: configuredClientCount > 0 ? "checkmark.circle.fill" : "circle")
                    .foregroundStyle(configuredClientCount > 0 ? Color.green : Color.secondary)
                Text("\(configuredClientCount) of \(ClientDescriptor.all.count) clients wired")
            }
            .font(.callout)
            OpenSettingsTabButton("Manage in Settings…", tab: .integrations)
                .controlSize(.small)
        }
    }

    private var configuredClientCount: Int {
        ClientDescriptor.all.filter {
            ClientConfig.mcpRow(appState.mcpConfigured(forClient: $0.mcpClientKey)).ok
        }.count
    }
}

/// Reusable section heading with SF Symbol icon, shared across Home and capability panels.
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

/// Pure presentation logic for the Home AI card, plus the shipped Gemini (GCP)
/// defaults (mirroring the CLI's `DefaultAIConfig`). Extracted from `HomeView`
/// so the model-name and status-line mapping are unit-testable.
enum HomeAI {
    static let provider = "gemini"
    static let genModel = "gemini-4-hydrogen"
    static let embedModel = "gemini-embedding-2"
    static let dims = 768

    static func settingsLinkTitle(ready: Bool) -> String {
        ready ? "AI settings…" : "Add your API key in Settings…"
    }

    static func headerTitle(_ status: AIStatusInfo?) -> String {
        guard let status else { return "AI · checking…" }
        if status.isMixedProviders {
            return "AI · \(ProviderDisplay.name(status.effectiveGenerationProvider)) + \(ProviderDisplay.name(status.effectiveEmbeddingProvider))"
        }
        return "AI · " + ProviderDisplay.name(status.effectiveGenerationProvider)
    }

    static func modelValue(_ id: String?) -> String {
        guard let id, !id.isEmpty else { return "(not set)" }
        return id
    }

    static func differsFromDefaults(_ status: AIStatusInfo?) -> Bool {
        guard let status else { return false }
        return status.effectiveGenerationProvider != provider
            || status.effectiveEmbeddingProvider != provider
            || status.genModel != genModel
            || status.embeddingModel != embedModel
            || status.dimensions != dims
    }

    static func resetConfirmText(_ status: AIStatusInfo?) -> String {
        var lines = [
            "This writes provider \(ProviderDisplay.name(provider)), generation model \(genModel), embedding model \(embedModel), and \(dims) dimensions to the vault config."
        ]
        if let status {
            let currentProvider = status.isMixedProviders
                ? "\(ProviderDisplay.name(status.effectiveGenerationProvider)) + \(ProviderDisplay.name(status.effectiveEmbeddingProvider))"
                : ProviderDisplay.name(status.provider)
            lines.append("Current: \(currentProvider) · \(modelValue(status.genModel)) · \(modelValue(status.embeddingModel)).")
        }
        lines.append("If the embedding model changes you may need Re-embed All afterwards.")
        return lines.joined(separator: "\n\n")
    }

    static func statusLine(_ status: AIStatusInfo?) -> String {
        guard let status else { return "Checking…" }
        if status.isMixedProviders {
            if status.embedAvailable && status.genAvailable { return "Hybrid AI ready" }
            let failingProvider = !status.genAvailable
                ? status.effectiveGenerationProvider
                : status.effectiveEmbeddingProvider
            let display = ProviderDisplay.name(failingProvider)
            if let reason = status.providers?.first(where: { $0.name == failingProvider })?.reason,
               !reason.isEmpty {
                return "Not ready: \(reason)"
            }
            return "Not ready: check \(display) credentials"
        }
        let display = ProviderDisplay.name(status.provider)
        if status.embedAvailable && status.genAvailable { return "\(display) ready" }
        if let reason = status.providers?.first(where: { $0.name == status.provider })?.reason,
           !reason.isEmpty {
            return "Not ready: \(reason)"
        }
        return "Not ready: check \(display) credentials"
    }

    static func reembedHintAfterSave(_ status: AIStatusInfo?) -> String? {
        switch status?.portabilityStatus {
        case "dimension_break", "mixed", "model_mismatch":
            return "Saved. This vault's embeddings were made with a different model, so semantic search may be less accurate until you regenerate them. Run Re-embed All when you have a moment."
        default:
            return nil
        }
    }

    static func portabilityBanner(_ status: AIStatusInfo?) -> (message: String, buttonTitle: String, forceReembed: Bool)? {
        switch status?.portabilityStatus {
        case "dimension_break":
            if let vaultDim = status?.vaultEmbeddingDim, let dims = status?.dimensions {
                return (
                    "Vault embeddings are \(vaultDim)d, while \(status?.embeddingModel ?? "Gemini") produces \(dims)d. Semantic search is paused until you re-embed.",
                    "Re-embed All",
                    true
                )
            }
            return (
                "Vault embeddings use a different vector dimension than the active model. Semantic search is paused until you re-embed.",
                "Re-embed All",
                true
            )
        case "mixed":
            return (
                "Vault contains embeddings from multiple models. Re-embed all notes to normalize on \(status?.embeddingModel ?? "the active model").",
                "Re-embed All",
                true
            )
        case "model_mismatch":
            let previous = status?.vaultEmbeddingModels?.first ?? "another model"
            return (
                "Vault notes were embedded with \(previous), while config uses \(status?.embeddingModel ?? "a new model"). Re-embed all notes for consistent ranking.",
                "Re-embed All",
                true
            )
        case "upgrade_reembed_recommended":
            return (
                "A newer 2nb improved chunking and embeddings for this vault. Re-embed to apply the improvements.",
                "Re-embed",
                true
            )
        case "upgrade_reindex_recommended":
            return (
                "A newer 2nb improved indexing for this vault. Reindex to apply the improvements.",
                "Reindex",
                false
            )
        default:
            return nil
        }
    }
}

/// Pure presentation logic for the Obsidian plugin row on the Vault card.
enum HomePlugin {
    static func rowState(installed: String?, appVersion: String) -> (label: String, button: String?) {
        guard let installed else { return ("not installed", "Install") }
        if CLIVersion.isOlder(cli: installed, thanApp: appVersion) {
            return ("v\(installed) (update available)", "Update")
        }
        return (CLIVersion.parse(installed) != nil ? "v\(installed)" : installed, nil)
    }

    static func successMessage(updated: Bool) -> String {
        if updated {
            return "Plugin updated. Reload Obsidian (Cmd+R) to pick it up."
        }
        return "Plugin installed. Reload Obsidian (Cmd+R), then enable \"2ndbrain AI\" under Settings > Community plugins."
    }
}

/// Pure message logic for the drift banner's Update CLI button.
enum HomeCLIUpdate {
    static func resultMessage(before: String?, after: String?) -> String {
        if let after, after != before {
            return "CLI updated to \(after)."
        }
        let current = after ?? before ?? "unknown"
        return "CLI unchanged at \(current). Homebrew found no newer formula; the tap may not have shipped this release yet. Try again in a few minutes."
    }
}

/// Pure presentation logic for the Claude Code skill row.
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

/// Pure presentation logic for the Claude Code MCP-server row.
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
