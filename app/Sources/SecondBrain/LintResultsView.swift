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
    /// The Fix-all confirm sheet (nil = closed); holds the pre-checked plan.
    @State private var activeFixAll: FixAllPlan?
    /// Inline result banner after a fix (green), a stale-finding cleanup
    /// (orange informational), or a failure (orange error).
    @State private var actionMessage: String?
    @State private var actionTone: BannerTone = .success
    /// Per-finding fix classification (keyed by `BrokenFinding.id`), computed by
    /// probing each broken link's fixability once the report loads. Drives the
    /// class-aware header counts, the per-row badges, and the Fix-all plan.
    @State private var classifications: [String: LinkFixClass] = [:]
    /// True while `classifyBrokenFindings` is probing suggest-target per finding.
    @State private var classifying = false
    /// The "Fix each" walkthrough: the decision-class findings queued to step
    /// through the existing per-finding sheet, and the 0-based index of the one
    /// currently shown. Empty means no walkthrough is active (a single "Fix
    /// link…" opens the sheet in its normal one-off mode).
    @State private var walkthroughQueue: [BrokenFinding] = []
    @State private var walkthroughIndex = 0

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
                    let findings = brokenFindings(report)
                    if !findings.isEmpty {
                        // Class-aware header: how many broken links can be fixed in
                        // one click (drift repairs + confident relinks) vs how many
                        // need a human decision, with a single "Fix all" that
                        // applies every one-click case behind a preview confirm.
                        classAwareHeader(findings)
                        Divider()
                    }
                    List(report.issues) { issue in
                        IssueRow(
                            issue: issue,
                            linkClass: linkClass(for: issue),
                            onOpenInObsidian: { openInObsidian(issue) },
                            onSetValue: { setValue in activeSetValue = setValue },
                            onRepair: { repair in activeRepair = repair }
                        )
                    }
                    .listStyle(.inset)
                    .task(id: findings.map(\.id).joined(separator: "|")) {
                        await classifyBrokenFindings(findings)
                    }
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
            // In walkthrough mode the sheet resolving OR the user skipping just
            // advances to the next queued finding (no per-step re-lint); the
            // single end re-lint happens when the queue drains. In single mode
            // it behaves exactly as before: banner + re-lint on resolve/stale.
            let inQueue = !walkthroughQueue.isEmpty
            LinkResolutionSheet(
                fix: repair,
                queuePosition: inQueue ? (index: walkthroughIndex, total: walkthroughQueue.count) : nil,
                onOpenInObsidian: { openInObsidian(forPath: repair.path) },
                onResolved: { message in
                    if inQueue {
                        advanceWalkthrough()
                    } else {
                        activeRepair = nil
                        actionTone = .success
                        actionMessage = "\(message) Re-checking…"
                        Task { await appState.runLint() }
                    }
                },
                onStale: { message in
                    // The link no longer exists, so the finding is stale, not
                    // failed: informational banner + re-lint so it disappears.
                    if inQueue {
                        advanceWalkthrough()
                    } else {
                        activeRepair = nil
                        actionTone = .info
                        actionMessage = "\(message) Re-checking…"
                        Task { await appState.runLint() }
                    }
                },
                onSkip: inQueue ? { advanceWalkthrough() } : nil
            )
            .environment(appState)
        }
        .sheet(item: $activeFixAll) { plan in
            FixAllSheet(
                rewrites: plan.rewrites,
                onDone: { fixed, failed in
                    activeFixAll = nil
                    let banner = LinkFixOutcome.fixAllResultBanner(fixed: fixed, failed: failed)
                    actionTone = banner.tone
                    actionMessage = banner.message + " Re-checking…"
                    Task { await appState.runLint() }
                }
            )
            .environment(appState)
        }
        // The walkthrough sheet dismissing to nil (queue drained, or the user
        // cancelled mid-walk) ends the walkthrough: tear down the queue and
        // re-lint once so every fix applied across the steps is reflected. In
        // single-finding mode the queue is empty, so this is a no-op and the
        // per-finding closures own their own re-lint (no double re-check).
        .onChange(of: activeRepair?.id) { _, newID in
            if newID == nil && !walkthroughQueue.isEmpty {
                walkthroughQueue = []
                walkthroughIndex = 0
                actionTone = .success
                actionMessage = "Finished stepping through broken links. Re-checking…"
                Task { await appState.runLint() }
            }
        }
    }

    /// The class-aware header + Fix-all button shown above the findings list when
    /// at least one broken link exists.
    @ViewBuilder
    private func classAwareHeader(_ findings: [BrokenFinding]) -> some View {
        let classes = findings.compactMap { classifications[$0.id] }
        let counts = FixAllPlanner.counts(classes)
        // Ready only when the probe has finished AND produced a class for every
        // finding, so a re-classification after a fix shows "checking fixes…"
        // rather than a stale-but-complete count.
        let ready = !classifying && classes.count == findings.count
        HStack(spacing: 6) {
            Image(systemName: "link")
                .foregroundStyle(.secondary)
            if !ready {
                Text("\(findings.count) broken link\(findings.count == 1 ? "" : "s") · checking fixes…")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            } else {
                Text(FixAllPlanner.headerSummary(total: findings.count, counts: counts))
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
            Spacer()
            // Step through the decision-class findings (the ones Fix-all can't
            // resolve one-click) one at a time via the existing per-finding sheet.
            if ready && counts.decision >= 1 {
                Button("Fix each (\(counts.decision))") { startWalkthrough(findings) }
                    .buttonStyle(.bordered)
                    .controlSize(.small)
                    .disabled(appState.isLinting)
            }
            if ready && counts.oneClick >= 1 {
                Button("Fix all") { activeFixAll = FixAllPlan(rewrites: fixAllPlan(findings)) }
                    .buttonStyle(.borderedProminent)
                    .controlSize(.small)
                    .disabled(appState.isLinting)
            }
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 6)
    }

    /// Begin the "Fix each" walkthrough: build the decision-class queue (the
    /// complement of the Fix-all plan) from the current classifications and open
    /// the first finding in the per-finding sheet. A no-op if nothing needs a
    /// decision.
    private func startWalkthrough(_ findings: [BrokenFinding]) {
        let classified = findings.compactMap { f in classifications[f.id].map { (f, $0) } }
        let queue = FixAllPlanner.walkthroughQueue(from: classified)
        guard let first = queue.first else { return }
        walkthroughQueue = queue
        walkthroughIndex = 0
        activeRepair = ActiveRepair(path: first.path, target: first.target)
    }

    /// Advance the walkthrough to the next queued finding after a fix is applied
    /// or the finding is skipped. When the queue is exhausted, drop the sheet to
    /// nil; the `.onChange` on `activeRepair` then ends the walkthrough and
    /// re-lints once.
    private func advanceWalkthrough() {
        let next = walkthroughIndex + 1
        guard next < walkthroughQueue.count else {
            activeRepair = nil
            return
        }
        walkthroughIndex = next
        let finding = walkthroughQueue[next]
        activeRepair = ActiveRepair(path: finding.path, target: finding.target)
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

    /// The broken-wikilink findings in report order, normalized to
    /// `BrokenFinding`. The raw target and drift class come from the CLI's
    /// additive lint fields when present; on a pre-classification CLI the target
    /// falls back to `LintFinding.classify(message:)` and `fix` stays nil (the
    /// flow then leans on suggest-target alone).
    private func brokenFindings(_ report: LintReport) -> [BrokenFinding] {
        report.issues.compactMap { issue -> BrokenFinding? in
            let target: String
            if let t = issue.target, !t.isEmpty {
                target = t
            } else if case let .brokenLink(t) = LintFinding.classify(message: issue.message) {
                target = t
            } else {
                return nil
            }
            return BrokenFinding(
                path: issue.path, target: target, fix: issue.fix, driftTarget: issue.driftTarget)
        }
    }

    /// The stored classification for one issue's broken finding (nil for a
    /// non-broken issue or before classification completes).
    private func linkClass(for issue: LintIssue) -> LinkFixClass? {
        let target: String
        if let t = issue.target, !t.isEmpty {
            target = t
        } else if case let .brokenLink(t) = LintFinding.classify(message: issue.message) {
            target = t
        } else {
            return nil
        }
        return classifications["\(issue.path)|\(target)"]
    }

    /// The Fix-all plan derived from the stored classifications: one-click
    /// classes (drift repairs + confident relinks) become rewrites; ambiguous /
    /// missing findings are excluded (they route to the per-finding sheet).
    private func fixAllPlan(_ findings: [BrokenFinding]) -> [PlannedRewrite] {
        FixAllPlanner.plan(from: findings.compactMap { f in
            classifications[f.id].map { (f, $0) }
        })
    }

    /// Probe each broken finding's fixability once, storing the result. A "drift"
    /// finding needs no lookup (the CLI already resolved it); every other finding
    /// asks `suggest-target` (scoped with `--source` to the finding's own note)
    /// for its best candidate, then classifies. Best-effort: a lookup miss just
    /// classifies as missing rather than failing the whole pass.
    private func classifyBrokenFindings(_ findings: [BrokenFinding]) async {
        guard !findings.isEmpty else {
            classifications = [:]
            classifying = false
            return
        }
        classifying = true
        var result: [String: LinkFixClass] = [:]
        for f in findings {
            if f.fix == "drift" {
                result[f.id] = .repairable(driftTarget: f.driftTarget)
                continue
            }
            let candidates = (try? await appState.suggestTarget(target: f.target, sourcePath: f.path)) ?? []
            result[f.id] = FixAllPlanner.classify(
                fix: f.fix, topCandidate: candidates.first, driftTarget: f.driftTarget)
        }
        classifications = result
        classifying = false
    }
}

/// One validation finding with its remediation affordances. Pure presentation:
/// it classifies the message and surfaces the right buttons, delegating the
/// actual work to the parent via closures.
private struct IssueRow: View {
    let issue: LintIssue
    /// Fixability class for a broken-link finding (nil for other findings, or
    /// before classification completes). Renders the subtle per-row badge.
    let linkClass: LinkFixClass?
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
                    if let linkClass {
                        LinkClassBadge(linkClass: linkClass)
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

/// Subtle capsule showing a broken link's fixability class. A one-click class
/// (auto-repairable, did you mean?) tints green; a decision class (matches
/// several, no matching note) tints orange. Kept small so it reads as a hint,
/// not a control.
private struct LinkClassBadge: View {
    let linkClass: LinkFixClass

    private var tint: Color { linkClass.isOneClick ? .green : .orange }

    var body: some View {
        Text(linkClass.badgeText)
            .font(.caption2)
            .foregroundStyle(tint)
            .padding(.horizontal, 6)
            .padding(.vertical, 1)
            .background(Capsule().fill(tint.opacity(0.12)))
    }
}

/// The broken-link finding being repaired in the preview sheet.
struct ActiveRepair: Identifiable {
    let id = UUID()
    let path: String
    let target: String
}

/// The Fix-all confirm sheet's payload: the pre-checked plan of one-click
/// rewrites. Wrapped so it can drive `.sheet(item:)`.
struct FixAllPlan: Identifiable {
    let id = UUID()
    let rewrites: [PlannedRewrite]
}

/// One-tap confirm for the whole Fix-all plan. Lists every planned rewrite as a
/// pre-checked `[[target]] -> [[chosen]]` row so nothing is applied blind, then
/// applies each checked rewrite (`repair-links --write` / `relink --write`,
/// each reversible with `polish --undo`) and reports the aggregate via
/// `onDone`. Uses a SwiftUI sheet (like the per-finding sheet) since the
/// checkbox list needs interactive controls an NSAlert can't host.
private struct FixAllSheet: View {
    @Environment(AppState.self) private var appState
    @Environment(\.dismiss) private var dismiss
    let rewrites: [PlannedRewrite]
    /// Called with (fixed, failed) counts after the pass so the parent can show
    /// the aggregate banner, dismiss, and re-lint.
    let onDone: (_ fixed: Int, _ failed: Int) -> Void

    /// Finding ids the user has left checked (all pre-checked on open).
    @State private var checked: Set<String>
    @State private var applying = false
    @State private var errorText: String?

    init(rewrites: [PlannedRewrite], onDone: @escaping (Int, Int) -> Void) {
        self.rewrites = rewrites
        self.onDone = onDone
        _checked = State(initialValue: Set(rewrites.map(\.id)))
    }

    private var checkedCount: Int { checked.count }

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text("Fix \(rewrites.count) link\(rewrites.count == 1 ? "" : "s")")
                .font(.headline)
            Text("Each rewrite points a broken link at an existing note. Uncheck any you want to skip. Each edited note can be reverted with 2nb polish --undo (which restores its most recent change); to roll back a whole batch, use Obsidian's file history or git.")
                .font(.caption)
                .foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)

            ScrollView {
                VStack(alignment: .leading, spacing: 8) {
                    ForEach(rewrites) { rewrite in
                        Toggle(isOn: binding(for: rewrite)) {
                            VStack(alignment: .leading, spacing: 1) {
                                Text("[[\(rewrite.target)]] → [[\(rewrite.chosenDisplay)]]")
                                    .font(.callout)
                                HStack(spacing: 6) {
                                    Text(rewrite.kindLabel)
                                        .font(.caption2)
                                        .foregroundStyle(.secondary)
                                    Text(rewrite.path)
                                        .font(.caption2)
                                        .foregroundStyle(.tertiary)
                                        .lineLimit(1)
                                }
                            }
                        }
                        .toggleStyle(.checkbox)
                        .disabled(applying)
                    }
                }
                .padding(.vertical, 2)
            }
            .frame(maxHeight: 260)

            if let errorText {
                Text(errorText)
                    .font(.caption)
                    .foregroundStyle(.red)
                    .fixedSize(horizontal: false, vertical: true)
            }

            Divider()
            HStack {
                Spacer()
                Button("Cancel") { dismiss() }
                    .keyboardShortcut(.cancelAction)
                    .disabled(applying)
                Button(applying ? "Fixing…" : "Fix \(checkedCount)") { apply() }
                    .keyboardShortcut(.defaultAction)
                    .buttonStyle(.borderedProminent)
                    .disabled(applying || checkedCount == 0)
            }
        }
        .padding(16)
        .frame(width: 520)
    }

    private func binding(for rewrite: PlannedRewrite) -> Binding<Bool> {
        Binding(
            get: { checked.contains(rewrite.id) },
            set: { isOn in
                if isOn { checked.insert(rewrite.id) } else { checked.remove(rewrite.id) }
            }
        )
    }

    /// Apply each checked rewrite sequentially. A rewrite counts as fixed when
    /// the CLI reports a link was actually repaired/relinked; a no-op (the note
    /// changed since the check) or an error counts as failed so the aggregate
    /// banner is honest.
    private func apply() {
        let selected = rewrites.filter { checked.contains($0.id) }
        guard !selected.isEmpty else { return }
        applying = true
        errorText = nil
        Task {
            var fixed = 0
            var failed = 0
            for rewrite in selected {
                do {
                    let result: PolishResult
                    switch rewrite.action {
                    case .repair:
                        result = try await appState.repairLinks(
                            path: rewrite.path, target: rewrite.target, preview: false)
                    case .relink(let chosenPath, _):
                        result = try await appState.relink(
                            path: rewrite.path, from: rewrite.target, to: chosenPath, preview: false)
                    }
                    let n = result.linksRepaired?.count ?? 0
                    if n > 0 { fixed += n } else { failed += 1 }
                } catch {
                    failed += 1
                }
            }
            applying = false
            onDone(fixed, failed)
        }
    }
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
    /// Position within the "Fix each" walkthrough, e.g. (index: 1, total: 5).
    /// Non-nil puts the sheet in queue mode: it shows a "2 of 5" progress
    /// indicator, offers a Skip button, and visually recommends an action. Nil
    /// is the normal single-finding mode (opened from one "Fix link…" button).
    var queuePosition: (index: Int, total: Int)? = nil
    /// Open the note in Obsidian (delegated to the parent, which knows the vault).
    let onOpenInObsidian: () -> Void
    /// Called with a past-tense banner message after a successful resolution so
    /// the parent can dismiss, re-lint, and show the green banner.
    let onResolved: (String) -> Void
    /// Called when the link no longer exists (a stale finding) so the parent
    /// can dismiss, re-lint, and show an informational banner instead of a
    /// false success or an in-sheet dead end.
    let onStale: (String) -> Void
    /// In queue mode, skip this finding and advance to the next. Nil in
    /// single-finding mode, where no Skip button is shown.
    var onSkip: (() -> Void)? = nil

    /// In queue mode, which action to visually recommend: the top suggestion
    /// (relink) when one exists, else unlink. Nil in single-finding mode, so
    /// that path renders with no preselection, exactly as before.
    private enum RecommendedAction: Equatable { case relinkTop, unlink }
    private var recommendedAction: RecommendedAction? {
        guard queuePosition != nil, !loading else { return nil }
        return filteredSuggestions.isEmpty ? .unlink : .relinkTop
    }

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
            HStack {
                Text("Fix link")
                    .font(.headline)
                if let pos = queuePosition {
                    Spacer()
                    Text(FixAllPlanner.walkthroughProgress(index: pos.index, total: pos.total))
                        .font(.caption)
                        .foregroundStyle(.secondary)
                        .monospacedDigit()
                }
            }
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
                        // A path-qualified target ("folder/note") names a
                        // location, not a title to mint, so Create doesn't apply.
                        if FixAllPlanner.canCreateNote(forTarget: fix.target) {
                            createSection
                        }
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
                if let onSkip {
                    Button("Skip") { onSkip() }
                        .disabled(busy)
                }
                // In queue mode this stops the whole walkthrough (the parent's
                // onChange on `activeRepair` re-lints once); in single mode it
                // just closes the sheet.
                Button(queuePosition == nil ? "Cancel" : "Done") { dismiss() }
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
            ForEach(Array(filteredSuggestions.enumerated()), id: \.element.id) { index, cand in
                let recommended = recommendedAction == .relinkTop && index == 0
                HStack(spacing: 8) {
                    VStack(alignment: .leading, spacing: 1) {
                        Text(cand.displayTitle).font(.callout)
                        Text(cand.path).font(.caption2).foregroundStyle(.secondary).lineLimit(1)
                    }
                    if recommended {
                        Text("Recommended").font(.caption2).foregroundStyle(.green)
                    }
                    Spacer()
                    Group {
                        if recommended {
                            Button("Link") { relinkAction(cand) }.buttonStyle(.borderedProminent)
                        } else {
                            Button("Link") { relinkAction(cand) }
                        }
                    }
                    .controlSize(.small)
                    .disabled(busy)
                }
            }
        }
    }

    /// Repoint the broken link at the chosen candidate note (`.md` dropped).
    /// Shared by the plain and recommended Link buttons so preselection changes
    /// only the button's prominence, never the action.
    private func relinkAction(_ cand: SuggestTargetResult) {
        let to = (cand.path as NSString).deletingPathExtension
        run("Linked [[\(fix.target)]] → [[\(cand.displayTitle)]].") {
            let res = try await appState.relink(path: fix.path, from: fix.target, to: to, preview: false)
            return LinkFixOutcome.classify(result: res, target: fix.target, verb: "repoint")
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
        let recommended = recommendedAction == .unlink
        VStack(alignment: .leading, spacing: 6) {
            HStack(spacing: 6) {
                Label("Unlink", systemImage: "link.badge.plus")
                    .font(.subheadline).fontWeight(.medium)
                if recommended {
                    Text("Recommended").font(.caption2).foregroundStyle(.green)
                }
            }
            Text("Remove the link, keep the text “\(fix.target)”.")
                .font(.caption).foregroundStyle(.secondary)
            Group {
                if recommended {
                    Button("Unlink") { unlinkAction() }.buttonStyle(.borderedProminent)
                } else {
                    Button("Unlink") { unlinkAction() }
                }
            }
            .controlSize(.small)
            .disabled(busy)
        }
    }

    /// Remove the broken link, keeping its visible text. Shared by the plain and
    /// recommended Unlink buttons.
    private func unlinkAction() {
        run("Unlinked [[\(fix.target)]].") {
            let res = try await appState.unlink(path: fix.path, target: fix.target, preview: false)
            return LinkFixOutcome.classify(result: res, target: fix.target, verb: "unlink")
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
