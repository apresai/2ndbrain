import SwiftUI
import SecondBrainCore

/// Document graph visualization. This file is a slim shell — the heavy
/// lifting lives in `Graph/GraphCanvasView`, `Graph/GraphInspectorView`,
/// and `SecondBrainCore/Graph`. See `app/Sources/SecondBrain/Graph/` for
/// the simulation and settings plumbing.
struct GraphView: View {
    @Environment(AppState.self) var appState
    @State private var simulation: GraphSimulation?
    @State private var selectedNodeID: String?
    @State private var hoverNodeID: String?
    @State private var settings = GraphSettings()
    @State private var lastRebuildToken: Int = -1
    @State private var rebuildError: String?

    var body: some View {
        HSplitView {
            canvasSide
                .frame(minWidth: 500)
            GraphInspectorView(
                settings: $settings,
                onRebuild: { rebuild(force: true) },
                onResetViewport: { simulation?.reheat(to: 0.8) }
            )
            .frame(minWidth: 240, idealWidth: 280, maxWidth: 360)
        }
        .onAppear {
            settings.load(for: appState.vault?.rootURL.path)
            rebuild(force: true)
        }
        .onChange(of: settings) { _, _ in
            settings.save(for: appState.vault?.rootURL.path)
            rebuild(force: false)
        }
        .onChange(of: appState.graphNeedsRebuild) { _, token in
            if token != lastRebuildToken { rebuild(force: false) }
        }
        .onChange(of: appState.currentDocument?.document.id) { _, _ in
            if settings.mode == .local { rebuild(force: false) }
        }
    }

    @ViewBuilder
    private var canvasSide: some View {
        ZStack(alignment: .topTrailing) {
            if let sim = simulation {
                GraphCanvasView(
                    simulation: sim,
                    selectedNodeID: $selectedNodeID,
                    hoverNodeID: $hoverNodeID,
                    onOpenNode: openNode,
                    showArrows: settings.showArrows,
                    labelScale: settings.labelScale
                )
            } else if let err = rebuildError {
                VStack(spacing: 8) {
                    Image(systemName: "exclamationmark.triangle")
                        .font(.largeTitle)
                        .foregroundStyle(.secondary)
                    Text(err).foregroundStyle(.secondary)
                }
                .frame(maxWidth: .infinity, maxHeight: .infinity)
            } else {
                ProgressView("Loading graph...")
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
            }

            if let sim = simulation, sim.model.nodes.isEmpty {
                emptyOverlay
            }
        }
    }

    private var emptyOverlay: some View {
        VStack(spacing: 8) {
            Image(systemName: "point.3.connected.trianglepath.dotted")
                .font(.system(size: 42))
                .foregroundStyle(.tertiary)
            Text("No nodes match the current filters")
                .font(.callout)
                .foregroundStyle(.secondary)
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .background(.ultraThinMaterial)
    }

    // MARK: - Rebuild

    private func rebuild(force: Bool) {
        guard let db = appState.database else {
            rebuildError = "No index available. Run 'Rebuild Index' from the Tools menu."
            return
        }
        lastRebuildToken = appState.graphNeedsRebuild
        let ds = GraphDataSource(database: db)
        do {
            let model: GraphModel
            switch settings.mode {
            case .global:
                model = try ds.buildGlobal(filters: settings.filters, groups: settings.groups)
            case .local:
                guard let rootID = appState.currentDocument?.document.id else {
                    // No active doc — silently fall back to global so the user
                    // still sees something rather than an empty canvas.
                    model = try ds.buildGlobal(filters: settings.filters, groups: settings.groups)
                    break
                }
                model = try ds.buildLocal(
                    rootID: rootID,
                    depth: settings.localDepth,
                    filters: settings.filters,
                    groups: settings.groups
                )
            }

            if let existing = simulation, !force {
                existing.params = settings.forces
                existing.replace(model: model)
            } else {
                simulation = GraphSimulation(model: model, params: settings.forces)
            }
            rebuildError = nil
        } catch {
            rebuildError = "Failed to build graph: \(error.localizedDescription)"
        }
    }

    private func openNode(_ node: GraphNode) {
        guard node.kind == .document, let vault = appState.vault else { return }
        let url = vault.rootURL.appendingPathComponent(node.path)
        appState.openDocument(at: url)
    }
}
