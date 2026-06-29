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
        {"id":3,"ts":"2026-06-29T21:41:31Z","operation":"search","source":"mcp","duration_ms":1129,"ok":true,"mode":"hybrid","cli_version":"dev"},
        {"id":1,"ts":"2026-06-29T21:41:28Z","operation":"index","source":"cli","duration_ms":14297,"ok":true,"docs_indexed":2,"docs_per_sec":0.14}
      ],
      "aggregates": {"index":{"count":1,"avg_ms":14297,"p50_ms":14297,"avg_docs_per_sec":0.14},"search":{"count":2,"avg_ms":1190.5,"p50_ms":1252}}
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
    #expect(recent.count == 2)
    let search = try #require(recent.first { $0.operation == "search" })
    #expect(search.source == "mcp")
    #expect(search.mode == "hybrid")
    #expect(search.resultCount == nil) // omitempty absent → nil (MCP query rows have no count)
    #expect(search.docsIndexed == nil)

    let aggs = try #require(m.aggregates)
    #expect(aggs["index"]?.count == 1)
    #expect(aggs["index"]?.avgDocsPerSec == 0.14)
    #expect(aggs["search"]?.avgMs == 1190.5)
    #expect(aggs["search"]?.avgDocsPerSec == nil) // omitempty absent → nil

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
