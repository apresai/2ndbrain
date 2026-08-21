import Foundation
import Testing
@testable import SecondBrain

private func decodeModel(_ json: String) -> CatalogModelInfo {
    try! JSONDecoder().decode(CatalogModelInfo.self, from: Data(json.utf8))
}

// MARK: - modelKey / modelKeys

@Test("modelKey joins provider and model ID with a pipe")
func modelKeyFormat() {
    #expect(DiscoveryNudge.modelKey(provider: "bedrock", modelID: "deepseek.v3.2") == "bedrock|deepseek.v3.2")
}

@Test("modelKeys builds the same keys as repeated modelKey calls")
func modelKeysBuildsSet() {
    let keys = DiscoveryNudge.modelKeys(provider: "bedrock", modelIDs: ["a", "b"])
    #expect(keys == ["bedrock|a", "bedrock|b"])
}

// MARK: - probeableAndEnabled (the Validate candidate-set predicate)

@Test("probeableAndEnabled keeps a plain generation model with no enabled/compatible fields")
func probeableAndEnabledDefaultsToTrue() {
    let model = decodeModel(#"{"id":"deepseek.v3.2","name":"DeepSeek V3.2","provider":"bedrock","type":"generation"}"#)
    let out = DiscoveryNudge.probeableAndEnabled([model], provider: "bedrock")
    #expect(out.map(\.modelID) == [model.modelID])
}

@Test("probeableAndEnabled drops models from a different provider")
func probeableAndEnabledFiltersProvider() {
    let bedrock = decodeModel(#"{"id":"deepseek.v3.2","name":"DeepSeek","provider":"bedrock","type":"generation"}"#)
    let openrouter = decodeModel(#"{"id":"openai/gpt-4o","name":"GPT-4o","provider":"openrouter","type":"generation"}"#)
    let out = DiscoveryNudge.probeableAndEnabled([bedrock, openrouter], provider: "bedrock")
    #expect(out.map(\.modelID) == [bedrock.modelID])
}

@Test("probeableAndEnabled drops a vendor-policy-disabled model")
func probeableAndEnabledFiltersDisabled() {
    let disabled = decodeModel(#"{"id":"zai.glm-5","name":"GLM 5","provider":"bedrock","type":"generation","enabled":false}"#)
    let enabled = decodeModel(#"{"id":"deepseek.v3.2","name":"DeepSeek","provider":"bedrock","type":"generation","enabled":true}"#)
    let out = DiscoveryNudge.probeableAndEnabled([disabled, enabled], provider: "bedrock")
    #expect(out.map(\.modelID) == [enabled.modelID])
}

@Test("probeableAndEnabled drops rerank models")
func probeableAndEnabledFiltersRerank() {
    let rerank = decodeModel(#"{"id":"cohere.rerank-v3-5:0","name":"Cohere Rerank","provider":"bedrock","type":"rerank"}"#)
    let gen = decodeModel(#"{"id":"deepseek.v3.2","name":"DeepSeek","provider":"bedrock","type":"generation"}"#)
    let out = DiscoveryNudge.probeableAndEnabled([rerank, gen], provider: "bedrock")
    #expect(out.map(\.modelID) == [gen.modelID])
}

@Test("probeableAndEnabled drops statically incompatible models")
func probeableAndEnabledFiltersIncompatible() {
    let incompatible = decodeModel(#"{"id":"stability.image-gen","name":"Image Gen","provider":"bedrock","type":"generation","compatible":false}"#)
    let compatible = decodeModel(#"{"id":"deepseek.v3.2","name":"DeepSeek","provider":"bedrock","type":"generation","compatible":true}"#)
    let out = DiscoveryNudge.probeableAndEnabled([incompatible, compatible], provider: "bedrock")
    #expect(out.map(\.modelID) == [compatible.modelID])
}

// MARK: - newIDs

@Test("newIDs returns only models absent from the seen snapshot")
func newIDsFiltersSeen() {
    let seenModel = decodeModel(#"{"id":"us.anthropic.claude-haiku-4-5-20251001-v1:0","name":"Haiku","provider":"bedrock","type":"generation"}"#)
    let newModel = decodeModel(#"{"id":"deepseek.v3.2","name":"DeepSeek","provider":"bedrock","type":"generation"}"#)
    let seen: Set<String> = [DiscoveryNudge.modelKey(provider: "bedrock", modelID: seenModel.modelID)]
    let ids = DiscoveryNudge.newIDs(models: [seenModel, newModel], provider: "bedrock", seen: seen)
    #expect(ids == [newModel.modelID])
}

@Test("newIDs is empty once every probeable model has been seen")
func newIDsEmptyWhenAllSeen() {
    let a = decodeModel(#"{"id":"a","name":"A","provider":"bedrock","type":"generation"}"#)
    let b = decodeModel(#"{"id":"b","name":"B","provider":"bedrock","type":"embedding"}"#)
    let seen = DiscoveryNudge.modelKeys(provider: "bedrock", modelIDs: [a.modelID, b.modelID])
    #expect(DiscoveryNudge.newIDs(models: [a, b], provider: "bedrock", seen: seen).isEmpty)
}

@Test("newIDs never nudges about a vendor-policy-disabled model, seen or not")
func newIDsIgnoresPolicyDisabled() {
    let disabled = decodeModel(#"{"id":"zai.glm-5","name":"GLM 5","provider":"bedrock","type":"generation","enabled":false}"#)
    let ids = DiscoveryNudge.newIDs(models: [disabled], provider: "bedrock", seen: [])
    #expect(ids.isEmpty)
}

@Test("newIDs scopes to the requested provider only")
func newIDsFiltersToProvider() {
    let bedrock = decodeModel(#"{"id":"deepseek.v3.2","name":"DeepSeek","provider":"bedrock","type":"generation"}"#)
    let openrouter = decodeModel(#"{"id":"openai/gpt-4o","name":"GPT-4o","provider":"openrouter","type":"generation"}"#)
    let ids = DiscoveryNudge.newIDs(models: [bedrock, openrouter], provider: "bedrock", seen: [])
    #expect(ids == [bedrock.modelID])
}

// MARK: - shouldSuppressFirstRun

@Test("shouldSuppressFirstRun is true with no snapshot at all")
func suppressFirstRunNilSnapshot() {
    #expect(DiscoveryNudge.shouldSuppressFirstRun(seen: nil, provider: "bedrock"))
}

@Test("shouldSuppressFirstRun is true for an empty snapshot (no provider has ever seeded)")
func suppressFirstRunEmptySnapshot() {
    #expect(DiscoveryNudge.shouldSuppressFirstRun(seen: [], provider: "bedrock"))
}

@Test("shouldSuppressFirstRun is false once this provider's own key exists")
func suppressFirstRunProviderAlreadySeeded() {
    #expect(!DiscoveryNudge.shouldSuppressFirstRun(seen: ["bedrock|a"], provider: "bedrock"))
}

@Test("shouldSuppressFirstRun is scoped per provider: another provider's seeded snapshot does not cover a newly activated one")
func suppressFirstRunIsPerProviderNotGlobal() {
    // Regression: a global (seen == nil) check would suppress forever once
    // ANY provider has ever seeded, then badge a later-activated provider's
    // entire catalog as new the first time updateDiscoveryNudge sees it.
    let seenAfterBedrockSeeded: Set<String> = ["bedrock|us.anthropic.claude-haiku-4-5-20251001-v1:0"]
    #expect(!DiscoveryNudge.shouldSuppressFirstRun(seen: seenAfterBedrockSeeded, provider: "bedrock"))
    #expect(DiscoveryNudge.shouldSuppressFirstRun(seen: seenAfterBedrockSeeded, provider: "openrouter"))
}

// MARK: - Dismiss-then-rediscover idempotence

@Test("Dismissing the current new IDs clears the banner; a later real discovery reopens it")
func dismissThenRediscoverIdempotence() {
    let existing = decodeModel(#"{"id":"us.anthropic.claude-haiku-4-5-20251001-v1:0","name":"Haiku","provider":"bedrock","type":"generation"}"#)
    let discovered = decodeModel(#"{"id":"deepseek.v3.2","name":"DeepSeek","provider":"bedrock","type":"generation"}"#)

    // First reload after `existing` was already seeded: `discovered` is new.
    var seen = DiscoveryNudge.modelKeys(provider: "bedrock", modelIDs: [existing.modelID])
    var newIDs = DiscoveryNudge.newIDs(models: [existing, discovered], provider: "bedrock", seen: seen)
    #expect(newIDs == [discovered.modelID])

    // Dismiss: merge the current new IDs into the snapshot.
    seen.formUnion(DiscoveryNudge.modelKeys(provider: "bedrock", modelIDs: newIDs))
    newIDs = DiscoveryNudge.newIDs(models: [existing, discovered], provider: "bedrock", seen: seen)
    #expect(newIDs.isEmpty)

    // Re-running on the same catalog stays empty (idempotent dismiss).
    newIDs = DiscoveryNudge.newIDs(models: [existing, discovered], provider: "bedrock", seen: seen)
    #expect(newIDs.isEmpty)

    // A genuinely new model discovered later reopens the banner for just
    // that model, not for models already dismissed.
    let laterDiscovery = decodeModel(#"{"id":"qwen.qwen3-235b","name":"Qwen3","provider":"bedrock","type":"generation"}"#)
    newIDs = DiscoveryNudge.newIDs(models: [existing, discovered, laterDiscovery], provider: "bedrock", seen: seen)
    #expect(newIDs == [laterDiscovery.modelID])
}

// MARK: - allProviderKeys (the recording-side universe)

@Test("allProviderKeys records disabled and incompatible models too")
func allProviderKeysIgnoresFilters() {
    let enabled = decodeModel(#"{"id":"deepseek.v3.2","name":"DeepSeek","provider":"bedrock","type":"generation"}"#)
    let disabled = decodeModel(#"{"id":"zai.glm-5","name":"GLM 5","provider":"bedrock","type":"generation","enabled":false}"#)
    let rerank = decodeModel(#"{"id":"cohere.rerank-v3-5:0","name":"Rerank","provider":"bedrock","type":"rerank"}"#)
    let other = decodeModel(#"{"id":"openai/gpt-4o","name":"GPT-4o","provider":"openrouter","type":"generation"}"#)
    let keys = DiscoveryNudge.allProviderKeys([enabled, disabled, rerank, other], provider: "bedrock")
    #expect(keys == ["bedrock|deepseek.v3.2", "bedrock|zai.glm-5", "bedrock|cohere.rerank-v3-5:0"])
}

@Test("a vendor-checkbox toggle never re-badges models seeded while disabled")
func vendorToggleDoesNotNudge() {
    // First run: zai is policy-disabled. Seeding records the FULL catalog.
    let disabledZai = decodeModel(#"{"id":"zai.glm-5","name":"GLM 5","provider":"bedrock","type":"generation","enabled":false}"#)
    let seen = DiscoveryNudge.allProviderKeys([disabledZai], provider: "bedrock")
    // The user then enables the zai vendor: same model, now enabled. A
    // filter change is not a discovery event, so nothing is "new".
    let enabledZai = decodeModel(#"{"id":"zai.glm-5","name":"GLM 5","provider":"bedrock","type":"generation","enabled":true}"#)
    #expect(DiscoveryNudge.newIDs(models: [enabledZai], provider: "bedrock", seen: seen).isEmpty)
    // A genuinely new arrival still nudges once its vendor is enabled.
    let fresh = decodeModel(#"{"id":"zai.glm-6","name":"GLM 6","provider":"bedrock","type":"generation","enabled":true}"#)
    #expect(DiscoveryNudge.newIDs(models: [enabledZai, fresh], provider: "bedrock", seen: seen) == ["zai.glm-6"])
}
