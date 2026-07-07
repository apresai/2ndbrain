import SwiftUI
import AppKit
import SecondBrainCore

struct LintResultsView: View {
    @Environment(AppState.self) var appState
    @Binding var isPresented: Bool
    var isInline: Bool = false

    /// The finding currently being fixed via the "Set value…" sheet.
    @State private var activeSetValue: ActiveSetValue?
    /// The broken-link finding currently being repaired via the preview sheet.
    @State private var activeRepair: ActiveRepair?
    /// Inline result banner after a fix (green), a stale-finding cleanup
    /// (orange informational), or a failure (orange error).
    @State private var actionMessage: String?
    @State private var actionTone: BannerTone = .success
    /// True while the bulk "Repair drift links" pass is running.
    @State private var bulkBusy = false

    var body: some View {
        VStack(spacing: 0) {
            // Header
            HStack {
                Image(systemName: "checkmark.seal")
                    .foregroundStyle(.secondary)
                Text("Validate Knowledge Base")
                    .font(.title3)
                    .fontWeight(.medium)
                Spacer()
                if let report = appState.lintReport {
                    Text("\(report.filesChecked) files checked")
                        .font(.caption)
                        .foregroundStyle(.tertiary)
                }
            }
            .padding(12)

            if let actionMessage {
                Divider()
                HStack(spacing: 6) {
                    Image(systemName: actionTone.icon)
                        .foregroundStyle(actionTone.color)
                    Text(actionMessage)
                        .font(.caption)
                        .foregroundStyle(.secondary)
                    Spacer()
                }
                .padding(.horizontal, 12)
                .padding(.vertical, 6)
                .background(actionTone.color.opacity(0.08))
            }

            Divider()

            // Content
            if appState.isLinting {
                VStack(spacing: 12) {
                    ProgressView()
                    Text("Checking...")
                        .foregroundStyle(.secondary)
                }
                .frame(maxWidth: .infinity, maxHeight: .infinity)
            } else if let report = appState.lintReport {
                if report.issues.isEmpty {
                    VStack(spacing: 12) {
                        Image(systemName: "checkmark.circle.fill")
                            .font(.system(size: 48))
                            .foregroundStyle(.green)
                        Text("No issues found!")
                            .font(.headline)
                        Text("Your vault structure matches all schemas and contains no broken wikilinks.")
                            .font(.caption)
                            .foregroundStyle(.secondary)
                            .multilineTextAlignment(.center)
                            .padding(.horizontal, 40)
                    }
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
                } else {
                    let brokenPaths = distinctBrokenLinkPaths(report)
                    let brokenCount = brokenLinkFindingCount(report)
                    if brokenCount > 1 {
                        // Bulk: one click repairs every confident case/separator
                        // drift link across all affected files. Safe — repair-links
                        // only rewrites unambiguous matches and skips the rest, so
                        // this can't mis-target; ambiguous/missing links stay for
                        // the per-finding sheet.
                        HStack(spacing: 6) {
                            Image(systemName: "wand.and.stars")
                                .foregroundStyle(.secondary)
                            Text("\(brokenCount) broken links across \(brokenPaths.count) file\(brokenPaths.count == 1 ? "" : "s")")
                                .font(.caption)
                                .foregroundStyle(.secondary)
                            Spacer()
                            Button(bulkBusy ? "Repairing…" : "Repair drift links") {
                                confirmAndRepairAllDrift(brokenPaths)
                            }
                            .controlSize(.small)
                            .disabled(bulkBusy || appState.isLinting)
                        }
                        .padding(.horizontal, 12)
                        .padding(.vertical, 6)
                        Divider()
                    }
                    List(report.issues) { issue in
                        IssueRow(
                            issue: issue,
                            onOpenInObsidian: { openInObsidian(issue) },
                            onSetValue: { setValue in activeSetValue = setValue },
                            onRepair: { repair in activeRepair = repair }
                        )
                    }
                    .listStyle(.inset)
                    // Don't let a per-finding fix race the bulk pass on the same
                    // file (both write atomically, but overlapping snapshots could
                    // clobber an undo). The bulk pass is brief.
                    .disabled(bulkBusy)
                }
            } else {
                VStack(spacing: 12) {
                    Image(systemName: "questionmark.circle")
                        .font(.system(size: 48))
                        .foregroundStyle(.secondary)
                    Text("Not validated yet")
                        .font(.headline)
                    Text("Run a validation scan to check for schema errors or broken links.")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
                .frame(maxWidth: .infinity, maxHeight: .infinity)
            }

            Divider()

            // Footer
            HStack {
                if let report = appState.lintReport, !report.issues.isEmpty {
                    Text("\(report.errors) errors, \(report.warnings) warnings")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
                Spacer()
                if !isInline {
                    Button("Close") {
                        isPresented = false
                    }
                }
                Button("Check Now") {
                    Task { await appState.runLint() }
                }
                .buttonStyle(.borderedProminent)
                .disabled(appState.isLinting)
            }
            .padding(12)
        }
        .frame(width: isInline ? nil : 580, height: isInline ? nil : 480)
        .sheet(item: $activeSetValue) { item in
            SetValueSheet(item: item) { field, value in
                activeSetValue = nil
                actionTone = .success
                actionMessage = "Set \(field) = \(value). Re-checking…"
                Task { await appState.runLint() }
            }
            .environment(appState)
        }
        .sheet(item: $activeRepair) { repair in
            LinkResolutionSheet(
                fix: repair,
                onOpenInObsidian: { openInObsidian(forPath: repair.path) },
                onResolved: { message in
                    activeRepair = nil
                    actionTone = .success
                    actionMessage = "\(message) Re-checking…"
                    Task { await appState.runLint() }
                },
                onStale: { message in
                    // The link no longer exists, so the finding is stale, not
                    // failed: informational banner + re-lint so it disappears.
                    activeRepair = nil
                    actionTone = .info
                    actionMessage = "\(message) Re-checking…"
                    Task { await appState.runLint() }
                }
            )
            .environment(appState)
        }
    }

    /// Open the note in Obsidian (not Finder), falling back to the file's
    /// default handler if Obsidian isn't installed. Unlike the old behavior, the
    /// panel stays open so the user can keep working through findings.
    private func openInObsidian(_ issue: LintIssue) {
        openInObsidian(forPath: issue.path)
    }

    private func openInObsidian(forPath path: String) {
        guard let vault = appState.vault else { return }
        let absURL = vault.rootURL.appendingPathComponent(path)
        // Prefer the name Obsidian itself registers for this folder (handles a
        // vault renamed inside Obsidian); fall back to the folder basename.
        let vaultName = ObsidianRegistry.load()?.vault(at: vault.rootURL)?.name
            ?? vault.rootURL.lastPathComponent
        ObsidianURL.open(vaultName: vaultName, relativePath: path, absoluteFileURL: absURL)
    }

    /// Distinct note paths that have at least one broken-wikilink finding, in
    /// report order. Drives the bulk "Repair drift links" affordance.
    private func distinctBrokenLinkPaths(_ report: LintReport) -> [String] {
        var seen = Set<String>()
        var paths = [String]()
        for issue in report.issues {
            if case .brokenLink = LintFinding.classify(message: issue.message) {
                if seen.insert(issue.path).inserted { paths.append(issue.path) }
            }
        }
        return paths
    }

    /// Total broken-wikilink findings across the report (one note can have
    /// several). The bulk bar shows once there is more than one, since a single
    /// finding is served just as well by its per-finding sheet.
    private func brokenLinkFindingCount(_ report: LintReport) -> Int {
        report.issues.reduce(into: 0) { count, issue in
            if case .brokenLink = LintFinding.classify(message: issue.message) { count += 1 }
        }
    }

    /// Confirm (the pass touches multiple files), then repair every confident
    /// drift link across `paths`, re-lint, and report how many were fixed. Each
    /// file is reversible with `2nb polish <path> --undo`.
    private func confirmAndRepairAllDrift(_ paths: [String]) {
        let alert = NSAlert()
        alert.messageText = "Repair drift links across \(paths.count) files?"
        alert.informativeText = "Fixes every link whose only drift from an existing note is case, spacing, or hyphens. Ambiguous or genuinely missing links are left untouched for a per-finding fix. Each file is reversible (polish --undo)."
        alert.addButton(withTitle: "Repair")
        alert.addButton(withTitle: "Cancel")
        guard alert.runModal() == .alertFirstButtonReturn else { return }

        bulkBusy = true
        actionMessage = nil
        Task {
            do {
                let (repaired, failed) = try await appState.repairAllDrift(paths: paths)
                bulkBusy = false
                let banner = LinkFixOutcome.bulkRepairBanner(repaired: repaired, failed: failed)
                actionTone = banner.tone
                actionMessage = banner.message + " Re-checking…"
                await appState.runLint()
            } catch {
                bulkBusy = false
                actionTone = .error
                actionMessage = (error as? CLIError)?.errorDescription ?? error.localizedDescription
            }
        }
    }
}

/// One validation finding with its remediation affordances. Pure presentation:
/// it classifies the message and surfaces the right buttons, delegating the
/// actual work to the parent via closures.
private struct IssueRow: View {
    let issue: LintIssue
    let onOpenInObsidian: () -> Void
    let onSetValue: (ActiveSetValue) -> Void
    let onRepair: (ActiveRepair) -> Void

    private var finding: LintFinding { LintFinding.classify(message: issue.message) }

    var body: some View {
        HStack(alignment: .top, spacing: 8) {
            Image(systemName: issue.level == "error" ? "exclamationmark.octagon.fill" : "exclamationmark.triangle.fill")
                .foregroundStyle(issue.level == "error" ? .red : .orange)
                .font(.body)

            VStack(alignment: .leading, spacing: 4) {
                HStack {
                    Text(issue.path)
                        .font(.body)
                        .fontWeight(.medium)
                        .lineLimit(1)
                    if let line = issue.line, line > 0 {
                        Text("line \(line)")
                            .font(.caption)
                            .foregroundStyle(.secondary)
                    }
                }

                Text(issue.message)
                    .font(.caption)
                    .foregroundStyle(.secondary)

                HStack(spacing: 8) {
                    Button("Open in Obsidian", action: onOpenInObsidian)
                        .controlSize(.small)

                    switch finding {
                    case let .missingField(field, _):
                        Button("Set value…") {
                            onSetValue(ActiveSetValue(path: issue.path, field: field, allowed: [], currentValue: nil))
                        }
                        .controlSize(.small)
                    case let .invalidEnum(field, value, allowed):
                        Button("Set value…") {
                            onSetValue(ActiveSetValue(path: issue.path, field: field, allowed: allowed, currentValue: value))
                        }
                        .controlSize(.small)
                    case let .brokenLink(target):
                        Button("Fix link…") {
                            onRepair(ActiveRepair(path: issue.path, target: target))
                        }
                        .controlSize(.small)
                    case .parseError, .other:
                        EmptyView()
                    }
                }
            }
            Spacer()
        }
        .padding(.vertical, 4)
    }
}

/// The broken-link finding being repaired in the preview sheet.
struct ActiveRepair: Identifiable {
    let id = UUID()
    let path: String
    let target: String
}

/// The finding being edited in the Set-value sheet. `allowed` non-empty means an
/// enum (render a picker); empty means a free-text required field.
struct ActiveSetValue: Identifiable {
    let id = UUID()
    let path: String
    let field: String
    let allowed: [String]
    let currentValue: String?
}

/// Sheet to set one frontmatter field, fixing a missing-required-field or
/// invalid-enum finding. Enum findings get a picker of valid values; missing
/// fields get a validated text field. The CLI schema-validates the write.
private struct SetValueSheet: View {
    @Environment(AppState.self) private var appState
    @Environment(\.dismiss) private var dismiss
    let item: ActiveSetValue
    /// Called with (field, value) after a successful write so the parent can
    /// dismiss, re-lint, and show a banner.
    let onSaved: (String, String) -> Void

    @State private var selected: String
    @State private var freeText: String = ""
    @State private var saving = false
    @State private var errorText: String?

    init(item: ActiveSetValue, onSaved: @escaping (String, String) -> Void) {
        self.item = item
        self.onSaved = onSaved
        _selected = State(initialValue: item.allowed.first ?? "")
    }

    private var isEnum: Bool { !item.allowed.isEmpty }

    private var resolvedValue: String {
        isEnum ? selected : freeText.trimmingCharacters(in: .whitespacesAndNewlines)
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            Text("Set ‘\(item.field)’")
                .font(.headline)
            Text(item.path)
                .font(.caption)
                .foregroundStyle(.secondary)
                .lineLimit(1)

            if isEnum {
                if let current = item.currentValue {
                    Text("Current value ‘\(current)’ is not allowed.")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
                Picker("Value", selection: $selected) {
                    ForEach(item.allowed, id: \.self) { Text($0).tag($0) }
                }
                .labelsHidden()
                .pickerStyle(.menu)
            } else {
                TextField("Value for \(item.field)", text: $freeText)
                    .textFieldStyle(.roundedBorder)
            }

            if let errorText {
                Text(errorText)
                    .font(.caption)
                    .foregroundStyle(.red)
                    .fixedSize(horizontal: false, vertical: true)
            }

            HStack {
                Spacer()
                Button("Cancel") { dismiss() }
                    .keyboardShortcut(.cancelAction)
                    .disabled(saving)
                Button(saving ? "Saving…" : "Save") { save() }
                    .keyboardShortcut(.defaultAction)
                    .buttonStyle(.borderedProminent)
                    .disabled(saving || resolvedValue.isEmpty)
            }
        }
        .padding(16)
        .frame(width: 360)
    }

    private func save() {
        let value = resolvedValue
        guard !value.isEmpty else { return }
        saving = true
        errorText = nil
        Task {
            do {
                try await appState.setMeta(path: item.path, key: item.field, value: value)
                saving = false
                onSaved(item.field, value)
            } catch {
                saving = false
                errorText = (error as? CLIError)?.errorDescription ?? error.localizedDescription
            }
        }
    }
}

/// Resolves a broken `[[wikilink]]` with no dead ends. On open it concurrently
/// loads a deterministic repair preview (`2nb repair-links` dry run) and ranked
/// "did you mean?" candidates (`2nb suggest-target`), then presents, in priority
/// order: Repair drift (when a confident target exists, with a diff), Did-you-mean
/// suggestions (relink to a chosen note), Create the note, and Unlink (keep the
/// text). Create and Unlink are ALWAYS offered, so every finding has a real fix.
/// Every mutating action is reversible with `polish --undo`. Each action's
/// result is classified by `LinkFixOutcome`: success and stale findings both
/// dismiss and re-lint (green vs informational banner); actionable guidance
/// keeps the sheet open so the user picks another option.
private struct LinkResolutionSheet: View {
    @Environment(AppState.self) private var appState
    @Environment(\.dismiss) private var dismiss
    let fix: ActiveRepair
    /// Open the note in Obsidian (delegated to the parent, which knows the vault).
    let onOpenInObsidian: () -> Void
    /// Called with a past-tense banner message after a successful resolution so
    /// the parent can dismiss, re-lint, and show the green banner.
    let onResolved: (String) -> Void
    /// Called when the link no longer exists (a stale finding) so the parent
    /// can dismiss, re-lint, and show an informational banner instead of a
    /// false success or an in-sheet dead end.
    let onStale: (String) -> Void

    @State private var loading = true
    @State private var repairPreview: PolishResult?
    @State private var suggestions: [SuggestTargetResult] = []
    /// Non-error guidance when an action found the link but could not fix it
    /// (e.g. no confident target): the sheet stays open so the user picks
    /// another option.
    @State private var guidanceText: String?
    @State private var errorText: String?
    @State private var busy = false

    /// The single confident repair, if `repair-links` found one (case/separator
    /// drift to exactly one existing note).
    private var confidentRepair: RepairLinkRepair? {
        repairPreview?.linksRepaired?.first
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text("Fix link")
                .font(.headline)
            Text("[[\(fix.target)]] in \(fix.path)")
                .font(.caption)
                .foregroundStyle(.secondary)
                .lineLimit(1)

            if loading {
                HStack(spacing: 6) {
                    ProgressView().controlSize(.small)
                    Text("Finding fixes…").foregroundStyle(.secondary)
                }
                .frame(maxWidth: .infinity, minHeight: 80)
            } else {
                ScrollView {
                    VStack(alignment: .leading, spacing: 14) {
                        if let r = confidentRepair, let p = repairPreview {
                            repairSection(r, preview: p)
                        }
                        if !filteredSuggestions.isEmpty {
                            suggestSection
                        }
                        createSection
                        unlinkSection
                    }
                    .padding(.vertical, 2)
                }
                .frame(maxHeight: 300)
            }

            if let guidanceText {
                Text(guidanceText)
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
            }

            if let errorText {
                Text(errorText)
                    .font(.caption)
                    .foregroundStyle(.red)
                    .fixedSize(horizontal: false, vertical: true)
            }

            Divider()
            HStack {
                Button("Open in Obsidian") { onOpenInObsidian() }
                    .controlSize(.small)
                    .disabled(busy)
                Spacer()
                Button("Cancel") { dismiss() }
                    .keyboardShortcut(.cancelAction)
                    .disabled(busy)
            }
        }
        .padding(16)
        .frame(width: 480)
        .task { await load() }
    }

    // MARK: Sections

    @ViewBuilder
    private func repairSection(_ r: RepairLinkRepair, preview p: PolishResult) -> some View {
        VStack(alignment: .leading, spacing: 6) {
            Label("Repair drift (recommended)", systemImage: "wand.and.stars")
                .font(.subheadline).fontWeight(.medium)
            Text("[[\(r.raw)]] → [[\(r.newTarget ?? "")]]")
                .font(.callout)
            DiffView(original: p.original, modified: p.polished)
                .frame(maxHeight: 140)
                .overlay(RoundedRectangle(cornerRadius: 6).stroke(.quaternary))
            Button(busy ? "Repairing…" : "Repair") {
                run("Repaired [[\(fix.target)]].") {
                    let res = try await appState.repairLinks(path: fix.path, target: fix.target, preview: false)
                    return LinkFixOutcome.classify(result: res, target: fix.target, verb: "repair")
                }
            }
            .buttonStyle(.borderedProminent)
            .controlSize(.small)
            .disabled(busy)
        }
    }

    @ViewBuilder
    private var suggestSection: some View {
        VStack(alignment: .leading, spacing: 6) {
            Label("Did you mean?", systemImage: "sparkle.magnifyingglass")
                .font(.subheadline).fontWeight(.medium)
            ForEach(filteredSuggestions) { cand in
                HStack(spacing: 8) {
                    VStack(alignment: .leading, spacing: 1) {
                        Text(cand.displayTitle).font(.callout)
                        Text(cand.path).font(.caption2).foregroundStyle(.secondary).lineLimit(1)
                    }
                    Spacer()
                    Button("Link") {
                        let to = (cand.path as NSString).deletingPathExtension
                        run("Linked [[\(fix.target)]] → [[\(cand.displayTitle)]].") {
                            let res = try await appState.relink(path: fix.path, from: fix.target, to: to, preview: false)
                            return LinkFixOutcome.classify(result: res, target: fix.target, verb: "repoint")
                        }
                    }
                    .controlSize(.small)
                    .disabled(busy)
                }
            }
        }
    }

    @ViewBuilder
    private var createSection: some View {
        VStack(alignment: .leading, spacing: 6) {
            Label("Create the note", systemImage: "doc.badge.plus")
                .font(.subheadline).fontWeight(.medium)
            Text("Make a new note titled “\(fix.target)” so the link resolves.")
                .font(.caption).foregroundStyle(.secondary)
            Button("Create note") {
                run("Created note for [[\(fix.target)]].") {
                    _ = try await appState.createStub(title: fix.target)
                    return .success
                }
            }
            .controlSize(.small)
            .disabled(busy)
        }
    }

    @ViewBuilder
    private var unlinkSection: some View {
        VStack(alignment: .leading, spacing: 6) {
            Label("Unlink", systemImage: "link.badge.plus")
                .font(.subheadline).fontWeight(.medium)
            Text("Remove the link, keep the text “\(fix.target)”.")
                .font(.caption).foregroundStyle(.secondary)
            Button("Unlink") {
                run("Unlinked [[\(fix.target)]].") {
                    let res = try await appState.unlink(path: fix.path, target: fix.target, preview: false)
                    return LinkFixOutcome.classify(result: res, target: fix.target, verb: "unlink")
                }
            }
            .controlSize(.small)
            .disabled(busy)
        }
    }

    // MARK: Logic

    /// Drop the finding's own note (a note is never a fix for its own broken
    /// link). The CLI's --source exclusion already guarantees this; the local
    /// filter is defense-in-depth for a CLI whose exclusion no-ops (an older
    /// CLI without the flag returns no suggestions at all, so there is nothing
    /// to filter here). Also drop any suggestion that duplicates the confident
    /// repair target (compared by the note's bare name) so it isn't offered
    /// twice.
    private var filteredSuggestions: [SuggestTargetResult] {
        let withoutSource = suggestions.filter { $0.path != fix.path }
        guard let n = confidentRepair?.newTarget, !n.isEmpty else { return withoutSource }
        let repairName = (n as NSString).lastPathComponent.lowercased()
        return withoutSource.filter {
            (($0.path as NSString).deletingPathExtension as NSString).lastPathComponent.lowercased() != repairName
        }
    }

    private func load() async {
        loading = true
        errorText = nil
        // Repair preview and suggestions are independent — fetch concurrently.
        // repair-links can error (e.g. read-only file); suggestions never do.
        async let repairResult: PolishResult? = try? await appState.repairLinks(path: fix.path, target: fix.target, preview: true)
        async let suggestResult: [SuggestTargetResult] = (try? await appState.suggestTarget(target: fix.target, sourcePath: fix.path)) ?? []
        repairPreview = await repairResult
        suggestions = await suggestResult
        loading = false
    }

    /// Run a mutating resolution. `work` returns the classified outcome:
    /// `.success` dismisses via `onResolved` (green banner + re-lint);
    /// `.actionable` shows guidance in-sheet and keeps the sheet open (the link
    /// still exists, the user should pick another option); `.stale` dismisses
    /// via `onStale` (informational banner + re-lint) so a finding whose link
    /// no longer exists disappears instead of stranding the user in the sheet.
    private func run(_ successMessage: String, _ work: @escaping () async throws -> LinkFixOutcome) {
        busy = true
        guidanceText = nil
        errorText = nil
        Task {
            do {
                let outcome = try await work()
                busy = false
                switch outcome {
                case .success:
                    onResolved(successMessage)
                case .actionable(let message):
                    guidanceText = message
                case .stale(let message):
                    onStale(message)
                }
            } catch {
                busy = false
                errorText = (error as? CLIError)?.errorDescription ?? error.localizedDescription
            }
        }
    }
}
