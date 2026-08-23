import Foundation

/// Pure matrix logic for the Benchmarks pane's compare view: turns the
/// `models bench compare --json` feed (the latest run per model x probe pair,
/// []bench.Run like history) into models-as-rows x probes-as-columns cells.
/// Split out of the view so the ordering, latest-wins reduction, and the
/// quality recovery from detail strings are unit-testable.
enum BenchCompareMatrix {
    /// Probe column order, ported from the CLI compare's own probeOrder
    /// (bench.go runBenchCompare: embed, generate, search, rag), with
    /// retrieval appended: the CLI's human view omits it, but the JSON feed
    /// carries it and it is the one probe with a quality score.
    static let probeOrder = ["embed", "generate", "search", "rag", "retrieval"]

    struct Cell: Equatable {
        let latencyMs: Int64
        let ok: Bool
        /// Recovered from the retrieval detail string; nil for probes that
        /// measure latency only.
        let quality: Double?
        let detail: String?
    }

    struct Row: Identifiable, Equatable {
        var id: String { provider + "|" + modelID }
        let provider: String
        let modelID: String
        let cells: [String: Cell]
    }

    /// The ordered probe columns actually present in the feed: the known
    /// probes in CLI order, then any future probe the CLI grows, sorted, so
    /// nothing the feed carries is silently dropped.
    static func columns(_ runs: [BenchRunInfo]) -> [String] {
        let present = Set(runs.map(\.probe))
        var cols = probeOrder.filter { present.contains($0) }
        cols.append(contentsOf: present.subtracting(probeOrder).sorted())
        return cols
    }

    /// One row per model, keeping the LATEST run per (model, probe) when the
    /// feed carries duplicates (the CLI already reduces to latest-per-pair;
    /// this keeps the matrix honest against a merged or stale feed). Rows
    /// sort by model id then provider for a stable table.
    static func rows(_ runs: [BenchRunInfo]) -> [Row] {
        var byModel: [String: (provider: String, modelID: String, cells: [String: BenchRunInfo])] = [:]
        for run in runs {
            let key = run.provider + "|" + run.modelID
            var entry = byModel[key] ?? (run.provider, run.modelID, [:])
            if let existing = entry.cells[run.probe] {
                // RFC3339 timestamps compare lexicographically.
                if run.timestamp > existing.timestamp { entry.cells[run.probe] = run }
            } else {
                entry.cells[run.probe] = run
            }
            byModel[key] = entry
        }
        return byModel.values
            .map { entry in
                Row(
                    provider: entry.provider,
                    modelID: entry.modelID,
                    cells: entry.cells.mapValues { run in
                        Cell(
                            latencyMs: run.latencyMs,
                            ok: run.ok,
                            quality: quality(fromDetail: run.detail),
                            detail: run.detail
                        )
                    }
                )
            }
            .sorted {
                if $0.modelID != $1.modelID { return $0.modelID < $1.modelID }
                return $0.provider < $1.provider
            }
    }

    /// Recovers the retrieval probe's quality score from its recorded detail
    /// string ("mrr@10=0.870 recall@10=0.933 pairs=45"): bench.db stores no
    /// quality column, so the matrix parses the one the probe wrote.
    static func quality(fromDetail detail: String?) -> Double? {
        guard let detail, let mrr = detail.range(of: "mrr@") else { return nil }
        let after = detail[mrr.upperBound...]
        guard let eq = after.firstIndex(of: "=") else { return nil }
        let valueStart = after.index(after: eq)
        let valueEnd = after[valueStart...].firstIndex(of: " ") ?? after.endIndex
        return Double(after[valueStart..<valueEnd])
    }

    /// One cell's display text: latency for a pass (prefixed by the quality
    /// score when the probe measured one), FAIL for a failure, "-" when the
    /// model never ran the probe.
    static func cellText(_ cell: Cell?) -> String {
        guard let cell else { return "-" }
        if !cell.ok { return "FAIL" }
        if let quality = cell.quality {
            return String(format: "q=%.2f %dms", quality, cell.latencyMs)
        }
        return "\(cell.latencyMs)ms"
    }
}
