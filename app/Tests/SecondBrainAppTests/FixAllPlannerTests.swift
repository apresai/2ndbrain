import Foundation
import Testing
@testable import SecondBrain

// Pins the class-aware Validation logic: how a broken finding is classified from
// its lint `fix` field plus ranked suggest-target candidates, and how the
// Fix-all plan, header counts, and helpers are derived. All pure (no CLI), so a
// stub `suggest` closure exercises the async planner.
//
// Policy (updated): only HIGH confidence (or drift) is one-click / Fix-all.
// Medium/low candidates become `.recommend` (top 2-3 for a human pick).

private func candidate(
    path: String, title: String = "", score: Double = 1.0, confidence: String?, reason: String? = nil
) -> SuggestTargetResult {
    SuggestTargetResult(path: path, title: title, score: score, snippet: "", confidence: confidence, reason: reason)
}

// MARK: - classify

@Test("a drift fix classifies as repairable, ignoring any candidate")
func classifyDriftIsRepairable() {
    let cls = FixAllPlanner.classify(
        fix: "drift", topCandidate: candidate(path: "x.md", confidence: "low"),
        driftTarget: "Ghostty Config")
    #expect(cls == .repairable(driftTarget: "Ghostty Config"))
    #expect(cls.isOneClick)
}

@Test("a high-confidence candidate classifies as did-you-mean (one-click)")
func classifyHighIsDidYouMean() {
    let cand = candidate(path: "resources/auth-flow.md", title: "Auth Flow", confidence: "high")
    let cls = FixAllPlanner.classify(fix: "missing", topCandidate: cand, driftTarget: nil)
    #expect(cls == .didYouMean(cand))
    #expect(cls.isOneClick)
}

@Test("a MEDIUM candidate classifies as recommend (not one-click)")
func classifyMediumIsRecommend() {
    // Medium is a recommendation only: Fix-all is high-confidence exclusive so
    // a weak semantic hit cannot bulk-rewrite the vault.
    let cand = candidate(path: "resources/ghostty-config.md", title: "Ghostty Config", confidence: "medium")
    let cls = FixAllPlanner.classify(fix: "ambiguous", topCandidate: cand, driftTarget: nil)
    #expect(cls == .recommend([cand]))
    #expect(!cls.isOneClick)
    #expect(cls.recommendations.count == 1)
}

@Test("multiple non-high candidates become a top-3 recommend shortlist")
func classifyRecommendCapsAtThree() {
    let cands = (1...5).map { i in
        candidate(path: "n\(i).md", title: "N\(i)", confidence: "low")
    }
    let cls = FixAllPlanner.classify(fix: "missing", candidates: cands, driftTarget: nil)
    guard case .recommend(let recs) = cls else {
        Issue.record("expected recommend, got \(cls)")
        return
    }
    #expect(recs.count == 3)
    #expect(recs.map(\.path) == ["n1.md", "n2.md", "n3.md"])
    #expect(!cls.isOneClick)
}

@Test("a low candidate with an ambiguous fix and no shortlist uses ambiguous")
func classifyEmptyAmbiguousIsAmbiguous() {
    let cls = FixAllPlanner.classify(fix: "ambiguous", candidates: [], driftTarget: nil)
    #expect(cls == .ambiguous)
    #expect(!cls.isOneClick)
}

@Test("no candidate with a missing fix classifies as missing (decision)")
func classifyMissingIsMissing() {
    let cls = FixAllPlanner.classify(fix: "missing", topCandidate: nil, driftTarget: nil)
    #expect(cls == .missing)
    #expect(!cls.isOneClick)
}

@Test("an old CLI (nil fix) leans on the candidate: high -> did-you-mean, else recommend/missing")
func classifyOldCLIDegrades() {
    let high = candidate(path: "n.md", title: "Note", confidence: "high")
    #expect(FixAllPlanner.classify(fix: nil, topCandidate: high, driftTarget: nil) == .didYouMean(high))
    #expect(FixAllPlanner.classify(fix: nil, topCandidate: nil, driftTarget: nil) == .missing)
    let low = candidate(path: "n.md", confidence: "low")
    #expect(FixAllPlanner.classify(fix: nil, topCandidate: low, driftTarget: nil) == .recommend([low]))
}

// MARK: - plan

@Test("a drift finding plans a repair to its drift target and skips the lookup")
func planDriftIsRepair() async {
    let finding = BrokenFinding(
        path: "notes/ghostty.md", target: "ghostty", fix: "drift", driftTarget: "Ghostty Config")
    var lookups = 0
    let plan = await FixAllPlanner.plan(findings: [finding]) { _ in
        lookups += 1
        return []
    }
    // A drift fix is already resolved by the CLI, so no suggest-target call.
    #expect(lookups == 0)
    #expect(plan.count == 1)
    #expect(plan[0].path == "notes/ghostty.md")
    #expect(plan[0].target == "ghostty")
    #expect(plan[0].action == .repair(driftTarget: "Ghostty Config"))
    #expect(plan[0].chosenDisplay == "Ghostty Config")
}

@Test("a high candidate plans a relink; medium does not")
func planHighIsRelinkMediumExcluded() async {
    let highFinding = BrokenFinding(path: "n.md", target: "auth flow", fix: "missing", driftTarget: nil)
    let high = candidate(path: "resources/auth-flow.md", title: "Auth Flow", confidence: "high")
    let planHigh = await FixAllPlanner.plan(findings: [highFinding]) { _ in [high] }
    #expect(planHigh.count == 1)
    #expect(planHigh[0].action == .relink(chosenPath: "resources/auth-flow", chosenDisplay: "Auth Flow"))
    #expect(planHigh[0].kindLabel == "High confidence")

    let medFinding = BrokenFinding(path: "m.md", target: "ghostty", fix: "missing", driftTarget: nil)
    let med = candidate(path: "resources/ghostty-config.md", title: "Ghostty Config", confidence: "medium")
    let planMed = await FixAllPlanner.plan(findings: [medFinding]) { _ in [med] }
    #expect(planMed.isEmpty)
}

@Test("low/no-candidate decision findings are excluded from the plan")
func planExcludesDecisions() async {
    let ambiguous = BrokenFinding(path: "a.md", target: "setup", fix: "ambiguous", driftTarget: nil)
    let missing = BrokenFinding(path: "b.md", target: "gone", fix: "missing", driftTarget: nil)
    let plan = await FixAllPlanner.plan(findings: [ambiguous, missing]) { f in
        // Only low-confidence candidates for both, so neither should plan.
        [candidate(path: "z.md", confidence: "low")]
    }
    #expect(plan.isEmpty)
}

@Test("a mixed set plans only the one-click findings, in order")
func planMixedSubset() async {
    let drift = BrokenFinding(path: "1.md", target: "ghostty", fix: "drift", driftTarget: "Ghostty Config")
    let didYouMean = BrokenFinding(path: "2.md", target: "auth", fix: "missing", driftTarget: nil)
    let decision = BrokenFinding(path: "3.md", target: "mystery", fix: "missing", driftTarget: nil)
    let plan = await FixAllPlanner.plan(findings: [drift, didYouMean, decision]) { f in
        switch f.target {
        case "auth": return [candidate(path: "resources/auth.md", title: "Auth", confidence: "high")]
        default: return [candidate(path: "maybe.md", confidence: "medium")]
        }
    }
    #expect(plan.map(\.path) == ["1.md", "2.md"])
    #expect(plan[0].action == .repair(driftTarget: "Ghostty Config"))
    #expect(plan[1].action == .relink(chosenPath: "resources/auth", chosenDisplay: "Auth"))
}

@Test("duplicate occurrences of the same broken link plan a single rewrite")
func planDedupsDuplicateOccurrences() async {
    // lint emits one finding per link occurrence; a note that references the
    // same broken target twice must plan ONE rewrite (repair-links/relink are
    // whole-file), or SwiftUI sees duplicate ids and apply() double-runs it.
    let first = BrokenFinding(path: "n.md", target: "ghostty", fix: "drift", driftTarget: "Ghostty Config")
    let second = BrokenFinding(path: "n.md", target: "ghostty", fix: "drift", driftTarget: "Ghostty Config")
    let plan = await FixAllPlanner.plan(findings: [first, second]) { _ in [] }
    #expect(plan.count == 1)
    #expect(plan[0].id == "n.md|ghostty")
    #expect(Set(plan.map(\.id)).count == plan.count) // no duplicate Identifiable ids
}

@Test("plan(from:) projects classifications without a lookup")
func planFromClassifications() {
    let f1 = BrokenFinding(path: "1.md", target: "a", fix: "drift", driftTarget: "Alpha")
    let cand = candidate(path: "beta.md", title: "Beta", confidence: "high")
    let f2 = BrokenFinding(path: "2.md", target: "b", fix: "missing", driftTarget: nil)
    let f3 = BrokenFinding(path: "3.md", target: "c", fix: "ambiguous", driftTarget: nil)
    let med = candidate(path: "gamma.md", title: "Gamma", confidence: "medium")
    let plan = FixAllPlanner.plan(from: [
        (f1, .repairable(driftTarget: "Alpha")),
        (f2, .didYouMean(cand)),
        (f3, .recommend([med])),
    ])
    #expect(plan.count == 2)
    #expect(plan[0].action == .repair(driftTarget: "Alpha"))
    #expect(plan[1].action == .relink(chosenPath: "beta", chosenDisplay: "Beta"))
}

// MARK: - counts + header

@Test("counts split one-click vs decision classes")
func countsSplit() {
    let classes: [LinkFixClass] = [
        .repairable(driftTarget: "X"),
        .didYouMean(candidate(path: "y.md", confidence: "high")),
        .recommend([candidate(path: "z.md", confidence: "medium")]),
        .ambiguous,
        .missing,
    ]
    let counts = FixAllPlanner.counts(classes)
    #expect(counts.oneClick == 2)
    #expect(counts.decision == 3)
    #expect(counts.total == 5)
}

@Test("headerSummary renders the class-aware line with correct pluralization")
func headerSummaryString() {
    #expect(
        FixAllPlanner.headerSummary(total: 7, counts: ClassCounts(oneClick: 2, decision: 5))
            == "7 broken links: 2 one-click fixable, 5 need a decision")
    // Singular link + singular decision.
    #expect(
        FixAllPlanner.headerSummary(total: 1, counts: ClassCounts(oneClick: 0, decision: 1))
            == "1 broken link: 0 one-click fixable, 1 needs a decision")
    // Nothing needs a decision.
    #expect(
        FixAllPlanner.headerSummary(total: 3, counts: ClassCounts(oneClick: 3, decision: 0))
            == "3 broken links: 3 one-click fixable, 0 need a decision")
}

// MARK: - create gate

@Test("canCreateNote is false for a path-qualified target, true for a plain title")
func canCreateNotePathQualified() {
    #expect(FixAllPlanner.canCreateNote(forTarget: "Auth Flow"))
    #expect(!FixAllPlanner.canCreateNote(forTarget: "resources/auth-flow"))
    #expect(!FixAllPlanner.canCreateNote(forTarget: "folder/sub/note"))
}

// MARK: - badges

@Test("each class maps to its subtle badge label")
func linkFixClassBadgeLabels() {
    #expect(LinkFixClass.repairable(driftTarget: "X").badgeText == "auto-repairable")
    #expect(LinkFixClass.didYouMean(candidate(path: "y.md", confidence: "high")).badgeText == "high confidence")
    #expect(LinkFixClass.recommend([candidate(path: "z.md", confidence: "medium")]).badgeText == "top picks")
    #expect(LinkFixClass.ambiguous.badgeText == "matches several")
    #expect(LinkFixClass.missing.badgeText == "no matching note")
}

// MARK: - Fix-all result banner

@Test("fixAllResultBanner picks tone and pluralizes")
func fixAllBannerTones() {
    let ok = LinkFixOutcome.fixAllResultBanner(fixed: 3, failed: 0)
    #expect(ok.tone == .success)
    #expect(ok.message == "Fixed 3 links.")

    let one = LinkFixOutcome.fixAllResultBanner(fixed: 1, failed: 0)
    #expect(one.message == "Fixed 1 link.")

    let none = LinkFixOutcome.fixAllResultBanner(fixed: 0, failed: 2)
    #expect(none.tone == .error)
    #expect(none.message == "No links were fixed. 2 couldn’t be applied.")

    let partial = LinkFixOutcome.fixAllResultBanner(fixed: 2, failed: 1)
    #expect(partial.tone == .success)
    #expect(partial.message == "Fixed 2 links. 1 couldn’t be applied.")
}

// MARK: - verdict-driven classification (suggest-target --verdict envelope)

private func envelope(
    action: String, to: String? = nil, confidence: String? = nil,
    llm: String = "off", candidates: [SuggestTargetResult] = []
) -> SuggestVerdictEnvelope {
    SuggestVerdictEnvelope(
        recommendation: SuggestRecommendation(
            action: action, to: to, title: nil, confidence: confidence, reason: nil),
        llm: llm,
        candidates: candidates)
}

@Test("an unlink verdict classifies as removable, keeping weak candidates for the sheet")
func classifyVerdictUnlinkIsRemovable() {
    let weak = candidate(path: "weak.md", confidence: "low")
    let cls = FixAllPlanner.classify(
        fix: "missing",
        envelope: envelope(action: "unlink", llm: "declined", candidates: [weak]),
        driftTarget: nil)
    #expect(cls == .removable(candidates: [weak]))
    #expect(cls.isRemovable)
    #expect(!cls.isOneClick)
    #expect(cls.recommendations == [weak])
    #expect(cls.badgeText == "remove?")
}

@Test("a high relink verdict classifies as one-click did-you-mean")
func classifyVerdictHighRelinkIsDidYouMean() {
    let top = candidate(path: "resources/auth-flow.md", title: "Auth Flow", confidence: "high")
    let cls = FixAllPlanner.classify(
        fix: "missing",
        envelope: envelope(action: "relink", to: top.path, confidence: "high", llm: "skipped", candidates: [top]),
        driftTarget: nil)
    #expect(cls == .didYouMean(top))
    #expect(cls.isOneClick)
}

@Test("a medium relink verdict classifies as recommend (never one-click)")
func classifyVerdictMediumRelinkIsRecommend() {
    let top = candidate(path: "a.md", confidence: "medium")
    let other = candidate(path: "b.md", confidence: "low")
    let cls = FixAllPlanner.classify(
        fix: "missing",
        envelope: envelope(action: "relink", to: top.path, confidence: "medium", llm: "promoted", candidates: [top, other]),
        driftTarget: nil)
    #expect(cls == .recommend([top, other]))
    #expect(!cls.isOneClick)
}

@Test("a drift fix wins over any verdict (no envelope consulted)")
func classifyVerdictDriftStillRepairable() {
    let cls = FixAllPlanner.classify(
        fix: "drift",
        envelope: envelope(action: "unlink", llm: "declined"),
        driftTarget: "Real Note")
    #expect(cls == .repairable(driftTarget: "Real Note"))
}

@Test("an unknown verdict action degrades to the candidate-only classification")
func classifyVerdictUnknownActionDegrades() {
    let top = candidate(path: "a.md", confidence: "medium")
    let cls = FixAllPlanner.classify(
        fix: "missing",
        envelope: envelope(action: "mystery", candidates: [top]),
        driftTarget: nil)
    #expect(cls == .recommend([top]))
}

// MARK: - removable counts, header, and removal plan

@Test("counts splits one-click, decision, and removable")
func countsSplitsRemovable() {
    let classes: [LinkFixClass] = [
        .repairable(driftTarget: "X"),
        .recommend([candidate(path: "a.md", confidence: "medium")]),
        .removable(candidates: []),
        .removable(candidates: []),
        .missing,
    ]
    let counts = FixAllPlanner.counts(classes)
    #expect(counts == ClassCounts(oneClick: 1, decision: 2, removable: 2))
    #expect(counts.total == 5)
}

@Test("headerSummary appends the removable clause only when nonzero")
func headerSummaryRemovableClause() {
    #expect(
        FixAllPlanner.headerSummary(total: 7, counts: ClassCounts(oneClick: 2, decision: 4, removable: 1))
            == "7 broken links: 2 one-click fixable, 4 need a decision, 1 removable")
    #expect(
        FixAllPlanner.headerSummary(total: 7, counts: ClassCounts(oneClick: 2, decision: 5))
            == "7 broken links: 2 one-click fixable, 5 need a decision")
}

@Test("removalPlan collects removable findings, deduped by id")
func removalPlanDedupes() {
    let f1 = BrokenFinding(path: "n.md", target: "dead", fix: "missing", driftTarget: nil)
    let f1dup = BrokenFinding(path: "n.md", target: "dead", fix: "missing", driftTarget: nil)
    let f2 = BrokenFinding(path: "m.md", target: "gone", fix: "missing", driftTarget: nil)
    let keep = BrokenFinding(path: "k.md", target: "ok", fix: "missing", driftTarget: nil)
    let plan = FixAllPlanner.removalPlan(from: [
        (f1, .removable(candidates: [])),
        (f1dup, .removable(candidates: [])),
        (f2, .removable(candidates: [])),
        (keep, .recommend([candidate(path: "a.md", confidence: "medium")])),
    ])
    #expect(plan.map(\.id) == ["n.md|dead", "m.md|gone"])
}

@Test("removable findings stay in the walkthrough queue (the Fix-all complement invariant)")
func walkthroughIncludesRemovable() {
    let dead = BrokenFinding(path: "n.md", target: "dead", fix: "missing", driftTarget: nil)
    let drift = BrokenFinding(path: "d.md", target: "Drift", fix: "drift", driftTarget: "Drift Note")
    let queue = FixAllPlanner.walkthroughQueue(from: [
        (drift, .repairable(driftTarget: "Drift Note")),
        (dead, .removable(candidates: [])),
    ])
    #expect(queue.map(\.id) == ["n.md|dead"])
}

// MARK: - Remove-all result banner

@Test("removeAllResultBanner picks tone and pluralizes")
func removeAllBannerTones() {
    let ok = LinkFixOutcome.removeAllResultBanner(removed: 2, failed: 0)
    #expect(ok.tone == .success)
    #expect(ok.message == "Removed 2 dead links (text kept).")

    let one = LinkFixOutcome.removeAllResultBanner(removed: 1, failed: 0)
    #expect(one.message == "Removed 1 dead link (text kept).")

    let none = LinkFixOutcome.removeAllResultBanner(removed: 0, failed: 2)
    #expect(none.tone == .error)
    #expect(none.message == "No links were removed. 2 couldn’t be applied.")
}
