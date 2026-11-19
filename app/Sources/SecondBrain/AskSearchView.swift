import SwiftUI
#if canImport(AppKit)
import AppKit
#endif
@preconcurrency import Quartz

enum AskSearchMode: String, CaseIterable, Identifiable {
    case ask = "Ask Gemini"
    case search = "Hybrid Search"
    var id: String { rawValue }
}

/// Progressive stage of the two-stage search pipeline.
enum SearchPhase: String, Equatable {
    case empty
    case words
    case meaning
    case wordsOnly
}

/// Partitions search results into primary high-confidence hits (`>= 0.5 * topScore`,
/// always keeping at least one hit when non-empty) and trailing weaker hits.
struct PartitionedSearchResults: Equatable {
    let strong: [CLISearchResult]
    let weaker: [CLISearchResult]

    init(_ results: [CLISearchResult]) {
        guard !results.isEmpty else {
            self.strong = []
            self.weaker = []
            return
        }
        let topScore = results.map(\.score).max() ?? 0
        let threshold = topScore * 0.5
        var strongHits: [CLISearchResult] = []
        var weakerHits: [CLISearchResult] = []
        for item in results {
            if item.score >= threshold {
                strongHits.append(item)
            } else {
                weakerHits.append(item)
            }
        }
        if strongHits.isEmpty {
            strongHits.append(results[0])
            weakerHits = Array(results.dropFirst())
        }
        self.strong = strongHits
        self.weaker = weakerHits
    }
}

struct ConversationExchange: Identifiable, Equatable {
    let id = UUID()
    let question: String
    let answer: String
    let sources: [String]
    let rewrittenQuery: String?
    let mode: String
    let warnings: [String]
}

/// Pure helpers extracted from AskSearchView so conversation formatting and
/// degraded-mode detection stay unit-testable without launching the CLI.
enum AskSearchPresentation {
    static func historyTurns(from exchanges: [ConversationExchange]) -> [AIChatTurn] {
        var turns: [AIChatTurn] = []
        turns.reserveCapacity(exchanges.count * 2)
        for exchange in exchanges {
            turns.append(AIChatTurn(role: "user", content: exchange.question))
            turns.append(AIChatTurn(role: "assistant", content: exchange.answer))
        }
        return turns
    }

    static func isDegradedBM25(mode: String, warnings: [String]) -> Bool {
        mode == "bm25" || !warnings.isEmpty
    }
}

struct AskSearchView: View {
    @Environment(AppState.self) var appState
    @State private var mode: AskSearchMode = .ask
    @State private var questionText = ""
    @State private var searchQuery = ""
    @State private var exchanges: [ConversationExchange] = []
    @State private var searchResults: [CLISearchResult] = []
    @State private var lastSearchMode: String = ""
    @State private var searchPhase: SearchPhase = .empty
    @State private var searchGeneration: Int = 0
    @State private var searchTask: Task<Void, Never>?
    @State private var selectedResultID: String?
    @State private var showWeakerMatches = false
    @State private var isAsking = false
    @State private var isHybridPending = false
    @State private var errorMessage: String?
    @State private var copiedCitationBanner: String?
    @State private var quickLookBridge = SearchQuickLookBridge()
    @FocusState private var searchFieldFocused: Bool
    @FocusState private var askFieldFocused: Bool

    private var partitioned: PartitionedSearchResults {
        PartitionedSearchResults(searchResults)
    }

    private var visibleResults: [CLISearchResult] {
        showWeakerMatches ? searchResults : partitioned.strong
    }

    private var selectedResult: CLISearchResult? {
        if let selectedResultID,
           let match = searchResults.first(where: { $0.id == selectedResultID }) {
            return match
        }
        return searchResults.first
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            headerBar
            Divider()
            statusBanners
            Group {
                switch mode {
                case .ask:
                    askBody
                case .search:
                    searchBody
                }
            }
            .frame(maxWidth: .infinity, maxHeight: .infinity)
        }
        .onAppear {
            applyRequestedModeIfNeeded()
            syncTrackerStatus()
            focusActiveInput()
        }
        .onChange(of: appState.requestedAskSearchMode) { _, _ in
            applyRequestedModeIfNeeded()
        }
        .onChange(of: appState.guiInputBridge.pendingTextCommand) { _, cmd in
            applyTextCommand(cmd)
        }
        .onChange(of: appState.guiInputBridge.pendingButtonCommand) { _, cmd in
            applyButtonCommand(cmd)
        }
        .onChange(of: mode) { _, newMode in
            appState.actionTracker.updateStatus { $0.askSearchMode = newMode.rawValue }
            appState.actionTracker.record(
                category: .navigation,
                action: "ask.switch_mode",
                phase: .succeeded,
                summary: "Switched Ask & Search mode to \(newMode.rawValue)",
                metadata: ["mode": newMode.rawValue]
            )
            focusActiveInput()
        }
        .onChange(of: searchPhase) { _, newPhase in
            appState.actionTracker.updateStatus { $0.lastSearchPhase = newPhase.rawValue }
        }
        .onChange(of: appState.vault?.rootURL) { _, _ in
            searchTask?.cancel()
            searchTask = nil
            searchGeneration += 1
            exchanges.removeAll()
            searchResults.removeAll()
            lastSearchMode = ""
            searchPhase = .empty
            selectedResultID = nil
            showWeakerMatches = false
            isHybridPending = false
            errorMessage = nil
        }
        .onDisappear {
            searchTask?.cancel()
            searchTask = nil
        }
    }

    private var headerBar: some View {
        HStack(spacing: 12) {
            Picker("Mode", selection: $mode) {
                ForEach(AskSearchMode.allCases) { m in
                    Text(m.rawValue).tag(m)
                }
            }
            .pickerStyle(.segmented)
            .labelsHidden()
            .frame(maxWidth: 260)
            .accessibilityIdentifier("askSearch.modePicker")

            Spacer()

            if let status = appState.aiStatus {
                Label("\(status.genModel) · \(status.embeddingModel)", systemImage: "sparkles")
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
            }

            if mode == .ask && !exchanges.isEmpty {
                Button("New Chat") {
                    exchanges.removeAll()
                    errorMessage = nil
                    appState.actionTracker.record(
                        category: .ask,
                        action: "ask.clear_conversation",
                        phase: .succeeded,
                        summary: "Cleared Ask Gemini conversation"
                    )
                }
                .controlSize(.small)
            }
        }
        .padding(.horizontal, 16)
        .padding(.vertical, 10)
    }

    @ViewBuilder
    private var statusBanners: some View {
        if let status = appState.aiStatus, (!status.embedAvailable || !status.genAvailable) {
            HStack(spacing: 10) {
                Image(systemName: "exclamationmark.triangle.fill")
                    .foregroundStyle(.orange)
                Text("Gemini models need credentials before asking or semantic search will use embeddings.")
                    .font(.caption)
                Spacer()
                OpenSettingsTabButton("Configure Gemini…", tab: .ai)
                    .controlSize(.small)
            }
            .padding(.horizontal, 16)
            .padding(.vertical, 8)
            .background(Color.orange.opacity(0.08))
        }

        if !appState.lastSemanticWarnings.isEmpty {
            VStack(alignment: .leading, spacing: 2) {
                ForEach(appState.lastSemanticWarnings, id: \.self) { warning in
                    Label(warning, systemImage: "info.circle")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            .padding(.horizontal, 16)
            .padding(.vertical, 6)
            .background(Color.secondary.opacity(0.08))
        }

        if let errorMessage {
            HStack(spacing: 8) {
                Image(systemName: "xmark.octagon.fill")
                    .foregroundStyle(.red)
                Text(errorMessage)
                    .font(.caption)
                    .textSelection(.enabled)
                Spacer()
                Button("Dismiss") { self.errorMessage = nil }
                    .controlSize(.small)
            }
            .padding(.horizontal, 16)
            .padding(.vertical, 8)
            .background(Color.red.opacity(0.08))
        }
    }

    // MARK: - Ask Gemini

    private var askBody: some View {
        VStack(spacing: 0) {
            ScrollViewReader { proxy in
                ScrollView {
                    LazyVStack(alignment: .leading, spacing: 16) {
                        if exchanges.isEmpty && !isAsking {
                            ContentUnavailableView(
                                "Ask Your Vault with Gemini",
                                systemImage: "sparkle.magnifyingglass",
                                description: Text("Ask grounded questions across your indexed Obsidian notes. Follow-up turns keep conversation context.")
                            )
                            .frame(maxWidth: .infinity)
                            .padding(.top, 48)
                        }

                        ForEach(exchanges) { ex in
                            exchangeCard(ex)
                                .id(ex.id)
                        }

                        if isAsking {
                            HStack(spacing: 8) {
                                ProgressView()
                                    .controlSize(.small)
                                VStack(alignment: .leading, spacing: 2) {
                                    Text("Searching vault & querying Gemini…")
                                        .font(.callout)
                                        .foregroundStyle(.secondary)
                                    ColdStartHint()
                                }
                            }
                            .padding(12)
                        }
                    }
                    .padding(16)
                }
                .onChange(of: exchanges.count) { _, _ in
                    if let last = exchanges.last {
                        proxy.scrollTo(last.id, anchor: .bottom)
                    }
                }
            }

            Divider()

            HStack(spacing: 10) {
                TextField("Ask a question about your notes…", text: $questionText, axis: .vertical)
                    .textFieldStyle(.roundedBorder)
                    .lineLimit(1...4)
                    .focused($askFieldFocused)
                    .accessibilityIdentifier("askSearch.askField")
                    .onSubmit {
                        Task { await submitQuestion() }
                    }
                    .disabled(appState.vault == nil || isAsking)

                Button {
                    Task { await submitQuestion() }
                } label: {
                    Label("Ask", systemImage: "paperplane.fill")
                }
                .keyboardShortcut(.return, modifiers: [.command])
                .disabled(appState.vault == nil || isAsking || questionText.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
                .accessibilityIdentifier("askSearch.sendButton")
            }
            .padding(12)
        }
    }

    private func exchangeCard(_ ex: ConversationExchange) -> some View {
        ThemedCard(style: .standard, padding: 14) {
            VStack(alignment: .leading, spacing: 10) {
                HStack {
                    Text(ex.question)
                        .font(.headline)
                        .textSelection(.enabled)
                    Spacer()
                    Text(ex.mode.uppercased())
                        .font(.caption2.monospaced())
                        .foregroundStyle(.secondary)
                        .padding(.horizontal, 6)
                        .padding(.vertical, 2)
                        .background(Color.secondary.opacity(0.12))
                        .clipShape(Capsule())
                }

                if let rewritten = ex.rewrittenQuery, !rewritten.isEmpty {
                    Text("Searched for: \(rewritten)")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }

                Text(ex.answer)
                    .font(.body)
                    .textSelection(.enabled)
                    .frame(maxWidth: .infinity, alignment: .leading)

                if !ex.sources.isEmpty {
                    ScrollView(.horizontal, showsIndicators: false) {
                        HStack(spacing: 6) {
                            ForEach(ex.sources, id: \.self) { src in
                                Button {
                                    appState.openNoteInObsidian(relativePath: src)
                                } label: {
                                    Label(src, systemImage: "arrow.up.forward.app")
                                        .font(.caption)
                                }
                                .buttonStyle(.bordered)
                                .controlSize(.small)
                                .help("Open \(src) in Obsidian")
                            }
                        }
                    }
                }
            }
        }
    }

    // MARK: - Two-Stage Hybrid Search + Split Preview

    private var searchBody: some View {
        VStack(alignment: .leading, spacing: 0) {
            searchInputBar
            Divider()

            HStack(spacing: 0) {
                resultsColumn
                    .frame(width: 330)
                Divider()
                NotePreviewPane(
                    vaultRoot: appState.vault?.rootURL,
                    result: selectedResult,
                    onOpenObsidian: { path in appState.openNoteInObsidian(relativePath: path) },
                    onRevealFinder: { url in NSWorkspace.shared.activateFileViewerSelecting([url]) },
                    onCopyCitation: { hit in copyCitation(hit) }
                )
                .frame(maxWidth: .infinity, maxHeight: .infinity)
            }
            .frame(maxWidth: .infinity, maxHeight: .infinity)
            .background(SearchQuickLookAnchor(bridge: quickLookBridge).frame(width: 0, height: 0))

            Divider()
            searchFooterBar
        }
        .onKeyPress(.upArrow) {
            moveSelection(delta: -1)
            return .handled
        }
        .onKeyPress(.downArrow) {
            moveSelection(delta: 1)
            return .handled
        }
        .onKeyPress(.space) {
            guard !searchFieldFocused, selectedResult != nil else { return .ignored }
            toggleQuickLook()
            return .handled
        }
        .onKeyPress(.return) {
            guard let hit = selectedResult else { return .ignored }
            if NSEvent.modifierFlags.contains(.command) {
                revealSelectedInFinder(hit)
                return .handled
            }
            guard !searchFieldFocused else { return .ignored }
            appState.openNoteInObsidian(relativePath: hit.path)
            return .handled
        }
        .onKeyPress(characters: CharacterSet(charactersIn: "cC")) { press in
            guard press.modifiers.contains(.command),
                  !searchFieldFocused,
                  let hit = selectedResult else {
                return .ignored
            }
            copyCitation(hit)
            return .handled
        }
    }

    private var searchInputBar: some View {
        HStack(spacing: 10) {
            HStack(spacing: 8) {
                Image(systemName: "magnifyingglass")
                    .foregroundStyle(.secondary)
                TextField("Instant keyword + Gemini semantic search…", text: $searchQuery)
                    .textFieldStyle(.plain)
                    .focused($searchFieldFocused)
                    .accessibilityIdentifier("askSearch.searchField")
                    .onSubmit {
                        submitImmediateSearch()
                    }
                    .onChange(of: searchQuery) { _, next in
                        scheduleTwoStageSearch(for: next)
                    }
                    .disabled(appState.vault == nil)
                if !searchQuery.isEmpty {
                    Button {
                        searchQuery = ""
                    } label: {
                        Image(systemName: "xmark.circle.fill")
                            .foregroundStyle(.secondary)
                    }
                    .buttonStyle(.plain)
                }
            }
            .padding(.horizontal, 10)
            .padding(.vertical, 7)
            .background(
                RoundedRectangle(cornerRadius: 8, style: .continuous)
                    .fill(Color.primary.opacity(0.05))
            )

            phasePill

            if let toast = copiedCitationBanner {
                Text("Copied \(toast)")
                    .font(.caption.weight(.medium))
                    .foregroundStyle(.green)
                    .padding(.horizontal, 8)
                    .padding(.vertical, 4)
                    .background(Color.green.opacity(0.12), in: Capsule())
                    .transition(.opacity)
            }
        }
        .padding(.horizontal, 14)
        .padding(.vertical, 10)
    }

    @ViewBuilder
    private var phasePill: some View {
        switch searchPhase {
        case .empty:
            EmptyView()
        case .words:
            HStack(spacing: 6) {
                if isHybridPending {
                    ProgressView()
                        .controlSize(.mini)
                }
                Text("Instant keyword…")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
            .padding(.horizontal, 8)
            .padding(.vertical, 4)
            .background(Color.secondary.opacity(0.1), in: Capsule())
        case .meaning:
            Label("Gemini Hybrid", systemImage: "sparkles")
                .font(.caption.weight(.medium))
                .foregroundStyle(Color.accentColor)
                .padding(.horizontal, 8)
                .padding(.vertical, 4)
                .background(Color.accentColor.opacity(0.12), in: Capsule())
        case .wordsOnly:
            Label("Keyword only (BM25)", systemImage: "text.magnifyingglass")
                .font(.caption)
                .foregroundStyle(.orange)
                .padding(.horizontal, 8)
                .padding(.vertical, 4)
                .background(Color.orange.opacity(0.12), in: Capsule())
        }
    }

    private var resultsColumn: some View {
        ScrollViewReader { proxy in
            ScrollView {
                LazyVStack(alignment: .leading, spacing: 6) {
                    if searchResults.isEmpty {
                        emptySearchColumn
                    } else {
                        ForEach(partitioned.strong) { result in
                            resultRow(result)
                                .id(result.id)
                        }

                        if !partitioned.weaker.isEmpty {
                            weakerDisclosureRow
                            if showWeakerMatches {
                                ForEach(partitioned.weaker) { result in
                                    resultRow(result)
                                        .id(result.id)
                                }
                            }
                        }
                    }
                }
                .padding(10)
            }
            .onChange(of: selectedResultID) { _, newID in
                guard let newID else { return }
                withAnimation(.easeOut(duration: 0.12)) {
                    proxy.scrollTo(newID, anchor: .center)
                }
            }
        }
        .background(Color.primary.opacity(0.015))
    }

    @ViewBuilder
    private var emptySearchColumn: some View {
        if searchPhase == .empty {
            ContentUnavailableView(
                "Type to Search Notes",
                systemImage: "magnifyingglass",
                description: Text("Instant BM25 matches appear as you type, followed by Gemini semantic ranking.")
            )
            .padding(.top, 48)
        } else if isHybridPending {
            HStack(spacing: 8) {
                ProgressView()
                    .controlSize(.small)
                Text("Searching vault…")
                    .font(.callout)
                    .foregroundStyle(.secondary)
            }
            .frame(maxWidth: .infinity, alignment: .center)
            .padding(.top, 48)
        } else {
            ContentUnavailableView(
                "No Matching Notes",
                systemImage: "magnifyingglass",
                description: Text("Try broader terms or run Sync Index if notes were added recently.")
            )
            .padding(.top, 48)
        }
    }

    private var weakerDisclosureRow: some View {
        Button {
            withAnimation(.easeInOut(duration: 0.15)) {
                showWeakerMatches.toggle()
            }
        } label: {
            HStack(spacing: 6) {
                Image(systemName: showWeakerMatches ? "chevron.down" : "chevron.right")
                    .font(.caption2)
                Text("\(partitioned.weaker.count) weaker \(partitioned.weaker.count == 1 ? "match" : "matches")")
                    .font(.caption.weight(.medium))
                Spacer()
            }
            .foregroundStyle(.secondary)
            .padding(.horizontal, 8)
            .padding(.vertical, 6)
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
    }

    private func resultRow(_ result: CLISearchResult) -> some View {
        let isSelected = selectedResult?.id == result.id
        return Button {
            selectedResultID = result.id
        } label: {
            VStack(alignment: .leading, spacing: 4) {
                HStack(alignment: .firstTextBaseline, spacing: 6) {
                    Text(result.title.isEmpty ? result.path : result.title)
                        .font(.callout.weight(.semibold))
                        .lineLimit(1)
                    Spacer(minLength: 4)
                    Text(String(format: "%.2f", result.score))
                        .font(.caption2.monospacedDigit())
                        .foregroundStyle(.secondary)
                }

                HStack(spacing: 4) {
                    Text(result.path)
                        .font(.caption2.monospaced())
                        .foregroundStyle(.secondary)
                        .lineLimit(1)
                        .truncationMode(.middle)
                    if let heading = result.headingPath, !heading.isEmpty {
                        Text("› \(heading)")
                            .font(.caption2)
                            .foregroundStyle(.secondary)
                            .lineLimit(1)
                    }
                }

                if let content = result.content?.trimmingCharacters(in: .whitespacesAndNewlines),
                   !content.isEmpty {
                    Text(content)
                        .font(.caption)
                        .foregroundStyle(.secondary)
                        .lineLimit(2)
                }
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            .padding(.horizontal, 10)
            .padding(.vertical, 8)
            .background(
                RoundedRectangle(cornerRadius: 8, style: .continuous)
                    .fill(isSelected ? Color.accentColor.opacity(0.14) : Color.clear)
            )
            .overlay(
                RoundedRectangle(cornerRadius: 8, style: .continuous)
                    .strokeBorder(isSelected ? Color.accentColor.opacity(0.45) : Color.clear, lineWidth: 1)
            )
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .simultaneousGesture(TapGesture(count: 2).onEnded {
            appState.openNoteInObsidian(relativePath: result.path)
        })
    }

    private var searchFooterBar: some View {
        HStack(spacing: 14) {
            if !searchResults.isEmpty {
                Text("\(searchResults.count) result\(searchResults.count == 1 ? "" : "s")")
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .monospacedDigit()
            }

            Spacer()

            HStack(spacing: 12) {
                legendItem(["↑", "↓"], label: "Move")
                Button {
                    toggleQuickLook()
                } label: {
                    legendItem(["Space"], label: "Quick Look")
                }
                .buttonStyle(.plain)
                .disabled(selectedResult == nil)
                .keyboardShortcut("y", modifiers: .command)

                legendItem(["↩"], label: "Obsidian")
                legendItem(["⌘", "↩"], label: "Finder")
                legendItem(["⌘", "C"], label: "[[Link]]")
            }
        }
        .padding(.horizontal, 14)
        .frame(height: 30)
        .background(.bar)
    }

    private func legendItem(_ caps: [String], label: String) -> some View {
        HStack(spacing: 5) {
            Keycaps(caps: caps, size: 10)
            Text(label)
                .font(.caption2)
                .foregroundStyle(.secondary)
        }
    }

    // MARK: - Actions

    private func applyRequestedModeIfNeeded() {
        guard let requested = appState.requestedAskSearchMode else { return }
        appState.requestedAskSearchMode = nil
        if mode != requested {
            mode = requested
        }
        appState.actionTracker.updateStatus { $0.askSearchMode = mode.rawValue }
        focusActiveInput()
    }

    private func syncTrackerStatus() {
        appState.actionTracker.updateStatus {
            $0.askSearchMode = mode.rawValue
            $0.lastSearchPhase = searchPhase.rawValue
        }
    }

    private func focusActiveInput() {
        let targetMode = mode
        Task { @MainActor in
            try? await Task.sleep(for: .milliseconds(40))
            if targetMode == .search {
                searchFieldFocused = true
            } else {
                askFieldFocused = true
            }
        }
    }

    private func submitImmediateSearch() {
        let trimmed = searchQuery.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty, appState.vault != nil else { return }
        searchTask?.cancel()
        searchGeneration += 1
        let gen = searchGeneration
        searchTask = Task {
            await runTwoStageSearch(query: trimmed, generation: gen, immediateHybrid: true)
        }
    }

    private func applyTextCommand(_ cmd: GUITextCommand?) {
        guard let cmd else { return }
        switch cmd.targetIdentifier {
        case "askSearch.searchField":
            guard mode == .search, appState.vault != nil else { return }
            searchQuery = cmd.text
            appState.guiInputBridge.acknowledge(cmd.id)
            if cmd.submit {
                Task { @MainActor in
                    try? await Task.sleep(for: .milliseconds(35))
                    submitImmediateSearch()
                }
            }
        case "askSearch.askField":
            guard mode == .ask, appState.vault != nil, !isAsking else { return }
            questionText = cmd.text
            appState.guiInputBridge.acknowledge(cmd.id)
            if cmd.submit {
                Task { await submitQuestion() }
            }
        default:
            break
        }
    }

    private func applyButtonCommand(_ cmd: GUIButtonCommand?) {
        guard let cmd, cmd.targetIdentifier == "askSearch.sendButton", mode == .ask else { return }
        let disabled = appState.vault == nil || isAsking || questionText.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
        if disabled {
            appState.guiInputBridge.acknowledgeButtonDisabled(cmd.id, label: "askSearch.sendButton")
        } else {
            appState.guiInputBridge.acknowledgeButtonClicked(cmd.id, label: "Ask")
            Task { await submitQuestion() }
        }
    }

    private func submitQuestion() async {
        let prompt = questionText.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !prompt.isEmpty, appState.vault != nil, !isAsking else { return }
        isAsking = true
        errorMessage = nil
        let priorTurns = AskSearchPresentation.historyTurns(from: exchanges)
        defer { isAsking = false }
        do {
            let response = try await appState.askVault(question: prompt, history: priorTurns)
            exchanges.append(ConversationExchange(
                question: prompt,
                answer: response.answer,
                sources: response.sources,
                rewrittenQuery: response.rewrittenQuery,
                mode: response.mode,
                warnings: response.warnings ?? []
            ))
            questionText = ""
        } catch {
            errorMessage = error.localizedDescription
        }
    }

    private func scheduleTwoStageSearch(for rawQuery: String) {
        searchTask?.cancel()
        searchGeneration += 1
        let gen = searchGeneration
        let trimmed = rawQuery.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty, appState.vault != nil else {
            searchResults = []
            lastSearchMode = ""
            searchPhase = .empty
            selectedResultID = nil
            showWeakerMatches = false
            isHybridPending = false
            return
        }
        searchTask = Task {
            await runTwoStageSearch(query: trimmed, generation: gen, immediateHybrid: false)
        }
    }

    private func runTwoStageSearch(query: String, generation: Int, immediateHybrid: Bool) async {
        errorMessage = nil
        if !immediateHybrid {
            searchPhase = .words
            isHybridPending = true
            do {
                let fast = try await appState.searchVault(query: query, limit: 20, bm25Only: true)
                guard !Task.isCancelled, generation == searchGeneration else { return }
                searchResults = fast.results
                lastSearchMode = fast.mode
                syncSelection(to: fast.results)
            } catch {
                guard !Task.isCancelled, generation == searchGeneration else { return }
            }

            try? await Task.sleep(nanoseconds: 150_000_000)
            guard !Task.isCancelled, generation == searchGeneration else { return }
        } else {
            searchPhase = .words
            isHybridPending = true
        }

        do {
            let hybrid = try await appState.searchVault(query: query, limit: 20, bm25Only: false)
            guard !Task.isCancelled, generation == searchGeneration else { return }
            searchResults = hybrid.results
            lastSearchMode = hybrid.mode
            searchPhase = AskSearchPresentation.isDegradedBM25(
                mode: hybrid.mode,
                warnings: hybrid.warnings ?? []
            ) ? .wordsOnly : .meaning
            isHybridPending = false
            syncSelection(to: hybrid.results)
        } catch {
            guard !Task.isCancelled, generation == searchGeneration else { return }
            isHybridPending = false
            errorMessage = error.localizedDescription
        }
    }

    private func syncSelection(to results: [CLISearchResult]) {
        if let current = selectedResultID, results.contains(where: { $0.id == current }) {
            return
        }
        selectedResultID = results.first?.id
    }

    private func moveSelection(delta: Int) {
        let items = visibleResults
        guard !items.isEmpty else { return }
        let currentIndex = items.firstIndex(where: { $0.id == selectedResult?.id }) ?? 0
        let nextIndex = max(0, min(items.count - 1, currentIndex + delta))
        selectedResultID = items[nextIndex].id
    }

    private func revealSelectedInFinder(_ result: CLISearchResult) {
        guard let vaultRoot = appState.vault?.rootURL else { return }
        let fileURL = vaultRoot.appendingPathComponent(result.path)
        NSWorkspace.shared.activateFileViewerSelecting([fileURL])
    }

    private func copyCitation(_ result: CLISearchResult) {
        let citation = NoteCitation.wikilink(path: result.path, title: result.title)
        NSPasteboard.general.clearContents()
        NSPasteboard.general.setString(citation, forType: .string)
        withAnimation(.easeInOut(duration: 0.15)) {
            copiedCitationBanner = citation
        }
        Task {
            try? await Task.sleep(nanoseconds: 1_400_000_000)
            if copiedCitationBanner == citation {
                withAnimation(.easeInOut(duration: 0.15)) {
                    copiedCitationBanner = nil
                }
            }
        }
    }

    private func toggleQuickLook() {
        guard let hit = selectedResult, let vaultRoot = appState.vault?.rootURL else { return }
        let url = vaultRoot.appendingPathComponent(hit.path)
        quickLookBridge.toggle(url: url)
    }
}

@MainActor
private final class SearchQuickLookBridge {
    weak var anchorView: QuickLookAnchorNSView?

    func toggle(url: URL) {
        anchorView?.previewURL = url
        guard let panel = QLPreviewPanel.shared() else { return }
        if QLPreviewPanel.sharedPreviewPanelExists(), panel.isVisible {
            panel.orderOut(nil)
            return
        }
        anchorView?.window?.makeFirstResponder(anchorView)
        panel.reloadData()
        panel.makeKeyAndOrderFront(nil)
    }
}

@MainActor
private final class QuickLookAnchorNSView: NSView, @preconcurrency QLPreviewPanelDataSource {
    var previewURL: URL?

    override var acceptsFirstResponder: Bool { true }

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

private struct SearchQuickLookAnchor: NSViewRepresentable {
    let bridge: SearchQuickLookBridge

    func makeNSView(context: Context) -> QuickLookAnchorNSView {
        let view = QuickLookAnchorNSView()
        bridge.anchorView = view
        return view
    }

    func updateNSView(_ nsView: QuickLookAnchorNSView, context: Context) {
        bridge.anchorView = nsView
    }
}
