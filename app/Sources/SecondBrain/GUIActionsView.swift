import SwiftUI
#if canImport(AppKit)
import AppKit
#endif

/// Filter scope for the in-app GUI Actions & Status inspector.
enum GUIActionsFilter: String, CaseIterable, Identifiable {
    case all = "All"
    case indexAndSearch = "Index & Search"
    case aiAndConfig = "AI & Config"
    case errors = "Errors"

    var id: String { rawValue }

    func matches(_ event: GUIActionEvent) -> Bool {
        switch self {
        case .all:
            return true
        case .indexAndSearch:
            return event.category == .index || event.category == .search || event.category == .ask
        case .aiAndConfig:
            return event.category == .settings || event.category == .lint || event.category == .polish || event.category == .mcp || event.category == .engine
        case .errors:
            return event.phase == .failed
        }
    }
}

/// Live in-app observability surface (`Activity > Actions & Status`, Cmd+Shift+J)
/// showing the current `GUIStatusSnapshot` and recent structured `GUIActionEvent` feed.
struct GUIActionsView: View {
    @Environment(AppState.self) private var appState
    @State private var filter: GUIActionsFilter = .all
    @State private var searchText = ""
    @State private var expandedEventIDs: Set<String> = []
    @State private var copiedStatusFeedback = false

    private var filteredEvents: [GUIActionEvent] {
        let query = searchText.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        return appState.actionTracker.events.filter { event in
            guard filter.matches(event) else { return false }
            guard !query.isEmpty else { return true }
            if event.action.lowercased().contains(query) { return true }
            if event.summary.lowercased().contains(query) { return true }
            if event.category.rawValue.lowercased().contains(query) { return true }
            if let detail = event.detail?.lowercased(), detail.contains(query) { return true }
            return event.metadata.values.contains { $0.lowercased().contains(query) }
        }
    }

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 14) {
                liveStatusCard
                filterToolbar
                eventFeedCard
            }
            .padding(.horizontal, 18)
            .padding(.bottom, 18)
        }
        .accessibilityIdentifier("guiActions.view")
    }

    // MARK: - Live Status Card

    private var liveStatusCard: some View {
        let status = appState.actionTracker.status
        return ThemedCard(style: .prominent, padding: 14) {
            VStack(alignment: .leading, spacing: 12) {
                HStack(alignment: .center, spacing: 10) {
                    SheetSectionHeader(title: "Live Status & Telemetry", systemImage: "waveform.path.ecg")
                    Spacer()
                    Button(copiedStatusFeedback ? "Copied JSON" : "Copy Status JSON") {
                        copyStatusJSON(status)
                    }
                    .controlSize(.small)
                    .accessibilityIdentifier("guiActions.copyStatusButton")

                    Button("Reveal Log in Finder") {
                        revealActionsLog()
                    }
                    .controlSize(.small)
                    .accessibilityIdentifier("guiActions.revealLogButton")

                    Button("Clear Session") {
                        appState.actionTracker.clearSessionEvents()
                        expandedEventIDs.removeAll()
                    }
                    .controlSize(.small)
                    .accessibilityIdentifier("guiActions.clearButton")
                }

                LazyVGrid(
                    columns: [GridItem(.adaptive(minimum: 220), spacing: 12, alignment: .top)],
                    alignment: .leading,
                    spacing: 10
                ) {
                    statusCell(
                        title: "Vault",
                        systemImage: "externaldrive",
                        primary: status.vaultName ?? "No vault bound",
                        secondary: "\(status.fileCount) file(s) · \(status.docsIndexed) indexed · \(status.embeddingCount) embeddings"
                    )
                    statusCell(
                        title: "Gemini AI",
                        systemImage: "sparkles",
                        primary: "\(status.aiProvider ?? "—") · \(status.genModel ?? "—")",
                        secondary: "Embed: \(status.embedModel ?? "—") · Ready: embed=\(status.embedAvailable ? "yes" : "no"), gen=\(status.genAvailable ? "yes" : "no")\(status.portabilityStatus.map { " · \($0)" } ?? "")"
                    )
                    statusCell(
                        title: "Index",
                        systemImage: "arrow.triangle.2.circlepath",
                        primary: indexSummaryText(status),
                        secondary: indexDetailText(status),
                        highlightError: status.lastIndexError
                    )
                    statusCell(
                        title: "Last Search",
                        systemImage: "magnifyingglass",
                        primary: searchSummaryText(status),
                        secondary: searchDetailText(status)
                    )
                    statusCell(
                        title: "Last Ask",
                        systemImage: "text.bubble",
                        primary: status.lastAskQuestion ?? "No Ask queries yet",
                        secondary: askDetailText(status)
                    )
                }

                if let lastError = status.lastError, !lastError.isEmpty {
                    HStack(alignment: .top, spacing: 6) {
                        Image(systemName: "exclamationmark.triangle.fill")
                            .foregroundStyle(.red)
                        Text("Last error: \(lastError)")
                            .font(.caption)
                            .foregroundStyle(.red)
                            .textSelection(.enabled)
                    }
                }

                HStack(spacing: 8) {
                    Text("Log: \(appState.actionTracker.activeActionsLogURL.path)")
                        .font(.caption2.monospaced())
                        .foregroundStyle(.secondary)
                        .lineLimit(1)
                        .truncationMode(.middle)
                        .textSelection(.enabled)
                    Spacer()
                    Text("\(status.eventCount) action(s) recorded")
                        .font(.caption2)
                        .foregroundStyle(.secondary)
                }
            }
        }
    }

    private func statusCell(
        title: String,
        systemImage: String,
        primary: String,
        secondary: String,
        highlightError: String? = nil
    ) -> some View {
        VStack(alignment: .leading, spacing: 3) {
            Label(title, systemImage: systemImage)
                .font(.caption.weight(.semibold))
                .foregroundStyle(.secondary)
            Text(primary)
                .font(.callout.weight(.medium))
                .lineLimit(2)
            Text(secondary)
                .font(.caption2)
                .foregroundStyle(.secondary)
                .lineLimit(2)
            if let highlightError, !highlightError.isEmpty {
                Text(highlightError)
                    .font(.caption2)
                    .foregroundStyle(.red)
                    .lineLimit(2)
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .padding(8)
        .background(Color.secondary.opacity(0.06))
        .clipShape(RoundedRectangle(cornerRadius: AppTheme.compactRadius, style: .continuous))
    }

    private func indexSummaryText(_ status: GUIStatusSnapshot) -> String {
        if status.isIndexing {
            let pct = status.indexFraction.map { " (\(Int(($0 * 100).rounded()))%)" } ?? ""
            return "Running: \(status.indexPhase ?? "indexing")\(pct)"
        }
        return "Idle (\(status.indexPhase ?? "ready"))"
    }

    private func indexDetailText(_ status: GUIStatusSnapshot) -> String {
        if status.embeddingTotal > 0 {
            return "Files: \(status.filesIndexed) · Chunks: \(status.chunksCreated) · Embeds: \(status.embeddingCurrent)/\(status.embeddingTotal)"
        }
        return "Files: \(status.filesIndexed) · Docs: \(status.docsIndexed) · Chunks: \(status.chunksCreated)"
    }

    private func searchSummaryText(_ status: GUIStatusSnapshot) -> String {
        guard let query = status.lastSearchQuery, !query.isEmpty else {
            return "No searches yet"
        }
        return "\"\(query)\" (\(status.lastSearchMode ?? "—"))"
    }

    private func searchDetailText(_ status: GUIStatusSnapshot) -> String {
        let phase = status.lastSearchPhase ?? "idle"
        let hits = status.lastSearchHitCount.map { "\($0) hit(s)" } ?? "—"
        let top = status.lastSearchTopPath.map { " · top: \($0)" } ?? ""
        return "Phase: \(phase) · \(hits)\(top)"
    }

    private func askDetailText(_ status: GUIStatusSnapshot) -> String {
        let state = status.lastAskStatus ?? "idle"
        let sources = status.lastAskSourcesCount.map { "\($0) source(s)" } ?? "—"
        return "State: \(state) · Sources: \(sources)"
    }

    // MARK: - Filter Toolbar

    private var filterToolbar: some View {
        HStack(spacing: 10) {
            Picker("Filter", selection: $filter) {
                ForEach(GUIActionsFilter.allCases) { item in
                    Text(item.rawValue).tag(item)
                }
            }
            .pickerStyle(.segmented)
            .labelsHidden()
            .frame(maxWidth: 420)
            .accessibilityIdentifier("guiActions.filterPicker")

            TextField("Filter actions, summaries, or paths…", text: $searchText)
                .textFieldStyle(.roundedBorder)
                .accessibilityIdentifier("guiActions.filterField")
        }
    }

    // MARK: - Action Event Feed

    private var eventFeedCard: some View {
        let rows = filteredEvents
        return ThemedCard(style: .standard, padding: 12) {
            if rows.isEmpty {
                VStack(spacing: 6) {
                    Image(systemName: "tray")
                        .font(.title3)
                        .foregroundStyle(.secondary)
                    Text("No matching GUI actions recorded yet")
                        .font(.callout)
                        .foregroundStyle(.secondary)
                }
                .frame(maxWidth: .infinity, minHeight: 120)
            } else {
                LazyVStack(alignment: .leading, spacing: 6) {
                    ForEach(rows) { event in
                        GUIActionEventRow(
                            event: event,
                            isExpanded: expandedEventIDs.contains(event.id),
                            onToggle: {
                                if expandedEventIDs.contains(event.id) {
                                    expandedEventIDs.remove(event.id)
                                } else {
                                    expandedEventIDs.insert(event.id)
                                }
                            }
                        )
                        if event.id != rows.last?.id {
                            Divider()
                        }
                    }
                }
            }
        }
    }

    // MARK: - Actions

    private func copyStatusJSON(_ status: GUIStatusSnapshot) {
        let encoder = JSONEncoder()
        encoder.outputFormatting = [.prettyPrinted, .sortedKeys]
        guard let data = try? encoder.encode(status),
              let text = String(data: data, encoding: .utf8) else {
            return
        }
        #if canImport(AppKit)
        NSPasteboard.general.clearContents()
        NSPasteboard.general.setString(text, forType: .string)
        #endif
        copiedStatusFeedback = true
        Task { @MainActor in
            try? await Task.sleep(for: .seconds(1.5))
            copiedStatusFeedback = false
        }
    }

    private func revealActionsLog() {
        #if canImport(AppKit)
        let target = appState.actionTracker.activeActionsLogURL
        if FileManager.default.fileExists(atPath: target.path) {
            NSWorkspace.shared.activateFileViewerSelecting([target])
        } else {
            NSWorkspace.shared.open(target.deletingLastPathComponent())
        }
        #endif
    }
}

private struct GUIActionEventRow: View {
    let event: GUIActionEvent
    let isExpanded: Bool
    let onToggle: () -> Void

    private var phaseSymbol: (name: String, color: Color) {
        switch event.phase {
        case .started: return ("clock", .blue)
        case .progress: return ("arrow.triangle.2.circlepath", .orange)
        case .succeeded: return ("checkmark.circle.fill", .green)
        case .failed: return ("exclamationmark.triangle.fill", .red)
        }
    }

    private var hasDetails: Bool {
        !(event.detail ?? "").isEmpty || !event.metadata.isEmpty
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            Button(action: {
                if hasDetails { onToggle() }
            }) {
                HStack(alignment: .center, spacing: 8) {
                    Text("#\(event.seq)")
                        .font(.caption2.monospaced())
                        .foregroundStyle(.secondary)
                        .frame(minWidth: 32, alignment: .leading)

                    Image(systemName: phaseSymbol.name)
                        .foregroundStyle(phaseSymbol.color)
                        .font(.caption)

                    Text(event.category.displayLabel)
                        .font(.caption2.weight(.semibold))
                        .padding(.horizontal, 6)
                        .padding(.vertical, 2)
                        .background(Color.secondary.opacity(0.12))
                        .clipShape(Capsule())

                    Text(event.action)
                        .font(.caption.monospaced())
                        .foregroundStyle(.secondary)

                    Text(event.summary)
                        .font(.callout)
                        .lineLimit(1)

                    Spacer()

                    if let ms = event.durationMs {
                        Text(Formatters.duration(ms))
                            .font(.caption2.monospaced())
                            .foregroundStyle(.secondary)
                    }

                    Text(Formatters.relativeDate(event.timestamp))
                        .font(.caption2)
                        .foregroundStyle(.secondary)

                    if hasDetails {
                        Image(systemName: isExpanded ? "chevron.down" : "chevron.right")
                            .font(.caption2)
                            .foregroundStyle(.secondary)
                    }
                }
                .contentShape(Rectangle())
            }
            .buttonStyle(.plain)

            if isExpanded && hasDetails {
                VStack(alignment: .leading, spacing: 4) {
                    if let detail = event.detail, !detail.isEmpty {
                        Text(detail)
                            .font(.caption.monospaced())
                            .foregroundStyle(event.phase == .failed ? .red : .primary)
                            .textSelection(.enabled)
                    }
                    if !event.metadata.isEmpty {
                        ForEach(event.metadata.keys.sorted(), id: \.self) { key in
                            HStack(alignment: .top, spacing: 6) {
                                Text("\(key):")
                                    .font(.caption2.monospaced().weight(.semibold))
                                    .foregroundStyle(.secondary)
                                Text(event.metadata[key] ?? "")
                                    .font(.caption2.monospaced())
                                    .textSelection(.enabled)
                            }
                        }
                    }
                }
                .padding(.leading, 40)
                .padding(.bottom, 4)
            }
        }
        .padding(.vertical, 3)
    }
}
