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

    @State private var aiStatus: AIStatusInfo?
    @State private var models: [CatalogModelInfo] = []
    @State private var testResults: [String: TestOutcome] = [:]
    @State private var loading = true
    @State private var isDiscovering = false
    @State private var errorMessage: String?
    @State private var filter = CatalogFilter()
    @State private var searchText: String = ""
    /// Group keys currently collapsed. Default is expanded so users
    /// immediately see their models; collapsing is an opt-in to
    /// reduce clutter on very large catalogs.
    @State private var collapsedGroups: Set<String> = []

    struct TestOutcome: Equatable {
        var running: Bool
        var ok: Bool?
        var latency: String?
        var detail: String?
    }

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
        VStack(spacing: 0) {
            header
            Divider()
            ScrollView {
                VStack(alignment: .leading, spacing: 20) {
                    providersSection
                    activeSection
                    catalogSection
                }
                .padding()
            }
            Divider()
            footer
        }
        .frame(width: 820, height: 640)
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
            Button("Close") { onClose() }
                .keyboardShortcut(.cancelAction)
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
                ForEach(aiStatus?.providers ?? fallbackProviders()) { p in
                    providerCard(p)
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
        ]
    }

    private func providerCard(_ p: ProviderStatusInfo) -> some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack {
                Text(providerDisplayName(p.name)).font(.headline)
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

    private func providerDisplayName(_ name: String) -> String {
        switch name {
        case "bedrock": return "AWS Bedrock"
        case "openrouter": return "OpenRouter"
        case "ollama": return "Ollama (local)"
        default: return name.capitalized
        }
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
            } else {
                Text("Loading…").foregroundStyle(.secondary)
            }
        }
    }

    private func providerDisabledBanner(_ provider: String) -> some View {
        HStack(spacing: 8) {
            Image(systemName: "exclamationmark.triangle.fill")
                .foregroundStyle(.orange)
            Text("Active provider \(providerDisplayName(provider)) is disabled — re-enable it or pick another active model.")
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
        }
        .padding(.vertical, 4)
    }

    private func activeMeta(for entry: CatalogModelInfo?) -> String {
        guard let m = entry else { return "untested" }
        var parts: [String] = []
        if let t = m.testedAt, !t.isEmpty { parts.append("tested " + t) }
        if let bench = testResults[m.modelID]?.latency { parts.append(bench) }
        if let price = priceLabel(m) { parts.append(price) }
        return parts.isEmpty ? "no test data" : parts.joined(separator: " · ")
    }

    private func priceLabel(_ m: CatalogModelInfo) -> String? {
        if let pIn = m.priceIn, pIn > 0 {
            if let pOut = m.priceOut, pOut > 0 {
                return String(format: "$%.2f/$%.2f per M", pIn, pOut)
            }
            return String(format: "$%.3f/M", pIn)
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
                } else {
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

            if filteredModels().isEmpty {
                Text("No models match the current filter.")
                    .foregroundStyle(.secondary)
                    .padding()
                    .frame(maxWidth: .infinity)
            }
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

    /// filteredModels applies the search text + filter toggles. Used
    /// both for the empty-state check and as the input to group
    /// partitioning.
    private func filteredModels() -> [CatalogModelInfo] {
        let needle = searchText.trimmingCharacters(in: .whitespaces).lowercased()
        return models.filter { m in
            if filter.testedOnly && (m.testedAt ?? "").isEmpty { return false }
            if filter.enabledOnly && (m.enabled == false) { return false }
            if !needle.isEmpty {
                let vendor = VendorInfo.from(modelID: m.modelID, provider: m.provider)
                let haystack = (m.modelID + " " + vendor.display + " " + (m.name)).lowercased()
                if !haystack.contains(needle) { return false }
            }
            return true
        }
    }

    @ViewBuilder
    private func catalogTypeSection(type: String, title: String) -> some View {
        let ofType = filteredModels().filter { $0.modelType == type }
        if !ofType.isEmpty {
            VStack(alignment: .leading, spacing: 4) {
                HStack {
                    Text(title).font(.subheadline.bold())
                    Text("(\(ofType.count))").font(.caption).foregroundStyle(.secondary)
                    Spacer()
                }
                .padding(.top, 6)
                ForEach(groupedByVendor(ofType), id: \.key) { group in
                    vendorGroupView(group: group)
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
            let v = VendorInfo.from(modelID: m.modelID, provider: m.provider)
            let k = groupKey(type: m.modelType, vendor: v.vendor, provider: m.provider)
            byKey[k, default: []].append(m)
            displayByKey[k] = (v.vendor, v.display, m.provider)
        }
        let groups = byKey.map { (k, list) -> VendorGroup in
            let (vendor, display, provider) = displayByKey[k] ?? ("other", "Other", "")
            let sorted = list.sorted { versionSortKey($0.modelID) > versionSortKey($1.modelID) }
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
                Text(providerDisplayName(group.provider))
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
            errorMessage = "Bulk toggle \(group.vendorDisplay) failed: \(error.localizedDescription)"
        }
    }

    private func modelRow(_ m: CatalogModelInfo) -> some View {
        let outcome = testResults[m.modelID]
        let isActive = aiStatus?.provider == m.provider
            && (m.modelID == aiStatus?.embeddingModel || m.modelID == aiStatus?.genModel)
        let disabledProvider = providerIsDisabled(m.provider)
        return HStack(alignment: .center, spacing: 10) {
            VStack(alignment: .leading, spacing: 2) {
                HStack(spacing: 6) {
                    Text(m.modelID)
                        .font(.body.monospaced())
                        .lineLimit(1)
                        .truncationMode(.middle)
                    if isActive {
                        Text("ACTIVE")
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
            if let outcome {
                outcomeBadge(outcome)
            }
            rowActions(m, disabled: disabledProvider)
        }
        .padding(.vertical, 4)
        .opacity(disabledProvider ? 0.55 : 1.0)
    }

    private func metaLine(_ m: CatalogModelInfo) -> String {
        var parts: [String] = [m.provider, m.modelType]
        if let d = m.dimensions, d > 0 { parts.append("\(d)d") }
        if let c = m.contextLen, c > 0 { parts.append("\(c / 1000)k ctx") }
        if let p = priceLabel(m) { parts.append(p) }
        if let strat = m.invokeStrategy, !strat.isEmpty { parts.append(strat) }
        return parts.joined(separator: " · ")
    }

    @ViewBuilder
    private func outcomeBadge(_ o: TestOutcome) -> some View {
        if o.running {
            ProgressView().controlSize(.small)
        } else if let ok = o.ok {
            if ok {
                Image(systemName: "checkmark.circle.fill").foregroundStyle(.green)
            } else {
                Image(systemName: "xmark.circle.fill")
                    .foregroundStyle(.red)
                    .help(o.detail ?? "failed")
            }
        }
    }

    private func rowActions(_ m: CatalogModelInfo, disabled: Bool) -> some View {
        HStack(spacing: 6) {
            Button("Test") { Task { await testRow(m) } }
                .controlSize(.small)
                .buttonStyle(.borderless)
                .disabled(disabled || testResults[m.modelID]?.running == true)
            Button("Set active") { Task { await makeActive(m) } }
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
            errorMessage = "Failed to load AI state: \(error.localizedDescription)"
            hubLog.error("AI Hub reload failed: \(error.localizedDescription)")
        }
    }

    private func discover() async {
        isDiscovering = true
        defer { isDiscovering = false }
        // fetchModelsForWizard already passes --discover; re-run it.
        do {
            models = try await appState.fetchModelsForWizard()
        } catch {
            errorMessage = "Discover failed: \(error.localizedDescription)"
        }
    }

    private func toggleProvider(_ p: ProviderStatusInfo) async {
        do {
            try await appState.setProviderDisabled(p.name, disabled: !p.disabled)
            // FSEvents on config.yaml will refresh; also reload proactively.
            await reload()
        } catch {
            errorMessage = "Toggle \(p.name) failed: \(error.localizedDescription)"
        }
    }

    private func testRow(_ m: CatalogModelInfo) async {
        testResults[m.modelID] = TestOutcome(running: true)
        do {
            let scope = "vault"
            let result = try await appState.testAndSave(
                modelID: m.modelID, provider: m.provider, type: m.modelType, scope: scope
            )
            testResults[m.modelID] = TestOutcome(
                running: false,
                ok: result.ok,
                latency: result.latency,
                detail: result.detail
            )
            // A successful save bumps catalog version via FSEvents — let
            // the watcher reload us. For fast feedback also kick reload.
            if result.ok { Task { await reload() } }
        } catch {
            testResults[m.modelID] = TestOutcome(
                running: false, ok: false, latency: nil, detail: error.localizedDescription
            )
        }
    }

    private func makeActive(_ m: CatalogModelInfo) async {
        do {
            try await appState.setActiveModel(type: m.modelType, modelID: m.modelID)
            await reload()
        } catch {
            errorMessage = "Set active failed: \(error.localizedDescription)"
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
            errorMessage = "Toggle \(m.modelID) failed: \(error.localizedDescription)"
        }
    }
}
