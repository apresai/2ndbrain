import Foundation
import Testing
@testable import SecondBrain

// MARK: - Raw CLI runner (real subprocess, no mocks)

@Test("runCLIGlobalRaw returns stdout AND a non-zero status instead of throwing")
@MainActor
func runCLIGlobalRawKeepsStdoutOnFailure() async throws {
    let state = AppState()
    let result = try await state.runCLIGlobalRaw(["definitely-not-a-subcommand"])
    #expect(result.status != 0)
    #expect(!result.stderrText.isEmpty)
}

@Test("runCLIGlobal still throws on a non-zero exit, carrying stderr")
@MainActor
func runCLIGlobalStillThrows() async throws {
    let state = AppState()
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

@Test("extraEnvironment overlays the inherited environment rather than replacing it")
@MainActor
func extraEnvironmentOverlaysInheritedEnvironment() async throws {
    let state = AppState()
    let result = try await state.runCLIGlobalRaw(
        ["--version"],
        extraEnvironment: ["TWONB_PROBE_CANARY": "1"]
    )
    #expect(result.status == 0)
    #expect(String(decoding: result.stdout, as: UTF8.self).contains("2nb"))
}

// MARK: - Probe before save (KeyPersistence)

@Test("A rejected key is never written to Keychain")
func rejectedKeyIsNotPersisted() {
    #expect(!KeyPersistence.shouldPersistProbedKey(verdict: .rejected, hasExistingKey: true))
    #expect(!KeyPersistence.shouldPersistProbedKey(verdict: .rejected, hasExistingKey: false))
}

@Test("An accepted key is written whether or not one already exists")
func acceptedKeyIsPersisted() {
    #expect(KeyPersistence.shouldPersistProbedKey(verdict: .accepted, hasExistingKey: true))
    #expect(KeyPersistence.shouldPersistProbedKey(verdict: .accepted, hasExistingKey: false))
}

@Test("An unconfirmed key is still written, because unknown is not a rejection")
func unknownKeyIsPersisted() {
    #expect(KeyPersistence.shouldPersistProbedKey(verdict: .unknown, hasExistingKey: true))
    #expect(KeyPersistence.shouldPersistProbedKey(verdict: .unknown, hasExistingKey: false))
}

@Test("An unreachable provider never clobbers a working key, but does not block first run")
func unreachableKeyPersistsOnlyWhenNothingToLose() {
    #expect(!KeyPersistence.shouldPersistProbedKey(verdict: .unreachable, hasExistingKey: true))
    #expect(KeyPersistence.shouldPersistProbedKey(verdict: .unreachable, hasExistingKey: false))
}

@Test("A refusal to save says so, and says the old key survived")
func notPersistedMessageExplainsItself() throws {
    let rejected = try #require(KeyPersistence.message(verdict: .rejected, persisted: false, provider: "gemini"))
    #expect(rejected.contains("not saved"))
    #expect(rejected.contains("untouched"))

    let offline = try #require(KeyPersistence.message(verdict: .unreachable, persisted: false, provider: "gemini"))
    #expect(offline.contains("not saved"))

    let unverified = try #require(KeyPersistence.message(verdict: .unreachable, persisted: true, provider: "gemini"))
    #expect(unverified.contains("without confirmation"))

    #expect(KeyPersistence.message(verdict: .accepted, persisted: true, provider: "gemini") == nil)
}

// MARK: - Gemini 4 Settings Catalog & Thinking Depth

@Test("GeminiSettingsCatalog exposes Gemini 4 generation and Gemini embedding models")
func geminiCatalogModels() {
    #expect(GeminiSettingsCatalog.defaultGenerationModel == "gemini-4-hydrogen")
    #expect(GeminiSettingsCatalog.defaultEmbeddingModel == "gemini-embedding-2")
    #expect(GeminiSettingsCatalog.generationModels.map(\.id) == [
        "gemini-4-hydrogen",
        "gemini-4-argon",
        "gemini-4-carbon",
    ])
    #expect(GeminiSettingsCatalog.embeddingModels.map(\.id) == [
        "gemini-embedding-2",
        "gemini-embedding-001",
    ])
}

@Test("GeminiSettingsCatalog gates Extra High (xhigh) thinking strictly to Gemini 4 Hydrogen")
func geminiThinkingLevelGating() {
    #expect(GeminiSettingsCatalog.validThinkingLevels(for: "gemini-4-hydrogen").map(\.id)
        == ["", "low", "medium", "high", "xhigh"])
    #expect(GeminiSettingsCatalog.validThinkingLevels(for: "gemini-4-argon").map(\.id)
        == ["", "low", "medium", "high"])
    #expect(GeminiSettingsCatalog.validThinkingLevels(for: "gemini-4-carbon").map(\.id)
        == ["", "low", "medium", "high"])

    #expect(GeminiSettingsCatalog.sanitizedThinking("xhigh", for: "gemini-4-hydrogen") == "xhigh")
    #expect(GeminiSettingsCatalog.sanitizedThinking("xhigh", for: "gemini-4-argon") == "high")
    #expect(GeminiSettingsCatalog.sanitizedThinking("xhigh", for: "gemini-4-carbon") == "high")
    #expect(GeminiSettingsCatalog.sanitizedThinking("none", for: "gemini-4-hydrogen") == "low")
    #expect(GeminiSettingsCatalog.sanitizedThinking("bogus", for: "gemini-4-hydrogen") == "")
}

// MARK: - Google Regions & Gemini Guidance

@Test("GoogleRegions lists common GCP regions with us-central1 default")
func googleRegionsCommon() {
    #expect(GoogleRegions.common.first?.id == "us-central1")
    #expect(GoogleRegions.isCommon("us-central1"))
    #expect(GoogleRegions.isCommon("us-east4"))
    #expect(GoogleRegions.isCommon("europe-west1"))
    #expect(!GoogleRegions.isCommon("invalid-region"))

    let ids = GoogleRegions.common.map(\.id)
    #expect(Set(ids).count == ids.count)
}

@Test("GoogleRegions risk check allows standard models and flags unknown")
func googleRegionsRisk() {
    #expect(GoogleRegions.risk(forRegion: "us-central1", embeddingModel: "gemini-embedding-2") == .safe)
    #expect(GoogleRegions.risk(forRegion: "us-central1", embeddingModel: "gemini-embedding-001") == .safe)
    let riskNil = GoogleRegions.risk(forRegion: "us-central1", embeddingModel: nil)
    guard case .unverifiable(let text) = riskNil else {
        Issue.record("expected .unverifiable for nil model, got \(riskNil)")
        return
    }
    #expect(text.contains("Vertex AI"))
}

@Test("ModelAccessPresentation maps Gemini access_denied and permission_denied to Vertex AI console URL")
func modelAccessPresentationGemini() {
    let guidance = ModelAccessPresentation.guidance(
        code: "access_denied",
        provider: "gemini",
        region: "my-test-proj"
    )
    #expect(guidance?.badge == "no access")
    #expect(guidance?.actionLabel == "Open Vertex AI console")
    #expect(guidance?.actionURL?.absoluteString.contains("console.cloud.google.com/vertex-ai") == true)
    #expect(guidance?.actionURL?.absoluteString.contains("project=my-test-proj") == true)
    #expect(guidance?.advice.contains("roles/aiplatform.user") == true)

    let permGuidance = ModelAccessPresentation.guidance(
        code: "permission_denied",
        provider: "gemini"
    )
    #expect(permGuidance?.badge == "no access")
    #expect(permGuidance?.actionLabel == "Open Vertex AI console")
    #expect(permGuidance?.actionURL?.absoluteString == "https://console.cloud.google.com/vertex-ai/")
}

@Test("vertexAIConsoleURL sanitizes project ID")
func vertexAIConsoleURLSanitization() {
    #expect(ModelAccessPresentation.vertexAIConsoleURL().absoluteString == "https://console.cloud.google.com/vertex-ai/")
    #expect(ModelAccessPresentation.vertexAIConsoleURL(project: "my-gcp-project").absoluteString == "https://console.cloud.google.com/vertex-ai/?project=my-gcp-project")
    #expect(ModelAccessPresentation.vertexAIConsoleURL(project: "evil.com/redirect").absoluteString == "https://console.cloud.google.com/vertex-ai/")
}

@Test("LocalGemmaCatalog exposes EmbeddingGemma 2 as default embedding model")
func localGemmaCatalogEmbedDefaults() throws {
    #expect(LocalGemmaCatalog.defaultEmbedModel == "embeddinggemma-2")
    let embed = try #require(LocalGemmaCatalog.models.first { $0.role == "embed" })
    #expect(embed.id == "embeddinggemma-2")
    #expect(embed.title == "EmbeddingGemma 2")
    #expect(embed.fileName == "embeddinggemma-2-Q8_0.gguf")
    #expect(embed.sizeBytes == 309_855_456)
    #expect(embed.sizeDisplay == "310 MB")
}

// MARK: - Self-test decoding and presentation

@Test("The doctor payload decodes, including a failing self-test")
func doctorReportDecodes() throws {
    let json = """
    {"latest":"v0.23.4","checked":true,"in_sync":true,"ok":false,
     "cli":{"name":"cli","status":"ok","installed":true,"update_available":false},
     "app":{"name":"app","status":"ok","installed":true,"update_available":false},
     "plugin":{"name":"plugin","status":"ok","installed":true,"update_available":false},
     "selftest":{"ok":false,"vault_bound":true,"vault_path":"/v","provider":"gemini",
       "credentials":"rejected",
       "checks":[{"name":"api key","ok":false,"detail":"rejected by gemini","fix":"set a working key"}]}}
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
    let rejected = SelfTestReport(
        ok: false, vaultBound: true, vaultPath: "/v", provider: "gemini",
        credentials: "rejected", checks: []
    )
    #expect(SelfTestPresentation.headline(rejected).contains("rejected"))

    let entitled = SelfTestReport(
        ok: false, vaultBound: true, vaultPath: "/v", provider: "gemini",
        credentials: "accepted", checks: []
    )
    let text = SelfTestPresentation.headline(entitled)
    #expect(text.contains("works"))
    #expect(!text.contains("rejected"))
}

@Test("An all-clear reads as an all-clear")
func headlineReportsSuccess() {
    let good = SelfTestReport(
        ok: true, vaultBound: true, vaultPath: "/v", provider: "gemini",
        credentials: "accepted", checks: []
    )
    #expect(SelfTestPresentation.headline(good) == "Everything works.")
    #expect(!SelfTestPresentation.isFailure(good))
}

@Test("An unconfirmed credential never reads as accepted")
func headlineDoesNotOverclaimUnknown() {
    let unknown = SelfTestReport(
        ok: false, vaultBound: false, vaultPath: nil, provider: "gemini",
        credentials: "unknown", checks: []
    )
    let text = SelfTestPresentation.headline(unknown)
    #expect(text.contains("Could not confirm"))
    #expect(!text.contains("rejected"))
}

@Test("A network failure is reported as a network failure, not a bad key")
func headlineSeparatesNetworkFromCredentials() {
    let offline = SelfTestReport(
        ok: false, vaultBound: true, vaultPath: "/v", provider: "gemini",
        credentials: "unreachable", checks: []
    )
    let text = SelfTestPresentation.headline(offline)
    #expect(text.contains("reach"))
    #expect(!text.contains("rejected"))
}

@Test("An unrecognized credential value degrades to unknown rather than crashing")
func credentialVerdictTolersatesUnknownVocabulary() {
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
        ok: false, vaultBound: false, vaultPath: nil, provider: "gemini",
        credentials: "accepted", checks: [warn, fail, pass]
    )
    #expect(SelfTestPresentation.problems(report).count == 2)
}

@Test("Local Gemma self-test headlines explain keyless offline status accurately")
func llamaLocalSelfTestHeadlines() {
    let ok = SelfTestReport(
        ok: true, vaultBound: true, vaultPath: "/v", provider: "llama-local",
        credentials: "accepted", checks: []
    )
    #expect(SelfTestPresentation.headline(ok) == "Local Gemma is ready (on-device, no API key needed).")

    let partial = SelfTestReport(
        ok: false, vaultBound: true, vaultPath: "/v", provider: "llama-local",
        credentials: "accepted", checks: []
    )
    let partialHeadline = SelfTestPresentation.headline(partial)
    #expect(partialHeadline.contains("Local Gemma"))
    #expect(!partialHeadline.contains("API key"))

    let rejected = SelfTestReport(
        ok: false, vaultBound: true, vaultPath: "/v", provider: "llama-local",
        credentials: "rejected", checks: []
    )
    #expect(SelfTestPresentation.headline(rejected) == "Local Gemma configuration was rejected.")

    for verdict in ["unreachable", "unknown"] {
        let unreachable = SelfTestReport(
            ok: false, vaultBound: false, vaultPath: nil, provider: "llama-local",
            credentials: verdict, checks: []
        )
        let headline = SelfTestPresentation.headline(unreachable)
        #expect(headline.contains("Local Gemma"))
        #expect(headline.contains("models"))
        #expect(!headline.contains("Network?"))
        #expect(!headline.contains("API key"))
    }
}

@Test("SelfTestPresentation.onboardingChecks includes model probes and problems")
func onboardingChecksFiltering() {
    let apiKeyOk = SelfTestCheck(name: "api key", ok: true, warn: nil, detail: "accepted", fix: nil)
    let searchOk = SelfTestCheck(name: "search model (gemini-embedding-2)", ok: true, warn: nil, detail: "responded in 100ms", fix: nil)
    let answerOk = SelfTestCheck(name: "answer model (gemini-4-hydrogen)", ok: true, warn: nil, detail: "responded in 200ms", fix: nil)
    let vaultWarn = SelfTestCheck(name: "vault", ok: true, warn: true, detail: "not indexed", fix: "run index")
    let mcpFail = SelfTestCheck(name: "mcp search", ok: false, warn: nil, detail: "failed", fix: "re-index")

    let report = SelfTestReport(
        ok: false, vaultBound: true, vaultPath: "/v", provider: "gemini",
        credentials: "accepted",
        checks: [apiKeyOk, searchOk, answerOk, vaultWarn, mcpFail]
    )
    let selected = SelfTestPresentation.onboardingChecks(report)
    #expect(selected.map(\.name) == [
        "search model (gemini-embedding-2)",
        "answer model (gemini-4-hydrogen)",
        "vault",
        "mcp search",
    ])
}

@Test("ModelAccessPresentation provides non-CLI guidance for llama-local provider_unreachable")
func modelAccessPresentationLlamaLocal() {
    let guidance = ModelAccessPresentation.guidance(code: "provider_unreachable", provider: "llama-local")
    #expect(guidance?.badge == "unreachable")
    #expect(guidance?.title == "Provider unreachable")
    #expect(guidance?.advice == "On-device Gemma model weights may not be downloaded yet, or the local runtime could not start. Download the local models in Settings and retry.")
    #expect(guidance?.advice.contains("2nb") == false)
}

@Test("GUIStatusSnapshot serializes and decodes per-slot probe and doctor telemetry fields")
func statusSnapshotProbeAndDoctorFields() throws {
    var snapshot = GUIStatusSnapshot.initial(appVersion: "0.23.4", timestamp: "2026-11-16T00:00:00Z")
    #expect(snapshot.lastGenProbeModel == nil)
    #expect(snapshot.lastGenProbeProvider == nil)
    #expect(snapshot.lastGenProbeOK == nil)
    #expect(snapshot.lastGenProbeDetail == nil)
    #expect(snapshot.lastGenProbeLatency == nil)
    #expect(snapshot.lastEmbedProbeModel == nil)
    #expect(snapshot.lastEmbedProbeProvider == nil)
    #expect(snapshot.lastEmbedProbeOK == nil)
    #expect(snapshot.lastEmbedProbeDetail == nil)
    #expect(snapshot.lastEmbedProbeLatency == nil)
    #expect(snapshot.lastModelsTestOK == nil)
    #expect(snapshot.lastDoctorOK == nil)
    #expect(snapshot.lastDoctorCredentials == nil)
    #expect(snapshot.lastDoctorSummary == nil)

    snapshot.lastGenProbeModel = "gemma4-e2b"
    snapshot.lastGenProbeProvider = "llama-local"
    snapshot.lastGenProbeOK = true
    snapshot.lastGenProbeDetail = "4"
    snapshot.lastGenProbeLatency = "420ms"
    snapshot.lastEmbedProbeModel = "embeddinggemma-2"
    snapshot.lastEmbedProbeProvider = "llama-local"
    snapshot.lastEmbedProbeOK = true
    snapshot.lastEmbedProbeDetail = "dims=256, L2=1.00"
    snapshot.lastEmbedProbeLatency = "85ms"
    snapshot.lastModelsTestOK = true
    snapshot.lastDoctorOK = true
    snapshot.lastDoctorCredentials = "accepted"
    snapshot.lastDoctorSummary = "Local Gemma is ready (on-device, no API key needed)."

    let data = try JSONEncoder().encode(snapshot)
    let decoded = try JSONDecoder().decode(GUIStatusSnapshot.self, from: data)

    #expect(decoded.lastGenProbeModel == "gemma4-e2b")
    #expect(decoded.lastGenProbeProvider == "llama-local")
    #expect(decoded.lastGenProbeOK == true)
    #expect(decoded.lastGenProbeDetail == "4")
    #expect(decoded.lastGenProbeLatency == "420ms")
    #expect(decoded.lastEmbedProbeModel == "embeddinggemma-2")
    #expect(decoded.lastEmbedProbeProvider == "llama-local")
    #expect(decoded.lastEmbedProbeOK == true)
    #expect(decoded.lastEmbedProbeDetail == "dims=256, L2=1.00")
    #expect(decoded.lastEmbedProbeLatency == "85ms")
    #expect(decoded.lastModelsTestOK == true)
    #expect(decoded.lastDoctorOK == true)
    #expect(decoded.lastDoctorCredentials == "accepted")
    #expect(decoded.lastDoctorSummary == "Local Gemma is ready (on-device, no API key needed).")
}

// MARK: - AI Model Test Evaluation & Slot Extraction

private func make19CheckModelMismatchReport(
    embeddingsOK: Bool = false,
    embeddingsDetail: String = "MODEL MISMATCH",
    kbSearchOK: Bool = true,
    kbSearchDetail: String = "kb_search answered via hybrid search (vector channel live)",
    answerOK: Bool = true,
    answerDetail: String = "responded in 4.734s (replied \"4\")",
    searchOK: Bool = true,
    searchDetail: String = "responded in 1.528s (dims=768, L2=1.00)",
    dimensionsOK: Bool = true,
    credentials: String = "accepted",
    extraChecks: [SelfTestCheck] = []
) -> SelfTestReport {
    var checks: [SelfTestCheck] = [
        SelfTestCheck(name: "api key", ok: credentials == "accepted", warn: credentials != "accepted" && credentials != "rejected", detail: "no API key required (on-device)", fix: nil),
        SelfTestCheck(name: "search model (embeddinggemma-2)", ok: searchOK, warn: nil, detail: searchDetail, fix: searchOK ? nil : "ensure local models are downloaded"),
        SelfTestCheck(name: "answer model (gemma4-e2b)", ok: answerOK, warn: nil, detail: answerDetail, fix: answerOK ? nil : "ensure local models are downloaded"),
        SelfTestCheck(name: "vault", ok: true, warn: nil, detail: "/tmp/vault", fix: nil),
        SelfTestCheck(name: "provider known", ok: true, warn: nil, detail: "llama-local", fix: nil),
        SelfTestCheck(name: "active provider enabled", ok: true, warn: nil, detail: "not disabled", fix: nil),
        SelfTestCheck(name: "model belongs to active provider", ok: true, warn: nil, detail: "embedding + generation models match active slot providers", fix: nil),
        SelfTestCheck(name: "ai.dimensions matches model", ok: dimensionsOK, warn: nil, detail: dimensionsOK ? "768d" : "ai.dimensions is 999 but embeddinggemma-2 supports 256, 512, 768", fix: dimensionsOK ? nil : "set valid dimensions"),
        SelfTestCheck(name: "embeddings match selection", ok: embeddingsOK, warn: nil, detail: embeddingsDetail, fix: embeddingsOK ? nil : "Run 2nb index --force-reembed"),
        SelfTestCheck(name: "similarity threshold resolves", ok: true, warn: nil, detail: "0.2 (default)", fix: nil),
        SelfTestCheck(name: "mcp tools registered", ok: true, warn: nil, detail: "22 tools registered", fix: nil),
        SelfTestCheck(name: "kb_info round-trip", ok: true, warn: nil, detail: "kb_info answered", fix: nil),
        SelfTestCheck(name: "kb_list round-trip", ok: true, warn: nil, detail: "kb_list answered", fix: nil),
        SelfTestCheck(name: "kb_search round-trip", ok: kbSearchOK, warn: nil, detail: kbSearchDetail, fix: kbSearchOK ? nil : "run 2nb ai status"),
        SelfTestCheck(name: "ai readiness", ok: true, warn: nil, detail: "llama-local ready", fix: nil),
        SelfTestCheck(name: "mcp configured (~/.claude.json)", ok: true, warn: true, detail: "not configured", fix: "run 2nb mcp install"),
        SelfTestCheck(name: "initialize instructions", ok: true, warn: nil, detail: "present", fix: nil),
        SelfTestCheck(name: "index WAL health", ok: true, warn: nil, detail: "WAL 0 B, DB 128 KB", fix: nil),
        SelfTestCheck(name: "mcp server processes", ok: true, warn: true, detail: "3 running, 3 stale (>6h idle)", fix: "run 2nb mcp reap"),
    ]
    checks.append(contentsOf: extraChecks)
    let allOK = checks.allSatisfy { $0.ok }
    return SelfTestReport(
        ok: allOK,
        vaultBound: true,
        vaultPath: "/tmp/vault",
        provider: "llama-local",
        credentials: credentials,
        checks: checks
    )
}

@Test("19-check model_mismatch payload passes AI model test with re-embed note and zero MCP noise")
func modelMismatch19ChecksPassesModelTest() {
    let report = make19CheckModelMismatchReport()
    #expect(report.checks.count == 19)
    #expect(report.ok == false)

    #expect(SelfTestPresentation.modelsTestPassed(report) == true)
    #expect(SelfTestPresentation.relevantConfigIssues(report).isEmpty)

    let headline = SelfTestPresentation.modelsHeadline(report, genProvider: "llama-local", embedProvider: "llama-local")
    #expect(headline.contains("responded and passed"))
    #expect(headline.contains("re-embed"))

    let slots = SelfTestPresentation.extractSlotResults(report, genProvider: "llama-local", embedProvider: "llama-local")
    #expect(slots.count == 2)
    #expect(slots[0].slot == .generation)
    #expect(slots[0].modelID == "gemma4-e2b")
    #expect(slots[0].provider == "llama-local")
    #expect(slots[0].ok == true)
    #expect(slots[0].latency == "4.734s")
    #expect(slots[0].responseProof == "replied \"4\"")
    #expect(slots[0].proofLabel == "Verified response: \"4\"")

    #expect(slots[1].slot == .embedding)
    #expect(slots[1].modelID == "embeddinggemma-2")
    #expect(slots[1].provider == "llama-local")
    #expect(slots[1].ok == true)
    #expect(slots[1].latency == "1.528s")
    #expect(slots[1].responseProof == "dims=768, L2=1.00")
    #expect(slots[1].proofLabel == "Verified vector: dims=768, L2=1.00")
}

@Test("dimension_break plus kb_search BM25 fallback still passes AI model test")
func dimensionBreakWithBM25FallbackPassesModelTest() {
    let report = make19CheckModelMismatchReport(
        embeddingsOK: false,
        embeddingsDetail: "DIMENSION BREAK",
        kbSearchOK: false,
        kbSearchDetail: "kb_search silently fell back to bm25 despite 5 embedded documents — the vector channel is down"
    )
    #expect(report.ok == false)
    #expect(SelfTestPresentation.modelsTestPassed(report) == true)
    #expect(SelfTestPresentation.relevantConfigIssues(report).isEmpty)

    let embMatch = report.checks.first { $0.name == "embeddings match selection" }!
    let kbSearch = report.checks.first { $0.name == "kb_search round-trip" }!
    #expect(SelfTestPresentation.isReembedPendingFailure(embMatch, in: report) == true)
    #expect(SelfTestPresentation.isReembedPendingFailure(kbSearch, in: report) == true)
    #expect(SelfTestPresentation.isMCPOrStorageCheck(kbSearch) == true)
}

@Test("Honest failure cases fail modelsTestPassed and report the failing cause")
func honestFailuresFailModelTest() {
    // 1. Answer model fails
    let answerFail = make19CheckModelMismatchReport(
        embeddingsOK: true,
        embeddingsDetail: "OK",
        answerOK: false,
        answerDetail: "connection refused (provider_unreachable)"
    )
    #expect(SelfTestPresentation.modelsTestPassed(answerFail) == false)
    let answerHeadline = SelfTestPresentation.modelsHeadline(answerFail, genProvider: "llama-local", embedProvider: "llama-local")
    #expect(answerHeadline.contains("gemma4-e2b"))
    let answerSlots = SelfTestPresentation.extractSlotResults(answerFail, genProvider: "llama-local", embedProvider: "llama-local")
    #expect(answerSlots[0].ok == false)
    #expect(answerSlots[0].errorCode == "provider_unreachable")
    #expect(answerSlots[0].responseProof == "connection refused")

    // 2. Search model fails
    let searchFail = make19CheckModelMismatchReport(
        embeddingsOK: true,
        embeddingsDetail: "OK",
        searchOK: false,
        searchDetail: "model not found (not_found)"
    )
    #expect(SelfTestPresentation.modelsTestPassed(searchFail) == false)
    let searchHeadline = SelfTestPresentation.modelsHeadline(searchFail, genProvider: "llama-local", embedProvider: "llama-local")
    #expect(searchHeadline.contains("embeddinggemma-2"))

    // 3. Credentials rejected / unreachable / unknown
    for cred in ["rejected", "unreachable", "unknown"] {
        let credFail = make19CheckModelMismatchReport(
            embeddingsOK: true,
            embeddingsDetail: "OK",
            credentials: cred
        )
        #expect(SelfTestPresentation.modelsTestPassed(credFail) == false)
        #expect(SelfTestPresentation.modelsHeadline(credFail) == SelfTestPresentation.headline(credFail))
    }

    // 4. ai.dimensions matches model fails
    let dimFail = make19CheckModelMismatchReport(
        embeddingsOK: true,
        embeddingsDetail: "OK",
        dimensionsOK: false
    )
    #expect(SelfTestPresentation.modelsTestPassed(dimFail) == false)
    let issues = SelfTestPresentation.relevantConfigIssues(dimFail)
    #expect(issues.map(\.name) == ["ai.dimensions matches model"])
    #expect(SelfTestPresentation.modelsHeadline(dimFail).contains("ai.dimensions matches model"))

    // 5. Unexpected non-MCP/non-reembed failure fails modelsTestPassed
    let unexpected = make19CheckModelMismatchReport(
        embeddingsOK: true,
        embeddingsDetail: "OK",
        extraChecks: [SelfTestCheck(name: "unexpected check", ok: false, warn: nil, detail: "boom", fix: nil)]
    )
    #expect(SelfTestPresentation.modelsTestPassed(unexpected) == false)
}

@Test("parsePassingDetail, parseCheckModelID, and ModelSlotTestResult.fromProbe handle all variants")
func parseHelpersAndFromProbe() {
    #expect(SelfTestPresentation.parseCheckModelID("search model (embeddinggemma-2)", prefix: "search model") == "embeddinggemma-2")
    #expect(SelfTestPresentation.parseCheckModelID("answer model (gemini-4-hydrogen)", prefix: "answer model") == "gemini-4-hydrogen")
    #expect(SelfTestPresentation.parseCheckModelID("search model", prefix: "search model") == "")

    let embedParsed = SelfTestPresentation.parsePassingDetail("responded in 1.528s (dims=768, L2=1.00)")
    #expect(embedParsed.latency == "1.528s")
    #expect(embedParsed.proof == "dims=768, L2=1.00")

    let genParsed = SelfTestPresentation.parsePassingDetail("responded in 4.734s (replied \"4\")")
    #expect(genParsed.latency == "4.734s")
    #expect(genParsed.proof == "replied \"4\"")

    let bareParsed = SelfTestPresentation.parsePassingDetail("responded in 200ms")
    #expect(bareParsed.latency == "200ms")
    #expect(bareParsed.proof == "")

    let probe = AIProbeResult(
        modelID: "gemini-4-hydrogen",
        provider: "gemini",
        modelType: "generation",
        ok: true,
        detail: "4",
        latency: "310ms",
        errorCode: nil,
        remediation: nil,
        invokeStrategy: nil
    )
    let slot = ModelSlotTestResult.fromProbe(probe, slot: .generation)
    #expect(slot.id == "generation")
    #expect(slot.modelID == "gemini-4-hydrogen")
    #expect(slot.provider == "gemini")
    #expect(slot.ok == true)
    #expect(slot.latency == "310ms")
    #expect(slot.responseProof == "4")
    #expect(slot.proofLabel == "Verified response: \"4\"")
}

@Test("AppState.resolvedSlotProvider parses mixed and single provider known checks")
func resolvedSlotProviderParsing() {
    let mixed = SelfTestReport(
        ok: true,
        vaultBound: true,
        vaultPath: "/v",
        provider: "gemini",
        credentials: "accepted",
        checks: [SelfTestCheck(name: "provider known", ok: true, warn: nil, detail: "embed: llama-local, generation: gemini", fix: nil)]
    )
    #expect(AppState.resolvedSlotProvider(from: mixed, slot: .embedding, fallback: nil) == "llama-local")
    #expect(AppState.resolvedSlotProvider(from: mixed, slot: .generation, fallback: nil) == "gemini")

    let single = SelfTestReport(
        ok: true,
        vaultBound: true,
        vaultPath: "/v",
        provider: "llama-local",
        credentials: "accepted",
        checks: [SelfTestCheck(name: "provider known", ok: true, warn: nil, detail: "llama-local", fix: nil)]
    )
    #expect(AppState.resolvedSlotProvider(from: single, slot: .embedding, fallback: nil) == "llama-local")
    #expect(AppState.resolvedSlotProvider(from: single, slot: .generation, fallback: nil) == "llama-local")
}
