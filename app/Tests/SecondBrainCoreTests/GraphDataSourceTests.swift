import Testing
import Foundation
import GRDB
@testable import SecondBrainCore

@MainActor
private func makeDataSourceFixture() throws -> (DatabaseManager, GraphDataSource, URL) {
    let url = FileManager.default.temporaryDirectory
        .appendingPathComponent("gds-\(UUID().uuidString).db")
    let db = try DatabaseManager(path: url.path)

    // Seed: three ADRs, two notes, one runbook. Edges connect ADR1↔Note1,
    // ADR2↔Runbook. ADR3 and Note2 are orphans.
    let docs: [(id: String, path: String, title: String, type: String, status: String)] = [
        ("A1", "adr-1.md",  "ADR One",   "adr",     "accepted"),
        ("A2", "adr-2.md",  "ADR Two",   "adr",     "proposed"),
        ("A3", "adr-3.md",  "ADR Three", "adr",     "proposed"),
        ("N1", "note-1.md", "Note One",  "note",    "draft"),
        ("N2", "note-2.md", "Note Two",  "note",    "draft"),
        ("R1", "run.md",    "Runbook",   "runbook", "active"),
    ]
    let edges: [(String, String)] = [
        ("A1", "N1"),
        ("A2", "R1"),
    ]
    let tags: [(String, String)] = [
        ("A1", "arch"), ("A1", "security"),
        ("A2", "arch"),
        ("N1", "draft"),
        ("N2", "draft"),
        ("R1", "infra"),
    ]

    try db.pool.write { conn in
        for d in docs {
            try conn.execute(
                sql: "INSERT INTO documents(id, path, title, doc_type, status) VALUES (?, ?, ?, ?, ?)",
                arguments: [d.id, d.path, d.title, d.type, d.status]
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
        // One unresolved link — A3 -> "ghost" (no matching doc).
        try conn.execute(
            sql: """
                INSERT INTO links(source_id, target_id, target_raw, resolved)
                VALUES (?, NULL, ?, 0)
            """,
            arguments: ["A3", "ghost"]
        )
        for t in tags {
            try conn.execute(sql: "INSERT INTO tags(doc_id, tag) VALUES (?, ?)", arguments: [t.0, t.1])
        }
    }
    return (db, GraphDataSource(database: db), url)
}

@Test("buildGlobal returns every document when filters are default")
@MainActor
func globalDefault() throws {
    let (_, ds, url) = try makeDataSourceFixture()
    defer { try? FileManager.default.removeItem(at: url) }

    let model = try ds.buildGlobal(filters: GraphFilters(), groups: [])
    #expect(model.nodes.count == 6)
    #expect(model.edges.count == 2)
}

@Test("showOrphans=false removes disconnected nodes")
@MainActor
func hideOrphans() throws {
    let (_, ds, url) = try makeDataSourceFixture()
    defer { try? FileManager.default.removeItem(at: url) }

    var filters = GraphFilters()
    filters.showOrphans = false
    let model = try ds.buildGlobal(filters: filters, groups: [])
    let ids = Set(model.nodes.map(\.id))
    #expect(ids == Set(["A1", "A2", "N1", "R1"]))  // A3 and N2 removed
}

@Test("includeTags keeps only docs with matching tags")
@MainActor
func includeTags() throws {
    let (_, ds, url) = try makeDataSourceFixture()
    defer { try? FileManager.default.removeItem(at: url) }

    var filters = GraphFilters()
    filters.includeTags = ["arch"]
    let model = try ds.buildGlobal(filters: filters, groups: [])
    let ids = Set(model.nodes.map(\.id))
    #expect(ids == Set(["A1", "A2"]))
}

@Test("excludeTags removes matching docs")
@MainActor
func excludeTags() throws {
    let (_, ds, url) = try makeDataSourceFixture()
    defer { try? FileManager.default.removeItem(at: url) }

    var filters = GraphFilters()
    filters.excludeTags = ["draft"]
    let model = try ds.buildGlobal(filters: filters, groups: [])
    let ids = Set(model.nodes.map(\.id))
    #expect(!ids.contains("N1"))
    #expect(!ids.contains("N2"))
}

@Test("searchText filters on title + path substring")
@MainActor
func searchText() throws {
    let (_, ds, url) = try makeDataSourceFixture()
    defer { try? FileManager.default.removeItem(at: url) }

    var filters = GraphFilters()
    filters.searchText = "Runbook"
    let model = try ds.buildGlobal(filters: filters, groups: [])
    #expect(model.nodes.map(\.id) == ["R1"])
}

@Test("includeDocTypes limits by doc_type")
@MainActor
func includeDocTypes() throws {
    let (_, ds, url) = try makeDataSourceFixture()
    defer { try? FileManager.default.removeItem(at: url) }

    var filters = GraphFilters()
    filters.includeDocTypes = ["adr"]
    let model = try ds.buildGlobal(filters: filters, groups: [])
    #expect(Set(model.nodes.map(\.id)) == Set(["A1", "A2", "A3"]))
}

@Test("showTagNodes adds synthetic tag nodes connected to their docs")
@MainActor
func tagNodes() throws {
    let (_, ds, url) = try makeDataSourceFixture()
    defer { try? FileManager.default.removeItem(at: url) }

    var filters = GraphFilters()
    filters.showTagNodes = true
    let model = try ds.buildGlobal(filters: filters, groups: [])
    let tagIDs = model.nodes.filter { $0.kind == .tag }.map(\.id)
    #expect(tagIDs.contains("tag:arch"))
    #expect(tagIDs.contains("tag:infra"))
    // "arch" tag should have edges to A1 and A2
    let archEdges = model.edges.filter { $0.sourceID == "tag:arch" || $0.targetID == "tag:arch" }
    #expect(archEdges.count == 2)
}

@Test("showUnresolvedLinks adds ghost nodes")
@MainActor
func unresolvedLinks() throws {
    let (_, ds, url) = try makeDataSourceFixture()
    defer { try? FileManager.default.removeItem(at: url) }

    var filters = GraphFilters()
    filters.showUnresolvedLinks = true
    let model = try ds.buildGlobal(filters: filters, groups: [])
    let ghostID = "unresolved:ghost"
    #expect(model.nodes.contains { $0.id == ghostID })
    #expect(model.edges.contains { $0.sourceID == "A3" && $0.targetID == ghostID && !$0.resolved })
}

@Test("group query assigns color by type")
@MainActor
func groupColorByType() throws {
    let (_, ds, url) = try makeDataSourceFixture()
    defer { try? FileManager.default.removeItem(at: url) }

    let pink = GraphColor(r: 1, g: 0.4, b: 0.8)
    let groups = [GraphGroup(name: "ADRs", query: "type:adr", color: pink)]
    let model = try ds.buildGlobal(filters: GraphFilters(), groups: groups)
    let adrs = model.nodes.filter { $0.docType == "adr" }
    #expect(!adrs.isEmpty)
    for n in adrs { #expect(n.groupColor == pink) }
    let notes = model.nodes.filter { $0.docType == "note" }
    for n in notes { #expect(n.groupColor != pink) }
}

@Test("group query with multiple clauses ANDs them together")
@MainActor
func groupMultiClauseAND() throws {
    let (_, ds, url) = try makeDataSourceFixture()
    defer { try? FileManager.default.removeItem(at: url) }

    let teal = GraphColor(r: 0, g: 0.7, b: 0.7)
    let groups = [GraphGroup(name: "arch+proposed", query: "type:adr status:proposed", color: teal)]
    let model = try ds.buildGlobal(filters: GraphFilters(), groups: groups)
    // Only A2 and A3 are proposed ADRs
    let matched = model.nodes.filter { $0.groupColor == teal }.map(\.id)
    #expect(Set(matched) == Set(["A2", "A3"]))
}

@Test("group query 'orphan' matches unlinked docs")
@MainActor
func groupOrphanClause() throws {
    let (_, ds, url) = try makeDataSourceFixture()
    defer { try? FileManager.default.removeItem(at: url) }

    let yellow = GraphColor(r: 1, g: 1, b: 0)
    let groups = [GraphGroup(name: "orphans", query: "orphan", color: yellow)]
    let model = try ds.buildGlobal(filters: GraphFilters(), groups: groups)
    let matched = Set(model.nodes.filter { $0.groupColor == yellow }.map(\.id))
    #expect(matched == Set(["A3", "N2"]))
}

@Test("buildLocal keeps root even when filters would exclude it")
@MainActor
func localAlwaysKeepsRoot() throws {
    let (_, ds, url) = try makeDataSourceFixture()
    defer { try? FileManager.default.removeItem(at: url) }

    // Root A1 has tag "arch,security" — filter out "arch" and "security"
    // both; if the code were to apply filters blindly it would drop A1.
    var filters = GraphFilters()
    filters.excludeTags = ["arch", "security"]
    let model = try ds.buildLocal(rootID: "A1", depth: 1, filters: filters, groups: [])
    #expect(model.nodes.contains { $0.id == "A1" })
}

@Test("node degree is populated from resolved link count")
@MainActor
func nodeDegreePopulated() throws {
    let (_, ds, url) = try makeDataSourceFixture()
    defer { try? FileManager.default.removeItem(at: url) }

    let model = try ds.buildGlobal(filters: GraphFilters(), groups: [])
    // A1 links to N1 (out, 1) and has no incoming — degree 1
    let a1 = model.nodes.first { $0.id == "A1" }!
    #expect(a1.degree == 1)
    // A3 has no resolved edges — degree 0
    let a3 = model.nodes.first { $0.id == "A3" }!
    #expect(a3.degree == 0)
}
