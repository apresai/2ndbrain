import Foundation
import Testing
@testable import SecondBrain

// MARK: - Region guard

// The region field was free text nobody edited. As a dropdown it is one click,
// and one click can take embeddings offline: Nova-2 embeddings answer only in
// us-east-1, and recovering from a wrong pick costs a full re-embed. The guard
// is what makes the dropdown safe to ship, so it is pinned here.

@Test("A region change that would break the active embedding model is flagged")
func regionConstraintBlocksBreakingChange() {
    let breakage = BedrockRegions.constraint(
        forRegion: "eu-west-1",
        embeddingModel: "amazon.nova-2-multimodal-embeddings-v1:0"
    )
    #expect(breakage != nil)
    // The message must name both the required region and the consequence, or it
    // is just a scary dialog with no information in it.
    #expect(breakage?.contains("us-east-1") == true)
    #expect(breakage?.contains("re-embed") == true)
}

@Test("Staying in the model's required region is not flagged")
func regionConstraintAllowsRequiredRegion() {
    #expect(BedrockRegions.constraint(
        forRegion: "us-east-1",
        embeddingModel: "amazon.nova-2-multimodal-embeddings-v1:0"
    ) == nil)
}

@Test("An unconstrained embedding model can move region freely")
func regionConstraintIgnoresUnpinnedModels() {
    #expect(BedrockRegions.constraint(
        forRegion: "eu-central-1",
        embeddingModel: "cohere.embed-english-v3"
    ) == nil)
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
