import Testing
import Foundation
@testable import SecondBrain

// The escalation ladder is pure (CLIWatchdog.verdict) precisely so it can be
// pinned here without spawning processes: below the timeout nothing fires, at
// the timeout SIGTERM, after the grace SIGKILL, and an exited child is never
// signaled at any elapsed time.

@Test("Watchdog waits below the timeout")
func watchdogWaitsBelowTimeout() {
    #expect(CLIWatchdog.verdict(elapsed: 0, isRunning: true) == .wait)
    #expect(CLIWatchdog.verdict(elapsed: CLIWatchdog.timeout - 1, isRunning: true) == .wait)
}

@Test("Watchdog escalates SIGTERM at the timeout, SIGKILL after the grace")
func watchdogEscalates() {
    #expect(CLIWatchdog.verdict(elapsed: CLIWatchdog.timeout, isRunning: true) == .sigterm)
    #expect(CLIWatchdog.verdict(
        elapsed: CLIWatchdog.timeout + CLIWatchdog.killGrace - 1,
        isRunning: true
    ) == .sigterm)
    #expect(CLIWatchdog.verdict(
        elapsed: CLIWatchdog.timeout + CLIWatchdog.killGrace,
        isRunning: true
    ) == .sigkill)
}

@Test("Watchdog never signals an exited child")
func watchdogIgnoresExitedChild() {
    for elapsed: TimeInterval in [0, CLIWatchdog.timeout, CLIWatchdog.timeout + CLIWatchdog.killGrace + 1] {
        #expect(CLIWatchdog.verdict(elapsed: elapsed, isRunning: false) == .wait)
    }
}

@Test("Watchdog constants: 10 minutes, above the realistic cold-model path")
func watchdogConstants() {
    // 10 minutes is the planned absolute bound for request/response helpers.
    #expect(CLIWatchdog.timeout == 600)
    // It must sit above one mantle per-attempt bound (Go
    // ai.MantleAttemptTimeout, 240s) with room for a second sequential probe:
    // the app's request/response calls run at most two probes back to back
    // (Home Test, doctor tier 1), so 2 x 240s must fit under the watchdog or
    // a slow-but-working pair of cold probes would be killed by the app while
    // the CLI is still legitimately waiting.
    let mantleAttemptSeconds: TimeInterval = 240
    #expect(CLIWatchdog.timeout > 2 * mantleAttemptSeconds)
    #expect(CLIWatchdog.killGrace > 0)
}

@Test("Watchdog timer constructs, arms, and cancels without a process launch")
@MainActor
func watchdogTimerLifecycle() {
    // cancel() before arm() (the failed-launch path) and double-cancel must
    // both be safe no-ops; the never-launched Process is never signaled
    // because arm() is what schedules the work items.
    let process = Process()
    let timer = CLIWatchdogTimer(process: process, command: "2nb test") { _ in }
    timer.cancel()
    timer.cancel()
}
