import SwiftUI
#if canImport(AppKit)
import AppKit
#endif

/// The AI page of the Settings window: the four things a user actually needs to
/// get working AI, and a button that proves it.
///
/// The AI Hub this replaces as a *settings* surface carries ~67 controls at
/// open. That is the right amount for browsing a model catalog and the wrong
/// amount for answering "where does my key go?". The catalog stays in the main
/// window, where a 980x700 browser belongs; this page keeps connection,
/// credentials, the two active models, and the verdict.
struct SettingsAIView: View {
    @Environment(AppState.self) var appState
    @State private var reloading = false
    @State private var reloadPending = false
    @Environment(\.settingsHostIsInline) private var isInlineHost

    @State private var bedrock: BedrockMachineStatus?
    @State private var status: AIStatusInfo?
    @State private var regionSelection = ""
    @State private var customRegion = ""
    @State private var vaultRegion = ""
    @State private var suppressRegionChange = false
    @State private var newToken = ""
    @State private var enteringToken = false
    @State private var busy = false
    @State private var message: String?
    @State private var selfTest: SelfTestReport?
    @State private var testing = false
    @State private var testError: String?
    @State private var loadError: String?
    @State private var showRevalidateOffer = false

    var body: some View {
        Form {
            connectionSection
            credentialSection
            modelSection
            verdictSection
            if let message {
                Section {
                    Text(message).font(.caption).foregroundStyle(.secondary)
                }
            }
        }
        .formStyle(.grouped)
        .task { await reload() }
    }

    // MARK: - Connection

    private var connectionSection: some View {
        Section("Connection") {
            LabeledContent("Service") {
                Text(ProviderDisplay.name(status?.provider ?? "bedrock"))
                    .foregroundStyle(.secondary)
            }
            regionRow
            regionIncludeRow
        }
    }

    /// "Also verify in": additional regions Validate probes when a model
    /// fails in the primary (Bedrock model access is granted per region).
    /// Verification-only — the primary region above keeps its risk guard;
    /// adding or removing an include region can't take embeddings offline.
    @ViewBuilder
    private var regionIncludeRow: some View {
        let included = bedrock?.regions ?? []
        let choices = RegionIncludeSelection.choices(
            common: BedrockRegions.common.map(\.id),
            primary: effectiveRegion,
            stored: included
        )
        LabeledContent("Also verify in") {
            Menu(included.isEmpty ? "None" : included.joined(separator: ", ")) {
                ForEach(choices, id: \.self) { region in
                    Button {
                        Task { await applyIncludedRegions(RegionIncludeSelection.toggling(included, region: region)) }
                    } label: {
                        if included.contains(region) {
                            Label(region, systemImage: "checkmark")
                        } else {
                            Text(region)
                        }
                    }
                }
            }
            .disabled(busy)
        }
        Text(RegionIncludeSelection.summary(primary: effectiveRegion, included: included))
            .font(.caption)
            .foregroundStyle(.secondary)
    }

    @ViewBuilder
    private var regionRow: some View {
        Picker("Region", selection: $regionSelection) {
            ForEach(BedrockRegions.common) { r in
                Text(r.display).tag(r.id)
            }
            Text("Other…").tag(SettingsAIView.customRegionTag)
        }
        // `suppressRegionChange` guards the reentrancy: applyRegion and reload
        // both write regionSelection, which re-fires this handler. Without it,
        // cancelling the confirm dialog reverted the picker, the revert fired
        // onChange again, and the user got a redundant write plus a cheerful
        // "Region set to us-east-1." right after declining to change anything.
        .onChange(of: regionSelection) { old, new in
            guard !suppressRegionChange else { return }
            guard new != SettingsAIView.customRegionTag, new != old, !old.isEmpty else { return }
            Task { await applyRegion(new) }
        }

        if regionSelection == SettingsAIView.customRegionTag {
            HStack {
                TextField("region code", text: $customRegion, prompt: Text("eu-north-1"))
                    .textFieldStyle(.roundedBorder)
                Button("Save") { Task { await applyRegion(customRegion) } }
                    .disabled(customRegion.trimmingCharacters(in: .whitespaces).isEmpty)
            }
        }

        if let note = BedrockRegions.mantleNote(generationModel: status?.genModel ?? "") {
            Text(note).font(.caption).foregroundStyle(.secondary)
        }
    }

    static let customRegionTag = "__custom__"

    /// The Models tab lives in the main window, which is WelcomeView when no
    /// vault is bound. The button is a dead end until a vault is open.
    static func canChooseModels(vaultBound: Bool) -> Bool { vaultBound }

    // MARK: - Credentials

    private var credentialSection: some View {
        Section("API key") {
            if let loadError {
                Label(loadError, systemImage: "exclamationmark.triangle.fill")
                    .font(.caption)
                    .foregroundStyle(.orange)
            }
            if enteringToken || !(bedrock?.tokenSet ?? false) {
                SecureField("Bearer token", text: $newToken)
                    .textFieldStyle(.roundedBorder)
                HStack {
                    // Return activates Save and test only in the Settings
                    // window host. `.defaultAction` registers window-global,
                    // so in the inline sidebar host it would claim Return for
                    // the whole dashboard window while this tab is visible.
                    if isInlineHost {
                        saveTokenButton
                    } else {
                        saveTokenButton.keyboardShortcut(.defaultAction)
                    }
                    if bedrock?.tokenSet ?? false {
                        Button("Cancel") {
                            enteringToken = false
                            newToken = ""
                        }
                    }
                }
                // Validating on save is the single most-cited BYOK lesson: the
                // common failure is a key that is expired, mistyped, or for the
                // wrong provider, and none of that shows up until first use.
                Text("The key is verified against your active models before it is accepted, and stored outside the vault.")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            } else if let bedrock {
                LabeledContent("Key") {
                    HStack(spacing: 8) {
                        Text(bedrock.maskedToken).font(.body.monospaced())
                        Button("Replace") { enteringToken = true }
                            .controlSize(.small)
                        Button("Remove") { Task { await clearToken() } }
                            .controlSize(.small)
                            .disabled(busy)
                    }
                }
                Text("Stored in \(bedrock.path) (source: \(bedrock.tokenSource)). Never written into a vault.")
                    .font(.caption)
                    .foregroundStyle(.secondary)
                if bedrock.envOverridesStored {
                    Label("AWS_BEARER_TOKEN_BEDROCK in your shell (ends \(bedrock.tokenSuffix)) overrides this saved key (ends \(bedrock.storedTokenSuffix)) for terminal and MCP use. The app uses the saved key. Turn on the option below to make the saved key win everywhere in 2nb.", systemImage: "exclamationmark.triangle.fill")
                        .font(.caption)
                        .foregroundStyle(.orange)
                }
                Toggle("Always use the saved key in 2nb (ignore AWS_BEARER_TOKEN_BEDROCK)", isOn: Binding(
                    get: { bedrock.preferStoredToken },
                    set: { on in Task { await applyPreferStored(on) } }
                ))
                .toggleStyle(.checkbox)
                .font(.caption)
                .disabled(busy)
                if bedrock.preferStoredToken, !bedrock.storedTokenSuffix.isEmpty {
                    Text("2nb (terminal, MCP, this app) uses the saved key; your shell's AWS_BEARER_TOKEN_BEDROCK still serves other tools like the aws CLI.")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
            }
            if showRevalidateOffer {
                HStack(spacing: 8) {
                    Text("Re-validate your models with the new key?")
                        .font(.caption)
                    Button("Re-validate now") {
                        showRevalidateOffer = false
                        appState.showAIHub = true
                        appState.pendingValidateRequest = true
                    }
                    .controlSize(.small)
                    Button("Later") { showRevalidateOffer = false }
                        .controlSize(.small)
                }
            }
        }
    }

    private var saveTokenButton: some View {
        Button(busy ? "Checking…" : "Save and test") {
            Task { await saveToken() }
        }
        .disabled(busy || newToken.trimmingCharacters(in: .whitespaces).isEmpty)
    }

    // MARK: - Models

    private var modelSection: some View {
        Section("Models") {
            LabeledContent("Answers") {
                Text(status?.genModel ?? "—").font(.body.monospaced())
            }
            LabeledContent("Search") {
                Text(status?.embeddingModel ?? "—").font(.body.monospaced())
            }
            Button("Choose models…") { appState.showAIHub = true }
                .controlSize(.small)
                .disabled(!Self.canChooseModels(vaultBound: appState.vault != nil))
            Text(Self.canChooseModels(vaultBound: appState.vault != nil)
                 ? "Vendors, validation, and Answers/Search live in the Models tab."
                 : "Bind an Obsidian vault to choose vendors and models.")
                .font(.caption)
                .foregroundStyle(.secondary)
        }
    }

    // MARK: - Verdict

    private var verdictSection: some View {
        Section("Is it working?") {
            HStack {
                Button(testing ? "Testing…" : "Test everything") {
                    Task { await runTest() }
                }
                .disabled(testing)
                Text("calls both active models for real (a fraction of a cent)")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }

            if let testError {
                Label(testError, systemImage: "exclamationmark.triangle.fill")
                    .font(.caption)
                    .foregroundStyle(.orange)
            }

            if let selfTest {
                Label(
                    SelfTestPresentation.headline(selfTest),
                    systemImage: SelfTestPresentation.isFailure(selfTest) ? "xmark.circle.fill" : "checkmark.circle.fill"
                )
                .foregroundStyle(SelfTestPresentation.isFailure(selfTest) ? .red : .green)

                ForEach(selfTest.checks) { check in
                    HStack(alignment: .firstTextBaseline, spacing: 6) {
                        Image(systemName: SelfTestPresentation.symbol(for: check))
                            .foregroundStyle(check.ok ? (check.isWarning ? .orange : .green) : .red)
                        VStack(alignment: .leading, spacing: 2) {
                            Text("\(check.name): \(check.detail)").font(.caption)
                            if let fix = check.fix, !check.ok || check.isWarning {
                                Text(fix).font(.caption2).foregroundStyle(.secondary)
                            }
                        }
                    }
                }
            }
        }
    }

    // MARK: - Actions

    private func reload() async {
        // Single-flight against dual-host reload stacking; see
        // SettingsGeneralView.reload.
        if reloading { reloadPending = true; return }
        reloading = true
        defer {
            reloading = false
            // Coalesce, never drop: a reload requested while one was in flight
            // (a post-write refresh racing the .task load) re-runs once so the
            // view always ends on post-mutation data. Single-flight is per
            // INSTANCE by design: each host loads its own @State, and
            // concurrent read-only status reloads across hosts are harmless.
            if reloadPending { reloadPending = false; Task { await reload() } }
        }
        // The credential row is the reason this page exists, so a failure to
        // read it is reported rather than rendered as "no key". The AI status
        // and vault region legitimately fail with no vault bound, which is a
        // supported state here, so those stay quiet.
        do {
            bedrock = try await appState.refreshBedrockMachineConfig()
            loadError = nil
        } catch {
            bedrock = nil
            loadError = "Could not read stored credentials: \(error.localizedDescription)"
        }
        status = try? await appState.fetchAIStatus()
        vaultRegion = (try? await appState.getConfigValue("ai.bedrock.region")) ?? ""

        let current = effectiveRegion
        suppressRegionChange = true
        regionSelection = BedrockRegions.isCommon(current) ? current : SettingsAIView.customRegionTag
        if regionSelection == SettingsAIView.customRegionTag { customRegion = current }
        suppressRegionChange = false
    }

    /// The region actually in force. The machine-local file wins over vault
    /// config — the one setting in this product with that precedence, which is
    /// why the page edits the file and reads the vault value only as a
    /// fallback.
    private var effectiveRegion: String {
        if let r = bedrock?.region, !r.isEmpty { return r }
        if !vaultRegion.isEmpty { return vaultRegion }
        return "us-east-1"
    }

    private func applyRegion(_ region: String) async {
        let target = region.trimmingCharacters(in: .whitespaces)
        guard !target.isEmpty else { return }

        // Refuse-and-explain rather than caption-and-hope. A picker makes this
        // a one-click change; without the guard, one click can take embeddings
        // offline and the only recovery is a full re-embed.
        //
        // `unverifiable` warns too. The active model is unreadable exactly when
        // no vault is bound — the first-run state this page is built for — and
        // a guard that waves the change through whenever it cannot see its
        // input is worse than no guard, because it looks like one.
        let explanation: String?
        switch BedrockRegions.risk(forRegion: target, embeddingModel: status?.embeddingModel) {
        case .safe:
            explanation = nil
        case .breaks(let text), .unverifiable(let text):
            explanation = text
        }
        if let explanation {
            #if canImport(AppKit)
            let alert = NSAlert()
            alert.messageText = "Switch region to \(target)?"
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
        do {
            bedrock = try await appState.saveBedrockMachineConfig(region: target, token: nil)
            message = "Region set to \(target)."
            await reload()
        } catch {
            message = error.localizedDescription
            await reload()
        }
    }

    /// Probes the typed key through the environment, then stores it only if the
    /// verdict warrants it.
    ///
    /// The order is the point. `AWS_BEARER_TOKEN_BEDROCK` outranks the stored
    /// file in the CLI's precedence, so the probe exercises the candidate key
    /// against the active models while the existing credential stays on disk. A
    /// save-then-test would already have destroyed a working key by the time it
    /// learned the replacement was mistyped, with nothing to roll back to.
    private func saveToken() async {
        busy = true
        defer { busy = false }
        let token = newToken.trimmingCharacters(in: .whitespacesAndNewlines)
        let hadKey = bedrock?.tokenSet ?? false

        testing = true
        testError = nil
        let report: SelfTestReport
        do {
            report = try await appState.probeBedrockToken(token)
        } catch {
            testing = false
            selfTest = nil
            testError = error.localizedDescription
            message = "The key was not saved — it could not be checked."
            return
        }
        testing = false
        selfTest = report

        let verdict = CredentialVerdict(report.credentials)
        let persist = BedrockKeyPersistence.shouldPersistProbedKey(
            verdict: verdict,
            hasExistingKey: hadKey
        )
        guard persist else {
            message = BedrockKeyPersistence.message(
                verdict: verdict,
                persisted: false,
                provider: report.provider
            )
            return
        }

        do {
            bedrock = try await appState.saveBedrockMachineConfig(region: "", token: token)
            newToken = ""
            enteringToken = false
            message = BedrockKeyPersistence.message(
                verdict: verdict,
                persisted: true,
                provider: report.provider
            )
            status = try? await appState.fetchAIStatus()
            // Persisted model-access verdicts now predate this key; offer the
            // refresh rather than auto-spending on it. Vault-bound only — the
            // Models tab is WelcomeView otherwise.
            showRevalidateOffer = appState.vault != nil
        } catch {
            message = error.localizedDescription
        }
    }

    private func applyPreferStored(_ on: Bool) async {
        busy = true
        defer { busy = false }
        do {
            bedrock = try await appState.saveBedrockMachineConfig(region: "", token: nil, preferStored: on)
        } catch {
            message = error.localizedDescription
        }
    }

    private func applyIncludedRegions(_ regions: [String]) async {
        busy = true
        defer { busy = false }
        do {
            bedrock = try await appState.saveBedrockMachineConfig(region: "", token: nil, verifyRegions: regions)
        } catch {
            message = error.localizedDescription
        }
    }

    private func clearToken() async {
        busy = true
        defer { busy = false }
        do {
            bedrock = try await appState.clearBedrockToken()
            selfTest = nil
            message = "Key removed."
        } catch {
            message = error.localizedDescription
        }
    }

    private func runTest() async {
        testing = true
        testError = nil
        defer { testing = false }
        do {
            selfTest = try await appState.runDoctorSelfTest()
        } catch {
            selfTest = nil
            testError = error.localizedDescription
        }
        status = try? await appState.fetchAIStatus()
    }
}

/// Whether a probed Bedrock key earns a place on disk, and what to tell the
/// user about it. Pure logic so the rules are unit-testable without a key.
enum BedrockKeyPersistence {
    /// - `rejected`: never stored. The provider answered, and the answer was no.
    /// - `accepted` / `unknown`: stored. `unknown` means every model returned
    ///   access_denied, which is a missing entitlement at least as often as a
    ///   bad key — refusing to store it would strand a user whose key is fine.
    /// - `unreachable`: stored ONLY when there is nothing to lose. We never got
    ///   an answer either way, so overwriting a key that already works on a
    ///   dropped connection trades a working setup for an unverified one; but on
    ///   first run, refusing would leave the user with no key and no way past it.
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

    /// The caption under the verdict, or nil when the headline already says it.
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
