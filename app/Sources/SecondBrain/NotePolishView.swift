import SwiftUI
import SecondBrainCore

/// Pure helpers extracted from NotePolishView so summary formatting and diff
/// gating remain unit-testable without invoking `2nb polish`.
enum NotePolishPresentation {
    static func hasChanges(_ result: PolishResult, applyBacklinks: Bool = false) -> Bool {
        let bodyChanged = result.original != result.polished
        let schemaChanged = result.schemaRepaired == true || !(result.frontmatterFixes ?? []).isEmpty
        let backlinksChanged = applyBacklinks && !(result.backlinkPreviews ?? []).isEmpty
        return bodyChanged || schemaChanged || backlinksChanged
    }

    static func summaryLine(_ result: PolishResult) -> String {
        var parts: [String] = []
        if !result.model.isEmpty {
            parts.append(result.model)
        }
        parts.append(Formatters.duration(result.durationMs))
        if let added = result.linksAdded, !added.isEmpty {
            parts.append("\(added.count) link\(added.count == 1 ? "" : "s") suggested")
        }
        if let repaired = result.linksRepaired, !repaired.isEmpty {
            parts.append("\(repaired.count) link\(repaired.count == 1 ? "" : "s") repaired")
        }
        if let skipped = result.linksSkipped, !skipped.isEmpty {
            parts.append("\(skipped.count) unresolved")
        }
        if let fixes = result.frontmatterFixes, !fixes.isEmpty {
            parts.append("\(fixes.count) schema fix\(fixes.count == 1 ? "" : "es")")
        } else if result.schemaRepaired == true {
            parts.append("schema repaired")
        }
        if let applied = result.backlinksApplied, applied > 0 {
            parts.append("\(applied) backlink\(applied == 1 ? "" : "s") updated")
        } else if let previews = result.backlinkPreviews, !previews.isEmpty {
            parts.append("\(previews.count) backlink preview\(previews.count == 1 ? "" : "s")")
        }
        if let goal = result.goal, !goal.isEmpty {
            parts.append("goal: \(String(goal.prefix(32)))")
        }
        return parts.joined(separator: " · ")
    }

    static func undoBanner(_ result: PolishUndoResult) -> String {
        guard result.reverted else {
            return "\(result.path) is already at its pre-polish version; nothing to undo."
        }
        let restored = result.restoredPaths ?? []
        if restored.count > 1 {
            return "Reverted \(result.path) and \(restored.count - 1) linked note(s): \(restored.joined(separator: ", "))."
        }
        return "Reverted \(result.path) to its pre-polish backup."
    }
}

struct NotePolishView: View {
    @Environment(AppState.self) var appState
    @State private var searchQuery = ""
    @State private var searchResults: [NotePickerItem] = []
    @State private var selectedPath = ""
    @State private var selectedNoteItem: NotePickerItem?
    @State private var goalPrompt = ""
    @State private var suggestLinks = true
    @State private var repairLinks = true
    @State private var applyBacklinks = false
    @State private var preview: PolishResult?
    @State private var isLoadingNotes = false
    @State private var isRunning = false
    @State private var statusMessage: String?
    @State private var statusIsError = false
    @State private var searchTask: Task<Void, Never>?
    @State private var searchGeneration = 0

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            controlsHeader
            Divider()
            statusBanner
            previewBody
                .frame(maxWidth: .infinity, maxHeight: .infinity)
        }
        .task(id: appState.vault?.rootURL) {
            selectedPath = ""
            selectedNoteItem = nil
            preview = nil
            applyBacklinks = false
            scheduleNoteSearch(for: searchQuery, immediate: true)
            if let requested = appState.guiInputBridge.requestedPolishNotePath {
                applyRequestedPolishNotePath(requested)
            } else if let undoPath = appState.canUndoPolishPath {
                applyRequestedPolishNotePath(undoPath)
            }
        }
        .onAppear {
            applyRequestedPolishNotePath(appState.guiInputBridge.requestedPolishNotePath)
            if let cmd = appState.guiInputBridge.pendingTextCommand,
               appState.guiInputBridge.acknowledgedCommandID != cmd.id {
                applyTextCommand(cmd)
            }
            applyButtonCommand(appState.guiInputBridge.pendingButtonCommand)
        }
        .onDisappear {
            searchTask?.cancel()
            searchTask = nil
        }
        .onChange(of: searchQuery) { _, next in
            scheduleNoteSearch(for: next, immediate: false)
        }
        .onChange(of: goalPrompt) { _, _ in
            invalidatePreviewOnOptionChange()
        }
        .onChange(of: suggestLinks) { _, _ in
            invalidatePreviewOnOptionChange()
        }
        .onChange(of: repairLinks) { _, _ in
            invalidatePreviewOnOptionChange()
        }
        .onChange(of: appState.guiInputBridge.requestedPolishNotePath) { _, requested in
            applyRequestedPolishNotePath(requested)
        }
        .onChange(of: appState.guiInputBridge.pendingTextCommand) { _, cmd in
            applyTextCommand(cmd)
        }
        .onChange(of: appState.guiInputBridge.pendingButtonCommand) { _, cmd in
            applyButtonCommand(cmd)
        }
    }

    private var backlinksToggleLabel: String {
        let count = preview?.backlinkPreviews?.count ?? 0
        if count > 0 {
            return "Apply incoming backlinks (\(count) note\(count == 1 ? "" : "s"))"
        }
        return "Apply incoming backlinks"
    }

    private var controlsHeader: some View {
        VStack(alignment: .leading, spacing: 10) {
            // Search-first note picker
            HStack(spacing: 8) {
                HStack(spacing: 6) {
                    Image(systemName: "magnifyingglass")
                        .foregroundStyle(.secondary)
                    TextField("Search notes by title, path, or content…", text: $searchQuery)
                        .textFieldStyle(.plain)
                        .disabled(isRunning)
                        .accessibilityIdentifier("polish.searchField")
                        .onSubmit {
                            scheduleNoteSearch(for: searchQuery, immediate: true)
                        }
                    if !searchQuery.isEmpty {
                        Button {
                            searchQuery = ""
                        } label: {
                            Image(systemName: "xmark.circle.fill")
                                .foregroundStyle(.secondary)
                        }
                        .buttonStyle(.plain)
                        .disabled(isRunning)
                    }
                }
                .padding(.horizontal, 8)
                .padding(.vertical, 5)
                .background(RoundedRectangle(cornerRadius: 6).fill(Color.primary.opacity(0.05)))

                Button {
                    scheduleNoteSearch(for: searchQuery, immediate: true)
                } label: {
                    Image(systemName: "arrow.clockwise")
                }
                .help("Refresh note search")
                .disabled(appState.vault == nil || isLoadingNotes || isRunning)

                if !selectedPath.isEmpty {
                    Button {
                        appState.openNoteInObsidian(relativePath: selectedPath)
                    } label: {
                        Label("Open in Obsidian", systemImage: "arrow.up.forward.app")
                    }
                }
            }

            // Search results list (recent notes when query is empty)
            VStack(alignment: .leading, spacing: 4) {
                HStack {
                    let trimmed = searchQuery.trimmingCharacters(in: .whitespacesAndNewlines)
                    Text(trimmed.isEmpty ? "Recent Notes" : "Matching Notes (\(searchResults.count))")
                        .font(.caption2)
                        .foregroundStyle(.secondary)
                    if isLoadingNotes {
                        ProgressView()
                            .controlSize(.mini)
                    }
                    Spacer()
                    if !selectedPath.isEmpty {
                        Text("Selected: \(selectedPath)")
                            .font(.caption2)
                            .foregroundStyle(Color.accentColor)
                            .lineLimit(1)
                    }
                }

                ScrollView {
                    LazyVStack(alignment: .leading, spacing: 2) {
                        if searchResults.isEmpty {
                            Text(appState.vault == nil ? "Open a vault to search notes." : "No matching notes.")
                                .font(.caption)
                                .foregroundStyle(.secondary)
                                .padding(.vertical, 6)
                                .padding(.horizontal, 8)
                        } else {
                            ForEach(searchResults) { item in
                                Button {
                                    selectNote(path: item.path, item: item)
                                } label: {
                                    HStack(alignment: .top, spacing: 8) {
                                        Image(systemName: selectedPath == item.path ? "checkmark.circle.fill" : "doc.text")
                                            .foregroundStyle(selectedPath == item.path ? Color.accentColor : .secondary)
                                            .font(.caption)
                                            .padding(.top, 2)
                                        VStack(alignment: .leading, spacing: 1) {
                                            HStack(spacing: 6) {
                                                Text(item.displayTitle)
                                                    .font(.caption)
                                                    .fontWeight(.medium)
                                                    .foregroundStyle(.primary)
                                                    .lineLimit(1)
                                                Text(item.path)
                                                    .font(.caption2)
                                                    .foregroundStyle(.secondary)
                                                    .lineLimit(1)
                                            }
                                            if let snippet = item.snippet, !snippet.isEmpty {
                                                Text(snippet)
                                                    .font(.caption2)
                                                    .foregroundStyle(.tertiary)
                                                    .lineLimit(1)
                                            }
                                        }
                                        Spacer()
                                    }
                                    .padding(.horizontal, 8)
                                    .padding(.vertical, 4)
                                    .background(
                                        RoundedRectangle(cornerRadius: 5)
                                            .fill(selectedPath == item.path ? Color.accentColor.opacity(0.12) : Color.clear)
                                    )
                                    .contentShape(Rectangle())
                                }
                                .buttonStyle(.plain)
                                .disabled(isRunning)
                            }
                        }
                    }
                }
                .frame(minHeight: 72, maxHeight: 132)
                .background(RoundedRectangle(cornerRadius: 6).stroke(.quaternary))
                .accessibilityIdentifier("polish.searchResultsList")
            }

            // Optional goal/lens input
            HStack(spacing: 8) {
                Image(systemName: "scope")
                    .foregroundStyle(.secondary)
                TextField("Optional goal or lens (e.g. '3 executive bullets', 'fix tone')…", text: $goalPrompt)
                    .textFieldStyle(.roundedBorder)
                    .disabled(isRunning)
                    .accessibilityIdentifier("polish.goalField")
            }

            // Options + Actions
            HStack(spacing: 14) {
                Toggle("Suggest [[wikilinks]]", isOn: $suggestLinks)
                    .disabled(isRunning)
                Toggle("Repair broken links", isOn: $repairLinks)
                    .disabled(isRunning)
                Toggle(backlinksToggleLabel, isOn: $applyBacklinks)
                    .disabled(isRunning)
                    .accessibilityIdentifier("polish.applyBacklinksToggle")

                Spacer()

                if let canUndoPath = appState.canUndoPolishPath, canUndoPath == selectedPath {
                    Button("Undo Last Polish") {
                        Task { await undoPolish(path: canUndoPath) }
                    }
                    .disabled(isRunning)
                    .accessibilityIdentifier("polish.undoButton")
                }

                Button("Preview Polish") {
                    Task { await runPreview() }
                }
                .disabled(selectedPath.isEmpty || appState.vault == nil || isRunning)
                .accessibilityIdentifier("polish.previewButton")

                Button("Apply to Note") {
                    Task { await applyPolish() }
                }
                .buttonStyle(.borderedProminent)
                .disabled(
                    selectedPath.isEmpty
                        || appState.vault == nil
                        || isRunning
                        || preview.map { !NotePolishPresentation.hasChanges($0, applyBacklinks: applyBacklinks) } ?? true
                )
                .accessibilityIdentifier("polish.applyButton")
            }
        }
        .padding(.horizontal, 16)
        .padding(.vertical, 10)
    }

    @ViewBuilder
    private var statusBanner: some View {
        if let statusMessage {
            HStack(spacing: 8) {
                Image(systemName: statusIsError ? "exclamationmark.triangle.fill" : "checkmark.circle.fill")
                    .foregroundStyle(statusIsError ? .red : .green)
                Text(statusMessage)
                    .font(.caption)
                    .textSelection(.enabled)
                Spacer()
                Button("Dismiss") { self.statusMessage = nil }
                    .controlSize(.small)
            }
            .padding(.horizontal, 16)
            .padding(.vertical, 8)
            .background((statusIsError ? Color.red : Color.green).opacity(0.08))
        }
    }

    @ViewBuilder
    private var previewBody: some View {
        if isRunning {
            VStack(spacing: 10) {
                ProgressView()
                Text("Generating Gemini polish diff…")
                    .font(.callout)
                    .foregroundStyle(.secondary)
                ColdStartHint()
            }
            .frame(maxWidth: .infinity, maxHeight: .infinity)
        } else if let preview {
            ScrollView {
                VStack(alignment: .leading, spacing: 12) {
                    HStack(spacing: 8) {
                        Label(NotePolishPresentation.summaryLine(preview), systemImage: "wand.and.stars")
                            .font(.caption)
                            .foregroundStyle(.secondary)
                        Spacer()
                    }
                    .padding(.top, 10)

                    if let warning = preview.warning, !warning.isEmpty {
                        Label(warning, systemImage: "exclamationmark.triangle")
                            .font(.caption)
                            .foregroundStyle(.orange)
                    }

                    if let fmFixes = preview.frontmatterFixes, !fmFixes.isEmpty {
                        VStack(alignment: .leading, spacing: 4) {
                            Text("Schema & Frontmatter Repairs (\(fmFixes.count))")
                                .font(.caption)
                                .fontWeight(.semibold)
                                .foregroundStyle(.secondary)
                            ForEach(fmFixes) { ff in
                                HStack(spacing: 6) {
                                    Text("\(ff.field): \(ff.from.map { "\($0) → " } ?? "")\(ff.to)")
                                        .font(.caption)
                                    Text(ff.reason)
                                        .font(.caption2)
                                        .foregroundStyle(.blue)
                                        .padding(.horizontal, 6)
                                        .padding(.vertical, 1)
                                        .background(Capsule().fill(Color.blue.opacity(0.12)))
                                }
                            }
                        }
                        .padding(8)
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .background(RoundedRectangle(cornerRadius: 6).fill(Color.primary.opacity(0.03)))
                    }

                    if preview.original != preview.polished {
                        DiffView(original: preview.original, modified: preview.polished)
                            .frame(minHeight: 180)
                            .overlay(RoundedRectangle(cornerRadius: 6).stroke(.quaternary))
                    } else if !NotePolishPresentation.hasChanges(preview, applyBacklinks: applyBacklinks) {
                        ContentUnavailableView(
                            "Note Already Clean",
                            systemImage: "checkmark.seal",
                            description: Text("Gemini found no structural, clarity, schema, or link changes to suggest for \(preview.path).")
                        )
                    }

                    if let backlinks = preview.backlinkPreviews, !backlinks.isEmpty {
                        VStack(alignment: .leading, spacing: 8) {
                            HStack {
                                Label("Incoming Backlinks (\(backlinks.count) referencing note\(backlinks.count == 1 ? "" : "s"))", systemImage: "link")
                                    .font(.subheadline)
                                    .fontWeight(.semibold)
                                Spacer()
                                Text(applyBacklinks ? "Will apply on Apply" : "Preview only (toggle ON to write)")
                                    .font(.caption2)
                                    .foregroundStyle(applyBacklinks ? Color.accentColor : .secondary)
                            }

                            ForEach(backlinks) { item in
                                VStack(alignment: .leading, spacing: 6) {
                                    HStack(spacing: 6) {
                                        Text(item.path)
                                            .font(.caption)
                                            .fontWeight(.medium)
                                        Text(item.applied ? "Applied" : "Preview")
                                            .font(.caption2)
                                            .foregroundStyle(item.applied ? .green : .secondary)
                                            .padding(.horizontal, 6)
                                            .padding(.vertical, 1)
                                            .background(Capsule().fill((item.applied ? Color.green : Color.secondary).opacity(0.12)))
                                        Spacer()
                                        ForEach(item.changes, id: \.self) { change in
                                            Text(change)
                                                .font(.caption2)
                                                .foregroundStyle(.blue)
                                                .padding(.horizontal, 5)
                                                .padding(.vertical, 1)
                                                .background(Capsule().fill(Color.blue.opacity(0.1)))
                                        }
                                    }
                                    DiffView(original: item.original, modified: item.polished)
                                        .frame(minHeight: 90, maxHeight: 200)
                                        .overlay(RoundedRectangle(cornerRadius: 6).stroke(.quaternary))
                                }
                                .padding(8)
                                .background(RoundedRectangle(cornerRadius: 6).fill(Color.primary.opacity(0.025)))
                            }
                        }
                        .accessibilityIdentifier("polish.backlinksPreview")
                    }
                }
                .padding(.horizontal, 16)
                .padding(.bottom, 16)
            }
        } else {
            ContentUnavailableView(
                "AI Note Polish",
                systemImage: "wand.and.stars",
                description: Text("Search for a note, optionally enter a goal or lens, and click Preview Polish before writing any changes.")
            )
        }
    }

    // MARK: - Actions

    private func scheduleNoteSearch(for rawQuery: String, immediate: Bool) {
        searchTask?.cancel()
        searchGeneration += 1
        let gen = searchGeneration
        guard appState.vault != nil else {
            searchResults = []
            isLoadingNotes = false
            return
        }
        isLoadingNotes = true
        searchTask = Task {
            if !immediate {
                try? await Task.sleep(for: .milliseconds(80))
            }
            guard !Task.isCancelled else { return }
            let results = await appState.searchNotesForPicker(query: rawQuery, limit: 50)
            guard !Task.isCancelled, gen == searchGeneration else { return }
            searchResults = results
            isLoadingNotes = false
        }
    }

    private func invalidatePreviewOnOptionChange() {
        guard !isRunning else { return }
        if preview != nil || applyBacklinks {
            preview = nil
            applyBacklinks = false
        }
    }

    private func selectNote(path: String, item: NotePickerItem? = nil) {
        selectedPath = path
        selectedNoteItem = item ?? searchResults.first(where: { $0.path == path })
        preview = nil
        applyBacklinks = false
    }

    private func applyRequestedPolishNotePath(_ requested: String?) {
        guard let requested, !requested.isEmpty, let vault = appState.vault else { return }
        let fileURL = vault.rootURL.appendingPathComponent(requested)
        let inDB = (try? appState.database?.document(atPath: requested)) != nil
        let onDisk = FileManager.default.fileExists(atPath: fileURL.path)
        guard inDB || onDisk else { return }

        if selectedPath != requested {
            selectNote(path: requested)
        }
    }

    private func applyTextCommand(_ cmd: GUITextCommand?) {
        guard let cmd else { return }
        switch cmd.targetIdentifier {
        case "polish.searchField":
            searchQuery = cmd.text
            appState.guiInputBridge.acknowledge(cmd.id)
            scheduleNoteSearch(for: cmd.text, immediate: true)
        case "polish.goalField":
            goalPrompt = cmd.text
            appState.guiInputBridge.acknowledge(cmd.id)
            if cmd.submit && !selectedPath.isEmpty && !isRunning {
                Task { await runPreview() }
            }
        default:
            break
        }
    }

    private func runPreview() async {
        guard !selectedPath.isEmpty, !isRunning else { return }
        isRunning = true
        statusMessage = nil
        applyBacklinks = false
        defer { isRunning = false }
        do {
            preview = try await appState.polishNote(
                path: selectedPath,
                goal: goalPrompt,
                schema: true,
                links: suggestLinks,
                repairLinks: repairLinks,
                backlinks: true,
                applyBacklinks: false,
                write: false
            )
        } catch {
            statusIsError = true
            statusMessage = AppState.boundedRedacted((error as? CLIError)?.errorDescription ?? error.localizedDescription)
        }
    }

    private func applyPolish() async {
        guard !selectedPath.isEmpty, !isRunning else { return }
        isRunning = true
        statusMessage = nil
        let shouldApplyBacklinks = applyBacklinks
        let expectedBacklinks = preview?.backlinkPreviews?.map(\.path) ?? []
        defer { isRunning = false }
        do {
            let applied = try await appState.polishNote(
                path: selectedPath,
                goal: goalPrompt,
                schema: true,
                links: suggestLinks,
                repairLinks: repairLinks,
                backlinks: true,
                applyBacklinks: shouldApplyBacklinks,
                write: true,
                expectedBacklinkPaths: shouldApplyBacklinks ? expectedBacklinks : []
            )
            preview = applied
            appState.canUndoPolishPath = applied.path
            appState.canUndoPolishBacklinkPaths = applied.backlinkPreviews?.filter(\.applied).map(\.path) ?? []
            applyBacklinks = false
            statusIsError = false
            let backlinksNote = (applied.backlinksApplied ?? 0) > 0
                ? " and \(applied.backlinksApplied ?? 0) backlink note(s)"
                : ""
            statusMessage = "Updated \(applied.path)\(backlinksNote) (\(NotePolishPresentation.summaryLine(applied))). A backup snapshot was saved for Undo."
        } catch {
            statusIsError = true
            statusMessage = AppState.boundedRedacted((error as? CLIError)?.errorDescription ?? error.localizedDescription)
        }
    }

    private func undoPolish(path: String) async {
        guard !isRunning else { return }
        isRunning = true
        statusMessage = nil
        defer { isRunning = false }
        do {
            let res = try await appState.undoPolishNote(
                path: path,
                expectedBacklinkPaths: appState.canUndoPolishBacklinkPaths
            )
            appState.canUndoPolishPath = nil
            appState.canUndoPolishBacklinkPaths = []
            preview = nil
            applyBacklinks = false
            statusIsError = false
            statusMessage = NotePolishPresentation.undoBanner(res)
        } catch {
            statusIsError = true
            statusMessage = AppState.boundedRedacted((error as? CLIError)?.errorDescription ?? error.localizedDescription)
        }
    }

    private func applyButtonCommand(_ cmd: GUIButtonCommand?) {
        guard let cmd else { return }
        switch cmd.targetIdentifier {
        case "polish.applyBacklinksToggle":
            if appState.vault == nil || isRunning {
                appState.guiInputBridge.acknowledgeButtonDisabled(cmd.id, label: "polish.applyBacklinksToggle")
            } else {
                applyBacklinks.toggle()
                appState.guiInputBridge.acknowledgeButtonClicked(cmd.id, label: backlinksToggleLabel)
            }
        case "polish.previewButton":
            if selectedPath.isEmpty || appState.vault == nil || isRunning {
                appState.guiInputBridge.acknowledgeButtonDisabled(cmd.id, label: "polish.previewButton")
            } else {
                appState.guiInputBridge.acknowledgeButtonClicked(cmd.id, label: "Preview Polish")
                Task { await runPreview() }
            }
        case "polish.applyButton":
            let hasDiff = preview.map { NotePolishPresentation.hasChanges($0, applyBacklinks: applyBacklinks) } ?? false
            if selectedPath.isEmpty || appState.vault == nil || isRunning || !hasDiff {
                appState.guiInputBridge.acknowledgeButtonDisabled(cmd.id, label: "polish.applyButton")
            } else {
                appState.guiInputBridge.acknowledgeButtonClicked(cmd.id, label: "Apply to Note")
                Task { await applyPolish() }
            }
        case "polish.undoButton":
            guard let path = appState.canUndoPolishPath, path == selectedPath else { return }
            if isRunning {
                appState.guiInputBridge.acknowledgeButtonDisabled(cmd.id, label: "polish.undoButton")
            } else {
                appState.guiInputBridge.acknowledgeButtonClicked(cmd.id, label: "Undo Last Polish")
                Task { await undoPolish(path: path) }
            }
        default:
            break
        }
    }
}
