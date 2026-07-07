import Foundation
import Testing
@testable import SecondBrain

// Pins the class-aware Validation logic: how a broken finding is classified from
// its lint `fix` field plus its top suggest-target candidate, and how the
// Fix-all plan, header counts, and helpers are derived. All pure (no CLI), so a
// stub `suggest` closure exercises the async planner. The real-data finding that
// drives the design (strict high-confidence yields ZERO matches on real vaults;
// ghostty -> "Ghostty Config" is MEDIUM) is why MEDIUM candidates must plan.

private func candidate(
    path: String, title: String = "", score: Double = 1.0, confidence: String?
) -> SuggestTargetResult {
    SuggestTargetResult(path: path, title: title, score: score, snippet: "", confidence: confidence)
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

@Test("a high-confidence candidate classifies as did-you-mean")
func classifyHighIsDidYouMean() {
    let cand = candidate(path: "resources/auth-flow.md", title: "Auth Flow", confidence: "high")
    let cls = FixAllPlanner.classify(fix: "missing", topCandidate: cand, driftTarget: nil)
    #expect(cls == .didYouMean(cand))
    #expect(cls.isOneClick)
}

@Test("a MEDIUM candidate also classifies as did-you-mean (the real-data rule)")
func classifyMediumIsDidYouMean() {
    // ghostty -> "Ghostty Config" is medium on real data; a high-only rule would
    // no-op Fix-all, so medium must be one-click.
    let cand = candidate(path: "resources/ghostty-config.md", title: "Ghostty Config", confidence: "medium")
    let cls = FixAllPlanner.classify(fix: "ambiguous", topCandidate: cand, driftTarget: nil)
    #expect(cls == .didYouMean(cand))
    #expect(cls.isOneClick)
}

@Test("a low candidate with an ambiguous fix classifies as ambiguous (decision)")
func classifyLowAmbiguousIsAmbiguous() {
    let cand = candidate(path: "a.md", confidence: "low")
    let cls = FixAllPlanner.classify(fix: "ambiguous", topCandidate: cand, driftTarget: nil)
    #expect(cls == .ambiguous)
    #expect(!cls.isOneClick)
}

@Test("no candidate with a missing fix classifies as missing (decision)")
func classifyMissingIsMissing() {
    let cls = FixAllPlanner.classify(fix: "missing", topCandidate: nil, driftTarget: nil)
    #expect(cls == .missing)
    #expect(!cls.isOneClick)
}

@Test("an old CLI (nil fix) leans on the candidate: high -> did-you-mean, else missing")
func classifyOldCLIDegrades() {
    let high = candidate(path: "n.md", title: "Note", confidence: "high")
    #expect(FixAllPlanner.classify(fix: nil, topCandidate: high, driftTarget: nil) == .didYouMean(high))
    #expect(FixAllPlanner.classify(fix: nil, topCandidate: nil, driftTarget: nil) == .missing)
    let low = candidate(path: "n.md", confidence: "low")
    #expect(FixAllPlanner.classify(fix: nil, topCandidate: low, driftTarget: nil) == .missing)
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

@Test("a medium/high candidate plans a relink to the candidate (md dropped)")
func planCandidateIsRelink() async {
    let finding = BrokenFinding(path: "n.md", target: "auth flow", fix: "missing", driftTarget: nil)
    let cand = candidate(path: "resources/auth-flow.md", title: "Auth Flow", confidence: "medium")
    let plan = await FixAllPlanner.plan(findings: [finding]) { _ in [cand] }
    #expect(plan.count == 1)
    #expect(plan[0].action == .relink(chosenPath: "resources/auth-flow", chosenDisplay: "Auth Flow"))
    #expect(plan[0].chosenDisplay == "Auth Flow")
    #expect(plan[0].kindLabel == "Did you mean")
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
        default: return []
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
    let plan = FixAllPlanner.plan(from: [
        (f1, .repairable(driftTarget: "Alpha")),
        (f2, .didYouMean(cand)),
        (f3, .ambiguous),
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
        .ambiguous,
        .missing,
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
    #expect(LinkFixClass.didYouMean(candidate(path: "y.md", confidence: "high")).badgeText == "did you mean?")
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
