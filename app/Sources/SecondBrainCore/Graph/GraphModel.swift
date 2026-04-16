import Foundation
import CoreGraphics

/// A node in the document graph — either a real markdown document or a
/// synthetic "tag" node used when the "Show tags as nodes" toggle is on.
///
/// Positions and velocities are mutated in place by `GraphSimulation` on
/// every integration step, which is why the struct is a class: the
/// simulation pushes millions of small updates per second on large vaults
/// and copy-on-write on an array of value types would dominate.
public final class GraphNode: Identifiable, Equatable, Hashable, @unchecked Sendable {
    public enum Kind: String, Sendable {
        case document
        case tag
    }

    public let id: String
    public let title: String
    public let path: String
    public let docType: String
    public let kind: Kind
    public var tags: [String]
    public var degree: Int
    public var groupColor: GraphColor?

    // Simulation state — read by the renderer, mutated by GraphSimulation.
    public var position: CGPoint
    public var velocity: CGVector
    public var pinned: Bool

    public init(
        id: String,
        title: String,
        path: String = "",
        docType: String = "",
        kind: Kind = .document,
        tags: [String] = [],
        degree: Int = 0,
        groupColor: GraphColor? = nil,
        position: CGPoint = .zero,
        velocity: CGVector = .zero,
        pinned: Bool = false
    ) {
        self.id = id
        self.title = title
        self.path = path
        self.docType = docType
        self.kind = kind
        self.tags = tags
        self.degree = degree
        self.groupColor = groupColor
        self.position = position
        self.velocity = velocity
        self.pinned = pinned
    }

    public static func == (lhs: GraphNode, rhs: GraphNode) -> Bool { lhs.id == rhs.id }
    public func hash(into hasher: inout Hasher) { hasher.combine(id) }
}

public struct GraphEdge: Identifiable, Sendable, Hashable {
    public let id: String
    public let sourceID: String
    public let targetID: String
    public let resolved: Bool

    public init(sourceID: String, targetID: String, resolved: Bool = true) {
        self.id = "\(sourceID)->\(targetID)"
        self.sourceID = sourceID
        self.targetID = targetID
        self.resolved = resolved
    }
}

/// RGBA color in 0...1. Platform-agnostic so Core and Tests don't need to
/// import AppKit. The app layer converts this to NSColor for rendering.
public struct GraphColor: Equatable, Hashable, Codable, Sendable {
    public var r: Double
    public var g: Double
    public var b: Double
    public var a: Double

    public init(r: Double, g: Double, b: Double, a: Double = 1.0) {
        self.r = r; self.g = g; self.b = b; self.a = a
    }

    public static let adr      = GraphColor(r: 0.30, g: 0.56, b: 0.96)  // blue
    public static let runbook  = GraphColor(r: 0.36, g: 0.77, b: 0.49)  // green
    public static let postmortem = GraphColor(r: 0.94, g: 0.43, b: 0.43) // red
    public static let prd      = GraphColor(r: 0.72, g: 0.45, b: 0.95)  // purple
    public static let prfaq    = GraphColor(r: 0.95, g: 0.65, b: 0.25)  // orange
    public static let note     = GraphColor(r: 0.60, g: 0.60, b: 0.60)  // gray
    public static let tagNode  = GraphColor(r: 0.98, g: 0.80, b: 0.30)  // gold

    /// Default color for a document by doc-type. User-defined groups override this.
    public static func forDocType(_ type: String) -> GraphColor {
        switch type.lowercased() {
        case "adr":        return .adr
        case "runbook":    return .runbook
        case "postmortem": return .postmortem
        case "prd":        return .prd
        case "prfaq":      return .prfaq
        default:           return .note
        }
    }
}

/// User-defined color group. A group matches nodes whose metadata satisfies
/// the query string; matching nodes take the group's color instead of the
/// doc-type default. Query DSL:
///
///   type:adr              — doc_type equals "adr"
///   tag:auth              — has tag "auth"
///   path:/docs/           — path contains substring "/docs/"
///   status:draft          — frontmatter-status equals "draft"
///   orphan                — no incoming or outgoing resolved links
///
/// Multiple clauses space-separated AND together. The parser is deliberately
/// small — we don't want a full expression language in a visualization tool.
public struct GraphGroup: Identifiable, Equatable, Hashable, Codable, Sendable {
    public var id: UUID
    public var name: String
    public var query: String
    public var color: GraphColor

    public init(id: UUID = UUID(), name: String, query: String, color: GraphColor) {
        self.id = id; self.name = name; self.query = query; self.color = color
    }
}

/// Full graph snapshot produced by GraphDataSource. Callers get
/// value-semantics on the arrays but the nodes themselves remain reference
/// types so the simulation can mutate positions in place.
public struct GraphModel: Sendable {
    public var nodes: [GraphNode]
    public var edges: [GraphEdge]
    public var nodeByID: [String: GraphNode]
    public var adjacency: [String: [String]]  // undirected neighbor lookup

    public init(nodes: [GraphNode], edges: [GraphEdge]) {
        self.nodes = nodes
        self.edges = edges
        var byID: [String: GraphNode] = [:]
        byID.reserveCapacity(nodes.count)
        for n in nodes { byID[n.id] = n }
        self.nodeByID = byID

        var adj: [String: [String]] = [:]
        adj.reserveCapacity(nodes.count)
        for e in edges {
            adj[e.sourceID, default: []].append(e.targetID)
            adj[e.targetID, default: []].append(e.sourceID)
        }
        self.adjacency = adj
    }

    public static let empty = GraphModel(nodes: [], edges: [])

    public func neighbors(of id: String) -> [String] { adjacency[id] ?? [] }
}
