import SwiftUI
import AppKit
import SecondBrainCore

/// Bridges the Core Graphics NSGraphCanvas into SwiftUI. Owns nothing itself;
/// its parent view owns the GraphSimulation and passes it down so the same
/// simulation instance survives filter tweaks (which only replace the model).
struct GraphCanvasView: NSViewRepresentable {
    let simulation: GraphSimulation
    @Binding var selectedNodeID: String?
    @Binding var hoverNodeID: String?
    var onOpenNode: (GraphNode) -> Void
    var showArrows: Bool
    var labelScale: Double

    func makeNSView(context: Context) -> NSGraphCanvas {
        let view = NSGraphCanvas(
            simulation: simulation,
            onSelect: { id in
                Task { @MainActor in selectedNodeID = id }
            },
            onHover: { id in
                Task { @MainActor in hoverNodeID = id }
            },
            onOpen: onOpenNode
        )
        view.showArrows = showArrows
        view.labelScale = labelScale
        return view
    }

    func updateNSView(_ view: NSGraphCanvas, context: Context) {
        view.selectedNodeID = selectedNodeID
        view.hoverNodeID = hoverNodeID
        view.showArrows = showArrows
        view.labelScale = labelScale
        view.onOpen = onOpenNode
        view.needsDisplay = true
    }
}

/// The pixel-pushing NSView that renders the graph and drives the display
/// link. Mutable state lives here rather than the SwiftUI wrapper so that
/// pan/zoom during a simulation tick don't trigger SwiftUI redraws.
final class NSGraphCanvas: NSView {
    let simulation: GraphSimulation
    var selectedNodeID: String?
    var hoverNodeID: String?
    var showArrows: Bool = true
    var labelScale: Double = 1.0

    var onSelect: (String?) -> Void
    var onHover: (String?) -> Void
    var onOpen: (GraphNode) -> Void

    // Viewport transform: world → screen = (p - offset) * scale + screenCenter.
    // Start centered on origin at 1x zoom.
    private var scale: CGFloat = 1.0
    private var offset: CGPoint = .zero
    private let minScale: CGFloat = 0.15
    private let maxScale: CGFloat = 4.0

    // Interaction state.
    private var draggingNode: GraphNode?
    private var isPanning = false
    private var panStart: CGPoint = .zero
    private var panStartOffset: CGPoint = .zero
    private var trackingArea: NSTrackingArea?

    private var displayLink: CADisplayLink?

    init(simulation: GraphSimulation, onSelect: @escaping (String?) -> Void,
         onHover: @escaping (String?) -> Void, onOpen: @escaping (GraphNode) -> Void) {
        self.simulation = simulation
        self.onSelect = onSelect
        self.onHover = onHover
        self.onOpen = onOpen
        super.init(frame: .zero)
        wantsLayer = true
        layer?.backgroundColor = NSColor.textBackgroundColor.cgColor
    }

    required init?(coder: NSCoder) { fatalError("init(coder:) not supported") }

    deinit { MainActor.assumeIsolated { stopDisplayLink() } }

    override var isFlipped: Bool { false }  // screen-math origin at bottom-left

    override func viewDidMoveToWindow() {
        super.viewDidMoveToWindow()
        if window != nil { startDisplayLink() }
        else { stopDisplayLink() }
    }

    override func updateTrackingAreas() {
        if let existing = trackingArea { removeTrackingArea(existing) }
        let area = NSTrackingArea(
            rect: bounds,
            options: [.mouseMoved, .activeInKeyWindow, .inVisibleRect],
            owner: self,
            userInfo: nil
        )
        addTrackingArea(area)
        trackingArea = area
    }

    // MARK: - Display link

    // Uses NSView.displayLink(target:selector:) — the callback fires on the
    // main thread automatically, so we don't need to hop off CVDisplayLink's
    // background thread (which trips Swift 6 @MainActor isolation checks).
    private func startDisplayLink() {
        guard displayLink == nil else { return }
        let link = displayLink(target: self, selector: #selector(tick(_:)))
        self.displayLink = link
        link.add(to: .main, forMode: .common)
    }

    private func stopDisplayLink() {
        displayLink?.invalidate()
        displayLink = nil
    }

    @objc private func tick(_ sender: CADisplayLink) {
        _ = simulation.step()
        if window != nil { needsDisplay = true }
    }

    // MARK: - Viewport math

    /// Apply the current pan/zoom to convert a world-space point to the
    /// NSView's coordinate system.
    private func worldToScreen(_ p: CGPoint) -> CGPoint {
        let cx = bounds.midX
        let cy = bounds.midY
        return CGPoint(
            x: (p.x - offset.x) * scale + cx,
            y: (p.y - offset.y) * scale + cy
        )
    }

    private func screenToWorld(_ p: CGPoint) -> CGPoint {
        let cx = bounds.midX
        let cy = bounds.midY
        return CGPoint(
            x: (p.x - cx) / scale + offset.x,
            y: (p.y - cy) / scale + offset.y
        )
    }

    /// Visual node radius grows slowly with degree — `sqrt` keeps the hub
    /// difference legible without drowning the canvas in big blobs.
    private func nodeRadius(for node: GraphNode) -> CGFloat {
        let base = node.kind == .tag ? 4.0 : 5.0
        let boost = sqrt(Double(max(node.degree, 0))) * 1.2
        return CGFloat(base + boost)
    }

    // MARK: - Draw

    override func draw(_ dirtyRect: NSRect) {
        guard let ctx = NSGraphicsContext.current?.cgContext else { return }
        let model = simulation.model

        // Edges first so nodes paint on top.
        drawEdges(ctx: ctx, model: model)
        drawNodes(ctx: ctx, model: model)
    }

    private func drawEdges(ctx: CGContext, model: GraphModel) {
        let selNeighbors = selectedNodeID.map { Set(model.neighbors(of: $0)) } ?? []

        ctx.saveGState()
        for edge in model.edges {
            guard let a = model.nodeByID[edge.sourceID],
                  let b = model.nodeByID[edge.targetID] else { continue }
            let p1 = worldToScreen(a.position)
            let p2 = worldToScreen(b.position)
            if !isLineVisible(p1, p2) { continue }

            let highlighted = isEdgeHighlighted(edge, selNeighbors: selNeighbors)
            let alpha: CGFloat = selectedNodeID == nil ? 0.5 : (highlighted ? 0.9 : 0.1)
            let color = edge.resolved
                ? NSColor.secondaryLabelColor.withAlphaComponent(alpha)
                : NSColor.tertiaryLabelColor.withAlphaComponent(alpha)
            ctx.setStrokeColor(color.cgColor)
            ctx.setLineWidth(highlighted ? 1.5 : 1.0)
            if !edge.resolved {
                ctx.setLineDash(phase: 0, lengths: [4, 3])
            } else {
                ctx.setLineDash(phase: 0, lengths: [])
            }
            ctx.beginPath()
            ctx.move(to: p1)
            ctx.addLine(to: p2)
            ctx.strokePath()

            if showArrows && edge.resolved {
                drawArrowHead(ctx: ctx, from: p1, to: p2, targetRadius: nodeRadius(for: b) * scale, color: color)
            }
        }
        ctx.restoreGState()
    }

    private func drawNodes(ctx: CGContext, model: GraphModel) {
        let selID = selectedNodeID
        let selNeighbors = selID.map { Set(model.neighbors(of: $0)) } ?? []
        let labelVisible = (scale * CGFloat(labelScale)) >= 0.6

        for node in model.nodes {
            let p = worldToScreen(node.position)
            let r = nodeRadius(for: node) * scale
            if !isRectVisible(CGRect(x: p.x - r, y: p.y - r, width: r * 2, height: r * 2)) {
                continue
            }

            let color: NSColor = {
                if let c = node.groupColor { return NSColor(graphColor: c) }
                return NSColor(graphColor: .forDocType(node.docType))
            }()

            let dimmed = (selID != nil && selID != node.id && !selNeighbors.contains(node.id))
            let drawColor = dimmed ? color.withAlphaComponent(0.2) : color
            ctx.setFillColor(drawColor.cgColor)
            ctx.fillEllipse(in: CGRect(x: p.x - r, y: p.y - r, width: r * 2, height: r * 2))

            if selID == node.id {
                ctx.setStrokeColor(NSColor.controlAccentColor.cgColor)
                ctx.setLineWidth(2)
                ctx.strokeEllipse(in: CGRect(x: p.x - r - 1, y: p.y - r - 1,
                                             width: r * 2 + 2, height: r * 2 + 2))
            } else if hoverNodeID == node.id {
                ctx.setStrokeColor(NSColor.labelColor.withAlphaComponent(0.6).cgColor)
                ctx.setLineWidth(1)
                ctx.strokeEllipse(in: CGRect(x: p.x - r - 1, y: p.y - r - 1,
                                             width: r * 2 + 2, height: r * 2 + 2))
            }

            if labelVisible {
                drawLabel(node.title, at: CGPoint(x: p.x, y: p.y - r - 4),
                          dimmed: dimmed, kind: node.kind)
            }
        }
    }

    private func drawLabel(_ text: String, at anchor: CGPoint, dimmed: Bool, kind: GraphNode.Kind) {
        let font = NSFont.systemFont(ofSize: 10, weight: kind == .tag ? .medium : .regular)
        let color = dimmed
            ? NSColor.tertiaryLabelColor
            : NSColor.labelColor
        let attrs: [NSAttributedString.Key: Any] = [
            .font: font,
            .foregroundColor: color,
        ]
        let str = NSAttributedString(string: text, attributes: attrs)
        let size = str.size()
        let origin = CGPoint(x: anchor.x - size.width / 2, y: anchor.y - size.height)
        str.draw(at: origin)
    }

    private func drawArrowHead(ctx: CGContext, from: CGPoint, to: CGPoint,
                               targetRadius: CGFloat, color: NSColor) {
        let dx = to.x - from.x
        let dy = to.y - from.y
        let len = sqrt(dx * dx + dy * dy)
        guard len > 1 else { return }
        let ux = dx / len, uy = dy / len
        // Step back from target center so the arrow lands on the node rim.
        let tip = CGPoint(x: to.x - ux * targetRadius, y: to.y - uy * targetRadius)
        let size: CGFloat = 6
        let leftX = tip.x - ux * size + (-uy) * size * 0.5
        let leftY = tip.y - uy * size + ux * size * 0.5
        let rightX = tip.x - ux * size - (-uy) * size * 0.5
        let rightY = tip.y - uy * size - ux * size * 0.5

        ctx.saveGState()
        ctx.setFillColor(color.cgColor)
        ctx.beginPath()
        ctx.move(to: tip)
        ctx.addLine(to: CGPoint(x: leftX, y: leftY))
        ctx.addLine(to: CGPoint(x: rightX, y: rightY))
        ctx.closePath()
        ctx.fillPath()
        ctx.restoreGState()
    }

    private func isEdgeHighlighted(_ edge: GraphEdge, selNeighbors: Set<String>) -> Bool {
        guard let sel = selectedNodeID else { return false }
        return edge.sourceID == sel || edge.targetID == sel ||
               (selNeighbors.contains(edge.sourceID) && selNeighbors.contains(edge.targetID))
    }

    private func isLineVisible(_ a: CGPoint, _ b: CGPoint) -> Bool {
        let minX = min(a.x, b.x), maxX = max(a.x, b.x)
        let minY = min(a.y, b.y), maxY = max(a.y, b.y)
        return bounds.intersects(CGRect(x: minX, y: minY, width: maxX - minX + 1, height: maxY - minY + 1))
    }

    private func isRectVisible(_ r: CGRect) -> Bool { bounds.intersects(r) }

    // MARK: - Hit testing

    /// Screen-space hit test. Picks the topmost node whose circle contains
    /// the point; ties go to the most-recently-drawn node (iteration order).
    private func hitTest(screenPoint p: CGPoint) -> GraphNode? {
        let model = simulation.model
        var hit: GraphNode?
        for node in model.nodes {
            let center = worldToScreen(node.position)
            let r = nodeRadius(for: node) * scale
            let dx = p.x - center.x
            let dy = p.y - center.y
            if dx * dx + dy * dy <= r * r {
                hit = node
            }
        }
        return hit
    }

    // MARK: - Input events

    override var acceptsFirstResponder: Bool { true }

    override func mouseMoved(with event: NSEvent) {
        let p = convert(event.locationInWindow, from: nil)
        let hit = hitTest(screenPoint: p)
        if hit?.id != hoverNodeID {
            hoverNodeID = hit?.id
            onHover(hit?.id)
            needsDisplay = true
        }
    }

    override func mouseDown(with event: NSEvent) {
        let p = convert(event.locationInWindow, from: nil)
        if let hit = hitTest(screenPoint: p) {
            if event.clickCount >= 2 {
                onOpen(hit)
                return
            }
            draggingNode = hit
            hit.pinned = true
            selectedNodeID = hit.id
            onSelect(hit.id)
        } else {
            isPanning = true
            panStart = p
            panStartOffset = offset
            selectedNodeID = nil
            onSelect(nil)
        }
        needsDisplay = true
    }

    override func mouseDragged(with event: NSEvent) {
        let p = convert(event.locationInWindow, from: nil)
        if let node = draggingNode {
            let world = screenToWorld(p)
            node.position = world
            node.velocity = .zero
            simulation.reheat(to: 0.3)
        } else if isPanning {
            let dx = (p.x - panStart.x) / scale
            let dy = (p.y - panStart.y) / scale
            offset = CGPoint(x: panStartOffset.x - dx, y: panStartOffset.y - dy)
            needsDisplay = true
        }
    }

    override func mouseUp(with event: NSEvent) {
        if let node = draggingNode {
            // Leave the node pinned — matches Obsidian behavior. User can
            // unpin via a future context menu; for now we respect the
            // explicit drag as a strong "put this here" signal.
            _ = node
            draggingNode = nil
        }
        isPanning = false
    }

    override func scrollWheel(with event: NSEvent) {
        // Zoom on pinch/scroll; pan on trackpad two-finger swipe without
        // Cmd. Pattern follows how Maps and Xcode handle it.
        if event.modifierFlags.contains(.command) || event.subtype == .mouseEvent {
            let factor: CGFloat = event.deltaY > 0 ? 1.08 : 0.93
            zoom(by: factor, around: convert(event.locationInWindow, from: nil))
        } else if event.hasPreciseScrollingDeltas {
            offset = CGPoint(
                x: offset.x - event.scrollingDeltaX / scale,
                y: offset.y + event.scrollingDeltaY / scale
            )
            needsDisplay = true
        } else {
            let factor: CGFloat = event.deltaY > 0 ? 1.08 : 0.93
            zoom(by: factor, around: convert(event.locationInWindow, from: nil))
        }
    }

    override func magnify(with event: NSEvent) {
        let factor = 1 + event.magnification
        zoom(by: factor, around: convert(event.locationInWindow, from: nil))
    }

    private func zoom(by factor: CGFloat, around screenPoint: CGPoint) {
        let worldBefore = screenToWorld(screenPoint)
        let newScale = max(minScale, min(maxScale, scale * factor))
        guard newScale != scale else { return }
        scale = newScale
        // Keep the point under the cursor stationary in world space.
        let worldAfter = screenToWorld(screenPoint)
        offset = CGPoint(
            x: offset.x + (worldBefore.x - worldAfter.x),
            y: offset.y + (worldBefore.y - worldAfter.y)
        )
        needsDisplay = true
    }

    override func keyDown(with event: NSEvent) {
        // Space = reset viewport.
        if event.characters == " " {
            scale = 1.0
            offset = .zero
            needsDisplay = true
            return
        }
        super.keyDown(with: event)
    }
}

private extension NSColor {
    convenience init(graphColor c: GraphColor) {
        self.init(srgbRed: c.r, green: c.g, blue: c.b, alpha: c.a)
    }
}
