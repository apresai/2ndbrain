import SwiftUI
#if canImport(AppKit)
import AppKit
#endif

/// Pure helpers for the Advanced settings section, split out for unit tests.
enum AdvancedConfig {
    /// Maps the threshold-override checkbox + field to the value written to
    /// `ai.similarity_threshold`. Unchecked writes "0", which the CLI treats
    /// as "unset: fall through vault > calibration > model > default".
    static func thresholdWriteValue(overrideOn: Bool, text: String) -> String? {
        guard overrideOn else { return "0" }
        guard let v = Double(text), v > 0, v <= 1 else { return nil }
        return text
    }

    /// Honest result line for a calibration run. The CLI exits 0 but OMITS
    /// `saved_to` when it refuses to save (asymmetric models like Nova-2,
    /// where doc-to-doc calibration overstates the search-time threshold),
    /// and `--porcelain` suppresses its warning, so the save claim must be
    /// gated on the payload, not on exit status.
    static func calibrationMessage(_ info: CalibrationInfo) -> String {
        if let saved = info.savedTo, !saved.isEmpty {
            return String(format: "Calibrated %@: recommended %.2f (p95 %.2f), saved to the %@ catalog.", info.model, info.recommendedThreshold, info.p95 ?? 0, saved)
        }
        return String(format: "Computed %.2f for %@ but did NOT save it: this model embeds queries asymmetrically, so a doc-to-doc calibration overstates the search threshold. The built-in recommendation stays active.", info.recommendedThreshold, info.model)
    }

    /// Flattens `config show --json` output into ordered key-path/value rows
    /// for the read-only viewer. Generic on purpose: no schema coupling, so
    /// new CLI config keys appear without a Swift change.
    static func flatten(_ data: Data) -> [(key: String, value: String)] {
        guard let root = try? JSONSerialization.jsonObject(with: data) else { return [] }
        var rows: [(String, String)] = []
        walk(root, path: "", into: &rows)
        return rows.sorted { $0.0 < $1.0 }
    }

    private static func walk(_ node: Any, path: String, into rows: inout [(String, String)]) {
        switch node {
        case let dict as [String: Any]:
            for (k, v) in dict {
                walk(v, path: path.isEmpty ? k : "\(path).\(k)", into: &rows)
            }
        case let arr as [Any]:
            if arr.isEmpty {
                rows.append((path, "[]"))
            } else {
                for (i, v) in arr.enumerated() {
                    walk(v, path: "\(path)[\(i)]", into: &rows)
                }
            }
        case let s as String:
            rows.append((path, s))
        case let n as NSNumber:
            rows.append((path, n.stringValue))
        case is NSNull:
            rows.append((path, "null"))
        default:
            rows.append((path, String(describing: node)))
        }
    }
}

/// Advanced settings: the previously invisible `ai.*` tuning knobs, the
/// calibration/concurrency tools, and a read-only effective-config viewer.
/// Every write goes through `2nb config set`, so the CLI's validation (and
/// its error text) is the single source of truth; Swift never re-implements
/// the ranges.
struct AIAdvancedSettingsView: View {
    @Environment(AppState.self) var appState
    let aiStatus: AIStatusInfo?
    let models: [CatalogModelInfo]
    let onReload: () async -> Void

    // Knob field state, loaded from the config on appear.
    @State private var thresholdOverrideOn = false
    @State private var thresholdText = ""
    @State private var bm25Weight = ""
    @State private var vectorWeight = ""
    @State private var ragContextBudget = ""
    @State private var ragNoteBudget = ""
    @State private var rerankCandidates = ""
    @State private var embedConcurrency = ""
    @State private var dimensionSelection = 0
    @State private var bedrockProfile = ""
    @State private var bedrockRegion = ""
    @State private var ollamaEndpoint = ""

    @State private var rowErrors: [String: String] = [:]
    @State private var statusText: String?
    @State private var calibrating = false
    @State private var probing = false
    @State private var configRows: [(key: String, value: String)] = []
    @State private var showEffectiveConfig = false

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            retrievalSection
            embeddingSection
            providerSection
            toolsSection
            effectiveConfigSection
            if let statusText {
                Text(statusText).font(.caption).foregroundStyle(.secondary)
            }
        }
        .task { await loadCurrentValues() }
    }

    // MARK: - Sections

    private var retrievalSection: some View {
        VStack(alignment: .leading, spacing: 8) {
            Text("Retrieval tuning").font(.subheadline.bold())
            HStack(spacing: 8) {
                Toggle("Override automatic threshold", isOn: $thresholdOverrideOn)
                    .toggleStyle(.checkbox)
                if thresholdOverrideOn {
                    TextField("0.20", text: $thresholdText)
                        .textFieldStyle(.roundedBorder)
                        .frame(width: 70)
                }
                Button("Save") { Task { await saveThreshold() } }
                    .controlSize(.small)
                Text(effectiveThresholdCaption)
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
            rowError("ai.similarity_threshold")
            knobRow(label: "BM25 weight", key: "ai.bm25_weight", text: $bm25Weight, hint: "keyword channel bias, default 1.0")
            knobRow(label: "Vector weight", key: "ai.vector_weight", text: $vectorWeight, hint: "semantic channel bias, default 1.0")
            knobRow(label: "RAG context budget", key: "ai.rag_context_budget", text: $ragContextBudget, hint: "total runes fed to ask, default 60000")
            knobRow(label: "RAG note budget", key: "ai.rag_note_budget", text: $ragNoteBudget, hint: "per-note cap, default 20000")
            knobRow(label: "Rerank candidate docs", key: "ai.rerank.candidate_docs", text: $rerankCandidates, hint: "over-fetch pool when rerank is on, default 50, max 100")
        }
    }

    private var embeddingSection: some View {
        VStack(alignment: .leading, spacing: 8) {
            Text("Embedding").font(.subheadline.bold())
            knobRow(label: "Embed concurrency", key: "ai.embed_concurrency", text: $embedConcurrency, hint: "parallel embed calls, 1 to 64")
            if let dims = activeEmbedModelDimensions, dims.count > 1 {
                HStack(spacing: 8) {
                    Text("Dimensions")
                    Picker("", selection: $dimensionSelection) {
                        ForEach(dims, id: \.self) { d in
                            Text("\(d)").tag(d)
                        }
                    }
                    .labelsHidden()
                    .frame(width: 110)
                    Button("Save") { Task { await saveConfig("ai.dimensions", "\(dimensionSelection)") } }
                        .controlSize(.small)
                    Text("changing this needs Re-embed All")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
                rowError("ai.dimensions")
            }
        }
    }

    private var providerSection: some View {
        VStack(alignment: .leading, spacing: 8) {
            Text("Provider endpoints").font(.subheadline.bold())
            knobRow(label: "Bedrock profile", key: "ai.bedrock.profile", text: $bedrockProfile, hint: "AWS profile name")
            knobRow(label: "Bedrock region", key: "ai.bedrock.region", text: $bedrockRegion, hint: "e.g. us-east-1")
            knobRow(label: "Ollama endpoint", key: "ai.ollama.endpoint", text: $ollamaEndpoint, hint: "default http://localhost:11434")
        }
    }

    private var toolsSection: some View {
        VStack(alignment: .leading, spacing: 8) {
            Text("Tools").font(.subheadline.bold())
            HStack(spacing: 10) {
                Button(calibrating ? "Calibrating…" : "Calibrate threshold") {
                    Task { await calibrate() }
                }
                .disabled(calibrating)
                Text("free: samples stored embeddings, saves a recommendation")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
            HStack(spacing: 10) {
                Button(probing ? "Probing…" : "Find safe concurrency") {
                    Task { await runEmbedProbe() }
                }
                .disabled(probing)
                Text("paid: ramps real embedding calls to find your account's ceiling (takes minutes)")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
        }
    }

    private var effectiveConfigSection: some View {
        DisclosureGroup("Effective configuration (read-only)", isExpanded: $showEffectiveConfig) {
            VStack(alignment: .leading, spacing: 2) {
                if configRows.isEmpty {
                    Text("Loading…").font(.caption).foregroundStyle(.secondary)
                } else {
                    ForEach(configRows, id: \.key) { row in
                        HStack(alignment: .top, spacing: 8) {
                            Text(row.key)
                                .font(.caption.monospaced())
                                .foregroundStyle(.secondary)
                                .frame(width: 260, alignment: .leading)
                            Text(row.value)
                                .font(.caption.monospaced())
                                .textSelection(.enabled)
                        }
                    }
                }
            }
            .padding(.top, 4)
        }
        .font(.subheadline)
        .onChange(of: showEffectiveConfig) { _, expanded in
            if expanded { Task { await loadEffectiveConfig() } }
        }
    }

    // MARK: - Rows

    @ViewBuilder
    private func knobRow(label: String, key: String, text: Binding<String>, hint: String) -> some View {
        HStack(spacing: 8) {
            Text(label).frame(width: 170, alignment: .leading)
            TextField("", text: text)
                .textFieldStyle(.roundedBorder)
                .frame(width: 150)
            Button("Save") { Task { await saveConfig(key, text.wrappedValue) } }
                .controlSize(.small)
            Text(hint).font(.caption).foregroundStyle(.secondary)
        }
        rowError(key)
    }

    @ViewBuilder
    private func rowError(_ key: String) -> some View {
        if let err = rowErrors[key] {
            Text(err)
                .font(.caption)
                .foregroundStyle(.red)
                .textSelection(.enabled)
        }
    }

    private var effectiveThresholdCaption: String {
        guard let status = aiStatus, let t = status.similarityThreshold else { return "" }
        let source = status.similarityThresholdSource ?? "unknown"
        return String(format: "effective now: %.2f (%@)", t, source)
    }

    private var activeEmbedModelDimensions: [Int]? {
        guard let status = aiStatus else { return nil }
        return models.first { $0.provider == status.provider && $0.modelID == status.embeddingModel }?.supportedDimensions
    }

    // MARK: - Actions

    private func loadCurrentValues() async {
        // Raw stored values. The CLI prints "0" for unset resolve-to-default
        // numeric knobs; render those as blank fields (the hint names the
        // default) so a user never mistakes "unset" for a literal zero.
        func unsetAsBlank(_ key: String) async -> String {
            let raw = (try? await appState.getConfigValue(key)) ?? ""
            return raw == "0" ? "" : raw
        }
        thresholdText = (try? await appState.getConfigValue("ai.similarity_threshold")) ?? ""
        thresholdOverrideOn = (Double(thresholdText) ?? 0) > 0
        if !thresholdOverrideOn { thresholdText = "" }
        bm25Weight = await unsetAsBlank("ai.bm25_weight")
        vectorWeight = await unsetAsBlank("ai.vector_weight")
        ragContextBudget = await unsetAsBlank("ai.rag_context_budget")
        ragNoteBudget = await unsetAsBlank("ai.rag_note_budget")
        rerankCandidates = await unsetAsBlank("ai.rerank.candidate_docs")
        embedConcurrency = await unsetAsBlank("ai.embed_concurrency")
        bedrockProfile = (try? await appState.getConfigValue("ai.bedrock.profile")) ?? ""
        bedrockRegion = (try? await appState.getConfigValue("ai.bedrock.region")) ?? ""
        ollamaEndpoint = (try? await appState.getConfigValue("ai.ollama.endpoint")) ?? ""
        if let dims = (try? await appState.getConfigValue("ai.dimensions")), let d = Int(dims) {
            dimensionSelection = d
        }
    }

    private func saveThreshold() async {
        guard let value = AdvancedConfig.thresholdWriteValue(overrideOn: thresholdOverrideOn, text: thresholdText) else {
            rowErrors["ai.similarity_threshold"] = "Threshold must be a number between 0 and 1."
            return
        }
        await saveConfig("ai.similarity_threshold", value)
    }

    private func saveConfig(_ key: String, _ value: String) async {
        rowErrors[key] = nil
        do {
            try await appState.setConfigValue(key, value)
            statusText = "Saved \(key) = \(value)"
            await onReload()
        } catch {
            rowErrors[key] = error.localizedDescription
        }
    }

    private func calibrate() async {
        calibrating = true
        defer { calibrating = false }
        do {
            let result = try await appState.calibrateThreshold()
            statusText = AdvancedConfig.calibrationMessage(result)
            await onReload()
        } catch {
            statusText = "Calibration failed: \(error.localizedDescription)"
        }
    }

    private func runEmbedProbe() async {
        #if canImport(AppKit)
        let alert = NSAlert()
        alert.messageText = "Find safe embed concurrency?"
        alert.informativeText = "This ramps REAL embedding calls over a discarded sample of your vault (typically well under a dollar, several minutes). The result is a recommended ai.embed_concurrency for this account."
        alert.addButton(withTitle: "Run probe")
        alert.addButton(withTitle: "Cancel")
        guard alert.runModal() == .alertFirstButtonReturn else { return }
        #endif
        probing = true
        defer { probing = false }
        do {
            let result = try await appState.embedProbe()
            embedConcurrency = "\(result.recommended)"
            statusText = "Probe done: recommended concurrency \(result.recommended). Press Save on Embed concurrency to apply."
        } catch {
            statusText = "Probe failed: \(error.localizedDescription)"
        }
    }

    private func loadEffectiveConfig() async {
        if let data = try? await appState.fetchConfigShow() {
            configRows = AdvancedConfig.flatten(data)
        }
    }
}
