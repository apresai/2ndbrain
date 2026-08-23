import SwiftUI

/// Testing & Benchmarking: everything measurable in one sidebar destination —
/// validation (can this account call X), benchmarks (how fast is X), the
/// performance observatory (how is the vault doing), and, next, retrieval
/// quality.
///
/// Follows the DashboardGroupViews pattern: a segmented picker over EXISTING
/// inline views hosted unchanged, with the selected section owned by
/// ContentView so a menu deep link can land on a pane and the pane survives
/// leaving and returning to the tab.
struct TestingView: View {
    enum Section: String, CaseIterable, Identifiable {
        case validate = "Validate"
        case benchmarks = "Benchmarks"
        case performance = "Performance"
        case quality = "Quality"
        var id: String { rawValue }
    }

    @Binding var section: Section
    /// The Benchmarks pane's single-flight claim, owned by ContentView so it
    /// survives this tab (and the pane) being torn down mid-run.
    var benchRun: BenchRunModel

    var body: some View {
        VStack(spacing: 0) {
            Picker("", selection: $section) {
                ForEach(Section.allCases) { Text($0.rawValue).tag($0) }
            }
            .pickerStyle(.segmented)
            .labelsHidden()
            .padding([.horizontal, .top])
            .padding(.bottom, 8)

            switch section {
            case .validate:
                // The Models tab's vendor + Validate mechanics, hosted in
                // validate-only mode (one implementation, two hosts — the
                // same dual-host idiom as SettingsView(isInline:)).
                SimpleModelsView(validateOnly: true)
            case .benchmarks:
                TestingBenchmarksView(benchRun: benchRun)
            case .performance:
                MetricsView(isPresented: .constant(true), isInline: true)
            case .quality:
                TestingQualityPlaceholder()
            }
        }
    }
}

/// Quality arrives with the eval integration (the retrieval scorecard and the
/// answer jury). The placeholder keeps the section visible so the tab's shape
/// is stable when it lands.
struct TestingQualityPlaceholder: View {
    var body: some View {
        VStack(spacing: 8) {
            Image(systemName: "checkmark.seal")
                .font(.system(size: 32))
                .foregroundStyle(.secondary)
            Text("Quality measurement arrives with the eval integration")
                .font(.headline)
            Text("The retrieval scorecard and answer jury land here next. Until then, run `2nb eval` in a terminal for Recall@10 / MRR@10 over your own notes.")
                .font(.caption)
                .foregroundStyle(.secondary)
                .multilineTextAlignment(.center)
                .padding(.horizontal, 40)
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
    }
}

// MARK: - Benchmarks pane

/// Benchmarks v1: run one model x probe, run the favorites battery, and the
/// recorded history with a model facet. Tables only — charts are a follow-up
/// so the consolidation doesn't gate on a new dependency.
struct TestingBenchmarksView: View {
    @Environment(AppState.self) var appState

    /// Shared single-flight claim for bench runs (see BenchRunModel): owned
    /// by ContentView, NOT view-local, so leaving and re-entering this pane
    /// while a run's Task is still streaming cannot start a second
    /// concurrent `models bench`.
    var benchRun: BenchRunModel

    @State private var models: [CatalogModelInfo] = []
    @State private var favorites: [BenchFavoriteInfo] = []
    @State private var aiStatus: AIStatusInfo?
    @State private var selectedModelID = ""
    @State private var probe = "generate"
    @State private var events: [BenchmarkEvent] = []
    @State private var history: [BenchRunInfo] = []
    @State private var historyLoaded = false
    @State private var historyFacet = ""
    @State private var loading = true
    @State private var errorMessage: String?

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 20) {
                if appState.vault == nil {
                    Text("Bind an Obsidian vault to run benchmarks.")
                        .foregroundStyle(.secondary)
                } else {
                    runSection
                    if !events.isEmpty || benchRun.running {
                        eventsSection
                    }
                    historySection
                }
            }
            .padding()
            .frame(maxWidth: .infinity, alignment: .leading)
        }
        .task { await reload() }
        .alert("Benchmark failed", isPresented: Binding(
            get: { errorMessage != nil },
            set: { if !$0 { errorMessage = nil } }
        )) {
            Button("OK", role: .cancel) { errorMessage = nil }
        } message: {
            Text(errorMessage ?? "")
        }
    }

    private var runSection: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack {
                Text("Run")
                    .font(.subheadline.bold())
                    .textCase(.uppercase)
                    .foregroundStyle(.secondary)
                Spacer()
                if loading || benchRun.running { ProgressView().controlSize(.small) }
            }
            HStack(spacing: 8) {
                Picker("Model", selection: Binding(
                    get: { selectedModelID },
                    set: { newID in
                        selectedModelID = newID
                        alignProbeToSelection()
                    }
                )) {
                    if models.isEmpty {
                        Text("No models").tag("")
                    }
                    ForEach(models) { m in
                        Text("\(WorkingModelPresentation.displayName(m)) (\(m.modelType))")
                            .tag(m.modelID)
                    }
                }
                .labelsHidden()
                .frame(maxWidth: 340)
                Picker("Probe", selection: $probe) {
                    ForEach(probeOptions, id: \.self) { p in
                        Text(BenchProbes.label(p)).tag(p)
                    }
                }
                .labelsHidden()
                .fixedSize()
                Button("Run") { Task { await runOne() } }
                    .disabled(benchRun.running || selectedModelID.isEmpty)
            }
            Text(BenchProbes.costProbe(probe) == nil
                 ? "This probe scores locally — no API calls, no cost."
                 : "Makes real API calls; a cost confirm shows the estimate first.")
                .font(.caption)
                .foregroundStyle(.secondary)
            HStack(spacing: 8) {
                Button("Run favorites (full battery)") { Task { await runFavorites() } }
                    .disabled(benchRun.running)
                Text(favoritesCaption)
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
        }
    }

    private var eventsSection: some View {
        VStack(alignment: .leading, spacing: 4) {
            Text("Live run")
                .font(.subheadline.bold())
                .textCase(.uppercase)
                .foregroundStyle(.secondary)
            ForEach(events.suffix(12)) { event in
                Text(BenchProbes.eventLine(event))
                    .font(.caption.monospaced())
                    .foregroundStyle(event.result?.ok == false ? .red : .secondary)
                    .lineLimit(2)
            }
        }
    }

    private var historySection: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack {
                Text("History")
                    .font(.subheadline.bold())
                    .textCase(.uppercase)
                    .foregroundStyle(.secondary)
                Spacer()
                Picker("Model", selection: $historyFacet) {
                    Text("All models").tag("")
                    ForEach(historyModelIDs, id: \.self) { Text($0).tag($0) }
                }
                .labelsHidden()
                .frame(maxWidth: 320)
                Button {
                    Task { await loadHistory() }
                } label: {
                    Image(systemName: "arrow.clockwise")
                }
                .buttonStyle(.plain)
            }
            let rows = history.filter { historyFacet.isEmpty || $0.modelID == historyFacet }
            if rows.isEmpty {
                Text(historyLoaded ? "No benchmark runs recorded yet." : "Loading…")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            } else {
                ForEach(rows.prefix(50)) { run in
                    historyRow(run)
                }
            }
        }
    }

    private func historyRow(_ run: BenchRunInfo) -> some View {
        HStack(spacing: 10) {
            Text(Formatters.relativeDate(run.timestamp))
                .font(.caption)
                .foregroundStyle(.tertiary)
                .frame(width: 78, alignment: .leading)
            Text(run.modelID)
                .font(.caption.monospaced())
                .lineLimit(1)
                .truncationMode(.middle)
                .frame(maxWidth: 260, alignment: .leading)
            Text(run.probe)
                .font(.caption)
                .frame(width: 64, alignment: .leading)
            Text(Formatters.duration(Int(run.latencyMs)))
                .font(.caption.monospacedDigit())
                .frame(width: 60, alignment: .leading)
            Text(run.ok ? "OK" : "FAIL")
                .font(.caption2.bold())
                .foregroundStyle(run.ok ? Color.secondary : Color.red)
            if let detail = run.detail, !detail.isEmpty {
                Text(detail)
                    .font(.caption2)
                    .foregroundStyle(.tertiary)
                    .lineLimit(1)
            }
            Spacer()
        }
        .padding(.vertical, 1)
    }

    // MARK: State derivation

    private var probeOptions: [String] {
        BenchProbes.options(forModelType: selectedModelType)
    }

    private var selectedModelType: String {
        models.first(where: { $0.modelID == selectedModelID })?.modelType ?? "generation"
    }

    private var historyModelIDs: [String] {
        Array(Set(history.map(\.modelID))).sorted()
    }

    private var favoritesCaption: String {
        if favorites.isEmpty {
            return "No favorites yet — runs the active models. Add one: 2nb models bench fav <model-id>"
        }
        return favorites.count == 1 ? "1 favorite" : "\(favorites.count) favorites"
    }

    /// Keeps the probe valid for the selected model's type (an embedding
    /// model cannot run the generate/rag probes and vice versa).
    private func alignProbeToSelection() {
        let options = probeOptions
        if !options.contains(probe) {
            probe = options.first ?? "generate"
        }
    }

    // MARK: Actions

    private func reload() async {
        guard appState.vault != nil else {
            loading = false
            return
        }
        loading = true
        defer { loading = false }
        do {
            async let modelsTask = appState.fetchModelsCatalog(discover: false)
            async let statusTask = appState.fetchAIStatus()
            async let favsTask = appState.fetchBenchFavorites()
            models = BenchProbes.benchableModels(try await modelsTask)
            aiStatus = try? await statusTask
            favorites = (try? await favsTask) ?? []
            if selectedModelID.isEmpty || !models.contains(where: { $0.modelID == selectedModelID }) {
                selectedModelID = defaultSelection()
            }
            alignProbeToSelection()
        } catch {
            errorMessage = error.localizedDescription
        }
        await loadHistory()
    }

    /// The active generation model when it is benchable, else the first row.
    private func defaultSelection() -> String {
        if let gen = aiStatus?.genModel, models.contains(where: { $0.modelID == gen }) {
            return gen
        }
        return models.first?.modelID ?? ""
    }

    private func loadHistory() async {
        history = (try? await appState.fetchBenchHistory(limit: 100)) ?? []
        historyLoaded = true
    }

    private func runOne() async {
        // Claim the single-flight slot BEFORE the first await (the cost preview
        // shells the CLI): a check-then-set across an await is the exact race
        // VerifyRunModel closes, and a second click during the confirm dialog
        // would start a second run. The claim lives in the shared BenchRunModel
        // so it also holds across this pane being torn down and recreated.
        guard let model = models.first(where: { $0.modelID == selectedModelID }) else { return }
        guard benchRun.beginRun() else { return }
        defer { benchRun.endRun() }
        // Local probes (retrieval scores stored embeddings, search is BM25
        // over the index) bill nothing, so they skip the spend confirm.
        if let costProbe = BenchProbes.costProbe(probe) {
            guard await confirmPaidOperation(
                appState: appState,
                modelIDs: [model.modelID],
                probe: costProbe,
                operation: "Benchmark \(model.modelID)"
            ) else { return }
        }
        events = []
        do {
            try await appState.benchmarkModel(
                modelID: model.modelID,
                provider: model.provider,
                type: model.modelType,
                probe: probe
            ) { event in
                events.append(event)
            }
            await loadHistory()
        } catch {
            errorMessage = error.localizedDescription
        }
    }

    private func runFavorites() async {
        // Same rule as runOne: claim the slot before the cost-preview awaits.
        guard benchRun.beginRun() else { return }
        defer { benchRun.endRun() }
        // Refresh favorites STRICTLY before estimating: the CLI's battery
        // runs the REAL favorites list, so a failed fetch collapsed to []
        // (the reload path's best-effort read) would price the active models
        // while the CLI bills the favorites. A failed fetch aborts with no
        // spend; a genuinely empty list falls back to the actives below,
        // which matches the CLI's own selection for a bare `models bench`.
        do {
            favorites = try await appState.fetchBenchFavorites()
        } catch {
            errorMessage = "Couldn't read the benchmark favorites, so the cost estimate would not match what the battery runs. Nothing was started. (\(error.localizedDescription))"
            return
        }
        // Estimate the PAID part of the full battery per target (search and
        // retrieval bill nothing) so the confirm shows a real number. The
        // targets mirror the CLI's own selection: favorites, else the active
        // embedding + generation pair.
        let targets = favoritesOrActives()
        var estimates: [CostEstimate] = []
        var total = 0.0
        var previewed = !targets.isEmpty
        for group in BenchProbes.batteryPreviewGroups(targets) {
            if let preview = try? await appState.costPreview(modelIDs: group.ids, probe: group.probe) {
                estimates.append(contentsOf: preview.estimates)
                total += preview.totalUSD
            } else {
                previewed = false
            }
        }
        let combined = previewed ? CostPreviewResponse(estimates: estimates, totalUSD: total) : nil
        guard confirmPaidOperation(preview: combined, operation: "Benchmark favorites") else { return }
        events = []
        do {
            try await appState.benchmarkFavorites { event in
                events.append(event)
            }
            await loadHistory()
        } catch {
            errorMessage = error.localizedDescription
        }
    }

    private func favoritesOrActives() -> [(modelID: String, modelType: String)] {
        if !favorites.isEmpty {
            return favorites.map { ($0.modelID, $0.modelType) }
        }
        var out: [(String, String)] = []
        if let status = aiStatus {
            if !status.embeddingModel.isEmpty { out.append((status.embeddingModel, "embedding")) }
            if !status.genModel.isEmpty { out.append((status.genModel, "generation")) }
        }
        return out
    }
}

/// Probe vocabulary for the Benchmarks pane, mirroring the CLI
/// (`models bench --probe embed|generate|retrieval|search|rag` and the
/// no-probe full battery in bench.go runBenchProbes). Split out of the view
/// so the type gating, cost mapping, and battery estimate are unit-testable.
enum BenchProbes {
    /// Probes that apply to a model type: an embedding model cannot run the
    /// generation-side probes and vice versa (the CLI's full battery makes
    /// the same split).
    static func options(forModelType type: String) -> [String] {
        type == "embedding" ? ["embed", "retrieval"] : ["generate", "search", "rag"]
    }

    static func label(_ probe: String) -> String {
        switch probe {
        case "embed": return "Embed"
        case "generate": return "Generate"
        case "retrieval": return "Retrieval"
        case "search": return "Search"
        case "rag": return "RAG"
        default: return probe
        }
    }

    /// The cost-preview probe kind for a bench probe, or nil for the
    /// zero-API probes (retrieval scores stored embeddings locally; search
    /// is BM25 over the index) that need no spend confirm.
    static func costProbe(_ probe: String) -> String? {
        switch probe {
        case "embed": return "bench_embed"
        case "generate": return "bench_gen"
        case "rag": return "bench_rag"
        default: return nil
        }
    }

    /// Models the run-one picker offers: rerank models have no bench probes
    /// and statically incompatible entries cannot be invoked. Embeddings
    /// first (mirroring the catalog's ordering rationale), then by name.
    static func benchableModels(_ models: [CatalogModelInfo]) -> [CatalogModelInfo] {
        models
            .filter { $0.modelType != "rerank" && $0.compatible != false }
            .sorted {
                if $0.modelType != $1.modelType { return $0.modelType < $1.modelType }
                return $0.modelID < $1.modelID
            }
    }

    /// (cost-probe kind, model IDs) groups covering the PAID probes of a
    /// favorites full battery: embedding targets bill one embed probe;
    /// generation targets bill generate + rag (search and retrieval are
    /// local). This is what the "Run favorites" confirm estimates.
    static func batteryPreviewGroups(_ targets: [(modelID: String, modelType: String)]) -> [(probe: String, ids: [String])] {
        var groups: [String: [String]] = [:]
        for target in targets {
            let kinds = target.modelType == "embedding" ? ["bench_embed"] : ["bench_gen", "bench_rag"]
            for kind in kinds {
                groups[kind, default: []].append(target.modelID)
            }
        }
        return groups.sorted { $0.key < $1.key }.map { ($0.key, $0.value) }
    }

    /// One display line per streamed bench event (shared with the model
    /// catalog picker's live feed).
    static func eventLine(_ event: BenchmarkEvent) -> String {
        if let result = event.result {
            let status = result.skipped == true ? "SKIP" : (result.ok ? "PASS" : "FAIL")
            let detail = result.detail.map { " \($0)" } ?? ""
            return "\(result.probe) \(status) \(result.latencyMs)ms\(detail)"
        }
        if event.event == "model_start", let id = event.modelID {
            return "→ \(id)"
        }
        return event.message ?? event.event
    }
}
