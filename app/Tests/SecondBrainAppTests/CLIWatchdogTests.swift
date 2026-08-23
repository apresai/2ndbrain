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

@Test("Watchdog constants strictly contain the mirrored CLI ceilings")
func watchdogConstants() {
    // The watchdog must STRICTLY CONTAIN the CLI's own longest documented
    // deadlines for the commands these helpers run, or the app reproduces
    // the outer-shorter-than-inner inversion one layer up (a working cold
    // probe killed by its caller). The mirrors carry the Go derivations:
    // MaxProbeDeadline 753s (3 x 240s + backoff + 30s slack, one models-test
    // probe) and doctorModelTierTimeout 1536s (doctor tier 1's two
    // SEQUENTIAL probes: 2 x 753s + 30s). If a Go-side raise outgrows the
    // watchdog, this is the test that fails instead of the user's probe.
    #expect(CLIWatchdog.cliMaxProbeDeadlineMirror == 753,
            "mirror drifted from cli/internal/ai/timeouts.go MaxProbeDeadline (3x240s + 3s backoff + 30s slack)")
    #expect(CLIWatchdog.cliDoctorTierMirror == 2 * CLIWatchdog.cliMaxProbeDeadlineMirror + 30,
            "mirror drifted from doctor_cmd.go doctorModelTierTimeout (2 x MaxProbeDeadline + 30s)")
    #expect(CLIWatchdog.timeout > CLIWatchdog.cliMaxProbeDeadlineMirror)
    #expect(CLIWatchdog.timeout > CLIWatchdog.cliDoctorTierMirror)
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
