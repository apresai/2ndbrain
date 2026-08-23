import Foundation
import Observation

/// Single-flight claim for one `models bench` run, shared across pane
/// recreations of the Testing tab's Benchmarks section.
///
/// The claim used to be a per-view `@State var running` inside
/// TestingBenchmarksView, which dies with the pane: switching Testing
/// sections tears the pane down while its unstructured run Task keeps
/// streaming the CLI, and a fresh pane's `running` starts false, so a second
/// concurrent `models bench` could be started against the same bench.db.
/// Hoisting the flag here (owned by ContentView and passed in, the same
/// ownership pattern as the Testing section selection and VerifyRunModel's
/// per-surface state) makes the claim survive pane recreation: the new pane
/// sees the in-flight run and refuses a second one.
///
/// Mirrors VerifyRunModel's begin/end contract: callers claim via
/// `beginRun()` BEFORE their first await (the cost preview shells the CLI,
/// and a check-then-set across an await is exactly the race this closes) and
/// release via `endRun()` in a defer so a thrown stream still resets.
@MainActor
@Observable
final class BenchRunModel {
    /// A bench run is in flight somewhere (this pane or a torn-down
    /// predecessor whose Task is still streaming).
    private(set) var running = false

    /// Claims the single-flight slot for one run. False means another run is
    /// already in flight and the caller must not start a second one.
    func beginRun() -> Bool {
        guard !running else { return false }
        running = true
        return true
    }

    /// Releases the slot. Call from a defer so a thrown stream still resets.
    func endRun() {
        running = false
    }
}
