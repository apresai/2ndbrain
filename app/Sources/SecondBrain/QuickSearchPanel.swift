import AppKit
import Observation
@preconcurrency import Quartz
import SwiftUI

enum HUDMode: String, Equatable {
    case search
    case ask
}

/// Floating borderless `NSPanel` for quick search and Ask Gemini:
/// floats above fullscreen apps and Spaces and bridges `QLPreviewPanel`.
@MainActor
final class QuickSearchPanelWindow: NSPanel, @preconcurrency QLPreviewPanelDataSource {
    var previewURL: URL?

    init(contentRect: NSRect) {
        super.init(
            contentRect: contentRect,
            styleMask: [.borderless, .fullSizeContentView, .nonactivatingPanel],
            backing: .buffered,
            defer: false
        )
        isFloatingPanel = true
        level = .floating
        collectionBehavior = [.canJoinAllSpaces, .fullScreenAuxiliary, .transient]
        isOpaque = false
        backgroundColor = .clear
        hasShadow = true
        hidesOnDeactivate = false
        isMovableByWindowBackground = false
    }

    override var canBecomeKey: Bool { true }
    override var canBecomeMain: Bool { false }

    nonisolated override func acceptsPreviewPanelControl(_ panel: QLPreviewPanel!) -> Bool {
        true
    }

    nonisolated override func beginPreviewPanelControl(_ panel: QLPreviewPanel!) {
        MainActor.assumeIsolated {
            panel?.dataSource = self
        }
    }

    nonisolated override func endPreviewPanelControl(_ panel: QLPreviewPanel!) {
        MainActor.assumeIsolated {
            panel?.dataSource = nil
        }
    }

    func numberOfPreviewItems(in panel: QLPreviewPanel!) -> Int {
        previewURL == nil ? 0 : 1
    }

    func previewPanel(_ panel: QLPreviewPanel!, previewItemAt index: Int) -> (any QLPreviewItem)! {
        previewURL as NSURL?
    }
}

/// View model shared between the floating HUD view and `QuickSearchPanelController`.
@Observable @MainActor
final class QuickSearchViewModel {
    var mode: HUDMode = .search
    var query: String = ""
    var results: [CLISearchResult] = []
    var selectedResultID: String?
    var showWeakerMatches = false
    var phase: SearchPhase = .empty
    var isSearching = false
    var isAsking = false
    var askResponse: CLIAskResponse?
    var errorMessage: String?
    var copiedToast: String?
    var focusTrigger: Int = 0

    @ObservationIgnored private var searchTask: Task<Void, Never>?
    @ObservationIgnored private(set) var searchGeneration: Int = 0

    var partitioned: PartitionedSearchResults {
        PartitionedSearchResults(results)
    }

    var visibleResults: [CLISearchResult] {
        showWeakerMatches ? results : partitioned.strong
    }

    var selectedResult: CLISearchResult? {
        if let selectedResultID, let match = results.first(where: { $0.id == selectedResultID }) {
            return match
        }
        return results.first
    }

    var isExpanded: Bool {
        !query.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
            || !results.isEmpty
            || askResponse != nil
            || isAsking
            || errorMessage != nil
    }

    func toggleMode(appState: AppState?) {
        errorMessage = nil
        mode = (mode == .search) ? .ask : .search
        appState?.actionTracker.record(
            category: .navigation,
            action: "hud.toggle_mode",
            phase: .succeeded,
            summary: "Toggled Quick Search HUD mode to \(mode.rawValue)",
            metadata: ["mode": mode.rawValue]
        )
        if mode == .search {
            askResponse = nil
            if let appState {
                handleQueryChange(appState: appState)
            }
        } else {
            searchTask?.cancel()
            searchTask = nil
            results = []
            selectedResultID = nil
            phase = .empty
            isSearching = false
            appState?.actionTracker.updateStatus { $0.lastSearchPhase = SearchPhase.empty.rawValue }
        }
    }

    func handleQueryChange(appState: AppState) {
        if mode == .search && query.hasPrefix("? ") {
            mode = .ask
            query = String(query.dropFirst(2))
            searchTask?.cancel()
            searchTask = nil
            results = []
            selectedResultID = nil
            phase = .empty
            isSearching = false
            appState.actionTracker.updateStatus { $0.lastSearchPhase = SearchPhase.empty.rawValue }
            return
        }

        guard mode == .search else { return }

        searchTask?.cancel()
        searchGeneration += 1
        let gen = searchGeneration
        let trimmed = query.trimmingCharacters(in: .whitespacesAndNewlines)

        guard !trimmed.isEmpty, appState.vault != nil else {
            results = []
            selectedResultID = nil
            showWeakerMatches = false
            phase = .empty
            isSearching = false
            errorMessage = nil
            appState.actionTracker.updateStatus { $0.lastSearchPhase = SearchPhase.empty.rawValue }
            return
        }

        searchTask = Task { [weak self] in
            await self?.runTwoStageSearch(query: trimmed, generation: gen, immediateHybrid: false, appState: appState)
        }
    }

    func runImmediateSearch(appState: AppState) {
        let trimmed = query.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty, appState.vault != nil else { return }
        searchTask?.cancel()
        searchGeneration += 1
        let gen = searchGeneration
        searchTask = Task { [weak self] in
            await self?.runTwoStageSearch(query: trimmed, generation: gen, immediateHybrid: true, appState: appState)
        }
    }

    func submitAsk(appState: AppState) async {
        let prompt = query.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !prompt.isEmpty, appState.vault != nil, !isAsking else { return }
        isAsking = true
        errorMessage = nil
        defer { isAsking = false }
        do {
            askResponse = try await appState.askVault(question: prompt, history: [])
        } catch {
            errorMessage = error.localizedDescription
        }
    }

    func moveSelection(delta: Int) {
        let items = visibleResults
        guard !items.isEmpty else { return }
        let currentIndex = items.firstIndex(where: { $0.id == selectedResult?.id }) ?? 0
        let nextIndex = max(0, min(items.count - 1, currentIndex + delta))
        selectedResultID = items[nextIndex].id
    }

    func copySelectedCitation() {
        guard let hit = selectedResult else { return }
        let citation = NoteCitation.wikilink(path: hit.path, title: hit.title)
        NSPasteboard.general.clearContents()
        NSPasteboard.general.setString(citation, forType: .string)
        copiedToast = citation
        Task { @MainActor [weak self] in
            try? await Task.sleep(nanoseconds: 1_400_000_000)
            if self?.copiedToast == citation {
                self?.copiedToast = nil
            }
        }
    }

    func resetTransientState() {
        searchTask?.cancel()
        searchTask = nil
        errorMessage = nil
        copiedToast = nil
    }

    private func runTwoStageSearch(
        query: String,
        generation: Int,
        immediateHybrid: Bool,
        appState: AppState
    ) async {
        errorMessage = nil
        phase = .words
        appState.actionTracker.updateStatus { $0.lastSearchPhase = SearchPhase.words.rawValue }
        isSearching = true

        if !immediateHybrid {
            do {
                let fast = try await appState.searchVault(query: query, limit: 20, bm25Only: true)
                guard !Task.isCancelled, generation == searchGeneration else { return }
                results = fast.results
                syncSelection()
            } catch {
                guard !Task.isCancelled, generation == searchGeneration else { return }
            }

            try? await Task.sleep(nanoseconds: 150_000_000)
            guard !Task.isCancelled, generation == searchGeneration else { return }
        }

        do {
            let hybrid = try await appState.searchVault(query: query, limit: 20, bm25Only: false)
            guard !Task.isCancelled, generation == searchGeneration else { return }
            results = hybrid.results
            let nextPhase: SearchPhase = AskSearchPresentation.isDegradedBM25(
                mode: hybrid.mode,
                warnings: hybrid.warnings ?? []
            ) ? .wordsOnly : .meaning
            phase = nextPhase
            appState.actionTracker.updateStatus { $0.lastSearchPhase = nextPhase.rawValue }
            isSearching = false
            syncSelection()
        } catch {
            guard !Task.isCancelled, generation == searchGeneration else { return }
            isSearching = false
            errorMessage = error.localizedDescription
            appState.actionTracker.updateStatus { $0.lastSearchPhase = "failed" }
        }
    }

    private func syncSelection() {
        if let selectedResultID, results.contains(where: { $0.id == selectedResultID }) {
            return
        }
        selectedResultID = results.first?.id
    }
}

/// SwiftUI body rendered inside `QuickSearchPanelWindow`.
struct QuickSearchPanelView: View {
    @Environment(AppState.self) private var appState
    @Bindable var model: QuickSearchViewModel
    let onDismiss: () -> Void
    let onToggleQuickLook: (URL) -> Void

    @FocusState private var inputFocused: Bool

    init(
        model: QuickSearchViewModel = QuickSearchViewModel(),
        onDismiss: @escaping () -> Void = {},
        onToggleQuickLook: @escaping (URL) -> Void = { _ in }
    ) {
        self.model = model
        self.onDismiss = onDismiss
        self.onToggleQuickLook = onToggleQuickLook
    }

    var body: some View {
        VStack(spacing: 0) {
            inputBar

            if model.isExpanded {
                Divider()
                expandedBody
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
                Divider()
                footerBar
            }
        }
        .frame(width: 780, height: model.isExpanded ? 480 : 62)
        .background(.ultraThinMaterial)
        .clipShape(RoundedRectangle(cornerRadius: AppTheme.panelRadius, style: .continuous))
        .overlay(
            RoundedRectangle(cornerRadius: AppTheme.panelRadius, style: .continuous)
                .strokeBorder(Color.primary.opacity(0.14), lineWidth: 1)
        )
        .shadow(color: .black.opacity(0.24), radius: 24, x: 0, y: 12)
        .onAppear {
            inputFocused = true
        }
        .onChange(of: model.focusTrigger) { _, _ in
            Task { @MainActor in
                try? await Task.sleep(for: .milliseconds(30))
                inputFocused = true
            }
        }
    }

    private var inputBar: some View {
        HStack(spacing: 12) {
            Button {
                model.toggleMode(appState: appState)
            } label: {
                HStack(spacing: 5) {
                    Image(systemName: model.mode == .ask ? "sparkles" : "magnifyingglass")
                        .font(.caption.weight(.semibold))
                    Text(model.mode == .ask ? "Ask Gemini" : "Search")
                        .font(.caption.weight(.semibold))
                }
                .padding(.horizontal, 10)
                .padding(.vertical, 5)
                .background(
                    Capsule()
                        .fill(model.mode == .ask ? Color.accentColor.opacity(0.18) : Color.primary.opacity(0.08))
                )
                .foregroundStyle(model.mode == .ask ? Color.accentColor : Color.primary)
            }
            .buttonStyle(.plain)
            .help("Press Tab to switch between Search and Ask Gemini")
            .accessibilityIdentifier("hud.modeButton")

            TextField(
                model.mode == .ask
                    ? "Ask Gemini across your vault…"
                    : "Search notes instantly (or type \"? \" to ask Gemini)…",
                text: $model.query
            )
            .textFieldStyle(.plain)
            .font(.system(size: 16))
            .focused($inputFocused)
            .accessibilityIdentifier("hud.inputField")
            .onChange(of: model.query) { _, _ in
                model.handleQueryChange(appState: appState)
            }
            .onSubmit {
                handleSubmit()
            }
            .disabled(appState.vault == nil)

            if model.isSearching || model.isAsking {
                ProgressView()
                    .controlSize(.small)
            } else if let toast = model.copiedToast {
                Text("Copied \(toast)")
                    .font(.caption.weight(.medium))
                    .foregroundStyle(.green)
            } else if appState.vault == nil {
                Text("No Vault Bound")
                    .font(.caption)
                    .foregroundStyle(.orange)
            } else if model.mode == .search && model.phase == .meaning {
                Label("Hybrid", systemImage: "sparkles")
                    .font(.caption)
                    .foregroundStyle(Color.accentColor)
            } else if model.mode == .search && model.phase == .wordsOnly {
                Text("BM25")
                    .font(.caption)
                    .foregroundStyle(.orange)
            }

            if !model.query.isEmpty {
                Button {
                    model.query = ""
                    model.handleQueryChange(appState: appState)
                } label: {
                    Image(systemName: "xmark.circle.fill")
                        .foregroundStyle(.secondary)
                }
                .buttonStyle(.plain)
            }
        }
        .padding(.horizontal, 18)
        .frame(height: 62)
    }

    @ViewBuilder
    private var expandedBody: some View {
        if let err = model.errorMessage {
            VStack(alignment: .leading, spacing: 8) {
                Label(err, systemImage: "exclamationmark.triangle.fill")
                    .foregroundStyle(.red)
                    .font(.callout)
            }
            .padding(20)
            .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
        } else if model.mode == .search {
            HStack(spacing: 0) {
                searchResultsColumn
                    .frame(width: 300)
                Divider()
                NotePreviewPane(
                    vaultRoot: appState.vault?.rootURL,
                    result: model.selectedResult,
                    onOpenObsidian: { path in
                        appState.openNoteInObsidian(relativePath: path)
                        onDismiss()
                    },
                    onRevealFinder: { url in
                        NSWorkspace.shared.activateFileViewerSelecting([url])
                        onDismiss()
                    },
                    onCopyCitation: { _ in
                        model.copySelectedCitation()
                    }
                )
                .frame(maxWidth: .infinity, maxHeight: .infinity)
            }
        } else {
            askBody
        }
    }

    private var searchResultsColumn: some View {
        ScrollViewReader { proxy in
            ScrollView {
                LazyVStack(alignment: .leading, spacing: 5) {
                    if model.results.isEmpty && !model.isSearching {
                        Text("No matching notes found.")
                            .font(.callout)
                            .foregroundStyle(.secondary)
                            .padding(16)
                    } else {
                        ForEach(model.partitioned.strong) { result in
                            hudResultRow(result)
                                .id(result.id)
                        }
                        if !model.partitioned.weaker.isEmpty {
                            Button {
                                withAnimation(.easeInOut(duration: 0.14)) {
                                    model.showWeakerMatches.toggle()
                                }
                            } label: {
                                HStack(spacing: 6) {
                                    Image(systemName: model.showWeakerMatches ? "chevron.down" : "chevron.right")
                                        .font(.caption2)
                                    Text("\(model.partitioned.weaker.count) weaker \(model.partitioned.weaker.count == 1 ? "match" : "matches")")
                                        .font(.caption.weight(.medium))
                                }
                                .foregroundStyle(.secondary)
                                .padding(.horizontal, 8)
                                .padding(.vertical, 6)
                            }
                            .buttonStyle(.plain)

                            if model.showWeakerMatches {
                                ForEach(model.partitioned.weaker) { result in
                                    hudResultRow(result)
                                        .id(result.id)
                                }
                            }
                        }
                    }
                }
                .padding(8)
            }
            .onChange(of: model.selectedResultID) { _, newID in
                if let newID {
                    proxy.scrollTo(newID, anchor: .center)
                }
            }
        }
    }

    private func hudResultRow(_ result: CLISearchResult) -> some View {
        let isSelected = model.selectedResult?.id == result.id
        return Button {
            model.selectedResultID = result.id
        } label: {
            VStack(alignment: .leading, spacing: 2) {
                HStack {
                    Text(result.title.isEmpty ? result.path : result.title)
                        .font(.callout.weight(.medium))
                        .lineLimit(1)
                    Spacer()
                    Text(String(format: "%.2f", result.score))
                        .font(.caption2.monospacedDigit())
                        .foregroundStyle(.secondary)
                }
                Text(result.path)
                    .font(.caption2.monospaced())
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
                    .truncationMode(.middle)
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            .padding(.horizontal, 9)
            .padding(.vertical, 7)
            .background(
                RoundedRectangle(cornerRadius: 8, style: .continuous)
                    .fill(isSelected ? Color.accentColor.opacity(0.16) : Color.clear)
            )
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .simultaneousGesture(TapGesture(count: 2).onEnded {
            appState.openNoteInObsidian(relativePath: result.path)
            onDismiss()
        })
    }

    private var askBody: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 14) {
                if model.isAsking {
                    HStack(spacing: 10) {
                        ProgressView()
                            .controlSize(.small)
                        VStack(alignment: .leading, spacing: 2) {
                            Text("Asking Gemini across your vault…")
                                .font(.callout)
                                .foregroundStyle(.secondary)
                            ColdStartHint()
                        }
                    }
                    .padding(16)
                } else if let answer = model.askResponse {
                    ThemedCard(style: .standard, padding: 16) {
                        VStack(alignment: .leading, spacing: 10) {
                            HStack {
                                Label("Gemini Answer", systemImage: "sparkles")
                                    .font(.caption.weight(.semibold))
                                    .foregroundStyle(Color.accentColor)
                                Spacer()
                                Text(answer.mode.uppercased())
                                    .font(.caption2.monospaced())
                                    .foregroundStyle(.secondary)
                            }
                            Text(answer.answer)
                                .font(.body)
                                .textSelection(.enabled)

                            if !answer.sources.isEmpty {
                                ScrollView(.horizontal, showsIndicators: false) {
                                    HStack(spacing: 6) {
                                        ForEach(answer.sources, id: \.self) { src in
                                            Button {
                                                appState.openNoteInObsidian(relativePath: src)
                                                onDismiss()
                                            } label: {
                                                Label(src, systemImage: "arrow.up.forward.app")
                                                    .font(.caption)
                                            }
                                            .buttonStyle(.bordered)
                                            .controlSize(.small)
                                        }
                                    }
                                }
                            }
                        }
                    }
                    .padding(16)
                } else {
                    ContentUnavailableView(
                        "Ask Gemini",
                        systemImage: "sparkles",
                        description: Text("Type a question above and press Return for a grounded answer with Obsidian citations.")
                    )
                    .padding(.top, 56)
                }
            }
            .frame(maxWidth: .infinity, alignment: .leading)
        }
    }

    private var footerBar: some View {
        HStack(spacing: 12) {
            HStack(spacing: 5) {
                Keycaps(caps: ["⇥"], size: 10)
                Text("Search / Ask")
                    .font(.caption2)
                    .foregroundStyle(.secondary)
            }

            if model.mode == .search {
                HStack(spacing: 5) {
                    Keycaps(caps: ["↑", "↓"], size: 10)
                    Text("Move")
                        .font(.caption2)
                        .foregroundStyle(.secondary)
                }
                Button {
                    if let hit = model.selectedResult, let root = appState.vault?.rootURL {
                        onToggleQuickLook(root.appendingPathComponent(hit.path))
                    }
                } label: {
                    HStack(spacing: 5) {
                        Keycaps(caps: ["⌘", "Y"], size: 10)
                        Text("Quick Look")
                            .font(.caption2)
                            .foregroundStyle(.secondary)
                    }
                }
                .buttonStyle(.plain)
                .disabled(model.selectedResult == nil)
            }

            Spacer()

            HStack(spacing: 10) {
                HStack(spacing: 5) {
                    Keycaps(caps: ["↩"], size: 10)
                    Text(model.mode == .ask ? "Ask" : "Obsidian")
                        .font(.caption2)
                        .foregroundStyle(.secondary)
                }
                if model.mode == .search {
                    HStack(spacing: 5) {
                        Keycaps(caps: ["⌘", "C"], size: 10)
                        Text("[[Link]]")
                            .font(.caption2)
                            .foregroundStyle(.secondary)
                    }
                }
                HStack(spacing: 5) {
                    Keycaps(caps: ["⎋"], size: 10)
                    Text("Close")
                        .font(.caption2)
                        .foregroundStyle(.secondary)
                }
            }
        }
        .padding(.horizontal, 16)
        .frame(height: 30)
    }

    private func handleSubmit() {
        switch model.mode {
        case .ask:
            Task { await model.submitAsk(appState: appState) }
        case .search:
            if let hit = model.selectedResult, model.phase != .empty {
                appState.openNoteInObsidian(relativePath: hit.path)
                onDismiss()
            } else {
                model.runImmediateSearch(appState: appState)
            }
        }
    }
}

/// Manages the floating `QuickSearchPanelWindow`, keyboard navigation monitor,
/// height transitions, and Quick Look panel lifecycle.
@MainActor
final class QuickSearchPanelController {
    private let appState: AppState
    private let viewModel = QuickSearchViewModel()
    private var panel: QuickSearchPanelWindow?
    private var keyMonitor: Any?
    private var resignObserver: NSObjectProtocol?
    private var isObservingSize = false
    private var lastExpanded = false
    private var pinnedTopY: CGFloat?

    private var isTestEnvironment: Bool {
        ProcessInfo.processInfo.environment["2NB_TEST"] == "1"
    }

    init(appState: AppState) {
        self.appState = appState
    }

    var isPanelVisible: Bool {
        panel?.isVisible == true
    }

    var panelWindow: NSPanel? {
        panel
    }

    func setQueryFromAutomation(_ text: String, submit: Bool) async -> Bool {
        guard let panel, panel.isVisible, appState.vault != nil else { return false }
        let prevGen = viewModel.searchGeneration
        viewModel.query = text
        try? await Task.sleep(for: .milliseconds(40))
        if viewModel.mode == .search && viewModel.searchGeneration == prevGen {
            viewModel.handleQueryChange(appState: appState)
        }
        if submit {
            if viewModel.mode == .ask {
                await viewModel.submitAsk(appState: appState)
            } else {
                viewModel.runImmediateSearch(appState: appState)
            }
        }
        return true
    }

    func toggle() {
        guard !isTestEnvironment else { return }
        if let panel, panel.isVisible {
            hide()
        } else {
            show()
        }
    }

    func show() {
        guard !isTestEnvironment else { return }
        ensurePanel()
        guard let panel else { return }

        let targetHeight: CGFloat = viewModel.isExpanded ? 480 : 62
        lastExpanded = viewModel.isExpanded
        position(panel: panel, height: targetHeight)

        NSApp.activate(ignoringOtherApps: true)
        panel.makeKeyAndOrderFront(nil)
        viewModel.focusTrigger &+= 1
        appState.actionTracker.updateStatus { $0.hudVisible = true }
        appState.actionTracker.record(
            category: .navigation,
            action: "hud.open",
            phase: .succeeded,
            summary: "Opened Quick Ask & Search HUD"
        )
        installMonitors(for: panel)
        observePanelSize()
    }

    func hide() {
        let wasVisible = panel?.isVisible == true
        if QLPreviewPanel.sharedPreviewPanelExists(), let ql = QLPreviewPanel.shared(), ql.isVisible {
            ql.orderOut(nil)
        }
        isObservingSize = false
        removeMonitors()
        viewModel.resetTransientState()
        panel?.orderOut(nil)
        appState.actionTracker.updateStatus { $0.hudVisible = false }
        if wasVisible {
            appState.actionTracker.record(
                category: .navigation,
                action: "hud.close",
                phase: .succeeded,
                summary: "Closed Quick Ask & Search HUD"
            )
        }
    }

    private func ensurePanel() {
        guard panel == nil else { return }
        let frame = NSRect(x: 0, y: 0, width: 780, height: 62)
        let win = QuickSearchPanelWindow(contentRect: frame)
        let rootView = QuickSearchPanelView(
            model: viewModel,
            onDismiss: { [weak self] in self?.hide() },
            onToggleQuickLook: { [weak self] url in self?.toggleQuickLook(for: url) }
        )
        .environment(appState)

        let hosting = NSHostingView(rootView: rootView)
        hosting.frame = win.contentView?.bounds ?? frame
        hosting.autoresizingMask = [.width, .height]
        win.contentView = hosting
        self.panel = win
    }

    private func position(panel: QuickSearchPanelWindow, height: CGFloat) {
        let visible = (NSScreen.main ?? NSScreen.screens.first)?.visibleFrame
            ?? NSRect(x: 0, y: 0, width: 1440, height: 900)
        let width: CGFloat = 780
        let x = visible.midX - width / 2
        let topY = min(visible.maxY - 90, visible.maxY - visible.height * 0.14)
        pinnedTopY = topY
        panel.setFrame(NSRect(x: x, y: topY - height, width: width, height: height), display: true)
    }

    private func observePanelSize() {
        isObservingSize = true
        trackSizeChange()
    }

    private func trackSizeChange() {
        guard isObservingSize else { return }
        withObservationTracking {
            let expanded = viewModel.isExpanded
            if expanded != lastExpanded, let panel, panel.isVisible {
                lastExpanded = expanded
                let newHeight: CGFloat = expanded ? 480 : 62
                let current = panel.frame
                let topY = pinnedTopY ?? current.maxY
                panel.setFrame(
                    NSRect(x: current.origin.x, y: topY - newHeight, width: 780, height: newHeight),
                    display: true,
                    animate: true
                )
            }
        } onChange: { [weak self] in
            Task { @MainActor [weak self] in
                self?.trackSizeChange()
            }
        }
    }

    private func toggleQuickLook(for url: URL) {
        guard let panel else { return }
        panel.previewURL = url
        guard let ql = QLPreviewPanel.shared() else { return }
        if QLPreviewPanel.sharedPreviewPanelExists(), ql.isVisible {
            ql.orderOut(nil)
            return
        }
        ql.reloadData()
        ql.makeKeyAndOrderFront(nil)
    }

    private func installMonitors(for panel: QuickSearchPanelWindow) {
        removeMonitors()

        if LaunchOptions.current.guiControlDir == nil {
            resignObserver = NotificationCenter.default.addObserver(
                forName: NSWindow.didResignKeyNotification,
                object: panel,
                queue: .main
            ) { [weak self] _ in
                Task { @MainActor [weak self] in
                    if QLPreviewPanel.sharedPreviewPanelExists(), QLPreviewPanel.shared()?.isVisible == true {
                        return
                    }
                    self?.hide()
                }
            }
        }

        keyMonitor = NSEvent.addLocalMonitorForEvents(matching: .keyDown) { [weak self, weak panel] event in
            guard let self, let panel, event.window === panel else { return event }
            let mods = event.modifierFlags.intersection(.deviceIndependentFlagsMask)

            switch event.keyCode {
            case 53: // Esc
                if QLPreviewPanel.sharedPreviewPanelExists(), let ql = QLPreviewPanel.shared(), ql.isVisible {
                    ql.orderOut(nil)
                } else {
                    self.hide()
                }
                return nil

            case 48: // Tab
                if mods.isEmpty {
                    self.viewModel.toggleMode(appState: self.appState)
                    return nil
                }

            case 125: // Down Arrow
                if self.viewModel.mode == .search && !self.viewModel.visibleResults.isEmpty {
                    self.viewModel.moveSelection(delta: 1)
                    return nil
                }

            case 126: // Up Arrow
                if self.viewModel.mode == .search && !self.viewModel.visibleResults.isEmpty {
                    self.viewModel.moveSelection(delta: -1)
                    return nil
                }

            case 16: // Command+Y -> Quick Look
                if mods.contains(.command),
                   self.viewModel.mode == .search,
                   let hit = self.viewModel.selectedResult,
                   let root = self.appState.vault?.rootURL {
                    self.toggleQuickLook(for: root.appendingPathComponent(hit.path))
                    return nil
                }

            case 49: // Space (when text view isn't actively focused)
                let editingText = (panel.firstResponder as? NSTextView) != nil
                if !editingText,
                   self.viewModel.mode == .search,
                   let hit = self.viewModel.selectedResult,
                   let root = self.appState.vault?.rootURL {
                    self.toggleQuickLook(for: root.appendingPathComponent(hit.path))
                    return nil
                }

            case 36: // Return (Command+Return reveals in Finder)
                if mods.contains(.command),
                   self.viewModel.mode == .search,
                   let hit = self.viewModel.selectedResult,
                   let root = self.appState.vault?.rootURL {
                    NSWorkspace.shared.activateFileViewerSelecting([root.appendingPathComponent(hit.path)])
                    self.hide()
                    return nil
                }

            case 8: // Command+C -> copy [[wikilink]] unless user highlighted text
                if mods.contains(.command), self.viewModel.mode == .search {
                    let selectedLength = (panel.firstResponder as? NSTextView)?.selectedRange().length ?? 0
                    if selectedLength == 0, self.viewModel.selectedResult != nil {
                        self.viewModel.copySelectedCitation()
                        return nil
                    }
                }

            default:
                break
            }

            return event
        }
    }

    private func removeMonitors() {
        if let keyMonitor {
            NSEvent.removeMonitor(keyMonitor)
            self.keyMonitor = nil
        }
        if let resignObserver {
            NotificationCenter.default.removeObserver(resignObserver)
            self.resignObserver = nil
        }
    }
}
