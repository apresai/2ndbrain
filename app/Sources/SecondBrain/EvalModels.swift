import Foundation

// Decoders for the `2nb eval` family (the Testing tab's Quality pane), plus
// the pure gating logic that derives the confirm preview and the --cost-cap
// from the CLI's own estimate. The shapes mirror cli/internal/cli/eval.go
// (EvalReport, EvalEstimateReport), eval_answers.go (AnswersReport), and
// eval_tune.go (TuneReport / TuneEntry).

/// `2nb eval [answers|tune] --estimate --json` (EvalEstimateReport in the CLI).
struct EvalEstimateInfo: Codable, Equatable {
    let command: String
    let n: Int
    let qaCached: Bool
    let generationUSD: Double
    let embedUSD: Double
    let answersUSD: Double
    let totalUSD: Double
    let costCap: Double

    enum CodingKeys: String, CodingKey {
        case command, n
        case qaCached = "qa_cached"
        case generationUSD = "generation_usd"
        case embedUSD = "embed_usd"
        case answersUSD = "answers_usd"
        case totalUSD = "total_usd"
        case costCap = "cost_cap"
    }
}

/// The retrieval configuration a scorecard was measured under (EvalConfig).
struct EvalConfigInfo: Codable, Equatable {
    let threshold: Double
    let bm25Weight: Double
    let vectorWeight: Double

    enum CodingKeys: String, CodingKey {
        case threshold
        case bm25Weight = "bm25_weight"
        case vectorWeight = "vector_weight"
    }
}

/// `2nb eval --json` (EvalReport in the CLI).
struct EvalReportInfo: Codable, Equatable {
    let n: Int
    let k: Int
    let config: EvalConfigInfo
    let recallAtK: Double
    let recallAt1: Double
    let mrrAtK: Double
    let qaCached: Bool
    let generatedAt: String

    enum CodingKeys: String, CodingKey {
        case n, k, config
        case recallAtK = "recall_at_k"
        case recallAt1 = "recall_at_1"
        case mrrAtK = "mrr_at_k"
        case qaCached = "qa_cached"
        case generatedAt = "generated_at"
    }
}

/// `2nb eval answers --json` (AnswersReport in the CLI).
struct EvalAnswersInfo: Codable, Equatable {
    let n: Int
    let answered: Int
    let failed: Int
    let correctness: Double
    let completeness: Double
    let grounding: Double
    let composite: Double
    let nJudges: Int
    let selfJudged: Bool
    let judges: [String]
    let qaCached: Bool
    let generatedAt: String

    enum CodingKeys: String, CodingKey {
        case n, answered, failed, correctness, completeness, grounding, composite, judges
        case nJudges = "n_judges"
        case selfJudged = "self_judged"
        case qaCached = "qa_cached"
        case generatedAt = "generated_at"
    }
}

/// One swept configuration's scorecard (TuneEntry in the CLI).
struct EvalTuneEntryInfo: Codable, Equatable {
    let name: String
    let threshold: Double
    let bm25Weight: Double
    let vectorWeight: Double
    let bm25Only: Bool?
    let recallAtK: Double
    let recallAt1: Double
    let mrrAtK: Double

    enum CodingKeys: String, CodingKey {
        case name, threshold
        case bm25Weight = "bm25_weight"
        case vectorWeight = "vector_weight"
        case bm25Only = "bm25_only"
        case recallAtK = "recall_at_k"
        case recallAt1 = "recall_at_1"
        case mrrAtK = "mrr_at_k"
    }
}

/// `2nb eval tune --json` (TuneReport in the CLI).
struct EvalTuneInfo: Codable, Equatable {
    let n: Int
    let k: Int
    let current: EvalTuneEntryInfo
    let configs: [EvalTuneEntryInfo]
    let best: EvalTuneEntryInfo
    let suggestion: [String]?
    let qaCached: Bool
    let generatedAt: String

    enum CodingKeys: String, CodingKey {
        case n, k, current, configs, best, suggestion
        case qaCached = "qa_cached"
        case generatedAt = "generated_at"
    }
}

/// Pure gating + copy for the Quality pane, mirroring VerifyFlow's shape so
/// the cost-gate conventions stay in one recognizable pattern.
enum EvalFlow {
    /// The CLI's own `--cost-cap` default (eval.go). A failed estimate falls
    /// back to it: the flag is passed explicitly, so the fallback must match
    /// the CLI default rather than silently tightening it.
    static let cliDefaultCostCap = 0.25

    /// The `--cost-cap` for an eval run: double the CLI's estimate plus a
    /// cent of headroom, so the spend guard still trips on a runaway but
    /// never on the amount the user just approved (VerifyFlow.costCap's
    /// convention).
    static func costCap(estimate: EvalEstimateInfo?) -> Double {
        guard let estimate else { return cliDefaultCostCap }
        return estimate.totalUSD * 2 + 0.01
    }

    /// Adapts the CLI estimate to the shared PaidOperationConfirm dialog. A
    /// nil estimate degrades to the numberless confirm (never block the
    /// action on the estimator).
    static func confirmPreview(estimate: EvalEstimateInfo?) -> CostPreviewResponse? {
        guard let estimate else { return nil }
        return CostPreviewResponse(estimates: [], totalUSD: estimate.totalUSD)
    }

    /// The one-line QA-set context above the scorecard button: a vault with
    /// no cached QA set learns about the one-time generation cost BEFORE any
    /// spend; a cached vault learns re-runs are near-free.
    static func qaContext(estimate: EvalEstimateInfo?) -> String {
        guard let estimate else {
            return "Couldn't load the cost preview; the run confirm will show without a number."
        }
        if estimate.qaCached {
            return "Cached QA set of \(estimate.n) questions. Re-runs only re-embed the queries (cents)."
        }
        return String(
            format: "No cached QA set yet. The first run generates %d questions from your notes (one-time, ~$%.4f); later runs reuse the cache.",
            estimate.n, estimate.generationUSD
        )
    }
}
