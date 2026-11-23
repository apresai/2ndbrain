import Foundation
import SecondBrainCore
import Testing
@testable import SecondBrain

// Regression tests for work that used to run on (or wedge) the main actor.
// Real processes and real files only; nothing is mocked.

// MARK: - DiffView

/// Rebuilds both sides from a diff, which any correct diff must allow.
private func sides(_ lines: [DiffLine]) -> (old: [String], new: [String]) {
    var old: [String] = []
    var new: [String] = []
    for line in lines {
        switch line.kind {
        case .unchanged: old.append(line.text); new.append(line.text)
        case .removed: old.append(line.text)
        case .added: new.append(line.text)
        }
    }
    return (old, new)
}

/// Length of the longest common subsequence: a minimal diff keeps exactly
/// this many lines unchanged.
private func lcsLength(_ a: [String], _ b: [String]) -> Int {
    var prev = [Int](repeating: 0, count: b.count + 1)
    for x in a {
        var cur = [Int](repeating: 0, count: b.count + 1)
        for (j, y) in b.enumerated() {
            cur[j + 1] = x == y ? prev[j] + 1 : max(prev[j + 1], cur[j])
        }
        prev = cur
    }
    return prev[b.count]
}

@Test("lineDiff is a correct, minimal diff on random inputs")
func lineDiffIsCorrectAndMinimal() {
    var rng = SystemRandomNumberGenerator()
    let alphabet = ["a", "b", "c", "d", ""]
    for _ in 0..<400 {
        let a = (0..<Int.random(in: 0...14, using: &rng)).map { _ in alphabet.randomElement(using: &rng)! }
        let b = (0..<Int.random(in: 0...14, using: &rng)).map { _ in alphabet.randomElement(using: &rng)! }
        let diff = lineDiff(a, b)
        let rebuilt = sides(diff)
        #expect(rebuilt.old == a && rebuilt.new == b, "diff does not reproduce \(a) -> \(b)")
        let kept = diff.filter { $0.kind == .unchanged }.count
        #expect(kept == lcsLength(a, b), "diff of \(a) -> \(b) is not minimal")
    }
}

@Test("lineDiff past maxEdits still reproduces both sides")
func lineDiffFallbackIsCorrect() {
    let a = (0..<300).map { "old \($0)" }
    let b = (0..<300).map { "new \($0)" }
    let diff = lineDiff(["head"] + a + ["tail"], ["head"] + b + ["tail"], maxEdits: 50)
    let rebuilt = sides(diff)
    #expect(rebuilt.old == ["head"] + a + ["tail"])
    #expect(rebuilt.new == ["head"] + b + ["tail"])
    #expect(diff.first?.kind == .unchanged && diff.last?.kind == .unchanged)
}

@Test("lineDiff on a long note with a few edits is fast")
func lineDiffLongNoteIsFast() {
    let original = (0..<5000).map { "line \($0) of a long note" }
    var modified = original
    modified[10] = "edited near the top"
    modified[2500] = "edited in the middle"
    modified.insert("inserted", at: 4000)
    let start = ContinuousClock.now
    let diff = lineDiff(original, modified)
    let elapsed = ContinuousClock.now - start
    #expect(sides(diff).old == original && sides(diff).new == modified)
    // The O(n x m) table took 25M steps and ~200 MB here.
    #expect(elapsed < .seconds(1), "lineDiff took \(elapsed)")
}

// MARK: - Pipe EOF

private func makeScript(_ body: String) throws -> URL {
    let dir = URL(fileURLWithPath: NSTemporaryDirectory())
        .appendingPathComponent("MainThreadStallTests-\(UUID().uuidString)")
    try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
    let script = dir.appendingPathComponent("fake-cli.sh")
    try ("#!/bin/sh\n" + body + "\n").write(to: script, atomically: true, encoding: .utf8)
    try FileManager.default.setAttributes([.posixPermissions: 0o755], ofItemAtPath: script.path)
    return script
}

@Test("Large stdout and stderr arrive complete and in order")
@MainActor
func largeOutputArrivesInOrder() async throws {
    setenv("2NB_TEST", "1", 1)
    let state = AppState()
    // ~1 MB on stdout, interleaved with stderr, so many readability callbacks
    // are in flight when the process exits.
    state.cliExecutablePathOverride = try makeScript("seq 1 150000; seq 1 20000 >&2").path
    let expected = (1...150_000).map(String.init).joined(separator: "\n") + "\n"
    for _ in 0..<5 {
        let out = try await state.runCLI(["noop"], cwd: URL(fileURLWithPath: NSTemporaryDirectory()))
        #expect(String(decoding: out, as: UTF8.self) == expected)
    }
}

// MARK: - Keychain tool

@Test("runSecurity reports the exit status, and a missing item reads as absent, not unknown")
func runSecurityReportsStatus() async throws {
    // A lookup for an item that cannot exist: read-only.
    let outcome = await AppState.runSecurity(
        ["find-generic-password", "-s", "dev.apresai.2ndbrain.test-\(UUID().uuidString)", "-a", "none", "-w"],
        captureStdout: true
    )
    guard case .exited(let status, _) = outcome else {
        Issue.record("expected an exit, got \(outcome)")
        return
    }
    #expect(status == 44)
    #expect(AppState.storedKeyLookup(from: outcome) == .absent)
}

@Test("Only a definite answer is treated as known; a timeout or failure is unknown")
func storedKeyLookupMapping() {
    #expect(AppState.storedKeyLookup(from: .exited(0, Data("k-123\n".utf8))) == .present("k-123"))
    #expect(AppState.storedKeyLookup(from: .exited(44, Data())) == .absent)
    #expect(AppState.storedKeyLookup(from: .exited(51, Data())) == .unknown)
    #expect(AppState.storedKeyLookup(from: .timedOut) == .unknown)
    #expect(AppState.storedKeyLookup(from: .launchFailed) == .unknown)
}

@Test("runSecurity escalates to SIGKILL when a child ignores SIGTERM")
func runSecurityEscalatesToSigkill() async throws {
    let script = try makeScript("trap '' TERM; sleep 30")
    let start = ContinuousClock.now
    let outcome = await AppState.runSecurity(
        [],
        timeout: 0.2,
        killGrace: 0.2,
        executablePath: script.path
    )
    let elapsed = ContinuousClock.now - start
    #expect(outcome == .timedOut)
    #expect(elapsed < .seconds(5), "expected SIGKILL escalation, took \(elapsed)")
}

@Test("NotePolishView.noteExists checks disk and database off the main actor")
func polishNoteExistsChecksDiskAndDB() async throws {
    let root = URL(fileURLWithPath: NSTemporaryDirectory())
        .appendingPathComponent("PolishNoteExists-\(UUID().uuidString)")
    try FileManager.default.createDirectory(at: root, withIntermediateDirectories: true)
    let note = root.appendingPathComponent("present.md")
    try "hello".write(to: note, atomically: true, encoding: .utf8)
    let vault = VaultManager(rootURL: root)

    let found = await Task.detached {
        NotePolishView.noteExists(path: "present.md", vault: vault, database: nil)
    }.value
    let missing = await Task.detached {
        NotePolishView.noteExists(path: "missing.md", vault: vault, database: nil)
    }.value
    #expect(found)
    #expect(!missing)
}

// MARK: - File watcher

@Test("Stopping and dropping watchers while events arrive is safe, and a stopped watcher delivers nothing")
func watcherStopIsSafe() async throws {
    let dir = URL(fileURLWithPath: NSTemporaryDirectory())
        .appendingPathComponent("WatcherTests-\(UUID().uuidString)")
    try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)

    final class Counter: @unchecked Sendable {
        private let lock = NSLock()
        private var n = 0
        func add() { lock.lock(); n += 1; lock.unlock() }
        var value: Int { lock.lock(); defer { lock.unlock() }; return n }
    }

    // Churn: a watcher is replaced while files change under it, the shape
    // of a vault switch. With an unretained context this could touch a
    // freed watcher.
    for i in 0..<40 {
        var watcher: FSEventsWatcher? = FSEventsWatcher(path: dir.path) { _ in }
        watcher?.start()
        try "\(i)".write(to: dir.appendingPathComponent("n\(i).md"), atomically: true, encoding: .utf8)
        try await Task.sleep(for: .milliseconds(5))
        watcher?.stop()
        watcher = nil
    }

    let live = Counter()
    let watcher = FSEventsWatcher(path: dir.path) { _ in live.add() }
    watcher.start()
    try await Task.sleep(for: .milliseconds(300))
    try "x".write(to: dir.appendingPathComponent("live.md"), atomically: true, encoding: .utf8)
    for _ in 0..<50 where live.value == 0 { try await Task.sleep(for: .milliseconds(100)) }
    #expect(live.value > 0, "a running watcher delivered no events")

    watcher.stop()
    let afterStop = live.value
    try "y".write(to: dir.appendingPathComponent("after.md"), atomically: true, encoding: .utf8)
    try await Task.sleep(for: .milliseconds(1500))
    #expect(live.value == afterStop, "a stopped watcher still delivered events")
}

// MARK: - Index open

@Test("openVault returns before the index is opened, and databaseReady waits for it")
@MainActor
func openVaultOpensIndexOffMain() async throws {
    setenv("2NB_TEST", "1", 1)
    let root = URL(fileURLWithPath: NSTemporaryDirectory())
        .appendingPathComponent("IndexOpenTests-\(UUID().uuidString)")
    try VaultManager.initializeVault(at: root)
    let state = AppState()
    state.openVault(at: root)
    await state.databaseReady()
    #expect(state.database != nil)

    // Switching vaults drops the old handle at once rather than leaving it
    // in place until the new one opens.
    let other = URL(fileURLWithPath: NSTemporaryDirectory())
        .appendingPathComponent("IndexOpenTests-\(UUID().uuidString)")
    try VaultManager.initializeVault(at: other)
    state.openVault(at: other)
    #expect(state.database == nil)
    await state.databaseReady()
    #expect(state.database != nil)
}

// MARK: - Vault create

@Test("createVaultAsync creates and binds the vault")
@MainActor
func createVaultAsyncBinds() async throws {
    setenv("2NB_TEST", "1", 1)
    let root = URL(fileURLWithPath: NSTemporaryDirectory())
        .appendingPathComponent("CreateVaultTests-\(UUID().uuidString)")
    let state = AppState()
    await state.createVaultAsync(at: root)
    #expect(state.vault?.rootURL.standardizedFileURL == root.standardizedFileURL)
    #expect(FileManager.default.fileExists(atPath: root.appendingPathComponent(".2ndbrain").path))
}

// MARK: - Pipe EOF timeout

@Test("A grandchild holding the pipe open bounds the wait and is reported as incomplete")
@MainActor
func inheritedPipeIsBoundedAndReported() async throws {
    setenv("2NB_TEST", "1", 1)
    let state = AppState()
    // The background sleep inherits stdout and keeps it open after the
    // script exits, so EOF never arrives.
    state.cliExecutablePathOverride = try makeScript("echo hi; sleep 30 &").path
    let start = ContinuousClock.now
    do {
        _ = try await state.runCLI(["noop"], cwd: URL(fileURLWithPath: NSTemporaryDirectory()))
        Issue.record("expected outputIncomplete")
    } catch CLIError.outputIncomplete {
        // expected
    }
    #expect(ContinuousClock.now - start < .seconds(10))
}

// MARK: - File list

@Test("refreshFiles walks off the main actor and a later refresh wins")
@MainActor
func refreshFilesLatestWins() async throws {
    setenv("2NB_TEST", "1", 1)
    let root = URL(fileURLWithPath: NSTemporaryDirectory())
        .appendingPathComponent("RefreshFilesTests-\(UUID().uuidString)")
    try VaultManager.initializeVault(at: root)
    let state = AppState()
    state.openVault(at: root)
    try "a".write(to: root.appendingPathComponent("one.md"), atomically: true, encoding: .utf8)
    let first = state.refreshFiles()
    try "b".write(to: root.appendingPathComponent("two.md"), atomically: true, encoding: .utf8)
    let second = state.refreshFiles()
    await first?.value
    await second?.value
    // The watcher sees these writes too and starts its own (newer) refresh,
    // which supersedes ours: latest wins, so wait for the list to settle.
    for _ in 0..<40 where state.files.count < 2 { try await Task.sleep(for: .milliseconds(50)) }
    #expect(Set(state.files.map(\.relativePath)) == ["one.md", "two.md"], "got \(state.files.map(\.relativePath))")
}

// MARK: - Edits during a full index

@Test("A note queued while a full index runs is kept, not dropped")
@MainActor
func editsDuringIndexAreKept() async throws {
    setenv("2NB_TEST", "1", 1)
    let root = URL(fileURLWithPath: NSTemporaryDirectory())
        .appendingPathComponent("IndexingEditTests-\(UUID().uuidString)")
    try VaultManager.initializeVault(at: root)
    let state = AppState()
    state.openVault(at: root)
    state.externalReindexDebounce = .milliseconds(10)
    let note = root.appendingPathComponent("edited.md")
    try "---\ntitle: Edited\n---\nbody\n".write(to: note, atomically: true, encoding: .utf8)

    state.isIndexing = true
    state.scheduleExternalReindex(path: note.path)
    try await Task.sleep(for: .milliseconds(200))
    // The drain ran while indexing and must have kept the path.
    #expect(state.pendingExternalPaths.contains(note.path))
}
