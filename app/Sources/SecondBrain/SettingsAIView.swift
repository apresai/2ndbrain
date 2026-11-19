import SwiftUI
#if canImport(AppKit)
import AppKit
#endif

/// Curated Gemini 4 generation models, Gemini embedding models, and thinking
/// depth options exposed by the Settings UI. Pure data + helpers so unit tests
/// can validate defaults and Hydrogen-exclusive `xhigh` gating directly.
enum GeminiSettingsCatalog {
    struct ModelOption: Identifiable, Equatable {
        let id: String
        let title: String
        let subtitle: String
    }

    struct ThinkingOption: Identifiable, Equatable {
        let id: String
        let label: String
    }

    static let provider = "gemini"
    static let defaultDimensions = 768
    static let defaultGenerationModel = "gemini-4-hydrogen"
    static let defaultEmbeddingModel = "gemini-embedding-2"

    static let generationModels: [ModelOption] = [
        ModelOption(
            id: "gemini-4-hydrogen",
            title: "Gemini 4 Hydrogen (Default)",
            subtitle: "Multimodal reasoning & agentic model · 1M context · supports Extra High thinking"
        ),
        ModelOption(
            id: "gemini-4-argon",
            title: "Gemini 4 Argon",
            subtitle: "Efficient frontier reasoning · 1M context"
        ),
        ModelOption(
            id: "gemini-4-carbon",
            title: "Gemini 4 Carbon",
            subtitle: "Flagship frontier reasoning · 1M context"
        ),
    ]

    static let embeddingModels: [ModelOption] = [
        ModelOption(
            id: "gemini-embedding-2",
            title: "Gemini Embedding 2 (Default)",
            subtitle: "768 dimensions · Multimodal + text"
        ),
        ModelOption(
            id: "gemini-embedding-001",
            title: "Gemini Embedding 001",
            subtitle: "768 dimensions · Text"
        ),
    ]

    static func validThinkingLevels(for modelID: String) -> [ThinkingOption] {
        var options: [ThinkingOption] = [
            ThinkingOption(id: "", label: "Model Default"),
            ThinkingOption(id: "low", label: "Low"),
            ThinkingOption(id: "medium", label: "Medium"),
            ThinkingOption(id: "high", label: "High"),
        ]
        if modelID == "gemini-4-hydrogen" {
            options.append(ThinkingOption(id: "xhigh", label: "Extra High"))
        }
        return options
    }

    static func sanitizedThinking(_ level: String, for modelID: String) -> String {
        let normalized = level.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        switch normalized {
        case "":
            return ""
        case "none", "low":
            return "low"
        case "medium":
            return "medium"
        case "high":
            return "high"
        case "xhigh":
            return modelID == "gemini-4-hydrogen" ? "xhigh" : "high"
        default:
            return ""
        }
    }
}

/// Whether a probed API key earns a place in Keychain, and what to tell the
/// user about it. Pure logic so the rules are unit-testable without a key.
enum KeyPersistence {
    static func shouldPersistProbedKey(verdict: CredentialVerdict, hasExistingKey: Bool) -> Bool {
        switch verdict {
        case .accepted, .unknown:
            return true
        case .rejected:
            return false
        case .unreachable:
            return !hasExistingKey
        }
    }

    static func message(verdict: CredentialVerdict, persisted: Bool, provider: String) -> String? {
        switch (verdict, persisted) {
        case (.rejected, false):
            return "\(provider) rejected that key, so it was not saved. Your existing key is untouched."
        case (.unreachable, false):
            return "That key could not be checked, so it was not saved. Your existing key is untouched."
        case (.unreachable, true):
            return "Saved without confirmation — \(provider) could not be reached to check it."
        default:
            return nil
        }
    }
}

// MARK: - Local Gemma Catalog

enum LocalGemmaCatalog {
    struct LocalModel: Identifiable, Equatable {
        let id: String
        let role: String
        let title: String
        let subtitle: String
        let fileName: String
        let sizeBytes: Int64
        let sizeDisplay: String

        func isCached() -> Bool {
            let baseCacheDir: URL
            if let xdg = ProcessInfo.processInfo.environment["XDG_CACHE_HOME"], !xdg.isEmpty {
                baseCacheDir = URL(fileURLWithPath: xdg, isDirectory: true)
            } else if let caches = FileManager.default.urls(for: .cachesDirectory, in: .userDomainMask).first {
                baseCacheDir = caches
            } else {
                baseCacheDir = FileManager.default.homeDirectoryForCurrentUser.appendingPathComponent("Library/Caches", isDirectory: true)
            }
            let modelURL = baseCacheDir
                .appendingPathComponent("2nb", isDirectory: true)
                .appendingPathComponent("models", isDirectory: true)
                .appendingPathComponent(id, isDirectory: true)
                .appendingPathComponent(fileName)
            return FileManager.default.fileExists(atPath: modelURL.path)
        }
    }

    static let models: [LocalModel] = [
        LocalModel(
            id: "embeddinggemma-2",
            role: "embed",
            title: "EmbeddingGemma 2",
            subtitle: "768d Gemma 4 embeddings · Apache 2.0 · fully offline",
            fileName: "embeddinggemma-2-Q8_0.gguf",
            sizeBytes: 309_855_456,
            sizeDisplay: "310 MB"
        ),
        LocalModel(
            id: "gemma4-e2b",
            role: "gen",
            title: "Gemma 4 E2B",
            subtitle: "On-device reasoning & generation · 128K context · <4GB footprint",
            fileName: "gemma-4-e2b-it-Q4_K_M.gguf",
            sizeBytes: 3_106_736_256,
            sizeDisplay: "3.11 GB"
        ),
        LocalModel(
            id: "bge-reranker-v2-m3",
            role: "rerank",
            title: "BGE Reranker v2 M3",
            subtitle: "Cross-encoder neural reranking · 8K context · CPU/Metal",
            fileName: "bge-reranker-v2-m3-Q8_0.gguf",
            sizeBytes: 635_676_416,
            sizeDisplay: "636 MB"
        )
    ]

    static let allModelIDs = models.map(\.id)
    static let totalSizeBytes: Int64 = models.reduce(0) { $0 + $1.sizeBytes }
    static let defaultDimensions = 768
    static let defaultGenModel = "gemma4-e2b"
    static let defaultEmbedModel = "embeddinggemma-2"
    static let defaultRerankModel = "bge-reranker-v2-m3"

    struct MatryoshkaDimension: Identifiable, Equatable {
        let id: Int
        let label: String
        let subtitle: String
    }

    static let matryoshkaDimensions: [MatryoshkaDimension] = [
        MatryoshkaDimension(
            id: 256,
            label: "256d (Fast / Compact)",
            subtitle: "3x index compression, sub-millisecond search, minimal memory footprint"
        ),
        MatryoshkaDimension(
            id: 512,
            label: "512d (Balanced)",
            subtitle: "Optimal quality/speed trade-off (retains ~98% full-width retrieval performance)"
        ),
        MatryoshkaDimension(
            id: 768,
            label: "768d (Full Fidelity)",
            subtitle: "Native EmbeddingGemma vector width, maximum semantic precision"
        )
    ]

    struct TaskPrefixPreset: Identifiable, Equatable {
        let id: String
        let label: String
        let description: String
    }

    static let taskPrefixPresets: [TaskPrefixPreset] = [
        TaskPrefixPreset(
            id: "search",
            label: "Search Retrieval (Default)",
            description: "Applies 'task: search result | query: ' for queries and 'title: ... | text: ...' for documents"
        ),
        TaskPrefixPreset(
            id: "code",
            label: "Code Retrieval",
            description: "Applies 'task: code retrieval | query: ' optimized for code snippet matching"
        ),
        TaskPrefixPreset(
            id: "qa",
            label: "Question Answering",
            description: "Applies 'task: question answering | query: ' for direct question matching"
        ),
        TaskPrefixPreset(
            id: "none",
            label: "Symmetric / Raw (None)",
            description: "Passes raw input without task prefixes"
        )
    ]
}

enum AIProviderChoice: String, CaseIterable, Identifiable {
    case gemini = "gemini"
    case localGemma = "llama-local"

    var id: String { rawValue }
    var label: String {
        switch self {
        case .gemini: return "Google Gemini (Cloud)"
        case .localGemma: return "Local Gemma (On-Device)"
        }
    }
}

/// The Gemini settings tab in the Settings window (Cmd+,): authentication
/// (BYOK Gemini API Key or BYOP Vertex AI GCP Project), Gemini 4 model &
/// thinking-depth selection, and live connection verification via `2nb doctor`.
struct SettingsAIView: View {
    @Environment(AppState.self) var appState
    @State private var reloading = false
    @State private var reloadPending = false

    enum GeminiAuthMode: String, CaseIterable, Identifiable {
        case apiKey = "apiKey"
        case vertexAI = "vertexAI"

        var id: String { rawValue }
        var label: String {
            switch self {
            case .apiKey: return "Gemini API Key (BYOK)"
            case .vertexAI: return "Vertex AI (GCP Project)"
            }
        }
    }

    static let customRegionTag = "__custom__"

    @State private var geminiAuthMode: GeminiAuthMode = .apiKey
    @State private var geminiProject = ""
    @State private var savedGeminiProject = ""
    @State private var googleRegionSelection = GoogleRegions.defaultRegion
    @State private var customRegion = ""
    @State private var newGeminiKey = ""
    @State private var storedGeminiKey = ""
    @State private var enteringGeminiKey = false

    @State private var status: AIStatusInfo?
    @State private var selectedGenModel = GeminiSettingsCatalog.defaultGenerationModel
    @State private var selectedEmbedModel = GeminiSettingsCatalog.defaultEmbeddingModel
    @State private var selectedThinking = ""

    // Slot provider & Local Gemma tuning state
    @State private var selectedGenProvider: AIProviderChoice = .gemini
    @State private var selectedEmbedProvider: AIProviderChoice = .gemini
    @State private var activeLocalEmbedModel = LocalGemmaCatalog.defaultEmbedModel
    @State private var selectedMatryoshkaDim: Int = 768
    @State private var selectedTaskPrefix: String = "search"

    @State private var busy = false
    @State private var message: String?
    @State private var selfTest: SelfTestReport?
    @State private var selfTestProviders: (gen: String, embed: String)?
    @State private var testing = false
    @State private var testError: String?

    @State private var testingGen = false
    @State private var genProbeResult: AIProbeResult?
    @State private var genProbeError: String?
    @State private var testingEmbed = false
    @State private var embedProbeResult: AIProbeResult?
    @State private var embedProbeError: String?

    private var activeProvider: String {
        status?.provider ?? "gemini"
    }

    private var isUsingOtherProvider: Bool {
        guard appState.vault != nil, let status else { return false }
        let known: Set<String> = ["", "gemini", "google", "llama-local"]
        return !known.contains(status.effectiveGenerationProvider)
            || !known.contains(status.effectiveEmbeddingProvider)
    }

    private var activeOtherProviderName: String {
        guard let status else { return ProviderDisplay.name(activeProvider) }
        let known: Set<String> = ["", "gemini", "google", "llama-local"]
        if !known.contains(status.effectiveGenerationProvider) {
            return ProviderDisplay.name(status.effectiveGenerationProvider)
        }
        if !known.contains(status.effectiveEmbeddingProvider) {
            return ProviderDisplay.name(status.effectiveEmbeddingProvider)
        }
        return ProviderDisplay.name(status.provider)
    }

    var body: some View {
        Form {
            if isUsingOtherProvider {
                otherProviderBanner
            }

            modelTestSection
            generationSlotSection
            embeddingSlotSection

            if selectedGenProvider == .gemini || selectedEmbedProvider == .gemini || appState.vault == nil {
                authenticationSection
            }

            if selectedGenProvider == .localGemma
                || selectedEmbedProvider == .localGemma
                || LocalGemmaCatalog.models.contains(where: { $0.isCached() }) {
                localModelsCard
            }

            if appState.vault != nil {
                aiUsageSection
            }

            if let message {
                Section {
                    Text(message)
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
            }
        }
        .formStyle(.grouped)
        .task {
            await reload()
            applyButtonCommand(appState.guiInputBridge.pendingButtonCommand)
        }
        .onChange(of: appState.guiInputBridge.pendingButtonCommand) { _, cmd in
            applyButtonCommand(cmd)
        }
        .onChange(of: appState.vault?.rootURL) { _, _ in
            clearAllSlotProbes()
            selfTest = nil
            testError = nil
        }
        .onChange(of: appState.isIndexing) { wasIndexing, isIndexing in
            if wasIndexing && !isIndexing {
                Task {
                    await reload()
                    if selfTest != nil {
                        await runTest()
                    }
                }
            }
        }
    }

    // MARK: - Non-Gemini Override Banner

    private var otherProviderBanner: some View {
        Section {
            VStack(alignment: .leading, spacing: 8) {
                Label(
                    "Vault is currently set to \(activeOtherProviderName)",
                    systemImage: "exclamationmark.triangle.fill"
                )
                .font(.callout.weight(.medium))
                .foregroundStyle(.orange)

                Text("This vault was configured for another provider via the CLI. The macOS companion targets Google Gemini 4 and Local Gemma.")
                    .font(.caption)
                    .foregroundStyle(.secondary)

                Button(busy ? "Switching…" : "Switch Vault to Google Gemini") {
                    Task { await switchVaultToGemini() }
                }
                .controlSize(.small)
                .disabled(busy)
            }
            .padding(.vertical, 2)
        }
    }

    // MARK: - Authentication

    private var authenticationSection: some View {
        Section("Google Gemini Authentication") {
            Picker("Mode", selection: authModeBinding) {
                ForEach(GeminiAuthMode.allCases) { mode in
                    Text(mode.label).tag(mode)
                }
            }
            .pickerStyle(.segmented)

            if geminiAuthMode == .apiKey {
                apiKeyRows
            } else {
                vertexAIRows
            }
        }
    }

    @ViewBuilder
    private var apiKeyRows: some View {
        if !savedGeminiProject.isEmpty {
            VStack(alignment: .leading, spacing: 6) {
                Label(
                    "Vertex AI project \"\(savedGeminiProject)\" is active on this vault.",
                    systemImage: "info.circle"
                )
                .font(.caption)
                .foregroundStyle(.orange)

                Text("While a GCP project is configured, `2nb` uses Vertex AI instead of your API key.")
                    .font(.caption2)
                    .foregroundStyle(.secondary)

                Button("Clear GCP Project & Use API Key") {
                    Task { await clearGeminiProject() }
                }
                .controlSize(.small)
                .disabled(busy)
            }
        }

        if enteringGeminiKey || storedGeminiKey.isEmpty {
            SecureField("GEMINI_API_KEY", text: $newGeminiKey)
                .textFieldStyle(.roundedBorder)
            HStack {
                Button(busy ? "Verifying…" : "Save & Verify") {
                    Task { await saveGeminiKey() }
                }
                .keyboardShortcut(.defaultAction)
                .disabled(busy || newGeminiKey.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)

                if !storedGeminiKey.isEmpty {
                    Button("Cancel") {
                        enteringGeminiKey = false
                        newGeminiKey = ""
                    }
                }
            }
            Text("Stored in macOS Keychain via `GEMINI_API_KEY` (never `GOOGLE_API_KEY`). Verified against Gemini before saving.")
                .font(.caption)
                .foregroundStyle(.secondary)
        } else {
            LabeledContent("API Key") {
                HStack(spacing: 8) {
                    Text(Self.maskedKey(storedGeminiKey))
                        .font(.body.monospaced())
                    Button("Replace") { enteringGeminiKey = true }
                        .controlSize(.small)
                    Button("Remove") { Task { await clearGeminiKey() } }
                        .controlSize(.small)
                        .disabled(busy)
                }
            }
            Text("Stored securely in macOS Keychain outside the vault.")
                .font(.caption)
                .foregroundStyle(.secondary)
        }
    }

    @ViewBuilder
    private var vertexAIRows: some View {
        LabeledContent("GCP Project") {
            HStack {
                TextField("GCP Project", text: $geminiProject, prompt: Text("your-gcp-project-id"))
                    .textFieldStyle(.roundedBorder)
                Button("Save") {
                    Task { await saveGeminiProject() }
                }
                .controlSize(.small)
                .disabled(busy || appState.vault == nil)
            }
        }
        Text(appState.vault == nil
             ? "Bind an Obsidian vault to save your GCP project ID for Vertex AI."
             : "Your own Google Cloud project ID for Vertex AI (e.g. your-gcp-project-id).")
            .font(.caption)
            .foregroundStyle(.secondary)

        Picker("Region", selection: googleRegionBinding) {
            ForEach(GoogleRegions.common) { region in
                Text(region.display).tag(region.id)
            }
            Text("Other…").tag(Self.customRegionTag)
        }
        .disabled(busy || appState.vault == nil)

        if googleRegionSelection == Self.customRegionTag {
            HStack {
                TextField("Region code", text: $customRegion, prompt: Text("us-central1"))
                    .textFieldStyle(.roundedBorder)
                Button("Save") {
                    Task { await applyGoogleRegion(customRegion) }
                }
                .controlSize(.small)
                .disabled(busy || appState.vault == nil || customRegion.trimmingCharacters(in: .whitespaces).isEmpty)
            }
        }

        VStack(alignment: .leading, spacing: 6) {
            Text("Authenticates via Google Application Default Credentials (`gcloud auth application-default login`).")
                .font(.caption)
                .foregroundStyle(.secondary)

            Button(testing ? "Testing…" : "Test Vertex AI / ADC") {
                Task { await testVertexAICredentials() }
            }
            .controlSize(.small)
            .accessibilityIdentifier("settings.testVertexAI")
            .disabled(busy || testing || testingGen || testingEmbed || geminiProject.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
        }
    }

    // MARK: - Per-Slot Configuration Sections

    private var generationSlotSection: some View {
        Section("Generation (Ask, Chat & Polish)") {
            Picker("Source", selection: genProviderBinding) {
                ForEach(AIProviderChoice.allCases) { choice in
                    Text(choice.label).tag(choice)
                }
            }
            .pickerStyle(.segmented)
            .disabled(busy || appState.vault == nil)

            if selectedGenProvider == .gemini {
                Picker("Model", selection: genModelBinding) {
                    ForEach(GeminiSettingsCatalog.generationModels) { option in
                        Text(option.title).tag(option.id)
                    }
                    if !GeminiSettingsCatalog.generationModels.contains(where: { $0.id == selectedGenModel }) {
                        Text("\(selectedGenModel) (Custom)").tag(selectedGenModel)
                    }
                }
                .disabled(busy || appState.vault == nil)

                if let genOption = GeminiSettingsCatalog.generationModels.first(where: { $0.id == selectedGenModel }) {
                    Text(genOption.subtitle)
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }

                Picker("Thinking Depth", selection: thinkingBinding) {
                    ForEach(GeminiSettingsCatalog.validThinkingLevels(for: selectedGenModel)) { option in
                        Text(option.label).tag(option.id)
                    }
                }
                .disabled(busy || appState.vault == nil)

                Text(selectedGenModel == "gemini-4-hydrogen"
                     ? "Extra High thinking is calibrated on Gemini 4 Hydrogen."
                     : "Uses Gemini 4 adaptive thinking depth (`ai.reasoning_effort`). Switch to Hydrogen for Extra High.")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            } else {
                localModelStatusRow(LocalGemmaCatalog.defaultGenModel)
                Text("Generation runs on-device via Apple Silicon Metal GPU acceleration.")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }

            HStack(spacing: 8) {
                Button(testingGen ? "Testing…" : "Test Generation Model") {
                    Task { await runGenerationTest() }
                }
                .controlSize(.small)
                .accessibilityIdentifier("settings.testGeneration")
                .disabled(busy || testing || testingGen || testingEmbed)

                if testingGen {
                    ProgressView()
                        .controlSize(.small)
                }
            }

            if testingGen {
                ColdStartHint()
            }

            slotProbeResultView(
                result: genProbeResult,
                errorMessage: genProbeError,
                isEmbedding: false
            )

            if appState.vault == nil {
                Text("Open an Obsidian vault to customize generation settings.")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
        }
    }

    private var needsEmbeddingReindex: Bool {
        reembedBannerMessage != nil || status?.portabilityStatus == "unindexed"
    }

    private var embeddingSlotSection: some View {
        Section("Embeddings (Semantic Search & Indexing)") {
            Picker("Source", selection: embedProviderBinding) {
                ForEach(AIProviderChoice.allCases) { choice in
                    Text(choice.label).tag(choice)
                }
            }
            .pickerStyle(.segmented)
            .disabled(busy || appState.vault == nil)

            if selectedEmbedProvider == .gemini {
                Picker("Model", selection: embedModelBinding) {
                    ForEach(GeminiSettingsCatalog.embeddingModels) { option in
                        Text(option.title).tag(option.id)
                    }
                    if !GeminiSettingsCatalog.embeddingModels.contains(where: { $0.id == selectedEmbedModel }) {
                        Text("\(selectedEmbedModel) (Custom)").tag(selectedEmbedModel)
                    }
                }
                .disabled(busy || appState.vault == nil)

                if let embedOption = GeminiSettingsCatalog.embeddingModels.first(where: { $0.id == selectedEmbedModel }) {
                    Text(embedOption.subtitle)
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
            } else {
                localModelStatusRow(LocalGemmaCatalog.defaultEmbedModel)

                if activeLocalEmbedModel != LocalGemmaCatalog.defaultEmbedModel {
                    HStack(spacing: 8) {
                        Image(systemName: "arrow.up.circle.fill")
                            .foregroundStyle(.orange)
                        Text("Vault is currently using legacy \(activeLocalEmbedModel).")
                            .font(.caption)
                        Spacer()
                        Button("Switch to EmbeddingGemma 2") {
                            activeLocalEmbedModel = LocalGemmaCatalog.defaultEmbedModel
                            Task { await applyEmbeddingSlot(provider: "llama-local", modelID: LocalGemmaCatalog.defaultEmbedModel, dimensions: selectedMatryoshkaDim) }
                        }
                        .buttonStyle(.borderedProminent)
                        .controlSize(.small)
                        .accessibilityIdentifier("settings.upgradeEmbeddingGemma2")
                        .disabled(busy || appState.vault == nil)
                    }
                    .padding(8)
                    .background(Color.orange.opacity(0.08), in: RoundedRectangle(cornerRadius: 8, style: .continuous))
                }

                VStack(alignment: .leading, spacing: 6) {
                    Text("Matryoshka Representation Learning (MRL) Dimensions")
                        .font(.body.weight(.medium))
                    Picker("Dimensions", selection: matryoshkaBinding) {
                        ForEach(LocalGemmaCatalog.matryoshkaDimensions) { opt in
                            Text(opt.label).tag(opt.id)
                        }
                    }
                    .pickerStyle(.segmented)
                    .disabled(busy || appState.vault == nil)

                    if let selectedOpt = LocalGemmaCatalog.matryoshkaDimensions.first(where: { $0.id == selectedMatryoshkaDim }) {
                        Text(selectedOpt.subtitle)
                            .font(.caption)
                            .foregroundStyle(.secondary)
                    }
                }
                .padding(.vertical, 2)

                VStack(alignment: .leading, spacing: 6) {
                    Picker("Task Prefix Preset", selection: taskPrefixBinding) {
                        ForEach(LocalGemmaCatalog.taskPrefixPresets) { opt in
                            Text(opt.label).tag(opt.id)
                        }
                    }
                    .disabled(busy || appState.vault == nil)

                    if let selectedPreset = LocalGemmaCatalog.taskPrefixPresets.first(where: { $0.id == selectedTaskPrefix }) {
                        Text(selectedPreset.description)
                            .font(.caption)
                            .foregroundStyle(.secondary)
                    }
                }
                .padding(.vertical, 2)
            }

            HStack(spacing: 8) {
                Button(testingEmbed ? "Testing…" : "Test Embedding Model") {
                    Task { await runEmbeddingTest() }
                }
                .controlSize(.small)
                .accessibilityIdentifier("settings.testEmbedding")
                .disabled(busy || testing || testingGen || testingEmbed)

                if testingEmbed {
                    ProgressView()
                        .controlSize(.small)
                }
            }

            if testingEmbed {
                ColdStartHint()
            }

            slotProbeResultView(
                result: embedProbeResult,
                errorMessage: embedProbeError,
                isEmbedding: true
            )

            if appState.vault == nil {
                Text("Open an Obsidian vault to customize embedding settings.")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            } else if appState.isIndexing {
                VStack(alignment: .leading, spacing: 4) {
                    ProgressView(value: appState.indexFraction ?? 0.08)
                    if let ep = appState.embeddingProgress, ep.total > 0 {
                        let chunks = ep.chunksTotal.map { ct in
                            let cd = ep.chunksDone ?? 0
                            return " · \(cd)/\(ct) chunks"
                        } ?? ""
                        let speed = ep.docsPerSec.map {
                            String(format: " · %.1f docs/s", locale: Locale(identifier: "en_US_POSIX"), $0)
                        } ?? ""
                        let etaText = ep.remainingSeconds.map { " · ~\(Formatters.eta($0)) left" } ?? ""
                        Text("Re-embedding \(ep.current)/\(ep.total)\(chunks)\(speed)\(etaText)…")
                            .font(.caption2)
                            .foregroundStyle(.secondary)
                            .monospacedDigit()
                    } else {
                        Text("Re-embedding vault notes…")
                            .font(.caption2)
                            .foregroundStyle(.secondary)
                    }
                }
            } else if needsEmbeddingReindex {
                VStack(alignment: .leading, spacing: 4) {
                    HStack {
                        Button("Re-embed Vault Now") {
                            appState.startForceReembed()
                        }
                        .buttonStyle(.borderedProminent)
                        .controlSize(.small)
                        .accessibilityIdentifier("settings.reembedNow")
                        Text("Updates stored vectors for your selected embedding model and dimensions.")
                            .font(.caption)
                            .foregroundStyle(.secondary)
                    }
                    if let est = appState.metricsReport?.reembedEstimate, est.embeddableDocs > 0 {
                        let costText = est.isCloud
                            ? Formatters.usd(est.estimatedCostUSD, knownPricing: est.knownPricing)
                            : "$0.00 on-device"
                        Text("Est. ~\(Formatters.eta(est.estimatedSeconds)) for \(est.embeddableDocs) notes (\(est.totalChunks) chunks) · \(costText)")
                            .font(.caption2)
                            .foregroundStyle(.secondary)
                            .monospacedDigit()
                    }
                }
            } else {
                Text("Switching embedding source or dimensions requires re-indexing (`Sync Index` or `Re-embed Vault Now`).")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
        }
    }

    // MARK: - Connection Check

    private var reembedBannerMessage: String? {
        if let banner = HomeAI.portabilityBanner(status), banner.forceReembed {
            return banner.message
        }
        if let broken = selfTest?.checks.first(where: { $0.name == "embeddings match selection" && !$0.ok }) {
            return broken.fix ?? broken.detail
        }
        return nil
    }

    private var combinedSlotResults: [ModelSlotTestResult] {
        var genResult: ModelSlotTestResult? = genProbeResult.map {
            ModelSlotTestResult.fromProbe($0, slot: .generation)
        }
        var embedResult: ModelSlotTestResult? = embedProbeResult.map {
            ModelSlotTestResult.fromProbe($0, slot: .embedding)
        }
        if let selfTest {
            let genProv = selfTestProviders?.gen ?? currentGenProviderID
            let embedProv = selfTestProviders?.embed ?? currentEmbedProviderID
            let extracted = SelfTestPresentation.extractSlotResults(
                selfTest,
                genProvider: genProv,
                embedProvider: embedProv
            )
            if genResult == nil {
                genResult = extracted.first { $0.slot == .generation }
            }
            if embedResult == nil {
                embedResult = extracted.first { $0.slot == .embedding }
            }
        }
        return [genResult, embedResult].compactMap { $0 }
    }

    private var overallTestVerdict: (passed: Bool, headline: String)? {
        if let selfTest {
            let passed = SelfTestPresentation.modelsTestPassed(selfTest)
            let genProv = selfTestProviders?.gen ?? currentGenProviderID
            let embedProv = selfTestProviders?.embed ?? currentEmbedProviderID
            return (passed, SelfTestPresentation.modelsHeadline(selfTest, genProvider: genProv, embedProvider: embedProv))
        }
        if let gen = genProbeResult, let embed = embedProbeResult {
            let passed = gen.ok && embed.ok
            if passed {
                if reembedBannerMessage != nil {
                    return (true, "Both AI models responded and passed — re-embed the vault when ready to update search vectors.")
                }
                if gen.provider == "llama-local" && embed.provider == "llama-local" {
                    return (true, "Both Local Gemma models responded and passed (on-device, no API key needed).")
                }
                return (true, "Both AI models responded and passed.")
            }
            if !gen.ok && !embed.ok {
                return (false, "Both generation and embedding model tests failed.")
            }
            if !gen.ok {
                return (false, "Generation model (\(gen.modelID)) failed.")
            }
            return (false, "Embedding model (\(embed.modelID)) failed.")
        }
        return nil
    }

    private var modelTestSection: some View {
        Section("Model Test & Results") {
            VStack(alignment: .leading, spacing: 8) {
                HStack(spacing: 10) {
                    Button(testing ? "Testing Models…" : "Test AI Connection") {
                        Task { await runTest() }
                    }
                    .buttonStyle(.borderedProminent)
                    .accessibilityIdentifier("settings.testConnection")
                    .disabled(busy || testing || testingGen || testingEmbed)

                    if testing || testingGen || testingEmbed {
                        ProgressView()
                            .controlSize(.small)
                        Text("Waiting for live model response…")
                            .font(.caption)
                            .foregroundStyle(.secondary)
                    }
                }

                Text("Invokes \(currentGenModelID) and \(currentEmbedModelID) live and verifies their responses")
                    .font(.caption)
                    .foregroundStyle(.secondary)

                if testing || testingGen || testingEmbed {
                    ColdStartHint()
                }
            }

            if let testError {
                Label(testError, systemImage: "exclamationmark.triangle.fill")
                    .font(.caption)
                    .foregroundStyle(.orange)
            }

            if overallTestVerdict != nil || !combinedSlotResults.isEmpty {
                VStack(alignment: .leading, spacing: 10) {
                    Text("Test Results")
                        .font(.subheadline.weight(.semibold))

                    if let verdict = overallTestVerdict {
                        HStack(spacing: 8) {
                            Image(systemName: verdict.passed ? "checkmark.circle.fill" : "xmark.circle.fill")
                                .foregroundStyle(verdict.passed ? .green : .red)
                            Text(verdict.headline)
                                .font(.callout.weight(.semibold))
                                .foregroundStyle(verdict.passed ? Color.primary : Color.red)
                        }
                    }

                    ForEach(combinedSlotResults) { slot in
                        slotResultCard(slot)
                    }

                    if let selfTest {
                        let configIssues = SelfTestPresentation.relevantConfigIssues(selfTest)
                        ForEach(configIssues) { check in
                            HStack(alignment: .firstTextBaseline, spacing: 6) {
                                Image(systemName: SelfTestPresentation.symbol(for: check))
                                    .foregroundStyle(check.ok ? (check.isWarning ? .orange : .green) : .red)
                                VStack(alignment: .leading, spacing: 2) {
                                    Text("\(check.name): \(check.detail)")
                                        .font(.caption)
                                    if let fix = check.fix {
                                        Text(fix)
                                            .font(.caption2)
                                            .foregroundStyle(.secondary)
                                    }
                                }
                            }
                        }
                    }

                    if overallTestVerdict?.passed == true, let reembedMsg = reembedBannerMessage {
                        VStack(alignment: .leading, spacing: 8) {
                            Label("Models passed — vault index needs re-embedding", systemImage: "exclamationmark.triangle.fill")
                                .font(.callout.weight(.medium))
                                .foregroundStyle(.orange)

                            Text(reembedMsg)
                                .font(.caption)
                                .foregroundStyle(.secondary)

                            if appState.isIndexing {
                                VStack(alignment: .leading, spacing: 4) {
                                    ProgressView(value: appState.indexFraction ?? 0.08)
                                    if let ep = appState.embeddingProgress, ep.total > 0 {
                                        Text("Re-embedding \(ep.current)/\(ep.total)…")
                                            .font(.caption2)
                                            .foregroundStyle(.secondary)
                                            .monospacedDigit()
                                    } else {
                                        Text("Re-embedding vault notes…")
                                            .font(.caption2)
                                            .foregroundStyle(.secondary)
                                    }
                                }
                            } else {
                                Button("Re-embed Vault Now") {
                                    appState.startForceReembed()
                                }
                                .buttonStyle(.borderedProminent)
                                .controlSize(.small)
                                .accessibilityIdentifier("settings.reembedNow")
                                .disabled(appState.vault == nil)
                            }
                        }
                        .padding(10)
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .background(Color.orange.opacity(0.08), in: RoundedRectangle(cornerRadius: 8, style: .continuous))
                    }
                }
                .padding(.top, 4)
            }
        }
    }

    @ViewBuilder
    private func slotResultCard(_ slot: ModelSlotTestResult) -> some View {
        let strategy = slot.slot == .generation ? genProbeResult?.invokeStrategy : embedProbeResult?.invokeStrategy
        VStack(alignment: .leading, spacing: 4) {
            HStack(spacing: 8) {
                Image(systemName: slot.ok ? "checkmark.circle.fill" : "xmark.circle.fill")
                    .foregroundStyle(slot.ok ? .green : .red)

                Text(slot.slot == .generation ? "Generation" : "Embeddings")
                    .font(.caption.weight(.semibold))

                Text(slot.modelID.isEmpty ? "(not set)" : slot.modelID)
                    .font(.caption.monospaced())
                    .lineLimit(1)
                    .truncationMode(.middle)

                Text("· \(ProviderDisplay.name(slot.provider))")
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)

                Spacer()

                let badgeText = slot.ok
                    ? (slot.latency.map { "PASS · \($0)" } ?? "PASS")
                    : "FAIL"
                Text(badgeText)
                    .font(.caption2.weight(.semibold).monospacedDigit())
                    .padding(.horizontal, 7)
                    .padding(.vertical, 2)
                    .background(
                        (slot.ok ? Color.green : Color.red).opacity(0.14),
                        in: Capsule()
                    )
                    .foregroundStyle(slot.ok ? .green : .red)
            }

            if let proof = slot.proofLabel {
                Text(proof)
                    .font(.caption2.monospaced())
                    .foregroundStyle(.secondary)
                    .textSelection(.enabled)
                    .padding(.leading, 22)
            } else if !slot.ok {
                Text(slot.responseProof)
                    .font(.caption2)
                    .foregroundStyle(.red)
                    .textSelection(.enabled)
                    .padding(.leading, 22)

                guidanceView(
                    code: slot.errorCode,
                    provider: slot.provider,
                    remediation: slot.remediation,
                    strategy: strategy
                )
            }
        }
        .padding(8)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(Color.primary.opacity(0.035), in: RoundedRectangle(cornerRadius: 8, style: .continuous))
    }

    @ViewBuilder
    private func guidanceView(
        code: String?,
        provider: String,
        remediation: String?,
        strategy: String?
    ) -> some View {
        if let g = ModelAccessPresentation.guidance(
            code: code,
            provider: provider,
            remediation: provider == "llama-local" ? nil : remediation,
            region: guidanceRegion(for: provider),
            strategy: strategy
        ) {
            VStack(alignment: .leading, spacing: 2) {
                Text("\(g.badge.uppercased()) · \(g.title)")
                    .font(.caption2.weight(.semibold))
                    .foregroundStyle(.orange)
                if !g.advice.isEmpty {
                    Text(g.advice)
                        .font(.caption2)
                        .foregroundStyle(.secondary)
                }
                if case .url(let url) = g.action, let label = g.actionLabel {
                    Link(label, destination: url)
                        .font(.caption2)
                }
            }
            .padding(.leading, 20)
        }
    }

    // MARK: - Bindings (fire CLI writes only on user interaction)

    private var authModeBinding: Binding<GeminiAuthMode> {
        Binding(
            get: { geminiAuthMode },
            set: { newMode in
                guard newMode != geminiAuthMode else { return }
                geminiAuthMode = newMode
                clearAllSlotProbes()
                selfTest = nil
                testError = nil
            }
        )
    }

    private var genProviderBinding: Binding<AIProviderChoice> {
        Binding(
            get: { selectedGenProvider },
            set: { choice in
                guard choice != selectedGenProvider else { return }
                selectedGenProvider = choice
                switch choice {
                case .gemini:
                    Task { await applyGenerationSlot(provider: "gemini", modelID: selectedGenModel) }
                case .localGemma:
                    Task { await applyGenerationSlot(provider: "llama-local", modelID: LocalGemmaCatalog.defaultGenModel) }
                }
            }
        )
    }

    private var embedProviderBinding: Binding<AIProviderChoice> {
        Binding(
            get: { selectedEmbedProvider },
            set: { choice in
                guard choice != selectedEmbedProvider else { return }
                selectedEmbedProvider = choice
                switch choice {
                case .gemini:
                    Task { await applyEmbeddingSlot(provider: "gemini", modelID: selectedEmbedModel, dimensions: GeminiSettingsCatalog.defaultDimensions) }
                case .localGemma:
                    activeLocalEmbedModel = LocalGemmaCatalog.defaultEmbedModel
                    Task { await applyEmbeddingSlot(provider: "llama-local", modelID: LocalGemmaCatalog.defaultEmbedModel, dimensions: selectedMatryoshkaDim) }
                }
            }
        )
    }

    private var googleRegionBinding: Binding<String> {
        Binding(
            get: { googleRegionSelection },
            set: { newValue in
                guard newValue != googleRegionSelection else { return }
                googleRegionSelection = newValue
                if newValue != Self.customRegionTag {
                    Task { await applyGoogleRegion(newValue) }
                }
            }
        )
    }

    private var genModelBinding: Binding<String> {
        Binding(
            get: { selectedGenModel },
            set: { newValue in
                guard newValue != selectedGenModel else { return }
                selectedGenModel = newValue
                Task { await applyGenerationModel(newValue) }
            }
        )
    }

    private var thinkingBinding: Binding<String> {
        Binding(
            get: { selectedThinking },
            set: { newValue in
                guard newValue != selectedThinking else { return }
                selectedThinking = newValue
                Task { await applyThinkingDepth(newValue) }
            }
        )
    }

    private var embedModelBinding: Binding<String> {
        Binding(
            get: { selectedEmbedModel },
            set: { newValue in
                guard newValue != selectedEmbedModel else { return }
                selectedEmbedModel = newValue
                Task { await applyEmbeddingModel(newValue) }
            }
        )
    }

    // MARK: - Local Gemma Cache & Controls

    @ViewBuilder
    private func localModelStatusRow(_ modelID: String) -> some View {
        if let m = LocalGemmaCatalog.models.first(where: { $0.id == modelID }) {
            localModelRowView(m)
        }
    }

    private func localModelRowView(_ m: LocalGemmaCatalog.LocalModel) -> some View {
        HStack(alignment: .top) {
            VStack(alignment: .leading, spacing: 2) {
                HStack {
                    Text(m.title)
                        .font(.body.weight(.medium))
                    Text("· \(m.role)")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
                Text(m.subtitle)
                    .font(.caption2)
                    .foregroundStyle(.secondary)
            }
            Spacer()
            HStack(spacing: 8) {
                Text(m.sizeDisplay)
                    .font(.caption.monospaced())
                    .foregroundStyle(.secondary)
                if m.isCached() {
                    Label("Cached", systemImage: "checkmark.circle.fill")
                        .font(.caption2.weight(.medium))
                        .foregroundStyle(.green)
                } else {
                    Button("Download") {
                        Task { await appState.downloadLocalModels([m.id]) }
                    }
                    .controlSize(.mini)
                    .disabled(appState.isIndexing || (appState.localModelDownload != nil && !appState.localModelDownload!.finished))
                }
            }
        }
        .padding(.vertical, 2)
    }

    private var localModelsCard: some View {
        Section("Local Model Cache") {
            ForEach(LocalGemmaCatalog.models) { m in
                localModelRowView(m)
            }

            if let dl = appState.localModelDownload, !dl.finished {
                VStack(alignment: .leading, spacing: 6) {
                    HStack {
                        ProgressView().controlSize(.small)
                        Text("Downloading \(dl.currentModel.isEmpty ? "models…" : dl.currentModel)")
                            .font(.callout)
                        Spacer()
                        Text("\(dl.completed.count)/\(dl.models.count)")
                            .font(.caption)
                            .foregroundStyle(.secondary)
                    }
                    ProgressView(value: dl.fraction)
                    if dl.total > 0 {
                        Text("\(ByteCountFormatter.string(fromByteCount: dl.done, countStyle: .file)) / \(ByteCountFormatter.string(fromByteCount: dl.total, countStyle: .file))")
                            .font(.caption)
                            .foregroundStyle(.secondary)
                    }
                }
                .padding(.vertical, 4)
            } else {
                HStack(spacing: 12) {
                    Button((appState.localModelDownload?.failed ?? false) ? "Retry Download" : "Download Local Models (~4.0 GB)") {
                        confirmAndDownload()
                    }
                    .disabled(appState.isIndexing || (appState.localModelDownload != nil && !appState.localModelDownload!.finished))

                    Spacer()

                    Button("Delete Local Models") {
                        confirmAndDelete()
                    }
                    .disabled(appState.isIndexing || (appState.localModelDownload != nil && !appState.localModelDownload!.finished))
                }

                if let dl = appState.localModelDownload, dl.finished {
                    if dl.failed {
                        Label(dl.error ?? "Download failed", systemImage: "exclamationmark.triangle.fill")
                            .font(.caption)
                            .foregroundStyle(.orange)
                    } else {
                        Label("Downloaded \(dl.completed.count) local model\(dl.completed.count == 1 ? "" : "s") (~4.0 GB cached).", systemImage: "checkmark.circle.fill")
                            .font(.caption)
                            .foregroundStyle(.green)
                    }
                }
            }
        }
    }

    private var matryoshkaBinding: Binding<Int> {
        Binding(
            get: { selectedMatryoshkaDim },
            set: { newDim in
                guard newDim != selectedMatryoshkaDim else { return }
                selectedMatryoshkaDim = newDim
                Task { await applyMatryoshkaDimension(newDim) }
            }
        )
    }

    private var taskPrefixBinding: Binding<String> {
        Binding(
            get: { selectedTaskPrefix },
            set: { newPreset in
                guard newPreset != selectedTaskPrefix else { return }
                selectedTaskPrefix = newPreset
                Task { await applyTaskPrefixPreset(newPreset) }
            }
        )
    }

    private func applyMatryoshkaDimension(_ newDim: Int) async {
        busy = true
        defer { busy = false }
        clearEmbedProbe()
        do {
            try await appState.setMatryoshkaDimension(newDim)
            await reload()
            message = "Updated embedding dimensions to \(newDim)d."
        } catch {
            message = error.localizedDescription
            await reload()
        }
    }

    private func applyTaskPrefixPreset(_ newPreset: String) async {
        busy = true
        defer { busy = false }
        clearEmbedProbe()
        do {
            try await appState.setTaskPrefixPreset(newPreset)
            await reload()
            message = "Updated task prefix preset to '\(newPreset)'."
        } catch {
            message = error.localizedDescription
            await reload()
        }
    }

    private func confirmAndDownload() {
        #if canImport(AppKit)
        if ProcessInfo.processInfo.environment["2NB_TEST"] != "1" {
            let alert = NSAlert()
            alert.messageText = "Download local models?"
            alert.informativeText = "Downloads about 4.0 GB (EmbeddingGemma 2, Gemma 4 E2B, and BGE Reranker v2 M3) into ~/Library/Caches/2nb so you can run AI fully offline. This can take several minutes."
            alert.addButton(withTitle: "Download")
            alert.addButton(withTitle: "Cancel")
            guard alert.runModal() == .alertFirstButtonReturn else { return }
        }
        #endif
        Task { await appState.downloadLocalModels(LocalGemmaCatalog.allModelIDs) }
    }

    private func confirmAndDelete() {
        let active = status?.effectiveGenerationProvider == "llama-local" || status?.effectiveEmbeddingProvider == "llama-local"
        #if canImport(AppKit)
        if ProcessInfo.processInfo.environment["2NB_TEST"] != "1" {
            let alert = NSAlert()
            alert.messageText = "Delete the local models?"
            alert.informativeText = active
                ? "You are currently using local models. Deleting them frees about 4.0 GB but disables local AI until you switch that slot back to Gemini or re-download."
                : "Frees about 4.0 GB from ~/Library/Caches/2nb/models. You can re-download any time."
            alert.addButton(withTitle: "Delete")
            alert.addButton(withTitle: "Cancel")
            guard alert.runModal() == .alertFirstButtonReturn else { return }
        }
        #endif
        Task {
            busy = true
            defer { busy = false }
            clearAllSlotProbes()
            selfTest = nil
            testError = nil
            do {
                _ = try await appState.deleteLocalModels(LocalGemmaCatalog.allModelIDs)
                message = "Deleted local models. Freed ~4.0 GB."
                await reload()
            } catch {
                message = error.localizedDescription
            }
        }
    }

    private func applyButtonCommand(_ cmd: GUIButtonCommand?) {
        guard let cmd else { return }
        switch cmd.targetIdentifier {
        case "settings.genProvider.gemini":
            guard !busy, appState.vault != nil else {
                appState.guiInputBridge.acknowledgeButtonDisabled(cmd.id, label: cmd.targetIdentifier)
                return
            }
            appState.guiInputBridge.acknowledgeButtonClicked(cmd.id, label: AIProviderChoice.gemini.label)
            selectedGenProvider = .gemini
            Task { await applyGenerationSlot(provider: "gemini", modelID: selectedGenModel) }
        case "settings.genProvider.local":
            guard !busy, appState.vault != nil else {
                appState.guiInputBridge.acknowledgeButtonDisabled(cmd.id, label: cmd.targetIdentifier)
                return
            }
            appState.guiInputBridge.acknowledgeButtonClicked(cmd.id, label: AIProviderChoice.localGemma.label)
            selectedGenProvider = .localGemma
            Task { await applyGenerationSlot(provider: "llama-local", modelID: LocalGemmaCatalog.defaultGenModel) }
        case "settings.embedProvider.gemini":
            guard !busy, appState.vault != nil else {
                appState.guiInputBridge.acknowledgeButtonDisabled(cmd.id, label: cmd.targetIdentifier)
                return
            }
            appState.guiInputBridge.acknowledgeButtonClicked(cmd.id, label: AIProviderChoice.gemini.label)
            selectedEmbedProvider = .gemini
            Task { await applyEmbeddingSlot(provider: "gemini", modelID: selectedEmbedModel, dimensions: GeminiSettingsCatalog.defaultDimensions) }
        case "settings.embedProvider.local":
            guard !busy, appState.vault != nil else {
                appState.guiInputBridge.acknowledgeButtonDisabled(cmd.id, label: cmd.targetIdentifier)
                return
            }
            appState.guiInputBridge.acknowledgeButtonClicked(cmd.id, label: AIProviderChoice.localGemma.label)
            selectedEmbedProvider = .localGemma
            activeLocalEmbedModel = LocalGemmaCatalog.defaultEmbedModel
            Task { await applyEmbeddingSlot(provider: "llama-local", modelID: LocalGemmaCatalog.defaultEmbedModel, dimensions: selectedMatryoshkaDim) }
        case "settings.upgradeEmbeddingGemma2":
            guard !busy, appState.vault != nil else {
                appState.guiInputBridge.acknowledgeButtonDisabled(cmd.id, label: cmd.targetIdentifier)
                return
            }
            appState.guiInputBridge.acknowledgeButtonClicked(cmd.id, label: "Switch to EmbeddingGemma 2")
            activeLocalEmbedModel = LocalGemmaCatalog.defaultEmbedModel
            Task { await applyEmbeddingSlot(provider: "llama-local", modelID: LocalGemmaCatalog.defaultEmbedModel, dimensions: selectedMatryoshkaDim) }
        case "settings.reembedNow":
            guard appState.vault != nil, !appState.isIndexing else {
                appState.guiInputBridge.acknowledgeButtonDisabled(cmd.id, label: cmd.targetIdentifier)
                return
            }
            appState.guiInputBridge.acknowledgeButtonClicked(cmd.id, label: "Re-embed Vault Now")
            appState.startForceReembed()
        case "settings.openAIUsage":
            guard appState.vault != nil else {
                appState.guiInputBridge.acknowledgeButtonDisabled(cmd.id, label: cmd.targetIdentifier)
                return
            }
            appState.guiInputBridge.acknowledgeButtonClicked(cmd.id, label: "View Full Usage & Cost…")
            appState.showAIUsage = true
        case "settings.resetAIUsage":
            guard appState.vault != nil, !busy, !appState.isIndexing else {
                appState.guiInputBridge.acknowledgeButtonDisabled(cmd.id, label: cmd.targetIdentifier)
                return
            }
            appState.guiInputBridge.acknowledgeButtonClicked(cmd.id, label: "Reset Usage History…")
            confirmAndResetUsageHistory()
        case "settings.testGeneration":
            guard !busy, !testing, !testingGen, !testingEmbed else {
                appState.guiInputBridge.acknowledgeButtonDisabled(cmd.id, label: cmd.targetIdentifier)
                return
            }
            appState.guiInputBridge.acknowledgeButtonClicked(cmd.id, label: "Test Generation Model")
            Task { await runGenerationTest() }
        case "settings.testEmbedding":
            guard !busy, !testing, !testingGen, !testingEmbed else {
                appState.guiInputBridge.acknowledgeButtonDisabled(cmd.id, label: cmd.targetIdentifier)
                return
            }
            appState.guiInputBridge.acknowledgeButtonClicked(cmd.id, label: "Test Embedding Model")
            Task { await runEmbeddingTest() }
        case "settings.testConnection":
            guard !busy, !testing, !testingGen, !testingEmbed else {
                appState.guiInputBridge.acknowledgeButtonDisabled(cmd.id, label: cmd.targetIdentifier)
                return
            }
            appState.guiInputBridge.acknowledgeButtonClicked(cmd.id, label: "Test AI Connection")
            Task { await runTest() }
        case "settings.testVertexAI":
            guard !busy, !testing, !testingGen, !testingEmbed,
                  !geminiProject.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else {
                appState.guiInputBridge.acknowledgeButtonDisabled(cmd.id, label: cmd.targetIdentifier)
                return
            }
            appState.guiInputBridge.acknowledgeButtonClicked(cmd.id, label: "Test Vertex AI / ADC")
            Task { await testVertexAICredentials() }
        case let target where target.hasPrefix("settings.genModel."):
            let modelID = String(target.dropFirst("settings.genModel.".count))
            guard !busy, appState.vault != nil, selectedGenProvider == .gemini, !modelID.isEmpty else {
                appState.guiInputBridge.acknowledgeButtonDisabled(cmd.id, label: cmd.targetIdentifier)
                return
            }
            appState.guiInputBridge.acknowledgeButtonClicked(cmd.id, label: modelID)
            selectedGenModel = modelID
            Task { await applyGenerationModel(modelID) }
        case let target where target.hasPrefix("settings.embedModel."):
            let modelID = String(target.dropFirst("settings.embedModel.".count))
            guard !busy, appState.vault != nil, selectedEmbedProvider == .gemini, !modelID.isEmpty else {
                appState.guiInputBridge.acknowledgeButtonDisabled(cmd.id, label: cmd.targetIdentifier)
                return
            }
            appState.guiInputBridge.acknowledgeButtonClicked(cmd.id, label: modelID)
            selectedEmbedModel = modelID
            Task { await applyEmbeddingModel(modelID) }
        case let target where target.hasPrefix("settings.matryoshka."):
            let dimStr = String(target.dropFirst("settings.matryoshka.".count))
            guard !busy, appState.vault != nil, selectedEmbedProvider == .localGemma,
                  let dim = Int(dimStr),
                  LocalGemmaCatalog.matryoshkaDimensions.contains(where: { $0.id == dim }) else {
                appState.guiInputBridge.acknowledgeButtonDisabled(cmd.id, label: cmd.targetIdentifier)
                return
            }
            appState.guiInputBridge.acknowledgeButtonClicked(cmd.id, label: "\(dim)d")
            selectedMatryoshkaDim = dim
            Task { await applyMatryoshkaDimension(dim) }
        default:
            break
        }
    }

    // MARK: - Actions

    private func reload() async {
        if reloading { reloadPending = true; return }
        reloading = true
        defer {
            reloading = false
            if reloadPending { reloadPending = false; Task { await reload() } }
        }

        storedGeminiKey = appState.getStoredGeminiKey() ?? ""

        guard appState.vault != nil else {
            status = nil
            activeLocalEmbedModel = LocalGemmaCatalog.defaultEmbedModel
            return
        }

        status = try? await appState.fetchAIStatus()
        await appState.refreshMetrics()

        if let status {
            selectedGenProvider = (status.effectiveGenerationProvider == "llama-local") ? .localGemma : .gemini
            selectedEmbedProvider = (status.effectiveEmbeddingProvider == "llama-local") ? .localGemma : .gemini
        }

        if let rawDim = try? await appState.getConfigValue("ai.dimensions"),
           let dim = Int(rawDim.trimmingCharacters(in: .whitespacesAndNewlines)),
           LocalGemmaCatalog.matryoshkaDimensions.contains(where: { $0.id == dim }) {
            selectedMatryoshkaDim = dim
        }

        if let rawPrefix = try? await appState.getConfigValue("ai.llama.task_prefix") {
            let normalized = rawPrefix.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
            if !normalized.isEmpty {
                selectedTaskPrefix = normalized == "symmetric" ? "none" : normalized
            }
        }

        let project = (try? await appState.getConfigValue("ai.gemini.project")) ?? ""
        savedGeminiProject = project == "0" ? "" : project
        geminiProject = savedGeminiProject

        let rawLocation = (try? await appState.getConfigValue("ai.gemini.location")) ?? ""
        let effectiveLocation = (rawLocation.isEmpty || rawLocation == "0")
            ? GoogleRegions.defaultRegion
            : rawLocation
        if GoogleRegions.isCommon(effectiveLocation) {
            googleRegionSelection = effectiveLocation
            customRegion = ""
        } else {
            googleRegionSelection = Self.customRegionTag
            customRegion = effectiveLocation
        }

        if let status, status.effectiveGenerationProvider != "llama-local", !status.genModel.isEmpty {
            selectedGenModel = status.genModel
        }
        if let status, status.effectiveEmbeddingProvider != "llama-local", !status.embeddingModel.isEmpty {
            selectedEmbedModel = status.embeddingModel
        }
        if let status, status.effectiveEmbeddingProvider == "llama-local", !status.embeddingModel.isEmpty {
            activeLocalEmbedModel = status.embeddingModel
        } else {
            activeLocalEmbedModel = LocalGemmaCatalog.defaultEmbedModel
        }

        let rawThinking = (try? await appState.getConfigValue("ai.reasoning_effort")) ?? ""
        selectedThinking = GeminiSettingsCatalog.sanitizedThinking(rawThinking, for: selectedGenModel)

        if !savedGeminiProject.isEmpty {
            geminiAuthMode = .vertexAI
        } else {
            geminiAuthMode = .apiKey
        }
    }

    private func switchVaultToGemini() async {
        busy = true
        defer { busy = false }
        clearAllSlotProbes()
        selfTest = nil
        testError = nil
        do {
            let location = googleRegionSelection == Self.customRegionTag
                ? customRegion.trimmingCharacters(in: .whitespacesAndNewlines)
                : googleRegionSelection
            try await appState.saveAIConfig(
                provider: "gemini",
                embedModel: GeminiSettingsCatalog.defaultEmbeddingModel,
                genModel: GeminiSettingsCatalog.defaultGenerationModel,
                dims: 768,
                geminiProject: geminiProject.trimmingCharacters(in: .whitespacesAndNewlines),
                geminiLocation: location.isEmpty ? GoogleRegions.defaultRegion : location
            )
            message = "Switched vault provider to Google Gemini (Gemini 4 Hydrogen + Gemini Embedding 2)."
            await reload()
        } catch {
            message = error.localizedDescription
        }
    }

    private func saveGeminiProject() async {
        let project = geminiProject.trimmingCharacters(in: .whitespacesAndNewlines)
        busy = true
        defer { busy = false }
        clearAllSlotProbes()
        selfTest = nil
        testError = nil
        do {
            try await appState.setConfigValue("ai.gemini.project", project)
            savedGeminiProject = project
            message = project.isEmpty
                ? "GCP Project cleared (using Gemini API Key BYOK)."
                : "GCP Project set to \(project)."
            await reload()
        } catch {
            message = "Failed to save project: \(error.localizedDescription)"
        }
    }

    private func clearGeminiProject() async {
        geminiProject = ""
        await saveGeminiProject()
    }

    private func applyGoogleRegion(_ region: String) async {
        let target = region.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !target.isEmpty else { return }

        let explanation: String?
        switch GoogleRegions.risk(forRegion: target, embeddingModel: selectedEmbedModel) {
        case .safe:
            explanation = nil
        case .breaks(let text), .unverifiable(let text):
            explanation = text
        }
        if let explanation {
            #if canImport(AppKit)
            let alert = NSAlert()
            alert.messageText = "Switch GCP region to \(target)?"
            alert.informativeText = explanation
            alert.alertStyle = .warning
            alert.addButton(withTitle: "Cancel")
            alert.addButton(withTitle: "Switch anyway")
            guard alert.runModal() == .alertSecondButtonReturn else {
                await reload()
                return
            }
            #endif
        }

        busy = true
        defer { busy = false }
        clearAllSlotProbes()
        selfTest = nil
        testError = nil
        do {
            try await appState.setConfigValue("ai.gemini.location", target)
            message = "Google Cloud region set to \(target)."
            await reload()
        } catch {
            message = error.localizedDescription
            await reload()
        }
    }

    private func saveGeminiKey() async {
        busy = true
        defer { busy = false }
        clearAllSlotProbes()
        let key = newGeminiKey.trimmingCharacters(in: .whitespacesAndNewlines)
        if key.contains("GOOGLE_API_KEY") {
            message = "2nb uses GEMINI_API_KEY, not GOOGLE_API_KEY. Please paste the API key value itself."
            return
        }

        testing = true
        testError = nil
        let report: SelfTestReport
        do {
            report = try await appState.probeCredentials(provider: "gemini", apiKey: key)
        } catch {
            testing = false
            selfTest = nil
            selfTestProviders = nil
            testError = error.localizedDescription
            message = "The key was not saved — it could not be checked."
            return
        }
        testing = false
        selfTestProviders = (gen: report.provider, embed: report.provider)
        selfTest = report

        let verdict = CredentialVerdict(report.credentials)
        let persist = KeyPersistence.shouldPersistProbedKey(
            verdict: verdict,
            hasExistingKey: !storedGeminiKey.isEmpty
        )
        guard persist else {
            message = KeyPersistence.message(
                verdict: verdict,
                persisted: false,
                provider: report.provider
            )
            return
        }

        do {
            try await appState.saveGeminiKey(key)
            if appState.vault != nil && !savedGeminiProject.isEmpty {
                try await appState.setConfigValue("ai.gemini.project", "")
                geminiProject = ""
                savedGeminiProject = ""
            }
            storedGeminiKey = key
            newGeminiKey = ""
            enteringGeminiKey = false
            message = KeyPersistence.message(
                verdict: verdict,
                persisted: true,
                provider: report.provider
            ) ?? "Gemini API key saved."
            await reload()
        } catch {
            message = error.localizedDescription
        }
    }

    private func clearGeminiKey() async {
        busy = true
        defer { busy = false }
        clearAllSlotProbes()
        testError = nil
        do {
            try await appState.clearGeminiKey()
            storedGeminiKey = ""
            selfTest = nil
            message = "Gemini API key removed."
        } catch {
            message = error.localizedDescription
        }
    }

    private func testVertexAICredentials() async {
        testing = true
        testError = nil
        defer { testing = false }
        let location = googleRegionSelection == Self.customRegionTag
            ? customRegion.trimmingCharacters(in: .whitespacesAndNewlines)
            : googleRegionSelection
        do {
            let report = try await appState.probeCredentials(
                provider: "gemini",
                project: geminiProject.trimmingCharacters(in: .whitespacesAndNewlines),
                location: location.isEmpty ? GoogleRegions.defaultRegion : location
            )
            selfTestProviders = (gen: report.provider, embed: report.provider)
            selfTest = report
        } catch {
            selfTest = nil
            selfTestProviders = nil
            testError = error.localizedDescription
        }
        if appState.vault != nil {
            await appState.refreshAIStatus()
            status = appState.aiStatus
        }
    }

    private func applyGenerationSlot(provider: String, modelID: String) async {
        busy = true
        defer { busy = false }
        clearGenProbe()
        do {
            try await appState.setGenerationSlot(provider: provider, modelID: modelID)
            if provider == "gemini" {
                let sanitized = GeminiSettingsCatalog.sanitizedThinking(selectedThinking, for: modelID)
                if sanitized != selectedThinking {
                    selectedThinking = sanitized
                    try await appState.setConfigValue("ai.reasoning_effort", sanitized)
                }
            }
            message = "Generation set to \(ProviderDisplay.name(provider)) · \(modelID)."
            await reload()
        } catch {
            message = error.localizedDescription
            await reload()
        }
    }

    private func applyEmbeddingSlot(provider: String, modelID: String, dimensions: Int) async {
        busy = true
        defer { busy = false }
        clearEmbedProbe()
        do {
            try await appState.setEmbeddingSlot(provider: provider, modelID: modelID, dimensions: dimensions)
            message = "Embeddings set to \(ProviderDisplay.name(provider)) · \(modelID) (\(dimensions)d). Run Re-embed Vault Now or Sync Index to update vectors."
            await reload()
        } catch {
            message = error.localizedDescription
            await reload()
        }
    }

    private func applyGenerationModel(_ modelID: String) async {
        await applyGenerationSlot(provider: "gemini", modelID: modelID)
    }

    private func applyThinkingDepth(_ level: String) async {
        busy = true
        defer { busy = false }
        clearGenProbe()
        let sanitized = GeminiSettingsCatalog.sanitizedThinking(level, for: selectedGenModel)
        do {
            try await appState.setConfigValue("ai.reasoning_effort", sanitized)
            message = sanitized.isEmpty
                ? "Thinking depth reset to Model Default."
                : "Thinking depth set to \(sanitized)."
            await reload()
        } catch {
            message = error.localizedDescription
            await reload()
        }
    }

    private func applyEmbeddingModel(_ modelID: String) async {
        await applyEmbeddingSlot(provider: "gemini", modelID: modelID, dimensions: GeminiSettingsCatalog.defaultDimensions)
    }

    private var currentGenProviderID: String {
        selectedGenProvider.rawValue
    }

    private var currentGenModelID: String {
        selectedGenProvider == .localGemma ? LocalGemmaCatalog.defaultGenModel : selectedGenModel
    }

    private var currentEmbedProviderID: String {
        selectedEmbedProvider.rawValue
    }

    private var currentEmbedModelID: String {
        selectedEmbedProvider == .localGemma ? activeLocalEmbedModel : selectedEmbedModel
    }

    private func clearGenProbe() {
        genProbeResult = nil
        genProbeError = nil
        selfTest = nil
        selfTestProviders = nil
        testError = nil
    }

    private func clearEmbedProbe() {
        embedProbeResult = nil
        embedProbeError = nil
        selfTest = nil
        selfTestProviders = nil
        testError = nil
    }

    private func clearAllSlotProbes() {
        clearGenProbe()
        clearEmbedProbe()
    }

    private func guidanceRegion(for provider: String) -> String? {
        guard provider == "gemini" || provider == "google" else { return nil }
        let proj = (savedGeminiProject.isEmpty ? geminiProject : savedGeminiProject)
            .trimmingCharacters(in: .whitespacesAndNewlines)
        return proj.isEmpty ? nil : proj
    }

    @ViewBuilder
    private func slotProbeResultView(
        result: AIProbeResult?,
        errorMessage: String?,
        isEmbedding: Bool
    ) -> some View {
        if let errorMessage {
            Label(errorMessage, systemImage: "exclamationmark.triangle.fill")
                .font(.caption)
                .foregroundStyle(.orange)
        }
        if let r = result {
            let slotResult = ModelSlotTestResult.fromProbe(r, slot: isEmbedding ? .embedding : .generation)
            VStack(alignment: .leading, spacing: 4) {
                if r.ok {
                    let title = r.latency.isEmpty
                        ? "\(r.modelID) responded"
                        : "\(r.modelID) responded in \(r.latency)"
                    Label(title, systemImage: "checkmark.circle.fill")
                        .font(.caption.weight(.medium))
                        .foregroundStyle(.green)
                    if let proof = slotResult.proofLabel {
                        Text(proof)
                            .font(.caption2.monospaced())
                            .foregroundStyle(.secondary)
                            .textSelection(.enabled)
                    }
                } else {
                    let latSuffix = r.latency.isEmpty ? "" : " (\(r.latency))"
                    let summary = !slotResult.responseProof.isEmpty
                        ? "\(r.modelID) failed\(latSuffix): \(slotResult.responseProof)"
                        : "\(r.modelID) failed\(latSuffix)"
                    Label(summary, systemImage: "xmark.circle.fill")
                        .font(.caption.weight(.medium))
                        .foregroundStyle(.red)
                        .textSelection(.enabled)
                    guidanceView(
                        code: r.errorCode,
                        provider: r.provider,
                        remediation: r.remediation,
                        strategy: r.invokeStrategy
                    )
                }
            }
        }
    }

    private func runGenerationTest() async {
        testingGen = true
        genProbeError = nil
        genProbeResult = nil
        defer { testingGen = false }
        do {
            genProbeResult = try await appState.testModel(
                provider: currentGenProviderID,
                modelID: currentGenModelID,
                modelType: "generation"
            )
        } catch {
            genProbeError = error.localizedDescription
        }
    }

    private func runEmbeddingTest() async {
        testingEmbed = true
        embedProbeError = nil
        embedProbeResult = nil
        defer { testingEmbed = false }
        do {
            embedProbeResult = try await appState.testModel(
                provider: currentEmbedProviderID,
                modelID: currentEmbedModelID,
                modelType: "embedding"
            )
        } catch {
            embedProbeError = error.localizedDescription
        }
    }

    private func runTest() async {
        testing = true
        testError = nil
        defer { testing = false }
        do {
            if appState.vault != nil && geminiAuthMode == .vertexAI {
                let trimmedProject = geminiProject.trimmingCharacters(in: .whitespacesAndNewlines)
                if !trimmedProject.isEmpty && trimmedProject != savedGeminiProject {
                    try await appState.setConfigValue("ai.gemini.project", trimmedProject)
                    savedGeminiProject = trimmedProject
                }
            }
            let extraEnv: [String: String]? = (appState.vault == nil && selectedGenProvider == .localGemma && selectedEmbedProvider == .localGemma)
                ? ["2NB_DOCTOR_PROBE_PROVIDER": "llama-local"]
                : nil
            let report = try await appState.runDoctorSelfTest(extraEnvironment: extraEnv)
            let genProv = extraEnv == nil
                ? AppState.resolvedSlotProvider(from: report, slot: .generation, fallback: currentGenProviderID)
                : report.provider
            let embedProv = extraEnv == nil
                ? AppState.resolvedSlotProvider(from: report, slot: .embedding, fallback: currentEmbedProviderID)
                : report.provider
            selfTestProviders = (gen: genProv, embed: embedProv)
            selfTest = report

            let slots = SelfTestPresentation.extractSlotResults(report, genProvider: genProv, embedProvider: embedProv)
            if let genSlot = slots.first(where: { $0.slot == .generation }) {
                genProbeError = nil
                genProbeResult = AIProbeResult(
                    modelID: genSlot.modelID.isEmpty ? currentGenModelID : genSlot.modelID,
                    provider: genSlot.provider,
                    modelType: "generation",
                    ok: genSlot.ok,
                    detail: genSlot.responseProof.isEmpty ? nil : genSlot.responseProof,
                    latency: genSlot.latency ?? "",
                    errorCode: genSlot.errorCode,
                    remediation: genSlot.remediation,
                    invokeStrategy: nil
                )
            }
            if let embedSlot = slots.first(where: { $0.slot == .embedding }) {
                embedProbeError = nil
                embedProbeResult = AIProbeResult(
                    modelID: embedSlot.modelID.isEmpty ? currentEmbedModelID : embedSlot.modelID,
                    provider: embedSlot.provider,
                    modelType: "embedding",
                    ok: embedSlot.ok,
                    detail: embedSlot.responseProof.isEmpty ? nil : embedSlot.responseProof,
                    latency: embedSlot.latency ?? "",
                    errorCode: embedSlot.errorCode,
                    remediation: embedSlot.remediation,
                    invokeStrategy: nil
                )
            }
        } catch {
            selfTest = nil
            selfTestProviders = nil
            testError = error.localizedDescription
        }
        if appState.vault != nil {
            await appState.refreshAIStatus()
            status = appState.aiStatus
        }
    }

    // MARK: - AI Usage & Cloud Cost

    private var aiUsageSection: some View {
        Section("AI Usage & Estimated Cloud Cost") {
            if let usage = appState.metricsReport?.aiUsage {
                let u30 = usage.last30d
                LabeledContent("30-Day Cloud Cost") {
                    Text(Formatters.usd(u30.cloudCostUSD, knownPricing: u30.knownPricing))
                        .font(.body.monospacedDigit().weight(.semibold))
                }
                LabeledContent("30-Day Calls") {
                    Text("\(u30.cloudCalls) cloud · \(u30.localCalls) on-device ($0.00)")
                        .font(.callout.monospacedDigit())
                        .foregroundStyle(.secondary)
                }
                LabeledContent("30-Day Tokens") {
                    Text("\(Formatters.tokens(u30.cloudInputTokens + u30.cloudOutputTokens)) cloud · \(Formatters.tokens(u30.localTokens)) on-device")
                        .font(.callout.monospacedDigit())
                        .foregroundStyle(.secondary)
                }
            } else {
                Text("No AI operations recorded yet for this vault.")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }

            if let est = appState.metricsReport?.reembedEstimate, est.embeddableDocs > 0 {
                LabeledContent("Full Re-embed Estimate") {
                    let costLabel = est.isCloud
                        ? Formatters.usd(est.estimatedCostUSD, knownPricing: est.knownPricing)
                        : "$0.00 on-device"
                    Text("~\(Formatters.eta(est.estimatedSeconds)) · \(est.embeddableDocs) notes (\(est.totalChunks) chunks) · \(costLabel)")
                        .font(.caption.monospacedDigit())
                        .foregroundStyle(.secondary)
                }
            }

            HStack(spacing: 10) {
                Button("View Full Usage & Cost…") {
                    appState.showAIUsage = true
                }
                .controlSize(.small)
                .accessibilityIdentifier("settings.openAIUsage")

                Spacer()

                Button("Reset Usage History…") {
                    confirmAndResetUsageHistory()
                }
                .controlSize(.small)
                .accessibilityIdentifier("settings.resetAIUsage")
                .disabled(busy || appState.isIndexing)
            }
        }
    }

    private func confirmAndResetUsageHistory() {
        #if canImport(AppKit)
        if ProcessInfo.processInfo.environment["2NB_TEST"] != "1" && LaunchOptions.current.guiControlDir == nil {
            let alert = NSAlert()
            alert.messageText = "Reset AI usage and metrics history?"
            alert.informativeText = "Clears recorded AI call counts, token totals, and historical speed samples in metrics.db for this vault."
            alert.addButton(withTitle: "Reset")
            alert.addButton(withTitle: "Cancel")
            guard alert.runModal() == .alertFirstButtonReturn else { return }
        }
        #endif
        Task {
            busy = true
            defer { busy = false }
            do {
                try await appState.clearMetricsHistory()
                message = "Cleared AI usage and metrics history."
            } catch {
                message = error.localizedDescription
            }
        }
    }

    private static func maskedKey(_ key: String) -> String {
        guard key.count > 4 else { return "••••••••" }
        return "••••••••" + key.suffix(4)
    }
}
