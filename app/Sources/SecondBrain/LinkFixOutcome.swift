import Foundation
import SwiftUI

/// Classifies the `PolishResult` of a link-fix action (repair, relink, unlink)
/// into what the resolution sheet should do next. "Nothing repaired" is not one
/// condition: a `links_skipped` entry for the target means the link still
/// exists but this action could not fix it confidently (keep the sheet open
/// with guidance), while no trace of the target at all means the note changed
/// since the last check (a stale finding: dismiss and re-lint so it
/// disappears). Pure and view-free so every branch is unit-testable.
enum LinkFixOutcome: Equatable {
    /// The action changed a link. Show the success banner, dismiss, re-lint.
    case success
    /// The link still exists but needs the user to pick a different
    /// resolution. Show the message in-sheet as guidance; the sheet stays open.
    case actionable(String)
    /// The link is gone (stale finding). Dismiss, surface the message as an
    /// informational banner, and re-lint so the stale finding disappears.
    case stale(String)

    /// `target` is the authored wikilink target the action was scoped to (the
    /// `T` from `broken wikilink: [[T]]`); `verb` names the action for the
    /// stale message ("repair", "repoint", "unlink").
    static func classify(result: PolishResult, target: String, verb: String) -> LinkFixOutcome {
        if let repaired = result.linksRepaired, !repaired.isEmpty {
            return .success
        }
        // lint emits the target untrimmed while repair-links trims links_skipped
        // raw values, so a hand-authored padded link like [[ Auth Flow ]] would
        // otherwise miss the skip entry and misroute to .stale.
        let needle = target.trimmingCharacters(in: .whitespaces)
        if let skipped = result.linksSkipped?.first(where: { $0.raw == needle }) {
            switch skipped.reason {
            case "no_match":
                return .actionable("No existing note matches [[\(needle)]]. Pick a suggestion below, create it, or unlink.")
            case "ambiguous":
                return .actionable("[[\(needle)]] matches more than one note. Pick the right one below.")
            default:
                // An unrecognized skip reason still means the link was found,
                // so the finding is not stale. Keep the sheet open rather than
                // falsely dismissing it.
                return .actionable("[[\(needle)]] could not be fixed automatically. Pick an option below.")
            }
        }
        // No repair and no skip entry for this target: relink/unlink matched
        // nothing, or repair-links found no such link. The note changed since
        // the lint report was produced.
        return .stale("No [[\(needle)]] link found to \(verb). The note changed since the last check.")
    }

    /// Banner for the bulk drift-repair pass: error tint only when nothing
    /// succeeded; a partial success stays green with the failure count
    /// appended. Pure so the three-way tone selection is unit-testable.
    static func bulkRepairBanner(repaired: Int, failed: Int) -> (tone: BannerTone, message: String) {
        let tone: BannerTone = failed > 0 && repaired == 0 ? .error : .success
        var msg = repaired > 0
            ? "Repaired \(repaired) drift link\(repaired == 1 ? "" : "s")."
            : "No confident drift links to repair — the rest need a per-finding fix."
        if failed > 0 {
            msg += " \(failed) file\(failed == 1 ? "" : "s") couldn’t be processed."
        }
        return (tone, msg)
    }
}

/// Visual tone of the inline result banner: green success, orange
/// informational (a stale finding that was cleaned up), or orange error.
/// Lives beside LinkFixOutcome (not in the view) so tone selection stays
/// unit-testable.
enum BannerTone {
    case success
    case info
    case error

    var icon: String {
        switch self {
        case .success: "checkmark.circle.fill"
        case .info: "info.circle.fill"
        case .error: "exclamationmark.triangle.fill"
        }
    }

    var color: Color {
        switch self {
        case .success: .green
        case .info, .error: .orange
        }
    }
}
