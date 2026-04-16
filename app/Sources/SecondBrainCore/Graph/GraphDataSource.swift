import Foundation
import CoreGraphics

/// Filters applied when building a graph. Omitting a value means "don't
/// filter on that dimension" — an empty includeTags list is treated as
/// "include all tags", not "include nothing".
public struct GraphFilters: Equatable, Sendable {
    public var searchText: String = ""
    public var includeTags: [String] = []
    public var excludeTags: [String] = []
    public var includeDocTypes: Set<String> = []  // empty => all types
    public var showOrphans: Bool = true
    public var showUnresolvedLinks: Bool = false
    public var showTagNodes: Bool = false

    public init() {}
}

/// Builds a GraphModel from the database applying the active filters and
/// group color rules. All work happens synchronously on the caller's thread —
/// the caller is responsible for hopping off the main queue for large vaults.
public final class GraphDataSource: @unchecked Sendable {
    private let db: DatabaseManager

    public init(database: DatabaseManager) {
        self.db = database
    }

    /// Build the full vault graph.
    public func buildGlobal(filters: GraphFilters, groups: [GraphGroup]) throws -> GraphModel {
        let docs = try db.allDocuments()
        let edgeRows = try db.allLinks()
        let tagMap = try db.allTagsByDoc()
        let degrees = try db.docDegrees()

        var unresolved: [(source: String, rawTarget: String)] = []
        if filters.showUnresolvedLinks {
            unresolved = try db.unresolvedLinks()
        }

        return buildModel(
            docs: docs,
            edges: edgeRows,
            tagMap: tagMap,
            degrees: degrees,
            unresolved: unresolved,
            filters: filters,
            groups: groups
        )
    }

    /// Build a subgraph centered on `rootID` reaching `depth` hops outward.
    public func buildLocal(rootID: String, depth: Int, filters: GraphFilters, groups: [GraphGroup]) throws -> GraphModel {
        let (docs, edgeRows) = try db.localGraph(rootID: rootID, depth: depth)
        let tagMap = try db.allTagsByDoc()
        let degrees = try db.docDegrees()

        return buildModel(
            docs: docs,
            edges: edgeRows,
            tagMap: tagMap,
            degrees: degrees,
            unresolved: [],
            filters: filters,
            groups: groups,
            rootID: rootID
        )
    }

    // MARK: - Shared pipeline

    private func buildModel(
        docs: [DocumentRecord],
        edges edgeRows: [(source: String, target: String)],
        tagMap: [String: [String]],
        degrees: [String: Int],
        unresolved: [(source: String, rawTarget: String)],
        filters: GraphFilters,
        groups: [GraphGroup],
        rootID: String? = nil
    ) -> GraphModel {
        let includeSet = Set(filters.includeTags.map { $0.lowercased() })
        let excludeSet = Set(filters.excludeTags.map { $0.lowercased() })
        let query = filters.searchText.lowercased()

        // Candidate doc nodes after filters (the root is always kept so the
        // local graph always renders its center point).
        var candidateDocs: [DocumentRecord] = []
        candidateDocs.reserveCapacity(docs.count)
        for d in docs {
            let isRoot = (d.id == rootID)
            if !isRoot && !matches(doc: d, tags: tagMap[d.id] ?? [], filters: filters,
                                   includeSet: includeSet, excludeSet: excludeSet, query: query) {
                continue
            }
            candidateDocs.append(d)
        }

        let keptIDs = Set(candidateDocs.map(\.id))
        var keptEdges: [GraphEdge] = []
        keptEdges.reserveCapacity(edgeRows.count)
        for e in edgeRows {
            if keptIDs.contains(e.source) && keptIDs.contains(e.target) {
                keptEdges.append(GraphEdge(sourceID: e.source, targetID: e.target, resolved: true))
            }
        }

        // Orphan filter — drop nodes with no kept edges when the toggle is off.
        if !filters.showOrphans && rootID == nil {
            var connected: Set<String> = []
            for e in keptEdges { connected.insert(e.sourceID); connected.insert(e.targetID) }
            candidateDocs.removeAll { !connected.contains($0.id) }
        }

        var nodes: [GraphNode] = candidateDocs.map { doc in
            let tags = tagMap[doc.id] ?? []
            let color = groupColor(for: doc, tags: tags, degrees: degrees, groups: groups)
                ?? GraphColor.forDocType(doc.docType)
            return GraphNode(
                id: doc.id,
                title: doc.title.isEmpty ? URL(fileURLWithPath: doc.path).deletingPathExtension().lastPathComponent : doc.title,
                path: doc.path,
                docType: doc.docType,
                kind: .document,
                tags: tags,
                degree: degrees[doc.id] ?? 0,
                groupColor: color
            )
        }

        // Optional: synthetic tag nodes. A tag becomes a node if at least one
        // kept document carries it. Edges go from the tag node to every kept
        // document that has the tag.
        if filters.showTagNodes {
            var tagToDocs: [String: [String]] = [:]
            for doc in candidateDocs {
                for tag in tagMap[doc.id] ?? [] {
                    tagToDocs[tag, default: []].append(doc.id)
                }
            }
            for (tag, docIDs) in tagToDocs {
                let tagID = "tag:\(tag)"
                nodes.append(GraphNode(
                    id: tagID,
                    title: "#\(tag)",
                    kind: .tag,
                    degree: docIDs.count,
                    groupColor: .tagNode
                ))
                for doc in docIDs {
                    keptEdges.append(GraphEdge(sourceID: tagID, targetID: doc, resolved: true))
                }
            }
        }

        // Optional: unresolved-link stubs. Each unresolved target becomes a
        // dashed "ghost" node so the user sees what links go nowhere.
        if filters.showUnresolvedLinks {
            var seen: Set<String> = []
            for u in unresolved where keptIDs.contains(u.source) {
                let ghostID = "unresolved:\(u.rawTarget)"
                if seen.insert(ghostID).inserted {
                    nodes.append(GraphNode(
                        id: ghostID,
                        title: u.rawTarget,
                        kind: .document,
                        groupColor: GraphColor(r: 0.85, g: 0.85, b: 0.85, a: 0.5)
                    ))
                }
                keptEdges.append(GraphEdge(sourceID: u.source, targetID: ghostID, resolved: false))
            }
        }

        return GraphModel(nodes: nodes, edges: keptEdges)
    }

    private func matches(
        doc: DocumentRecord,
        tags: [String],
        filters: GraphFilters,
        includeSet: Set<String>,
        excludeSet: Set<String>,
        query: String
    ) -> Bool {
        if !filters.includeDocTypes.isEmpty && !filters.includeDocTypes.contains(doc.docType) {
            return false
        }
        let tagsLower = tags.map { $0.lowercased() }
        if !includeSet.isEmpty && includeSet.isDisjoint(with: tagsLower) { return false }
        if !excludeSet.isEmpty && !excludeSet.isDisjoint(with: tagsLower) { return false }
        if !query.isEmpty {
            let hay = doc.title.lowercased() + " " + doc.path.lowercased()
            if !hay.contains(query) { return false }
        }
        return true
    }

    // MARK: - Group matching

    /// Returns the first group whose query matches this doc, or nil.
    func groupColor(
        for doc: DocumentRecord,
        tags: [String],
        degrees: [String: Int],
        groups: [GraphGroup]
    ) -> GraphColor? {
        for g in groups {
            if evaluate(query: g.query, doc: doc, tags: tags, degrees: degrees) {
                return g.color
            }
        }
        return nil
    }

    /// Evaluates a tiny DSL: clauses separated by whitespace, all ANDed.
    /// Supported clauses:
    ///   type:<value>, tag:<value>, path:<substring>, status:<value>,
    ///   orphan (no resolved links), title:<substring>.
    /// Unknown clauses evaluate to false (fail-closed so typos don't recolor
    /// the whole vault).
    func evaluate(query raw: String, doc: DocumentRecord, tags: [String], degrees: [String: Int]) -> Bool {
        let clauses = raw.split(whereSeparator: { $0.isWhitespace }).map(String.init)
        guard !clauses.isEmpty else { return false }
        for clause in clauses {
            if !evaluateClause(clause, doc: doc, tags: tags, degrees: degrees) {
                return false
            }
        }
        return true
    }

    private func evaluateClause(
        _ clause: String,
        doc: DocumentRecord,
        tags: [String],
        degrees: [String: Int]
    ) -> Bool {
        let lower = clause.lowercased()
        if lower == "orphan" {
            return (degrees[doc.id] ?? 0) == 0
        }
        guard let colon = lower.firstIndex(of: ":") else { return false }
        let key = String(lower[..<colon])
        let value = String(lower[lower.index(after: colon)...])
        guard !value.isEmpty else { return false }
        switch key {
        case "type":   return doc.docType.lowercased() == value
        case "status": return doc.status.lowercased() == value
        case "tag":    return tags.contains { $0.lowercased() == value }
        case "path":   return doc.path.lowercased().contains(value)
        case "title":  return doc.title.lowercased().contains(value)
        default:       return false
        }
    }
}
