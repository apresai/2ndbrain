import Foundation
import Testing
@testable import SecondBrain

/// Decode an `AIStatusInfo` from a JSON fixture (the same shape `2nb ai status
/// --json` emits), so the Home AI-card logic can be exercised without a vault.
private func decodeStatus(_ json: String) -> AIStatusInfo {
    try! JSONDecoder().decode(AIStatusInfo.self, from: Data(json.utf8))
}

@Test("HomeAI.modelValue shows the raw id or a not-set placeholder")
func homeAIModelValue() {
    #expect(HomeAI.modelValue("us.anthropic.claude-sonnet-5") == "us.anthropic.claude-sonnet-5")
    #expect(HomeAI.modelValue("") == "(not set)")
    #expect(HomeAI.modelValue(nil) == "(not set)")
}

@Test("HomeAI.headerTitle reflects the ACTIVE provider, never hardcoded copy")
func homeAIHeaderTitle() {
    #expect(HomeAI.headerTitle(nil) == "AI · checking…")

    let gemini = decodeStatus(#"{"provider":"gemini","embedding_model":"e","generation_model":"g","dimensions":768,"embed_available":true,"gen_available":true,"embedding_count":10,"document_count":10}"#)
    #expect(HomeAI.headerTitle(gemini) == "AI · Google Gemini (GCP)")

    let bedrock = decodeStatus(#"{"provider":"bedrock","embedding_model":"e","generation_model":"g","dimensions":1024,"embed_available":true,"gen_available":true,"embedding_count":10,"document_count":10}"#)
    #expect(HomeAI.headerTitle(bedrock) == "AI · AWS Bedrock")

    let ollama = decodeStatus(#"{"provider":"ollama","embedding_model":"e","generation_model":"g","dimensions":768,"embed_available":true,"gen_available":true,"embedding_count":10,"document_count":10}"#)
    #expect(HomeAI.headerTitle(ollama) == "AI · Ollama (local)")
}

@Test("HomeAI.statusLine is provider-generic: ready, reason, or credentials fallback")
func homeAIStatusLine() {
    #expect(HomeAI.statusLine(nil) == "Checking…")

    let readyGemini = decodeStatus(#"{"provider":"gemini","embedding_model":"gemini-embedding-2","generation_model":"gemini-4-hydrogen","dimensions":768,"embed_available":true,"gen_available":true,"embedding_count":10,"document_count":10}"#)
    #expect(HomeAI.statusLine(readyGemini) == "Google Gemini (GCP) ready")

    let ready = decodeStatus(#"{"provider":"bedrock","embedding_model":"e","generation_model":"g","dimensions":1024,"embed_available":true,"gen_available":true,"embedding_count":10,"document_count":10}"#)
    #expect(HomeAI.statusLine(ready) == "AWS Bedrock ready")

    let readyOllama = decodeStatus(#"{"provider":"ollama","embedding_model":"e","generation_model":"g","dimensions":768,"embed_available":true,"gen_available":true,"embedding_count":10,"document_count":10}"#)
    #expect(HomeAI.statusLine(readyOllama) == "Ollama (local) ready")

    // Not ready, with an actionable reason from the ACTIVE provider.
    let withReason = decodeStatus(#"{"provider":"ollama","embedding_model":"e","generation_model":"g","dimensions":768,"embed_available":false,"gen_available":false,"embedding_count":0,"document_count":10,"providers":[{"name":"bedrock","config_present":true,"disabled":false,"reachable":true},{"name":"ollama","config_present":true,"disabled":false,"reachable":false,"reason":"server not running"}]}"#)
    #expect(HomeAI.statusLine(withReason) == "Not ready: server not running")

    // Not ready, no provider reason available: provider-named fallback.
    let noReason = decodeStatus(#"{"provider":"openrouter","embedding_model":"e","generation_model":"g","dimensions":1024,"embed_available":false,"gen_available":true,"embedding_count":0,"document_count":10}"#)
    #expect(HomeAI.statusLine(noReason) == "Not ready: check OpenRouter credentials")
}

@Test("HomeAI defaults mirror the CLI DefaultAIConfig contract")
func homeAIDefaults() {
    #expect(HomeAI.provider == "gemini")
    #expect(HomeAI.genModel == "gemini-4-hydrogen")
    #expect(HomeAI.embedModel == "gemini-embedding-2")
    #expect(HomeAI.dims == 768)
}

@Test("HomeAI.differsFromDefaults gates the Reset button on real drift")
func homeAIDiffersFromDefaults() {
    // Unknown status: no button (nothing to compare against).
    #expect(HomeAI.differsFromDefaults(nil) == false)

    // Exactly the defaults: no drift, no button.
    let defaults = decodeStatus(#"{"provider":"gemini","embedding_model":"gemini-embedding-2","generation_model":"gemini-4-hydrogen","dimensions":768,"embed_available":true,"gen_available":true,"embedding_count":10,"document_count":10}"#)
    #expect(HomeAI.differsFromDefaults(defaults) == false)

    // A different provider, model, or dimension each counts as drift.
    let otherProvider = decodeStatus(#"{"provider":"ollama","embedding_model":"gemini-embedding-2","generation_model":"gemini-4-hydrogen","dimensions":768,"embed_available":true,"gen_available":true,"embedding_count":10,"document_count":10}"#)
    #expect(HomeAI.differsFromDefaults(otherProvider))

    let otherGen = decodeStatus(#"{"provider":"gemini","embedding_model":"gemini-embedding-2","generation_model":"gemini-4-carbon","dimensions":768,"embed_available":true,"gen_available":true,"embedding_count":10,"document_count":10}"#)
    #expect(HomeAI.differsFromDefaults(otherGen))

    let otherDims = decodeStatus(#"{"provider":"gemini","embedding_model":"gemini-embedding-2","generation_model":"gemini-4-hydrogen","dimensions":256,"embed_available":true,"gen_available":true,"embedding_count":10,"document_count":10}"#)
    #expect(HomeAI.differsFromDefaults(otherDims))
}

@Test("HomeAI.resetConfirmText names the target and the current config")
func homeAIResetConfirmText() {
    let current = decodeStatus(#"{"provider":"ollama","embedding_model":"nomic-embed-text","generation_model":"gemma3:4b","dimensions":768,"embed_available":true,"gen_available":true,"embedding_count":10,"document_count":10}"#)
    let text = HomeAI.resetConfirmText(current)
    #expect(text.contains(HomeAI.genModel))
    #expect(text.contains(HomeAI.embedModel))
    #expect(text.contains("Ollama (local)"))
    #expect(text.contains("gemma3:4b"))
}

@Test("ProviderDisplay maps known providers and capitalizes unknowns")
func providerDisplayNames() {
    #expect(ProviderDisplay.name("gemini") == "Google Gemini (GCP)")
    #expect(ProviderDisplay.name("google") == "Google Gemini (GCP)")
    #expect(ProviderDisplay.name("bedrock") == "AWS Bedrock")
    #expect(ProviderDisplay.name("openrouter") == "OpenRouter")
    #expect(ProviderDisplay.name("ollama") == "Ollama (local)")
    #expect(ProviderDisplay.name("llama-local") == "Local Gemma (On-Device)")
    #expect(ProviderDisplay.name("something") == "Something")
}

@Test("HomeAI.reembedHintAfterSave nudges only on a model/dimension mismatch")
func homeAIReembedHintAfterSave() {
    // nil status (no AI info yet) → no hint, plain confirmation shows instead.
    #expect(HomeAI.reembedHintAfterSave(nil) == nil)

    // Healthy vault (embeddings match the active model) → no hint.
    let ok = decodeStatus(#"{"provider":"bedrock","embedding_model":"e","generation_model":"g","dimensions":1024,"embed_available":true,"gen_available":true,"embedding_count":10,"document_count":10,"portability_status":"ok"}"#)
    #expect(HomeAI.reembedHintAfterSave(ok) == nil)

    // Unindexed / no-provider states are not a "wrong embeddings" problem → no hint.
    for status in ["unindexed", "no_provider", "provider_unavailable", "stale"] {
        let s = decodeStatus(#"{"provider":"bedrock","embedding_model":"e","generation_model":"g","dimensions":1024,"embed_available":true,"gen_available":true,"embedding_count":0,"document_count":10,"portability_status":"\#(status)"}"#)
        #expect(HomeAI.reembedHintAfterSave(s) == nil, "\(status) should not prompt a re-embed")
    }

    // The three mismatch states each prompt a Re-embed All nudge.
    for status in ["dimension_break", "mixed", "model_mismatch"] {
        let s = decodeStatus(#"{"provider":"bedrock","embedding_model":"e","generation_model":"g","dimensions":1024,"embed_available":true,"gen_available":true,"embedding_count":10,"document_count":10,"portability_status":"\#(status)"}"#)
        let hint = HomeAI.reembedHintAfterSave(s)
        #expect(hint != nil, "\(status) should prompt a re-embed")
        #expect(hint?.contains("Re-embed All") == true, "\(status) hint should name the Re-embed All action")
    }
}

@Test("AIStatusInfo.embeddableDenominator prefers vault_embeddable_docs, falls back to document_count")
func aiStatusEmbeddableDenominator() {
    // Current CLI: 117 files, 2 empty notes → 115 embeddable. The embedded
    // ratio should read 115/115, not 115/117.
    let current = decodeStatus(#"{"provider":"bedrock","embedding_model":"e","generation_model":"g","dimensions":1024,"embed_available":true,"gen_available":true,"embedding_count":115,"document_count":117,"vault_embeddable_docs":115,"vault_empty_docs":2,"portability_status":"ok"}"#)
    #expect(current.embeddableDenominator == 115)
    #expect(current.embeddingCount == 115)

    // Older 2nb binary without the field → fall back to the raw document count
    // so the GUI still shows a sensible denominator after a CLI/app drift.
    let legacy = decodeStatus(#"{"provider":"bedrock","embedding_model":"e","generation_model":"g","dimensions":1024,"embed_available":true,"gen_available":true,"embedding_count":115,"document_count":117}"#)
    #expect(legacy.embeddableDenominator == 117)
}

@Test("settingsLinkTitle leads with the key when not ready, stays as a subdued route when ready")
func settingsLinkCopy() {
    #expect(HomeAI.settingsLinkTitle(ready: false) == "Add your API key in Settings…")
    #expect(HomeAI.settingsLinkTitle(ready: true) == "AI settings…")
}

@Test("HomeAI handles mixed embedding and generation providers cleanly")
func homeAIMixedProviders() {
    let mixedReady = decodeStatus(#"{"provider":"gemini","embedding_provider":"llama-local","generation_provider":"gemini","embedding_model":"embeddinggemma-2","generation_model":"gemini-4-hydrogen","dimensions":256,"embed_available":true,"gen_available":true,"embedding_count":10,"document_count":10}"#)
    #expect(mixedReady.isMixedProviders == true)
    #expect(mixedReady.effectiveEmbeddingProvider == "llama-local")
    #expect(mixedReady.effectiveGenerationProvider == "gemini")
    #expect(HomeAI.headerTitle(mixedReady) == "AI · Google Gemini (GCP) + Local Gemma (On-Device)")
    #expect(HomeAI.statusLine(mixedReady) == "Hybrid AI ready")
    #expect(HomeAI.differsFromDefaults(mixedReady) == true)

    let mixedFailing = decodeStatus(#"{"provider":"gemini","embedding_provider":"llama-local","generation_provider":"gemini","embedding_model":"embeddinggemma-2","generation_model":"gemini-4-hydrogen","dimensions":256,"embed_available":true,"gen_available":false,"embedding_count":10,"document_count":10,"providers":[{"name":"gemini","config_present":false,"disabled":false,"reachable":false,"reason":"credentials missing"},{"name":"llama-local","config_present":true,"disabled":false,"reachable":true}]}"#)
    #expect(HomeAI.statusLine(mixedFailing) == "Not ready: credentials missing")
}
