import Foundation
import Testing
@testable import SecondBrain

/// Decode an `AIStatusInfo` from a JSON fixture (the same shape `2nb ai status
/// --json` emits), so the Home AI-card logic can be exercised without a vault.
private func decodeStatus(_ json: String) -> AIStatusInfo {
    try! JSONDecoder().decode(AIStatusInfo.self, from: Data(json.utf8))
}

@Test("HomeAI.friendlyModel maps the default Bedrock models and passes others through")
func homeAIFriendlyModel() {
    #expect(HomeAI.friendlyModel(HomeAI.genModel) == "Claude Haiku 4.5")
    #expect(HomeAI.friendlyModel(HomeAI.embedModel) == "Amazon Nova-2")
    #expect(HomeAI.friendlyModel("some.other.model") == "some.other.model")
    #expect(HomeAI.friendlyModel("") == nil)
    #expect(HomeAI.friendlyModel(nil) == nil)
}

@Test("HomeAI.statusLine reports ready, the provider reason, or a credentials fallback")
func homeAIStatusLine() {
    #expect(HomeAI.statusLine(nil) == "Checking…")

    let ready = decodeStatus(#"{"provider":"bedrock","embedding_model":"e","generation_model":"g","dimensions":1024,"embed_available":true,"gen_available":true,"embedding_count":10,"document_count":10}"#)
    #expect(HomeAI.statusLine(ready) == "Bedrock ready")

    // Not ready, with an actionable provider reason → surface the reason.
    let withReason = decodeStatus(#"{"provider":"bedrock","embedding_model":"e","generation_model":"g","dimensions":1024,"embed_available":false,"gen_available":false,"embedding_count":0,"document_count":10,"providers":[{"name":"bedrock","config_present":true,"disabled":false,"reachable":false,"reason":"AccessDeniedException: enable model access"}]}"#)
    #expect(HomeAI.statusLine(withReason) == "Not ready — AccessDeniedException: enable model access")

    // Not ready, no provider reason available → generic credentials fallback.
    let noReason = decodeStatus(#"{"provider":"bedrock","embedding_model":"e","generation_model":"g","dimensions":1024,"embed_available":false,"gen_available":true,"embedding_count":0,"document_count":10}"#)
    #expect(HomeAI.statusLine(noReason) == "Not ready — check AWS credentials")
}

@Test("HomeAI defaults mirror the CLI DefaultAIConfig contract")
func homeAIDefaults() {
    #expect(HomeAI.provider == "bedrock")
    #expect(HomeAI.genModel == "us.anthropic.claude-haiku-4-5-20251001-v1:0")
    #expect(HomeAI.embedModel == "amazon.nova-2-multimodal-embeddings-v1:0")
    #expect(HomeAI.dims == 1024)
}
