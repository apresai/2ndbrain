import SwiftUI

/// ModelWizardView lets users discover, cost-preview, test, and save
/// AI models in one flow. Uses the CLI primitives landed in phases 1–5
/// rather than the monolithic `2nb models wizard` subprocess so each
/// step can be driven natively and canceled independently.
struct ModelWizardView: View {
    @Environment(AppState.self) var appState
    let onClose: () -> Void

    enum Phase {
        case loading
        case selecting
        case confirming
        case testing
        case done
    }

    @State private var phase: Phase = .loading
    @State private var models: [CatalogModelInfo] = []
    @State private var selected: Set<String> = []
    @State private var estimate: CostPreviewResponse?
    @State private var testResults: [String: TestOutcome] = [:]
    @State private var errorMessage: String?
    @State private var saveScope: String = "vault"

    /// Per-model testing state. Kept separate from CatalogModelInfo so
    /// the view can re-render rows as each test finishes without
    /// re-fetching the catalog.
    struct TestOutcome: Equatable {
        var running: Bool
        var ok: Bool?
        var latency: String?
        var detail: String?
    }

    var body: some View {
        VStack(spacing: 0) {
            header
            Divider()
            content
                .frame(maxWidth: .infinity, maxHeight: .infinity)
            Divider()
            footer
        }
        .frame(width: 720, height: 560)
        .task { await loadModels() }
    }

    // MARK: - Subviews

    private var header: some View {
        VStack(alignment: .leading, spacing: 4) {
            Text("Model Wizard")
                .font(.title2.bold())
            Text(subtitle)
                .font(.callout)
                .foregroundStyle(.secondary)
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .padding()
    }

    private var subtitle: String {
        switch phase {
        case .loading: return "Discovering models from your configured providers…"
        case .selecting: return "Pick the models you want to verify."
        case .confirming: return "Review the estimated cost before running tests."
        case .testing: return "Testing each selected model…"
        case .done: return "Done. Verified models are now available in the model picker."
        }
    }

    @ViewBuilder
    private var content: some View {
        switch phase {
        case .loading:
            ProgressView().padding()
        case .selecting, .confirming, .testing, .done:
            modelListView
        }
    }

    private var modelListView: some View {
        List {
            ForEach(groupedProviders(), id: \.self) { provider in
                Section(provider) {
                    ForEach(models.filter { $0.provider == provider }) { model in
                        row(for: model)
                    }
                }
            }
        }
        .listStyle(.inset)
    }

    @ViewBuilder
    private func row(for model: CatalogModelInfo) -> some View {
        let outcome = testResults[model.modelID]
        HStack(spacing: 8) {
            Toggle(isOn: Binding(
                get: { selected.contains(model.modelID) },
                set: { isOn in
                    if isOn { selected.insert(model.modelID) }
                    else { selected.remove(model.modelID) }
                }
            )) {
                EmptyView()
            }
            .labelsHidden()
            .disabled(phase == .testing || phase == .done)

            VStack(alignment: .leading, spacing: 2) {
                HStack(spacing: 6) {
                    Text(model.name.isEmpty ? model.modelID : model.name)
                        .font(.body)
                    if let tier = model.tier, tier == "verified" {
                        Text("verified")
                            .font(.caption2.monospaced())
                            .foregroundStyle(.green)
                    } else if let tier = model.tier, tier == "user_verified" {
                        Text("user-verified")
                            .font(.caption2.monospaced())
                            .foregroundStyle(.blue)
                    }
                }
                Text(metaLine(for: model))
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }

            Spacer()

            if let outcome {
                outcomeIndicator(outcome)
            }
        }
        .contentShape(Rectangle())
        .onTapGesture {
            guard phase == .selecting else { return }
            if selected.contains(model.modelID) {
                selected.remove(model.modelID)
            } else {
                selected.insert(model.modelID)
            }
        }
    }

    @ViewBuilder
    private func outcomeIndicator(_ o: TestOutcome) -> some View {
        if o.running {
            ProgressView().controlSize(.small)
        } else if let ok = o.ok {
            if ok {
                HStack(spacing: 4) {
                    Image(systemName: "checkmark.circle.fill").foregroundStyle(.green)
                    if let lat = o.latency { Text(lat).font(.caption.monospacedDigit()) }
                }
            } else {
                HStack(spacing: 4) {
                    Image(systemName: "xmark.circle.fill").foregroundStyle(.red)
                    if let d = o.detail { Text(d).font(.caption).foregroundStyle(.red).lineLimit(1) }
                }
                .help(o.detail ?? "")
            }
        }
    }

    @ViewBuilder
    private var footer: some View {
        HStack {
            if phase == .confirming, let est = estimate {
                VStack(alignment: .leading, spacing: 2) {
                    Text(String(format: "Estimated test cost: $%.6f", est.totalUSD))
                        .font(.callout.monospacedDigit())
                    Text("\(selected.count) model(s) selected")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
            } else if phase == .done {
                Text(summaryLine())
                    .font(.callout)
            } else if phase == .selecting {
                Text("\(selected.count) selected")
                    .font(.callout)
                    .foregroundStyle(.secondary)
            }

            Spacer()

            if phase == .selecting {
                Picker("Save to", selection: $saveScope) {
                    Text("This vault").tag("vault")
                    Text("Global").tag("global")
                }
                .pickerStyle(.menu)
                .fixedSize()
            }

            Button("Cancel", role: .cancel) { onClose() }

            switch phase {
            case .selecting:
                Button("Preview cost") {
                    Task { await previewCost() }
                }
                .keyboardShortcut(.defaultAction)
                .disabled(selected.isEmpty)
            case .confirming:
                Button("Run tests") {
                    Task { await runTests() }
                }
                .keyboardShortcut(.defaultAction)
            case .testing:
                Button("Run tests") {}.disabled(true)
            case .done:
                Button("Close") { onClose() }
                    .keyboardShortcut(.defaultAction)
            case .loading:
                EmptyView()
            }
        }
        .padding()
        .alert(
            "Wizard error",
            isPresented: Binding(
                get: { errorMessage != nil },
                set: { if !$0 { errorMessage = nil } }
            ),
            actions: { Button("OK") { errorMessage = nil } },
            message: { Text(errorMessage ?? "") }
        )
    }

    // MARK: - Helpers

    private func groupedProviders() -> [String] {
        var seen = Set<String>()
        var order: [String] = []
        for m in models where !seen.contains(m.provider) {
            seen.insert(m.provider)
            order.append(m.provider)
        }
        return order
    }

    private func metaLine(for m: CatalogModelInfo) -> String {
        var parts: [String] = [m.modelType]
        if let d = m.dimensions, d > 0 { parts.append("\(d)-dim") }
        if let c = m.contextLen, c > 0 { parts.append("\(c/1000)k ctx") }
        if let strat = m.invokeStrategy, !strat.isEmpty { parts.append(strat) }
        return parts.joined(separator: " · ")
    }

    private func summaryLine() -> String {
        let passed = testResults.values.filter { $0.ok == true }.count
        let failed = testResults.values.filter { $0.ok == false }.count
        return "Passed: \(passed)   Failed: \(failed)   Saved to \(saveScope) catalog"
    }

    // MARK: - Actions

    private func loadModels() async {
        phase = .loading
        do {
            let fetched = try await appState.fetchModelsForWizard()
            models = fetched
            // Pre-check verified models only, to give users a reasonable
            // starting point without surprising them with unverified picks.
            selected = Set(fetched.filter { $0.tier == "verified" }.map { $0.modelID })
            phase = .selecting
        } catch {
            errorMessage = "Failed to load models: \(error.localizedDescription)"
            phase = .selecting
        }
    }

    private func previewCost() async {
        let ids = Array(selected)
        do {
            estimate = try await appState.costPreview(modelIDs: ids, probe: "test")
            phase = .confirming
        } catch {
            errorMessage = "Cost preview failed: \(error.localizedDescription)"
        }
    }

    private func runTests() async {
        phase = .testing
        let toTest = models.filter { selected.contains($0.modelID) }
        // Seed each row as "running" so the UI shows spinners immediately
        // rather than waiting for the first test to return.
        for m in toTest {
            testResults[m.modelID] = TestOutcome(running: true)
        }
        for m in toTest {
            do {
                let result = try await appState.testAndSave(
                    modelID: m.modelID,
                    provider: m.provider,
                    type: m.modelType,
                    scope: saveScope
                )
                testResults[m.modelID] = TestOutcome(
                    running: false,
                    ok: result.ok,
                    latency: result.latency,
                    detail: result.detail
                )
            } catch {
                testResults[m.modelID] = TestOutcome(
                    running: false,
                    ok: false,
                    latency: nil,
                    detail: error.localizedDescription
                )
            }
        }
        phase = .done
    }
}
