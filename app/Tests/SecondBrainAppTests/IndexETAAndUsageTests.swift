import Foundation
import Testing
@testable import SecondBrain

@Test("Formatters.usd formats zero, sub-cent, standard, and unpriced values")
func formattersUSD() {
    #expect(Formatters.usd(0) == "$0.00")
    #expect(Formatters.usd(0.00005) == "<$0.0001")
    #expect(Formatters.usd(0.0042) == "$0.0042")
    #expect(Formatters.usd(2.25) == "$2.25")
    #expect(Formatters.usd(0, knownPricing: false) == "unpriced")
}

@Test("Formatters.eta formats nil, sub-second, seconds, minutes, and hours")
func formattersETA() {
    #expect(Formatters.eta(nil) == "—")
    #expect(Formatters.eta(-3) == "—")
    #expect(Formatters.eta(0) == "0s")
    #expect(Formatters.eta(0.4) == "<1s")
    #expect(Formatters.eta(42) == "42s")
    #expect(Formatters.eta(65) == "1m05s")
    #expect(Formatters.eta(3725) == "1h02m")
}

@Test("Formatters.tokens scales K and M thresholds cleanly")
func formattersTokens() {
    #expect(Formatters.tokens(0) == "0")
    #expect(Formatters.tokens(640) == "640")
    #expect(Formatters.tokens(1_250) == "1.2K")
    #expect(Formatters.tokens(1_260) == "1.3K")
    #expect(Formatters.tokens(125_000) == "125K")
    #expect(Formatters.tokens(1_500_000) == "1.5M")
}

@Test("IndexETAEstimator parses rich and legacy stderr start and progress lines")
func parseStderrLines() {
    let richStart = IndexETAEstimator.parseStartLine(
        "  embedding 120 documents (340 chunks, ~18.4K tokens, concurrency 4, est. ~1m15s)..."
    )
    #expect(richStart?.totalDocs == 120)
    #expect(richStart?.totalChunks == 340)
    #expect(richStart?.estimatedTokens == 18_400)
    #expect(richStart?.concurrency == 4)
    #expect(richStart?.estimatedSeconds == 75)

    let legacyStart = IndexETAEstimator.parseStartLine("  embedding 42 documents (concurrency 2)...")
    #expect(legacyStart?.totalDocs == 42)
    #expect(legacyStart?.concurrency == 2)
    #expect(legacyStart?.totalChunks == nil)
    #expect(legacyStart?.estimatedSeconds == nil)

    let richProg = IndexETAEstimator.parseProgressLine(
        "  embedded 12/120 (34/340 chunks, 2.4 docs/s, ~45s left): projects/notes.md"
    )
    #expect(richProg?.current == 12)
    #expect(richProg?.total == 120)
    #expect(richProg?.chunksDone == 34)
    #expect(richProg?.chunksTotal == 340)
    #expect(richProg?.docsPerSec == 2.4)
    #expect(richProg?.remainingSeconds == 45)
    #expect(richProg?.notePath == "projects/notes.md")

    let legacyProg = IndexETAEstimator.parseProgressLine("  embedded 5/10: alpha.md")
    #expect(legacyProg?.current == 5)
    #expect(legacyProg?.total == 10)
    #expect(legacyProg?.chunksDone == nil)
    #expect(legacyProg?.remainingSeconds == nil)
    #expect(legacyProg?.notePath == "alpha.md")
}

@Test("IndexETAEstimator parses duration and token shorthands")
func parseDurationAndTokens() {
    #expect(IndexETAEstimator.parseDurationString("<1s") == 0.5)
    #expect(IndexETAEstimator.parseDurationString("45s") == 45)
    #expect(IndexETAEstimator.parseDurationString("1m05s") == 65)
    #expect(IndexETAEstimator.parseDurationString("2m 5s") == 125)
    #expect(IndexETAEstimator.parseDurationString("1h10m") == 4200)
    #expect(IndexETAEstimator.parseTokenString("18.4K") == 18_400)
    #expect(IndexETAEstimator.parseTokenString("2.5M") == 2_500_000)
    #expect(IndexETAEstimator.parseTokenString("900") == 900)
}

@Test("IndexETAEstimator liveRemainingSeconds blends initial estimate early and uses linear rate after 4 docs")
func liveRemainingBlending() {
    #expect(IndexETAEstimator.liveRemainingSeconds(current: 0, total: 10, elapsedSeconds: 0, initialEstimateSeconds: 40) == 40)
    #expect(IndexETAEstimator.liveRemainingSeconds(current: 10, total: 10, elapsedSeconds: 20, initialEstimateSeconds: 40) == 0)

    // At current=2 (< 4), weight=0.5: linear=2*(10-2)/2=8, prior=max(0, 40-2)=38 -> 0.5*8 + 0.5*38 = 23
    let blended = IndexETAEstimator.liveRemainingSeconds(current: 2, total: 10, elapsedSeconds: 2, initialEstimateSeconds: 40)
    #expect(blended == 23)

    // At current=4 (>= 4), pure linear: elapsed=8 * (10-4)/4 = 12
    let linear = IndexETAEstimator.liveRemainingSeconds(current: 4, total: 10, elapsedSeconds: 8, initialEstimateSeconds: 40)
    #expect(linear == 12)
}

@Test("MetricsReport decodes empty vault and hybrid AI usage JSON contracts")
func metricsReportDecoding() throws {
    let emptyJSON = """
    {
      "last_build": null,
      "gauges": {},
      "recent": [],
      "aggregates": {},
      "total_input_tokens": 0,
      "total_output_tokens": 0,
      "ai_usage": {
        "today": {"cloud_cost_usd": 0, "known_pricing": true, "cloud_calls": 0, "local_calls": 0, "cloud_input_tokens": 0, "cloud_output_tokens": 0, "local_tokens": 0, "unpriced_calls": 0},
        "last_7d": {"cloud_cost_usd": 0, "known_pricing": true, "cloud_calls": 0, "local_calls": 0, "cloud_input_tokens": 0, "cloud_output_tokens": 0, "local_tokens": 0, "unpriced_calls": 0},
        "last_30d": {"cloud_cost_usd": 0, "known_pricing": true, "cloud_calls": 0, "local_calls": 0, "cloud_input_tokens": 0, "cloud_output_tokens": 0, "local_tokens": 0, "unpriced_calls": 0},
        "all_time": {"cloud_cost_usd": 0, "known_pricing": true, "cloud_calls": 0, "local_calls": 0, "cloud_input_tokens": 0, "cloud_output_tokens": 0, "local_tokens": 0, "unpriced_calls": 0},
        "by_model": []
      },
      "reembed_estimate": {
        "embeddable_docs": 0,
        "total_chunks": 0,
        "total_chars": 0,
        "estimated_tokens": 0,
        "estimated_seconds": 0,
        "speed_source": "none",
        "docs_per_sec": 0,
        "provider": "llama-local",
        "model": "embeddinggemma-2",
        "is_cloud": false,
        "estimated_cost_usd": 0,
        "known_pricing": true
      }
    }
    """
    let decodedEmpty = try JSONDecoder().decode(MetricsReport.self, from: Data(emptyJSON.utf8))
    #expect(decodedEmpty.lastBuild == nil)
    #expect(decodedEmpty.recent?.isEmpty == true)
    #expect(decodedEmpty.aiUsage?.byModel.isEmpty == true)
    #expect(decodedEmpty.reembedEstimate?.speedSource == "none")

    let hybridJSON = """
    {
      "last_build": {
        "id": 7,
        "ts": "2026-11-17T18:00:00Z",
        "operation": "reembed",
        "source": "cli",
        "duration_ms": 1400,
        "ok": true,
        "docs_indexed": 12,
        "chunks_created": 24,
        "embedded": 12,
        "embedding_provider": "llama-local",
        "embedding_model": "embeddinggemma-2",
        "input_tokens": 3000,
        "output_tokens": 0,
        "cost_usd": 0,
        "known_pricing": true
      },
      "recent": [],
      "total_input_tokens": 1003200,
      "total_output_tokens": 100000,
      "ai_usage": {
        "today": {"cloud_cost_usd": 2.25, "known_pricing": true, "cloud_calls": 1, "local_calls": 2, "cloud_input_tokens": 1000000, "cloud_output_tokens": 100000, "local_tokens": 3200, "unpriced_calls": 0},
        "last_7d": {"cloud_cost_usd": 2.25, "known_pricing": true, "cloud_calls": 1, "local_calls": 2, "cloud_input_tokens": 1000000, "cloud_output_tokens": 100000, "local_tokens": 3200, "unpriced_calls": 0},
        "last_30d": {"cloud_cost_usd": 2.25, "known_pricing": true, "cloud_calls": 1, "local_calls": 2, "cloud_input_tokens": 1000000, "cloud_output_tokens": 100000, "local_tokens": 3200, "unpriced_calls": 0},
        "all_time": {"cloud_cost_usd": 2.25, "known_pricing": true, "cloud_calls": 1, "local_calls": 2, "cloud_input_tokens": 1000000, "cloud_output_tokens": 100000, "local_tokens": 3200, "unpriced_calls": 0},
        "by_model": [
          {"provider": "gemini", "model": "gemini-4-hydrogen", "slot": "generation", "is_cloud": true, "calls": 1, "input_tokens": 1000000, "output_tokens": 100000, "estimated_cost_usd": 2.25, "known_pricing": true, "price_label": "$1.50/$7.50 per 1M"},
          {"provider": "llama-local", "model": "embeddinggemma-2", "slot": "embedding", "is_cloud": false, "calls": 2, "input_tokens": 3200, "output_tokens": 0, "estimated_cost_usd": 0, "known_pricing": true, "price_label": "On-device ($0.00)"}
        ]
      },
      "reembed_estimate": {
        "embeddable_docs": 12,
        "total_chunks": 24,
        "total_chars": 12000,
        "estimated_tokens": 3000,
        "estimated_seconds": 3.5,
        "speed_source": "history",
        "docs_per_sec": 8.5,
        "provider": "llama-local",
        "model": "embeddinggemma-2",
        "is_cloud": false,
        "estimated_cost_usd": 0,
        "known_pricing": true
      }
    }
    """
    let decodedHybrid = try JSONDecoder().decode(MetricsReport.self, from: Data(hybridJSON.utf8))
    #expect(decodedHybrid.lastBuild?.id == "7")
    #expect(decodedHybrid.aiUsage?.last30d.cloudCostUSD == 2.25)
    #expect(decodedHybrid.aiUsage?.last30d.localCalls == 2)
    #expect(decodedHybrid.aiUsage?.byModel.count == 2)
    #expect(decodedHybrid.reembedEstimate?.speedSource == "history")
    #expect(decodedHybrid.reembedEstimate?.isCloud == false)
}

@Test("GUIStatusSnapshot round-trips ETA and 30d AI usage telemetry fields")
func guiStatusSnapshotETAAndUsageRoundTrip() throws {
    var snap = GUIStatusSnapshot.initial(appVersion: "0.24.0", timestamp: "2026-11-17T19:00:00Z")
    snap.indexEstimatedRemainingSeconds = 18.5
    snap.indexDocsPerSec = 4.2
    snap.aiUsage30dCostUSD = 1.75
    snap.aiUsage30dCloudCalls = 6
    snap.aiUsage30dLocalCalls = 14

    let encoded = try JSONEncoder().encode(snap)
    let decoded = try JSONDecoder().decode(GUIStatusSnapshot.self, from: encoded)
    #expect(decoded.indexEstimatedRemainingSeconds == 18.5)
    #expect(decoded.indexDocsPerSec == 4.2)
    #expect(decoded.aiUsage30dCostUSD == 1.75)
    #expect(decoded.aiUsage30dCloudCalls == 6)
    #expect(decoded.aiUsage30dLocalCalls == 14)
}
