import Foundation
import Testing
@testable import SecondBrain

/// Decode CatalogModelInfo fixtures from JSON (the same shape `2nb models
/// list --json` emits) so curation logic is exercised through the real
/// decode path, including the optional new fields.
private func decodeModel(_ json: String) -> CatalogModelInfo {
    try! JSONDecoder().decode(CatalogModelInfo.self, from: Data(json.utf8))
}

private func decodeStatus(_ json: String) -> AIStatusInfo {
    try! JSONDecoder().decode(AIStatusInfo.self, from: Data(json.utf8))
}

private let recommendedBedrock = decodeModel(#"{"id":"us.anthropic.claude-sonnet-5","name":"Claude Sonnet 5","provider":"bedrock","type":"generation","price_input_per_million":2,"price_output_per_million":10,"compatible":true,"tier":"verified","recommended":true}"#)
private let verifiedNotRecommended = decodeModel(#"{"id":"amazon.nova-micro-v1:0","name":"Nova Micro","provider":"bedrock","type":"generation","price_input_per_million":0.035,"price_output_per_million":0.14,"compatible":true,"tier":"verified"}"#)
private let testedByUser = decodeModel(#"{"id":"mistral.mistral-large-2407-v1:0","name":"Mistral Large","provider":"bedrock","type":"generation","compatible":true,"tier":"user_verified","tested_at":"2026-07-01T00:00:00Z"}"#)
private let failedTest = decodeModel(#"{"id":"us.anthropic.claude-opus-4-8","name":"Claude Opus 4.8","provider":"bedrock","type":"generation","compatible":true,"tier":"verified","recommended":true,"tested_at":"2026-07-01T00:00:00Z","test_error":"403","test_error_code":"access_denied"}"#)
private let discovered = decodeModel(#"{"id":"ai21.jamba-1-5-large-v1:0","name":"Jamba 1.5","provider":"bedrock","type":"generation","compatible":true,"tier":"unverified"}"#)
private let incompatible = decodeModel(#"{"id":"amazon.nova-canvas-v1:0","name":"Nova Canvas","provider":"bedrock","type":"generation","compatible":false,"compatibility_reason":"image model","tier":"unverified","recommended":true}"#)
private let otherProvider = decodeModel(#"{"id":"gemma3:4b","name":"Gemma 3 4B","provider":"ollama","type":"generation","compatible":true,"tier":"verified","recommended":true}"#)

@Test("CatalogModelInfo decodes the new optional fields and tolerates their absence")
func catalogModelDecodesNewFields() {
    #expect(recommendedBedrock.recommended == true)
    #expect(failedTest.testErrorCode == "access_denied")
    // Old-CLI JSON (fields absent) decodes with nils.
    #expect(discovered.recommended == nil)
    #expect(discovered.testErrorCode == nil)
    let matryoshka = decodeModel(#"{"id":"amazon.nova-2-multimodal-embeddings-v1:0","name":"Nova 2","provider":"bedrock","type":"embedding","dimensions":1024,"supported_dimensions":[256,384,1024,3072],"compatible":true,"tier":"verified"}"#)
    #expect(matryoshka.supportedDimensions == [256, 384, 1024, 3072])
}

@Test("isCurated keeps recommended, verified, and user-tested models of the active provider")
func curationRules() {
    let active: Set<String> = []
    #expect(ModelCuration.isCurated(recommendedBedrock, activeProvider: "bedrock", activeIDs: active))
    #expect(ModelCuration.isCurated(verifiedNotRecommended, activeProvider: "bedrock", activeIDs: active))
    #expect(ModelCuration.isCurated(testedByUser, activeProvider: "bedrock", activeIDs: active))
    // A recommended model whose last test FAILED still shows (it is tier
    // verified), so the user sees the failure state rather than a hole.
    #expect(ModelCuration.isCurated(failedTest, activeProvider: "bedrock", activeIDs: active))
    // Discovered/unverified, incompatible, and other-provider models are out.
    #expect(!ModelCuration.isCurated(discovered, activeProvider: "bedrock", activeIDs: active))
    #expect(!ModelCuration.isCurated(incompatible, activeProvider: "bedrock", activeIDs: active))
    #expect(!ModelCuration.isCurated(otherProvider, activeProvider: "bedrock", activeIDs: active))
}

@Test("isCurated always includes the ACTIVE models, even off-provider or unverified")
func curationKeepsActiveModels() {
    let activeIDs: Set<String> = ["ollama|gemma3:4b", "bedrock|ai21.jamba-1-5-large-v1:0"]
    #expect(ModelCuration.isCurated(otherProvider, activeProvider: "bedrock", activeIDs: activeIDs))
    #expect(ModelCuration.isCurated(discovered, activeProvider: "bedrock", activeIDs: activeIDs))
}

@Test("isCurated degrades to verified+tested when the CLI predates the recommended flag")
func curationDegradesWithoutRecommendedFlag() {
    // Same model, no recommended field anywhere: verified tier still curates.
    let legacyVerified = decodeModel(#"{"id":"us.anthropic.claude-haiku-4-5-20251001-v1:0","name":"Haiku","provider":"bedrock","type":"generation","compatible":true,"tier":"verified"}"#)
    #expect(ModelCuration.isCurated(legacyVerified, activeProvider: "bedrock", activeIDs: []))
    let legacyDiscovered = decodeModel(#"{"id":"x.y","name":"XY","provider":"bedrock","type":"generation","compatible":true,"tier":"unverified"}"#)
    #expect(!ModelCuration.isCurated(legacyDiscovered, activeProvider: "bedrock", activeIDs: []))
}

@Test("partition splits and preserves order; isDemoted marks untested unverified rows")
func partitionAndDemotion() {
    let all = [recommendedBedrock, discovered, testedByUser, otherProvider]
    let (curated, rest) = ModelCuration.partition(all, activeProvider: "bedrock", activeIDs: [])
    #expect(curated.map(\.modelID) == [recommendedBedrock.modelID, testedByUser.modelID])
    #expect(rest.map(\.modelID) == [discovered.modelID, otherProvider.modelID])

    #expect(ModelCuration.isDemoted(discovered))
    #expect(!ModelCuration.isDemoted(recommendedBedrock))
    // A tested unverified model is not demoted: the user asked about it.
    let testedUnverified = decodeModel(#"{"id":"z.z","name":"ZZ","provider":"bedrock","type":"generation","compatible":true,"tier":"unverified","tested_at":"2026-07-01T00:00:00Z","test_error":"boom"}"#)
    #expect(!ModelCuration.isDemoted(testedUnverified))
    // No tier at all (defensive: malformed CLI output) counts as demoted
    // when untested and not curated.
    let noTier = decodeModel(#"{"id":"w.w","name":"WW","provider":"bedrock","type":"generation","compatible":true}"#)
    #expect(ModelCuration.isDemoted(noTier))
    #expect(!ModelCuration.isCurated(noTier, activeProvider: "bedrock", activeIDs: []))
}

@Test("activeIDs collects embed, gen, and rerank slots from ai status")
func activeIDsFromStatus() {
    let status = decodeStatus(#"{"provider":"bedrock","embedding_model":"amazon.nova-2-multimodal-embeddings-v1:0","generation_model":"us.anthropic.claude-haiku-4-5-20251001-v1:0","dimensions":1024,"embed_available":true,"gen_available":true,"embedding_count":1,"document_count":1,"rerank_enabled":true,"rerank_model":"cohere.rerank-v3-5:0","rerank_available":true}"#)
    let ids = ModelCuration.activeIDs(status)
    #expect(ids.contains("bedrock|amazon.nova-2-multimodal-embeddings-v1:0"))
    #expect(ids.contains("bedrock|us.anthropic.claude-haiku-4-5-20251001-v1:0"))
    #expect(ids.contains("bedrock|cohere.rerank-v3-5:0"))
    #expect(ModelCuration.activeIDs(nil).isEmpty)
}
