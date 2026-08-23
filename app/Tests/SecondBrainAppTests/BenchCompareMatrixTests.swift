import Foundation
import Testing
@testable import SecondBrain

// The compare matrix consumes `models bench compare --json` (the latest run
// per model x probe pair, the same []bench.Run shape as history). These tests
// pin the column order, the latest-wins reduction, the quality recovery from
// detail strings, and the cell rendering.

private func runs(_ json: String) -> [BenchRunInfo] {
    try! JSONDecoder().decode([BenchRunInfo].self, from: Data(json.utf8))
}

// A realistic compare feed: two models, mixed probes, one failure, and a
// retrieval run carrying the recorded quality detail.
private let compareFixture = #"""
[
 {"id":1,"timestamp":"2026-08-20T10:00:00Z","provider":"bedrock","model_id":"amazon.nova-2-multimodal-embeddings-v1:0","probe":"embed","latency_ms":412,"ok":true,"detail":"dims=1024","vault_doc_count":151},
 {"id":2,"timestamp":"2026-08-20T10:00:00Z","provider":"bedrock","model_id":"amazon.nova-2-multimodal-embeddings-v1:0","probe":"retrieval","latency_ms":88,"ok":true,"detail":"mrr@10=0.870 recall@10=0.933 pairs=45","vault_doc_count":151},
 {"id":3,"timestamp":"2026-08-20T10:00:00Z","provider":"bedrock","model_id":"us.anthropic.claude-haiku-4-5-20251001-v1:0","probe":"generate","latency_ms":950,"ok":true,"vault_doc_count":151},
 {"id":4,"timestamp":"2026-08-20T10:00:00Z","provider":"bedrock","model_id":"us.anthropic.claude-haiku-4-5-20251001-v1:0","probe":"search","latency_ms":12,"ok":true,"detail":"results=5","vault_doc_count":151},
 {"id":5,"timestamp":"2026-08-20T10:00:00Z","provider":"bedrock","model_id":"us.anthropic.claude-haiku-4-5-20251001-v1:0","probe":"rag","latency_ms":2100,"ok":false,"detail":"throttled","vault_doc_count":151}
]
"""#

@Test("Columns follow the CLI compare probe order, retrieval appended last")
func compareColumnsOrder() {
    let cols = BenchCompareMatrix.columns(runs(compareFixture))
    // Ported from bench.go runBenchCompare's probeOrder (embed, generate,
    // search, rag) with retrieval after: only probes present render.
    #expect(cols == ["embed", "generate", "search", "rag", "retrieval"])

    let partial = runs(#"[{"id":1,"timestamp":"t","provider":"p","model_id":"m","probe":"rag","latency_ms":1,"ok":true}]"#)
    #expect(BenchCompareMatrix.columns(partial) == ["rag"])
}

@Test("Unknown future probes still get a column, after the known order")
func compareColumnsUnknownProbe() {
    let feed = runs(#"[{"id":1,"timestamp":"t","provider":"p","model_id":"m","probe":"embed","latency_ms":1,"ok":true},{"id":2,"timestamp":"t","provider":"p","model_id":"m","probe":"zeta","latency_ms":1,"ok":true}]"#)
    #expect(BenchCompareMatrix.columns(feed) == ["embed", "zeta"])
}

@Test("Rows group per model with one cell per probe, sorted by model id")
func compareRowsGrouping() {
    let rows = BenchCompareMatrix.rows(runs(compareFixture))
    #expect(rows.count == 2)
    #expect(rows[0].modelID == "amazon.nova-2-multimodal-embeddings-v1:0")
    #expect(rows[0].cells["embed"]?.latencyMs == 412)
    #expect(rows[0].cells["retrieval"]?.quality == 0.870)
    #expect(rows[1].modelID == "us.anthropic.claude-haiku-4-5-20251001-v1:0")
    #expect(rows[1].cells["rag"]?.ok == false)
    #expect(rows[1].cells["embed"] == nil)
}

@Test("Duplicate (model, probe) pairs keep the latest run")
func compareRowsLatestWins() {
    let feed = runs(#"""
    [
     {"id":1,"timestamp":"2026-08-19T10:00:00Z","provider":"p","model_id":"m","probe":"generate","latency_ms":900,"ok":false,"detail":"old"},
     {"id":2,"timestamp":"2026-08-20T10:00:00Z","provider":"p","model_id":"m","probe":"generate","latency_ms":400,"ok":true,"detail":"new"}
    ]
    """#)
    let rows = BenchCompareMatrix.rows(feed)
    #expect(rows.count == 1)
    #expect(rows[0].cells["generate"]?.latencyMs == 400)
    #expect(rows[0].cells["generate"]?.ok == true)
}

@Test("Quality parses from the retrieval detail string, and only from it")
func compareQualityParsing() {
    #expect(BenchCompareMatrix.quality(fromDetail: "mrr@10=0.870 recall@10=0.933 pairs=45") == 0.870)
    #expect(BenchCompareMatrix.quality(fromDetail: "mrr@10=1 recall@10=1 pairs=12") == 1)
    #expect(BenchCompareMatrix.quality(fromDetail: "dims=1024") == nil)
    #expect(BenchCompareMatrix.quality(fromDetail: "results=5") == nil)
    #expect(BenchCompareMatrix.quality(fromDetail: nil) == nil)
    #expect(BenchCompareMatrix.quality(fromDetail: "mrr@10=") == nil)
}

@Test("Cell text renders latency, quality-prefixed latency, FAIL, and absent")
func compareCellText() {
    let pass = BenchCompareMatrix.Cell(latencyMs: 412, ok: true, quality: nil, detail: nil)
    #expect(BenchCompareMatrix.cellText(pass) == "412ms")
    let quality = BenchCompareMatrix.Cell(latencyMs: 88, ok: true, quality: 0.87, detail: nil)
    #expect(BenchCompareMatrix.cellText(quality) == "q=0.87 88ms")
    let fail = BenchCompareMatrix.Cell(latencyMs: 2100, ok: false, quality: nil, detail: "throttled")
    #expect(BenchCompareMatrix.cellText(fail) == "FAIL")
    #expect(BenchCompareMatrix.cellText(nil) == "-")
}

@Test("An empty compare feed produces no rows and no columns (CLI emits null)")
func compareEmpty() {
    #expect(BenchCompareMatrix.rows([]).isEmpty)
    #expect(BenchCompareMatrix.columns([]).isEmpty)
    // fetchBenchCompare falls back to [] when the CLI emits JSON null.
    #expect((try? JSONDecoder().decode([BenchRunInfo].self, from: Data("null".utf8))) == nil)
}
