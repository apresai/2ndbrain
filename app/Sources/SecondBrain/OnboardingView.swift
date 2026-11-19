import SwiftUI
import SecondBrainCore
#if canImport(AppKit)
import AppKit
#endif

/// State machine for the 3-step first-run onboarding wizard.
/// Extracted so step transitions and gating can be verified directly in tests.
@Observable @MainActor
final class OnboardingState {
    enum Step: Int, CaseIterable, Identifiable {
        case vault = 0
        case gemini = 1
        case shortcutAndClients = 2

        var id: Int { rawValue }

        var title: String {
            switch self {
            case .vault: return "Obsidian Vault"
            case .gemini: return "Google Gemini"
            case .shortcutAndClients: return "Shortcut & AI Clients"
            }
        }

        var stepNumber: Int { rawValue + 1 }

        var next: Step? { Step(rawValue: rawValue + 1) }
        var previous: Step? { Step(rawValue: rawValue - 1) }
    }

    enum AuthMode: String, CaseIterable, Identifiable {
        case apiKey = "Gemini API Key"
        case vertexAI = "Vertex AI (GCP)"

        var id: String { rawValue }
    }

    var step: Step = .vault
    var authMode: AuthMode = .apiKey
    var hasBoundVault: Bool

    var canAdvanceFromVault: Bool { hasBoundVault }

    init(step: Step = .vault, hasBoundVault: Bool = false) {
        self.step = step
        self.hasBoundVault = hasBoundVault
    }

    @discardableResult
    func advance() -> Bool {
        if step == .vault && !canAdvanceFromVault {
            return false
        }
        guard let next = step.next else { return false }
        step = next
        return true
    }

    @discardableResult
    func back() -> Bool {
        guard let previous = step.previous else { return false }
        step = previous
        return true
    }

    func go(to target: Step) {
        if target.rawValue > Step.vault.rawValue && !canAdvanceFromVault {
            return
        }
        step = target
    }
}

/// First-run 3-step setup guide (`Vault` -> `Gemini` -> `Shortcut & AI Clients`).
/// Starts background indexing as soon as Step 1 advances with a bound vault so
/// embeddings populate while the user finishes setup.
struct OnboardingView: View {
    @Environment(AppState.self) private var appState
    @Environment(\.dismiss) private var dismiss

    @State private var flow = OnboardingState()
    @State private var detectedVaults: [ObsidianRegistry.Vault] = []

    // Step 2: Gemini BYOK / BYOP inputs
    @State private var apiKeyInput = ""
    @State private var storedGeminiKey = ""
    @State private var projectInput = ""
    @State private var locationInput = GoogleRegions.defaultRegion
    @State private var probingGemini = false
    @State private var geminiReport: SelfTestReport?
    @State private var geminiMessage: String?
    @State private var geminiIsError = false

    // Step 3: Shortcut test + AI client setup
    @State private var hotkeyTested = false
    @State private var configuringClientID: String?
    @State private var clientMessage: String?
    @State private var clientIsError = false

    private var isTestEnvironment: Bool {
        ProcessInfo.processInfo.environment["2NB_TEST"] == "1"
    }

    var body: some View {
        VStack(spacing: 0) {
            header
            Divider()

            ScrollView {
                VStack(alignment: .leading, spacing: 16) {
                    switch flow.step {
                    case .vault:
                        vaultStep
                    case .gemini:
                        geminiStep
                    case .shortcutAndClients:
                        shortcutAndClientsStep
                    }
                }
                .padding(24)
            }

            Divider()
            footer
        }
        .frame(width: 660, height: 540)
        .task {
            await loadSavedGeminiConfig()
        }
        .onAppear {
            syncInitialState()
        }
        .onChange(of: appState.vault != nil) { _, hasVault in
            flow.hasBoundVault = hasVault
        }
        .onChange(of: flow.step) { _, newStep in
            updateHotkeyTestHook(active: newStep == .shortcutAndClients)
        }
        .onDisappear {
            updateHotkeyTestHook(active: false)
        }
    }

    // MARK: - Header

    private var header: some View {
        HStack(spacing: 14) {
            VStack(alignment: .leading, spacing: 3) {
                HStack(spacing: 8) {
                    Text("Welcome to 2ndbrain")
                        .font(.title3.weight(.semibold))
                    BetaBadge()
                }
                Text("Step \(flow.step.stepNumber) of \(OnboardingState.Step.allCases.count): \(flow.step.title)")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }

            Spacer()

            HStack(spacing: 8) {
                ForEach(OnboardingState.Step.allCases) { step in
                    stepPill(step)
                }
            }
        }
        .padding(.horizontal, 24)
        .padding(.vertical, 14)
        .background(.bar)
    }

    private func stepPill(_ step: OnboardingState.Step) -> some View {
        let isCurrent = flow.step == step
        let isDone = step.rawValue < flow.step.rawValue
        return Button {
            flow.go(to: step)
        } label: {
            HStack(spacing: 5) {
                Image(systemName: isDone ? "checkmark.circle.fill" : "\(step.stepNumber).circle.fill")
                    .font(.caption)
                Text(step.title)
                    .font(.caption.weight(isCurrent ? .semibold : .regular))
            }
            .padding(.horizontal, 9)
            .padding(.vertical, 5)
            .background(
                Capsule()
                    .fill(isCurrent ? Color.accentColor.opacity(0.16) : Color.primary.opacity(0.05))
            )
            .foregroundStyle(isCurrent ? Color.accentColor : (isDone ? Color.primary : Color.secondary))
        }
        .buttonStyle(.plain)
        .disabled(step.rawValue > OnboardingState.Step.vault.rawValue && !flow.canAdvanceFromVault)
    }

    // MARK: - Step 1: Vault

    private var vaultStep: some View {
        VStack(alignment: .leading, spacing: 16) {
            VStack(alignment: .leading, spacing: 6) {
                Text("Choose your Obsidian vault")
                    .font(.headline)
                Text("2ndbrain stays local-first on your machine and mirrors the vault you have open in Obsidian.")
                    .font(.callout)
                    .foregroundStyle(.secondary)
            }

            if let vault = appState.vault {
                ThemedCard(style: .prominent) {
                    HStack(spacing: 12) {
                        Image(systemName: "checkmark.seal.fill")
                            .font(.title2)
                            .foregroundStyle(.green)
                        VStack(alignment: .leading, spacing: 3) {
                            Text(vault.rootURL.lastPathComponent)
                                .font(.headline)
                            Text(vault.rootURL.path)
                                .font(.caption.monospaced())
                                .foregroundStyle(.secondary)
                                .lineLimit(1)
                                .truncationMode(.middle)
                        }
                        Spacer()
                        Button("Change…") {
                            pickVaultFolder()
                        }
                    }
                }
            } else {
                ThemedCard(style: .standard) {
                    VStack(alignment: .leading, spacing: 12) {
                        if let openVault = detectedVaults.first(where: \.open) ?? detectedVaults.first {
                            HStack {
                                VStack(alignment: .leading, spacing: 2) {
                                    Text("Detected Obsidian vault")
                                        .font(.caption.weight(.semibold))
                                        .foregroundStyle(.secondary)
                                    Text(openVault.name)
                                        .font(.headline)
                                    Text(openVault.path)
                                        .font(.caption.monospaced())
                                        .foregroundStyle(.secondary)
                                        .lineLimit(1)
                                        .truncationMode(.middle)
                                }
                                Spacer()
                                Button("Use This Vault") {
                                    bindVault(at: openVault.url)
                                }
                                .buttonStyle(.borderedProminent)
                            }
                            Divider()
                        }

                        if detectedVaults.count > 1 {
                            Text("Other Obsidian vaults")
                                .font(.caption.weight(.semibold))
                                .foregroundStyle(.secondary)
                            ForEach(detectedVaults.dropFirst(), id: \.id) { item in
                                HStack {
                                    VStack(alignment: .leading, spacing: 2) {
                                        Text(item.name)
                                            .font(.callout.weight(.medium))
                                        Text(item.path)
                                            .font(.caption2.monospaced())
                                            .foregroundStyle(.secondary)
                                            .lineLimit(1)
                                            .truncationMode(.middle)
                                    }
                                    Spacer()
                                    Button("Use") {
                                        bindVault(at: item.url)
                                    }
                                    .controlSize(.small)
                                }
                            }
                            Divider()
                        }

                        HStack {
                            Text("Or pick any vault folder manually")
                                .font(.callout)
                                .foregroundStyle(.secondary)
                            Spacer()
                            Button("Browse Folder…") {
                                pickVaultFolder()
                            }
                        }
                    }
                }
            }

            ThemedCard(style: .subtle) {
                HStack(spacing: 10) {
                    Image(systemName: "bolt.horizontal.circle")
                        .foregroundStyle(.secondary)
                    Text("Once you click Continue, 2ndbrain starts building your BM25 keyword and vector index in the background while you finish setup.")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
            }
        }
    }

    // MARK: - Step 2: Gemini

    private var geminiStep: some View {
        VStack(alignment: .leading, spacing: 16) {
            VStack(alignment: .leading, spacing: 6) {
                Text("Connect Google Gemini (BYOK / BYOP)")
                    .font(.headline)
                Text("This open-source app uses your own Gemini credentials. Configure either a personal GEMINI_API_KEY in macOS Keychain or your own Google Cloud project for Vertex AI. Defaults: gemini-4-hydrogen + gemini-embedding-2 (768d).")
                    .font(.callout)
                    .foregroundStyle(.secondary)
            }

            Picker("Credential Mode", selection: $flow.authMode) {
                ForEach(OnboardingState.AuthMode.allCases) { mode in
                    Text(mode.rawValue).tag(mode)
                }
            }
            .pickerStyle(.segmented)

            ThemedCard(style: .standard) {
                VStack(alignment: .leading, spacing: 12) {
                    switch flow.authMode {
                    case .apiKey:
                        Text("Gemini Developer API Key")
                            .font(.subheadline.weight(.semibold))
                        if !storedGeminiKey.isEmpty {
                            HStack(spacing: 8) {
                                Image(systemName: "checkmark.circle.fill")
                                    .foregroundStyle(.green)
                                Text("Key saved in Keychain (paste a new key below to replace it).")
                                    .font(.caption)
                                    .foregroundStyle(.secondary)
                            }
                        }
                        SecureField("Paste your GEMINI_API_KEY…", text: $apiKeyInput)
                            .textFieldStyle(.roundedBorder)
                        HStack {
                            Text("Stored securely in macOS Keychain; never writes GOOGLE_API_KEY.")
                                .font(.caption)
                                .foregroundStyle(.secondary)
                            Spacer()
                            Button(probingGemini ? "Testing…" : "Test & Save Key") {
                                Task { await testAndSaveGeminiKey() }
                            }
                            .buttonStyle(.borderedProminent)
                            .disabled(probingGemini || apiKeyInput.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
                        }

                    case .vertexAI:
                        Text("Google Cloud Vertex AI")
                            .font(.subheadline.weight(.semibold))
                        TextField("your-gcp-project-id", text: $projectInput)
                            .textFieldStyle(.roundedBorder)
                        Picker("Location", selection: $locationInput) {
                            ForEach(GoogleRegions.common) { region in
                                Text(region.display).tag(region.id)
                            }
                        }
                        HStack {
                            Text("Authenticates via Application Default Credentials (gcloud auth application-default login).")
                                .font(.caption)
                                .foregroundStyle(.secondary)
                            Spacer()
                            Button(probingGemini ? "Testing…" : "Test & Save Project") {
                                Task { await testAndSaveVertexProject() }
                            }
                            .buttonStyle(.borderedProminent)
                            .disabled(probingGemini || projectInput.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
                        }
                    }

                    if let geminiReport {
                        Divider()
                        HStack(spacing: 8) {
                            Image(systemName: SelfTestPresentation.isFailure(geminiReport) ? "exclamationmark.triangle.fill" : "checkmark.circle.fill")
                                .foregroundStyle(SelfTestPresentation.isFailure(geminiReport) ? Color.orange : Color.green)
                            Text(SelfTestPresentation.headline(geminiReport))
                                .font(.callout.weight(.medium))
                        }

                        ForEach(SelfTestPresentation.onboardingChecks(geminiReport)) { check in
                            HStack(alignment: .firstTextBaseline, spacing: 6) {
                                Image(systemName: SelfTestPresentation.symbol(for: check))
                                    .foregroundStyle(check.ok ? (check.isWarning ? .orange : .green) : .red)
                                VStack(alignment: .leading, spacing: 2) {
                                    Text("\(check.name): \(check.detail)")
                                        .font(.caption)
                                    if let fix = check.fix, !check.ok || check.isWarning {
                                        Text(fix)
                                            .font(.caption2)
                                            .foregroundStyle(.secondary)
                                    }
                                }
                            }
                        }
                    }

                    if let geminiMessage, !geminiMessage.isEmpty {
                        Text(geminiMessage)
                            .font(.caption)
                            .foregroundStyle(geminiIsError ? .red : .secondary)
                    }
                }
            }

            if let status = appState.aiStatus, status.embedAvailable && status.genAvailable {
                ThemedCard(style: .subtle) {
                    HStack(spacing: 8) {
                        Image(systemName: "sparkles")
                            .foregroundStyle(Color.accentColor)
                        Text("Gemini is active: \(status.genModel) + \(status.embeddingModel) (\(status.dimensions)d).")
                            .font(.caption)
                    }
                }
            }
        }
    }

    // MARK: - Step 3: Shortcut & AI Clients

    private var shortcutAndClientsStep: some View {
        let hotkey = HotkeyManager.shared
        return VStack(alignment: .leading, spacing: 16) {
            VStack(alignment: .leading, spacing: 6) {
                Text("Quick Search shortcut & AI coding clients")
                    .font(.headline)
                Text("Summon the floating Ask & Search HUD from anywhere on macOS, or wire your AI coding assistants in one click.")
                    .font(.callout)
                    .foregroundStyle(.secondary)
            }

            ThemedCard(style: .standard) {
                VStack(alignment: .leading, spacing: 12) {
                    HStack(alignment: .top) {
                        VStack(alignment: .leading, spacing: 4) {
                            Text("Global Quick Search hotkey")
                                .font(.subheadline.weight(.semibold))
                            Text("Press your shortcut now to test it live.")
                                .font(.caption)
                                .foregroundStyle(.secondary)
                        }
                        Spacer()
                        VStack(alignment: .trailing, spacing: 6) {
                            ShortcutRecorder(
                                combo: hotkey.combo,
                                apply: { hotkey.apply($0) },
                                listening: { hotkey.setListening($0) }
                            )
                            Button("Reset to ⌃⇧Space") {
                                hotkey.resetToDefault()
                            }
                            .controlSize(.small)
                            .disabled(hotkey.combo?.spec == KeyCombo.defaultSpec)
                        }
                    }

                    if hotkeyTested {
                        HStack(spacing: 8) {
                            Image(systemName: "checkmark.circle.fill")
                                .foregroundStyle(.green)
                            Text("Hotkey works! Press Esc inside the HUD anytime to dismiss it.")
                                .font(.caption.weight(.medium))
                                .foregroundStyle(.green)
                        }
                        .transition(.opacity)
                    }

                    Divider()

                    HStack(spacing: 14) {
                        HStack(spacing: 5) {
                            Keycaps(caps: ["⇥"])
                            Text("Search / Ask")
                                .font(.caption)
                                .foregroundStyle(.secondary)
                        }
                        HStack(spacing: 5) {
                            Keycaps(caps: ["↑", "↓"])
                            Text("Navigate")
                                .font(.caption)
                                .foregroundStyle(.secondary)
                        }
                        HStack(spacing: 5) {
                            Keycaps(caps: ["Space"])
                            Text("Quick Look")
                                .font(.caption)
                                .foregroundStyle(.secondary)
                        }
                        HStack(spacing: 5) {
                            Keycaps(caps: ["↩"])
                            Text("Obsidian")
                                .font(.caption)
                                .foregroundStyle(.secondary)
                        }
                    }
                }
            }

            ThemedCard(style: .standard) {
                VStack(alignment: .leading, spacing: 10) {
                    Text("AI coding clients (optional)")
                        .font(.subheadline.weight(.semibold))

                    ForEach(ClientDescriptor.all) { client in
                        let status = ClientConfig.mcpRow(appState.mcpConfigured(forClient: client.mcpClientKey))
                        HStack(spacing: 10) {
                            Image(systemName: client.systemImage)
                                .frame(width: 18)
                                .foregroundStyle(.secondary)
                            VStack(alignment: .leading, spacing: 1) {
                                Text(client.displayName)
                                    .font(.callout.weight(.medium))
                                Text(status.label)
                                    .font(.caption2)
                                    .foregroundStyle(status.ok ? .green : .secondary)
                            }
                            Spacer()
                            if status.ok {
                                Image(systemName: "checkmark.circle.fill")
                                    .foregroundStyle(.green)
                            } else {
                                Button(configuringClientID == client.id ? "Wiring…" : "Connect") {
                                    Task { await configureClient(client) }
                                }
                                .controlSize(.small)
                                .disabled(configuringClientID != nil || appState.vault == nil)
                            }
                        }
                    }

                    if let clientMessage, !clientMessage.isEmpty {
                        Divider()
                        Text(clientMessage)
                            .font(.caption)
                            .foregroundStyle(clientIsError ? .red : .secondary)
                    }
                }
            }
        }
    }

    // MARK: - Footer

    private var footer: some View {
        HStack(spacing: 12) {
            indexingStatusPill

            Spacer()

            if flow.step != .vault {
                Button("Back") {
                    flow.back()
                }
            }

            if let next = flow.step.next {
                Button(flow.step == .gemini ? "Continue" : "Continue & Index") {
                    if flow.step == .vault {
                        persistBoundVaultAndStartIndex()
                    }
                    flow.go(to: next)
                }
                .buttonStyle(.borderedProminent)
                .disabled(flow.step == .vault && !flow.canAdvanceFromVault)
            } else {
                Button("Finish Setup") {
                    finishSetup()
                }
                .buttonStyle(.borderedProminent)
            }
        }
        .padding(.horizontal, 24)
        .padding(.vertical, 14)
        .background(.bar)
    }

    @ViewBuilder
    private var indexingStatusPill: some View {
        if appState.isIndexing {
            HStack(spacing: 8) {
                ProgressView(value: appState.indexFraction ?? 0.08)
                    .frame(width: 90)
                if let ep = appState.embeddingProgress, ep.total > 0 {
                    Text("Indexing \(ep.current)/\(ep.total)")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                        .monospacedDigit()
                } else {
                    Text("Indexing vault…")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
            }
        } else if let status = appState.aiStatus, status.documentCount > 0 {
            Label("\(status.documentCount) notes · \(status.embeddingCount) embedded", systemImage: "checkmark.circle")
                .font(.caption)
                .foregroundStyle(.secondary)
        } else {
            Button("Skip for now") {
                finishSetup()
            }
            .buttonStyle(.plain)
            .font(.caption)
            .foregroundStyle(.secondary)
        }
    }

    // MARK: - Actions

    private func syncInitialState() {
        flow.hasBoundVault = appState.vault != nil
        if !isTestEnvironment {
            detectedVaults = ObsidianRegistry.load()?.vaults.filter(\.exists) ?? []
        }
        storedGeminiKey = appState.getStoredGeminiKey() ?? ""
    }

    private func loadSavedGeminiConfig() async {
        guard appState.vault != nil else { return }
        if let project = try? await appState.getConfigValue("ai.gemini.project"),
           !project.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
            projectInput = project.trimmingCharacters(in: .whitespacesAndNewlines)
        }
        if let location = try? await appState.getConfigValue("ai.gemini.location"),
           !location.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
            locationInput = location.trimmingCharacters(in: .whitespacesAndNewlines)
        }
    }

    private func pickVaultFolder() {
        let panel = NSOpenPanel()
        panel.canChooseFiles = false
        panel.canChooseDirectories = true
        panel.allowsMultipleSelection = false
        panel.prompt = "Select Vault"
        if panel.runModal() == .OK, let url = panel.url {
            bindVault(at: url)
        }
    }

    private func bindVault(at url: URL) {
        if appState.openPickedVault(at: url) {
            flow.hasBoundVault = true
            if !isTestEnvironment {
                UserDefaults.standard.set(url.path, forKey: "lastVaultPath")
            }
        }
    }

    private func persistBoundVaultAndStartIndex() {
        guard let vault = appState.vault else { return }
        if !isTestEnvironment {
            UserDefaults.standard.set(vault.rootURL.path, forKey: "lastVaultPath")
        }
        if !appState.isIndexing {
            appState.startIndex()
        }
    }

    private func testAndSaveGeminiKey() async {
        let trimmed = apiKeyInput.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty else { return }
        probingGemini = true
        geminiMessage = nil
        geminiIsError = false
        defer { probingGemini = false }

        do {
            let report = try await appState.probeCredentials(provider: "gemini", apiKey: trimmed)
            geminiReport = report
            let verdict = CredentialVerdict(report.credentials)
            let persist = KeyPersistence.shouldPersistProbedKey(
                verdict: verdict,
                hasExistingKey: !storedGeminiKey.isEmpty
            )
            guard persist else {
                geminiMessage = KeyPersistence.message(verdict: verdict, persisted: false, provider: "Gemini")
                geminiIsError = true
                return
            }

            try await appState.saveGeminiKey(trimmed)
            storedGeminiKey = trimmed
            apiKeyInput = ""

            if appState.vault != nil {
                try await appState.saveAIConfig(
                    provider: GeminiSettingsCatalog.provider,
                    embedModel: GeminiSettingsCatalog.defaultEmbeddingModel,
                    genModel: GeminiSettingsCatalog.defaultGenerationModel,
                    dims: GeminiSettingsCatalog.defaultDimensions
                )
                if !appState.isIndexing {
                    appState.startIndex()
                }
            }
            geminiMessage = KeyPersistence.message(verdict: verdict, persisted: true, provider: "Gemini")
                ?? "Gemini API key saved with Gemini 4 defaults."
        } catch {
            geminiMessage = error.localizedDescription
            geminiIsError = true
        }
    }

    private func testAndSaveVertexProject() async {
        let trimmedProject = projectInput.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmedProject.isEmpty else { return }
        let trimmedLocation = locationInput.trimmingCharacters(in: .whitespacesAndNewlines)
        let resolvedLocation = trimmedLocation.isEmpty ? GoogleRegions.defaultRegion : trimmedLocation

        probingGemini = true
        geminiMessage = nil
        geminiIsError = false
        defer { probingGemini = false }

        do {
            let report = try await appState.probeCredentials(
                provider: "gemini",
                project: trimmedProject,
                location: resolvedLocation
            )
            geminiReport = report
            let verdict = CredentialVerdict(report.credentials)
            if verdict == .rejected {
                geminiMessage = "Vertex AI rejected credentials for \(trimmedProject)."
                geminiIsError = true
                return
            }

            if appState.vault != nil {
                try await appState.saveAIConfig(
                    provider: GeminiSettingsCatalog.provider,
                    embedModel: GeminiSettingsCatalog.defaultEmbeddingModel,
                    genModel: GeminiSettingsCatalog.defaultGenerationModel,
                    dims: GeminiSettingsCatalog.defaultDimensions,
                    geminiProject: trimmedProject,
                    geminiLocation: resolvedLocation
                )
                if !appState.isIndexing {
                    appState.startIndex()
                }
            }
            geminiMessage = "Saved Vertex AI project \(trimmedProject) (\(resolvedLocation))."
        } catch {
            geminiMessage = error.localizedDescription
            geminiIsError = true
        }
    }

    private func configureClient(_ client: ClientDescriptor) async {
        configuringClientID = client.id
        clientMessage = nil
        clientIsError = false
        defer { configuringClientID = nil }

        do {
            let results = try await appState.setupClient(client.mcpClientKey)
            let match = results.first { $0.client == client.mcpClientKey } ?? results.first
            switch ClientConfig.configureOutcome(client, result: match) {
            case .success(let msg), .manual(let msg):
                clientMessage = msg
                clientIsError = false
            case .failure(let msg):
                clientMessage = msg
                clientIsError = true
            }
            await appState.refreshMCPConfigured()
        } catch {
            clientMessage = error.localizedDescription
            clientIsError = true
        }
    }

    private func updateHotkeyTestHook(active: Bool) {
        guard !isTestEnvironment else { return }
        if active {
            HotkeyManager.shared.onboardingTestHook = {
                withAnimation(.easeInOut(duration: 0.15)) {
                    hotkeyTested = true
                }
            }
        } else {
            HotkeyManager.shared.onboardingTestHook = nil
        }
    }

    private func finishSetup() {
        updateHotkeyTestHook(active: false)
        if !isTestEnvironment {
            UserDefaults.standard.set(true, forKey: "hasCompletedOnboarding")
        }
        dismiss()
    }
}
