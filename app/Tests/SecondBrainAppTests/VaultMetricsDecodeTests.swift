import Foundation
import Testing
@testable import SecondBrain

/// Locks the Swift decoding of `2nb metrics --json` against the CLI's JSON
/// contract — especially that `omitempty`-absent fields decode to `nil` (an MCP
/// search carries no `result_count`/`docs_indexed`) and that `last_build` can be
/// null on an empty vault.
@Test("VaultMetrics decodes a full metrics --json payload")
func vaultMetricsDecodeFull() throws {
    let json = """
    {
      "last_build": {"id":1,"ts":"2026-06-29T21:41:28Z","operation":"index","source":"cli","duration_ms":14297,"ok":true,"files_scanned":2,"docs_indexed":2,"chunks_created":2,"embedded":2,"embed_ms":13543,"total_chars":95,"embedding_model":"amazon.nova-2","embedding_dims":1024,"cli_version":"dev","docs_per_sec":0.14,"embeddings_per_sec":0.15,"chars_per_sec":6.64},
      "gauges": {"doc_count":2,"embedded_count":2,"embedding_coverage":1,"chunk_count":2,"stale_count":0,"index_db_bytes":4370432,"wal_bytes":4152,"last_index_at":"2026-06-29T21:41:28Z","embedding_model":"amazon.nova-2","embedding_dims":1024},
      "recent": [
        {"id":4,"ts":"2026-06-29T21:41:35Z","operation":"ask","source":"cli","duration_ms":2100,"ok":true,"result_count":3,"input_tokens":1200,"output_tokens":340},
        {"id":3,"ts":"2026-06-29T21:41:31Z","operation":"search","source":"mcp","duration_ms":1129,"ok":true,"mode":"hybrid","cli_version":"dev"},
        {"id":1,"ts":"2026-06-29T21:41:28Z","operation":"index","source":"cli","duration_ms":14297,"ok":true,"docs_indexed":2,"docs_per_sec":0.14,"input_tokens":23}
      ],
      "aggregates": {"index":{"count":1,"avg_ms":14297,"p50_ms":14297,"avg_docs_per_sec":0.14,"tokens_in":23},"ask":{"count":1,"avg_ms":2100,"p50_ms":2100,"tokens_in":1200,"tokens_out":340}},
      "total_input_tokens":1223,"total_output_tokens":340
    }
    """
    let m = try JSONDecoder().decode(VaultMetrics.self, from: Data(json.utf8))

    let b = try #require(m.lastBuild)
    #expect(b.operation == "index")
    #expect(b.durationMs == 14297)
    #expect(b.docsIndexed == 2)
    #expect(b.docsPerSec == 0.14)
    #expect(b.embeddingModel == "amazon.nova-2")

    #expect(m.gauges.docCount == 2)
    #expect(m.gauges.embeddingCoverage == 1)
    #expect(m.gauges.indexDbBytes == 4370432)
    #expect(m.gauges.walBytes == 4152)

    let recent = try #require(m.recent)
    #expect(recent.count == 3)
    let search = try #require(recent.first { $0.operation == "search" })
    #expect(search.source == "mcp")
    #expect(search.mode == "hybrid")
    #expect(search.resultCount == nil) // omitempty absent → nil (MCP query rows have no count)
    #expect(search.docsIndexed == nil)

    // ask carries real generation tokens (Bedrock); embed an estimate.
    let ask = try #require(recent.first { $0.operation == "ask" })
    #expect(ask.inputTokens == 1200)
    #expect(ask.outputTokens == 340)
    #expect(ask.resultCount == 3)

    let aggs = try #require(m.aggregates)
    #expect(aggs["index"]?.count == 1)
    #expect(aggs["index"]?.avgDocsPerSec == 0.14)
    #expect(aggs["index"]?.tokensIn == 23)
    #expect(aggs["ask"]?.tokensIn == 1200)
    #expect(aggs["ask"]?.tokensOut == 340)
    #expect(aggs["ask"]?.avgDocsPerSec == nil) // omitempty absent → nil

    #expect(m.totalInputTokens == 1223)
    #expect(m.totalOutputTokens == 340)

    // Identifiable ids are unique across the recent list (no ForEach collisions).
    #expect(Set(recent.map(\.id)).count == recent.count)
}

// Decodes the real empty-vault contract. The CLI now emits `[]`/`{}`, but a nil
// Go slice/map would marshal to `null`; decoding must survive either so a fresh
// or `metrics clear`-ed vault never blanks the whole tab on a decode throw.
@Test("VaultMetrics tolerates null recent/aggregates and a null last_build")
func vaultMetricsDecodeEmpty() throws {
    let json = """
    {"last_build":null,"gauges":{"doc_count":0,"embedded_count":0,"embedding_coverage":0,"chunk_count":0,"stale_count":0,"index_db_bytes":0,"wal_bytes":0},"recent":null,"aggregates":null}
    """
    let m = try JSONDecoder().decode(VaultMetrics.self, from: Data(json.utf8))
    #expect(m.lastBuild == nil)
    #expect((m.recent ?? []).isEmpty)
    #expect((m.aggregates ?? [:]).isEmpty)
    #expect(m.gauges.docCount == 0)
    #expect(m.gauges.embeddingModel == nil)
    // Token totals are absent from a pre-token (or empty) payload; they must
    // decode to nil rather than throwing — the contract that keeps the tab from
    // blanking on an older CLI's output.
    #expect(m.totalInputTokens == nil)
    #expect(m.totalOutputTokens == nil)
}

// And the post-fix CLI output (`[]`/`{}`) must decode just as cleanly.
@Test("VaultMetrics decodes empty arrays/objects from the fixed CLI output")
func vaultMetricsDecodeEmptyBracketed() throws {
    let json = """
    {"last_build":null,"gauges":{"doc_count":1,"embedded_count":0,"embedding_coverage":0,"chunk_count":0,"stale_count":0,"index_db_bytes":4096,"wal_bytes":0},"recent":[],"aggregates":{}}
    """
    let m = try JSONDecoder().decode(VaultMetrics.self, from: Data(json.utf8))
    #expect(m.lastBuild == nil)
    #expect((m.recent ?? []).isEmpty)
    #expect((m.aggregates ?? [:]).isEmpty)
    #expect(m.gauges.docCount == 1)
}
