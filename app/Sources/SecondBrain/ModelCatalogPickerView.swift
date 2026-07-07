import SwiftUI
import os

private let pickerLog = Logger(subsystem: "dev.apresai.2ndbrain", category: "model-picker")

struct ModelCatalogPickerContext: Equatable {
    var typeScope: String?
    var modelID: String?
}

struct ModelCatalogPickerView: View {
    @Environment(AppState.self) private var appState

    let models: [CatalogModelInfo]
    let aiStatus: AIStatusInfo?
    let initialType: String?
    let initialModelID: String?
    let onClose: () -> Void
    let onReload: () async -> Void

    @State private var searchText = ""
    @State private var typeFilter = "all"
    @State private var providerFilter = "all"
    @State private var tierFilter = "all"
    @State private var enabledFilter = "all"
    @State private var testedOnly = false
    @State private var compatibleOnly = false
    @State private var sortMode = PickerSort.best
    @State private var selectedKey: String?
    @State private var costProbeSelection = "test"
    @State private var benchmarkProbeSelection = "embed"
    @State private var costPreview: CostPreviewResponse?
    @State private var benchmarkEvents: [BenchmarkEvent] = []
    @State private var thresholdText = ""
    @State private var statusText: String?
    @State private var errorText: String?
    @State private var isTesting = false
    @State private var isBenchmarking = false
    @State private var isCosting = false

    var body: some View {
        VStack(spacing: 0) {
            header
            Divider()
            HStack(spacing: 0) {
                sidebar
                Divider()
                detail
            }
        }
        .frame(width: 980, height: 700)
        .background(.regularMaterial)
        .clipShape(RoundedRectangle(cornerRadius: 10))
        .shadow(radius: 24)
        .task { initializeSelection() }
        .onChange(of: selectedKey) { _, _ in resetDetailInputs() }
        .alert(
            "Model picker error",
            isPresented: Binding(get: { errorText != nil }, set: { if !$0 { errorText = nil } }),
            actions: { Button("OK") { errorText = nil } },
            message: { Text(errorText ?? "") }
        )
    }

    private var header: some View {
        HStack {
            VStack(alignment: .leading, spacing: 2) {
                Text("Model Catalog")
                    .font(.title2.bold())
                Text("Browse, test, benchmark, enable, and activate models from the catalog.")
                    .font(.callout)
                    .foregroundStyle(.secondary)
            }
            Spacer()
            if let statusText {
                Text(statusText)
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
            Button(action: onClose) {
                Image(systemName: "xmark.circle.fill")
                    .font(.title3)
            }
            .buttonStyle(.plain)
            .help("Close")
        }
        .padding()
    }

    private var sidebar: some View {
        VStack(spacing: 10) {
            searchField
            filterBar
            List(selection: $selectedKey) {
                ForEach(filteredModels) { model in
                    ModelCatalogSidebarRow(model: model, activeKinds: activeKinds(for: model))
                        .tag(model.id)
                }
            }
            .listStyle(.sidebar)
        }
        .frame(width: 330)
        .padding(10)
    }

    private var searchField: some View {
        HStack {
            Image(systemName: "magnifyingglass")
                .foregroundStyle(.secondary)
            TextField("Search models", text: $searchText)
                .textFieldStyle(.plain)
            if !searchText.isEmpty {
                Button(action: { searchText = "" }) {
                    Image(systemName: "xmark.circle.fill")
                        .foregroundStyle(.secondary)
                }
                .buttonStyle(.plain)
            }
        }
        .padding(7)
        .background(Color(nsColor: .controlBackgroundColor))
        .clipShape(RoundedRectangle(cornerRadius: 7))
    }

    private var filterBar: some View {
        VStack(spacing: 8) {
            HStack {
                Picker("Type", selection: $typeFilter) {
                    Text("All").tag("all")
                    Text("Embedding").tag("embedding")
                    Text("Generation").tag("generation")
                    Text("Rerank").tag("rerank")
                }
                Picker("Sort", selection: $sortMode) {
                    ForEach(PickerSort.allCases) { sort in
                        Text(sort.label).tag(sort)
                    }
                }
            }
            HStack {
                Picker("Provider", selection: $providerFilter) {
                    Text("All Providers").tag("all")
                    ForEach(providers, id: \.self) { provider in
                        Text(provider).tag(provider)
                    }
                }
                Picker("Tier", selection: $tierFilter) {
                    Text("All Tiers").tag("all")
                    Text("Verified").tag("verified")
                    Text("Tested by You").tag("user_verified")
                    Text("Unverified").tag("unverified")
                }
            }
            HStack {
                Picker("Enabled", selection: $enabledFilter) {
                    Text("Any").tag("all")
                    Text("Default").tag("default")
                    Text("Enabled").tag("enabled")
                    Text("Disabled").tag("disabled")
                }
                Toggle("Tested", isOn: $testedOnly)
                Toggle("Compatible", isOn: $compatibleOnly)
            }
            .font(.caption)
        }
        .controlSize(.small)
    }

    @ViewBuilder
    private var detail: some View {
        if let model = selectedModel {
            ScrollView {
                VStack(alignment: .leading, spacing: 14) {
                    detailHeader(model)
                    providerWarning(model)
                    factsSection(model)
                    statusSection(model)
                    controlsSection(model)
                    costSection(model)
                    benchmarkSection(model)
                }
                .padding()
            }
            .frame(maxWidth: .infinity, maxHeight: .infinity)
        } else {
            VStack(spacing: 8) {
                Image(systemName: "square.stack.3d.up")
                    .font(.largeTitle)
                    .foregroundStyle(.secondary)
                Text("No model selected")
                    .font(.headline)
                Text("Adjust filters or choose a model from the catalog.")
                    .foregroundStyle(.secondary)
            }
            .frame(maxWidth: .infinity, maxHeight: .infinity)
        }
    }

    private func detailHeader(_ model: CatalogModelInfo) -> some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack(alignment: .firstTextBaseline) {
                VStack(alignment: .leading, spacing: 3) {
                    Text(model.name.isEmpty ? model.modelID : model.name)
                        .font(.title3.bold())
                    Text(model.modelID)
                        .font(.callout.monospaced())
                        .foregroundStyle(.secondary)
                        .textSelection(.enabled)
                }
                Spacer()
                ForEach(activeKinds(for: model), id: \.self) { kind in
                    Badge(text: "ACTIVE as \(kind)", color: .blue)
                }
            }
            HStack {
                Badge(text: model.vendorDisplay ?? "Other", color: .secondary)
                if let family = model.family, !family.isEmpty {
                    Badge(text: family, color: .secondary)
                }
                Badge(text: model.provider, color: .secondary)
                Badge(text: model.modelType, color: .secondary)
                tierBadge(model)
                compatibilityBadge(model)
                testBadge(model)
            }
        }
    }

    @ViewBuilder
    private func providerWarning(_ model: CatalogModelInfo) -> some View {
        if let status = aiStatus, status.provider != model.provider {
            HStack(spacing: 8) {
                Image(systemName: "exclamationmark.triangle.fill")
                    .foregroundStyle(.orange)
                Text("This app uses one shared ai.provider. Setting this active will switch the provider from \(status.provider) to \(model.provider).")
                    .font(.callout)
            }
            .padding(8)
            .background(Color.orange.opacity(0.12))
            .clipShape(RoundedRectangle(cornerRadius: 7))
        }
    }

    private func factsSection(_ model: CatalogModelInfo) -> some View {
        DetailSection(title: "Facts", systemImage: "info.circle") {
            Grid(alignment: .leading, horizontalSpacing: 22, verticalSpacing: 8) {
                factRow("Context", value: formatContext(model.contextLen))
                factRow("Dimensions", value: model.dimensions.map { "\($0)" } ?? "-")
                factRow("Input price", value: priceValue(model.priceIn, suffix: "/M tokens"))
                factRow("Output price", value: priceValue(model.priceOut, suffix: "/M tokens"))
                factRow("Request price", value: priceValue(model.priceRequest, suffix: "/request"))
                factRow("Price source", value: model.priceSource ?? "-")
                factRow("Price override", value: boolValue(model.priceOverride))
                factRow("Credentials", value: boolValue(model.credentials))
                factRow("Reachable", value: boolValue(model.reachable))
                factRow("Rate limit", value: formatRateLimit(model))
                factRow("Invoke", value: model.invokeStrategy ?? "-")
                factRow("Local", value: model.local == true ? "yes" : "no")
                if let notes = model.notes, !notes.isEmpty {
                    factRow("Notes", value: notes)
                }
            }
        }
    }

    private func statusSection(_ model: CatalogModelInfo) -> some View {
        DetailSection(title: "Status", systemImage: "checkmark.seal") {
            VStack(alignment: .leading, spacing: 8) {
                HStack {
                    Text("Enable state")
                    Spacer()
                    Menu(enableStateLabel(model)) {
                        Button("Default") { Task { await setEnableState(model, "default") } }
                        Button("Enabled") { Task { await setEnableState(model, "enabled") } }
                        Button("Disabled") { Task { await setEnableState(model, "disabled") } }
                    }
                    .menuStyle(.borderlessButton)
                }
                statusLine("Compatible", value: model.compatible == false ? "No" : "Yes", detail: model.compatibilityReason)
                if let testedAt = model.testedAt, !testedAt.isEmpty {
                    statusLine("Tested", value: testedAt, detail: nil)
                } else {
                    statusLine("Tested", value: "Untested", detail: nil)
                }
                accessCallout(model)
                statusLine("Test latency", value: model.testLatencyMs.map { "\($0)ms" } ?? "-", detail: nil)
                if let benchmark = model.benchmark {
                    statusLine("Benchmark", value: benchmark.avgLatencyMs.map { "\($0)ms avg" } ?? "available", detail: benchmark.ranAt)
                } else {
                    statusLine("Benchmark", value: "Not run", detail: nil)
                }
            }
        }
    }

    /// The failure panel that replaced the buried tooltip: title, guidance,
    /// an action link where one exists (e.g. the Bedrock Model-access page),
    /// and the raw error selectable underneath.
    @ViewBuilder
    private func accessCallout(_ model: CatalogModelInfo) -> some View {
        if let err = model.testError, !err.isEmpty {
            let guidance = ModelAccessPresentation.guidance(code: model.testErrorCode, provider: model.provider)
            VStack(alignment: .leading, spacing: 6) {
                Label(guidance?.title ?? "Last test failed", systemImage: "exclamationmark.triangle.fill")
                    .font(.callout.weight(.semibold))
                    .foregroundStyle(.red)
                if let advice = guidance?.advice, !advice.isEmpty {
                    Text(advice).font(.callout)
                }
                if let label = guidance?.actionLabel, let url = guidance?.actionURL {
                    Link(label, destination: url).font(.callout)
                }
                Text(err)
                    .font(.caption.monospaced())
                    .foregroundStyle(.secondary)
                    .textSelection(.enabled)
                    .lineLimit(4)
            }
            .padding(10)
            .frame(maxWidth: .infinity, alignment: .leading)
            .background(Color.red.opacity(0.08))
            .clipShape(RoundedRectangle(cornerRadius: 8))
        }
    }

    private func controlsSection(_ model: CatalogModelInfo) -> some View {
        DetailSection(title: "Actions", systemImage: "slider.horizontal.3") {
            VStack(alignment: .leading, spacing: 10) {
                HStack {
                    Button {
                        Task { await testModel(model) }
                    } label: {
                        Label(isTesting ? "Testing" : "Test", systemImage: "bolt.fill")
                    }
                    .disabled(isTesting || model.compatible == false)

                    Button {
                        Task { await setActive(model, reembed: false) }
                    } label: {
                        Label("Set Active", systemImage: "checkmark.circle")
                    }
                    .disabled(model.compatible == false || appState.isIndexing)
                    .help(appState.isIndexing ? "Wait for the index rebuild to finish" : "Set as active \(model.modelType)")

                    if model.modelType == "embedding" {
                        Button {
                            Task { await setActive(model, reembed: true) }
                        } label: {
                            Label("Set Active + Re-embed", systemImage: "arrow.triangle.2.circlepath")
                        }
                        .disabled(model.compatible == false || appState.isIndexing)
                    }
                }
                .controlSize(.small)

                if model.modelType == "embedding" {
                    VStack(alignment: .leading, spacing: 4) {
                        HStack {
                            Text("Catalog recommendation for this model")
                                .help("Saves this model's recommended similarity threshold to the user catalog. It feeds the automatic resolution chain when no vault override is set; the vault-level override lives under Advanced settings in the AI Hub.")
                            TextField("0.00-1.00", text: $thresholdText)
                                .textFieldStyle(.roundedBorder)
                                .frame(width: 100)
                            Button("Save") { Task { await saveThreshold(model) } }
                                .disabled(Double(thresholdText) == nil)
                            Text("recommended \(formatThreshold(model.recommendedSimilarityThreshold))")
                                .foregroundStyle(.secondary)
                        }
                        if let t = aiStatus?.similarityThreshold {
                            Text(String(format: "effective threshold now: %.2f (%@)", t, aiStatus?.similarityThresholdSource ?? "unknown"))
                                .font(.caption)
                                .foregroundStyle(.secondary)
                        }
                    }
                    .font(.callout)
                }
            }
        }
    }

    private func costSection(_ model: CatalogModelInfo) -> some View {
        DetailSection(title: "Cost Preview", systemImage: "dollarsign.circle") {
            VStack(alignment: .leading, spacing: 8) {
                HStack {
                    Picker("Probe", selection: $costProbeSelection) {
                        Text("Test").tag("test")
                        Text("Embed").tag("embed")
                        Text("Generate").tag("generate")
                        Text("RAG").tag("rag")
                        Text("Retrieval").tag("retrieval")
                    }
                    .frame(width: 180)
                    Button {
                        Task { await previewCost(model) }
                    } label: {
                        Label(isCosting ? "Estimating" : "Estimate", systemImage: "sum")
                    }
                    .disabled(isCosting)
                }
                if let cost = costPreview?.estimates.first {
                    Text(String(format: "%@ · $%.6f · %@ pricing · %d input · %d output · %d request(s)",
                                cost.probe,
                                cost.usd,
                                cost.knownPricing ? "known" : "unknown",
                                cost.inputTokens,
                                cost.outputTokens,
                                cost.requests))
                        .font(.caption)
                        .foregroundStyle(.secondary)
                } else {
                    Text("No estimate loaded.")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
            }
        }
    }

    private func benchmarkSection(_ model: CatalogModelInfo) -> some View {
        DetailSection(title: "Benchmark", systemImage: "speedometer") {
            VStack(alignment: .leading, spacing: 8) {
                HStack {
                    Picker("Probe", selection: $benchmarkProbeSelection) {
                        Text("Embed").tag("embed")
                        Text("Generate").tag("generate")
                        Text("RAG").tag("rag")
                        Text("Retrieval").tag("retrieval")
                    }
                    .frame(width: 180)
                    Button {
                        Task { await benchmark(model) }
                    } label: {
                        Label(isBenchmarking ? "Running" : "Benchmark", systemImage: "timer")
                    }
                    .disabled(isBenchmarking || model.compatible == false)
                }
                if let benchmark = model.benchmark {
                    Text("Latest: \(benchmark.avgLatencyMs.map { "\($0)ms avg" } ?? "-") · quality \(formatQuality(benchmark.qualityScore)) · docs \(benchmark.vaultDocCount ?? 0)")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
                if benchmarkEvents.isEmpty {
                    Text("No benchmark events yet.")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                } else {
                    VStack(alignment: .leading, spacing: 3) {
                        ForEach(benchmarkEvents.suffix(6)) { event in
                            Text(eventLine(event))
                                .font(.caption.monospaced())
                                .foregroundStyle(event.result?.ok == false ? .red : .secondary)
                                .lineLimit(2)
                        }
                    }
                }
            }
        }
    }

    private var selectedModel: CatalogModelInfo? {
        if let selectedKey, let model = models.first(where: { $0.id == selectedKey }) {
            return model
        }
        return filteredModels.first
    }

    private var filteredModels: [CatalogModelInfo] {
        let needle = searchText.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        return models.filter { model in
            if typeFilter != "all" && model.modelType != typeFilter { return false }
            if providerFilter != "all" && model.provider != providerFilter { return false }
            if tierFilter != "all" && (model.tier ?? "") != tierFilter { return false }
            if testedOnly && ((model.testedAt ?? "").isEmpty || !(model.testError ?? "").isEmpty) { return false }
            if compatibleOnly && model.compatible == false { return false }
            switch enabledFilter {
            case "default": if model.enabled != nil { return false }
            case "enabled": if model.enabled != true { return false }
            case "disabled": if model.enabled != false { return false }
            default: break
            }
            if !needle.isEmpty {
                let haystack = [
                    model.modelID,
                    model.name,
                    model.vendorDisplay ?? "",
                    model.family ?? "",
                    model.provider,
                    model.tier ?? "",
                ].joined(separator: " ").lowercased()
                if !haystack.contains(needle) { return false }
            }
            return true
        }
        .sorted(by: sortMode.comparator)
    }

    private var providers: [String] {
        Array(Set(models.map(\.provider))).sorted()
    }

    private func initializeSelection() {
        if let initialType {
            typeFilter = initialType
        }
        if let initialModelID, let model = models.first(where: { $0.modelID == initialModelID && (initialType == nil || $0.modelType == initialType) }) {
            selectedKey = model.id
        } else {
            selectedKey = filteredModels.first?.id
        }
        resetDetailInputs()
    }

    private func resetDetailInputs() {
        guard let model = selectedModel else { return }
        thresholdText = model.recommendedSimilarityThreshold.map { String(format: "%.2f", $0) } ?? ""
        benchmarkProbeSelection = model.modelType == "embedding" ? "embed" : "generate"
        costPreview = nil
        benchmarkEvents = []
        statusText = nil
    }

    private func testModel(_ model: CatalogModelInfo) async {
        guard await confirmPaidOperation(appState: appState, modelIDs: [model.modelID], probe: "test", operation: "Test \(model.modelID)") else { return }
        isTesting = true
        defer { isTesting = false }
        do {
            let result = try await appState.testAndSave(modelID: model.modelID, provider: model.provider, type: model.modelType, scope: "vault")
            if result.ok {
                statusText = "Test passed: \(result.latency)"
            } else {
                let guidance = ModelAccessPresentation.guidance(code: result.errorCode, provider: model.provider, remediation: result.remediation)
                var line = "Test failed"
                if let badge = guidance?.badge { line += " [\(badge)]" }
                if let advice = guidance?.advice, !advice.isEmpty { line += ": \(advice)" }
                errorText = line
            }
            pickerLog.info("Picker test result model=\(model.modelID, privacy: .public) ok=\(result.ok) code=\(result.errorCode ?? "", privacy: .public)")
            await onReload()
        } catch {
            errorText = "Test failed: \(error.localizedDescription)"
        }
    }

    private func setActive(_ model: CatalogModelInfo, reembed: Bool) async {
        do {
            try await appState.setActiveModel(type: model.modelType, modelID: model.modelID, provider: model.provider)
            statusText = "Active \(model.modelType) updated"
            if reembed {
                appState.rebuildIndex(forceReembed: true)
            }
            await onReload()
        } catch {
            errorText = "Set active failed: \(error.localizedDescription)"
        }
    }

    private func setEnableState(_ model: CatalogModelInfo, _ state: String) async {
        do {
            try await appState.setModelEnableState(model.modelID, provider: model.provider, scope: "vault", state: state)
            statusText = "Enable state set to \(state)"
            await onReload()
        } catch {
            errorText = "Enable state failed: \(error.localizedDescription)"
        }
    }

    private func saveThreshold(_ model: CatalogModelInfo) async {
        guard let threshold = Double(thresholdText), threshold >= 0, threshold <= 1 else {
            errorText = "Threshold must be between 0 and 1."
            return
        }
        do {
            try await appState.setModelSimilarityThreshold(model, threshold: threshold, scope: "vault")
            statusText = "Threshold saved"
            await onReload()
        } catch {
            errorText = "Threshold save failed: \(error.localizedDescription)"
        }
    }

    private func previewCost(_ model: CatalogModelInfo) async {
        isCosting = true
        defer { isCosting = false }
        do {
            costPreview = try await appState.costPreview(modelIDs: [model.modelID], probe: costProbe(costProbeSelection))
            statusText = "Cost estimate loaded"
        } catch {
            errorText = "Cost preview failed: \(error.localizedDescription)"
        }
    }

    private func benchmark(_ model: CatalogModelInfo) async {
        let probe = benchmarkProbe(benchmarkProbeSelection)
        // The retrieval probe scores stored embeddings locally (zero API
        // calls), so it needs no spend confirm.
        if probe != "retrieval" {
            guard await confirmPaidOperation(appState: appState, modelIDs: [model.modelID], probe: costProbe(probe), operation: "Benchmark \(model.modelID)") else { return }
        }
        isBenchmarking = true
        benchmarkEvents = []
        defer { isBenchmarking = false }
        do {
            try await appState.benchmarkModel(modelID: model.modelID, provider: model.provider, type: model.modelType, probe: benchmarkProbe(benchmarkProbeSelection)) { event in
                benchmarkEvents.append(event)
            }
            statusText = "Benchmark complete"
            await onReload()
        } catch {
            errorText = "Benchmark failed: \(error.localizedDescription)"
        }
    }

    private func activeKinds(for model: CatalogModelInfo) -> [String] {
        guard aiStatus?.provider == model.provider else { return [] }
        var kinds: [String] = []
        if aiStatus?.embeddingModel == model.modelID { kinds.append("embedding") }
        if aiStatus?.genModel == model.modelID { kinds.append("generation") }
        if aiStatus?.rerankModel == model.modelID { kinds.append("rerank") }
        return kinds
    }

    private func tierBadge(_ model: CatalogModelInfo) -> some View {
        switch model.tier {
        case "verified": return Badge(text: "verified shipped", color: .green)
        case "user_verified": return Badge(text: "tested by you", color: .blue)
        case "unverified": return Badge(text: "unverified", color: .gray)
        default: return Badge(text: "unknown tier", color: .gray)
        }
    }

    private func compatibilityBadge(_ model: CatalogModelInfo) -> some View {
        if model.compatible == false {
            return Badge(text: "incompatible", color: .red, help: model.compatibilityReason)
        }
        return Badge(text: "compatible", color: .green)
    }

    private func testBadge(_ model: CatalogModelInfo) -> some View {
        if let label = ModelAccessPresentation.badgeLabel(testError: model.testError, testErrorCode: model.testErrorCode, provider: model.provider) {
            return Badge(text: label, color: .red, help: model.testError ?? label)
        }
        if let testedAt = model.testedAt, !testedAt.isEmpty {
            return Badge(text: "tested", color: .green, help: testedAt)
        }
        return Badge(text: "untested", color: .gray)
    }

    private func factRow(_ label: String, value: String) -> some View {
        GridRow {
            Text(label)
                .foregroundStyle(.secondary)
            Text(value)
                .textSelection(.enabled)
        }
    }

    private func statusLine(_ label: String, value: String, detail: String?) -> some View {
        VStack(alignment: .leading, spacing: 2) {
            HStack {
                Text(label)
                    .foregroundStyle(.secondary)
                Spacer()
                Text(value)
            }
            if let detail, !detail.isEmpty {
                Text(detail)
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .textSelection(.enabled)
            }
        }
    }

    private func enableStateLabel(_ model: CatalogModelInfo) -> String {
        guard let enabled = model.enabled else { return "Default" }
        return enabled ? "Enabled" : "Disabled"
    }

    private func formatContext(_ value: Int?) -> String {
        guard let value, value > 0 else { return "-" }
        if value >= 1_000_000 { return "\(value / 1_000_000)M tokens" }
        if value >= 1_000 { return "\(value / 1_000)k tokens" }
        return "\(value) tokens"
    }

    private func priceValue(_ value: Double?, suffix: String) -> String {
        guard let value else { return "-" }
        if value == 0 { return "free" }
        return String(format: "$%.4f%@", value, suffix)
    }

    private func boolValue(_ value: Bool?) -> String {
        guard let value else { return "-" }
        return value ? "yes" : "no"
    }

    private func formatRateLimit(_ model: CatalogModelInfo) -> String {
        var parts: [String] = []
        if let rps = model.rateLimitRPS, rps > 0 {
            parts.append(String(format: "%.2g rps", rps))
        }
        if let tpm = model.rateLimitTPM, tpm > 0 {
            parts.append("\(tpm) tpm")
        }
        return parts.isEmpty ? "-" : parts.joined(separator: " / ")
    }

    private func formatThreshold(_ value: Double?) -> String {
        guard let value else { return "-" }
        return String(format: "%.2f", value)
    }

    private func formatQuality(_ value: Double?) -> String {
        guard let value, value > 0 else { return "-" }
        return String(format: "%.2f", value)
    }

    private func costProbe(_ probe: String) -> String {
        switch probe {
        case "embed": return "bench_embed"
        case "generate": return "bench_gen"
        case "rag": return "bench_rag"
        case "retrieval": return "retrieval"
        default: return "test"
        }
    }

    private func benchmarkProbe(_ probe: String) -> String {
        switch probe {
        case "embed", "generate", "rag", "retrieval": return probe
        default:
            return selectedModel?.modelType == "embedding" ? "embed" : "generate"
        }
    }

    private func eventLine(_ event: BenchmarkEvent) -> String {
        if let result = event.result {
            let status = result.skipped == true ? "SKIP" : (result.ok ? "PASS" : "FAIL")
            let detail = result.detail.map { " \($0)" } ?? ""
            return "\(result.probe) \(status) \(result.latencyMs)ms\(detail)"
        }
        return event.message ?? event.event
    }
}

private enum PickerSort: String, CaseIterable, Identifiable {
    case best
    case cheapest
    case fastest
    case newest
    case name

    var id: String { rawValue }

    var label: String {
        switch self {
        case .best: return "Best"
        case .cheapest: return "Cheapest"
        case .fastest: return "Fastest"
        case .newest: return "Newest"
        case .name: return "Name"
        }
    }

    func comparator(_ a: CatalogModelInfo, _ b: CatalogModelInfo) -> Bool {
        switch self {
        case .best:
            return score(a) > score(b)
        case .cheapest:
            return priceRank(a) < priceRank(b)
        case .fastest:
            return (a.benchmark?.avgLatencyMs ?? a.testLatencyMs ?? Int64.max) < (b.benchmark?.avgLatencyMs ?? b.testLatencyMs ?? Int64.max)
        case .newest:
            return (a.versionSortKey ?? a.modelID) > (b.versionSortKey ?? b.modelID)
        case .name:
            return displayName(a) < displayName(b)
        }
    }

    private func score(_ model: CatalogModelInfo) -> Int {
        var value = 0
        if model.compatible != false { value += 20 }
        if model.tier == "verified" { value += 16 }
        if model.tier == "user_verified" { value += 12 }
        if model.testError == nil || model.testError == "" { value += 6 }
        if model.testedAt != nil { value += 4 }
        if model.enabled != false { value += 3 }
        if model.active == true { value += 2 }
        return value
    }

    private func priceRank(_ model: CatalogModelInfo) -> Double {
        let hasTokenPrice = (model.priceIn ?? 0) > 0 || (model.priceOut ?? 0) > 0
        let hasRequestPrice = (model.priceRequest ?? 0) > 0
        if model.local == true || (model.priceSource != nil && !hasTokenPrice && !hasRequestPrice) {
            return 0
        }
        if hasRequestPrice {
            return 1 + (model.priceRequest ?? 0)
        }
        if hasTokenPrice {
            return 10_000 + (model.priceIn ?? 0) + (model.priceOut ?? 0)
        }
        return Double.greatestFiniteMagnitude
    }

    private func displayName(_ model: CatalogModelInfo) -> String {
        (model.name.isEmpty ? model.modelID : model.name).lowercased()
    }
}

private struct ModelCatalogSidebarRow: View {
    let model: CatalogModelInfo
    let activeKinds: [String]

    var body: some View {
        VStack(alignment: .leading, spacing: 3) {
            HStack(spacing: 5) {
                Text(model.name.isEmpty ? model.modelID : model.name)
                    .lineLimit(1)
                if !activeKinds.isEmpty {
                    Image(systemName: "checkmark.circle.fill")
                        .foregroundStyle(.blue)
                        .help("Active as \(activeKinds.joined(separator: ", "))")
                }
                if model.enabled == false {
                    Image(systemName: "minus.circle")
                        .foregroundStyle(.secondary)
                        .help("Disabled")
                }
            }
            Text([model.vendorDisplay ?? model.provider, model.family ?? "", model.modelType].filter { !$0.isEmpty }.joined(separator: " · "))
                .font(.caption)
                .foregroundStyle(.secondary)
                .lineLimit(1)
            HStack(spacing: 6) {
                if model.compatible == false {
                    Label("incompatible", systemImage: "xmark.circle.fill")
                        .foregroundStyle(.red)
                } else if let err = model.testError, !err.isEmpty {
                    Label("failed", systemImage: "xmark.circle.fill")
                        .foregroundStyle(.red)
                } else if model.testedAt != nil {
                    Label("tested", systemImage: "checkmark.circle.fill")
                        .foregroundStyle(.green)
                } else {
                    Label("untested", systemImage: "circle")
                        .foregroundStyle(.secondary)
                }
            }
            .font(.caption2)
            .labelStyle(.titleAndIcon)
        }
        .padding(.vertical, 3)
    }
}

private struct DetailSection<Content: View>: View {
    let title: String
    let systemImage: String
    let content: Content

    init(title: String, systemImage: String, @ViewBuilder content: () -> Content) {
        self.title = title
        self.systemImage = systemImage
        self.content = content()
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            Label(title, systemImage: systemImage)
                .font(.subheadline.bold())
                .foregroundStyle(.secondary)
            content
        }
        .padding(12)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(Color(nsColor: .controlBackgroundColor))
        .clipShape(RoundedRectangle(cornerRadius: 8))
    }
}

private struct Badge: View {
    let text: String
    let color: Color
    var help: String?

    var body: some View {
        Text(text)
            .font(.caption2.bold())
            .padding(.horizontal, 6)
            .padding(.vertical, 2)
            .background(color.opacity(0.14))
            .foregroundStyle(color)
            .clipShape(RoundedRectangle(cornerRadius: 4))
            .help(help ?? text)
    }
}
