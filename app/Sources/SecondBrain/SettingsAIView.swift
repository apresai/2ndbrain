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

    @State private var bedrock: BedrockMachineStatus?
    @State private var status: AIStatusInfo?
    @State private var regionSelection = ""
    @State private var customRegion = ""
    @State private var vaultRegion = ""
    @State private var newToken = ""
    @State private var enteringToken = false
    @State private var busy = false
    @State private var message: String?
    @State private var selfTest: SelfTestReport?
    @State private var testing = false
    @State private var testError: String?

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
        }
    }

    @ViewBuilder
    private var regionRow: some View {
        Picker("Region", selection: $regionSelection) {
            ForEach(BedrockRegions.common) { r in
                Text(r.display).tag(r.id)
            }
            Text("Other…").tag(SettingsAIView.customRegionTag)
        }
        .onChange(of: regionSelection) { old, new in
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

    // MARK: - Credentials

    private var credentialSection: some View {
        Section("API key") {
            if enteringToken || !(bedrock?.tokenSet ?? false) {
                SecureField("Bearer token", text: $newToken)
                    .textFieldStyle(.roundedBorder)
                HStack {
                    Button(busy ? "Checking…" : "Save and test") {
                        Task { await saveToken() }
                    }
                    .keyboardShortcut(.defaultAction)
                    .disabled(busy || newToken.trimmingCharacters(in: .whitespaces).isEmpty)
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
            }
        }
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
            Button("Browse all models…") { appState.showAIHub = true }
                .controlSize(.small)
            Text("The full catalog, per-model tests, and vendor rules live in the Models tab — it needs more room than a settings window.")
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
        bedrock = try? await appState.refreshBedrockMachineConfig()
        status = try? await appState.fetchAIStatus()
        vaultRegion = (try? await appState.getConfigValue("ai.bedrock.region")) ?? ""
        let current = effectiveRegion
        regionSelection = BedrockRegions.isCommon(current) ? current : SettingsAIView.customRegionTag
        if regionSelection == SettingsAIView.customRegionTag { customRegion = current }
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
        if let breakage = BedrockRegions.constraint(forRegion: target, embeddingModel: status?.embeddingModel ?? "") {
            #if canImport(AppKit)
            let alert = NSAlert()
            alert.messageText = "Switch region to \(target)?"
            alert.informativeText = breakage
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

    private func saveToken() async {
        busy = true
        defer { busy = false }
        let token = newToken.trimmingCharacters(in: .whitespacesAndNewlines)
        do {
            bedrock = try await appState.saveBedrockMachineConfig(region: "", token: token)
            newToken = ""
            enteringToken = false
            // Verify immediately. A key that is silently wrong is the whole
            // failure mode this page exists to prevent.
            await runTest()
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
