import Foundation
import Testing
@testable import SecondBrain

// Pure tests for the CLI-backed nudge source: the session accumulation of
// the one-shot server-side NEW/GONE diff (DiscoverDiffSession), the
// source-selection gate (DiscoveryNudge.nudgeSource), and the CLI-backed
// banner set (DiscoveryNudge.cliBackedNewIDs).

private func report(_ json: String) -> DiscoverReportInfo {
    try! JSONDecoder().decode(DiscoverReportInfo.self, from: Data(json.utf8))
}

private func model(_ json: String) -> CatalogModelInfo {
    try! JSONDecoder().decode(CatalogModelInfo.self, from: Data(json.utf8))
}

private let poolV1 = #"{"id": "fake.v1", "name": "", "provider": "bedrock", "type": "generation"}"#
private let poolV2 = #"{"id": "fake.v2", "name": "", "provider": "bedrock", "type": "generation"}"#

private func envelope(models: [String], new: [String], gone: [String] = [], firstRun: Bool = false) -> DiscoverReportInfo {
    let modelsJSON = models.joined(separator: ",")
    let newJSON = new.joined(separator: ",")
    let goneJSON = gone.map { "\"\($0)\"" }.joined(separator: ",")
    return report(#"{"sources": [], "models": [\#(modelsJSON)], "new": [\#(newJSON)], "gone": [\#(goneJSON)]\#(firstRun ? #", "first_run": true"# : "")}"#)
}

// MARK: - DiscoverDiffSession.merged

@Test("a first-run report (silent seed, empty new) badges nothing")
func firstRunSeedsSilently() {
    let state = DiscoverDiffSession.merged(.init(), report: envelope(models: [poolV1, poolV2], new: [], firstRun: true))
    #expect(state.newKeys.isEmpty)
    #expect(state.goneKeys.isEmpty)
}

@Test("a NEW arrival enters the session and survives later steady-state runs")
func newArrivalSurvivesSteadyState() {
    var state = DiscoverDiffSession.merged(.init(), report: envelope(models: [poolV1, poolV2], new: [poolV2]))
    #expect(state.newKeys == ["bedrock|fake.v2"])
    // The next run's diff is empty (the CLI baseline advanced server-side),
    // but the model is still in the pool: the session keeps it badged until
    // the user acts, instead of a reload silently eating the banner.
    state = DiscoverDiffSession.merged(state, report: envelope(models: [poolV1, poolV2], new: []))
    #expect(state.newKeys == ["bedrock|fake.v2"])
}

@Test("a delisted session-NEW key moves to GONE; a returning model clears GONE")
func delistedMovesToGone() {
    var state = DiscoverDiffSession.merged(.init(), report: envelope(models: [poolV1, poolV2], new: [poolV2]))
    // v2 delisted: the same run reports it GONE and drops it from the pool.
    state = DiscoverDiffSession.merged(state, report: envelope(models: [poolV1], new: [], gone: ["bedrock|fake.v2"]))
    #expect(state.newKeys.isEmpty)
    #expect(state.goneKeys == ["bedrock|fake.v2"])
    // GONE accumulates across runs (the server reports it exactly once)...
    state = DiscoverDiffSession.merged(state, report: envelope(models: [poolV1], new: []))
    #expect(state.goneKeys == ["bedrock|fake.v2"])
    // ...until the model returns to the pool.
    state = DiscoverDiffSession.merged(state, report: envelope(models: [poolV1, poolV2], new: [poolV2]))
    #expect(state.goneKeys.isEmpty)
    #expect(state.newKeys == ["bedrock|fake.v2"])
}

@Test("afterAdd drops the added id immediately (the add run's pool still lists it)")
func afterAddDropsImmediately() {
    var state = DiscoverDiffSession.merged(.init(), report: envelope(models: [poolV1, poolV2], new: [poolV2]))
    // The --add envelope computes its pool BEFORE persisting the add, so v2
    // is still listed; the pool-intersection rule alone would keep the badge
    // one run too long.
    state = DiscoverDiffSession.merged(state, report: envelope(models: [poolV1, poolV2], new: []))
    state = DiscoverDiffSession.afterAdd(state, addedIDs: ["fake.v2"])
    #expect(state.newKeys.isEmpty)
}

@Test("clearingNew clears only the named provider's keys")
func clearingNewIsPerProvider() {
    let state = DiscoverDiffSession.State(
        newKeys: ["bedrock|fake.v2", "openrouter|meta/llama-4"],
        goneKeys: ["bedrock|old.model"]
    )
    let next = DiscoverDiffSession.clearingNew(state, provider: "bedrock")
    #expect(next.newKeys == ["openrouter|meta/llama-4"])
    #expect(next.goneKeys == ["bedrock|old.model"])
}

@Test("newRows resolves session keys against the pool, sorted by id")
func newRowsResolveAndSort() {
    let rep = envelope(models: [poolV2, poolV1], new: [])
    let state = DiscoverDiffSession.State(newKeys: ["bedrock|fake.v1", "bedrock|fake.v2"])
    #expect(DiscoverDiffSession.newRows(report: rep, state: state).map(\.modelID) == ["fake.v1", "fake.v2"])
    // A session key no longer in the pool renders nothing (self-cleans).
    let stale = DiscoverDiffSession.State(newKeys: ["bedrock|fake.v3"])
    #expect(DiscoverDiffSession.newRows(report: rep, state: stale).isEmpty)
}

// MARK: - Nudge source selection

@Test("the CLI diff is the source only once support is PROVEN; nil and false fall back")
func nudgeSourceSelection() {
    #expect(DiscoveryNudge.nudgeSource(cliSupported: true) == .cliBacked)
    // Unprobed (nil) must fall back, not show nothing: the capability is
    // settled by shape on the first successful run, and until then the
    // snapshot path keeps working exactly as on an older CLI.
    #expect(DiscoveryNudge.nudgeSource(cliSupported: nil) == .snapshot)
    #expect(DiscoveryNudge.nudgeSource(cliSupported: false) == .snapshot)
}

// MARK: - CLI-backed banner set

@Test("cliBackedNewIDs badges exactly the session-NEW, probeable-and-enabled models")
func cliBackedNewIDsFilters() {
    let newModel = model(#"{"id": "fake.v2", "name": "", "provider": "bedrock", "type": "generation"}"#)
    let known = model(#"{"id": "fake.v1", "name": "", "provider": "bedrock", "type": "generation"}"#)
    let ids = DiscoveryNudge.cliBackedNewIDs(
        models: [known, newModel],
        provider: "bedrock",
        sessionNewKeys: ["bedrock|fake.v2"],
        seen: nil
    )
    #expect(ids == ["fake.v2"])
}

@Test("cliBackedNewIDs applies the same predicate as Validate's candidate set")
func cliBackedNewIDsPredicate() {
    let disabled = model(#"{"id": "zai.glm-5", "name": "", "provider": "bedrock", "type": "generation", "enabled": false}"#)
    let rerank = model(#"{"id": "cohere.rerank-v3-5:0", "name": "", "provider": "bedrock", "type": "rerank"}"#)
    let incompatible = model(#"{"id": "stability.image", "name": "", "provider": "bedrock", "type": "generation", "compatible": false}"#)
    let other = model(#"{"id": "meta/llama-4", "name": "", "provider": "openrouter", "type": "generation"}"#)
    let keys: Set<String> = ["bedrock|zai.glm-5", "bedrock|cohere.rerank-v3-5:0", "bedrock|stability.image", "openrouter|meta/llama-4"]
    #expect(DiscoveryNudge.cliBackedNewIDs(
        models: [disabled, rerank, incompatible, other],
        provider: "bedrock",
        sessionNewKeys: keys,
        seen: nil
    ).isEmpty)
}

@Test("a model dismissed under the snapshot era is never re-announced by the CLI source")
func cliBackedNewIDsHonorsSnapshot() {
    // Migration: the old snapshot-based banner recorded fake.v2 as seen
    // (Dismiss records the full catalog). After a CLI upgrade the server
    // baseline may still list it NEW (the terminal never ran discover), but
    // the GUI must not re-announce it.
    let newModel = model(#"{"id": "fake.v2", "name": "", "provider": "bedrock", "type": "generation"}"#)
    let ids = DiscoveryNudge.cliBackedNewIDs(
        models: [newModel],
        provider: "bedrock",
        sessionNewKeys: ["bedrock|fake.v2"],
        seen: ["bedrock|fake.v2"]
    )
    #expect(ids.isEmpty)
}

@Test("first CLI-backed run end-to-end: a seeded pool never reaches the banner")
func firstCLIRunNeverBadges() {
    // The CLI's first run seeds its baseline silently: new is empty even
    // though the pool is not. Folding that report into a fresh session and
    // computing the banner set must produce nothing.
    let rep = envelope(models: [poolV1, poolV2], new: [], firstRun: true)
    let state = DiscoverDiffSession.merged(.init(), report: rep)
    let ids = DiscoveryNudge.cliBackedNewIDs(
        models: rep.models,
        provider: "bedrock",
        sessionNewKeys: state.newKeys,
        seen: nil
    )
    #expect(ids.isEmpty)
}
