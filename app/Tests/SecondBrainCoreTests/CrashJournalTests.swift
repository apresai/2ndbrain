import Testing
import Foundation
import SecondBrainCore

// Crash recovery is safety-critical — a lost snapshot means lost work after a
// crash — but had no unit coverage before. These tests exercise the public
// surface of CrashJournal against a temp directory.

private func withTempVault(_ body: (URL) throws -> Void) throws {
    let tmp = URL(fileURLWithPath: NSTemporaryDirectory())
        .appendingPathComponent("sb-crash-journal-\(UUID().uuidString)")
    let dot = tmp.appendingPathComponent(".2ndbrain")
    try FileManager.default.createDirectory(at: dot, withIntermediateDirectories: true)
    defer { try? FileManager.default.removeItem(at: tmp) }
    try body(dot)
}

@Test("savePreWriteSnapshot writes content synchronously")
func crashJournalPreWriteSnapshot() throws {
    try withTempVault { dotDir in
        let journal = CrashJournal(vaultDotDir: dotDir)
        let docID = "abc-123"
        try journal.savePreWriteSnapshot(documentID: docID, content: "hello world")

        let expected = dotDir.appendingPathComponent("recovery/\(docID).recovery.md")
        let data = try String(contentsOf: expected, encoding: .utf8)
        #expect(data == "hello world")
    }
}

@Test("clearSnapshotSync removes an existing snapshot")
func crashJournalClearSync() throws {
    try withTempVault { dotDir in
        let journal = CrashJournal(vaultDotDir: dotDir)
        let docID = "abc-456"
        try journal.savePreWriteSnapshot(documentID: docID, content: "temp")
        journal.clearSnapshotSync(documentID: docID)

        let expected = dotDir.appendingPathComponent("recovery/\(docID).recovery.md")
        #expect(!FileManager.default.fileExists(atPath: expected.path))
    }
}

@Test("recoverableDocuments returns saved snapshots newest-first")
func crashJournalRecoverableList() throws {
    try withTempVault { dotDir in
        let journal = CrashJournal(vaultDotDir: dotDir)
        try journal.savePreWriteSnapshot(documentID: "older", content: "one")
        // Bump the mtime of the second by writing after a small sleep so the
        // newest-first sort has a meaningful ordering to verify.
        Thread.sleep(forTimeInterval: 0.05)
        try journal.savePreWriteSnapshot(documentID: "newer", content: "two")

        let entries = journal.recoverableDocuments()
        #expect(entries.count == 2)
        #expect(entries.first?.documentID == "newer")
        #expect(entries.first?.content == "two")
    }
}

@Test("recoverableDocuments tolerates missing recovery directory contents")
func crashJournalEmptyRecovery() throws {
    try withTempVault { dotDir in
        let journal = CrashJournal(vaultDotDir: dotDir)
        let entries = journal.recoverableDocuments()
        #expect(entries.isEmpty)
    }
}

@Test("savePreWriteSnapshot overwrites prior content atomically")
func crashJournalOverwrite() throws {
    try withTempVault { dotDir in
        let journal = CrashJournal(vaultDotDir: dotDir)
        let docID = "overwrite-test"
        try journal.savePreWriteSnapshot(documentID: docID, content: "v1")
        try journal.savePreWriteSnapshot(documentID: docID, content: "v2")

        let expected = dotDir.appendingPathComponent("recovery/\(docID).recovery.md")
        let data = try String(contentsOf: expected, encoding: .utf8)
        #expect(data == "v2")
    }
}

// The async variants below are the code paths production code takes on every
// debounced autosave tick. Sleep-based synchronization is the standard pattern
// for a private serial DispatchQueue — the queue isn't exposed for explicit
// draining, and exposing it would be a wider API surface than the one test.

@Test("saveSnapshot (async) persists after queue drains")
func crashJournalAsyncSave() throws {
    try withTempVault { dotDir in
        let journal = CrashJournal(vaultDotDir: dotDir)
        let docID = "async-save"
        journal.saveSnapshot(documentID: docID, content: "async body")
        Thread.sleep(forTimeInterval: 0.1)

        let expected = dotDir.appendingPathComponent("recovery/\(docID).recovery.md")
        let data = try String(contentsOf: expected, encoding: .utf8)
        #expect(data == "async body")
    }
}

@Test("clearSnapshot (async) removes after queue drains")
func crashJournalAsyncClear() throws {
    try withTempVault { dotDir in
        let journal = CrashJournal(vaultDotDir: dotDir)
        let docID = "async-clear"
        try journal.savePreWriteSnapshot(documentID: docID, content: "delete me")
        journal.clearSnapshot(documentID: docID)
        Thread.sleep(forTimeInterval: 0.1)

        let expected = dotDir.appendingPathComponent("recovery/\(docID).recovery.md")
        #expect(!FileManager.default.fileExists(atPath: expected.path))
    }
}

@Test("clearAll removes every snapshot")
func crashJournalClearAll() throws {
    try withTempVault { dotDir in
        let journal = CrashJournal(vaultDotDir: dotDir)
        for id in ["a", "b", "c"] {
            try journal.savePreWriteSnapshot(documentID: id, content: id)
        }
        #expect(journal.recoverableDocuments().count == 3)

        journal.clearAll()
        Thread.sleep(forTimeInterval: 0.1)

        #expect(journal.recoverableDocuments().isEmpty)
    }
}
