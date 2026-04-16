import Testing
import Foundation
import CoreGraphics
@testable import SecondBrainCore

private func makeNode(_ id: String, x: CGFloat = 0, y: CGFloat = 0) -> GraphNode {
    GraphNode(id: id, title: id, position: CGPoint(x: x, y: y))
}

@Test("two-node graph converges to linkDistance")
func twoNodeConvergence() {
    let a = makeNode("A", x: -200, y: 0)
    let b = makeNode("B", x:  200, y: 0)
    let model = GraphModel(nodes: [a, b], edges: [GraphEdge(sourceID: "A", targetID: "B")])
    let sim = GraphSimulation(model: model)
    // Run to quiescence.
    for _ in 0..<3000 where sim.step() { }
    let dx = Double(b.position.x - a.position.x)
    let dy = Double(b.position.y - a.position.y)
    let dist = sqrt(dx*dx + dy*dy)
    #expect(abs(dist - sim.params.linkDistance) < 20,
            "expected ~\(sim.params.linkDistance), got \(dist)")
}

@Test("triangle graph converges without explosion")
func triangleStability() {
    let a = makeNode("A", x: 50, y: 0)
    let b = makeNode("B", x: -50, y: 50)
    let c = makeNode("C", x: -50, y: -50)
    let model = GraphModel(nodes: [a, b, c], edges: [
        GraphEdge(sourceID: "A", targetID: "B"),
        GraphEdge(sourceID: "B", targetID: "C"),
        GraphEdge(sourceID: "C", targetID: "A"),
    ])
    let sim = GraphSimulation(model: model)
    for _ in 0..<3000 where sim.step() { }

    func dist(_ p: GraphNode, _ q: GraphNode) -> Double {
        let dx = Double(q.position.x - p.position.x)
        let dy = Double(q.position.y - p.position.y)
        return sqrt(dx*dx + dy*dy)
    }
    let ab = dist(a, b), bc = dist(b, c), ca = dist(c, a)
    // All three edges should be approximately the same length.
    let mean = (ab + bc + ca) / 3
    #expect(abs(ab - mean) < 20)
    #expect(abs(bc - mean) < 20)
    #expect(abs(ca - mean) < 20)
    // No NaN/infinite positions.
    for n in [a, b, c] {
        #expect(n.position.x.isFinite)
        #expect(n.position.y.isFinite)
    }
}

@Test("simulation is stable on an empty graph")
func emptyGraphStable() {
    let sim = GraphSimulation(model: GraphModel(nodes: [], edges: []))
    for _ in 0..<100 { _ = sim.step() }
    #expect(sim.isAtRest)
}

@Test("pinned node does not move")
func pinnedNodeStays() {
    let a = makeNode("A", x: 0, y: 0)
    let b = makeNode("B", x: 500, y: 500)
    a.pinned = true
    let model = GraphModel(nodes: [a, b], edges: [GraphEdge(sourceID: "A", targetID: "B")])
    let sim = GraphSimulation(model: model)
    let startX = a.position.x
    let startY = a.position.y
    for _ in 0..<200 where sim.step() { }
    #expect(a.position.x == startX)
    #expect(a.position.y == startY)
}

@Test("replace preserves positions for surviving nodes")
func replacePreservesExisting() {
    let a = makeNode("A", x: -100, y: 0)
    let b = makeNode("B", x: 100, y: 0)
    let sim = GraphSimulation(model: GraphModel(
        nodes: [a, b],
        edges: [GraphEdge(sourceID: "A", targetID: "B")]
    ))
    for _ in 0..<500 where sim.step() { }
    let aPos = a.position
    let bPos = b.position

    // Build a new model where A and B are preserved but a new node C is added.
    let a2 = makeNode("A")
    let b2 = makeNode("B")
    let c2 = makeNode("C")
    sim.replace(model: GraphModel(
        nodes: [a2, b2, c2],
        edges: [
            GraphEdge(sourceID: "A", targetID: "B"),
            GraphEdge(sourceID: "A", targetID: "C"),
        ]
    ))
    #expect(a2.position == aPos)
    #expect(b2.position == bPos)
    // C got placed somewhere near the centroid, not at origin.
    #expect(!(c2.position.x == 0 && c2.position.y == 0))
}

@Test("large random graph runs step() under performance budget")
func largeGraphPerformance() {
    let nodeCount = 1000
    var nodes: [GraphNode] = []
    nodes.reserveCapacity(nodeCount)
    for i in 0..<nodeCount {
        nodes.append(makeNode("n\(i)",
                              x: CGFloat.random(in: -400...400),
                              y: CGFloat.random(in: -400...400)))
    }
    // Sparse random edges — roughly 2x node count so average degree ~4.
    var edges: [GraphEdge] = []
    for _ in 0..<(nodeCount * 2) {
        let i = Int.random(in: 0..<nodeCount)
        var j = Int.random(in: 0..<nodeCount)
        while j == i { j = Int.random(in: 0..<nodeCount) }
        edges.append(GraphEdge(sourceID: "n\(i)", targetID: "n\(j)"))
    }
    let sim = GraphSimulation(model: GraphModel(nodes: nodes, edges: edges))
    let start = Date()
    for _ in 0..<10 { _ = sim.step() }
    let perStep = Date().timeIntervalSince(start) / 10.0
    // 16 ms per step = 60 fps budget. We give it 30ms headroom for debug
    // builds on CI — release builds should be well below 16ms.
    #expect(perStep < 0.030, "step took \(perStep * 1000)ms on avg (target <30ms debug)")
}

@Test("alphaDecay brings simulation to rest")
func decaysToRest() {
    let a = makeNode("A", x: 100, y: 100)
    let b = makeNode("B", x: -100, y: -100)
    let sim = GraphSimulation(model: GraphModel(
        nodes: [a, b],
        edges: [GraphEdge(sourceID: "A", targetID: "B")]
    ))
    var steps = 0
    while sim.step() && steps < 5000 { steps += 1 }
    #expect(sim.isAtRest)
    #expect(steps < 5000, "simulation should reach rest within 5000 steps, took \(steps)")
}
