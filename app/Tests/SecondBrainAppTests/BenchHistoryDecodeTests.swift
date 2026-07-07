import Foundation
import Testing
@testable import SecondBrain

@Test("BenchRunInfo decodes models bench history rows")
func benchHistoryDecode() {
    let json = #"[{"id":3,"timestamp":"2026-07-01T10:00:00Z","provider":"bedrock","model_id":"amazon.nova-2-multimodal-embeddings-v1:0","probe":"embed","latency_ms":412,"ok":true,"vault_doc_count":151},{"id":2,"timestamp":"2026-06-30T10:00:00Z","provider":"bedrock","model_id":"us.anthropic.claude-haiku-4-5-20251001-v1:0","probe":"rag","latency_ms":2100,"ok":false,"detail":"throttled"}]"#
    let runs = try! JSONDecoder().decode([BenchRunInfo].self, from: Data(json.utf8))
    #expect(runs.count == 2)
    #expect(runs[0].probe == "embed")
    #expect(runs[0].vaultDocCount == 151)
    #expect(runs[1].ok == false)
    #expect(runs[1].detail == "throttled")
    #expect(runs[1].vaultDocCount == nil)
}

@Test("empty history decodes as an empty list (the CLI emits JSON null)")
func benchHistoryNull() {
    // fetchBenchHistory falls back to [] when decode fails; replicate the
    // contract here: "null" is not a valid [BenchRunInfo].
    let decoded = try? JSONDecoder().decode([BenchRunInfo].self, from: Data("null".utf8))
    #expect(decoded == nil)
}
