import Foundation
import Testing
@testable import SecondBrain

// BenchRunModel is the shared single-flight claim for `models bench` runs.
// It exists because the claim used to be a per-view @State flag that died
// with the Benchmarks pane: switching Testing sections tore the pane down
// while the run's Task kept streaming, and a fresh pane could start a second
// concurrent bench. These tests pin the claim/release semantics the views
// rely on (claim BEFORE the first await, release in a defer).

@Test("beginRun claims the slot exactly once")
@MainActor
func benchRunClaimsOnce() {
    let model = BenchRunModel()
    #expect(!model.running)
    #expect(model.beginRun(), "an idle model must grant the first claim")
    #expect(model.running)
    #expect(!model.beginRun(), "a second claim while a run is in flight must be refused; granting it is the double-run the shared model exists to prevent")
    #expect(model.running, "a refused claim must not clobber the in-flight run's flag")
}

@Test("endRun releases the slot for the next run")
@MainActor
func benchRunReleases() {
    let model = BenchRunModel()
    #expect(model.beginRun())
    model.endRun()
    #expect(!model.running)
    #expect(model.beginRun(), "after endRun the next run must be claimable, or one thrown stream would brick benchmarks until relaunch")
    model.endRun()
}

@Test("endRun on an idle model is a safe no-op")
@MainActor
func benchRunIdleEndIsSafe() {
    // The defer-based release can fire on paths that returned before any
    // work happened; releasing an idle model must not trap or wedge state.
    let model = BenchRunModel()
    model.endRun()
    #expect(!model.running)
    #expect(model.beginRun())
}
