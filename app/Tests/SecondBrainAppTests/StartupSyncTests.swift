import Foundation
import Testing
@testable import SecondBrain

@Test("StartupSync.shouldSync gates on a change AND not already indexing")
func startupSyncShouldSync() {
    #expect(StartupSync.shouldSync(vaultChanged: true, isIndexing: false))
    // No change → skip (a stable vault pays nothing on launch).
    #expect(!StartupSync.shouldSync(vaultChanged: false, isIndexing: false))
    // Already indexing → never start a second, racing index.
    #expect(!StartupSync.shouldSync(vaultChanged: true, isIndexing: true))
    #expect(!StartupSync.shouldSync(vaultChanged: false, isIndexing: true))
}

@Test("vaultChangedSinceIndex is true only when an indexable file is newer than the db")
func vaultChangedSinceIndex() throws {
    let fm = FileManager.default
    let root = fm.temporaryDirectory.appendingPathComponent("sb-startup-\(UUID().uuidString)")
    let sidecar = root.appendingPathComponent(".2ndbrain")
    try fm.createDirectory(at: sidecar, withIntermediateDirectories: true)
    let db = sidecar.appendingPathComponent("index.db")
    let note = root.appendingPathComponent("note.md")
    let canvas = root.appendingPathComponent("board.canvas")
    defer { try? fm.removeItem(at: root) }

    let base = Date(timeIntervalSince1970: 1_000_000)
    try "n".write(to: note, atomically: true, encoding: .utf8)
    try "{}".write(to: canvas, atomically: true, encoding: .utf8)

    // No db yet → never indexed → changed.
    #expect(StartupSync.vaultChangedSinceIndex(vaultRoot: root))

    // db newer than every indexable file → unchanged.
    try "x".write(to: db, atomically: true, encoding: .utf8)
    try fm.setAttributes([.modificationDate: base], ofItemAtPath: note.path)
    try fm.setAttributes([.modificationDate: base], ofItemAtPath: canvas.path)
    try fm.setAttributes([.modificationDate: base.addingTimeInterval(60)], ofItemAtPath: db.path)
    #expect(!StartupSync.vaultChangedSinceIndex(vaultRoot: root))

    // A .md edited after the index → changed.
    try fm.setAttributes([.modificationDate: base.addingTimeInterval(120)], ofItemAtPath: note.path)
    #expect(StartupSync.vaultChangedSinceIndex(vaultRoot: root))

    // A .canvas newer than the index also counts (the indexer reads those too).
    try fm.setAttributes([.modificationDate: base], ofItemAtPath: note.path)
    try fm.setAttributes([.modificationDate: base.addingTimeInterval(120)], ofItemAtPath: canvas.path)
    #expect(StartupSync.vaultChangedSinceIndex(vaultRoot: root))
}
