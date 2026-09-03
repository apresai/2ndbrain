import Foundation
import Testing
@testable import SecondBrain

// The watcher's two `index --doc` call sites used to catch a failure and
// log.debug it, so a note whose frontmatter broke on save silently dropped out
// of search until the next full index reported it. These pin the state
// transition, with no window and no CLI: set on failure, cleared only when THAT
// path indexes cleanly.

@MainActor
@Test("a failed index --doc is recorded with its path and the CLI's reason")
func indexingProblemIsRecorded() {
    let state = AppState()
    #expect(state.lastIndexingProblem == nil)

    state.noteIndexingProblem(path: "notes/broken.md", message: "  malformed YAML frontmatter  ")
    #expect(state.lastIndexingProblem?.path == "notes/broken.md")
    // Trimmed: the CLI's stderr arrives with surrounding whitespace.
    #expect(state.lastIndexingProblem?.message == "malformed YAML frontmatter")
}

@MainActor
@Test("the problem clears when the same note indexes cleanly")
func indexingProblemClearsForTheSamePath() {
    let state = AppState()
    state.noteIndexingProblem(path: "notes/broken.md", message: "malformed YAML frontmatter")
    state.clearIndexingProblem(for: "notes/broken.md")
    #expect(state.lastIndexingProblem == nil)
}

@MainActor
@Test("a different note succeeding does not hide an unfixed failure")
func indexingProblemSurvivesAnotherPathSucceeding() {
    let state = AppState()
    state.noteIndexingProblem(path: "notes/broken.md", message: "malformed YAML frontmatter")
    state.clearIndexingProblem(for: "notes/other.md")
    #expect(state.lastIndexingProblem?.path == "notes/broken.md")
}

@MainActor
@Test("a later failure replaces the earlier one")
func indexingProblemReplacedByALaterFailure() {
    let state = AppState()
    state.noteIndexingProblem(path: "notes/first.md", message: "first reason")
    state.noteIndexingProblem(path: "notes/second.md", message: "second reason")
    #expect(state.lastIndexingProblem == IndexingProblem(path: "notes/second.md", message: "second reason"))
}

// The banner outlived its cause. `clearIndexingProblem(for:)` is path-scoped and
// fires only from the single-note watcher, so a full rebuild (the Rebuild button
// and the startup sync) never cleared it, and a vault switch carried a problem
// naming a path from the vault just closed.

/// A clean vault's envelope: both lists present and empty, which `index --json`
/// guarantees.
private let cleanIndexEnvelope = """
{"files_scanned":2,"docs_indexed":2,"chunks_created":3,"links_found":1,\
"errors":0,"excluded_purged":0,"embedded":2,"embed_failed":0,\
"embed_skipped":0,"embed_retries":0,"unparseable":[],"unreadable":[]}
"""

@MainActor
@Test("a clean full index clears a problem the single-note watcher recorded")
func indexingProblemClearsAfterASuccessfulFullIndex() {
    let state = AppState()
    state.noteIndexingProblem(path: "notes/broken.md", message: "malformed YAML frontmatter")
    state.applyFullIndexOutcome(AppState.parseFullIndexSummary(cleanIndexEnvelope))
    #expect(state.lastIndexingProblem == nil)
}

// Exit 0 stopped meaning "nothing was wrong" the moment an unparseable note
// became non-fatal AND had its index row dropped. Clearing the banner on exit 0
// alone made Rebuild, and the startup sync on every launch, go quiet about a
// note that had just vanished from search.

@MainActor
@Test("a full index that skipped an unparseable note reports it instead of clearing")
func fullIndexReportsAnUnparseableNote() {
    let envelope = """
    {"files_scanned":2,"docs_indexed":1,"chunks_created":1,"links_found":0,\
    "errors":1,"excluded_purged":0,"embedded":1,"embed_failed":0,\
    "embed_skipped":0,"embed_retries":0,\
    "unparseable":[{"path":"broken.md","error":"malformed YAML frontmatter"}],\
    "unreadable":[]}
    """
    let state = AppState()
    state.applyFullIndexOutcome(AppState.parseFullIndexSummary(envelope))
    #expect(state.lastIndexingProblem?.path == "broken.md")
    #expect(state.lastIndexingProblem?.message == "malformed YAML frontmatter")
}

@MainActor
@Test("a full index that could not read a note says its entry was kept")
func fullIndexReportsAnUnreadableNote() {
    let envelope = """
    {"docs_indexed":1,"chunks_created":1,"links_found":0,\
    "unparseable":[],\
    "unreadable":[{"path":"locked.md","error":"permission denied"}]}
    """
    let state = AppState()
    state.applyFullIndexOutcome(AppState.parseFullIndexSummary(envelope))
    #expect(state.lastIndexingProblem?.path == "locked.md")
    // The two categories have different remedies, so the message says which.
    #expect(state.lastIndexingProblem?.message.contains("could not be read") == true)
    #expect(state.lastIndexingProblem?.message.contains("existing index entry was kept") == true)
}

@MainActor
@Test("more than one skipped note is counted, not hidden behind the first")
func fullIndexCountsEverySkippedNote() {
    let envelope = """
    {"docs_indexed":1,"chunks_created":1,"links_found":0,\
    "unparseable":[{"path":"a.md","error":"bad"},{"path":"b.md","error":"bad"}],\
    "unreadable":[{"path":"c.md","error":"permission denied"}]}
    """
    let state = AppState()
    state.applyFullIndexOutcome(AppState.parseFullIndexSummary(envelope))
    #expect(state.lastIndexingProblem?.path == "a.md")
    #expect(state.lastIndexingProblem?.message.contains("and 2 more") == true)
}

@MainActor
@Test("an envelope that does not decode keeps the existing report rather than dismissing it")
func fullIndexWithNoEnvelopeKeepsTheProblem() {
    let state = AppState()
    state.noteIndexingProblem(path: "notes/broken.md", message: "malformed YAML frontmatter")
    // What the old prose summary looked like, and what an older CLI on PATH
    // could still print: no evidence either way, so nothing is dismissed.
    state.applyFullIndexOutcome(AppState.parseFullIndexSummary("Indexed 2 files, 3 chunks, 1 links\n"))
    #expect(state.lastIndexingProblem?.path == "notes/broken.md")
}

@MainActor
@Test("the progress counters come from the envelope, not from a scrape")
func fullIndexCountersComeFromTheEnvelope() {
    let summary = AppState.parseFullIndexSummary(cleanIndexEnvelope)
    #expect(summary?.docsIndexed == 2)
    #expect(summary?.chunksCreated == 3)
    #expect(summary?.linksFound == 1)
    // The prose the regex used to scrape is not an envelope.
    #expect(AppState.parseFullIndexSummary("Indexed 2 files, 3 chunks, 1 links\n") == nil)
}

@MainActor
@Test("switching vaults drops a problem naming a note in the vault left behind")
func indexingProblemDoesNotSurviveAVaultSwitch() throws {
    let state = AppState()
    state.noteIndexingProblem(path: "notes/broken.md", message: "malformed YAML frontmatter")

    let other = URL(fileURLWithPath: NSTemporaryDirectory())
        .appendingPathComponent("sb-vault-switch-\(UUID().uuidString)", isDirectory: true)
    try FileManager.default.createDirectory(at: other, withIntermediateDirectories: true)
    defer { try? FileManager.default.removeItem(at: other) }

    state.openVault(at: other)
    #expect(state.lastIndexingProblem == nil)
}
