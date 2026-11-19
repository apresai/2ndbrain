import Testing
import Foundation
import GRDB
@testable import SecondBrainCore

@MainActor
private func makeSearchFixtureDB(withFTS5: Bool) throws -> (DatabaseManager, URL) {
    let url = FileManager.default.temporaryDirectory
        .appendingPathComponent("picker-\(UUID().uuidString).db")
    let db = try DatabaseManager(path: url.path)

    if withFTS5 {
        try db.pool.write { conn in
            try conn.execute(sql: """
                CREATE VIRTUAL TABLE IF NOT EXISTS chunks_fts USING fts5(
                    content,
                    heading_path,
                    content='chunks',
                    content_rowid='rowid',
                    tokenize='porter unicode61'
                );
                CREATE TRIGGER IF NOT EXISTS chunks_ai AFTER INSERT ON chunks BEGIN
                    INSERT INTO chunks_fts(rowid, content, heading_path)
                    VALUES (new.rowid, new.content, new.heading_path);
                END;
                CREATE TRIGGER IF NOT EXISTS chunks_ad AFTER DELETE ON chunks BEGIN
                    INSERT INTO chunks_fts(chunks_fts, rowid, content, heading_path)
                    VALUES ('delete', old.rowid, old.content, old.heading_path);
                END;
                CREATE TRIGGER IF NOT EXISTS chunks_au AFTER UPDATE ON chunks BEGIN
                    INSERT INTO chunks_fts(chunks_fts, rowid, content, heading_path)
                    VALUES ('delete', old.rowid, old.content, old.heading_path);
                    INSERT INTO chunks_fts(rowid, content, heading_path)
                    VALUES (new.rowid, new.content, new.heading_path);
                END;
            """)
        }
    }

    try db.pool.write { conn in
        // Seed 224 newer notes plus 1 oldest note beyond rank 200.
        for i in 1...224 {
            let id = String(format: "doc-%03d", i)
            let path = i == 1 ? "projects/alpha.md" : (i == 2 ? "areas/billing.md" : String(format: "notes/filler-%03d.md", i))
            let title = i == 1 ? "Alpha Launch" : (i == 2 ? "Billing Ops" : "Filler \(i)")
            let mod = String(format: "2026-11-%02dT10:%02d:00Z", 10 + (i % 15), i % 60)
            try conn.execute(
                sql: "INSERT INTO documents(id, path, title, doc_type, status, modified_at) VALUES (?, ?, ?, 'note', 'complete', ?)",
                arguments: [id, path, title, mod]
            )
            let body = i == 3
                ? "Contains unique quantum-canary-token inside body prose only."
                : "Standard filler body \(i)."
            try conn.execute(
                sql: "INSERT INTO chunks(id, doc_id, heading_path, level, content, content_hash, start_line, end_line, sort_order) VALUES (?, ?, ?, 1, ?, ?, 1, 5, 0)",
                arguments: ["chunk-\(id)", id, title, body, "hash-\(id)"]
            )
        }

        // Oldest note (modified_at 2020) — well past a 200-note recent cap.
        try conn.execute(
            sql: "INSERT INTO documents(id, path, title, doc_type, status, modified_at) VALUES ('doc-225', 'areas/note-225.md', 'Zebra Final Spec', 'note', 'complete', '2020-01-01T00:00:00Z')"
        )
        try conn.execute(
            sql: "INSERT INTO chunks(id, doc_id, heading_path, level, content, content_hash, start_line, end_line, sort_order) VALUES ('chunk-225', 'doc-225', 'Zebra Final Spec', 1, 'Contains needle-225-alpha in the oldest note.', 'hash-225', 1, 5, 0)"
        )
    }

    return (db, url)
}

@Test("DatabaseManager.ftsMatchQuery safely quotes hyphenated and slash terms")
func ftsMatchQueryQuoting() {
    #expect(DatabaseManager.ftsMatchQuery(for: "quantum-canary-token") == "\"quantum-canary-token\"")
    #expect(DatabaseManager.ftsMatchQuery(for: "projects/alpha") == "\"projects/alpha\"")
    #expect(DatabaseManager.ftsMatchQuery(for: "Billing Ops") == "billing ops")
    #expect(DatabaseManager.ftsMatchQuery(for: "  \"needle-225-alpha*\"? ") == "\"needle-225-alpha\"")
}

@Test("DatabaseManager.searchNotes handles recent notes cap, title, path, and FTS5 hyphenated content beyond 200 notes")
@MainActor
func searchNotesFTS5AndLargeVault() throws {
    let (db, url) = try makeSearchFixtureDB(withFTS5: true)
    defer { try? FileManager.default.removeItem(at: url) }

    #expect(try db.documentCount() == 225)

    // Empty query returns bounded recent notes (not all 225).
    let recent = try db.searchNotes(query: "", limit: 50)
    #expect(recent.count == 50)
    #expect(!recent.contains(where: { $0.path == "areas/note-225.md" }))

    // Title search
    let byTitle = try db.searchNotes(query: "Billing Ops", limit: 20)
    #expect(byTitle.first?.path == "areas/billing.md")
    #expect(byTitle.first?.matchSource == "title")

    // Path search
    let byPath = try db.searchNotes(query: "projects/alpha", limit: 20)
    #expect(byPath.first?.path == "projects/alpha.md")
    #expect(byPath.first?.matchSource == "path")

    // Hyphenated body FTS5 search
    let byFTS = try db.searchNotes(query: "quantum-canary-token", limit: 20)
    #expect(byFTS.count == 1)
    #expect(byFTS.first?.path == "notes/filler-003.md")
    #expect(byFTS.first?.matchSource == "content")
    #expect(byFTS.first?.snippet?.contains("quantum-canary-token") == true)

    // Oldest note beyond 200-note recent window is found by both title and FTS5 body
    let oldestByTitle = try db.searchNotes(query: "Zebra Final Spec", limit: 20)
    #expect(oldestByTitle.first?.path == "areas/note-225.md")

    let oldestByBody = try db.searchNotes(query: "needle-225-alpha", limit: 20)
    #expect(oldestByBody.first?.path == "areas/note-225.md")
}

@Test("DatabaseManager.searchNotes falls back to chunks.content LIKE when chunks_fts table is absent")
@MainActor
func searchNotesWithoutFTSFallback() throws {
    let (db, url) = try makeSearchFixtureDB(withFTS5: false)
    defer { try? FileManager.default.removeItem(at: url) }

    let hits = try db.searchNotes(query: "needle-225-alpha", limit: 20)
    #expect(hits.count == 1)
    #expect(hits.first?.path == "areas/note-225.md")
    #expect(hits.first?.matchSource == "content")
}

@Test("DatabaseManager.searchNotes deduplicates per document before LIMIT so 160-chunk note does not starve other notes")
@MainActor
func searchNotesDoesNotStarveOtherDocuments() throws {
    for withFTS5 in [true, false] {
        let (db, url) = try makeSearchFixtureDB(withFTS5: withFTS5)
        defer { try? FileManager.default.removeItem(at: url) }

        try db.pool.write { conn in
            try conn.execute(
                sql: "INSERT INTO documents(id, path, title, doc_type, status, modified_at) VALUES ('doc-heavy', 'notes/heavy-runbook.md', 'Heavy Runbook', 'note', 'complete', '2026-11-28T12:00:00Z')"
            )
            for i in 1...160 {
                try conn.execute(
                    sql: "INSERT INTO chunks(id, doc_id, heading_path, level, content, content_hash, start_line, end_line, sort_order) VALUES (?, 'doc-heavy', ?, 2, ?, ?, ?, ?, ?)",
                    arguments: [
                        "chunk-heavy-\(i)",
                        "Section \(i)",
                        "Section \(i) repeats shared-heavy-token for testing multi-chunk starvation.",
                        "hash-heavy-\(i)",
                        i * 10,
                        i * 10 + 5,
                        i - 1,
                    ]
                )
            }
            for j in 1...10 {
                let docID = "doc-shared-\(j)"
                let mod = String(format: "2026-11-%02dT09:00:00Z", 10 + j)
                try conn.execute(
                    sql: "INSERT INTO documents(id, path, title, doc_type, status, modified_at) VALUES (?, ?, ?, 'note', 'complete', ?)",
                    arguments: [docID, "notes/shared-\(j).md", "Shared \(j)", mod]
                )
                try conn.execute(
                    sql: "INSERT INTO chunks(id, doc_id, heading_path, level, content, content_hash, start_line, end_line, sort_order) VALUES (?, ?, ?, 1, ?, ?, 1, 5, 0)",
                    arguments: [
                        "chunk-shared-\(j)",
                        docID,
                        "Shared \(j)",
                        "Body mentions shared-heavy-token once.",
                        "hash-shared-\(j)",
                    ]
                )
            }
        }

        let hits = try db.searchNotes(query: "shared-heavy-token", limit: 50)
        #expect(hits.count == 11)
        let paths = Set(hits.map(\.path))
        #expect(paths.contains("notes/heavy-runbook.md"))
        for j in 1...10 {
            #expect(paths.contains("notes/shared-\(j).md"))
        }
    }
}
