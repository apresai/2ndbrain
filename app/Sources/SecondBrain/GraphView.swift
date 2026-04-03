import SwiftUI
import SecondBrainCore

struct GraphView: View {
    @Environment(AppState.self) var appState
    @State private var nodes: [GraphNode] = []
    @State private var edges: [GraphEdge] = []
    @State private var selectedNodeID: String?

    var body: some View {
        ZStack {
            // Edges
            Canvas { context, size in
                for edge in edges {
                    guard let from = nodes.first(where: { $0.id == edge.from }),
                          let to = nodes.first(where: { $0.id == edge.to }) else { continue }
                    var path = Path()
                    path.move(to: from.position)
                    path.addLine(to: to.position)
                    context.stroke(path, with: .color(.secondary.opacity(0.3)), lineWidth: 1)
                }
            }

            // Nodes
            ForEach($nodes) { $node in
                Text(node.title)
                    .font(.caption)
                    .padding(.horizontal, 8)
                    .padding(.vertical, 4)
                    .background(
                        node.id == selectedNodeID
                        ? Color.accentColor.opacity(0.3)
                        : nodeColor(for: node.docType)
                    )
                    .clipShape(RoundedRectangle(cornerRadius: 6))
                    .overlay(
                        RoundedRectangle(cornerRadius: 6)
                            .stroke(node.id == selectedNodeID ? Color.accentColor : Color.clear, lineWidth: 2)
                    )
                    .position(node.position)
                    .gesture(
                        DragGesture()
                            .onChanged { value in
                                node.position = value.location
                            }
                    )
                    .onTapGesture {
                        selectedNodeID = node.id
                    }
                    .onTapGesture(count: 2) {
                        openNode(node)
                    }
            }
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .background(Color(nsColor: .controlBackgroundColor))
        .onAppear { buildGraph() }
    }

    private func buildGraph() {
        guard let db = appState.database else { return }

        do {
            let docs = try db.allDocuments()
            let center = CGPoint(x: 400, y: 300)

            // Layout nodes in a circle
            nodes = docs.enumerated().map { index, doc in
                let angle = Double(index) / Double(max(docs.count, 1)) * 2 * .pi
                let radius: Double = min(250, Double(docs.count) * 15)
                let x = center.x + CGFloat(cos(angle) * radius)
                let y = center.y + CGFloat(sin(angle) * radius)
                return GraphNode(
                    id: doc.id,
                    title: doc.title,
                    docType: doc.docType,
                    path: doc.path,
                    position: CGPoint(x: x, y: y)
                )
            }

            // Query edges from links table
            let linkPairs = try db.allLinks()
            edges = linkPairs.map { GraphEdge(from: $0.source, to: $0.target) }
        } catch {
            // Empty graph on error
        }
    }

    private func nodeColor(for type: String) -> Color {
        switch type {
        case "adr": return Color.blue.opacity(0.2)
        case "runbook": return Color.green.opacity(0.2)
        case "postmortem": return Color.red.opacity(0.2)
        default: return Color.gray.opacity(0.15)
        }
    }

    private func openNode(_ node: GraphNode) {
        guard let vault = appState.vault else { return }
        let url = vault.rootURL.appendingPathComponent(node.path)
        appState.openDocument(at: url)
    }
}

struct GraphNode: Identifiable {
    let id: String
    var title: String
    var docType: String
    var path: String
    var position: CGPoint
}

struct GraphEdge: Identifiable {
    let id = UUID()
    let from: String
    let to: String
}
