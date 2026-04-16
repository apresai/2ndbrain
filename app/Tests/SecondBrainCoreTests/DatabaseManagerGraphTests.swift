import Testing
import Foundation
import GRDB
@testable import SecondBrainCore

/// Helper: seed a temp index.db with a small set of documents + resolved
/// links for graph-query tests. Returns an open DatabaseManager rooted at a
/// fresh temp directory; the caller is responsible for deleting the file.
@MainActor
private func makeGraphFixture() throws -> (DatabaseManager, URL) {
    let url = FileManager.default.temporaryDirectory
        .appendingPathComponent("graph-\(UUID().uuidString).db")
    let db = try DatabaseManager(path: url.path)

    // Six docs in a chain with a side-branch and a cycle:
    //   A -> B -> C -> D
    //        B -> E (side branch)
    //        D -> A (cycle)
    //   F is an orphan with no links.
    let docs: [(id: String, path: String, title: String, type: String)] = [
        ("A", "a.md", "Alpha", "note"),
        ("B", "b.md", "Bravo", "adr"),
        ("C", "c.md", "Charlie", "note"),
        ("D", "d.md", "Delta", "runbook"),
        ("E", "e.md", "Echo", "note"),
        ("F", "f.md", "Foxtrot", "note"),
    ]
    let edges: [(String, String)] = [
        ("A", "B"), ("B", "C"), ("C", "D"),
        ("B", "E"),
        ("D", "A"),
    ]
    let tags: [(id: String, tag: String)] = [
        ("A", "arch"), ("B", "arch"), ("B", "draft"), ("C", "arch"),
    ]

    try db.pool.write { conn in
        for d in docs {
            try conn.execute(
                sql: "INSERT INTO documents(id, path, title, doc_type) VALUES (?, ?, ?, ?)",
                arguments: [d.id, d.path, d.title, d.type]
            )
        }
        for e in edges {
            try conn.execute(
                sql: """
                    INSERT INTO links(source_id, target_id, target_raw, resolved)
                    VALUES (?, ?, ?, 1)
                """,
                arguments: [e.0, e.1, e.1]
            )
        }
        for t in tags {
            try conn.execute(
                sql: "INSERT INTO tags(doc_id, tag) VALUES (?, ?)",
                arguments: [t.id, t.tag]
            )
        }
    }
    return (db, url)
}

@Test("localGraph depth=0 returns only the root")
@MainActor
func localGraphDepthZero() throws {
    let (db, url) = try makeGraphFixture()
    defer { try? FileManager.default.removeItem(at: url) }

    let (nodes, edges) = try db.localGraph(rootID: "A", depth: 0)
    #expect(nodes.count == 1)
    #expect(nodes.first?.id == "A")
    #expect(edges.isEmpty)
}

@Test("localGraph depth=1 follows both directions")
@MainActor
func localGraphDepthOne() throws {
    let (db, url) = try makeGraphFixture()
    defer { try? FileManager.default.removeItem(at: url) }

    // From B at depth 1: reaches A (in), C (out), E (out)
    let (nodes, _) = try db.localGraph(rootID: "B", depth: 1)
    let ids = Set(nodes.map(\.id))
    #expect(ids == Set(["A", "B", "C", "E"]))
}

@Test("localGraph handles cycles without infinite traversal")
@MainActor
func localGraphCycleSafe() throws {
    let (db, url) = try makeGraphFixture()
    defer { try? FileManager.default.removeItem(at: url) }

    // A->B->C->D->A is a cycle. Depth 10 should return the 5 linked docs
    // (A,B,C,D,E) and NOT the orphan F, without looping forever.
    let (nodes, edges) = try db.localGraph(rootID: "A", depth: 10)
    let ids = Set(nodes.map(\.id))
    #expect(ids == Set(["A", "B", "C", "D", "E"]))
    #expect(edges.count == 5)  // all 5 resolved edges within the subgraph
}

@Test("localGraph excludes unreachable orphans")
@MainActor
func localGraphExcludesOrphan() throws {
    let (db, url) = try makeGraphFixture()
    defer { try? FileManager.default.removeItem(at: url) }

    let (nodes, _) = try db.localGraph(rootID: "A", depth: 5)
    #expect(!nodes.contains { $0.id == "F" })
}

@Test("docDegrees counts incoming + outgoing resolved links")
@MainActor
func docDegreesCounts() throws {
    let (db, url) = try makeGraphFixture()
    defer { try? FileManager.default.removeItem(at: url) }

    let degrees = try db.docDegrees()
    // A: out to B, in from D = 2
    // B: in from A, out to C+E = 3
    // D: in from C, out to A = 2
    // F: no links -> absent from map
    #expect(degrees["A"] == 2)
    #expect(degrees["B"] == 3)
    #expect(degrees["D"] == 2)
    #expect(degrees["F"] == nil)
}

@Test("tagsForDoc returns sorted tags")
@MainActor
func tagsForDocSorted() throws {
    let (db, url) = try makeGraphFixture()
    defer { try? FileManager.default.removeItem(at: url) }

    let bTags = try db.tagsForDoc(id: "B")
    #expect(bTags == ["arch", "draft"])
    let fTags = try db.tagsForDoc(id: "F")
    #expect(fTags.isEmpty)
}

@Test("allTagsByDoc returns complete map in one query")
@MainActor
func allTagsByDocMap() throws {
    let (db, url) = try makeGraphFixture()
    defer { try? FileManager.default.removeItem(at: url) }

    let map = try db.allTagsByDoc()
    #expect(map["A"] == ["arch"])
    #expect(map["B"] == ["arch", "draft"])
    #expect(map["F"] == nil)
}
