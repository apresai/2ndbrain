import Foundation

// MARK: - CLI Metrics & Re-embed Estimate Contracts (`2nb metrics --json`)

struct MetricsReport: Codable, Equatable, Sendable {
    let lastBuild: MetricsOperation?
    let recent: [MetricsOperation]?
    let totalInputTokens: Int?
    let totalOutputTokens: Int?
    let aiUsage: AIUsageSummary?
    let reembedEstimate: ReembedEstimate?

    enum CodingKeys: String, CodingKey {
        case lastBuild = "last_build"
        case recent
        case totalInputTokens = "total_input_tokens"
        case totalOutputTokens = "total_output_tokens"
        case aiUsage = "ai_usage"
        case reembedEstimate = "reembed_estimate"
    }
}

struct MetricsOperation: Codable, Equatable, Identifiable, Sendable {
    let rowID: Int64?
    let timestamp: String
    let operation: String
    let source: String
    let durationMs: Int64
    let ok: Bool
    let error: String?

    let docsIndexed: Int?
    let chunksCreated: Int?
    let embedded: Int?
    let embedMs: Int64?
    let embeddingModel: String?
    let inputTokens: Int?
    let outputTokens: Int?

    let embeddingProvider: String?
    let generationProvider: String?
    let generationModel: String?
    let embedInputTokens: Int?
    let genInputTokens: Int?
    let genOutputTokens: Int?
    let costUSD: Double?
    let knownPricing: Bool?
    let docsPerSec: Double?

    var id: String {
        rowID.map(String.init) ?? "\(timestamp)|\(operation)|\(durationMs)"
    }

    enum CodingKeys: String, CodingKey {
        case rowID = "id"
        case timestamp = "ts"
        case operation
        case source
        case durationMs = "duration_ms"
        case ok
        case error
        case docsIndexed = "docs_indexed"
        case chunksCreated = "chunks_created"
        case embedded
        case embedMs = "embed_ms"
        case embeddingModel = "embedding_model"
        case inputTokens = "input_tokens"
        case outputTokens = "output_tokens"
        case embeddingProvider = "embedding_provider"
        case generationProvider = "generation_provider"
        case generationModel = "generation_model"
        case embedInputTokens = "embed_input_tokens"
        case genInputTokens = "gen_input_tokens"
        case genOutputTokens = "gen_output_tokens"
        case costUSD = "cost_usd"
        case knownPricing = "known_pricing"
        case docsPerSec = "docs_per_sec"
    }
}

struct AIUsageSummary: Codable, Equatable, Sendable {
    let today: AIUsageWindowSummary
    let last7d: AIUsageWindowSummary
    let last30d: AIUsageWindowSummary
    let allTime: AIUsageWindowSummary
    let byModel: [AIModelUsageRow]

    enum CodingKeys: String, CodingKey {
        case today
        case last7d = "last_7d"
        case last30d = "last_30d"
        case allTime = "all_time"
        case byModel = "by_model"
    }
}

struct AIUsageWindowSummary: Codable, Equatable, Sendable {
    let cloudCostUSD: Double
    let knownPricing: Bool
    let cloudCalls: Int
    let localCalls: Int
    let cloudInputTokens: Int
    let cloudOutputTokens: Int
    let localTokens: Int
    let unpricedCalls: Int

    var totalCalls: Int { cloudCalls + localCalls }
    var totalTokens: Int { cloudInputTokens + cloudOutputTokens + localTokens }

    enum CodingKeys: String, CodingKey {
        case cloudCostUSD = "cloud_cost_usd"
        case knownPricing = "known_pricing"
        case cloudCalls = "cloud_calls"
        case localCalls = "local_calls"
        case cloudInputTokens = "cloud_input_tokens"
        case cloudOutputTokens = "cloud_output_tokens"
        case localTokens = "local_tokens"
        case unpricedCalls = "unpriced_calls"
    }
}

struct AIModelUsageRow: Codable, Equatable, Identifiable, Sendable {
    let provider: String
    let model: String
    let slot: String
    let isCloud: Bool
    let calls: Int
    let inputTokens: Int
    let outputTokens: Int
    let estimatedCostUSD: Double
    let knownPricing: Bool
    let priceLabel: String

    var id: String { "\(provider)|\(model)|\(slot)" }

    enum CodingKeys: String, CodingKey {
        case provider
        case model
        case slot
        case isCloud = "is_cloud"
        case calls
        case inputTokens = "input_tokens"
        case outputTokens = "output_tokens"
        case estimatedCostUSD = "estimated_cost_usd"
        case knownPricing = "known_pricing"
        case priceLabel = "price_label"
    }
}

struct ReembedEstimate: Codable, Equatable, Sendable {
    let embeddableDocs: Int
    let totalChunks: Int
    let totalChars: Int
    let estimatedTokens: Int
    let estimatedSeconds: Double
    let speedSource: String
    let docsPerSec: Double
    let provider: String
    let model: String
    let isCloud: Bool
    let estimatedCostUSD: Double
    let knownPricing: Bool

    enum CodingKeys: String, CodingKey {
        case embeddableDocs = "embeddable_docs"
        case totalChunks = "total_chunks"
        case totalChars = "total_chars"
        case estimatedTokens = "estimated_tokens"
        case estimatedSeconds = "estimated_seconds"
        case speedSource = "speed_source"
        case docsPerSec = "docs_per_sec"
        case provider
        case model
        case isCloud = "is_cloud"
        case estimatedCostUSD = "estimated_cost_usd"
        case knownPricing = "known_pricing"
    }
}

// MARK: - Live Index / Re-embed Stderr Progress & ETA Helpers

enum IndexETAEstimator {
    struct StartLineInfo: Equatable, Sendable {
        let totalDocs: Int
        let totalChunks: Int?
        let estimatedTokens: Int?
        let concurrency: Int?
        let estimatedSeconds: Double?
    }

    struct ProgressLineInfo: Equatable, Sendable {
        let current: Int
        let total: Int
        let chunksDone: Int?
        let chunksTotal: Int?
        let docsPerSec: Double?
        let remainingSeconds: Double?
        let notePath: String?
    }

    private static let richStartRegex = try! NSRegularExpression(
        pattern: #"embedding\s+(\d+)\s+documents\s*\(\s*(\d+)\s+chunks\s*,\s*~?([0-9.]+[KMGkmg]?)\s+tokens\s*,\s*concurrency\s+(\d+)\s*,\s*est\.\s*~?([^)]+)\)"#,
        options: [.caseInsensitive]
    )

    private static let simpleStartRegex = try! NSRegularExpression(
        pattern: #"embedding\s+(\d+)\s+documents(?:\s*\(\s*concurrency\s+(\d+)\s*\))?"#,
        options: [.caseInsensitive]
    )

    private static let richProgressRegex = try! NSRegularExpression(
        pattern: #"[Ee]mbed\w*\s+(\d+)/(\d+)\s*\(\s*(\d+)/(\d+)\s+chunks\s*,\s*([0-9.]+)\s+docs/s\s*,\s*~?([^)]+?)\s+left\s*\)\s*:\s*(.*)$"#
    )

    private static let simpleProgressRegex = try! NSRegularExpression(
        pattern: #"[Ee]mbed\w*\s+(\d+)/(\d+)(?:\s*:\s*(.*))?$"#
    )

    static func parseStartLine(_ line: String) -> StartLineInfo? {
        let trimmed = line.trimmingCharacters(in: .whitespacesAndNewlines)
        let ns = trimmed as NSString
        let range = NSRange(location: 0, length: ns.length)

        if let m = richStartRegex.firstMatch(in: trimmed, range: range),
           let docs = Int(ns.substring(with: m.range(at: 1))) {
            let chunks = Int(ns.substring(with: m.range(at: 2)))
            let tokens = parseTokenString(ns.substring(with: m.range(at: 3)))
            let concurrency = Int(ns.substring(with: m.range(at: 4)))
            let etaSec = parseDurationString(ns.substring(with: m.range(at: 5)))
            return StartLineInfo(
                totalDocs: docs,
                totalChunks: chunks,
                estimatedTokens: tokens,
                concurrency: concurrency,
                estimatedSeconds: etaSec
            )
        }

        if let m = simpleStartRegex.firstMatch(in: trimmed, range: range),
           let docs = Int(ns.substring(with: m.range(at: 1))) {
            var concurrency: Int?
            if m.range(at: 2).location != NSNotFound {
                concurrency = Int(ns.substring(with: m.range(at: 2)))
            }
            return StartLineInfo(
                totalDocs: docs,
                totalChunks: nil,
                estimatedTokens: nil,
                concurrency: concurrency,
                estimatedSeconds: nil
            )
        }

        return nil
    }

    static func parseProgressLine(_ line: String) -> ProgressLineInfo? {
        let trimmed = line.trimmingCharacters(in: .whitespacesAndNewlines)
        let ns = trimmed as NSString
        let range = NSRange(location: 0, length: ns.length)

        if let m = richProgressRegex.firstMatch(in: trimmed, range: range),
           let current = Int(ns.substring(with: m.range(at: 1))),
           let total = Int(ns.substring(with: m.range(at: 2))) {
            let chunksDone = Int(ns.substring(with: m.range(at: 3)))
            let chunksTotal = Int(ns.substring(with: m.range(at: 4)))
            let docsPerSec = Double(ns.substring(with: m.range(at: 5)))
            let remaining = parseDurationString(ns.substring(with: m.range(at: 6)))
            let path = ns.substring(with: m.range(at: 7)).trimmingCharacters(in: .whitespacesAndNewlines)
            return ProgressLineInfo(
                current: current,
                total: total,
                chunksDone: chunksDone,
                chunksTotal: chunksTotal,
                docsPerSec: docsPerSec,
                remainingSeconds: remaining,
                notePath: path.isEmpty ? nil : path
            )
        }

        if let m = simpleProgressRegex.firstMatch(in: trimmed, range: range),
           let current = Int(ns.substring(with: m.range(at: 1))),
           let total = Int(ns.substring(with: m.range(at: 2))) {
            var path: String?
            if m.range(at: 3).location != NSNotFound {
                let rawPath = ns.substring(with: m.range(at: 3)).trimmingCharacters(in: .whitespacesAndNewlines)
                if !rawPath.isEmpty { path = rawPath }
            }
            return ProgressLineInfo(
                current: current,
                total: total,
                chunksDone: nil,
                chunksTotal: nil,
                docsPerSec: nil,
                remainingSeconds: nil,
                notePath: path
            )
        }

        return nil
    }

    static func parseTokenString(_ raw: String) -> Int? {
        var cleaned = raw.trimmingCharacters(in: .whitespacesAndNewlines)
        if cleaned.hasPrefix("~") { cleaned.removeFirst() }
        guard !cleaned.isEmpty else { return nil }
        let upper = cleaned.uppercased()
        if upper.hasSuffix("K"), let val = Double(upper.dropLast()) {
            return Int((val * 1_000.0).rounded())
        }
        if upper.hasSuffix("M"), let val = Double(upper.dropLast()) {
            return Int((val * 1_000_000.0).rounded())
        }
        if upper.hasSuffix("G"), let val = Double(upper.dropLast()) {
            return Int((val * 1_000_000_000.0).rounded())
        }
        guard let val = Double(upper), val >= 0 else { return nil }
        return Int(val.rounded())
    }

    static func parseDurationString(_ raw: String) -> Double? {
        var s = raw.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        if s.hasPrefix("~") { s.removeFirst() }
        s = s.replacingOccurrences(of: " ", with: "")
        guard !s.isEmpty else { return nil }
        if s == "<1s" { return 0.5 }
        if let direct = Double(s) { return direct }

        var total: Double = 0
        var matchedAny = false
        var numberBuffer = ""
        for ch in s {
            if ch.isNumber || ch == "." {
                numberBuffer.append(ch)
            } else if !numberBuffer.isEmpty, let num = Double(numberBuffer) {
                matchedAny = true
                numberBuffer = ""
                switch ch {
                case "h": total += num * 3600
                case "m": total += num * 60
                case "s": total += num
                default: return nil
                }
            } else {
                return nil
            }
        }
        return matchedAny && numberBuffer.isEmpty ? total : nil
    }

    /// Estimates remaining seconds when the CLI didn't emit an explicit `~Xs left` value.
    static func liveRemainingSeconds(
        current: Int,
        total: Int,
        elapsedSeconds: Double,
        initialEstimateSeconds: Double?
    ) -> Double? {
        guard total > 0 else { return nil }
        if current >= total { return 0 }
        if current <= 0 { return initialEstimateSeconds }
        let linearRemaining = max(0, elapsedSeconds * Double(total - current) / Double(current))
        if let initial = initialEstimateSeconds, initial > 0, current < 4 {
            let weight = Double(current) / 4.0
            let priorRemaining = max(0, initial - elapsedSeconds)
            return max(0, weight * linearRemaining + (1.0 - weight) * priorRemaining)
        }
        return linearRemaining
    }
}
