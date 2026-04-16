import SwiftUI
import SecondBrainCore

/// Right-hand control panel for the graph: filters, forces, groups.
struct GraphInspectorView: View {
    @Binding var settings: GraphSettings
    var onRebuild: () -> Void
    var onResetViewport: () -> Void

    @State private var newTagInclude = ""
    @State private var newTagExclude = ""
    @State private var showAddGroup = false

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 14) {
                modeSection
                Divider()
                filtersSection
                Divider()
                displaySection
                Divider()
                forcesSection
                Divider()
                groupsSection
            }
            .padding(14)
        }
    }

    // MARK: - Mode

    private var modeSection: some View {
        VStack(alignment: .leading, spacing: 6) {
            sectionHeader("Mode")
            Picker("", selection: $settings.mode) {
                ForEach(GraphSettings.Mode.allCases) { m in
                    Text(m.displayName).tag(m)
                }
            }
            .pickerStyle(.segmented)
            .labelsHidden()
            if settings.mode == .local {
                HStack {
                    Text("Depth")
                    Stepper(value: $settings.localDepth, in: 1...6) {
                        Text("\(settings.localDepth)")
                    }
                    .labelsHidden()
                }
                .font(.callout)
            }
        }
    }

    // MARK: - Filters

    private var filtersSection: some View {
        VStack(alignment: .leading, spacing: 8) {
            sectionHeader("Filters")
            TextField("Search title or path...", text: $settings.filters.searchText)
                .textFieldStyle(.roundedBorder)

            tagChips(title: "Include tags",
                     tags: $settings.filters.includeTags,
                     inputText: $newTagInclude)

            tagChips(title: "Exclude tags",
                     tags: $settings.filters.excludeTags,
                     inputText: $newTagExclude)

            Toggle("Show orphans", isOn: $settings.filters.showOrphans)
                .accessibilityIdentifier("graph-toggle-orphans")
            Toggle("Show unresolved links", isOn: $settings.filters.showUnresolvedLinks)
                .accessibilityIdentifier("graph-toggle-unresolved")
            Toggle("Show tags as nodes", isOn: $settings.filters.showTagNodes)
                .accessibilityIdentifier("graph-toggle-tagnodes")

            docTypeFilterGrid
        }
        .font(.callout)
    }

    private var docTypeFilterGrid: some View {
        VStack(alignment: .leading, spacing: 4) {
            Text("Doc types").foregroundStyle(.secondary).font(.caption)
            WrapHStack(spacing: 6) {
                ForEach(docTypeOptions, id: \.self) { type in
                    let on = settings.filters.includeDocTypes.contains(type)
                    Button {
                        if on { settings.filters.includeDocTypes.remove(type) }
                        else  { settings.filters.includeDocTypes.insert(type) }
                    } label: {
                        Text(type)
                            .font(.caption)
                            .padding(.horizontal, 8)
                            .padding(.vertical, 3)
                            .background(on ? Color.accentColor.opacity(0.25) : Color.secondary.opacity(0.15))
                            .clipShape(Capsule())
                    }
                    .buttonStyle(.plain)
                }
            }
            if !settings.filters.includeDocTypes.isEmpty {
                Button("Clear doc-type filter") {
                    settings.filters.includeDocTypes = []
                }
                .font(.caption)
                .buttonStyle(.link)
            }
        }
    }

    private let docTypeOptions = ["adr", "runbook", "note", "postmortem", "prd", "prfaq"]

    private func tagChips(title: String, tags: Binding<[String]>, inputText: Binding<String>) -> some View {
        VStack(alignment: .leading, spacing: 4) {
            Text(title).foregroundStyle(.secondary).font(.caption)
            WrapHStack(spacing: 6) {
                ForEach(tags.wrappedValue, id: \.self) { t in
                    HStack(spacing: 4) {
                        Text(t).font(.caption)
                        Button {
                            tags.wrappedValue.removeAll { $0 == t }
                        } label: {
                            Image(systemName: "xmark.circle.fill")
                                .foregroundStyle(.secondary)
                        }
                        .buttonStyle(.plain)
                    }
                    .padding(.horizontal, 8)
                    .padding(.vertical, 3)
                    .background(Color.secondary.opacity(0.15))
                    .clipShape(Capsule())
                }
            }
            HStack {
                TextField("add tag", text: inputText)
                    .textFieldStyle(.roundedBorder)
                    .onSubmit { commitTagInput(tags: tags, inputText: inputText) }
                Button("Add") { commitTagInput(tags: tags, inputText: inputText) }
                    .disabled(inputText.wrappedValue.trimmingCharacters(in: .whitespaces).isEmpty)
            }
        }
    }

    private func commitTagInput(tags: Binding<[String]>, inputText: Binding<String>) {
        let t = inputText.wrappedValue.trimmingCharacters(in: .whitespaces).lowercased()
        guard !t.isEmpty, !tags.wrappedValue.contains(t) else {
            inputText.wrappedValue = ""
            return
        }
        tags.wrappedValue.append(t)
        inputText.wrappedValue = ""
    }

    // MARK: - Display

    private var displaySection: some View {
        VStack(alignment: .leading, spacing: 8) {
            sectionHeader("Display")
            Toggle("Show arrows", isOn: $settings.showArrows)
                .accessibilityIdentifier("graph-toggle-arrows")
            HStack {
                Text("Label size")
                Slider(value: $settings.labelScale, in: 0.5...1.8)
                Text(String(format: "%.1fx", settings.labelScale))
                    .monospacedDigit()
                    .frame(width: 40, alignment: .trailing)
                    .foregroundStyle(.secondary)
            }
            Button("Re-settle Layout") { onResetViewport() }
                .buttonStyle(.bordered)
                .accessibilityIdentifier("graph-btn-resettle")
            Button("Rebuild Graph") { onRebuild() }
                .buttonStyle(.bordered)
                .accessibilityIdentifier("graph-btn-rebuild")
        }
        .font(.callout)
    }

    // MARK: - Forces

    private var forcesSection: some View {
        VStack(alignment: .leading, spacing: 6) {
            sectionHeader("Forces")
            forceSlider("Repel",    value: $settings.forces.repelStrength, range: -600 ... -30)
            forceSlider("Link",     value: $settings.forces.linkStrength, range: 0.1 ... 1.5)
            forceSlider("Distance", value: $settings.forces.linkDistance, range: 20 ... 220)
            forceSlider("Center",   value: $settings.forces.centerStrength, range: 0 ... 0.2)
            forceSlider("Friction", value: $settings.forces.velocityDecay, range: 0.1 ... 0.8)
        }
        .font(.callout)
    }

    private func forceSlider(_ label: String, value: Binding<Double>, range: ClosedRange<Double>) -> some View {
        HStack {
            Text(label).frame(width: 70, alignment: .leading)
            Slider(value: value, in: range)
            Text(String(format: "%.2f", value.wrappedValue))
                .monospacedDigit()
                .font(.caption)
                .frame(width: 44, alignment: .trailing)
                .foregroundStyle(.secondary)
        }
    }

    // MARK: - Groups

    private var groupsSection: some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack {
                sectionHeader("Color Groups")
                Spacer()
                Button {
                    showAddGroup = true
                } label: {
                    Image(systemName: "plus.circle")
                }
                .buttonStyle(.plain)
            }
            if settings.groups.isEmpty {
                Text("No groups yet. Add one to color nodes by query.")
                    .font(.caption)
                    .foregroundStyle(.tertiary)
            } else {
                ForEach(settings.groups) { group in
                    groupRow(group)
                }
            }
        }
        .sheet(isPresented: $showAddGroup) {
            AddGroupSheet(isPresented: $showAddGroup) { newGroup in
                settings.groups.append(newGroup)
            }
        }
    }

    private func groupRow(_ group: GraphGroup) -> some View {
        HStack {
            Circle()
                .fill(Color(graph: group.color))
                .frame(width: 14, height: 14)
            VStack(alignment: .leading, spacing: 1) {
                Text(group.name).font(.callout)
                Text(group.query).font(.caption).foregroundStyle(.secondary)
            }
            Spacer()
            Button {
                settings.groups.removeAll { $0.id == group.id }
            } label: {
                Image(systemName: "trash")
                    .foregroundStyle(.secondary)
            }
            .buttonStyle(.plain)
        }
    }

    private func sectionHeader(_ title: String) -> some View {
        Text(title)
            .font(.headline)
            .foregroundStyle(.primary)
    }
}

// MARK: - AddGroupSheet

private struct AddGroupSheet: View {
    @Binding var isPresented: Bool
    var onAdd: (GraphGroup) -> Void

    @State private var name = ""
    @State private var query = ""
    @State private var color: Color = .pink

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text("New Color Group").font(.headline)
            Text("Query DSL: `type:adr`, `tag:foo`, `path:substring`, `status:accepted`, `orphan`. Clauses are space-separated and AND'd together.")
                .font(.caption)
                .foregroundStyle(.secondary)
            TextField("Name", text: $name)
            TextField("Query", text: $query)
            ColorPicker("Color", selection: $color)
            HStack {
                Button("Cancel") { isPresented = false }
                Spacer()
                Button("Add") {
                    let components = color.resolveGraphColor()
                    let group = GraphGroup(
                        name: name.trimmingCharacters(in: .whitespaces),
                        query: query.trimmingCharacters(in: .whitespaces),
                        color: components
                    )
                    onAdd(group)
                    isPresented = false
                }
                .disabled(name.trimmingCharacters(in: .whitespaces).isEmpty ||
                          query.trimmingCharacters(in: .whitespaces).isEmpty)
                .keyboardShortcut(.return)
            }
        }
        .padding(20)
        .frame(width: 360)
    }
}

// MARK: - WrapHStack

/// Minimal flowing-HStack replacement. SwiftUI still ships no built-in for
/// this at macOS 14; this implementation uses a Layout protocol conformance
/// so chip-style lists wrap at the available width.
private struct WrapHStack<Content: View>: View {
    let spacing: CGFloat
    @ViewBuilder let content: () -> Content

    init(spacing: CGFloat = 6, @ViewBuilder content: @escaping () -> Content) {
        self.spacing = spacing
        self.content = content
    }

    var body: some View {
        FlowLayout(spacing: spacing) {
            content()
        }
    }
}

private struct FlowLayout: Layout {
    var spacing: CGFloat = 6

    func sizeThatFits(proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) -> CGSize {
        let maxWidth = proposal.width ?? 320
        var x: CGFloat = 0
        var y: CGFloat = 0
        var rowHeight: CGFloat = 0
        for sub in subviews {
            let size = sub.sizeThatFits(.unspecified)
            if x + size.width > maxWidth, x > 0 {
                x = 0; y += rowHeight + spacing; rowHeight = 0
            }
            x += size.width + spacing
            rowHeight = max(rowHeight, size.height)
        }
        return CGSize(width: maxWidth, height: y + rowHeight)
    }

    func placeSubviews(in bounds: CGRect, proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) {
        let maxWidth = bounds.width
        var x: CGFloat = bounds.minX
        var y: CGFloat = bounds.minY
        var rowHeight: CGFloat = 0
        for sub in subviews {
            let size = sub.sizeThatFits(.unspecified)
            if x + size.width > bounds.minX + maxWidth, x > bounds.minX {
                x = bounds.minX
                y += rowHeight + spacing
                rowHeight = 0
            }
            sub.place(at: CGPoint(x: x, y: y), proposal: .unspecified)
            x += size.width + spacing
            rowHeight = max(rowHeight, size.height)
        }
    }
}

// MARK: - Color bridges

private extension Color {
    init(graph c: GraphColor) {
        self.init(.sRGB, red: c.r, green: c.g, blue: c.b, opacity: c.a)
    }

    /// Read the SwiftUI Color back into sRGB components for serialization.
    /// Uses NSColor as the bridge so dynamic colors resolve at save time.
    func resolveGraphColor() -> GraphColor {
        let ns = NSColor(self).usingColorSpace(.sRGB) ?? NSColor.systemPink
        return GraphColor(r: Double(ns.redComponent),
                          g: Double(ns.greenComponent),
                          b: Double(ns.blueComponent),
                          a: Double(ns.alphaComponent))
    }
}
