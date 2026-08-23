import Foundation
import Testing
@testable import SecondBrain

// Decoder fixtures for the `2nb eval` family (Quality pane) plus the pure
// EvalFlow gating. The estimate fixture is captured from a real
// `2nb eval --estimate --json` run; the report shapes mirror
// cli/internal/cli/eval.go, eval_answers.go, and eval_tune.go.

@Test("EvalEstimateInfo decodes a real `eval --estimate --json` payload")
func evalEstimateDecode() {
    // Captured from the built CLI against a fresh vault (uncached QA set).
    let json = #"""
    {
      "command": "eval",
      "n": 20,
      "qa_cached": false,
      "generation_usd": 0.13240000000000002,
      "embed_usd": 0.000054,
      "answers_usd": 0,
      "total_usd": 0.13245400000000002,
      "cost_cap": 0.25
    }
    """#
    let info = try! JSONDecoder().decode(EvalEstimateInfo.self, from: Data(json.utf8))
    #expect(info.command == "eval")
    #expect(info.n == 20)
    #expect(info.qaCached == false)
    #expect(info.generationUSD > 0.13 && info.generationUSD < 0.14)
    #expect(info.answersUSD == 0)
    #expect(info.costCap == 0.25)
}

@Test("EvalReportInfo decodes the EvalReport shape")
func evalReportDecode() {
    let json = #"""
    {
      "n": 20, "k": 10,
      "config": {"threshold": 0.25, "bm25_weight": 1, "vector_weight": 1},
      "recall_at_k": 1, "recall_at_1": 0.65, "mrr_at_k": 0.962,
      "qa_cached": true, "generated_at": "2026-08-22T10:00:00Z"
    }
    """#
    let report = try! JSONDecoder().decode(EvalReportInfo.self, from: Data(json.utf8))
    #expect(report.n == 20)
    #expect(report.k == 10)
    #expect(report.config.threshold == 0.25)
    #expect(report.recallAtK == 1)
    #expect(report.recallAt1 == 0.65)
    #expect(report.mrrAtK == 0.962)
    #expect(report.qaCached)
}

@Test("EvalAnswersInfo decodes the AnswersReport shape")
func evalAnswersDecode() {
    let json = #"""
    {
      "n": 10, "answered": 9, "failed": 1,
      "correctness": 4.4, "completeness": 4.1, "grounding": 4.6, "composite": 4.37,
      "n_judges": 1, "self_judged": true,
      "judges": ["us.anthropic.claude-haiku-4-5-20251001-v1:0"],
      "qa_cached": true, "generated_at": "2026-08-22T10:00:00Z"
    }
    """#
    let report = try! JSONDecoder().decode(EvalAnswersInfo.self, from: Data(json.utf8))
    #expect(report.answered == 9)
    #expect(report.failed == 1)
    #expect(report.composite == 4.37)
    #expect(report.selfJudged)
    #expect(report.judges.count == 1)
}

@Test("EvalTuneInfo decodes the TuneReport shape, suggestion optional")
func evalTuneDecode() {
    let json = #"""
    {
      "n": 20, "k": 10,
      "current": {"name": "current", "threshold": 0.25, "bm25_weight": 1, "vector_weight": 1, "recall_at_k": 0.95, "recall_at_1": 0.6, "mrr_at_k": 0.9},
      "configs": [
        {"name": "t0.20 b1.0 v1.5", "threshold": 0.2, "bm25_weight": 1, "vector_weight": 1.5, "recall_at_k": 1, "recall_at_1": 0.7, "mrr_at_k": 0.93},
        {"name": "bm25-only", "threshold": 0, "bm25_weight": 0, "vector_weight": 0, "bm25_only": true, "recall_at_k": 0.9, "recall_at_1": 0.5, "mrr_at_k": 0.85}
      ],
      "best": {"name": "t0.20 b1.0 v1.5", "threshold": 0.2, "bm25_weight": 1, "vector_weight": 1.5, "recall_at_k": 1, "recall_at_1": 0.7, "mrr_at_k": 0.93},
      "suggestion": [
        "2nb config set ai.similarity_threshold 0.2",
        "2nb config set ai.bm25_weight 1",
        "2nb config set ai.vector_weight 1.5"
      ],
      "qa_cached": true, "generated_at": "2026-08-22T10:00:00Z"
    }
    """#
    let report = try! JSONDecoder().decode(EvalTuneInfo.self, from: Data(json.utf8))
    #expect(report.best.name == "t0.20 b1.0 v1.5")
    #expect(report.configs.count == 2)
    #expect(report.configs[1].bm25Only == true)
    #expect(report.suggestion?.count == 3)

    // The no-suggestion case (current already best) omits the field.
    let noSuggestion = #"""
    {
      "n": 20, "k": 10,
      "current": {"name": "current", "threshold": 0.25, "bm25_weight": 1, "vector_weight": 1, "recall_at_k": 1, "recall_at_1": 0.7, "mrr_at_k": 0.96},
      "configs": [],
      "best": {"name": "current", "threshold": 0.25, "bm25_weight": 1, "vector_weight": 1, "recall_at_k": 1, "recall_at_1": 0.7, "mrr_at_k": 0.96},
      "qa_cached": true, "generated_at": "2026-08-22T10:00:00Z"
    }
    """#
    let quiet = try! JSONDecoder().decode(EvalTuneInfo.self, from: Data(noSuggestion.utf8))
    #expect(quiet.suggestion == nil)
    #expect(quiet.best.bm25Only == nil)
}

// MARK: - EvalFlow gating

@Test("Cost cap doubles the estimate plus a cent; nil falls back to the CLI default")
func evalFlowCostCap() {
    let estimate = try! JSONDecoder().decode(
        EvalEstimateInfo.self,
        from: Data(#"{"command":"eval","n":20,"qa_cached":false,"generation_usd":0.1,"embed_usd":0.0001,"answers_usd":0,"total_usd":0.1001,"cost_cap":0.25}"#.utf8)
    )
    #expect(abs(EvalFlow.costCap(estimate: estimate) - 0.2102) < 1e-9)
    // The fallback must match the CLI's own default (eval.go --cost-cap),
    // never silently tighten it.
    #expect(EvalFlow.costCap(estimate: nil) == 0.25)
    // A zero-priced estimate (estimator succeeded, pricing tables did not
    // know the model) is UNUSABLE: it must not derive a ~$0.01 cap that
    // slips past the nil-estimate refusal and aborts a billing run
    // mid-flight, and it must not render a $0.00 confirm.
    let zero = try! JSONDecoder().decode(
        EvalEstimateInfo.self,
        from: Data(#"{"command":"answers","n":20,"qa_cached":true,"generation_usd":0,"embed_usd":0,"answers_usd":0,"total_usd":0,"cost_cap":0.25}"#.utf8)
    )
    #expect(EvalFlow.usable(zero) == nil)
    #expect(EvalFlow.usable(nil) == nil)
    #expect(EvalFlow.usable(estimate) != nil)
    #expect(EvalFlow.costCap(estimate: zero) == 0.25)
    #expect(EvalFlow.confirmPreview(estimate: zero) == nil)
    // The bare scorecard and tune share the cost profile (QA acquisition +
    // embeds, one upfront gate) the CLI's 0.25 default cap was sized for, so
    // both may run on the nil-estimate fallback. Only answers refuses: its
    // jury generation bills above the default and a too-low explicit cap
    // aborts MID-RUN after partial spend.
    #expect(EvalFlow.mayRunWithoutEstimate(subcommand: nil))
    #expect(!EvalFlow.mayRunWithoutEstimate(subcommand: "answers"))
    #expect(EvalFlow.mayRunWithoutEstimate(subcommand: "tune"))
}

@Test("Confirm preview adapts the estimate; nil degrades to the numberless confirm")
func evalFlowConfirmPreview() {
    let estimate = try! JSONDecoder().decode(
        EvalEstimateInfo.self,
        from: Data(#"{"command":"eval","n":20,"qa_cached":false,"generation_usd":0.1,"embed_usd":0.0001,"answers_usd":0,"total_usd":0.1001,"cost_cap":0.25}"#.utf8)
    )
    let preview = EvalFlow.confirmPreview(estimate: estimate)
    #expect(preview?.totalUSD == 0.1001)
    #expect(preview?.estimates.isEmpty == true)
    #expect(EvalFlow.confirmPreview(estimate: nil) == nil)
}

@Test("QA context explains the one-time generation cost before any spend")
func evalFlowQAContext() {
    let uncached = try! JSONDecoder().decode(
        EvalEstimateInfo.self,
        from: Data(#"{"command":"eval","n":20,"qa_cached":false,"generation_usd":0.1324,"embed_usd":0.0001,"answers_usd":0,"total_usd":0.1325,"cost_cap":0.25}"#.utf8)
    )
    let fresh = EvalFlow.qaContext(estimate: uncached)
    #expect(fresh.contains("No cached QA set"))
    #expect(fresh.contains("one-time"))
    #expect(fresh.contains("$0.1324"))

    let cached = try! JSONDecoder().decode(
        EvalEstimateInfo.self,
        from: Data(#"{"command":"eval","n":12,"qa_cached":true,"generation_usd":0,"embed_usd":0.0001,"answers_usd":0,"total_usd":0.0001,"cost_cap":0.25}"#.utf8)
    )
    let reuse = EvalFlow.qaContext(estimate: cached)
    #expect(reuse.contains("Cached QA set of 12"))
    #expect(EvalFlow.qaContext(estimate: nil).contains("without a number"))
}

// MARK: - Argv builders

@Test("evalEstimateArgs pins the estimate argv per subcommand")
func evalEstimateArgsShape() {
    #expect(AppState.evalEstimateArgs(subcommand: nil) == ["eval", "--estimate", "--json", "--porcelain"])
    #expect(AppState.evalEstimateArgs(subcommand: "answers") == ["eval", "answers", "--estimate", "--json", "--porcelain"])
    #expect(AppState.evalEstimateArgs(subcommand: "tune") == ["eval", "tune", "--estimate", "--json", "--porcelain"])
}

@Test("evalRunArgs carries --yes and the derived --cost-cap")
func evalRunArgsShape() {
    #expect(AppState.evalRunArgs(subcommand: nil, costCap: 0.2102)
            == ["eval", "--json", "--yes", "--cost-cap", "0.2102", "--porcelain"])
    #expect(AppState.evalRunArgs(subcommand: "answers", costCap: 1.5)
            == ["eval", "answers", "--json", "--yes", "--cost-cap", "1.5000", "--porcelain"])
}

@Test("modelsListArgs appends --sort best only when asked")
func modelsListArgsSortBest() {
    #expect(AppState.modelsListArgs(discover: false) == ["models", "list", "--json", "--porcelain"])
    #expect(AppState.modelsListArgs(discover: false, sortBest: true)
            == ["models", "list", "--json", "--porcelain", "--sort", "best"])
    #expect(AppState.modelsListArgs(discover: true, sortBest: true)
            == ["models", "list", "--json", "--porcelain", "--discover", "--sort", "best"])
}
