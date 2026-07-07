import Foundation

/// A broken `[[wikilink]]` finding, normalized from a `LintIssue` for the
/// class-aware Validation flow. `fix`/`driftTarget` come from the CLI's additive
/// lint classification (`2nb lint --json`); they are nil on a pre-classification
/// CLI, where the flow degrades to the suggest-target candidate alone.
struct BrokenFinding: Equatable {
    let path: String        // the note that contains the broken link
    let target: String      // the raw authored [[target]]
    let fix: String?        // "drift" | "ambiguous" | "missing" | nil (old CLI)
    let driftTarget: String? // canonical target when fix == "drift"

    /// Stable identity: a repair scoped to `--target` fixes every occurrence of
    /// the same target in the same file, so path+target collapses duplicates.
    var id: String { "\(path)|\(target)" }
}

/// What can be done about one broken finding, and how confidently. The four
/// cases map one-to-one to the per-row badges. `.repairable` and `.didYouMean`
/// are one-click (they go into the Fix-all plan); `.ambiguous` and `.missing`
/// need a human decision (they route to the per-finding sheet). Pure and
/// Equatable so every branch is unit-testable.
enum LinkFixClass: Equatable {
    /// The target maps to exactly one existing note by name drift (case /
    /// separator / whitespace). One-click via `repair-links`.
    case repairable(driftTarget: String?)
    /// A high- or medium-confidence suggest-target candidate exists. One-click
    /// via `relink` to that note.
    case didYouMean(SuggestTargetResult)
    /// The target maps to more than one note and no confident candidate stands
    /// out. Needs a pick.
    case ambiguous
    /// The target maps to no note (or only low-confidence candidates). Needs a
    /// decision (relink to a low candidate, create, or unlink).
    case missing

    var isOneClick: Bool {
        switch self {
        case .repairable, .didYouMean: return true
        case .ambiguous, .missing: return false
        }
    }

    /// Subtle capsule label next to the finding.
    var badgeText: String {
        switch self {
        case .repairable: return "auto-repairable"
        case .didYouMean: return "did you mean?"
        case .ambiguous: return "matches several"
        case .missing: return "no matching note"
        }
    }
}

/// One planned link rewrite in the Fix-all flow: either a deterministic drift
/// repair or a relink to a confident candidate. Pure data so the planner is
/// unit-testable; the sheet renders `[[target]] -> [[chosenDisplay]]`.
struct PlannedRewrite: Identifiable, Equatable {
    enum Action: Equatable {
        /// `repair-links --target <target> --write`. `driftTarget` is the
        /// canonical target shown in the preview (the CLI recomputes it).
        case repair(driftTarget: String)
        /// `relink --from <target> --to <chosenPath> --write`. `chosenPath` is
        /// the vault-relative path with `.md` dropped; `chosenDisplay` is the
        /// human title.
        case relink(chosenPath: String, chosenDisplay: String)
    }

    let path: String    // the note containing the broken link
    let target: String  // the authored [[target]] being rewritten
    let action: Action

    var id: String { "\(path)|\(target)" }

    /// The label shown on the right side of the `[[target]] -> [[…]]` preview.
    var chosenDisplay: String {
        switch action {
        case .repair(let d): return d
        case .relink(_, let d): return d
        }
    }

    /// A subtle kind tag ("Repair" / "Did you mean").
    var kindLabel: String {
        switch action {
        case .repair: return "Repair"
        case .relink: return "Did you mean"
        }
    }
}

/// Counts of one-click-fixable vs decision-needed broken findings, for the
/// class-aware header.
struct ClassCounts: Equatable {
    let oneClick: Int
    let decision: Int
    var total: Int { oneClick + decision }
}

/// Pure planning for the class-aware Validation flow: classify each broken
/// finding, build the Fix-all plan, and summarize the counts. No CLI, no view:
/// the suggest-target lookup is injected so the whole thing is unit-testable.
enum FixAllPlanner {

    /// Classify one broken finding from its lint `fix` field and its top
    /// suggest-target candidate. A "drift" fix is always one-click repairable; a
    /// high/medium candidate is a confident relink; otherwise the CLI's
    /// ambiguous/missing verdict (or, on an old CLI with no `fix`, "missing")
    /// routes it to a decision.
    static func classify(
        fix: String?, topCandidate: SuggestTargetResult?, driftTarget: String?
    ) -> LinkFixClass {
        if fix == "drift" {
            return .repairable(driftTarget: driftTarget)
        }
        if let c = topCandidate, c.confidence == "high" || c.confidence == "medium" {
            return .didYouMean(c)
        }
        if fix == "ambiguous" {
            return .ambiguous
        }
        return .missing
    }

    /// The Fix-all plan projected from already-classified findings (synchronous,
    /// so the view derives it without re-running suggest-target): one-click
    /// classes become rewrites, decision classes are excluded.
    static func plan(from classified: [(BrokenFinding, LinkFixClass)]) -> [PlannedRewrite] {
        var plans: [PlannedRewrite] = []
        // lint emits one finding per link OCCURRENCE, so a note that references
        // the same broken target twice yields two findings with an identical
        // (path, target) id. repair-links/relink are whole-file by target, so a
        // single rewrite already fixes every occurrence; keep only the first per
        // id. Without this the plan carries duplicate Identifiable ids (SwiftUI
        // ForEach is undefined) and apply() runs the same rewrite twice, the
        // second a no-op that would inflate the failure count.
        var seen = Set<String>()
        for (finding, cls) in classified {
            let rewrite: PlannedRewrite
            switch cls {
            case .repairable(let driftTarget):
                rewrite = PlannedRewrite(
                    path: finding.path, target: finding.target,
                    action: .repair(driftTarget: driftTarget ?? finding.target))
            case .didYouMean(let cand):
                let to = (cand.path as NSString).deletingPathExtension
                rewrite = PlannedRewrite(
                    path: finding.path, target: finding.target,
                    action: .relink(chosenPath: to, chosenDisplay: cand.displayTitle))
            case .ambiguous, .missing:
                continue
            }
            if seen.insert(rewrite.id).inserted {
                plans.append(rewrite)
            }
        }
        return plans
    }

    /// End-to-end plan: classify each finding (fetching candidates only for the
    /// non-drift ones, since a drift fix needs no lookup) then project. This is
    /// the injectable entry point the unit tests drive with a stub `suggest`.
    static func plan(
        findings: [BrokenFinding],
        suggest: (BrokenFinding) async -> [SuggestTargetResult]
    ) async -> [PlannedRewrite] {
        var classified: [(BrokenFinding, LinkFixClass)] = []
        for f in findings {
            if f.fix == "drift" {
                classified.append((f, .repairable(driftTarget: f.driftTarget)))
            } else {
                let cands = await suggest(f)
                classified.append((f, classify(fix: f.fix, topCandidate: cands.first, driftTarget: f.driftTarget)))
            }
        }
        return plan(from: classified)
    }

    /// One-click vs decision split over a set of classifications.
    static func counts(_ classes: [LinkFixClass]) -> ClassCounts {
        let oneClick = classes.filter { $0.isOneClick }.count
        return ClassCounts(oneClick: oneClick, decision: classes.count - oneClick)
    }

    /// The class-aware header line, e.g.
    /// "7 broken links: 2 one-click fixable, 5 need a decision".
    static func headerSummary(total: Int, counts: ClassCounts) -> String {
        let links = "\(total) broken link\(total == 1 ? "" : "s")"
        let decides = counts.decision == 1 ? "needs a decision" : "need a decision"
        return "\(links): \(counts.oneClick) one-click fixable, \(counts.decision) \(decides)"
    }

    /// Whether "Create the note" is a sensible fix for a target. A path-qualified
    /// target (contains "/") names a specific location, not a title to mint, and
    /// the repair index refuses it by design, so hide Create for it.
    static func canCreateNote(forTarget target: String) -> Bool {
        !target.contains("/")
    }
}
