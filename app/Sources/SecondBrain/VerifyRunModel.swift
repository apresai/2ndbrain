import Foundation
import Observation

/// Live state for one streamed `models verify --events` run: the running
/// counter and the last per-model verdict while in flight.
///
/// Moved out of AIHubView (where it was the nested `AIHubView.VerifyProgress`)
/// so every validate surface shares one shape.
struct VerifyProgress: Equatable {
    var current: Int = 0
    var total: Int = 0
    var lastLine: String = ""
}

/// One shared run-state object for the streamed "Validate models" flow.
///
/// AIHubView and SimpleModelsView each carried a near-duplicate
/// `applyVerifyEvent` plus the same verifying/progress/summary state triple,
/// and the Testing tab's Validate pane would have been the third copy. This
/// model owns that state and the event fold, so every surface renders the
/// same run identically and a stream-contract change lands in exactly one
/// place. Pinned by VerifyRunModelTests against recorded real `--events`
/// sequences BEFORE the views were ported onto it.
@MainActor
@Observable
final class VerifyRunModel {
    /// A run is in flight. Claimed at entry via `beginRun()`, BEFORE the
    /// async cost preview + confirm, so a rapid double-tap cannot launch two
    /// runs (each would otherwise pass a check-then-set guard while the other
    /// awaits the preview).
    private(set) var running = false

    /// Live progress while the stream is open; nil outside a stream,
    /// including during the pre-stream cost preview + confirm.
    private(set) var progress: VerifyProgress?

    /// The just-completed run's summary in the full verify vocabulary
    /// ("7 verified, 3 no access, 1 throttled"), held until the next run
    /// starts streaming. More granular than the persisted `model_access`
    /// line, which folds every non-access-denied failure into "other".
    private(set) var lastSummary: String?

    /// Claims the single-flight slot for one run. False means another run is
    /// already in flight and the caller must not start a second one.
    func beginRun() -> Bool {
        guard !running else { return false }
        running = true
        return true
    }

    /// Releases the slot and clears live progress. The done-event summary
    /// stays for display. Call from a defer so a thrown stream still resets.
    func endRun() {
        running = false
        progress = nil
    }

    /// Opens the progress display just before streaming (after the cost
    /// confirm), seeding the expected total so the counter renders
    /// immediately even if the CLI's own start event is slow to arrive.
    func startStream(expectedTotal: Int) {
        lastSummary = nil
        progress = VerifyProgress(current: 0, total: expectedTotal)
    }

    /// Folds one streamed verify event into the live state.
    func apply(_ event: VerifyEvent) {
        switch event.event {
        case "start":
            progress = VerifyProgress(current: 0, total: event.total ?? 0)
        case "result":
            var p = progress ?? VerifyProgress()
            p.current = event.n ?? p.current
            if let total = event.total { p.total = total }
            if let r = event.result {
                p.lastLine = "\(r.ok ? "PASS" : "FAIL") \(r.modelID)"
            }
            progress = p
        case "done":
            lastSummary = VerifyFlow.summaryText(event.summary)
        default:
            break
        }
    }
}

/// Pure composition of the per-account access summary line ("7 verified,
/// 3 no access, checked 2d ago") from `ai status --json` `model_access`.
/// Extracted from AIHubView so the Testing tab's Validate pane renders the
/// same sentence from the same data.
enum AccessSummary {
    static func line(_ access: ModelAccessSummary?) -> String {
        guard let ma = access else { return "No models validated yet" }
        var parts = ["\(ma.verified) verified"]
        if ma.accessDenied > 0 { parts.append("\(ma.accessDenied) no access") }
        if ma.otherFailures > 0 { parts.append("\(ma.otherFailures) other") }
        var line = parts.joined(separator: ", ")
        if let when = ma.lastVerifiedAt, let rel = Formatters.relativeDateIfParseable(when) {
            line += ", checked \(rel)"
        }
        return line
    }
}
