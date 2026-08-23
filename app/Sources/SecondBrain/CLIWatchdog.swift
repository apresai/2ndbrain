import Foundation

/// Absolute wall-clock watchdog for the request/response CLI helpers
/// (`runCLIGlobalRaw` / `runCLIGlobal` / `runCLI` / `runCLIAllowingNonZero`),
/// which previously had no hang guard at all: a wedged `2nb` parked its
/// caller forever.
///
/// The CLI self-bounds every provider call (cli/internal/ai/timeouts.go), so
/// in normal operation the child always exits on its own and this never
/// fires. It exists for the pathological cases those bounds cannot cover: a
/// wedged binary, a stuck filesystem, a suspended child. Ten minutes sits
/// far above the realistic cold-model path (one mantle attempt is bounded at
/// 240s, and the app's request/response calls run at most two sequential
/// probes), so a slow working model is never killed, only a hang. The
/// streaming runners (verify / bench / index) are deliberately NOT under
/// this bound; see their comments in AppState.
///
/// Escalation: SIGTERM at `timeout`, then SIGKILL after `killGrace` for a
/// child that ignores SIGTERM. The escalation ladder is a pure function
/// (`verdict(elapsed:isRunning:)`) so it is unit-testable without spawning
/// processes; `CLIWatchdogTimer` is the runtime arm of the same constants.
enum CLIWatchdog {
    /// Absolute bound on one request/response CLI invocation, wall clock.
    static let timeout: TimeInterval = 600

    /// Grace between SIGTERM (which lets the CLI die cleanly) and SIGKILL
    /// (for a child that ignores it).
    static let killGrace: TimeInterval = 10

    enum Verdict: Equatable {
        case wait
        case sigterm
        case sigkill
    }

    /// Pure escalation policy: what to send a child at `elapsed` seconds.
    /// A child that already exited needs nothing regardless of elapsed time.
    static func verdict(elapsed: TimeInterval, isRunning: Bool) -> Verdict {
        guard isRunning else { return .wait }
        if elapsed >= timeout + killGrace { return .sigkill }
        if elapsed >= timeout { return .sigterm }
        return .wait
    }
}

/// Runtime arm of `CLIWatchdog` for one child process: two scheduled work
/// items (SIGTERM at `timeout`, SIGKILL at `timeout + killGrace`), each
/// re-checking `isRunning` before signaling.
///
/// Lifecycle: construct alongside the `Process`, `arm()` only after
/// `process.run()` succeeded (`terminate()` on a never-launched Process
/// raises), and `cancel()` first thing in the terminationHandler so a normal
/// exit can never race a late signal. `@unchecked Sendable`: the Process is
/// confined to this class after arming, the work items capture self weakly
/// (no retain cycle), and the item bookkeeping is lock-guarded.
final class CLIWatchdogTimer: @unchecked Sendable {
    private let process: Process
    private let command: String
    private let onEvent: @Sendable (String) -> Void
    private let lock = NSLock()
    private var termItem: DispatchWorkItem?
    private var killItem: DispatchWorkItem?

    /// - Parameter onEvent: called (off the main actor) with a one-line
    ///   description when a signal is actually sent, so the kill is
    ///   attributable in the logs instead of surfacing as a bare non-zero
    ///   exit with empty stderr.
    init(process: Process, command: String, onEvent: @escaping @Sendable (String) -> Void) {
        self.process = process
        self.command = command
        self.onEvent = onEvent
    }

    /// Starts the wall clock. Call only after `process.run()` succeeded.
    func arm(queue: DispatchQueue = .global(qos: .utility)) {
        lock.lock()
        defer { lock.unlock() }
        guard termItem == nil else { return }
        let term = DispatchWorkItem { [weak self] in self?.fire(.sigterm) }
        let kill = DispatchWorkItem { [weak self] in self?.fire(.sigkill) }
        termItem = term
        killItem = kill
        queue.asyncAfter(deadline: .now() + CLIWatchdog.timeout, execute: term)
        queue.asyncAfter(deadline: .now() + CLIWatchdog.timeout + CLIWatchdog.killGrace, execute: kill)
    }

    /// Call from the terminationHandler: a finished child needs no signal.
    /// Safe to call before `arm()` (a failed launch) and more than once.
    func cancel() {
        lock.lock()
        defer { lock.unlock() }
        termItem?.cancel()
        killItem?.cancel()
    }

    private func fire(_ verdict: CLIWatchdog.Verdict) {
        // Re-check liveness at fire time: cancel() in the terminationHandler
        // covers the normal path, this guard covers the exit-vs-fire race.
        guard process.isRunning else { return }
        switch verdict {
        case .sigterm:
            onEvent("CLI watchdog: \(command) still running after \(Int(CLIWatchdog.timeout))s; sending SIGTERM")
            process.terminate()
        case .sigkill:
            onEvent("CLI watchdog: \(command) ignored SIGTERM for \(Int(CLIWatchdog.killGrace))s; sending SIGKILL")
            kill(process.processIdentifier, SIGKILL)
        case .wait:
            break
        }
    }
}
