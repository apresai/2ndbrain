import Foundation
import Testing
@testable import SecondBrain

// VerifyRunModel is the one shared fold for the `models verify --events`
// stream: AIHubView and SimpleModelsView each carried a near-duplicate
// `applyVerifyEvent`, and the Testing tab would have been the third copy.
// These tests run BEFORE the views were ported onto the model and pin its
// behavior against recorded real event sequences (the NDJSON shapes captured
// from live `--events` runs, the same lines ModelVerifyDecodeTests pins the
// decoder with), so the refactor cannot silently change what a run renders.

/// Decodes one recorded NDJSON line exactly the way the streaming layer does.
private func event(_ line: String) throws -> VerifyEvent {
    try JSONDecoder().decode(VerifyEvent.self, from: Data(line.utf8))
}

/// A recorded full run: start → three results (pass, classified fail, pass)
/// → done with the summary map.
private let recordedRun: [String] = [
    #"{"event":"start","total":3,"estimated_usd":0.0012}"#,
    #"{"event":"result","n":1,"total":3,"result":{"model_id":"us.anthropic.claude-haiku-4-5-20251001-v1:0","provider":"bedrock","type":"generation","ok":true,"latency":"320ms"}}"#,
    #"{"event":"result","n":2,"total":3,"result":{"model_id":"us.anthropic.claude-opus-4-8","provider":"bedrock","type":"generation","ok":false,"detail":"403","code":"access_denied","remediation":"request access in the AWS console"}}"#,
    #"{"event":"result","n":3,"total":3,"result":{"model_id":"amazon.nova-2-multimodal-embeddings-v1:0","provider":"bedrock","type":"embedding","ok":true,"latency":"180ms"}}"#,
    #"{"event":"done","total":3,"summary":{"ok":2,"access_denied":1},"saved_scope":"vault"}"#,
]

@Test("A recorded full run folds into progress per result and the done summary")
@MainActor
func recordedRunFolds() throws {
    let model = VerifyRunModel()
    #expect(model.beginRun())
    model.startStream(expectedTotal: 3)

    model.apply(try event(recordedRun[0]))
    #expect(model.progress == VerifyProgress(current: 0, total: 3, lastLine: ""))

    model.apply(try event(recordedRun[1]))
    #expect(model.progress?.current == 1)
    #expect(model.progress?.lastLine == "PASS us.anthropic.claude-haiku-4-5-20251001-v1:0")

    model.apply(try event(recordedRun[2]))
    #expect(model.progress?.current == 2)
    #expect(model.progress?.lastLine == "FAIL us.anthropic.claude-opus-4-8")

    model.apply(try event(recordedRun[3]))
    #expect(model.progress?.current == 3)
    #expect(model.progress?.total == 3)

    model.apply(try event(recordedRun[4]))
    #expect(model.lastSummary == "2 verified, 1 no access")

    model.endRun()
    #expect(!model.running)
    #expect(model.progress == nil)
    // The summary survives endRun: it is what the surface shows after the run.
    #expect(model.lastSummary == "2 verified, 1 no access")
}

@Test("startStream seeds the expected total so the counter renders before the CLI's start event")
@MainActor
func startStreamSeedsTotal() {
    let model = VerifyRunModel()
    _ = model.beginRun()
    model.startStream(expectedTotal: 11)
    #expect(model.progress == VerifyProgress(current: 0, total: 11, lastLine: ""))
}

@Test("startStream clears the previous run's summary")
@MainActor
func startStreamClearsSummary() throws {
    let model = VerifyRunModel()
    _ = model.beginRun()
    model.startStream(expectedTotal: 3)
    model.apply(try event(recordedRun[4]))
    model.endRun()
    #expect(model.lastSummary != nil)

    _ = model.beginRun()
    model.startStream(expectedTotal: 2)
    #expect(model.lastSummary == nil)
}

@Test("beginRun is single-flight: a second claim is refused until endRun")
@MainActor
func beginRunSingleFlight() {
    let model = VerifyRunModel()
    #expect(model.beginRun())
    #expect(!model.beginRun())
    model.endRun()
    #expect(model.beginRun())
}

@Test("The zero-candidate run (start total 0, done without summary) reads as no models validated")
@MainActor
func zeroCandidateRun() throws {
    // Recorded stream contract: the zero-candidate done omits `summary`
    // entirely (Go omitempty on an empty map).
    let model = VerifyRunModel()
    _ = model.beginRun()
    model.startStream(expectedTotal: 0)
    model.apply(try event(#"{"event":"start","total":0,"estimated_usd":0}"#))
    model.apply(try event(#"{"event":"done","total":0,"saved_scope":"vault"}"#))
    #expect(model.lastSummary == "No models validated")
}

@Test("A result arriving before any start still creates progress defensively")
@MainActor
func resultBeforeStart() throws {
    let model = VerifyRunModel()
    _ = model.beginRun()
    model.apply(try event(recordedRun[1]))
    #expect(model.progress?.current == 1)
    #expect(model.progress?.total == 3)
    #expect(model.progress?.lastLine == "PASS us.anthropic.claude-haiku-4-5-20251001-v1:0")
}

@Test("A result without n keeps the running counter instead of resetting it")
@MainActor
func resultWithoutNKeepsCounter() throws {
    let model = VerifyRunModel()
    _ = model.beginRun()
    model.startStream(expectedTotal: 2)
    model.apply(try event(recordedRun[1]))
    #expect(model.progress?.current == 1)
    model.apply(try event(#"{"event":"result","result":{"model_id":"m2","provider":"bedrock","type":"generation","ok":true}}"#))
    #expect(model.progress?.current == 1)
    #expect(model.progress?.lastLine == "PASS m2")
}

@Test("Unknown event kinds are ignored (additive stream contract)")
@MainActor
func unknownEventIgnored() throws {
    let model = VerifyRunModel()
    _ = model.beginRun()
    model.startStream(expectedTotal: 3)
    model.apply(try event(#"{"event":"heartbeat","total":9}"#))
    #expect(model.progress == VerifyProgress(current: 0, total: 3, lastLine: ""))
    #expect(model.lastSummary == nil)
}

@Test("The multi-region recorded shapes (regions/region fields) fold unchanged")
@MainActor
func multiRegionShapesFold() throws {
    let model = VerifyRunModel()
    _ = model.beginRun()
    model.startStream(expectedTotal: 2)
    model.apply(try event(#"{"event":"start","total":2,"estimated_usd":0.0002,"regions":["us-east-1","us-west-2"]}"#))
    model.apply(try event(#"{"event":"result","n":1,"total":2,"result":{"model_id":"us.anthropic.claude-sonnet-5","provider":"bedrock","type":"generation","ok":true,"latency":"410ms","region":"us-west-2"}}"#))
    #expect(model.progress?.current == 1)
    #expect(model.progress?.lastLine == "PASS us.anthropic.claude-sonnet-5")
}

// MARK: - AccessSummary (extracted from AIHubView's accessSummaryLine)

@Test("AccessSummary composes verified / no access / other from model_access")
func accessSummaryComposes() {
    let ma = ModelAccessSummary(verified: 7, accessDenied: 3, otherFailures: 1, lastVerifiedAt: nil)
    #expect(AccessSummary.line(ma) == "7 verified, 3 no access, 1 other")
}

@Test("AccessSummary drops zero failure counts")
func accessSummaryDropsZeros() {
    let ma = ModelAccessSummary(verified: 5, accessDenied: 0, otherFailures: 0, lastVerifiedAt: nil)
    #expect(AccessSummary.line(ma) == "5 verified")
}

@Test("AccessSummary appends the checked clause only when the timestamp parses")
func accessSummaryCheckedClause() {
    let parsed = ModelAccessSummary(verified: 2, accessDenied: 0, otherFailures: 0, lastVerifiedAt: "2026-07-01T10:00:00Z")
    #expect(AccessSummary.line(parsed).hasPrefix("2 verified, checked "))
    let garbage = ModelAccessSummary(verified: 2, accessDenied: 0, otherFailures: 0, lastVerifiedAt: "not-a-date")
    #expect(AccessSummary.line(garbage) == "2 verified")
}

@Test("AccessSummary reads a missing summary as never validated")
func accessSummaryAbsent() {
    #expect(AccessSummary.line(nil) == "No models validated yet")
}
