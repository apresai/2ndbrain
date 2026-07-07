import SwiftUI
import os

private let hubLog = Logger(subsystem: "dev.apresai.2ndbrain", category: "aihub")

/// AIHubView is the single surface for everything AI in the app:
/// providers (enable / disable / status), active models (embedding +
/// generation, with inline swap), and the model catalog (test, set
/// active, enable / disable, discover). Subsumes what used to be
/// three separate wizards.
struct AIHubView: View {
    @Environment(AppState.self) var appState
    let onClose: () -> Void
    var isInline: Bool = false

    @State private var aiStatus: AIStatusInfo?
    @State private var models: [CatalogModelInfo] = []
    @State private var loading = true
    @State private var isDiscovering = false
    @State private var togglingRerank = false
    @State private var errorMessage: String?
    @State private var filter = CatalogFilter()
    @State private var searchText: String = ""
    @State private var pickerContext: ModelCatalogPickerContext?
    /// Group keys currently collapsed. Default is expanded so users
    /// immediately see their models; collapsing is an opt-in to
    /// reduce clutter on very large catalogs.
    @State private var collapsedGroups: Set<String> = []
    /// Curated-by-default: the catalog opens on the short trustworthy list
    /// (recommended / verified / tested-by-you on the active provider) and
    /// the long tail lives behind this explicit toggle.
    @State private var showAllModels = false

    struct CatalogFilter: Equatable {
        var testedOnly: Bool = false
        var enabledOnly: Bool = false
    }

    /// Catalog group key: (type, vendor, provider). Stored as a
    /// single string so it's both Hashable for the collapsed set
    /// and easy to print.
    private func groupKey(type: String, vendor: String, provider: String) -> String {
        "\(type)|\(vendor)|\(provider)"
    }

    var body: some View {
        ZStack {
            VStack(spacing: 0) {
                header
                Divider()
                ScrollView {
                    VStack(alignment: .leading, spacing: 20) {
                        providersSection
                        if localEngineFeatureEnabled {
                            localModelsSection
                        }
                        activeSection
                        catalogSection
                        DisclosureGroup("Advanced settings") {
                            AIAdvancedSettingsView(
                                aiStatus: aiStatus,
                                models: models,
                                onReload: { await reload() }
                            )
                            .padding(.top, 8)
                        }
                        .font(.subheadline.bold())
                    }
                    .padding()
                }
                Divider()
                footer
            }
            .frame(width: isInline ? nil : 820, height: isInline ? nil : 640)

            if let pickerContext {
                Color.black.opacity(0.18)
                    .ignoresSafeArea()
                    .onTapGesture { self.pickerContext = nil }
                ModelCatalogPickerView(
                    models: models,
                    aiStatus: aiStatus,
                    initialType: pickerContext.typeScope,
                    initialModelID: pickerContext.modelID,
                    onClose: { self.pickerContext = nil },
                    onReload: { await reload() }
                )
                .padding()
            }
        }
        .task { await reload() }
        .onChange(of: appState.modelsCatalogVersion) { _, _ in
            Task { await reload() }
        }
        .alert(
            "AI Hub error",
            isPresented: Binding(
                get: { errorMessage != nil },
                set: { if !$0 { errorMessage = nil } }
            ),
            actions: { Button("OK") { errorMessage = nil } },
            message: { Text(errorMessage ?? "") }
        )
    }

    // MARK: - Header / footer

    private var header: some View {
        HStack {
            VStack(alignment: .leading, spacing: 2) {
                Text("AI").font(.title2.bold())
                Text("Providers, active models, and the full catalog in one place.")
                    .font(.callout)
                    .foregroundStyle(.secondary)
            }
            Spacer()
            if loading { ProgressView().controlSize(.small) }
        }
        .padding()
    }

    private var footer: some View {
        HStack {
            Text(footerSummary)
                .font(.caption)
                .foregroundStyle(.secondary)
            Spacer()
            if !isInline {
                Button("Close") { onClose() }
                    .keyboardShortcut(.cancelAction)
            }
        }
        .padding()
    }

    private var footerSummary: String {
        let total = models.count
        let tested = models.filter { $0.testedAt != nil && !($0.testedAt ?? "").isEmpty }.count
        let active = models.filter { $0.provider == aiStatus?.provider && ($0.modelID == aiStatus?.embeddingModel || $0.modelID == aiStatus?.genModel) }.count
        return "\(total) models · \(tested) tested · \(active) active"
    }

    // MARK: - Providers

    private var providersSection: some View {
        VStack(alignment: .leading, spacing: 8) {
            sectionTitle("Providers")
            HStack(spacing: 12) {
                ForEach((aiStatus?.providers ?? fallbackProviders()).filter { localEngineFeatureEnabled || $0.name != "llama-local" }) { p in
                    providerCard(p)
                }
            }
        }
    }

    // MARK: - Local models (download)

    /// The llama-local (bundled Gemma) provider is NOT user-ready: the
    /// `llama-server` engine binary is not provisioned (neither bundled in the
    /// app nor downloaded), so nothing can run locally yet. Until it ships, hide
    /// every llama-local GUI surface behind this one flag — the download /
    /// activate / delete plumbing and the CLI `ai engine` commands stay, so
    /// flipping this to `true` (once the engine is bundled) re-enables the whole
    /// section and the provider card with no rewrite.
    private let localEngineFeatureEnabled = false

    /// The one-click local stack: EmbeddingGemma (embed) + Gemma 4 E2B (gen) +
    /// BGE reranker (~4 GB). The larger Gemma 4 E4B stays a `2nb ai engine pull`
    /// power option.
    private var localStackIDs: [String] { ["embeddinggemma-300m", "gemma4-e2b", "bge-reranker-v2-m3"] }
    private var localEmbedID: String { "embeddinggemma-300m" }
    private var localGenID: String { "gemma4-e2b" }
    private var localDims: Int { 768 }

    private var localModelsSection: some View {
        VStack(alignment: .leading, spacing: 8) {
            sectionTitle("Local models")
            if let dl = appState.localModelDownload, !dl.finished {
                localModelsProgress(dl)
            } else {
                localModelsControls
            }
        }
    }

    @ViewBuilder
    private var localModelsStatusLine: some View {
        if aiStatus?.provider == "llama-local" {
            Label("Running Gemma locally (fully offline). Pick a Bedrock model in Active to switch back.", systemImage: "checkmark.seal.fill")
                .font(.callout)
                .foregroundStyle(.green)
        } else if let dl = appState.localModelDownload, dl.finished {
            if dl.failed {
                Label(dl.error ?? "Download failed", systemImage: "exclamationmark.triangle.fill")
                    .font(.callout)
                    .foregroundStyle(.orange)
            } else {
                Label("Downloaded \(dl.completed.count) local model\(dl.completed.count == 1 ? "" : "s"). Choose \"Use these models\" to run them.", systemImage: "checkmark.circle.fill")
                    .font(.callout)
                    .foregroundStyle(.green)
            }
        } else {
            Text("Run Gemma generation, embeddings, and reranking fully offline via the bundled llama.cpp engine. Downloads about 4 GB.")
                .font(.caption)
                .foregroundStyle(.secondary)
        }
    }

    private var localModelsControls: some View {
        VStack(alignment: .leading, spacing: 6) {
            localModelsStatusLine
            HStack {
                Button((appState.localModelDownload?.failed ?? false) ? "Retry download" : "Download local models") {
                    confirmAndDownload()
                }
                .controlSize(.small)
                .buttonStyle(.bordered)
                if aiStatus?.provider != "llama-local" {
                    Button("Use these models") { confirmAndActivate() }
                        .controlSize(.small)
                        .buttonStyle(.borderedProminent)
                        .disabled(appState.isIndexing)
                }
                Spacer()
                Button("Delete local models") { confirmAndDelete() }
                    .controlSize(.small)
                    .buttonStyle(.bordered)
                    .disabled(appState.isIndexing)
            }
        }
    }

    private func localModelsProgress(_ dl: LocalModelDownload) -> some View {
        VStack(alignment: .leading, spacing: 4) {
            HStack {
                ProgressView().controlSize(.small)
                Text("Downloading \(dl.currentModel.isEmpty ? "…" : dl.currentModel)")
                    .font(.callout)
                Spacer()
                Text("\(dl.completed.count)/\(dl.models.count)")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
            ProgressView(value: dl.fraction)
            if dl.total > 0 {
                Text("\(byteString(dl.done)) / \(byteString(dl.total))")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
        }
    }

    private func byteString(_ n: Int64) -> String {
        ByteCountFormatter.string(fromByteCount: n, countStyle: .file)
    }

    private func confirmAndDownload() {
        let alert = NSAlert()
        alert.messageText = "Download local models?"
        alert.informativeText = "Downloads about 4 GB (EmbeddingGemma, Gemma 4 E2B, and the BGE reranker) into ~/Library/Caches/2nb so you can run AI fully offline. This can take several minutes."
        alert.addButton(withTitle: "Download")
        alert.addButton(withTitle: "Cancel")
        if alert.runModal() == .alertFirstButtonReturn {
            Task { await appState.downloadLocalModels(localStackIDs) }
        }
    }

    private func confirmAndActivate() {
        let alert = NSAlert()
        alert.messageText = "Use the local Gemma models?"
        alert.informativeText = "Switches BOTH embeddings and generation to the local models (one provider drives both) and re-embeds your whole vault with 768-dim EmbeddingGemma. That runs on CPU and is measurably lower retrieval quality than Nova, so it is best for fully-offline or private use. Download the models first if you haven't. You can switch back any time by picking a Bedrock model in Active."
        alert.addButton(withTitle: "Use Local + Re-embed")
        alert.addButton(withTitle: "Cancel")
        if alert.runModal() == .alertFirstButtonReturn {
            Task {
                do {
                    try await appState.activateLocalStack(embedID: localEmbedID, genID: localGenID, dimensions: localDims)
                } catch {
                    recordActionFailure("Switch to local models failed", error: error)
                }
            }
        }
    }

    private func confirmAndDelete() {
        let active = aiStatus?.provider == "llama-local"
        let alert = NSAlert()
        alert.messageText = "Delete the local models?"
        alert.informativeText = active
            ? "You are currently using local models. Deleting them frees about 4 GB but disables local AI until you switch back to Bedrock or re-download."
            : "Frees about 4 GB from ~/Library/Caches/2nb/models. You can re-download any time."
        alert.addButton(withTitle: "Delete")
        alert.addButton(withTitle: "Cancel")
        if alert.runModal() == .alertFirstButtonReturn {
            Task {
                do {
                    _ = try await appState.deleteLocalModels(localStackIDs)
                } catch {
                    recordActionFailure("Delete local models failed", error: error)
                }
            }
        }
    }

    private func fallbackProviders() -> [ProviderStatusInfo] {
        // Until the first `ai status` call lands, render placeholders
        // so the UI doesn't jank when the hub first opens.
        [
            ProviderStatusInfo(name: "bedrock", configPresent: false, disabled: false, reachable: false, reason: nil, detail: nil),
            ProviderStatusInfo(name: "openrouter", configPresent: false, disabled: false, reachable: false, reason: nil, detail: nil),
            ProviderStatusInfo(name: "ollama", configPresent: false, disabled: false, reachable: false, reason: nil, detail: nil),
            ProviderStatusInfo(name: "llama-local", configPresent: false, disabled: false, reachable: false, reason: nil, detail: nil),
        ]
    }

    private func providerCard(_ p: ProviderStatusInfo) -> some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack {
                Text(ProviderDisplay.name(p.name)).font(.headline)
                Spacer()
                Circle()
                    .fill(providerStatusColor(p))
                    .frame(width: 10, height: 10)
            }
            Text(providerStatusLabel(p))
                .font(.caption)
                .foregroundStyle(.secondary)
            if let detail = p.detail, !detail.isEmpty {
                Text(detail)
                    .font(.caption2.monospaced())
                    .foregroundStyle(.tertiary)
                    .lineLimit(1)
            }
            Spacer(minLength: 0)
            Button(p.disabled ? "Enable" : "Disable") {
                Task { await toggleProvider(p) }
            }
            .controlSize(.small)
            .buttonStyle(.bordered)
        }
        .frame(maxWidth: .infinity, minHeight: 108, alignment: .topLeading)
        .padding(10)
        .background(Color(nsColor: .controlBackgroundColor))
        .clipShape(RoundedRectangle(cornerRadius: 8))
        .opacity(p.disabled ? 0.55 : 1.0)
    }


    private func providerStatusLabel(_ p: ProviderStatusInfo) -> String {
        if p.disabled { return "disabled" }
        if !p.configPresent { return p.reason ?? "not configured" }
        if !p.reachable { return p.reason ?? "unreachable" }
        return "ready"
    }

    private func providerStatusColor(_ p: ProviderStatusInfo) -> Color {
        if p.disabled { return .gray }
        if !p.configPresent { return .orange }
        if !p.reachable { return .orange }
        return .green
    }

    // MARK: - Active models

    private var activeSection: some View {
        VStack(alignment: .leading, spacing: 8) {
            sectionTitle("Active")
            if let status = aiStatus {
                if providerIsDisabled(status.provider) {
                    providerDisabledBanner(status.provider)
                }
                activeRow(type: "embedding", modelID: status.embeddingModel, provider: status.provider)
                activeRow(type: "generation", modelID: status.genModel, provider: status.provider)
                rerankActiveRow(status)
            } else {
                Text("Loading…").foregroundStyle(.secondary)
            }
        }
    }

    private func providerDisabledBanner(_ provider: String) -> some View {
        HStack(spacing: 8) {
            Image(systemName: "exclamationmark.triangle.fill")
                .foregroundStyle(.orange)
            Text("Active provider \(ProviderDisplay.name(provider)) is disabled — re-enable it or pick another active model.")
                .font(.callout)
        }
        .padding(8)
        .background(Color.orange.opacity(0.12))
        .clipShape(RoundedRectangle(cornerRadius: 6))
    }

    private func providerIsDisabled(_ provider: String) -> Bool {
        aiStatus?.providers?.first(where: { $0.name == provider })?.disabled ?? false
    }

    private func activeRow(type: String, modelID: String, provider: String) -> some View {
        let entry = models.first(where: { $0.provider == provider && $0.modelID == modelID })
        return HStack(alignment: .top, spacing: 12) {
            Text(type.capitalized + ":")
                .font(.callout)
                .frame(width: 96, alignment: .leading)
            VStack(alignment: .leading, spacing: 2) {
                Text(modelID.isEmpty ? "(not set)" : modelID)
                    .font(.body.monospaced())
                Text(activeMeta(for: entry))
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
            Spacer()
            Button {
                pickerContext = ModelCatalogPickerContext(typeScope: type, modelID: entry?.modelID)
            } label: {
                Label("Change", systemImage: "chevron.down")
            }
            .controlSize(.small)
            .buttonStyle(.bordered)
        }
        .padding(.vertical, 4)
    }

    /// The rerank slot: a model + an on/off toggle. Rerank ships OFF and is
    /// measured to not help at this scale, so the caption says so and the toggle
    /// makes turning it off first-class.
    private func rerankActiveRow(_ status: AIStatusInfo) -> some View {
        let enabled = status.rerankEnabled ?? false
        let modelID = status.rerankModel ?? ""
        let entry = models.first(where: { $0.modelType == "rerank" && $0.modelID == modelID })
        return HStack(alignment: .top, spacing: 12) {
            Text("Rerank:")
                .font(.callout)
                .frame(width: 96, alignment: .leading)
            VStack(alignment: .leading, spacing: 2) {
                Text(enabled ? (modelID.isEmpty ? "(not set)" : modelID) : "Off")
                    .font(.body.monospaced())
                Text(rerankMeta(enabled: enabled, available: status.rerankAvailable, entry: entry))
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
            Spacer()
            Toggle("", isOn: Binding(
                get: { enabled },
                set: { newValue in Task { await toggleRerank(newValue) } }
            ))
            .labelsHidden()
            .toggleStyle(.switch)
            .controlSize(.small)
            .disabled(togglingRerank)
            Button {
                pickerContext = ModelCatalogPickerContext(typeScope: "rerank", modelID: entry?.modelID)
            } label: {
                Label("Change", systemImage: "chevron.down")
            }
            .controlSize(.small)
            .buttonStyle(.bordered)
        }
        .padding(.vertical, 4)
    }

    private func rerankMeta(enabled: Bool, available: Bool?, entry: CatalogModelInfo?) -> String {
        if !enabled {
            return "optional cross-encoder stage · off by default (measured to not help at this scale)"
        }
        if available == false {
            return "enabled but unavailable on the active provider"
        }
        return activeMeta(for: entry)
    }

    private func activeMeta(for entry: CatalogModelInfo?) -> String {
        guard let m = entry else { return "untested" }
        var parts: [String] = []
        if let t = m.testedAt, !t.isEmpty { parts.append("tested " + t) }
        if let latency = m.testLatencyMs, latency > 0 { parts.append("\(latency)ms test") }
        if let bench = m.benchmark?.avgLatencyMs, bench > 0 { parts.append("\(bench)ms bench") }
        if let price = priceLabel(m) { parts.append(price) }
        return parts.isEmpty ? "no test data" : parts.joined(separator: " · ")
    }

    private func priceLabel(_ m: CatalogModelInfo) -> String? {
        if let req = m.priceRequest, req > 0 {
            return String(format: "$%.4f/request", req)
        }
        if let pIn = m.priceIn, pIn > 0 {
            if let pOut = m.priceOut, pOut > 0 {
                return String(format: "$%.2f/$%.2f per M", pIn, pOut)
            }
            return String(format: "$%.3f/M", pIn)
        }
        if m.local == true || m.priceSource != nil {
            return "free"
        }
        return nil
    }

    // MARK: - Catalog

    private var catalogSection: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack {
                sectionTitle("Catalog")
                Spacer()
                if isDiscovering {
                    HStack(spacing: 4) {
                        ProgressView().controlSize(.small)
                        Text("discovering…").font(.caption).foregroundStyle(.secondary)
                    }
                } else if showAllModels {
                    // Discover belongs to the long-tail view: dumping fresh
                    // unvalidated vendor listings into the curated view would
                    // defeat its point.
                    Button("Discover more") {
                        Task { await discover() }
                    }
                    .controlSize(.small)
                    .buttonStyle(.bordered)
                }
                filterMenu
            }

            // Search input — fuzzy-matches model ID + vendor display name.
            // Hoisted above both type sections so it applies uniformly.
            HStack {
                Image(systemName: "magnifyingglass")
                    .foregroundStyle(.secondary)
                TextField("Search models or vendors", text: $searchText)
                    .textFieldStyle(.plain)
                if !searchText.isEmpty {
                    Button(action: { searchText = "" }) {
                        Image(systemName: "xmark.circle.fill").foregroundStyle(.secondary)
                    }
                    .buttonStyle(.plain)
                }
            }
            .padding(6)
            .background(Color(nsColor: .controlBackgroundColor))
            .clipShape(RoundedRectangle(cornerRadius: 6))

            // Embeddings first — higher-stakes pick, smaller set.
            catalogTypeSection(type: "embedding", title: "Embedding Models")
            catalogTypeSection(type: "generation", title: "Generation Models")
            catalogTypeSection(type: "rerank", title: "Reranking Models")

            curationToggle

            if filteredModels().isEmpty {
                Text("No models match the current filter.")
                    .foregroundStyle(.secondary)
                    .padding()
                    .frame(maxWidth: .infinity)
            }
        }
    }

    /// The curated/all switch, with the hidden-count so the short default
    /// view never reads as "this is everything".
    @ViewBuilder
    private var curationToggle: some View {
        let restCount = curationSplit().rest.count
        if !showAllModels && restCount > 0 {
            Button("Show all models (\(restCount) more)") { showAllModels = true }
                .controlSize(.small)
                .buttonStyle(.borderless)
        } else if showAllModels {
            Button("Show recommended only") { showAllModels = false }
                .controlSize(.small)
                .buttonStyle(.borderless)
        }
    }

    private var filterMenu: some View {
        Menu {
            Toggle("Tested only", isOn: $filter.testedOnly)
            Toggle("Enabled only", isOn: $filter.enabledOnly)
        } label: {
            Label("Filter", systemImage: "line.3.horizontal.decrease.circle")
        }
        .menuStyle(.borderlessButton)
        .controlSize(.small)
    }

    /// searchFiltered applies the search text + filter toggles only (no
    /// curation): the raw pool both view modes draw from.
    private func searchFiltered() -> [CatalogModelInfo] {
        let needle = searchText.trimmingCharacters(in: .whitespaces).lowercased()
        return models.filter { m in
            if filter.testedOnly && (m.testedAt ?? "").isEmpty { return false }
            if filter.enabledOnly && (m.enabled == false) { return false }
            if !needle.isEmpty {
                let haystack = [
                    m.modelID,
                    m.name,
                    m.vendorDisplay ?? "",
                    m.family ?? "",
                    m.provider,
                ].joined(separator: " ").lowercased()
                if !haystack.contains(needle) { return false }
            }
            return true
        }
    }

    /// Curated/rest split of the searched pool (shared by the toggle count
    /// and the default view).
    private func curationSplit() -> (curated: [CatalogModelInfo], rest: [CatalogModelInfo]) {
        ModelCuration.partition(
            searchFiltered(),
            activeProvider: aiStatus?.provider,
            activeIDs: ModelCuration.activeIDs(aiStatus)
        )
    }

    /// The models the current view mode shows: everything in all-mode, the
    /// curated short list otherwise.
    private func filteredModels() -> [CatalogModelInfo] {
        showAllModels ? searchFiltered() : curationSplit().curated
    }

    @ViewBuilder
    private func catalogTypeSection(type: String, title: String) -> some View {
        let ofType = filteredModels().filter { $0.modelType == type }
        if !ofType.isEmpty {
            // In all-mode, untested unverified discoveries render demoted in
            // their own collapsed group so they can't be mistaken for the
            // trustworthy rows above them.
            let main = showAllModels ? ofType.filter { !ModelCuration.isDemoted($0) } : ofType
            let demoted = showAllModels ? ofType.filter { ModelCuration.isDemoted($0) } : []
            VStack(alignment: .leading, spacing: 4) {
                HStack {
                    Text(title).font(.subheadline.bold())
                    Text("(\(ofType.count))").font(.caption).foregroundStyle(.secondary)
                    Spacer()
                }
                .padding(.top, 6)
                ForEach(groupedByVendor(main), id: \.key) { group in
                    vendorGroupView(group: group)
                }
                if !demoted.isEmpty {
                    DisclosureGroup("Untested, discovered from the provider (\(demoted.count)): run a test before trusting one") {
                        ForEach(groupedByVendor(demoted), id: \.key) { group in
                            vendorGroupView(group: group)
                        }
                    }
                    .font(.caption)
                    .foregroundStyle(.secondary)
                }
            }
        }
    }

    /// Vendor group: all models sharing (provider, vendor), newest first.
    /// Intentionally NOT Hashable — its model slice carries
    /// CatalogModelInfo which isn't Equatable, and we only need the
    /// string key for `ForEach(..., id: \.key)`.
    struct VendorGroup {
        let key: String              // "<type>|<vendor>|<provider>"
        let type: String
        let vendor: String           // machine slug used by --vendor flag
        let vendorDisplay: String
        let provider: String
        let models: [CatalogModelInfo]
    }

    /// Partitions a pre-filtered model slice by (vendor, provider)
    /// within the implicit type. Each group's models are sorted
    /// newest-first by versionSortKey.
    private func groupedByVendor(_ ms: [CatalogModelInfo]) -> [VendorGroup] {
        guard let firstType = ms.first?.modelType else { return [] }
        var byKey: [String: [CatalogModelInfo]] = [:]
        var displayByKey: [String: (String, String, String)] = [:] // vendor, display, provider
        for m in ms {
            let vendor = m.vendor ?? "other"
            let display = m.vendorDisplay ?? "Other"
            let k = groupKey(type: m.modelType, vendor: vendor, provider: m.provider)
            byKey[k, default: []].append(m)
            displayByKey[k] = (vendor, display, m.provider)
        }
        let groups = byKey.map { (k, list) -> VendorGroup in
            let (vendor, display, provider) = displayByKey[k] ?? ("other", "Other", "")
            let sorted = list.sorted { ($0.versionSortKey ?? $0.modelID) > ($1.versionSortKey ?? $1.modelID) }
            return VendorGroup(
                key: k,
                type: firstType,
                vendor: vendor,
                vendorDisplay: display,
                provider: provider,
                models: sorted
            )
        }
        // Sort groups by display name so the visual order is stable.
        return groups.sorted { $0.vendorDisplay < $1.vendorDisplay }
    }

    @ViewBuilder
    private func vendorGroupView(group: VendorGroup) -> some View {
        let collapsed = collapsedGroups.contains(group.key)
        let allDisabled = group.models.allSatisfy { $0.enabled == false }
        VStack(alignment: .leading, spacing: 2) {
            HStack(spacing: 8) {
                Button {
                    if collapsed { collapsedGroups.remove(group.key) }
                    else { collapsedGroups.insert(group.key) }
                } label: {
                    Image(systemName: collapsed ? "chevron.right" : "chevron.down")
                        .frame(width: 12)
                }
                .buttonStyle(.plain)

                Text(group.vendorDisplay).font(.subheadline.bold())
                Text("·").foregroundStyle(.secondary)
                Text(ProviderDisplay.name(group.provider))
                    .font(.caption.monospaced())
                    .foregroundStyle(.secondary)
                Text("(\(group.models.count))")
                    .font(.caption)
                    .foregroundStyle(.secondary)

                Spacer()

                // Equal-weight bulk toggles per the user's design choice.
                Button(allDisabled ? "Enable all" : "Disable all") {
                    Task { await bulkToggle(group: group, enable: allDisabled) }
                }
                .controlSize(.small)
                .buttonStyle(.bordered)

                Button(allDisabled ? "Disable all" : "Enable all") {
                    Task { await bulkToggle(group: group, enable: !allDisabled) }
                }
                .controlSize(.small)
                .buttonStyle(.bordered)
            }
            .padding(.vertical, 4)

            if !collapsed {
                VStack(alignment: .leading, spacing: 2) {
                    ForEach(group.models) { m in
                        modelRow(m)
                            .padding(.leading, 20)
                    }
                }
            }
        }
        .padding(.horizontal, 4)
    }

    private func bulkToggle(group: VendorGroup, enable: Bool) async {
        do {
            // Pass the IDs explicitly so discovered-only models (not yet
            // in the user catalog) are covered — the CLI's catalog
            // lookup would otherwise return no matches for those.
            let ids = group.models.map(\.modelID)
            try await appState.setVendorEnabled(
                vendor: group.vendor,
                provider: group.provider,
                scope: "vault",
                enabled: enable,
                modelIDs: ids
            )
            await reload()
        } catch {
            recordActionFailure("Bulk toggle \(group.vendorDisplay) failed", error: error)
        }
    }

    private func modelRow(_ m: CatalogModelInfo) -> some View {
        let activeKinds = activeKinds(for: m)
        let disabledProvider = providerIsDisabled(m.provider)
        return HStack(alignment: .center, spacing: 10) {
            VStack(alignment: .leading, spacing: 2) {
                HStack(spacing: 6) {
                    Text(m.modelID)
                        .font(.body.monospaced())
                        .lineLimit(1)
                        .truncationMode(.middle)
                    ForEach(activeKinds, id: \.self) { kind in
                        Text("ACTIVE \(kind)")
                            .font(.caption2.bold())
                            .padding(.horizontal, 4)
                            .padding(.vertical, 1)
                            .background(Color.accentColor.opacity(0.15))
                            .clipShape(RoundedRectangle(cornerRadius: 3))
                            .foregroundStyle(.blue)
                    }
                    if m.tier == "verified" {
                        Text("verified").font(.caption2.monospaced()).foregroundStyle(.green)
                    } else if m.tier == "user_verified" {
                        Text("tested").font(.caption2.monospaced()).foregroundStyle(.blue)
                    }
                    if m.enabled == false {
                        Text("disabled").font(.caption2.monospaced()).foregroundStyle(.secondary)
                    }
                }
                Text(metaLine(m))
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
            }
            Spacer()
            rowActions(m, disabled: disabledProvider)
        }
        .padding(.vertical, 4)
        .contentShape(Rectangle())
        .onTapGesture {
            pickerContext = ModelCatalogPickerContext(typeScope: m.modelType, modelID: m.modelID)
        }
        .opacity(disabledProvider ? 0.55 : 1.0)
    }

    private func activeKinds(for m: CatalogModelInfo) -> [String] {
        guard aiStatus?.provider == m.provider else { return [] }
        var kinds: [String] = []
        if m.modelID == aiStatus?.embeddingModel { kinds.append("embedding") }
        if m.modelID == aiStatus?.genModel { kinds.append("generation") }
        return kinds
    }

    private func metaLine(_ m: CatalogModelInfo) -> String {
        var parts: [String] = [m.provider, m.modelType]
        if let family = m.family, !family.isEmpty { parts.append(family) }
        if let d = m.dimensions, d > 0 { parts.append("\(d)d") }
        if let c = m.contextLen, c > 0 { parts.append("\(c / 1000)k ctx") }
        if let p = priceLabel(m) { parts.append(p) }
        if m.compatible == false { parts.append("incompatible") }
        else if m.compatible == true { parts.append("compatible") }
        if let err = m.testError, !err.isEmpty { parts.append("failed") }
        else if let testedAt = m.testedAt, !testedAt.isEmpty { parts.append("tested") }
        if let strat = m.invokeStrategy, !strat.isEmpty { parts.append(strat) }
        return parts.joined(separator: " · ")
    }

    private func rowActions(_ m: CatalogModelInfo, disabled: Bool) -> some View {
        HStack(spacing: 6) {
            Button("Details") {
                pickerContext = ModelCatalogPickerContext(typeScope: m.modelType, modelID: m.modelID)
            }
                .controlSize(.small)
                .buttonStyle(.borderless)
                .disabled(disabled)
            Button(m.enabled == false ? "Enable" : "Disable") {
                Task { await toggleEnabled(m) }
            }
            .controlSize(.small)
            .buttonStyle(.borderless)
        }
    }

    private func sectionTitle(_ text: String) -> some View {
        Text(text)
            .font(.subheadline.bold())
            .textCase(.uppercase)
            .foregroundStyle(.secondary)
    }

    // MARK: - Actions

    private func reload() async {
        loading = true
        defer { loading = false }
        do {
            async let statusTask = appState.fetchAIStatus()
            async let modelsTask = appState.fetchModelsForWizard()
            aiStatus = try await statusTask
            models = try await modelsTask
            hubLog.info("AI Hub reload: \(self.models.count) models · \(self.aiStatus?.providers?.count ?? 0) providers")
        } catch {
            recordActionFailure("Failed to load AI state", error: error)
        }
    }

    private func discover() async {
        isDiscovering = true
        defer { isDiscovering = false }
        // fetchModelsForWizard already passes --discover; re-run it.
        do {
            models = try await appState.fetchModelsForWizard()
        } catch {
            recordActionFailure("Discover failed", error: error)
        }
    }

    private func toggleProvider(_ p: ProviderStatusInfo) async {
        do {
            try await appState.setProviderDisabled(p.name, disabled: !p.disabled)
            // FSEvents on config.yaml will refresh; also reload proactively.
            await reload()
        } catch {
            recordActionFailure("Toggle \(p.name) failed", error: error)
        }
    }

    private func toggleRerank(_ enabled: Bool) async {
        togglingRerank = true
        defer { togglingRerank = false }
        do {
            try await appState.setRerankEnabled(enabled)
            await reload()
        } catch {
            recordActionFailure("Toggle rerank failed", error: error)
        }
    }

    private func toggleEnabled(_ m: CatalogModelInfo) async {
        let nextEnabled = !(m.enabled ?? true)
        do {
            try await appState.setModelEnabled(
                m.modelID, provider: m.provider, scope: "vault", enabled: nextEnabled
            )
            await reload()
        } catch {
            recordActionFailure("Toggle \(m.modelID) failed", error: error)
        }
    }

    private func recordActionFailure(_ message: String, error: Error) {
        errorMessage = "\(message): \(error.localizedDescription)"
        hubLog.error("\(message, privacy: .public): \(error.localizedDescription, privacy: .public)")
        appState.errorLogger?.log(message, error: error)
    }
}
