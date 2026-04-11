import SwiftUI

struct AISetupWizardView: View {
    @Environment(AppState.self) var appState
    @Binding var isPresented: Bool

    enum Step { case provider, credentials, models, testSave }

    @State private var step: Step = .provider
    @State private var selectedProvider: String?

    // Credentials
    @State private var bedrockProfile = ""
    @State private var bedrockRegion = "us-east-1"
    @State private var openrouterKey = ""
    @State private var ollamaStatus = "checking" // "checking", "ready", "not-running", "not-installed"

    // Models
    @State private var useEasyMode = true
    @State private var embedModelID = ""
    @State private var genModelID = ""
    @State private var embedDims = 0
    @State private var availableModels: [CatalogModelInfo] = []
    @State private var isLoadingModels = false

    // Test & Save
    @State private var isSaving = false
    @State private var saveStep = ""
    @State private var probeResults: [AIProbeResult] = []
    @State private var saveError: String?
    @State private var setupComplete = false

    @FocusState private var focusedField: String?

    var body: some View {
        VStack(spacing: 0) {
            header
            Divider()
            content
                .frame(maxWidth: .infinity, maxHeight: .infinity)
            Divider()
            footer
        }
        .frame(width: 560, height: 480)
        .background(.regularMaterial)
        .clipShape(RoundedRectangle(cornerRadius: 12))
        .shadow(radius: 20)
        .onKeyPress(.escape) { isPresented = false; return .handled }
    }

    // MARK: - Header

    @ViewBuilder
    private var header: some View {
        HStack {
            Image(systemName: "wand.and.sparkles")
                .foregroundStyle(.secondary)
            Text(headerTitle)
                .font(.title3)
                .fontWeight(.medium)
            Spacer()
            stepIndicator
        }
        .padding(12)
    }

    private var headerTitle: String {
        switch step {
        case .provider: return "Set Up AI"
        case .credentials: return "Credentials"
        case .models: return "Choose Models"
        case .testSave: return "Testing & Saving"
        }
    }

    @ViewBuilder
    private var stepIndicator: some View {
        HStack(spacing: 4) {
            ForEach(0..<4) { i in
                Circle()
                    .fill(i <= stepIndex ? Color.accentColor : Color.secondary.opacity(0.3))
                    .frame(width: 6, height: 6)
            }
        }
    }

    private var stepIndex: Int {
        switch step {
        case .provider: return 0
        case .credentials: return 1
        case .models: return 2
        case .testSave: return 3
        }
    }

    // MARK: - Content

    @ViewBuilder
    private var content: some View {
        switch step {
        case .provider: providerStep
        case .credentials: credentialsStep
        case .models: modelsStep
        case .testSave: testSaveStep
        }
    }

    // MARK: - Step 1: Provider

    private var providerStep: some View {
        VStack(spacing: 16) {
            Text("Choose an AI provider for embeddings and generation.")
                .font(.callout)
                .foregroundStyle(.secondary)
                .frame(maxWidth: .infinity, alignment: .leading)

            HStack(spacing: 12) {
                providerCard(id: "bedrock", icon: "cloud", title: "AWS Bedrock",
                             subtitle: "Uses AWS SSO credentials\nClaude, Nova, Llama")
                providerCard(id: "openrouter", icon: "key", title: "OpenRouter",
                             subtitle: "API key, pay-per-token\nGemma 4, GPT-4o, Claude")
                providerCard(id: "ollama", icon: "desktopcomputer", title: "Ollama",
                             subtitle: "Free, fully local\nPrivate, no cloud calls")
            }
        }
        .padding(16)
    }

    private func providerCard(id: String, icon: String, title: String, subtitle: String) -> some View {
        Button {
            selectedProvider = id
            applyEasyDefaults()
            if id == "ollama" {
                Task {
                    ollamaStatus = "checking"
                    let status = await appState.checkOllamaStatus()
                    if !status.installed { ollamaStatus = "not-installed" }
                    else if !status.running { ollamaStatus = "not-running" }
                    else { ollamaStatus = "ready" }
                }
            }
            step = .credentials
        } label: {
            VStack(spacing: 8) {
                Image(systemName: icon)
                    .font(.title)
                    .foregroundStyle(.primary)
                Text(title)
                    .font(.headline)
                Text(subtitle)
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .multilineTextAlignment(.center)
            }
            .frame(maxWidth: .infinity)
            .padding(.vertical, 20)
            .background(Color(.quaternarySystemFill))
            .clipShape(RoundedRectangle(cornerRadius: 8))
        }
        .buttonStyle(.plain)
    }

    // MARK: - Step 2: Credentials

    @ViewBuilder
    private var credentialsStep: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 16) {
                switch selectedProvider {
                case "bedrock":
                    bedrockCredentials
                case "openrouter":
                    openrouterCredentials
                case "ollama":
                    ollamaCredentials
                default:
                    EmptyView()
                }
            }
            .padding(16)
        }
    }

    private var bedrockCredentials: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text("AWS credentials are read from your config files. Provide the profile and region for Bedrock access.")
                .font(.callout)
                .foregroundStyle(.secondary)

            LabeledContent("AWS Profile") {
                TextField("default", text: $bedrockProfile)
                    .textFieldStyle(.roundedBorder)
                    .frame(width: 200)
                    .focused($focusedField, equals: "profile")
            }
            LabeledContent("AWS Region") {
                TextField("us-east-1", text: $bedrockRegion)
                    .textFieldStyle(.roundedBorder)
                    .frame(width: 200)
                    .focused($focusedField, equals: "region")
            }

            Text("Ensure your AWS profile has bedrock:InvokeModel permissions.")
                .font(.caption)
                .foregroundStyle(.tertiary)
        }
        .onAppear { focusedField = "profile" }
    }

    private var openrouterCredentials: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text("Enter your OpenRouter API key. It will be stored securely in the macOS Keychain.")
                .font(.callout)
                .foregroundStyle(.secondary)

            LabeledContent("API Key") {
                SecureField("sk-or-...", text: $openrouterKey)
                    .textFieldStyle(.roundedBorder)
                    .frame(width: 280)
                    .focused($focusedField, equals: "key")
            }

            Link("Get your key at openrouter.ai/keys",
                 destination: URL(string: "https://openrouter.ai/keys")!)
                .font(.callout)
        }
        .onAppear { focusedField = "key" }
    }

    private var ollamaCredentials: some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack(spacing: 8) {
                Circle()
                    .fill(ollamaStatus == "ready" ? .green :
                          ollamaStatus == "checking" ? .yellow : .red)
                    .frame(width: 8, height: 8)
                Text(ollamaStatusText)
                    .font(.callout)
            }

            if ollamaStatus == "not-installed" {
                Text("brew install ollama")
                    .font(.system(.callout, design: .monospaced))
                    .padding(6)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .background(.quaternary)
                    .clipShape(RoundedRectangle(cornerRadius: 4))
            } else if ollamaStatus == "not-running" {
                Text("ollama serve")
                    .font(.system(.callout, design: .monospaced))
                    .padding(6)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .background(.quaternary)
                    .clipShape(RoundedRectangle(cornerRadius: 4))

                Button("Retry") {
                    Task {
                        ollamaStatus = "checking"
                        let status = await appState.checkOllamaStatus()
                        if !status.installed { ollamaStatus = "not-installed" }
                        else if !status.running { ollamaStatus = "not-running" }
                        else { ollamaStatus = "ready" }
                    }
                }
            } else if ollamaStatus == "ready" {
                Text("Ollama is running and ready.")
                    .font(.callout)
                    .foregroundStyle(.secondary)
            }
        }
    }

    private var ollamaStatusText: String {
        switch ollamaStatus {
        case "checking": return "Checking Ollama..."
        case "ready": return "Ollama is running"
        case "not-running": return "Ollama is installed but not running"
        case "not-installed": return "Ollama is not installed"
        default: return ollamaStatus
        }
    }

    private var credentialsNextDisabled: Bool {
        switch selectedProvider {
        case "openrouter": return openrouterKey.trimmingCharacters(in: .whitespaces).isEmpty
        case "ollama": return ollamaStatus != "ready"
        default: return false
        }
    }

    // MARK: - Step 3: Models

    private var modelsStep: some View {
        VStack(alignment: .leading, spacing: 12) {
            Picker("", selection: $useEasyMode) {
                Text("Easy").tag(true)
                Text("Custom").tag(false)
            }
            .pickerStyle(.segmented)
            .frame(width: 200)

            if useEasyMode {
                easyModeView
            } else {
                customModeView
            }
        }
        .padding(16)
        .onChange(of: useEasyMode) { _, isEasy in
            if isEasy {
                applyEasyDefaults()
            } else if availableModels.isEmpty {
                loadModels()
            }
        }
    }

    private var easyModeView: some View {
        VStack(alignment: .leading, spacing: 16) {
            Text("Recommended defaults for \(selectedProvider ?? ""):")
                .font(.callout)
                .foregroundStyle(.secondary)

            VStack(alignment: .leading, spacing: 8) {
                LabeledContent("Embedding") {
                    VStack(alignment: .trailing) {
                        Text(embedModelID).font(.callout).lineLimit(1)
                        if embedDims > 0 {
                            Text("\(embedDims)d").font(.caption).foregroundStyle(.tertiary)
                        }
                    }
                }
                LabeledContent("Generation") {
                    Text(genModelID).font(.callout).lineLimit(1)
                }
            }

            Text("These models are tested and work well with 2ndbrain.")
                .font(.caption)
                .foregroundStyle(.tertiary)
        }
    }

    @ViewBuilder
    private var customModeView: some View {
        if isLoadingModels {
            VStack(spacing: 12) {
                ProgressView()
                Text("Loading model catalog...")
                    .foregroundStyle(.secondary)
            }
            .frame(maxWidth: .infinity, maxHeight: .infinity)
        } else {
            VStack(alignment: .leading, spacing: 12) {
                Text("Embedding Model")
                    .font(.caption)
                    .fontWeight(.semibold)
                    .foregroundStyle(.secondary)

                Picker("", selection: $embedModelID) {
                    ForEach(availableModels.filter { $0.modelType == "embedding" }) { m in
                        Text(modelLabel(m)).tag(m.modelID)
                    }
                }

                Text("Generation Model")
                    .font(.caption)
                    .fontWeight(.semibold)
                    .foregroundStyle(.secondary)

                Picker("", selection: $genModelID) {
                    ForEach(availableModels.filter { $0.modelType == "generation" }) { m in
                        Text(modelLabel(m)).tag(m.modelID)
                    }
                }
            }
        }
    }

    private func modelLabel(_ m: CatalogModelInfo) -> String {
        let price = (m.priceIn ?? 0) == 0 ? "free" : String(format: "$%.2f/M", m.priceIn ?? 0)
        let dims = m.dimensions.map { $0 > 0 ? " \($0)d" : "" } ?? ""
        return "\(m.modelID) (\(price)\(dims))"
    }

    // MARK: - Step 4: Test & Save

    private var testSaveStep: some View {
        VStack(alignment: .leading, spacing: 12) {
            if isSaving {
                VStack(spacing: 12) {
                    ProgressView()
                    Text(saveStep)
                        .font(.callout)
                        .foregroundStyle(.secondary)
                }
                .frame(maxWidth: .infinity, maxHeight: .infinity)
            } else if let error = saveError {
                VStack(spacing: 12) {
                    Image(systemName: "xmark.circle.fill")
                        .font(.system(size: 36))
                        .foregroundStyle(.red)
                    Text("Setup failed")
                        .font(.headline)
                    Text(error)
                        .font(.callout)
                        .foregroundStyle(.secondary)
                        .multilineTextAlignment(.center)
                }
                .frame(maxWidth: .infinity, maxHeight: .infinity)
            } else {
                VStack(alignment: .leading, spacing: 8) {
                    ForEach(probeResults, id: \.modelID) { result in
                        HStack(spacing: 8) {
                            Image(systemName: result.ok ? "checkmark.circle.fill" : "xmark.circle.fill")
                                .foregroundStyle(result.ok ? .green : .red)
                            VStack(alignment: .leading) {
                                Text(result.modelID)
                                    .font(.callout)
                                    .lineLimit(1)
                                Text("\(result.modelType) — \(result.ok ? result.latency : (result.detail ?? "failed"))")
                                    .font(.caption)
                                    .foregroundStyle(.secondary)
                            }
                        }
                    }

                    if setupComplete {
                        Divider()
                            .padding(.vertical, 4)

                        HStack(spacing: 8) {
                            Image(systemName: "checkmark.circle.fill")
                                .foregroundStyle(.green)
                                .font(.title2)
                            VStack(alignment: .leading) {
                                Text("Configuration saved")
                                    .font(.headline)
                                Text("Provider: \(selectedProvider ?? "") — ready to use")
                                    .font(.caption)
                                    .foregroundStyle(.secondary)
                            }
                        }
                    }
                }
                .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
                .padding(16)
            }
        }
        .onAppear { runSetup() }
    }

    // MARK: - Footer

    @ViewBuilder
    private var footer: some View {
        HStack {
            if step != .provider && step != .testSave {
                Button("Back") {
                    switch step {
                    case .credentials: step = .provider
                    case .models: step = .credentials
                    default: break
                    }
                }
            }

            Spacer()

            switch step {
            case .provider:
                Button("Cancel") { isPresented = false }
            case .credentials:
                Button("Cancel") { isPresented = false }
                Button("Next") { step = .models }
                    .buttonStyle(.borderedProminent)
                    .disabled(credentialsNextDisabled)
            case .models:
                Button("Cancel") { isPresented = false }
                Button("Set Up") {
                    step = .testSave
                }
                .buttonStyle(.borderedProminent)
                .disabled(embedModelID.isEmpty || genModelID.isEmpty)
            case .testSave:
                if setupComplete {
                    Button("Rebuild Index Now") {
                        isPresented = false
                        appState.rebuildIndex()
                    }
                    Button("Done") { isPresented = false }
                        .buttonStyle(.borderedProminent)
                } else if saveError != nil {
                    Button("Back") { step = .models; saveError = nil; probeResults = [] }
                    Button("Close") { isPresented = false }
                } else {
                    Button("Cancel") { isPresented = false }
                        .disabled(isSaving)
                }
            }
        }
        .padding(12)
    }

    // MARK: - Logic

    private func applyEasyDefaults() {
        switch selectedProvider {
        case "bedrock":
            embedModelID = "amazon.nova-2-multimodal-embeddings-v1:0"
            genModelID = "amazon.nova-micro-v1:0"
            embedDims = 1024
        case "openrouter":
            embedModelID = "nvidia/llama-nemotron-embed-vl-1b-v2:free"
            genModelID = "google/gemma-4-31b-it:free"
            embedDims = 1024
        case "ollama":
            embedModelID = "nomic-embed-text"
            genModelID = "qwen2.5:0.5b"
            embedDims = 768
        default:
            break
        }
    }

    private func loadModels() {
        guard let provider = selectedProvider else { return }
        isLoadingModels = true
        Task {
            do {
                availableModels = try await appState.fetchModels(provider: provider)
            } catch {
                availableModels = []
            }
            isLoadingModels = false
        }
    }

    private func runSetup() {
        guard let provider = selectedProvider else { return }
        isSaving = true
        saveError = nil
        probeResults = []
        setupComplete = false

        Task {
            // Save config
            saveStep = "Saving configuration..."
            do {
                try await appState.saveAIConfig(
                    provider: provider,
                    embedModel: embedModelID,
                    genModel: genModelID,
                    dims: embedDims,
                    bedrockProfile: bedrockProfile,
                    bedrockRegion: bedrockRegion,
                    openrouterKey: openrouterKey
                )
            } catch {
                saveError = "Failed to save config: \(error.localizedDescription)"
                isSaving = false
                return
            }

            // Test embedding model
            saveStep = "Testing embedding model..."
            do {
                let result = try await appState.testModel(
                    provider: provider, modelID: embedModelID, modelType: "embedding"
                )
                probeResults.append(result)
            } catch {
                probeResults.append(AIProbeResult(
                    modelID: embedModelID, provider: provider, modelType: "embedding",
                    ok: false, detail: error.localizedDescription, latency: ""
                ))
            }

            // Test generation model
            saveStep = "Testing generation model..."
            do {
                let result = try await appState.testModel(
                    provider: provider, modelID: genModelID, modelType: "generation"
                )
                probeResults.append(result)
            } catch {
                probeResults.append(AIProbeResult(
                    modelID: genModelID, provider: provider, modelType: "generation",
                    ok: false, detail: error.localizedDescription, latency: ""
                ))
            }

            // Refresh status
            await appState.refreshAIStatus()
            isSaving = false
            setupComplete = true
        }
    }
}
