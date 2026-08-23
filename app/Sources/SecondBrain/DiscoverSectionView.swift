import SwiftUI

/// The Testing tab's Discover card: per-source discovery-cache ages, a
/// Refresh that re-walks the vendor planes live (free, a control-plane
/// listing with no model invokes, but seconds, so it shows progress), the NEW
/// list with one-click Add and Add + Validate, and the GONE list rendered
/// informationally.
///
/// Hosted inside SimpleModelsView's validateOnly branch so the Validate pane
/// keeps a single scroller. Renders from AppState's shared latest report and
/// session diff (`DiscoverDiffSession`), so a discover run triggered by the
/// Models tab's nudge probe cannot eat the one-shot server-side diff before
/// this card shows it.
struct DiscoverSectionView: View {
    @Environment(AppState.self) var appState

    /// Single-flight for every discover CLI run this card starts (load,
    /// Refresh, Add, Add + Validate), with pending-rerun coalescing for the
    /// reload path (a load requested mid-run re-runs once it finishes).
    @State private var running = false
    @State private var loadPending = false
    /// True while the in-flight run is a `--refresh` live walk, which is the
    /// one slow case worth narrating.
    @State private var refreshing = false
    /// True while an Add + Validate probe is in flight: the patient probe
    /// deadlines make a working cold model look hung without the hint.
    @State private var validating = false
    @State private var errorMessage: String?
    /// Feedback from the last Add / Add + Validate ("Added x", "x: FAIL
    /// (bad_credentials)").
    @State private var actionLines: [String] = []

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            header
            if validating {
                ColdStartHint()
            }
            if appState.modelsDiscoverSupported == false {
                Text("Discovery needs a newer 2nb CLI (`models discover`). Update the CLI to see per-source cache ages and what is newly listed.")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            } else if let report = appState.latestDiscoverReport {
                sourcesSection(report)
                newSection(report)
                goneSection
            } else if running {
                Text("Reading the discovery listings…")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
            ForEach(actionLines, id: \.self) { line in
                Text(line)
                    .font(.caption.monospaced())
                    .foregroundStyle(.secondary)
            }
            if let errorMessage {
                Text(errorMessage)
                    .font(.caption)
                    .foregroundStyle(.red)
            }
        }
        .task { await load(refresh: false) }
    }

    private var header: some View {
        HStack {
            Text("Discover")
                .font(.subheadline.bold())
                .textCase(.uppercase)
                .foregroundStyle(.secondary)
            Spacer()
            if running {
                ProgressView().controlSize(.small)
                if refreshing {
                    Text("Walking the vendor planes…")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
            }
            Button("Refresh") { Task { await load(refresh: true) } }
                .controlSize(.small)
                .disabled(running || appState.modelsDiscoverSupported == false)
        }
    }

    private func sourcesSection(_ report: DiscoverReportInfo) -> some View {
        VStack(alignment: .leading, spacing: 2) {
            ForEach(report.sources) { source in
                Text(DiscoverPresentation.sourceLine(source))
                    .font(.caption.monospaced())
                    .foregroundStyle(source.stale ? .orange : .secondary)
            }
            Text("Refresh re-walks the listings live: a free catalog read, no model is invoked.")
                .font(.caption)
                .foregroundStyle(.tertiary)
        }
    }

    @ViewBuilder
    private func newSection(_ report: DiscoverReportInfo) -> some View {
        let rows = DiscoverDiffSession.newRows(report: report, state: appState.discoverDiffSession)
        if rows.isEmpty {
            Text("No new models since the last check.")
                .font(.caption)
                .foregroundStyle(.secondary)
        } else {
            VStack(alignment: .leading, spacing: 4) {
                Text(rows.count == 1 ? "1 newly listed model" : "\(rows.count) newly listed models")
                    .font(.callout.bold())
                ForEach(rows) { model in
                    newRow(model)
                }
                Text("Add records the model with its routing (free); Add + Validate also probes it once, after a cost confirm.")
                    .font(.caption)
                    .foregroundStyle(.tertiary)
            }
        }
    }

    private func newRow(_ model: CatalogModelInfo) -> some View {
        HStack(spacing: 8) {
            Text(model.modelID)
                .font(.caption.monospaced())
                .lineLimit(1)
                .truncationMode(.middle)
            Text(DiscoverPresentation.routeLabel(model))
                .font(.caption2)
                .foregroundStyle(.secondary)
                .padding(.horizontal, 4)
                .padding(.vertical, 1)
                .background(Color.secondary.opacity(0.12), in: RoundedRectangle(cornerRadius: 3))
            Spacer()
            Button("Add") { Task { await add(model, validate: false) } }
                .controlSize(.small)
                .disabled(running)
            // Only probeable rows offer the validate leg: the CLI's verify
            // path skips rerank/incompatible entries, and because --add
            // persists BEFORE --validate, a guaranteed refusal would leave
            // the add on disk with this run's envelope never emitted.
            if model.modelType != "rerank" && model.compatible != false {
                Button("Add + Validate") { Task { await add(model, validate: true) } }
                    .controlSize(.small)
                    .disabled(running)
            }
        }
    }

    @ViewBuilder
    private var goneSection: some View {
        let gone = DiscoverDiffSession.goneRows(appState.discoverDiffSession)
        if !gone.isEmpty {
            VStack(alignment: .leading, spacing: 2) {
                Text("Gone from discovery since the last check")
                    .font(.caption.bold())
                    .foregroundStyle(.secondary)
                ForEach(gone, id: \.self) { key in
                    Text(DiscoverPresentation.goneDisplay(key))
                        .font(.caption.monospaced())
                        .foregroundStyle(.tertiary)
                }
            }
        }
    }

    // MARK: Actions

    /// Loads (or refreshes) the discover report. Single-flight per instance
    /// with pending-rerun coalescing, the same pattern the settings tab
    /// views use: a load requested while one is in flight re-runs once it
    /// finishes rather than being dropped.
    private func load(refresh: Bool) async {
        // A settled pre-discover verdict is final for this app run: nothing
        // to load, and re-probing on every mount would just re-list the
        // catalog. (The verdict is only ever set on definitive evidence;
        // transient failures leave it unsettled and retry here.)
        guard appState.modelsDiscoverSupported != false else { return }
        if running { loadPending = true; return }
        running = true
        refreshing = refresh
        defer {
            running = false
            refreshing = false
            if loadPending { loadPending = false; Task { await load(refresh: false) } }
        }
        do {
            _ = try await appState.runModelsDiscover(refresh: refresh)
            errorMessage = nil
        } catch {
            errorMessage = error.localizedDescription
        }
    }

    /// One-click Add (free: persists the row with its routing) or Add +
    /// Validate (cost-previews the probe, confirms via PaidOperationConfirm,
    /// then adds and probes under the confirmed cap). Both re-render from
    /// the run's own envelope, so the row leaves the NEW list on success.
    private func add(_ model: CatalogModelInfo, validate: Bool) async {
        guard !running else { return }
        running = true
        validating = validate
        defer {
            running = false
            validating = false
            if loadPending { loadPending = false; Task { await load(refresh: false) } }
        }
        var cap: Double?
        if validate {
            // The preview resolves the id against the discovered pool too
            // (costPreviewArgs carries --discover), so a mantle-plane
            // discovery with no catalog entry yet is priced, not $0.
            let preview = try? await appState.costPreview(modelIDs: [model.modelID], probe: "test")
            guard confirmPaidOperation(preview: preview, operation: "Validate \(model.modelID)") else { return }
            cap = VerifyFlow.costCap(preview: preview)
        }
        do {
            // Pass the provider-qualified form the CLI now accepts, so the
            // add can never resolve a DIFFERENT provider's same-id row than
            // the one clicked (bare ids are refused when ambiguous).
            let key = DiscoveryNudge.modelKey(provider: model.provider, modelID: model.modelID)
            let outcome = try await appState.runModelsDiscover(
                add: [key],
                addKeys: [key],
                validate: validate,
                costCap: cap
            )
            errorMessage = nil
            switch outcome {
            case .report(let report):
                var lines: [String] = []
                for id in report.added ?? [] {
                    lines.append("Added \(id) to the catalog (unverified until a probe passes)")
                }
                lines.append(contentsOf: DiscoverPresentation.validateOutcomeLines(report.results ?? []))
                actionLines = lines
            case .unsupported:
                break
            }
        } catch {
            errorMessage = error.localizedDescription
            // The CLI persists --add BEFORE --validate and emits JSON only
            // on full success, so a validate refusal can leave the model in
            // the catalog while afterAdd and the catalog-version bump never
            // ran (the row would stay NEW and a retry would hit
            // already-in-catalog). Reconcile from reality with a free cached
            // re-listing: a persisted add has graduated out of the pool, so
            // the session pool-intersection clears its NEW badge, and the
            // version bump refreshes the pickers. If the add never
            // persisted, the pool still lists it and the badge stays.
            _ = try? await appState.runModelsDiscover()
            appState.modelsCatalogVersion += 1
        }
    }
}
