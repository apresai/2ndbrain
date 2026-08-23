import SwiftUI
#if canImport(AppKit)
import AppKit
#endif

/// Testing & Benchmarking: everything measurable in one sidebar destination —
/// validation (can this account call X), benchmarks (how fast is X), the
/// performance observatory (how is the vault doing), and retrieval/answer
/// quality (how well does search and RAG actually work).
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
    /// The single-flight claim for heavy measured runs (bench batteries,
    /// eval runs, the embed probe), owned by ContentView so it survives this
    /// tab (and any pane) being torn down mid-run.
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
                TestingQualityView(benchRun: benchRun)
            }
        }
    }
}

// MARK: - Benchmarks pane

/// Benchmarks: run one model x probe, run the favorites battery, manage the
/// favorites list, the models x probes compare matrix, the embed-concurrency
/// throughput curve, and the recorded history with a model facet. Tables
/// only; charts are a follow-up so nothing gates on a new dependency.
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
    @State private var compare: [BenchRunInfo] = []
    @State private var compareLoaded = false
    @State private var history: [BenchRunInfo] = []
    @State private var historyLoaded = false
    @State private var historyFacet = ""
    @State private var embedProbeResult: EmbedProbeInfo?
    @State private var probingEmbed = false
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
                    favoritesSection
                    compareSection
                    embedProbeSection
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
            Text("Models are ranked by measured evidence (bench quality, then probe results).")
                .font(.caption)
                .foregroundStyle(.tertiary)
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

    /// Manage the favorites list the full battery runs: the selected model
    /// can be starred in, and each favorite removed. bench.db is the store
    /// (`models bench fav/unfav/favs`).
    private var favoritesSection: some View {
        VStack(alignment: .leading, spacing: 8) {
            Text("Favorites")
                .font(.subheadline.bold())
                .textCase(.uppercase)
                .foregroundStyle(.secondary)
            HStack(spacing: 8) {
                Button("Add selected model") { Task { await addFavorite() } }
                    .disabled(selectedModelID.isEmpty || selectedIsFavorite)
                if selectedIsFavorite {
                    Text("Already a favorite.")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
            }
            if favorites.isEmpty {
                Text("No favorites yet. The full battery falls back to the active models.")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            } else {
                ForEach(favorites) { fav in
                    HStack(spacing: 10) {
                        Image(systemName: "star.fill")
                            .font(.caption)
                            .foregroundStyle(.yellow)
                        Text(fav.modelID)
                            .font(.caption.monospaced())
                            .lineLimit(1)
                            .truncationMode(.middle)
                        Text(fav.modelType)
                            .font(.caption)
                            .foregroundStyle(.secondary)
                        Button("Remove") { Task { await removeFavorite(fav) } }
                            .controlSize(.small)
                        Spacer()
                    }
                }
            }
        }
    }

    /// Models x probes matrix over the latest run per pair (`models bench
    /// compare --json`), the side-by-side the CLI renders per probe group.
    private var compareSection: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack {
                Text("Compare")
                    .font(.subheadline.bold())
                    .textCase(.uppercase)
                    .foregroundStyle(.secondary)
                Spacer()
                Button {
                    Task { await loadCompare() }
                } label: {
                    Image(systemName: "arrow.clockwise")
                }
                .buttonStyle(.plain)
            }
            let rows = BenchCompareMatrix.rows(compare)
            if rows.isEmpty {
                Text(compareLoaded
                     ? "No benchmark runs to compare yet. Run a probe above."
                     : "Loading…")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            } else {
                let columns = BenchCompareMatrix.columns(compare)
                Grid(alignment: .leading, horizontalSpacing: 14, verticalSpacing: 3) {
                    GridRow {
                        Text("MODEL")
                            .font(.caption2.bold())
                            .foregroundStyle(.tertiary)
                        ForEach(columns, id: \.self) { probe in
                            Text(BenchProbes.label(probe).uppercased())
                                .font(.caption2.bold())
                                .foregroundStyle(.tertiary)
                        }
                    }
                    ForEach(rows) { row in
                        GridRow {
                            Text(row.modelID)
                                .font(.caption.monospaced())
                                .lineLimit(1)
                                .truncationMode(.middle)
                                .frame(maxWidth: 280, alignment: .leading)
                            ForEach(columns, id: \.self) { probe in
                                let cell = row.cells[probe]
                                Text(BenchCompareMatrix.cellText(cell))
                                    .font(.caption.monospacedDigit())
                                    .foregroundStyle(cell?.ok == false ? Color.red : Color.secondary)
                                    .help(cell?.detail ?? "")
                            }
                        }
                    }
                }
            }
        }
    }

    /// The embed-concurrency throughput curve from `2nb ai embed-probe`:
    /// per-level docs/sec + errors, with the recommended setting. The probe
    /// payload always carried the levels; this is where they render.
    private var embedProbeSection: some View {
        VStack(alignment: .leading, spacing: 8) {
            Text("Embed throughput")
                .font(.subheadline.bold())
                .textCase(.uppercase)
                .foregroundStyle(.secondary)
            HStack(spacing: 8) {
                Button(probingEmbed ? "Probing…" : "Find safe concurrency") {
                    Task { await runEmbedProbe() }
                }
                .disabled(benchRun.running)
                Text("paid: ramps real embedding calls to find your account's ceiling (takes minutes)")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
            if let result = embedProbeResult {
                let maxThroughput = result.levels.compactMap(\.textsPerSec).max() ?? 0
                VStack(alignment: .leading, spacing: 3) {
                    ForEach(result.levels) { level in
                        HStack(spacing: 8) {
                            Text("x\(level.concurrency)")
                                .font(.caption.monospacedDigit())
                                .frame(width: 36, alignment: .leading)
                            RoundedRectangle(cornerRadius: 2)
                                .fill((level.errors ?? 0) > 0 ? Color.orange : Color.accentColor)
                                .frame(width: barWidth(level.textsPerSec, max: maxThroughput), height: 8)
                            Text(String(format: "%.1f texts/sec", level.textsPerSec ?? 0))
                                .font(.caption.monospacedDigit())
                                .foregroundStyle(.secondary)
                            if let errors = level.errors, errors > 0 {
                                Text("\(errors) errors")
                                    .font(.caption)
                                    .foregroundStyle(.orange)
                            }
                            if level.concurrency == result.recommended {
                                Text("recommended")
                                    .font(.caption2.bold())
                                    .foregroundStyle(.green)
                            }
                            Spacer()
                        }
                    }
                    Text("Apply with: 2nb config set ai.embed_concurrency \(result.recommended) (also in Settings → Advanced)")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                        .textSelection(.enabled)
                }
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

    private var selectedIsFavorite: Bool {
        guard let model = models.first(where: { $0.modelID == selectedModelID }) else { return false }
        return favorites.contains { $0.modelID == model.modelID && $0.provider == model.provider }
    }

    private var favoritesCaption: String {
        if favorites.isEmpty {
            return "No favorites yet: runs the active models. Add one in Favorites below."
        }
        return favorites.count == 1 ? "1 favorite" : "\(favorites.count) favorites"
    }

    /// Keeps the probe valid for the selected model's type (an embedding
    /// model cannot run the generate/search/rag probes and vice versa).
    private func alignProbeToSelection() {
        let options = probeOptions
        if !options.contains(probe) {
            probe = options.first ?? "generate"
        }
    }

    private func barWidth(_ value: Double?, max maxValue: Double) -> CGFloat {
        guard let value, maxValue > 0 else { return 2 }
        return Swift.max(2, CGFloat(value / maxValue) * 160)
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
            // sortBest: the CLI ranks by measured evidence (bench quality,
            // then tested-passing, recommended, tier, latency) and the JSON
            // follows the sort, so the picker's order reflects real data.
            async let modelsTask = appState.fetchModelsCatalog(discover: false, sortBest: true)
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
        await loadCompare()
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

    private func loadCompare() async {
        compare = (try? await appState.fetchBenchCompare()) ?? []
        compareLoaded = true
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
            await loadCompare()
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
            await loadCompare()
        } catch {
            errorMessage = error.localizedDescription
        }
    }

    private func addFavorite() async {
        guard let model = models.first(where: { $0.modelID == selectedModelID }) else { return }
        do {
            try await appState.addBenchFavorite(modelID: model.modelID, provider: model.provider)
            favorites = (try? await appState.fetchBenchFavorites()) ?? favorites
        } catch {
            errorMessage = error.localizedDescription
        }
    }

    private func removeFavorite(_ fav: BenchFavoriteInfo) async {
        do {
            try await appState.removeBenchFavorite(modelID: fav.modelID, provider: fav.provider)
            favorites = (try? await appState.fetchBenchFavorites()) ?? favorites
        } catch {
            errorMessage = error.localizedDescription
        }
    }

    private func runEmbedProbe() async {
        // Not a `models bench`, but it shares the single-flight claim: it is
        // a paid multi-minute run from the same pane, the claim survives the
        // pane being torn down mid-probe, and holding it keeps a bench
        // battery from hammering the provider mid-ramp (which would skew the
        // throughput curve the probe exists to measure).
        guard benchRun.beginRun() else { return }
        defer { benchRun.endRun() }
        // ai embed-probe has no cost-preview probe kind, so the confirm
        // carries this operation's own honest copy (same wording as the
        // Settings → Advanced runner for the same operation).
        #if canImport(AppKit)
        let alert = NSAlert()
        alert.messageText = "Find safe embed concurrency?"
        alert.informativeText = "This ramps REAL embedding calls over a discarded sample of your vault (typically well under a dollar, several minutes). The result is a recommended ai.embed_concurrency for this account."
        alert.addButton(withTitle: "Run probe")
        alert.addButton(withTitle: "Cancel")
        guard alert.runModal() == .alertFirstButtonReturn else { return }
        #endif
        probingEmbed = true
        defer { probingEmbed = false }
        do {
            embedProbeResult = try await appState.embedProbe()
        } catch {
            errorMessage = "Embed probe failed: \(error.localizedDescription)"
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

// MARK: - Quality pane

/// Retrieval + answer quality over the user's OWN notes, via the `2nb eval`
/// family: the retrieval scorecard (Recall@10 / R@1 / MRR@10), the LLM-jury
/// answer grades, and the tuning sweep's suggestions (suggest-only; the CLI
/// never applies them and neither does this pane).
///
/// Cost gating: every run button fetches the CLI's own `--estimate` first,
/// confirms via PaidOperationConfirm, then passes `--yes` with a cost cap
/// derived as 2x estimate + $0.01 (EvalFlow, mirroring VerifyFlow). A vault
/// with no cached QA set sees the one-time generation cost spelled out
/// before any spend.
struct TestingQualityView: View {
    @Environment(AppState.self) var appState

    /// Shares the Testing tab's single-flight claim: eval runs are long,
    /// paid, and survive pane teardown exactly like bench batteries, so one
    /// claim covers both (an eval can't start mid-battery and vice versa).
    var benchRun: BenchRunModel

    @State private var estimate: EvalEstimateInfo?
    @State private var estimateLoaded = false
    @State private var scorecard: EvalReportInfo?
    @State private var answers: EvalAnswersInfo?
    @State private var tune: EvalTuneInfo?
    @State private var runningLabel: String?
    @State private var errorMessage: String?

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 20) {
                if appState.vault == nil {
                    Text("Bind an Obsidian vault to measure quality.")
                        .foregroundStyle(.secondary)
                } else {
                    scorecardSection
                    answersSection
                    tuneSection
                }
            }
            .padding()
            .frame(maxWidth: .infinity, alignment: .leading)
        }
        .task { await loadEstimate() }
        .alert("Eval failed", isPresented: Binding(
            get: { errorMessage != nil },
            set: { if !$0 { errorMessage = nil } }
        )) {
            Button("OK", role: .cancel) { errorMessage = nil }
        } message: {
            Text(errorMessage ?? "")
        }
    }

    // MARK: Sections

    private var scorecardSection: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack {
                Text("Retrieval scorecard")
                    .font(.subheadline.bold())
                    .textCase(.uppercase)
                    .foregroundStyle(.secondary)
                Spacer()
                if runningLabel == "scorecard" { ProgressView().controlSize(.small) }
            }
            Text(estimateLoaded ? EvalFlow.qaContext(estimate: estimate) : "Loading cost preview…")
                .font(.caption)
                .foregroundStyle(.secondary)
            HStack(spacing: 8) {
                Button(runningLabel == "scorecard" ? "Scoring…" : "Run scorecard") {
                    Task { await runScorecard() }
                }
                .disabled(benchRun.running)
                Text("How well hybrid search ranks the right note for questions from your own vault.")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
            if let report = scorecard {
                metricsGrid([
                    ("Recall@\(report.k)", percent(report.recallAtK), "the right note reaches the top \(report.k)"),
                    ("R@1", percent(report.recallAt1), "the right note is ranked #1"),
                    ("MRR@\(report.k)", String(format: "%.3f", report.mrrAtK), ""),
                ])
                Text(scorecardContext(report))
                    .font(.caption)
                    .foregroundStyle(.tertiary)
            }
        }
    }

    private var answersSection: some View {
        VStack(alignment: .leading, spacing: 8) {
            Text("Answer quality (LLM jury)")
                .font(.subheadline.bold())
                .textCase(.uppercase)
                .foregroundStyle(.secondary)
            HStack(spacing: 8) {
                Button(runningLabel == "answers" ? "Grading…" : "Grade answers") {
                    Task { await runAnswers() }
                }
                .disabled(benchRun.running)
                Text("Real RAG answers over the QA set, graded 1-5 on correctness, completeness, and grounding. The costly eval.")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
            if let report = answers {
                metricsGrid([
                    ("Correctness", String(format: "%.2f / 5", report.correctness), ""),
                    ("Completeness", String(format: "%.2f / 5", report.completeness), ""),
                    ("Grounding", String(format: "%.2f / 5", report.grounding), ""),
                    ("Composite", String(format: "%.2f / 5", report.composite), ""),
                ])
                Text(answersContext(report))
                    .font(.caption)
                    .foregroundStyle(.tertiary)
            }
        }
    }

    private var tuneSection: some View {
        VStack(alignment: .leading, spacing: 8) {
            Text("Tuning sweep")
                .font(.subheadline.bold())
                .textCase(.uppercase)
                .foregroundStyle(.secondary)
            HStack(spacing: 8) {
                Button(runningLabel == "tune" ? "Sweeping…" : "Run tuning sweep") {
                    Task { await runTune() }
                }
                .disabled(benchRun.running)
                Text("Sweeps threshold and weight combinations locally. Suggest-only: nothing is applied.")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
            if let report = tune {
                metricsGrid([
                    ("Current", String(format: "MRR %.3f", report.current.mrrAtK), tuneEntryLabel(report.current)),
                    ("Best", String(format: "MRR %.3f", report.best.mrrAtK), tuneEntryLabel(report.best)),
                ])
                if report.best.bm25Only == true {
                    Text("The BM25-only baseline won: the semantic channel is hurting ranking on this QA set. Check embeddings are current before changing weights (a diagnosis, not a setting to apply).")
                        .font(.caption)
                        .foregroundStyle(.orange)
                } else if let suggestion = report.suggestion, !suggestion.isEmpty {
                    Text("Best config beats current. Apply it yourself with:")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                    ForEach(suggestion, id: \.self) { line in
                        Text(line)
                            .font(.caption.monospaced())
                            .textSelection(.enabled)
                    }
                } else {
                    Text("Your current config is already within noise of the best swept configuration. Nothing to change.")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
                if report.n < 20 {
                    Text("Caveat: only \(report.n) questions; small sets overfit. Prefer a larger QA set before acting on marginal wins.")
                        .font(.caption)
                        .foregroundStyle(.tertiary)
                }
            }
        }
    }

    // MARK: Rendering helpers

    private func metricsGrid(_ rows: [(String, String, String)]) -> some View {
        Grid(alignment: .leading, horizontalSpacing: 14, verticalSpacing: 3) {
            ForEach(rows, id: \.0) { row in
                GridRow {
                    Text(row.0)
                        .font(.caption)
                        .foregroundStyle(.secondary)
                    Text(row.1)
                        .font(.caption.monospacedDigit().bold())
                    Text(row.2)
                        .font(.caption)
                        .foregroundStyle(.tertiary)
                }
            }
        }
    }

    private func percent(_ value: Double) -> String {
        String(format: "%.0f%%", value * 100)
    }

    private func scorecardContext(_ report: EvalReportInfo) -> String {
        var parts = ["\(report.n) questions"]
        parts.append(report.qaCached ? "cached QA set" : "QA set generated this run")
        parts.append(String(format: "threshold %.2f, bm25 %.1f / vector %.1f",
                            report.config.threshold, report.config.bm25Weight, report.config.vectorWeight))
        parts.append("measured \(Formatters.relativeDate(report.generatedAt))")
        return parts.joined(separator: " · ")
    }

    private func answersContext(_ report: EvalAnswersInfo) -> String {
        var parts = ["\(report.answered) answered, \(report.failed) failed"]
        if report.selfJudged {
            parts.append("self-judged by \(report.judges.first ?? "the active model") (relative signal; biased as an absolute score)")
        } else {
            parts.append("panel: \(report.judges.joined(separator: ", "))")
        }
        parts.append("measured \(Formatters.relativeDate(report.generatedAt))")
        return parts.joined(separator: " · ")
    }

    private func tuneEntryLabel(_ entry: EvalTuneEntryInfo) -> String {
        if entry.bm25Only == true { return "bm25-only baseline" }
        return String(format: "threshold %.2f, bm25 %.1f / vector %.1f",
                      entry.threshold, entry.bm25Weight, entry.vectorWeight)
    }

    // MARK: Actions

    private func loadEstimate() async {
        guard appState.vault != nil else { return }
        estimate = EvalFlow.usable(try? await appState.evalEstimate())
        estimateLoaded = true
    }

    /// Shared run shape: claim the single-flight slot BEFORE the first await
    /// (estimate + confirm both cross awaits), re-estimate strictly before
    /// the confirm so the dialog prices what the run will actually do (the
    /// QA cache state can change under the pane), then run with the derived
    /// cap. A failed estimate degrades to the numberless confirm and the
    /// CLI's own default cap.
    private func runGated(
        label: String,
        subcommand: String?,
        operation: String,
        run: (Double) async throws -> Void
    ) async {
        guard benchRun.beginRun() else { return }
        defer { benchRun.endRun() }
        let fresh = EvalFlow.usable(try? await appState.evalEstimate(subcommand: subcommand))
        if subcommand == nil { estimate = fresh ?? estimate }
        if fresh == nil, !EvalFlow.mayRunWithoutEstimate(subcommand: subcommand) {
            // answers bills above the bare-eval default cap the fallback
            // would pass, and a too-low cap aborts mid-run after partial
            // spend; refuse and let the user retry the estimate instead.
            errorMessage = "Couldn't load the cost estimate for this run; not starting it without one. Try again."
            return
        }
        guard confirmPaidOperation(preview: EvalFlow.confirmPreview(estimate: fresh), operation: operation) else { return }
        runningLabel = label
        defer { runningLabel = nil }
        do {
            try await run(EvalFlow.costCap(estimate: fresh))
            // The run may have just generated the QA set; refresh the
            // cached-state line so the next confirm prices a cached run. A
            // FAILED refresh keeps the prior estimate rather than replacing
            // it with nil ("couldn't load" copy right after a success).
            if let refreshed = EvalFlow.usable(try? await appState.evalEstimate()) {
                estimate = refreshed
            }
            estimateLoaded = true
        } catch {
            errorMessage = error.localizedDescription
        }
    }

    private func runScorecard() async {
        await runGated(label: "scorecard", subcommand: nil, operation: "Run retrieval scorecard") { cap in
            scorecard = try await appState.runEvalScorecard(costCap: cap)
        }
    }

    private func runAnswers() async {
        await runGated(label: "answers", subcommand: "answers", operation: "Grade answers with the LLM jury") { cap in
            answers = try await appState.runEvalAnswers(costCap: cap)
        }
    }

    private func runTune() async {
        await runGated(label: "tune", subcommand: "tune", operation: "Run tuning sweep") { cap in
            tune = try await appState.runEvalTune(costCap: cap)
        }
    }
}

/// Probe vocabulary for the Benchmarks pane, mirroring the CLI
/// (`models bench --probe embed|generate|retrieval|search|rag` and the
/// no-probe full battery in bench.go runBenchProbes). Split out of the view
/// so the type gating, cost mapping, and battery estimate are unit-testable.
enum BenchProbes {
    /// Probes that apply to a model type: an embedding model cannot run the
    /// generation-side probes and vice versa (the CLI's full battery makes
    /// the same split). Shared by the Testing tab and the catalog picker so
    /// the two probe pickers cannot drift.
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
    /// first (mirroring the catalog's ordering rationale), PRESERVING the
    /// incoming order within each type: the picker feeds `models list
    /// --sort best`, so the CLI's measured ranking must survive the split.
    static func benchableModels(_ models: [CatalogModelInfo]) -> [CatalogModelInfo] {
        let usable = models.filter { $0.modelType != "rerank" && $0.compatible != false }
        return usable.filter { $0.modelType == "embedding" }
            + usable.filter { $0.modelType != "embedding" }
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
