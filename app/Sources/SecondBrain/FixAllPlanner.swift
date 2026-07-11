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

/// What can be done about one broken finding, and how confidently. The cases
/// map one-to-one to the per-row badges. `.repairable` and `.didYouMean` (HIGH
/// confidence only) are one-click (they go into the Fix-all plan).
/// `.recommend` holds the top 2-3 non-high candidates for a human pick;
/// `.ambiguous` and `.missing` need Create / Unlink / pick when there is no
/// shortlist. Pure and Equatable so every branch is unit-testable.
enum LinkFixClass: Equatable {
    /// The target maps to exactly one existing note by name drift (case /
    /// separator / whitespace). One-click via `repair-links`.
    case repairable(driftTarget: String?)
    /// A HIGH-confidence suggest-target candidate exists. One-click via
    /// `relink` to that note. Medium/low never land here (see `.recommend`).
    case didYouMean(SuggestTargetResult)
    /// Search / LLM found plausible notes but none is high-confidence. Top 2-3
    /// candidates for a human pick (inline on the list + the Fix link sheet).
    case recommend([SuggestTargetResult])
    /// The CLI's verdict recommends REMOVING the link (nothing >= medium
    /// matches, or the model explicitly declined every candidate). The
    /// weak candidates, if any, ride along so the sheet can still offer them.
    /// Removal is never one-click from Fix all — it goes through the
    /// Remove-dead-links sheet or an explicit per-row button.
    case removable(candidates: [SuggestTargetResult])
    /// The target maps to more than one note and no confident candidate stands
    /// out. Needs a pick.
    case ambiguous
    /// The target maps to no note (or the shortlist was empty). Needs a
    /// decision (create or unlink).
    case missing

    var isOneClick: Bool {
        switch self {
        case .repairable, .didYouMean: return true
        case .recommend, .removable, .ambiguous, .missing: return false
        }
    }

    /// Whether the machine recommendation for this finding is removal.
    var isRemovable: Bool {
        if case .removable = self { return true }
        return false
    }

    /// Up to three non-high candidates for inline recommendation UI. Empty for
    /// pure one-click / empty-missing cases.
    var recommendations: [SuggestTargetResult] {
        switch self {
        case .recommend(let cands): return Array(cands.prefix(FixAllPlanner.maxRecommendations))
        case .removable(let cands): return Array(cands.prefix(FixAllPlanner.maxRecommendations))
        case .didYouMean(let c): return [c]
        case .repairable, .ambiguous, .missing: return []
        }
    }

    /// Subtle capsule label next to the finding.
    var badgeText: String {
        switch self {
        case .repairable: return "auto-repairable"
        case .didYouMean: return "high confidence"
        case .recommend: return "top picks"
        case .removable: return "remove?"
        case .ambiguous: return "matches several"
        case .missing: return "no matching note"
        }
    }
}

/// One planned link rewrite in the Fix-all flow: either a deterministic drift
/// repair or a relink to a HIGH-confidence candidate. Pure data so the planner
/// is unit-testable; the sheet renders `[[target]] -> [[chosenDisplay]]`.
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

    /// A subtle kind tag ("Repair" / "High confidence").
    var kindLabel: String {
        switch action {
        case .repair: return "Repair"
        case .relink: return "High confidence"
        }
    }
}

/// Counts of one-click-fixable vs decision-needed vs removal-recommended
/// broken findings, for the class-aware header. `removable` is a subset
/// carved out of the non-one-click findings, not an addition to them.
struct ClassCounts: Equatable {
    let oneClick: Int
    let decision: Int
    let removable: Int
    var total: Int { oneClick + decision + removable }

    init(oneClick: Int, decision: Int, removable: Int = 0) {
        self.oneClick = oneClick
        self.decision = decision
        self.removable = removable
    }
}

/// Pure planning for the class-aware Validation flow: classify each broken
/// finding, build the Fix-all plan, and summarize the counts. No CLI, no view:
/// the suggest-target lookup is injected so the whole thing is unit-testable.
enum FixAllPlanner {

    /// How many candidates the list and sheet surface for a human pick.
    static let maxRecommendations = 3

    /// Classify one broken finding from its lint `fix` field and its ranked
    /// suggest-target candidates. Policy:
    /// - drift fix → always one-click repairable
    /// - top candidate confidence == "high" → one-click did-you-mean
    /// - any non-empty shortlist otherwise → recommend top 2-3 (decision)
    /// - else ambiguous / missing from the lint fix (or missing on old CLI)
    static func classify(
        fix: String?,
        candidates: [SuggestTargetResult],
        driftTarget: String?
    ) -> LinkFixClass {
        if fix == "drift" {
            return .repairable(driftTarget: driftTarget)
        }
        if let top = candidates.first, top.confidence == "high" {
            return .didYouMean(top)
        }
        let recs = Array(candidates.prefix(maxRecommendations))
        if !recs.isEmpty {
            return .recommend(recs)
        }
        if fix == "ambiguous" {
            return .ambiguous
        }
        return .missing
    }

    /// Back-compat wrapper used by older call sites / tests that only pass the
    /// top candidate. Prefer `classify(fix:candidates:driftTarget:)`.
    static func classify(
        fix: String?, topCandidate: SuggestTargetResult?, driftTarget: String?
    ) -> LinkFixClass {
        classify(fix: fix, candidates: topCandidate.map { [$0] } ?? [], driftTarget: driftTarget)
    }

    /// Classify from the CLI's `--verdict` envelope: the recommendation IS the
    /// classification. Policy:
    /// - drift fix → always one-click repairable (no envelope consulted)
    /// - recommendation "unlink" → `.removable` (weak candidates ride along)
    /// - recommendation "relink" at high confidence → one-click did-you-mean
    /// - recommendation "relink" otherwise (medium) → `.recommend` top 2-3
    /// - malformed/unknown action → degrade to the candidate-only classify.
    static func classify(
        fix: String?,
        envelope: SuggestVerdictEnvelope,
        driftTarget: String?
    ) -> LinkFixClass {
        if fix == "drift" {
            return .repairable(driftTarget: driftTarget)
        }
        switch envelope.recommendation.action {
        case "unlink":
            return .removable(candidates: Array(envelope.candidates.prefix(maxRecommendations)))
        case "relink":
            // The CLI orders candidates best-first and recommends the top one,
            // so candidates.first is the recommended note.
            if envelope.recommendation.confidence == "high", let top = envelope.candidates.first {
                return .didYouMean(top)
            }
            let recs = Array(envelope.candidates.prefix(maxRecommendations))
            if !recs.isEmpty {
                return .recommend(recs)
            }
            return fix == "ambiguous" ? .ambiguous : .missing
        default:
            return classify(fix: fix, candidates: envelope.candidates, driftTarget: driftTarget)
        }
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
            case .recommend, .removable, .ambiguous, .missing:
                continue
            }
            if seen.insert(rewrite.id).inserted {
                plans.append(rewrite)
            }
        }
        return plans
    }

    /// The Remove-dead-links plan: the `.removable` findings, deduped by id
    /// (lint emits one finding per occurrence; unlink is whole-file by target).
    /// Mirrors `plan(from:)`'s dedupe so the sheet count matches the button.
    static func removalPlan(from classified: [(BrokenFinding, LinkFixClass)]) -> [BrokenFinding] {
        var seen = Set<String>()
        var out: [BrokenFinding] = []
        for (finding, cls) in classified where cls.isRemovable {
            if seen.insert(finding.id).inserted {
                out.append(finding)
            }
        }
        return out
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
                classified.append((f, classify(fix: f.fix, candidates: cands, driftTarget: f.driftTarget)))
            }
        }
        return plan(from: classified)
    }

    /// One-click vs decision vs removable split over a set of classifications.
    static func counts(_ classes: [LinkFixClass]) -> ClassCounts {
        let oneClick = classes.filter { $0.isOneClick }.count
        let removable = classes.filter { $0.isRemovable }.count
        return ClassCounts(
            oneClick: oneClick,
            decision: classes.count - oneClick - removable,
            removable: removable)
    }

    /// The class-aware header line, e.g.
    /// "7 broken links: 2 one-click fixable, 4 need a decision, 1 removable".
    /// The removable clause is omitted when zero so the common case reads as
    /// before.
    static func headerSummary(total: Int, counts: ClassCounts) -> String {
        let links = "\(total) broken link\(total == 1 ? "" : "s")"
        let decides = counts.decision == 1 ? "needs a decision" : "need a decision"
        var line = "\(links): \(counts.oneClick) one-click fixable, \(counts.decision) \(decides)"
        if counts.removable > 0 {
            line += ", \(counts.removable) removable"
        }
        return line
    }

    /// Whether "Create the note" is a sensible fix for a target. A path-qualified
    /// target (contains "/") names a specific location, not a title to mint, and
    /// the repair index refuses it by design, so hide Create for it.
    static func canCreateNote(forTarget target: String) -> Bool {
        !target.contains("/")
    }

    /// The step-through queue for the "Fix each" walkthrough: the non-one-click
    /// findings — decisions AND removals, everything Fix-all cannot resolve in
    /// one click — in display order, deduped by id. This is exactly the complement of `plan(from:)` over
    /// the same classifications: a finding is in the Fix-all plan or this queue,
    /// never both and never neither. Deduping matches `plan(from:)` (lint emits
    /// one finding per link occurrence, but the sheet resolves a whole file by
    /// target, so a repeated broken target only needs one pass). Pure so the
    /// queue construction is unit-testable.
    static func walkthroughQueue(from classified: [(BrokenFinding, LinkFixClass)]) -> [BrokenFinding] {
        var seen = Set<String>()
        var queue: [BrokenFinding] = []
        for (finding, cls) in classified where !cls.isOneClick {
            if seen.insert(finding.id).inserted {
                queue.append(finding)
            }
        }
        return queue
    }

    /// 1-based progress label for the "Fix each" walkthrough, e.g. "2 of 5".
    /// `index` is the 0-based position of the finding currently shown.
    static func walkthroughProgress(index: Int, total: Int) -> String {
        "\(index + 1) of \(total)"
    }
}
