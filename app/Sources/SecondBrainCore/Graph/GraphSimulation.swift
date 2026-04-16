import Foundation
import CoreGraphics

/// Tunable parameters for the force-directed layout. Values are deliberately
/// close to d3-force defaults so Obsidian users land on familiar behavior.
public struct GraphSimulationParameters: Equatable, Sendable {
    public var centerStrength: Double = 0.03   // pull toward (centerX, centerY)
    public var repelStrength: Double = -200    // Coulomb-style node-node repulsion (negative = push apart)
    public var linkStrength: Double = 0.6      // spring stiffness on edges
    public var linkDistance: Double = 80       // rest length for edge springs
    public var velocityDecay: Double = 0.4     // friction per tick
    public var alpha: Double = 1.0             // current energy level
    public var alphaMin: Double = 0.002        // below this the simulation pauses
    public var alphaDecay: Double = 0.02       // per-tick decay toward 0
    public var theta: Double = 0.9             // Barnes-Hut accuracy parameter (lower = slower, more accurate)

    public var centerX: Double = 0
    public var centerY: Double = 0

    public init() {}
}

/// Force-directed graph simulation. Designed to be stepped on a display-link
/// timer (~60 Hz). Reheats when structure changes; decays to rest otherwise.
public final class GraphSimulation: @unchecked Sendable {
    public private(set) var model: GraphModel
    public var params: GraphSimulationParameters

    // Per-node link-degree for adaptive link strength (Jaccard-style: links
    // between high-degree nodes are weaker so hubs don't pile onto each other).
    private var edgeSourceDegree: [String: Int]
    private var edgeTargetDegree: [String: Int]

    public init(model: GraphModel, params: GraphSimulationParameters = GraphSimulationParameters()) {
        self.model = model
        self.params = params
        (self.edgeSourceDegree, self.edgeTargetDegree) = Self.computeDegrees(model)
        seedPositions(around: CGPoint(x: params.centerX, y: params.centerY))
    }

    /// Swap in a new graph. Nodes that existed before keep their positions +
    /// velocities (so a small delta doesn't look like a teleport). New nodes
    /// get fresh positions around the existing centroid.
    public func replace(model newModel: GraphModel) {
        let oldByID = Dictionary(uniqueKeysWithValues: model.nodes.map { ($0.id, $0) })
        let centroid = currentCentroid() ?? CGPoint(x: params.centerX, y: params.centerY)
        for n in newModel.nodes {
            if let existing = oldByID[n.id] {
                n.position = existing.position
                n.velocity = existing.velocity
                n.pinned = existing.pinned
            } else {
                n.position = Self.jitter(around: centroid)
                n.velocity = .zero
            }
        }
        self.model = newModel
        (self.edgeSourceDegree, self.edgeTargetDegree) = Self.computeDegrees(newModel)
        reheat()
    }

    /// Bump alpha back up so the simulation visibly rearranges after a change.
    public func reheat(to alpha: Double = 1.0) {
        params.alpha = max(params.alpha, alpha)
    }

    public var isAtRest: Bool { params.alpha < params.alphaMin }

    public var centerPoint: CGPoint {
        get { CGPoint(x: params.centerX, y: params.centerY) }
        set { params.centerX = Double(newValue.x); params.centerY = Double(newValue.y) }
    }

    // MARK: - Integration step

    /// Advance the simulation by one tick. `dt` is in seconds; typical value
    /// is 1/60. Returns false when the simulation has reached rest so the
    /// caller can pause its display link.
    @discardableResult
    public func step(dt: Double = 1.0 / 60.0) -> Bool {
        guard !isAtRest else { return false }
        guard !model.nodes.isEmpty else {
            params.alpha = 0
            return false
        }

        // Forces are applied as velocity deltas scaled by alpha so the
        // simulation cools smoothly to rest rather than stopping abruptly.
        let alpha = params.alpha

        applyRepulsion(alpha: alpha)
        applyLinks(alpha: alpha)
        applyCentering(alpha: alpha)

        // Integrate.
        let decay = params.velocityDecay
        for node in model.nodes where !node.pinned {
            node.velocity.dx *= (1 - decay)
            node.velocity.dy *= (1 - decay)
            node.position.x += CGFloat(node.velocity.dx * dt * 60)  // scale to 60fps baseline
            node.position.y += CGFloat(node.velocity.dy * dt * 60)
        }
        // Pinned nodes keep zero velocity even if forces accumulate.
        for node in model.nodes where node.pinned {
            node.velocity = .zero
        }

        // Multiplicative decay toward 0. alphaMin is the rest threshold, not
        // the asymptote — otherwise alpha approaches alphaMin from above and
        // isAtRest (alpha < alphaMin) never fires.
        params.alpha *= (1 - params.alphaDecay)
        return !isAtRest
    }

    // MARK: - Forces

    /// Barnes-Hut quadtree repulsion — O(n log n). For each node we descend
    /// the tree; a cell is treated as a single combined mass if its size is
    /// small compared to the distance (controlled by `theta`).
    private func applyRepulsion(alpha: Double) {
        guard model.nodes.count > 1 else { return }
        var tree = QuadTree(bounds: bounds(of: model.nodes))
        for node in model.nodes { tree.insert(node) }
        tree.computeAggregates()

        for node in model.nodes {
            tree.applyForce(to: node, theta: params.theta, strength: params.repelStrength, alpha: alpha)
        }
    }

    /// Spring force along each edge pulling endpoints toward `linkDistance`.
    private func applyLinks(alpha: Double) {
        for edge in model.edges {
            guard let a = model.nodeByID[edge.sourceID],
                  let b = model.nodeByID[edge.targetID] else { continue }
            let dx = Double(b.position.x - a.position.x)
            let dy = Double(b.position.y - a.position.y)
            let dist = max(sqrt(dx*dx + dy*dy), 0.001)
            let (ra, rb) = bias(source: edge.sourceID, target: edge.targetID)
            let k = params.linkStrength * alpha * (dist - params.linkDistance) / dist
            let fx = dx * k
            let fy = dy * k
            if !a.pinned { a.velocity.dx += fx * rb; a.velocity.dy += fy * rb }
            if !b.pinned { b.velocity.dx -= fx * ra; b.velocity.dy -= fy * ra }
        }
    }

    /// Weak pull toward (centerX, centerY). Keeps the graph anchored so it
    /// doesn't drift off-screen over time.
    private func applyCentering(alpha: Double) {
        let s = params.centerStrength * alpha
        for node in model.nodes where !node.pinned {
            node.velocity.dx += (params.centerX - Double(node.position.x)) * s
            node.velocity.dy += (params.centerY - Double(node.position.y)) * s
        }
    }

    private func bias(source: String, target: String) -> (Double, Double) {
        let sd = edgeSourceDegree[source] ?? 1
        let td = edgeTargetDegree[target] ?? 1
        let total = Double(sd + td)
        guard total > 0 else { return (0.5, 0.5) }
        return (Double(sd) / total, Double(td) / total)
    }

    // MARK: - Positioning helpers

    private func seedPositions(around center: CGPoint) {
        // Fresh nodes placed in a low-jitter random disk around the center —
        // more uniform than a circle layout, less degenerate than origin.
        for (_, node) in model.nodes.enumerated() {
            node.position = Self.jitter(around: center)
            node.velocity = .zero
        }
    }

    private func currentCentroid() -> CGPoint? {
        guard !model.nodes.isEmpty else { return nil }
        var sx: Double = 0
        var sy: Double = 0
        for n in model.nodes {
            sx += Double(n.position.x)
            sy += Double(n.position.y)
        }
        let count = Double(model.nodes.count)
        return CGPoint(x: sx / count, y: sy / count)
    }

    private func bounds(of nodes: [GraphNode]) -> Quad {
        var minX = Double.infinity
        var minY = Double.infinity
        var maxX = -Double.infinity
        var maxY = -Double.infinity
        for n in nodes {
            let x = Double(n.position.x)
            let y = Double(n.position.y)
            if x < minX { minX = x }
            if y < minY { minY = y }
            if x > maxX { maxX = x }
            if y > maxY { maxY = y }
        }
        // Pad and square-up so the tree isn't degenerate.
        let side = max(maxX - minX, maxY - minY, 1) + 10
        return Quad(minX: minX - 5, minY: minY - 5, side: side)
    }

    private static func jitter(around p: CGPoint) -> CGPoint {
        let r = CGFloat.random(in: 10...60)
        let theta = Double.random(in: 0..<(2 * .pi))
        return CGPoint(
            x: p.x + r * CGFloat(cos(theta)),
            y: p.y + r * CGFloat(sin(theta))
        )
    }

    private static func computeDegrees(_ model: GraphModel) -> ([String: Int], [String: Int]) {
        var src: [String: Int] = [:]
        var tgt: [String: Int] = [:]
        for e in model.edges {
            src[e.sourceID, default: 0] += 1
            tgt[e.targetID, default: 0] += 1
        }
        return (src, tgt)
    }
}

// MARK: - Barnes-Hut quadtree

/// Axis-aligned square region. Kept as a value type; the tree stores indices
/// into an internal children array to avoid struct-nested-class headaches.
struct Quad {
    var minX: Double
    var minY: Double
    var side: Double

    var maxX: Double { minX + side }
    var maxY: Double { minY + side }
    var cx: Double { minX + side / 2 }
    var cy: Double { minY + side / 2 }

    func contains(_ p: CGPoint) -> Bool {
        Double(p.x) >= minX && Double(p.x) <= maxX &&
        Double(p.y) >= minY && Double(p.y) <= maxY
    }

    /// Split into NW, NE, SW, SE. Returned in a stable order so children[0]
    /// is always NW etc.
    func subdivide() -> [Quad] {
        let h = side / 2
        return [
            Quad(minX: minX,     minY: minY + h, side: h),  // NW (upper-left)
            Quad(minX: minX + h, minY: minY + h, side: h),  // NE
            Quad(minX: minX,     minY: minY,     side: h),  // SW
            Quad(minX: minX + h, minY: minY,     side: h),  // SE
        ]
    }

    func childIndex(for p: CGPoint) -> Int {
        let east = Double(p.x) >= cx
        let north = Double(p.y) >= cy
        if north { return east ? 1 : 0 }
        else     { return east ? 3 : 2 }
    }
}

/// Simple flat quadtree: cells are stored in an array, each cell has either
/// a single node or indices of its four children. Keeps allocator pressure
/// low during step().
struct QuadTree {
    // A cell is a leaf when firstChild == -1. A cell is empty when both node
    // is nil and firstChild == -1.
    struct Cell {
        var quad: Quad
        var node: GraphNode?
        var firstChild: Int = -1  // index of NW child; NE/SW/SE follow contiguously
        var totalMass: Double = 0
        var cx: Double = 0        // mass-weighted centroid
        var cy: Double = 0
    }

    var cells: [Cell]

    init(bounds: Quad) {
        self.cells = [Cell(quad: bounds)]
    }

    mutating func insert(_ node: GraphNode) {
        insert(node: node, cellIndex: 0, depth: 0)
    }

    // Recursive insert with a depth guard. Duplicate positions (or very close
    // ones) would otherwise cause infinite subdivision; we cap at depth 24,
    // which bounds the smallest cell side at side / 2^24 — far below float
    // precision for any realistic graph.
    private mutating func insert(node: GraphNode, cellIndex: Int, depth: Int) {
        if cells[cellIndex].firstChild == -1 {
            if cells[cellIndex].node == nil {
                cells[cellIndex].node = node
                return
            }
            // Split and redistribute both the existing and new node.
            if depth >= 24 {
                // Overflow — just drop the duplicate to avoid runaway recursion.
                return
            }
            let existing = cells[cellIndex].node!
            cells[cellIndex].node = nil
            let children = cells[cellIndex].quad.subdivide()
            let firstChild = cells.count
            for q in children { cells.append(Cell(quad: q)) }
            cells[cellIndex].firstChild = firstChild

            // Re-insert the existing node at this subtree.
            let existingIdx = firstChild + cells[cellIndex].quad.childIndex(for: existing.position)
            insert(node: existing, cellIndex: existingIdx, depth: depth + 1)
        }
        let childIdx = cells[cellIndex].firstChild + cells[cellIndex].quad.childIndex(for: node.position)
        insert(node: node, cellIndex: childIdx, depth: depth + 1)
    }

    /// Populate `totalMass` and centroids by post-order traversal. Every
    /// document node contributes unit mass; high-degree nodes could weight
    /// more but unit-mass keeps the visual balance even.
    mutating func computeAggregates() {
        _ = computeAggregates(cellIndex: 0)
    }

    @discardableResult
    private mutating func computeAggregates(cellIndex: Int) -> (mass: Double, cx: Double, cy: Double) {
        if cells[cellIndex].firstChild == -1 {
            if let node = cells[cellIndex].node {
                cells[cellIndex].totalMass = 1
                cells[cellIndex].cx = Double(node.position.x)
                cells[cellIndex].cy = Double(node.position.y)
                return (1, cells[cellIndex].cx, cells[cellIndex].cy)
            }
            return (0, 0, 0)
        }
        var mass = 0.0
        var sumX = 0.0
        var sumY = 0.0
        for i in 0..<4 {
            let childIdx = cells[cellIndex].firstChild + i
            let (m, cx, cy) = computeAggregates(cellIndex: childIdx)
            mass += m
            sumX += cx * m
            sumY += cy * m
        }
        cells[cellIndex].totalMass = mass
        cells[cellIndex].cx = mass > 0 ? sumX / mass : 0
        cells[cellIndex].cy = mass > 0 ? sumY / mass : 0
        return (mass, cells[cellIndex].cx, cells[cellIndex].cy)
    }

    /// Apply repulsive forces to `target` using Barnes-Hut approximation.
    func applyForce(to target: GraphNode, theta: Double, strength: Double, alpha: Double) {
        guard !target.pinned else { return }
        applyForce(to: target, cellIndex: 0, theta: theta, strength: strength, alpha: alpha)
    }

    private func applyForce(to target: GraphNode, cellIndex: Int, theta: Double, strength: Double, alpha: Double) {
        let cell = cells[cellIndex]
        if cell.totalMass == 0 { return }
        let dx = cell.cx - Double(target.position.x)
        let dy = cell.cy - Double(target.position.y)
        let distSq = dx*dx + dy*dy
        // Tiny epsilon so we don't divide by zero if a node sits on a centroid.
        let dist = sqrt(max(distSq, 1e-6))

        let isLeaf = cell.firstChild == -1
        // Barnes-Hut criterion: use aggregate if far enough, recurse otherwise.
        if isLeaf || (cell.quad.side / dist) < theta {
            // Skip self-interaction.
            if isLeaf, let n = cell.node, n.id == target.id { return }
            let force = strength * cell.totalMass * alpha / distSq
            target.velocity.dx += (dx / dist) * force
            target.velocity.dy += (dy / dist) * force
            return
        }
        for i in 0..<4 {
            applyForce(to: target, cellIndex: cell.firstChild + i, theta: theta, strength: strength, alpha: alpha)
        }
    }
}
