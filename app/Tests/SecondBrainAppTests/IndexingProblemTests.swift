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

// The external-reindex debounce is AppState-wide, not vault-scoped. A change
// queued against the vault being closed would drain 1.5s later against the one
// just opened, and `relativePath(for:)` falls back to `lastPathComponent` for a
// path outside its root, so that drain could reindex a same-named note in the
// wrong vault or record a problem naming a path the new vault does not have.

@MainActor
@Test("switching vaults drops a reindex queued against the vault being left")
func vaultSwitchCancelsThePendingExternalReindex() throws {
    let fm = FileManager.default
    let first = URL(fileURLWithPath: NSTemporaryDirectory())
        .appendingPathComponent("sb-debounce-a-\(UUID().uuidString)", isDirectory: true)
    let second = URL(fileURLWithPath: NSTemporaryDirectory())
        .appendingPathComponent("sb-debounce-b-\(UUID().uuidString)", isDirectory: true)
    try fm.createDirectory(at: first, withIntermediateDirectories: true)
    try fm.createDirectory(at: second, withIntermediateDirectories: true)
    defer {
        try? fm.removeItem(at: first)
        try? fm.removeItem(at: second)
    }
    // A real file, because scheduleExternalReindex ignores a path that is gone.
    let note = first.appendingPathComponent("note.md")
    try "---\ntitle: Note\n---\n\nbody\n".write(to: note, atomically: true, encoding: .utf8)

    let state = AppState()
    state.openVault(at: first)
    state.scheduleExternalReindex(path: note.path)
    #expect(state.pendingExternalPaths.contains(note.path))
    #expect(state.externalReindexTask != nil)

    // Switch before the 1.5s debounce fires: no timing race, and the queue must
    // not survive into the new vault.
    state.openVault(at: second)
    #expect(state.pendingExternalPaths.isEmpty)
    #expect(state.externalReindexTask == nil)
}

@MainActor
@Test("suppressExternalReindex blocks FSEvents paths across /var vs /private/var symlink variants and evicts queued self-writes")
func suppressExternalReindexCoordinatesSelfWrites() throws {
    let fm = FileManager.default
    let root = URL(fileURLWithPath: NSTemporaryDirectory())
        .appendingPathComponent("sb-suppress-\(UUID().uuidString)", isDirectory: true)
    let otherVault = URL(fileURLWithPath: NSTemporaryDirectory())
        .appendingPathComponent("sb-suppress-other-\(UUID().uuidString)", isDirectory: true)
    try fm.createDirectory(at: root, withIntermediateDirectories: true)
    try fm.createDirectory(at: otherVault, withIntermediateDirectories: true)
    defer {
        try? fm.removeItem(at: root)
        try? fm.removeItem(at: otherVault)
    }

    let selfNote = root.appendingPathComponent("self.md")
    let externalNote = root.appendingPathComponent("external.md")
    try "---\ntitle: Self\n---\n\nbody\n".write(to: selfNote, atomically: true, encoding: .utf8)
    try "---\ntitle: External\n---\n\nbody\n".write(to: externalNote, atomically: true, encoding: .utf8)

    let state = AppState()
    state.openVault(at: root)

    // Pre-existing queued path (e.g. from /var/folders vs /private/var/folders)
    let canonicalSelf = AppState.canonicalWatcherPath(selfNote.path)
    state.scheduleExternalReindex(path: selfNote.path)
    state.scheduleExternalReindex(path: externalNote.path)
    #expect(state.pendingExternalPaths.contains(selfNote.path))
    #expect(state.pendingExternalPaths.contains(externalNote.path))

    // Suppressing by relative path evicts selfNote from pendingExternalPaths while preserving externalNote
    let baseTime = Date(timeIntervalSince1970: 2_000_000_000)
    state.suppressExternalReindex(paths: ["self.md"], duration: 5.0, now: baseTime)
    #expect(!state.pendingExternalPaths.contains(selfNote.path))
    #expect(state.pendingExternalPaths.contains(externalNote.path))
    #expect(state.externalReindexTask != nil)

    // Canonical symlink variant is still suppressed within TTL
    state.scheduleExternalReindex(path: canonicalSelf, now: baseTime.addingTimeInterval(2.0))
    #expect(!state.pendingExternalPaths.contains(canonicalSelf))

    // Once TTL expires, external watcher events queue normally again
    state.scheduleExternalReindex(path: canonicalSelf, now: baseTime.addingTimeInterval(6.0))
    #expect(state.pendingExternalPaths.contains(canonicalSelf))

    // Evicting the last pending path cancels externalReindexTask
    state.suppressExternalReindex(paths: ["self.md", "external.md"], duration: 5.0, now: baseTime.addingTimeInterval(10.0))
    #expect(state.pendingExternalPaths.isEmpty)
    #expect(state.externalReindexTask == nil)

    // Switching vaults clears suppressedExternalPaths
    state.openVault(at: otherVault)
    #expect(state.suppressedExternalPaths.isEmpty)
}

@MainActor
@Test("suppressExternalReindex with clampExisting replaces a 60s pre-write lease with a short post-write TTL")
func suppressExternalReindexClampExisting() throws {
    let fm = FileManager.default
    let root = URL(fileURLWithPath: NSTemporaryDirectory())
        .appendingPathComponent("sb-suppress-clamp-\(UUID().uuidString)", isDirectory: true)
    try fm.createDirectory(at: root, withIntermediateDirectories: true)
    defer { try? fm.removeItem(at: root) }

    let note = root.appendingPathComponent("note.md")
    try "---\ntitle: Note\n---\n\nbody\n".write(to: note, atomically: true, encoding: .utf8)

    let state = AppState()
    state.openVault(at: root)

    let t0 = Date(timeIntervalSince1970: 2_000_000_000)
    // Pre-write lease: 60s
    state.suppressExternalReindex(paths: ["note.md"], duration: 60.0, now: t0)
    // Post-write clamp at t0 + 2s: clamps down to t0 + 7s (5s after write completion)
    state.suppressExternalReindex(paths: ["note.md"], duration: 5.0, now: t0.addingTimeInterval(2.0), clampExisting: true)

    // Still suppressed 2s after write (t0 + 4s)
    state.scheduleExternalReindex(path: note.path, now: t0.addingTimeInterval(4.0))
    #expect(!state.pendingExternalPaths.contains(note.path))

    // Unsuppressed 6s after write (t0 + 8s), well inside original 60s window
    state.scheduleExternalReindex(path: note.path, now: t0.addingTimeInterval(8.0))
    #expect(state.pendingExternalPaths.contains(note.path))
}

@MainActor
@Test("scheduleExternalReindex ignores hidden paths, template folders, and placeholder templates")
func scheduleExternalReindexIgnoresHiddenAndTemplatePaths() throws {
    let fm = FileManager.default
    let root = URL(fileURLWithPath: NSTemporaryDirectory())
        .appendingPathComponent("sb-exclude-reindex-\(UUID().uuidString)", isDirectory: true)
    let obsidianDir = root.appendingPathComponent(".obsidian", isDirectory: true)
    let templatesDir = root.appendingPathComponent("Templates", isDirectory: true)
    let notesDir = root.appendingPathComponent("notes", isDirectory: true)
    try fm.createDirectory(at: obsidianDir, withIntermediateDirectories: true)
    try fm.createDirectory(at: templatesDir, withIntermediateDirectories: true)
    try fm.createDirectory(at: notesDir, withIntermediateDirectories: true)
    defer { try? fm.removeItem(at: root) }

    try "{\"folder\":\"Templates\"}".write(
        to: obsidianDir.appendingPathComponent("templates.json"),
        atomically: true,
        encoding: .utf8
    )

    let hiddenObsidian = obsidianDir.appendingPathComponent("README.md")
    let templateInFolder = templatesDir.appendingPathComponent("daily.md")
    let standalonePlaceholder = notesDir.appendingPathComponent("tmpl.md")
    let regularNote = notesDir.appendingPathComponent("regular.md")

    try "# Plugin README\n".write(to: hiddenObsidian, atomically: true, encoding: .utf8)
    try "---\ntitle: Daily\n---\n\nBody\n".write(to: templateInFolder, atomically: true, encoding: .utf8)
    try "---\r\ntitle: {{title}}\r\ndate: {{date}}\r\n---\r\n\r\nBody\r\n".write(to: standalonePlaceholder, atomically: true, encoding: .utf8)
    try "---\ntitle: Regular Note\n---\n\nBody\n".write(to: regularNote, atomically: true, encoding: .utf8)

    let state = AppState()
    state.openVault(at: root)
    defer { state.externalReindexTask?.cancel() }

    state.scheduleExternalReindex(path: hiddenObsidian.path)
    state.scheduleExternalReindex(path: templateInFolder.path)
    state.scheduleExternalReindex(path: standalonePlaceholder.path)
    #expect(state.pendingExternalPaths.isEmpty)
    #expect(state.externalReindexTask == nil)

    state.scheduleExternalReindex(path: regularNote.path)
    #expect(state.pendingExternalPaths == [regularNote.path])
    #expect(state.externalReindexTask != nil)
}
