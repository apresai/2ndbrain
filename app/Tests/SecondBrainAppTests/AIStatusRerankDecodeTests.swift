import Foundation
import Testing
@testable import SecondBrain

/// Locks the Swift decoding of the rerank slot fields in `2nb ai status --json`
/// (added alongside surfacing the rerank slot across the GUI), including the
/// back-compat path: an older CLI that omits them decodes to nil, not a failure.
@Test("AIStatusInfo decodes the rerank slot fields")
func aiStatusDecodesRerankFields() throws {
    let json = """
    {
      "provider":"bedrock",
      "embedding_model":"amazon.nova-2-multimodal-embeddings-v1:0",
      "generation_model":"us.anthropic.claude-haiku-4-5-20251001-v1:0",
      "dimensions":1024,
      "embed_available":true,
      "gen_available":true,
      "embedding_count":10,
      "document_count":12,
      "rerank_enabled":true,
      "rerank_model":"cohere.rerank-v3-5:0",
      "rerank_available":true
    }
    """
    let s = try JSONDecoder().decode(AIStatusInfo.self, from: Data(json.utf8))
    #expect(s.rerankEnabled == true)
    #expect(s.rerankModel == "cohere.rerank-v3-5:0")
    #expect(s.rerankAvailable == true)
}

@Test("AIStatusInfo without rerank fields decodes them as nil (older CLI)")
func aiStatusRerankBackCompat() throws {
    let json = """
    {
      "provider":"bedrock",
      "embedding_model":"amazon.nova-2",
      "generation_model":"claude-haiku",
      "dimensions":1024,
      "embed_available":true,
      "gen_available":true,
      "embedding_count":0,
      "document_count":0
    }
    """
    let s = try JSONDecoder().decode(AIStatusInfo.self, from: Data(json.utf8))
    #expect(s.rerankEnabled == nil)
    #expect(s.rerankModel == nil)
    #expect(s.rerankAvailable == nil)
}
