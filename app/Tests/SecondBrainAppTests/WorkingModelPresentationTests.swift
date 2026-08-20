import Foundation
import Testing
@testable import SecondBrain

private func decodeModel(_ json: String) -> CatalogModelInfo {
    try! JSONDecoder().decode(CatalogModelInfo.self, from: Data(json.utf8))
}

@Test("CatalogModelInfo decodes the CLI working flag and tolerates its absence")
func catalogDecodesWorkingFlag() {
    let working = decodeModel(#"{"id":"us.anthropic.claude-haiku-4-5-20251001-v1:0","name":"Haiku","provider":"bedrock","type":"generation","working":true}"#)
    let legacy = decodeModel(#"{"id":"us.anthropic.claude-sonnet-5","name":"Sonnet 5","provider":"bedrock","type":"generation","tier":"verified"}"#)
    #expect(working.working == true)
    #expect(legacy.working == nil)
}

@Test("Picker row shows price and last probe latency, never embed q= on generation")
func workingRowOmitsQualityOnGeneration() {
    let haiku = decodeModel(#"""
    {"id":"us.anthropic.claude-haiku-4-5-20251001-v1:0","name":"Claude Haiku 4.5","provider":"bedrock","type":"generation","price_input_per_million":1,"price_output_per_million":5,"test_latency_ms":412,"compatible":true,"working":true}
    """#)
    let line = WorkingModelPresentation.rowLine(haiku, why: "shipped default")
    #expect(line.contains("Claude Haiku 4.5"))
    #expect(line.contains("$1.00/$5.00 per M"))
    #expect(line.contains("412ms"))
    #expect(line.contains("shipped default"))
    #expect(!line.contains("q="))
    #expect(!line.contains("0.87"))
}

@Test("Thinking control is mantle-only")
func thinkingHiddenOnConverse() {
    let haiku = decodeModel(#"{"id":"us.anthropic.claude-haiku-4-5-20251001-v1:0","name":"Haiku","provider":"bedrock","type":"generation","invoke_strategy":"bedrock_converse"}"#)
    let grok = decodeModel(#"{"id":"xai.grok-4.3","name":"Grok 4.3","provider":"bedrock","type":"generation","invoke_strategy":"bedrock_mantle_responses"}"#)
    #expect(!WorkingModelPresentation.showsThinking(haiku))
    #expect(WorkingModelPresentation.showsThinking(grok))
}

@Test("Working-set picker prefers CLI working flag over builtin verified")
func pickerUsesWorkingFlag() {
    let unprobed = decodeModel(#"{"id":"us.anthropic.claude-sonnet-5","name":"Sonnet 5","provider":"bedrock","type":"generation","tier":"verified","recommended":true,"compatible":true,"working":false}"#)
    let probed = decodeModel(#"{"id":"us.anthropic.claude-haiku-4-5-20251001-v1:0","name":"Haiku","provider":"bedrock","type":"generation","tier":"verified","recommended":true,"compatible":true,"working":true}"#)
    let picks = WorkingModelPresentation.pickerModels(
        [unprobed, probed],
        type: "generation",
        activeID: nil,
        hasWorkingFlag: true
    )
    #expect(picks.map(\.modelID) == [probed.modelID])
}

@Test("Without a working flag, picker falls back to recommended and active")
func pickerFallsBackWithoutWorkingFlag() {
    let recommended = decodeModel(#"{"id":"us.anthropic.claude-haiku-4-5-20251001-v1:0","name":"Haiku","provider":"bedrock","type":"generation","recommended":true,"compatible":true}"#)
    let other = decodeModel(#"{"id":"amazon.nova-pro-v1:0","name":"Nova Pro","provider":"bedrock","type":"generation","tier":"verified","compatible":true}"#)
    let picks = WorkingModelPresentation.pickerModels(
        [recommended, other],
        type: "generation",
        activeID: other.modelID,
        hasWorkingFlag: false
    )
    #expect(Set(picks.map(\.modelID)) == [recommended.modelID, other.modelID])
}

@Test("working:false on every row still counts as CLI support; pickers do not fall back to recommended")
func pickerDoesNotFallBackWhenWorkingKeyIsAllFalse() {
    let sonnet = decodeModel(#"{"id":"us.anthropic.claude-sonnet-5","name":"Sonnet 5","provider":"bedrock","type":"generation","tier":"verified","recommended":true,"compatible":true,"working":false}"#)
    let haiku = decodeModel(#"{"id":"us.anthropic.claude-haiku-4-5-20251001-v1:0","name":"Haiku","provider":"bedrock","type":"generation","compatible":true,"working":false}"#)
    #expect(WorkingModelPresentation.hasWorkingFlag([sonnet, haiku]))
    let picks = WorkingModelPresentation.pickerModels(
        [sonnet, haiku],
        type: "generation",
        activeID: haiku.modelID,
        hasWorkingFlag: true
    )
    #expect(picks.map(\.modelID) == [haiku.modelID])
    #expect(!picks.map(\.modelID).contains(sonnet.modelID))
}

@Test("Failed active stays selectable even when working is false")
func failedActiveStaysSelectable() {
    let denied = decodeModel(#"{"id":"us.anthropic.claude-haiku-4-5-20251001-v1:0","name":"Haiku","provider":"bedrock","type":"generation","compatible":true,"working":false,"tested_at":"2026-08-20T00:00:00Z","test_error":"403","test_error_code":"access_denied"}"#)
    let sonnet = decodeModel(#"{"id":"us.anthropic.claude-sonnet-5","name":"Sonnet 5","provider":"bedrock","type":"generation","recommended":true,"compatible":true,"working":false}"#)
    let picks = WorkingModelPresentation.pickerModels(
        [denied, sonnet],
        type: "generation",
        activeID: denied.modelID,
        hasWorkingFlag: true
    )
    #expect(picks.map(\.modelID) == [denied.modelID])
}

@Test("Failed probes are listed separately, not as peers of working models")
func failedModelsSeparated() {
    let denied = decodeModel(#"{"id":"us.anthropic.claude-sonnet-5","name":"Sonnet 5","provider":"bedrock","type":"generation","tested_at":"2026-08-20T00:00:00Z","test_error":"403","test_error_code":"access_denied"}"#)
    let ok = decodeModel(#"{"id":"us.anthropic.claude-haiku-4-5-20251001-v1:0","name":"Haiku","provider":"bedrock","type":"generation","tested_at":"2026-08-20T00:00:00Z","working":true}"#)
    let failed = WorkingModelPresentation.failedModels([denied, ok], type: "generation")
    #expect(failed.map(\.modelID) == [denied.modelID])
    #expect(WorkingModelPresentation.why(denied, isShippedDefault: false, cheapestID: nil, fastestID: nil) == "no access")
}

@Test("Failed-validation list stays on the active provider")
func failedModelsFilterToProvider() {
    let bedrock = decodeModel(#"{"id":"us.anthropic.claude-sonnet-5","name":"Sonnet 5","provider":"bedrock","type":"generation","tested_at":"2026-08-20T00:00:00Z","test_error":"403","test_error_code":"access_denied"}"#)
    let openrouter = decodeModel(#"{"id":"openai/gpt-4o","name":"GPT-4o","provider":"openrouter","type":"generation","tested_at":"2026-08-20T00:00:00Z","test_error":"401","test_error_code":"bad_credentials"}"#)
    let failed = WorkingModelPresentation.failedModels([bedrock, openrouter], type: "generation", provider: "bedrock")
    #expect(failed.map(\.modelID) == [bedrock.modelID])
}

@Test("Failed disclosure names probe failures, not enable failures")
func failedDisclosureIsHonest() {
    #expect(WorkingModelPresentation.failedDisclosureTitle(count: 1) == "1 failed validation")
    #expect(WorkingModelPresentation.failedDisclosureTitle(count: 3) == "3 failed validation")
    #expect(!WorkingModelPresentation.failedDisclosureTitle(count: 2).contains("enabled"))
}

@Test("Validate nudge still shows when only untested actives are working")
func validateNudgeOnFreshVaultActives() {
    let haiku = decodeModel(#"{"id":"us.anthropic.claude-haiku-4-5-20251001-v1:0","name":"Haiku","provider":"bedrock","type":"generation","working":true}"#)
    let nova = decodeModel(#"{"id":"amazon.nova-2-multimodal-embeddings-v1:0","name":"Nova 2","provider":"bedrock","type":"embedding","working":true}"#)
    let sonnet = decodeModel(#"{"id":"us.anthropic.claude-sonnet-5","name":"Sonnet 5","provider":"bedrock","type":"generation","working":false}"#)
    let actives: Set<String> = [haiku.modelID, nova.modelID]

    #expect(WorkingModelPresentation.hasWorkingFlag([haiku, nova, sonnet]))
    #expect(WorkingModelPresentation.nothingProbed([haiku, nova, sonnet]))
    #expect(WorkingModelPresentation.onlyActivesAreWorking([haiku, nova, sonnet], activeIDs: actives))
    #expect(WorkingModelPresentation.shouldNudgeValidate([haiku, nova, sonnet], activeIDs: actives))

    // A real probe on a non-active model means Validate already ran.
    let probed = decodeModel(#"{"id":"us.anthropic.claude-sonnet-5","name":"Sonnet 5","provider":"bedrock","type":"generation","working":true,"tested_at":"2026-08-20T00:00:00Z"}"#)
    #expect(!WorkingModelPresentation.shouldNudgeValidate([haiku, nova, probed], activeIDs: actives))

    // Pre-flag CLI (no working field) still nudges.
    let legacy = decodeModel(#"{"id":"us.anthropic.claude-haiku-4-5-20251001-v1:0","name":"Haiku","provider":"bedrock","type":"generation","recommended":true}"#)
    #expect(WorkingModelPresentation.shouldNudgeValidate([legacy], activeIDs: [legacy.modelID]))
}

@Test("Why line never uses embed quality_score for generation")
func whyIgnoresEmbedQuality() {
    let gen = decodeModel(#"""
    {"id":"us.anthropic.claude-haiku-4-5-20251001-v1:0","name":"Haiku","provider":"bedrock","type":"generation","price_input_per_million":1,"price_output_per_million":5,"test_latency_ms":200,"benchmark":{"quality_score":0.87,"avg_latency_ms":180}}
    """#)
    let why = WorkingModelPresentation.why(
        gen,
        isShippedDefault: true,
        cheapestID: gen.modelID,
        fastestID: gen.modelID
    )
    #expect(why == "shipped default")
    #expect(!why.contains("0.87"))
}
