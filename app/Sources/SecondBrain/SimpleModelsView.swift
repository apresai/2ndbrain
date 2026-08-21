import SwiftUI

/// The simple Models tab: sticky vendors → Validate → Answers/Search pickers.
///
/// Catalog admin chrome (the old AI Hub) stays in `AIHubView` behind
/// "Full catalog" for one release, then goes away.
struct SimpleModelsView: View {
    @Environment(AppState.self) var appState

    @State private var aiStatus: AIStatusInfo?
    @State private var models: [CatalogModelInfo] = []
    @State private var policies: [VendorPolicyResult] = []
    @State private var selectedVendors: Set<String> = []
    @State private var loading = true
    @State private var savingPolicy = false
    @State private var verifying = false
    @State private var verifyProgress: AIHubView.VerifyProgress?
    @State private var lastVerifySummary: String?
    @State private var validateEstimate: CostPreviewResponse?
    @State private var errorMessage: String?
    @State private var vendorGuardMessage: String?
    @State private var reasoningEffort = ""
    @State private var showFullCatalog = false
    @State private var bedrockStatus: BedrockMachineStatus?
    /// True while "Uncheck all" has staged an empty board (nothing written;
    /// cleared by any successful policy write or reload resync).
    @State private var stagedEmpty = false
    /// Probeable-and-enabled model IDs present in the catalog but absent
    /// from the persisted "seen" snapshot. Recomputed on every reload;
    /// empty means no banner (including the seeded-silently first run).
    @State private var discoveryNewIDs: [String] = []

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            header
            Divider()
            ScrollView {
                VStack(alignment: .leading, spacing: 20) {
                    if appState.vault == nil {
                        Text("Bind an Obsidian vault to pick vendors and models. Until then, Settings uses the shipped Haiku + Nova-2 defaults.")
                            .foregroundStyle(.secondary)
                        // Credentials need no vault (`config bedrock` is
                        // machine-local), so the keyless first-run user can
                        // still act from here instead of hitting a dead end.
                        OpenSettingsTabButton("Open AI settings…", tab: .ai)
                            .controlSize(.small)
                    } else {
                        if staleVerdicts {
                            Label(StaleVerdicts.bannerText, systemImage: "clock.arrow.circlepath")
                                .font(.callout)
                                .foregroundStyle(.orange)
                        }
                        vendorSection
                        discoveryNudgeBanner
                        validateSection
                        pickersSection
                        thinkingSection
                        failedSection
                        DisclosureGroup("Full catalog", isExpanded: $showFullCatalog) {
                            // isInline drops the Hub's inner ScrollView so this
                            // page has one scroller. Do not restore minHeight.
                            AIHubView(onClose: {}, isInline: true)
                        }
                        .font(.subheadline.bold())
                    }
                }
                .padding()
            }
        }
        .task { await reload(discover: true) }
        .onChange(of: appState.modelsCatalogVersion) { _, _ in
            Task { await reload(discover: false) }
        }
        .onChange(of: appState.pendingValidateRequest, initial: true) { _, requested in
            // The post-key-save "re-validate now?" offer routes here. In the
            // PRIMARY flow the flag is set BEFORE this view mounts (the
            // Settings button routes the main window to the Models tab), so
            // `initial: true` is load-bearing: without it the attach-time
            // value is never observed and the request sticks, silently doing
            // nothing now and firing on some later unrelated toggle. The
            // Validate pass itself still cost-confirms before spending.
            guard requested else { return }
            appState.pendingValidateRequest = false
            Task { await runValidate() }
        }
        .alert("Couldn't load models", isPresented: Binding(
            get: { errorMessage != nil },
            set: { if !$0 { errorMessage = nil } }
        )) {
            Button("OK", role: .cancel) { errorMessage = nil }
        } message: {
            Text(errorMessage ?? "")
        }
    }

    private var header: some View {
        HStack {
            VStack(alignment: .leading, spacing: 2) {
                Text("Models").font(.title2.bold())
                Text("Choose vendors, validate what this account can call, then pick Answers and Search.")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
            Spacer()
            credentialsChip
            if loading {
                ProgressView().controlSize(.small)
            }
        }
        .padding()
    }

    /// Always-visible key-state chip. This tab is where "bad credentials"
    /// failures render, and it used to be the one surface with no route to
    /// fix them.
    private var credentialsChip: some View {
        let chip = CredentialChipPresentation.chip(bedrockStatus)
        return OpenSettingsTabButton(chip.label, tab: .ai)
            .buttonStyle(.bordered)
            .controlSize(.small)
            .tint(chip.warning ? .orange : nil)
    }

    private var staleVerdicts: Bool {
        StaleVerdicts.isStale(
            lastVerifiedAt: aiStatus?.modelAccess?.lastVerifiedAt,
            tokenUpdatedAt: bedrockStatus?.tokenUpdatedAt
        )
    }

    private var vendorSection: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack {
                Text("Vendors")
                    .font(.subheadline.bold())
                    .textCase(.uppercase)
                    .foregroundStyle(.secondary)
                Spacer()
                // Check all = remove the restriction (policy clear), so
                // vendors AWS adds later arrive enabled — an explicit
                // full-list write would freeze today's vocabulary and
                // silently block vendor #20. No-op when no policy exists
                // (everything is already unrestricted).
                Button("Check all") { Task { await checkAllVendors() } }
                    .controlSize(.small)
                    .disabled(savingPolicy || !hasBedrockPolicy)
                // Uncheck all stages an empty board WITHOUT writing: the CLI
                // refuses an empty enable-only list, and mapping this to
                // policy clear would ENABLE everything.
                Button("Uncheck all") { uncheckAllVendors() }
                    .controlSize(.small)
                    .disabled(savingPolicy || selectedVendors.isEmpty)
            }
            Text(stagedEmpty ? VendorSelection.stagedEmptyMessage : VendorSelection.restrictionCaption(hasPolicy: hasBedrockPolicy))
                .font(.caption)
                .foregroundStyle(stagedEmpty ? .orange : .secondary)
            WrapHStack(items: vendorChoices) { vendor in
                Toggle(vendor.display, isOn: vendorBinding(vendor.slug))
                    .toggleStyle(.checkbox)
                    .disabled(savingPolicy)
            }
            if let vendorGuardMessage {
                Text(vendorGuardMessage)
                    .font(.caption)
                    .foregroundStyle(.orange)
            }
        }
    }

    /// "N new models discovered": shown only when a reload finds
    /// probeable-and-enabled models absent from the persisted seen
    /// snapshot (see `DiscoveryNudge`). Silent on first launch (the
    /// snapshot seeds instead of badging the shipped catalog as new) and
    /// silent again once every current model has been shown at least once.
    @ViewBuilder
    private var discoveryNudgeBanner: some View {
        if !discoveryNewIDs.isEmpty {
            HStack(spacing: 8) {
                Label(
                    discoveryNewIDs.count == 1 ? "1 new model discovered" : "\(discoveryNewIDs.count) new models discovered",
                    systemImage: "sparkles"
                )
                .font(.callout)
                Spacer()
                Button("Validate new models") { Task { await validateNewModels() } }
                    .controlSize(.small)
                    .disabled(verifying)
                Button("Dismiss") { dismissDiscoveryNudge() }
                    .buttonStyle(.borderless)
                    .controlSize(.small)
            }
            .padding(8)
            .background(Color.accentColor.opacity(0.08), in: RoundedRectangle(cornerRadius: 6))
        }
    }

    private var validateSection: some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack {
                Button(verifying ? "Validating…" : "Validate") {
                    Task { await runValidate() }
                }
                .disabled(verifying || appState.vault == nil)
                if let estimate = validateEstimate {
                    Text(String(format: "Validate · est. $%.4f", estimate.totalUSD) + regionSuffix)
                        .font(.caption)
                        .foregroundStyle(.secondary)
                } else {
                    Text("List models for the checked vendors, try them, keep the ones that work." + regionSuffix)
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
            }
            if let progress = verifyProgress {
                Text("Validating \(progress.current)/\(progress.total) \(progress.lastLine)")
                    .font(.caption.monospaced())
                    .foregroundStyle(.secondary)
            } else if let lastVerifySummary {
                Text(lastVerifySummary)
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
        }
    }

    private var pickersSection: some View {
        VStack(alignment: .leading, spacing: 12) {
            modelPicker(
                title: "Answers",
                type: "generation",
                activeID: aiStatus?.genModel
            )
            modelPicker(
                title: "Search",
                type: "embedding",
                activeID: aiStatus?.embeddingModel
            )
            if shouldNudgeValidate {
                Text("Validate to see which models work for your account.")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
        }
    }

    @ViewBuilder
    private func modelPicker(title: String, type: String, activeID: String?) -> some View {
        let options = WorkingModelPresentation.pickerModels(
            models,
            type: type,
            activeID: activeID,
            hasWorkingFlag: hasWorkingFlag
        )
        let cheapest = WorkingModelPresentation.cheapestID(in: options)
        let fastest = WorkingModelPresentation.fastestID(in: options)
        VStack(alignment: .leading, spacing: 4) {
            Text(title)
                .font(.subheadline.bold())
                .textCase(.uppercase)
                .foregroundStyle(.secondary)
            Picker(title, selection: Binding(
                get: { activeID ?? "" },
                set: { newID in
                    guard !newID.isEmpty, let model = options.first(where: { $0.modelID == newID }) else { return }
                    Task { await setActive(model) }
                }
            )) {
                if options.isEmpty {
                    Text("No models yet").tag("")
                }
                ForEach(options) { model in
                    let why = WorkingModelPresentation.why(
                        model,
                        isShippedDefault: isShippedDefault(model),
                        cheapestID: cheapest,
                        fastestID: fastest
                    )
                    Text(WorkingModelPresentation.rowLine(model, why: why.isEmpty ? nil : why))
                        .tag(model.modelID)
                }
            }
            .labelsHidden()
            .disabled(appState.isIndexing)
        }
    }

    @ViewBuilder
    private var thinkingSection: some View {
        if let gen = models.first(where: { $0.modelID == aiStatus?.genModel }),
           WorkingModelPresentation.showsThinking(gen) {
            VStack(alignment: .leading, spacing: 4) {
                Text("Thinking")
                    .font(.subheadline.bold())
                    .textCase(.uppercase)
                    .foregroundStyle(.secondary)
                // Bind via set so a reload assignment does not persist.
                // A failed `config get` must not write "".
                Picker("Thinking", selection: Binding(
                    get: { reasoningEffort },
                    set: { new in
                        guard new != reasoningEffort else { return }
                        reasoningEffort = new
                        Task { await saveReasoning(new) }
                    }
                )) {
                    Text("Default").tag("")
                    Text("Off").tag("none")
                    Text("Low").tag("low")
                    Text("Medium").tag("medium")
                    Text("High").tag("high")
                }
                .pickerStyle(.segmented)
                .labelsHidden()
                Text("Only this model accepts a thinking depth. Other Answers models ignore it.")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
        }
    }

    @ViewBuilder
    private var failedSection: some View {
        let provider = aiStatus?.provider ?? "bedrock"
        let failed = WorkingModelPresentation.failedModels(models, type: "generation", provider: provider)
            + WorkingModelPresentation.failedModels(models, type: "embedding", provider: provider)
        if !failed.isEmpty {
            DisclosureGroup(WorkingModelPresentation.failedDisclosureTitle(count: failed.count)) {
                ForEach(failed) { model in
                    Text(WorkingModelPresentation.rowLine(
                        model,
                        why: WorkingModelPresentation.why(
                            model,
                            isShippedDefault: false,
                            cheapestID: nil,
                            fastestID: nil
                        )
                    ))
                    .font(.caption.monospaced())
                    .foregroundStyle(.secondary)
                }
                if failed.contains(where: { $0.testErrorCode == "bad_credentials" }) {
                    HStack(spacing: 4) {
                        Text("Credentials problem —")
                            .font(.caption)
                            .foregroundStyle(.secondary)
                        OpenSettingsTabButton("Open AI settings", tab: .ai)
                            .buttonStyle(.link)
                            .font(.caption)
                    }
                }
            }
            .font(.subheadline)
        }
    }

    private var hasWorkingFlag: Bool {
        WorkingModelPresentation.hasWorkingFlag(models)
    }

    private var shouldNudgeValidate: Bool {
        WorkingModelPresentation.shouldNudgeValidate(models, activeIDs: activeModelIDs)
    }

    private var activeModelIDs: Set<String> {
        Set([aiStatus?.genModel, aiStatus?.embeddingModel].compactMap { $0 }.filter { !$0.isEmpty })
    }

    private var hasBedrockPolicy: Bool {
        CatalogSummary.activePolicy(policies, provider: "bedrock") != nil
    }

    /// " across N regions" when additional verify regions are configured
    /// (primary + the bedrock.json list); "" single-region.
    private var regionSuffix: String {
        RegionIncludeSelection.validateSuffix(regionCount: 1 + (bedrockStatus?.regions.count ?? 0))
    }

    /// CLI `known_vendors` (including a synthetic no-policy row). The
    /// four-slug list is only a last-resort decode fallback for older CLIs.
    private var vendorSlugs: [String] {
        let fromCLI = policies.first(where: { $0.provider == "bedrock" })?.knownVendors ?? []
        return fromCLI.isEmpty ? VendorSelection.fallbackSlugs : fromCLI
    }

    private var vendorChoices: [VendorChoice] {
        VendorSelection.choices(slugs: vendorSlugs, catalog: models)
    }

    private func vendorBinding(_ slug: String) -> Binding<Bool> {
        Binding(
            get: { selectedVendors.contains(slug) },
            set: { on in
                switch VendorSelection.toggling(
                    selectedVendors,
                    slug: slug,
                    on: on,
                    together: VendorSelection.slugsSharingDisplay(with: slug, among: vendorSlugs)
                ) {
                case .applied(let next):
                    vendorGuardMessage = nil
                    stagedEmpty = false
                    selectedVendors = next
                    Task { await savePolicy() }
                case .refusedEmpty:
                    // From a staged-empty board the first re-check flows
                    // through .applied above; this refusal only fires when
                    // unchecking the LAST box one-by-one, which is usually
                    // an accident — the Uncheck-all button is the explicit
                    // route to empty.
                    vendorGuardMessage = VendorSelection.keepAtLeastOneMessage
                }
            }
        )
    }

    private func isShippedDefault(_ model: CatalogModelInfo) -> Bool {
        model.recommended == true || model.modelID == HomeAI.genModel || model.modelID == HomeAI.embedModel
    }

    /// `discover: true` on first load and after Validate. `false` after a
    /// policy write / catalog-version bump so a checkbox does not re-walk
    /// Bedrock.
    private func reload(discover: Bool) async {
        // Machine-local, vault-free, milliseconds: drives the header chip and
        // the stale-verdicts banner, so it loads even with no vault bound.
        bedrockStatus = try? await appState.refreshBedrockMachineConfig()
        guard appState.vault != nil else {
            loading = false
            return
        }
        loading = true
        defer { loading = false }
        do {
            async let modelsTask = appState.fetchModelsCatalog(discover: discover)
            async let policiesTask = appState.fetchVendorPolicy()
            models = try await modelsTask
            policies = (try? await policiesTask) ?? []
            applyVendorSelectionFromPolicies()
            if discover {
                async let statusTask = appState.fetchAIStatus()
                async let effortTask = appState.getConfigValue("ai.reasoning_effort")
                aiStatus = try await statusTask
                // Failed read must not become "" — that used to persist
                // via onChange and clear the user's setting.
                if let effort = try? await effortTask {
                    reasoningEffort = effort
                }
                let candidateIDs = probeableIDs(models.filter { $0.provider == (aiStatus?.provider ?? "bedrock") && ($0.enabled ?? true) })
                if !candidateIDs.isEmpty {
                    validateEstimate = try? await appState.costPreview(modelIDs: candidateIDs, probe: "test")
                }
            }
            // After aiStatus is current (when this was a discover:true
            // reload) so the seen-snapshot is never seeded/read under a
            // guessed provider. Vendor toggles also change which models are
            // enabled, so this still runs on every reload, not only
            // discover:true ones.
            updateDiscoveryNudge()
        } catch {
            errorMessage = error.localizedDescription
        }
    }

    private func applyVendorSelectionFromPolicies() {
        selectedVendors = VendorSelection.displayVendors(
            policy: CatalogSummary.activePolicy(policies, provider: "bedrock"),
            fallback: vendorSlugs
        )
        // Any resync to persisted truth ends a staged-empty board.
        stagedEmpty = false
    }

    /// Recomputes `discoveryNewIDs` against the persisted seen snapshot.
    /// Requires a known active provider (`aiStatus` populated) so a
    /// snapshot is never seeded, or read, under a guessed provider name
    /// (bedrock is only a display fallback elsewhere in this view, never
    /// safe to write into a per-provider persisted key). On the very first
    /// run for that provider (no key for it has ever been recorded) this
    /// seeds the snapshot with the current catalog silently instead of
    /// badging the whole shipped model list as new; see
    /// `DiscoveryNudge.shouldSuppressFirstRun`.
    private func updateDiscoveryNudge() {
        guard let provider = aiStatus?.provider else { return }
        let seen = appState.discoverySeenModelKeys
        guard !DiscoveryNudge.shouldSuppressFirstRun(seen: seen, provider: provider) else {
            let currentIDs = DiscoveryNudge.probeableAndEnabled(models, provider: provider).map(\.modelID)
            appState.recordDiscoverySeen(DiscoveryNudge.modelKeys(provider: provider, modelIDs: currentIDs))
            discoveryNewIDs = []
            return
        }
        discoveryNewIDs = DiscoveryNudge.newIDs(models: models, provider: provider, seen: seen ?? [])
    }

    /// "Validate new models": probes exactly the banner's IDs via the same
    /// pinned-ID path the main Validate button uses.
    private func validateNewModels() async {
        let ids = discoveryNewIDs
        guard !ids.isEmpty else { return }
        await runValidate(only: ids)
    }

    /// "Dismiss": marks the currently new IDs as seen without probing them.
    private func dismissDiscoveryNudge() {
        guard let provider = aiStatus?.provider, !discoveryNewIDs.isEmpty else { return }
        appState.recordDiscoverySeen(DiscoveryNudge.modelKeys(provider: provider, modelIDs: discoveryNewIDs))
        discoveryNewIDs = []
    }

    /// Check all = clear the vendor policy (restriction removed; future
    /// vendors arrive enabled). The button is disabled when no policy exists.
    private func checkAllVendors() async {
        savingPolicy = true
        defer { savingPolicy = false }
        do {
            try await appState.clearGUIVendorPolicies(provider: "bedrock")
            await reload(discover: false)
        } catch {
            errorMessage = error.localizedDescription
        }
    }

    /// Uncheck all = staged-empty: clear the checkboxes locally, write
    /// nothing, and say so. The first re-check writes a policy with exactly
    /// that vendor via the normal toggle path.
    private func uncheckAllVendors() {
        selectedVendors = []
        stagedEmpty = true
        vendorGuardMessage = nil
    }

    private func savePolicy() async {
        guard !selectedVendors.isEmpty else { return }
        savingPolicy = true
        defer { savingPolicy = false }
        do {
            _ = try await appState.setVendorPolicy(
                provider: "bedrock",
                vendors: selectedVendors.sorted(),
                scope: AppState.guiVendorPolicyScope
            )
            await reload(discover: false)
        } catch {
            errorMessage = error.localizedDescription
        }
    }

    /// `only` pins the candidate set to exactly those IDs (the discovery
    /// banner's "Validate new models" action) instead of re-discovering and
    /// recomputing the full enabled-vendor candidate set (the main Validate
    /// button, `only: nil`). Both paths share the same cost-preview /
    /// confirm / verify-stream body below.
    private func runValidate(only: [String]? = nil) async {
        guard !verifying else { return }
        verifying = true
        defer { verifying = false }
        let provider = aiStatus?.provider ?? "bedrock"
        let candidateIDs: [String]
        if let only {
            guard !only.isEmpty else { return }
            candidateIDs = only
        } else {
            // Discover once here (not on every checkbox) so a newly checked
            // vendor's Bedrock listings join the candidate set.
            do {
                models = try await appState.fetchModelsCatalog(discover: true)
            } catch {
                errorMessage = error.localizedDescription
                return
            }
            candidateIDs = probeableIDs(models.filter { $0.provider == provider && ($0.enabled ?? true) })
            guard !candidateIDs.isEmpty else {
                errorMessage = "No probeable models to validate. Check at least one vendor."
                return
            }
        }
        // Preview the same IDs verify will use (post-discover), not a
        // stale checkbox-reload estimate.
        let preview = try? await appState.costPreview(modelIDs: candidateIDs, probe: "test")
        validateEstimate = preview
        guard confirmPaidOperation(preview: preview, operation: "Validate") else { return }
        let cap = VerifyFlow.costCap(preview: preview)
        lastVerifySummary = nil
        verifyProgress = AIHubView.VerifyProgress(current: 0, total: candidateIDs.count)
        defer { verifyProgress = nil }
        do {
            // Same IDs as the cost-preview confirm. Empty IDs would add
            // --discover and let the CLI rebuild a larger pool than shown.
            try await appState.verifyModels(provider: provider, costCap: cap, modelIDs: candidateIDs) { event in
                applyVerifyEvent(event)
            }
            // These IDs have now been shown and probed; the discovery
            // banner must stop nudging about them.
            appState.recordDiscoverySeen(DiscoveryNudge.modelKeys(provider: provider, modelIDs: candidateIDs))
            await reload(discover: true)
        } catch {
            errorMessage = error.localizedDescription
        }
    }

    private func applyVerifyEvent(_ event: VerifyEvent) {
        switch event.event {
        case "start":
            verifyProgress = AIHubView.VerifyProgress(current: 0, total: event.total ?? 0)
        case "result":
            var p = verifyProgress ?? AIHubView.VerifyProgress()
            p.current = event.n ?? p.current
            if let total = event.total { p.total = total }
            if let r = event.result {
                p.lastLine = "\(r.ok ? "PASS" : "FAIL") \(r.modelID)"
            }
            verifyProgress = p
        case "done":
            lastVerifySummary = VerifyFlow.summaryText(event.summary)
        default:
            break
        }
    }

    private func probeableIDs(_ ms: [CatalogModelInfo]) -> [String] {
        ms.filter { $0.modelType != "rerank" && ($0.compatible ?? true) }.map(\.modelID)
    }

    private func setActive(_ model: CatalogModelInfo) async {
        do {
            try await appState.setActiveModel(type: model.modelType, modelID: model.modelID, provider: model.provider)
            await reload(discover: true)
        } catch {
            errorMessage = error.localizedDescription
        }
    }

    private func saveReasoning(_ value: String) async {
        do {
            try await appState.setConfigValue("ai.reasoning_effort", value)
        } catch {
            errorMessage = error.localizedDescription
        }
    }
}

/// Sticky-vendor checkbox rules for the simple Models tab. Split out of the
/// view so the last-uncheck refuse, vocabulary fallback, and display-name
/// dedupe are unit-testable without SwiftUI.
enum VendorSelection {
    static let keepAtLeastOneMessage = "Keep at least one vendor."

    /// Shown after "Uncheck all": nothing was written (the CLI refuses an
    /// empty enable-only list, and clearing the policy would ENABLE
    /// everything — the opposite of the visual state), so the empty board is
    /// pure staging until a vendor is re-checked.
    static let stagedEmptyMessage = "Nothing saved yet — check at least one vendor. The last saved vendor list still applies."

    /// Last-resort slugs when `models policy show` has no `known_vendors`
    /// (CLI predating the synthetic vocabulary row).
    static let fallbackSlugs = ["anthropic", "amazon", "xai", "openai"]

    /// Display names for Bedrock vendor slugs, matching `bedrockVendorDisplay`
    /// in `cli/internal/ai/vendor.go`. Used when the catalog has no row yet.
    static let bedrockLabels: [String: String] = [
        "anthropic": "Anthropic",
        "amazon": "Amazon",
        "meta": "Meta",
        "mistral": "Mistral",
        "cohere": "Cohere",
        "ai21": "AI21",
        "deepseek": "DeepSeek",
        "moonshot": "Moonshot",
        "moonshotai": "Moonshot",
        "qwen": "Qwen",
        "zai": "Z.ai",
        "writer": "Writer",
        "minimax": "MiniMax",
        "nvidia": "NVIDIA",
        "openai": "OpenAI",
        "xai": "xAI",
        "twelvelabs": "TwelveLabs",
        "google": "Google",
        "stability": "Stability AI",
    ]

    enum Outcome: Equatable {
        case applied(Set<String>)
        case refusedEmpty
    }

    /// Apply a checkbox toggle. Refuses emptying the set so the UI cannot
    /// show none-checked while a previous enable-only policy still stands.
    /// `together` is the display-name alias group (`moonshot` + `moonshotai`)
    /// so one checkbox writes the slugs the CLI actually matches.
    static func toggling(_ current: Set<String>, slug: String, on: Bool, together: [String] = []) -> Outcome {
        var next = current
        let group = together.isEmpty ? [slug] : together
        if on {
            for s in group { next.insert(s) }
        } else {
            for s in group { next.remove(s) }
        }
        if next.isEmpty { return .refusedEmpty }
        return .applied(next)
    }

    static func slugsSharingDisplay(with slug: String, among slugs: [String]) -> [String] {
        let name = label(for: slug)
        return slugs.filter { label(for: $0) == name }
    }

    /// Checkboxes when a policy exists; otherwise the vocabulary (CLI
    /// known_vendors, or the four-slug fallback) as a display-only selection
    /// (no policy is written until the user toggles).
    static func displayVendors(policy: VendorPolicyResult?, fallback: [String]) -> Set<String> {
        if let policy { return Set(policy.vendors) }
        return Set(fallback)
    }

    /// Do not claim "unchecked stay hidden" before a policy exists — with no
    /// policy the CLI restricts nothing.
    static func restrictionCaption(hasPolicy: Bool) -> String {
        if hasPolicy {
            return "New models from a checked vendor show up here. Unchecked vendors stay hidden."
        }
        return "No restriction yet — every vendor is available. Checking or unchecking saves a vendor list that applies across vaults."
    }

    static func label(for slug: String, catalog: [CatalogModelInfo] = []) -> String {
        if let display = catalog.first(where: { $0.vendor == slug })?.vendorDisplay,
           !display.isEmpty {
            return display
        }
        return bedrockLabels[slug] ?? slug.capitalized
    }

    /// One checkbox per display name. When two slugs share a label
    /// (`moonshot` / `moonshotai` → "Moonshot"), keep the shorter CLI slug
    /// (`moonshot`), which is what `VendorOf` emits for `moonshot.*` IDs.
    static func choices(slugs: [String], catalog: [CatalogModelInfo] = []) -> [VendorChoice] {
        var winner: [String: String] = [:]
        for slug in slugs {
            let name = label(for: slug, catalog: catalog)
            if let existing = winner[name] {
                if prefer(slug, over: existing) {
                    winner[name] = slug
                }
            } else {
                winner[name] = slug
            }
        }
        var seen = Set<String>()
        var out: [VendorChoice] = []
        for slug in slugs {
            let name = label(for: slug, catalog: catalog)
            guard winner[name] == slug, seen.insert(slug).inserted else { continue }
            out.append(VendorChoice(slug: slug, display: name))
        }
        return out
    }

    static func prefer(_ a: String, over b: String) -> Bool {
        if a.count != b.count { return a.count < b.count }
        return a < b
    }
}

struct VendorChoice: Identifiable, Equatable {
    var id: String { slug }
    let slug: String
    let display: String
}

/// "New models discovered" banner logic for the simple Models tab. Split out
/// so the seen-key computation, the probeable-and-enabled predicate, and
/// first-run suppression are unit-testable without SwiftUI or UserDefaults.
///
/// The banner exists because a catalog change (a newly enumerable vendor, a
/// newly listed model) can surface models the user has never seen without
/// any action on their part; it should say so once, then get out of the way.
enum DiscoveryNudge {
    /// `UserDefaults` key for the persisted "seen" snapshot
    /// (`AppState.discoverySeenModelKeys`). Declared next to the logic that
    /// decides what "seen" means; AppState only loads/stores it.
    static let key = "discoverySeenModelKeys"

    static func modelKey(provider: String, modelID: String) -> String {
        provider + "|" + modelID
    }

    static func modelKeys(provider: String, modelIDs: [String]) -> Set<String> {
        Set(modelIDs.map { modelKey(provider: provider, modelID: $0) })
    }

    /// The exact predicate the Validate flow uses for its candidate set:
    /// provider match, not vendor-policy-disabled, and probeable (not
    /// rerank, not statically incompatible). Matching it here means a
    /// policy-disabled vendor's discoveries never nudge, and "Validate new
    /// models" probes precisely what the banner counted.
    static func probeableAndEnabled(_ models: [CatalogModelInfo], provider: String) -> [CatalogModelInfo] {
        models.filter {
            $0.provider == provider
                && ($0.enabled ?? true)
                && $0.modelType != "rerank"
                && ($0.compatible ?? true)
        }
    }

    /// IDs present in the current catalog but absent from the seen snapshot.
    static func newIDs(models: [CatalogModelInfo], provider: String, seen: Set<String>) -> [String] {
        probeableAndEnabled(models, provider: provider)
            .map(\.modelID)
            .filter { !seen.contains(modelKey(provider: provider, modelID: $0)) }
    }

    /// True when no key for `provider` has ever been recorded to the seen
    /// snapshot: a fresh install (no snapshot at all), or a provider
    /// switched to for the first time on a machine whose snapshot already
    /// holds another provider's keys. Scoped per provider, not merely
    /// "does a snapshot exist at all": a global check would suppress
    /// forever after the FIRST provider ever seeds, then badge the entire
    /// catalog of every later-activated provider as new, which is the
    /// exact flood this suppression exists to prevent. The caller should
    /// seed silently in this case rather than badge the provider's full
    /// catalog as "new" on its first appearance.
    static func shouldSuppressFirstRun(seen: Set<String>?, provider: String) -> Bool {
        guard let seen else { return true }
        let prefix = provider + "|"
        return !seen.contains { $0.hasPrefix(prefix) }
    }
}

/// Wrapping vendor checkboxes. Adaptive grid so ~19 slugs wrap instead of
/// sitting on one HStack line. Compiles on macOS 14.
private struct WrapHStack<Item: Identifiable, Content: View>: View {
    let items: [Item]
    @ViewBuilder let content: (Item) -> Content

    private let columns = [GridItem(.adaptive(minimum: 132), spacing: 12, alignment: .leading)]

    var body: some View {
        LazyVGrid(columns: columns, alignment: .leading, spacing: 8) {
            ForEach(items) { item in
                content(item)
            }
        }
    }
}
