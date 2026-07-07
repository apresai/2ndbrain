import Foundation
import Testing
@testable import SecondBrain

// Pins the "Fix each" walkthrough helpers on FixAllPlanner: given classified
// broken findings, the step-through queue is exactly the decision-class findings
// (everything Fix-all cannot resolve one-click), in display order, deduped. It
// is the complement of `plan(from:)` over the same classifications. Plus the
// 1-based progress label. All pure (no CLI), matching FixAllPlannerTests style.

private func candidate(
    path: String, title: String = "", score: Double = 1.0, confidence: String?
) -> SuggestTargetResult {
    SuggestTargetResult(path: path, title: title, score: score, snippet: "", confidence: confidence)
}

// MARK: - walkthroughQueue

@Test("the walkthrough queue holds only decision-class findings, in display order")
func walkthroughQueueDecisionOnly() {
    let f1 = BrokenFinding(path: "1.md", target: "a", fix: "drift", driftTarget: "Alpha")
    let cand = candidate(path: "beta.md", title: "Beta", confidence: "high")
    let f2 = BrokenFinding(path: "2.md", target: "b", fix: "missing", driftTarget: nil)
    let f3 = BrokenFinding(path: "3.md", target: "c", fix: "ambiguous", driftTarget: nil)
    let f4 = BrokenFinding(path: "4.md", target: "d", fix: "missing", driftTarget: nil)
    let classified: [(BrokenFinding, LinkFixClass)] = [
        (f1, .repairable(driftTarget: "Alpha")),   // one-click, excluded
        (f2, .didYouMean(cand)),                    // one-click, excluded
        (f3, .ambiguous),                           // decision, included
        (f4, .missing),                             // decision, included
    ]
    let queue = FixAllPlanner.walkthroughQueue(from: classified)
    #expect(queue.map(\.id) == ["3.md|c", "4.md|d"])
}

@Test("a one-click-fixable finding is excluded from the walkthrough queue")
func walkthroughQueueExcludesOneClick() {
    let drift = BrokenFinding(path: "n.md", target: "ghostty", fix: "drift", driftTarget: "Ghostty Config")
    let didYouMean = BrokenFinding(path: "m.md", target: "auth", fix: "missing", driftTarget: nil)
    let cand = candidate(path: "auth.md", title: "Auth", confidence: "medium")
    let queue = FixAllPlanner.walkthroughQueue(from: [
        (drift, .repairable(driftTarget: "Ghostty Config")),
        (didYouMean, .didYouMean(cand)),
    ])
    #expect(queue.isEmpty)
}

@Test("the walkthrough queue is exactly the complement of the Fix-all plan")
func walkthroughQueueComplementsPlan() {
    let cand = candidate(path: "resources/auth.md", title: "Auth", confidence: "high")
    let classified: [(BrokenFinding, LinkFixClass)] = [
        (BrokenFinding(path: "1.md", target: "ghostty", fix: "drift", driftTarget: "Ghostty Config"),
         .repairable(driftTarget: "Ghostty Config")),
        (BrokenFinding(path: "2.md", target: "auth", fix: "missing", driftTarget: nil),
         .didYouMean(cand)),
        (BrokenFinding(path: "3.md", target: "mystery", fix: "missing", driftTarget: nil),
         .missing),
        (BrokenFinding(path: "4.md", target: "setup", fix: "ambiguous", driftTarget: nil),
         .ambiguous),
    ]
    let planIDs = Set(FixAllPlanner.plan(from: classified).map(\.id))
    let queueIDs = Set(FixAllPlanner.walkthroughQueue(from: classified).map(\.id))
    // Disjoint: a finding is in the Fix-all plan OR the walkthrough queue.
    #expect(planIDs.isDisjoint(with: queueIDs))
    // Together they cover every distinct finding, so nothing is dropped.
    let allIDs = Set(classified.map { $0.0.id })
    #expect(planIDs.union(queueIDs) == allIDs)
    #expect(planIDs == ["1.md|ghostty", "2.md|auth"])
    #expect(queueIDs == ["3.md|mystery", "4.md|setup"])
}

@Test("duplicate occurrences of the same decision finding queue once")
func walkthroughQueueDedupsDuplicates() {
    // lint emits one finding per link occurrence; the sheet resolves a whole
    // file by target, so a note referencing the same broken target twice must
    // only step through once.
    let first = BrokenFinding(path: "n.md", target: "gone", fix: "missing", driftTarget: nil)
    let second = BrokenFinding(path: "n.md", target: "gone", fix: "missing", driftTarget: nil)
    let queue = FixAllPlanner.walkthroughQueue(from: [
        (first, .missing),
        (second, .missing),
    ])
    #expect(queue.count == 1)
    #expect(queue[0].id == "n.md|gone")
}

// MARK: - walkthroughProgress

@Test("walkthroughProgress renders a 1-based position, e.g. 2 of 5")
func walkthroughProgressLabel() {
    #expect(FixAllPlanner.walkthroughProgress(index: 0, total: 5) == "1 of 5")
    #expect(FixAllPlanner.walkthroughProgress(index: 1, total: 5) == "2 of 5")
    #expect(FixAllPlanner.walkthroughProgress(index: 4, total: 5) == "5 of 5")
    #expect(FixAllPlanner.walkthroughProgress(index: 0, total: 1) == "1 of 1")
}
