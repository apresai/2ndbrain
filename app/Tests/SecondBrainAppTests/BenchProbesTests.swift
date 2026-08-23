import Foundation
import Testing
@testable import SecondBrain

// The Benchmarks pane's probe vocabulary mirrors the CLI
// (`models bench --probe embed|generate|retrieval|search|rag` and the
// no-probe full battery split in bench.go runBenchProbes). These tests pin
// the type gating, the zero-cost classification, and the favorites-battery
// estimate grouping.

@Test("Probe options split by model type the way the CLI's full battery does")
func probeOptionsByType() {
    #expect(BenchProbes.options(forModelType: "embedding") == ["embed", "retrieval"])
    #expect(BenchProbes.options(forModelType: "generation") == ["generate", "search", "rag"])
}

@Test("Cost probes map to the CLI cost-preview kinds; local probes are free")
func costProbeMapping() {
    #expect(BenchProbes.costProbe("embed") == "bench_embed")
    #expect(BenchProbes.costProbe("generate") == "bench_gen")
    #expect(BenchProbes.costProbe("rag") == "bench_rag")
    // retrieval scores stored embeddings locally; search is BM25 over the
    // index. Neither bills, so neither needs a spend confirm.
    #expect(BenchProbes.costProbe("retrieval") == nil)
    #expect(BenchProbes.costProbe("search") == nil)
}

@Test("Benchable models exclude rerank and incompatible entries, embeddings first")
func benchableModelsFilter() {
    func model(_ json: String) -> CatalogModelInfo {
        try! JSONDecoder().decode(CatalogModelInfo.self, from: Data(json.utf8))
    }
    let gen = model(#"{"id":"haiku","name":"Haiku","provider":"bedrock","type":"generation","compatible":true}"#)
    let embed = model(#"{"id":"nova","name":"Nova","provider":"bedrock","type":"embedding","compatible":true}"#)
    let rerank = model(#"{"id":"cohere.rerank-v3-5:0","name":"Rerank","provider":"bedrock","type":"rerank"}"#)
    let incompatible = model(#"{"id":"pegasus","name":"Pegasus","provider":"bedrock","type":"generation","compatible":false}"#)
    let out = BenchProbes.benchableModels([gen, rerank, embed, incompatible])
    #expect(out.map(\.modelID) == ["nova", "haiku"])
}

@Test("Benchable models preserve the incoming order within each type")
func benchableModelsPreserveBestOrder() {
    func model(_ json: String) -> CatalogModelInfo {
        try! JSONDecoder().decode(CatalogModelInfo.self, from: Data(json.utf8))
    }
    // The picker feeds `models list --sort best`, whose JSON order encodes
    // the CLI's measured ranking; an alphabetical re-sort here would erase
    // it. "zeta" ranks above "alpha" in the feed and must stay above it.
    let genBest = model(#"{"id":"zeta","name":"Zeta","provider":"bedrock","type":"generation","compatible":true}"#)
    let genWorse = model(#"{"id":"alpha","name":"Alpha","provider":"bedrock","type":"generation","compatible":true}"#)
    let embBest = model(#"{"id":"nova-z","name":"NovaZ","provider":"bedrock","type":"embedding","compatible":true}"#)
    let embWorse = model(#"{"id":"nova-a","name":"NovaA","provider":"bedrock","type":"embedding","compatible":true}"#)
    let out = BenchProbes.benchableModels([genBest, embBest, genWorse, embWorse])
    #expect(out.map(\.modelID) == ["nova-z", "nova-a", "zeta", "alpha"])
}

@Test("Favorites-battery estimate groups the paid probes per model type")
func batteryPreviewGroupsSplit() {
    let groups = BenchProbes.batteryPreviewGroups([
        (modelID: "nova", modelType: "embedding"),
        (modelID: "haiku", modelType: "generation"),
        (modelID: "grok", modelType: "generation"),
    ])
    // Sorted by probe kind for a stable confirm; search/retrieval never
    // appear (they bill nothing).
    #expect(groups.map(\.probe) == ["bench_embed", "bench_gen", "bench_rag"])
    #expect(groups[0].ids == ["nova"])
    #expect(groups[1].ids == ["haiku", "grok"])
    #expect(groups[2].ids == ["haiku", "grok"])
}

@Test("Empty targets produce no estimate groups (numberless confirm instead)")
func batteryPreviewGroupsEmpty() {
    #expect(BenchProbes.batteryPreviewGroups([]).isEmpty)
}

@Test("Event lines render results, model boundaries, and messages")
func benchEventLines() {
    func event(_ json: String) -> BenchmarkEvent {
        try! JSONDecoder().decode(BenchmarkEvent.self, from: Data(json.utf8))
    }
    let pass = event(#"{"event":"probe_result","model_id":"haiku","provider":"bedrock","type":"generation","probe":"generate","result":{"probe":"generate","latency_ms":412,"ok":true}}"#)
    #expect(BenchProbes.eventLine(pass) == "generate PASS 412ms")

    let fail = event(#"{"event":"probe_result","result":{"probe":"rag","latency_ms":2100,"ok":false,"detail":"throttled"}}"#)
    #expect(BenchProbes.eventLine(fail) == "rag FAIL 2100ms throttled")

    let skip = event(#"{"event":"probe_result","result":{"probe":"retrieval","latency_ms":0,"ok":true,"skipped":true,"detail":"too few links"}}"#)
    #expect(BenchProbes.eventLine(skip) == "retrieval SKIP 0ms too few links")

    let start = event(#"{"event":"model_start","model_id":"haiku","provider":"bedrock","type":"generation","message":"benchmark started"}"#)
    #expect(BenchProbes.eventLine(start) == "→ haiku")

    let done = event(#"{"event":"done","message":"benchmark complete"}"#)
    #expect(BenchProbes.eventLine(done) == "benchmark complete")
}
