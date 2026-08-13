import Foundation
import Testing
@testable import SecondBrain

// MARK: - Raw CLI runner (real subprocess, no mocks)

// runCLIGlobalRaw is the reason `2nb doctor`'s verdict survives a non-zero
// exit, and the refactor it required touched every runCLIGlobal caller. A
// decode test over a JSON literal cannot catch a regression in the process
// plumbing, so this spawns the real binary — matching AppStateCLITests, and
// the project's no-mock policy.

@Test("runCLIGlobalRaw returns stdout AND a non-zero status instead of throwing")
@MainActor
func runCLIGlobalRawKeepsStdoutOnFailure() async throws {
    let state = AppState()
    // An unknown subcommand: cobra exits non-zero and writes to stderr. The
    // point is that the call RETURNS rather than throwing, so a caller can
    // still read what the process produced.
    let result = try await state.runCLIGlobalRaw(["definitely-not-a-subcommand"])
    #expect(result.status != 0)
    #expect(!result.stderrText.isEmpty)
}

@Test("runCLIGlobal still throws on a non-zero exit, carrying stderr")
@MainActor
func runCLIGlobalStillThrows() async throws {
    let state = AppState()
    // The wrapper's contract must be unchanged for the existing callers: a
    // failure throws, and the message is the CLI's own stderr.
    await #expect(throws: (any Error).self) {
        _ = try await state.runCLIGlobal(["definitely-not-a-subcommand"])
    }
}

@Test("runCLIGlobal returns stdout on success")
@MainActor
func runCLIGlobalReturnsStdout() async throws {
    let state = AppState()
    let data = try await state.runCLIGlobal(["--version"])
    let text = String(decoding: data, as: UTF8.self)
    #expect(text.contains("2nb"))
}

// MARK: - Region guard

// The region field was free text nobody edited. As a dropdown it is one click,
// and one click can take embeddings offline: Nova-2 embeddings answer only in
// us-east-1, and recovering from a wrong pick costs a full re-embed. The guard
// is what makes the dropdown safe to ship, so it is pinned here.

@Test("A region change that would break the active embedding model is flagged")
func regionRiskBlocksBreakingChange() {
    let risk = BedrockRegions.risk(
        forRegion: "eu-west-1",
        embeddingModel: "amazon.nova-2-multimodal-embeddings-v1:0"
    )
    guard case .breaks(let text) = risk else {
        Issue.record("expected .breaks, got \(risk)")
        return
    }
    // The message must name both the required region and the consequence, or it
    // is just a scary dialog with no information in it.
    #expect(text.contains("us-east-1"))
    #expect(text.contains("re-embed"))
}

@Test("Staying in the model's required region is safe")
func regionRiskAllowsRequiredRegion() {
    #expect(BedrockRegions.risk(
        forRegion: "us-east-1",
        embeddingModel: "amazon.nova-2-multimodal-embeddings-v1:0"
    ) == .safe)
}

@Test("An unconstrained embedding model can move region freely")
func regionRiskIgnoresUnpinnedModels() {
    #expect(BedrockRegions.risk(
        forRegion: "eu-central-1",
        embeddingModel: "cohere.embed-english-v3"
    ) == .safe)
}

@Test("An unknown embedding model FAILS CLOSED rather than waving the change through")
func regionRiskFailsClosedWhenModelUnknown() {
    // This is the regression that matters. `ai status` needs a vault, so with
    // none bound — the first-run state this page exists for — the active model
    // is nil. An earlier version treated that as "no constraint found" and
    // saved a breaking region silently: the guard failed OPEN in exactly the
    // case it was written for.
    for unknown in [nil, ""] {
        let risk = BedrockRegions.risk(forRegion: "eu-west-1", embeddingModel: unknown)
        guard case .unverifiable(let text) = risk else {
            Issue.record("expected .unverifiable for \(String(describing: unknown)), got \(risk)")
            return
        }
        #expect(text.contains("us-east-1")) // still names the common trap
    }
}

@Test("Mantle models are called out as ignoring the region setting")
func mantleNoteExplainsPinnedModels() {
    // Without this, a user changes region, sees Grok keep working, and
    // reasonably concludes the setting does nothing.
    let note = BedrockRegions.mantleNote(generationModel: "xai.grok-4.3")
    #expect(note?.contains("us-west-2") == true)
    #expect(BedrockRegions.mantleNote(generationModel: "us.anthropic.claude-haiku-4-5-20251001-v1:0") == nil)
}

@Test("The offered region list is US-first and recognizes its own entries")
func commonRegionsAreCoherent() {
    #expect(BedrockRegions.common.first?.id == "us-east-1")
    #expect(BedrockRegions.isCommon("us-east-1"))
    #expect(BedrockRegions.isCommon("eu-west-1"))
    #expect(!BedrockRegions.isCommon("eu-north-1")) // falls through to free text
    // No duplicate codes, or the Picker would bind ambiguously.
    let ids = BedrockRegions.common.map(\.id)
    #expect(Set(ids).count == ids.count)
}

// MARK: - Self-test decoding and presentation

@Test("The doctor payload decodes, including a failing self-test")
func doctorReportDecodes() throws {
    let json = """
    {"latest":"v0.18.0","checked":true,"in_sync":true,"ok":false,
     "cli":{"name":"cli","status":"ok","installed":true,"update_available":false},
     "app":{"name":"app","status":"ok","installed":true,"update_available":false},
     "plugin":{"name":"plugin","status":"ok","installed":true,"update_available":false},
     "selftest":{"ok":false,"vault_bound":true,"vault_path":"/v","provider":"bedrock",
       "credentials":"rejected",
       "checks":[{"name":"api key","ok":false,"detail":"rejected by bedrock","fix":"set a working key"}]}}
    """
    let report = try JSONDecoder().decode(DoctorReport.self, from: Data(json.utf8))
    #expect(report.ok == false)
    let selftest = try #require(report.selftest)
    #expect(selftest.credentials == "rejected")
    #expect(selftest.checks.count == 1)
    #expect(selftest.checks[0].ok == false)
}

@Test("A rejected key and an un-entitled model get different headlines")
func headlineSeparatesKeyFromEntitlement() {
    // This is the whole point of the CLI's credential verdict. Collapsing both
    // into "AI failed" would send someone with a working key hunting for a new
    // one — the most expensive wrong answer this screen can give.
    let rejected = SelfTestReport(
        ok: false, vaultBound: true, vaultPath: "/v", provider: "bedrock",
        credentials: "rejected", checks: []
    )
    #expect(SelfTestPresentation.headline(rejected).contains("rejected"))

    let entitled = SelfTestReport(
        ok: false, vaultBound: true, vaultPath: "/v", provider: "bedrock",
        credentials: "accepted", checks: []
    )
    let text = SelfTestPresentation.headline(entitled)
    #expect(text.contains("works"))
    #expect(!text.contains("rejected"))
}

@Test("An all-clear reads as an all-clear")
func headlineReportsSuccess() {
    let good = SelfTestReport(
        ok: true, vaultBound: true, vaultPath: "/v", provider: "bedrock",
        credentials: "accepted", checks: []
    )
    #expect(SelfTestPresentation.headline(good) == "Everything works.")
    #expect(!SelfTestPresentation.isFailure(good))
}

@Test("An unconfirmed credential never reads as accepted")
func headlineDoesNotOverclaimUnknown() {
    // The CLI returns "unknown" when every model answered access_denied, which
    // is ambiguous between a bad key and a missing entitlement. The UI must not
    // resolve that ambiguity on its own.
    let unknown = SelfTestReport(
        ok: false, vaultBound: false, vaultPath: nil, provider: "bedrock",
        credentials: "unknown", checks: []
    )
    let text = SelfTestPresentation.headline(unknown)
    #expect(text.contains("Could not confirm"))
    #expect(!text.contains("rejected"))
}

@Test("A network failure is reported as a network failure, not a bad key")
func headlineSeparatesNetworkFromCredentials() {
    // "unreachable" means we never got an answer either way. Telling the user
    // their key was rejected on a dropped connection sends them to rotate a
    // perfectly good credential.
    let offline = SelfTestReport(
        ok: false, vaultBound: true, vaultPath: "/v", provider: "bedrock",
        credentials: "unreachable", checks: []
    )
    let text = SelfTestPresentation.headline(offline)
    #expect(text.contains("reach"))
    #expect(!text.contains("rejected"))
}

@Test("An unrecognized credential value degrades to unknown rather than crashing")
func credentialVerdictTolersatesUnknownVocabulary() {
    // A newer CLI could add a verdict this build has never heard of; the UI
    // must not force-unwrap its way into a crash on a settings page.
    #expect(CredentialVerdict("something-new") == .unknown)
    #expect(CredentialVerdict("accepted") == .accepted)
}

@Test("Warnings are distinguished from failures in the row symbols")
func warningsAreNotFailures() {
    let warn = SelfTestCheck(name: "vault", ok: true, warn: true, detail: "no vault bound", fix: "open one")
    let fail = SelfTestCheck(name: "api key", ok: false, warn: nil, detail: "rejected", fix: nil)
    let pass = SelfTestCheck(name: "search model", ok: true, warn: nil, detail: "responded", fix: nil)

    #expect(warn.isWarning)
    #expect(!fail.isWarning)
    #expect(!pass.isWarning)
    #expect(SelfTestPresentation.symbol(for: warn) == "exclamationmark.triangle.fill")
    #expect(SelfTestPresentation.symbol(for: fail) == "xmark.circle.fill")
    #expect(SelfTestPresentation.symbol(for: pass) == "checkmark.circle.fill")

    let report = SelfTestReport(
        ok: false, vaultBound: false, vaultPath: nil, provider: "bedrock",
        credentials: "accepted", checks: [warn, fail, pass]
    )
    // The collapsed view surfaces anything not plainly passing — and nothing else.
    #expect(SelfTestPresentation.problems(report).count == 2)
}

@Test("BedrockMachineStatus renders a masked key and tolerates a pre-0.18 CLI")
func maskedTokenRendering() throws {
    let withSuffix = """
    {"path":"/tmp/bedrock.json","token_set":true,"token_suffix":"9f2a","token_source":"file"}
    """
    let a = try JSONDecoder().decode(BedrockMachineStatus.self, from: Data(withSuffix.utf8))
    #expect(a.maskedToken.hasSuffix("9f2a"))
    #expect(!a.maskedToken.contains("token"))

    // An older CLI omits token_suffix entirely; the row must still say "set"
    // rather than failing to decode or claiming the key is absent.
    let noSuffix = """
    {"path":"/tmp/bedrock.json","token_set":true,"token_source":"env"}
    """
    let b = try JSONDecoder().decode(BedrockMachineStatus.self, from: Data(noSuffix.utf8))
    #expect(b.tokenSuffix == "")
    #expect(b.maskedToken == "••••••••")

    let unset = """
    {"path":"/tmp/bedrock.json","token_set":false,"token_source":"none"}
    """
    let c = try JSONDecoder().decode(BedrockMachineStatus.self, from: Data(unset.utf8))
    #expect(c.maskedToken == "not set")
}
