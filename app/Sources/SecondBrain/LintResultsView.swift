import SwiftUI
import AppKit
import SecondBrainCore

/// Pure presentation & enablement logic for the single-button `Fix Vault` UX.
/// View-free so every state transition and summary string is unit-testable.
enum FixVaultPresentation {
    static func canFixVault(_ report: FixVaultReport?) -> Bool {
        guard let report else { return false }
        return report.totalFixes > 0 || report.schemaUpdated || report.aiFixableCount > 0
    }

    static func canUndo(_ report: FixVaultReport?) -> Bool {
        report?.undoAvailable == true
    }

    static func primaryButtonTitle(_ report: FixVaultReport?, isFixing: Bool) -> String {
        if isFixing {
            return "Fixing Vault…"
        }
        guard let report else {
            return "Fix Vault"
        }
        let total = report.totalFixes + report.aiFixableCount
        guard total > 0 else {
            return "Fix Vault"
        }
        return "Fix Vault (\(total))"
    }

    static func summaryLine(fix: FixVaultReport, lint: LintReport?) -> String {
        var parts: [String] = []
        if fix.linksUpdated > 0 {
            parts.append("\(fix.linksUpdated) link\(fix.linksUpdated == 1 ? "" : "s") updated")
        }
        if fix.linksUnlinked > 0 {
            parts.append("\(fix.linksUnlinked) dead link\(fix.linksUnlinked == 1 ? "" : "s") unwrapped")
        }
        if fix.formattingFixed > 0 {
            parts.append("\(fix.formattingFixed) formatting fix\(fix.formattingFixed == 1 ? "" : "es")")
        }
        if fix.schemaUpdated {
            let count = max(fix.schemaIssuesResolved, fix.schemaStatusesAdded.count)
            parts.append("\(count) schema update\(count == 1 ? "" : "s")")
        }
        let fmCount = fix.notes.reduce(0) { $0 + $1.frontmatterFixes.count }
        if fmCount > 0 {
            parts.append("\(fmCount) property fix\(fmCount == 1 ? "" : "es")")
        }
        if fix.aiFixableCount > 0 {
            parts.append("\(fix.aiFixableCount) AI-fixable")
        }
        if fix.aiApplied && fix.aiFixesApplied > 0 {
            parts.append("\(fix.aiFixesApplied) AI fix\(fix.aiFixesApplied == 1 ? "" : "es") applied")
        }
        if !fix.ambiguous.isEmpty {
            let amb = fix.ambiguous.count
            parts.append("\(amb) need\(amb == 1 ? "s" : "") your choice")
        }
        if !parts.isEmpty {
            return parts.joined(separator: " · ")
        }
        if let lint, !lint.issues.isEmpty {
            return "\(lint.issues.count) issue\(lint.issues.count == 1 ? "" : "s") need your review"
        }
        return "All \(fix.filesScanned) files look clean."
    }

    static func appliedBanner(_ report: FixVaultReport) -> (tone: BannerTone, message: String) {
        if report.totalFixes > 0 {
            let schemaSuffix = report.schemaUpdated ? " and updated schema" : ""
            let aiSuffix = (report.aiApplied && report.aiFixesApplied > 0) ? " (\(report.aiFixesApplied) AI)" : ""
            let undoSuffix = report.undoAvailable ? " Undo anytime." : ""
            var message = "Fixed \(report.totalFixes) issue\(report.totalFixes == 1 ? "" : "s") across \(report.notesChanged) note\(report.notesChanged == 1 ? "" : "s")\(schemaSuffix)\(aiSuffix).\(undoSuffix)"
            if let skip = report.aiSkippedReason, !skip.isEmpty {
                message += " AI repair skipped: \(skip)"
                return (.info, message)
            }
            if !report.warnings.isEmpty {
                message += " Warning: \(report.warnings.joined(separator: "; "))"
                return (.info, message)
            }
            return (.success, message)
        }
        if report.schemaUpdated {
            let undoSuffix = report.undoAvailable ? " Undo anytime." : ""
            var message = "Updated note schema statuses.\(undoSuffix)"
            if let skip = report.aiSkippedReason, !skip.isEmpty {
                message += " AI repair skipped: \(skip)"
                return (.info, message)
            }
            if !report.warnings.isEmpty {
                message += " Warning: \(report.warnings.joined(separator: "; "))"
                return (.info, message)
            }
            return (.success, message)
        }
        if let skip = report.aiSkippedReason, !skip.isEmpty {
            return (.error, "AI repair skipped: \(skip)")
        }
        if let warning = report.warnings.first, !warning.isEmpty {
            return (.error, warning)
        }
        return (.info, "Vault had no automatic fixes to apply.")
    }

    static func undoBanner(_ result: FixUndoResult) -> (tone: BannerTone, message: String) {
        let schemaSuffix = result.schemaRestored ? " and schema" : ""
        return (
            .info,
            "Undid last fix: restored \(result.filesRestored) file\(result.filesRestored == 1 ? "" : "s")\(schemaSuffix)."
        )
    }

    /// Filter out lint issues that `Fix Vault` will resolve (deterministically
    /// or via AI repair) so the "Needs Your Choice" section only surfaces
    /// genuine manual decisions (e.g. ambiguous links).
    static func manualIssues(lint: LintReport, fix: FixVaultReport?) -> [LintIssue] {
        guard let fix else { return lint.issues }

        var handledTargets = Set<String>()
        var handledMissingFields = Set<String>()
        var handledLinkSyntaxPaths = Set<String>()
        var handledDatePaths = Set<String>()
        for note in fix.notes {
            for la in note.linkActions {
                if la.action == "relink" || la.action == "unlink" {
                    handledTargets.insert("\(note.path)|\(la.from)")
                } else if la.action == "format" {
                    handledLinkSyntaxPaths.insert(note.path)
                }
            }
            for ff in note.frontmatterFixes {
                if ff.reason == "missing_required" {
                    handledMissingFields.insert("\(note.path)|\(ff.field)")
                } else if ff.reason == "unquote_date" || ff.reason == "formatting" {
                    handledDatePaths.insert(note.path)
                }
            }
        }
        let addedStatuses = Set(fix.schemaStatusesAdded)
        let ambiguousKeys = Set(fix.ambiguous.map { "\($0.path)|\($0.target)" })

        var aiBrokenTargets = Set<String>()
        var aiHandledPathKinds = Set<String>()
        for ai in fix.aiFixableIssues {
            if ai.kind == "broken_link" {
                let target = ai.target ?? ""
                let key = "\(ai.path)|\(target)"
                if !ai.message.hasPrefix("ambiguous wikilink:") && !ambiguousKeys.contains(key) {
                    aiBrokenTargets.insert(key)
                }
            } else {
                aiHandledPathKinds.insert("\(ai.path)|\(ai.kind)")
            }
        }

        var existingBrokenKeys = Set<String>()
        var manual = lint.issues.filter { issue in
            let finding = LintFinding.classify(issue: issue)
            switch finding {
            case .brokenLink(let msgTarget):
                let target = (issue.target?.isEmpty == false ? issue.target : nil) ?? msgTarget
                let key = "\(issue.path)|\(target)"
                existingBrokenKeys.insert(key)
                if issue.fix == "ambiguous" || ambiguousKeys.contains(key) {
                    return true
                }
                return !handledTargets.contains(key) && !aiBrokenTargets.contains(key)
            case .invalidEnum(let field, let value, _):
                if field == "status" && addedStatuses.contains(value) {
                    return false
                }
                if aiHandledPathKinds.contains("\(issue.path)|schema_enum") {
                    return false
                }
                return true
            case .missingField(let field, _):
                if handledMissingFields.contains("\(issue.path)|\(field)")
                    || aiHandledPathKinds.contains("\(issue.path)|missing_required") {
                    return false
                }
                return true
            case .parseError:
                return !aiHandledPathKinds.contains("\(issue.path)|parse_error")
            case .other:
                if issue.kind == "link_syntax"
                    && (handledLinkSyntaxPaths.contains(issue.path) || aiHandledPathKinds.contains("\(issue.path)|formatting")) {
                    return false
                }
                if (issue.kind == "date_format" || issue.kind == "date_invalid")
                    && (handledDatePaths.contains(issue.path) || aiHandledPathKinds.contains("\(issue.path)|formatting")) {
                    return false
                }
                if aiHandledPathKinds.contains("\(issue.path)|formatting") {
                    return false
                }
                return true
            }
        }

        // Exact-basename ambiguous wikilinks (`[[dup]]`) are omitted by `2nb lint`
        // (which only reports `ErrTargetNotFound`) yet surfaced in `fix.ambiguous`.
        // Synthesize manual entries so "Needs Your Choice" and "Fix link…" stay
        // consistent with `summaryLine`.
        for amb in fix.ambiguous {
            let key = "\(amb.path)|\(amb.target)"
            if existingBrokenKeys.insert(key).inserted {
                manual.append(
                    LintIssue(
                        path: amb.path,
                        line: 1,
                        level: "warning",
                        message: "ambiguous wikilink: [[\(amb.target)]]",
                        target: amb.target,
                        fix: "ambiguous",
                        driftTarget: nil,
                        kind: "broken_link",
                        field: nil,
                        candidates: amb.candidates
                    )
                )
            }
        }
        return manual
    }
}

struct LintResultsView: View {
    @Environment(AppState.self) var appState
    @Binding var isPresented: Bool
    var isInline: Bool = false

    @State private var activeSetValue: ActiveSetValue?
    @State private var activeRepair: ActiveRepair?
    @State private var actionMessage: String?
    @State private var actionTone: BannerTone = .success

    var body: some View {
        let manualIssues = appState.lintReport.map {
            FixVaultPresentation.manualIssues(lint: $0, fix: appState.fixReport)
        } ?? []

        VStack(spacing: 0) {
            // Header
            HStack {
                Image(systemName: "checkmark.seal")
                    .foregroundStyle(.secondary)
                Text("Links & Schema")
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

            if let lintError = appState.lintError {
                Divider()
                HStack(spacing: 6) {
                    Image(systemName: "xmark.octagon.fill")
                        .foregroundStyle(.red)
                    Text("Lint failed: \(lintError)")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                    Spacer()
                }
                .padding(.horizontal, 12)
                .padding(.vertical, 6)
                .background(Color.red.opacity(0.08))
            }

            if let fixPreviewError = appState.fixPreviewError {
                Divider()
                HStack(spacing: 6) {
                    Image(systemName: "exclamationmark.triangle.fill")
                        .foregroundStyle(.orange)
                    Text("Fix preview unavailable: \(fixPreviewError)")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                    Spacer()
                }
                .padding(.horizontal, 12)
                .padding(.vertical, 6)
                .background(Color.orange.opacity(0.08))
            }

            Divider()

            // Primary Vault Health & Single-Button Action Card
            if appState.lintReport != nil || appState.fixReport != nil {
                vaultFixActionCard(manualIssueCount: manualIssues.count)
                Divider()
            }

            // Main Content
            if appState.isLinting && appState.lintReport == nil {
                VStack(spacing: 12) {
                    ProgressView()
                    Text("Scanning vault links and schemas…")
                        .foregroundStyle(.secondary)
                }
                .frame(maxWidth: .infinity, maxHeight: .infinity)
            } else if let report = appState.lintReport {
                let canFix = FixVaultPresentation.canFixVault(appState.fixReport)
                if report.issues.isEmpty && !canFix && manualIssues.isEmpty && appState.fixPreviewError == nil {
                    VStack(spacing: 12) {
                        Image(systemName: "checkmark.circle.fill")
                            .font(.system(size: 48))
                            .foregroundStyle(.green)
                        Text("No issues found!")
                            .font(.headline)
                        Text("Your vault links, formatting, and schemas are clean across \(report.filesChecked) files.")
                            .font(.caption)
                            .foregroundStyle(.secondary)
                            .multilineTextAlignment(.center)
                            .padding(.horizontal, 40)
                    }
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
                } else {
                    List {
                        if let fixReport = appState.fixReport, (fixReport.totalFixes > 0 || fixReport.schemaUpdated) {
                            Section("Planned Automatic Fixes (\(fixReport.totalFixes))") {
                                if fixReport.schemaUpdated {
                                    HStack(alignment: .top, spacing: 8) {
                                        Image(systemName: "doc.badge.gearshape")
                                            .foregroundStyle(.green)
                                        VStack(alignment: .leading, spacing: 2) {
                                            Text(".2ndbrain/schemas.yaml")
                                                .font(.body)
                                                .fontWeight(.medium)
                                            Text("Allow common note statuses (\(fixReport.schemaStatusesAdded.joined(separator: ", "))) — resolves \(fixReport.schemaIssuesResolved) note\(fixReport.schemaIssuesResolved == 1 ? "" : "s") without rewriting prose.")
                                                .font(.caption)
                                                .foregroundStyle(.secondary)
                                        }
                                    }
                                    .padding(.vertical, 3)
                                }

                                ForEach(fixReport.notes) { note in
                                    FixNotePreviewRow(note: note, onOpenInObsidian: {
                                        openInObsidian(forPath: note.path)
                                    })
                                }
                            }
                        }

                        if let fixReport = appState.fixReport, !fixReport.aiFixableIssues.isEmpty {
                            Section("Planned AI Repairs (\(fixReport.aiFixableIssues.count))") {
                                ForEach(fixReport.aiFixableIssues) { aiIssue in
                                    AIFixIssuePreviewRow(issue: aiIssue, onOpenInObsidian: {
                                        openInObsidian(forPath: aiIssue.path)
                                    })
                                }
                            }
                        }

                        if !manualIssues.isEmpty {
                            Section("Needs Your Choice (\(manualIssues.count))") {
                                ForEach(manualIssues) { issue in
                                    ManualIssueRow(
                                        issue: issue,
                                        candidates: ambiguousCandidates(for: issue),
                                        onOpenInObsidian: { openInObsidian(forPath: issue.path) },
                                        onSetValue: { activeSetValue = $0 },
                                        onRepair: { activeRepair = $0 }
                                    )
                                }
                            }
                        }
                    }
                    .listStyle(.inset)
                }
            } else if let lintError = appState.lintError {
                VStack(spacing: 12) {
                    Image(systemName: "exclamationmark.octagon.fill")
                        .font(.system(size: 48))
                        .foregroundStyle(.red)
                    Text("Lint scan failed")
                        .font(.headline)
                    Text(lintError)
                        .font(.caption)
                        .foregroundStyle(.secondary)
                        .multilineTextAlignment(.center)
                        .padding(.horizontal, 40)
                }
                .frame(maxWidth: .infinity, maxHeight: .infinity)
            } else {
                VStack(spacing: 12) {
                    Image(systemName: "checkmark.seal")
                        .font(.system(size: 48))
                        .foregroundStyle(.secondary)
                    Text("Not scanned yet")
                        .font(.headline)
                    Text("Click Rescan to check broken links, formatting, and schema rules.")
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
                Button(appState.isLinting ? "Scanning…" : "Rescan") {
                    Task { await appState.runLint() }
                }
                .accessibilityIdentifier("notes.rescanButton")
                .disabled(appState.isLinting || appState.isFixingVault || appState.vault == nil)
            }
            .padding(12)
        }
        .frame(width: isInline ? nil : 620, height: isInline ? nil : 520)
        .task(id: appState.vault?.rootURL) {
            if appState.lintReport == nil && !appState.isLinting && !appState.isFixingVault {
                await appState.runLint()
            }
        }
        .onAppear {
            applyButtonCommand(appState.guiInputBridge.pendingButtonCommand)
        }
        .onChange(of: appState.guiInputBridge.pendingButtonCommand) { _, cmd in
            applyButtonCommand(cmd)
        }
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
                    activeRepair = nil
                    actionTone = .info
                    actionMessage = "\(message) Re-checking…"
                    Task { await appState.runLint() }
                }
            )
            .environment(appState)
        }
    }

    // MARK: - Vault Fix Card

    @ViewBuilder
    private func vaultFixActionCard(manualIssueCount: Int) -> some View {
        let fix = appState.fixReport
        let canFix = FixVaultPresentation.canFixVault(fix)
        let canUndo = FixVaultPresentation.canUndo(fix)
        let busy = appState.isLinting || appState.isFixingVault

        HStack(spacing: 12) {
            Image(systemName: canFix ? "wand.and.stars" : (manualIssueCount == 0 ? "checkmark.circle.fill" : "exclamationmark.triangle.fill"))
                .font(.title2)
                .foregroundStyle(canFix ? Color.accentColor : (manualIssueCount == 0 ? Color.green : Color.orange))

            VStack(alignment: .leading, spacing: 3) {
                if let fix {
                    let actionableTotal = fix.totalFixes + fix.aiFixableCount
                    let affectedNotes = Set(fix.notes.map(\.path) + fix.aiFixableIssues.map(\.path)).count
                    let headline: String = {
                        if canFix {
                            if actionableTotal > 0 {
                                return "Ready to fix \(actionableTotal) issue\(actionableTotal == 1 ? "" : "s") across \(affectedNotes) note\(affectedNotes == 1 ? "" : "s")"
                            }
                            return "Ready to update vault schema"
                        }
                        return manualIssueCount == 0 ? "Vault links, formatting & schemas are clean" : "Manual review needed"
                    }()
                    Text(headline)
                        .font(.subheadline)
                        .fontWeight(.semibold)

                    Text(FixVaultPresentation.summaryLine(fix: fix, lint: appState.lintReport))
                        .font(.caption)
                        .foregroundStyle(.secondary)
                } else if let lint = appState.lintReport {
                    Text(lint.issues.isEmpty ? "Vault links & schemas are clean" : "\(lint.issues.count) issue\(lint.issues.count == 1 ? "" : "s") found")
                        .font(.subheadline)
                        .fontWeight(.semibold)
                }
            }

            Spacer()

            if canUndo {
                Button("Undo Last Fix") {
                    Task { await runUndoFixVault() }
                }
                .accessibilityIdentifier("notes.undoFixButton")
                .buttonStyle(.bordered)
                .disabled(busy)
            }

            Button(FixVaultPresentation.primaryButtonTitle(fix, isFixing: appState.isFixingVault)) {
                Task { await runFixVault() }
            }
            .accessibilityIdentifier("notes.fixVaultButton")
            .buttonStyle(.borderedProminent)
            .disabled(busy || !canFix)
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 10)
        .background(Color(nsColor: .controlBackgroundColor))
    }

    // MARK: - Actions

    private func runFixVault() async {
        do {
            let report = try await appState.applyVaultFix()
            let banner = FixVaultPresentation.appliedBanner(report)
            actionTone = banner.tone
            actionMessage = banner.message
        } catch {
            actionTone = .error
            actionMessage = AppState.boundedRedacted((error as? CLIError)?.errorDescription ?? error.localizedDescription)
        }
    }

    private func runUndoFixVault() async {
        do {
            let result = try await appState.undoVaultFix()
            let banner = FixVaultPresentation.undoBanner(result)
            actionTone = banner.tone
            actionMessage = banner.message
        } catch {
            actionTone = .error
            actionMessage = AppState.boundedRedacted((error as? CLIError)?.errorDescription ?? error.localizedDescription)
        }
    }

    private func applyButtonCommand(_ cmd: GUIButtonCommand?) {
        guard let cmd else { return }
        switch cmd.targetIdentifier {
        case "notes.fixVaultButton":
            if appState.vault == nil || appState.isLinting || appState.isFixingVault || !FixVaultPresentation.canFixVault(appState.fixReport) {
                appState.guiInputBridge.acknowledgeButtonDisabled(cmd.id, label: "notes.fixVaultButton")
            } else {
                appState.guiInputBridge.acknowledgeButtonClicked(
                    cmd.id,
                    label: FixVaultPresentation.primaryButtonTitle(appState.fixReport, isFixing: false)
                )
                Task { await runFixVault() }
            }
        case "notes.undoFixButton":
            if appState.vault == nil || appState.isLinting || appState.isFixingVault || !FixVaultPresentation.canUndo(appState.fixReport) {
                appState.guiInputBridge.acknowledgeButtonDisabled(cmd.id, label: "notes.undoFixButton")
            } else {
                appState.guiInputBridge.acknowledgeButtonClicked(cmd.id, label: "Undo Last Fix")
                Task { await runUndoFixVault() }
            }
        case "notes.rescanButton":
            if appState.vault == nil || appState.isLinting || appState.isFixingVault {
                appState.guiInputBridge.acknowledgeButtonDisabled(cmd.id, label: "notes.rescanButton")
            } else {
                appState.guiInputBridge.acknowledgeButtonClicked(cmd.id, label: "Rescan")
                Task { await appState.runLint() }
            }
        default:
            break
        }
    }

    private func ambiguousCandidates(for issue: LintIssue) -> [String] {
        if let candidates = issue.candidates, !candidates.isEmpty {
            return candidates
        }
        guard let fixReport = appState.fixReport else { return [] }
        let target: String
        if let t = issue.target, !t.isEmpty {
            target = t
        } else if case let .brokenLink(msgTarget) = LintFinding.classify(issue: issue) {
            target = msgTarget
        } else {
            return []
        }
        return fixReport.ambiguous.first(where: { $0.path == issue.path && $0.target == target })?.candidates ?? []
    }

    private func openInObsidian(forPath path: String) {
        guard let vault = appState.vault else { return }
        let absURL = vault.rootURL.appendingPathComponent(path)
        let vaultName = ObsidianRegistry.load()?.vault(at: vault.rootURL)?.name
            ?? vault.rootURL.lastPathComponent
        ObsidianURL.open(vaultName: vaultName, relativePath: path, absoluteFileURL: absURL)
    }
}

// MARK: - Subviews

private struct FixNotePreviewRow: View {
    let note: FixNoteChange
    let onOpenInObsidian: () -> Void

    var body: some View {
        HStack(alignment: .top, spacing: 8) {
            Image(systemName: "doc.text")
                .foregroundStyle(.secondary)
            VStack(alignment: .leading, spacing: 4) {
                Text(note.path)
                    .font(.body)
                    .fontWeight(.medium)
                    .lineLimit(1)

                ForEach(note.linkActions) { la in
                    HStack(spacing: 6) {
                        Text(label(for: la))
                            .font(.caption)
                            .foregroundStyle(.secondary)
                        Text(badge(for: la))
                            .font(.caption2)
                            .foregroundStyle(color(for: la))
                            .padding(.horizontal, 6)
                            .padding(.vertical, 1)
                            .background(Capsule().fill(color(for: la).opacity(0.12)))
                    }
                }

                ForEach(note.frontmatterFixes) { ff in
                    HStack(spacing: 6) {
                        Text(frontmatterFixLabel(for: ff))
                            .font(.caption)
                            .foregroundStyle(.secondary)
                        Text(frontmatterFixBadge(for: ff))
                            .font(.caption2)
                            .foregroundStyle(.blue)
                            .padding(.horizontal, 6)
                            .padding(.vertical, 1)
                            .background(Capsule().fill(Color.blue.opacity(0.12)))
                    }
                }
            }
            Spacer()
            Button("Open", action: onOpenInObsidian)
                .controlSize(.small)
        }
        .padding(.vertical, 3)
    }

    private func frontmatterFixLabel(for ff: FixFieldChange) -> String {
        switch ff.reason {
        case "unquote_date":
            return "\(ff.field): unquote \(ff.to)"
        case "parse_error":
            return "Repair malformed YAML frontmatter"
        case "formatting":
            return "\(ff.field): \(ff.to)"
        default:
            return "set \(ff.field) = \(ff.to)"
        }
    }

    private func frontmatterFixBadge(for ff: FixFieldChange) -> String {
        switch ff.reason {
        case "parse_error": return "YAML repair"
        case "schema_enum": return "AI schema"
        case "formatting": return "format"
        default: return "frontmatter"
        }
    }

    private func label(for action: FixLinkAction) -> String {
        switch action.action {
        case "relink":
            return "[[\(action.from)]] → [[\(action.to ?? "")]]"
        case "unlink":
            return "[[\(action.from)]] → plain text"
        case "format":
            return "Format \(action.from) → \(action.to ?? "")"
        default:
            return action.from
        }
    }

    private func badge(for action: FixLinkAction) -> String {
        switch action.reason {
        case "drift": return "drift"
        case "moved_path": return "moved path"
        case "obvious_match": return "obvious match"
        case "dead_link": return "dead link"
        case "link_syntax": return "syntax"
        case "ai_suggest": return "AI match"
        case "ai_disambiguate": return "AI context"
        default: return action.action
        }
    }

    private func color(for action: FixLinkAction) -> Color {
        switch action.action {
        case "relink": return .green
        case "unlink": return .secondary
        case "format": return .blue
        default: return .secondary
        }
    }
}

private struct AIFixIssuePreviewRow: View {
    let issue: AIFixableIssue
    let onOpenInObsidian: () -> Void

    var body: some View {
        HStack(alignment: .top, spacing: 8) {
            Image(systemName: "wand.and.stars")
                .foregroundStyle(Color.accentColor)
            VStack(alignment: .leading, spacing: 4) {
                HStack(spacing: 6) {
                    Text(issue.path)
                        .font(.body)
                        .fontWeight(.medium)
                        .lineLimit(1)
                    Text(kindLabel)
                        .font(.caption2)
                        .foregroundStyle(Color.accentColor)
                        .padding(.horizontal, 6)
                        .padding(.vertical, 1)
                        .background(Capsule().fill(Color.accentColor.opacity(0.12)))
                }
                Text(issue.message)
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
            Spacer()
            Button("Open", action: onOpenInObsidian)
                .controlSize(.small)
        }
        .padding(.vertical, 3)
    }

    private var kindLabel: String {
        switch issue.kind {
        case "parse_error": return "YAML repair"
        case "schema_enum": return "schema enum"
        case "missing_required": return "required field"
        case "broken_link": return "AI link"
        case "formatting": return "formatting"
        default: return issue.kind
        }
    }
}

private struct ManualIssueRow: View {
    let issue: LintIssue
    let candidates: [String]
    let onOpenInObsidian: () -> Void
    let onSetValue: (ActiveSetValue) -> Void
    let onRepair: (ActiveRepair) -> Void

    private var finding: LintFinding { LintFinding.classify(issue: issue) }

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
                    if !candidates.isEmpty {
                        Text("ambiguous (\(candidates.count))")
                            .font(.caption2)
                            .foregroundStyle(.orange)
                            .padding(.horizontal, 6)
                            .padding(.vertical, 1)
                            .background(Capsule().fill(Color.orange.opacity(0.12)))
                    }
                }

                Text(issue.message)
                    .font(.caption)
                    .foregroundStyle(.secondary)

                if !candidates.isEmpty {
                    Text("Matches: \(candidates.joined(separator: ", "))")
                        .font(.caption2)
                        .foregroundStyle(.tertiary)
                }

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
                            let resolvedTarget = (issue.target?.isEmpty == false ? issue.target : nil) ?? target
                            onRepair(ActiveRepair(path: issue.path, target: resolvedTarget))
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

/// The finding being edited in the Set-value sheet.
struct ActiveSetValue: Identifiable {
    let id = UUID()
    let path: String
    let field: String
    let allowed: [String]
    let currentValue: String?
}

/// Sheet to set one frontmatter field, fixing a missing-required-field or
/// invalid-enum finding.
private struct SetValueSheet: View {
    @Environment(AppState.self) private var appState
    @Environment(\.dismiss) private var dismiss
    let item: ActiveSetValue
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
                errorText = AppState.boundedRedacted((error as? CLIError)?.errorDescription ?? error.localizedDescription)
            }
        }
    }
}

/// Resolves an ambiguous or manual broken `[[wikilink]]` with choices to repoint,
/// create the missing note, or unwrap to plain text.
private struct LinkResolutionSheet: View {
    @Environment(AppState.self) private var appState
    @Environment(\.dismiss) private var dismiss
    let fix: ActiveRepair
    var queuePosition: (index: Int, total: Int)? = nil
    var preloaded: SuggestVerdictEnvelope? = nil
    let onOpenInObsidian: () -> Void
    let onResolved: (String) -> Void
    let onStale: (String) -> Void
    var onSkip: (() -> Void)? = nil

    private enum RecommendedAction: Equatable { case relinkTop, unlink }
    private var recommendedAction: RecommendedAction? {
        guard !loading, confidentRepair == nil else { return nil }
        if let verdict {
            return verdict.action == "unlink" ? .unlink : .relinkTop
        }
        return filteredSuggestions.isEmpty ? .unlink : .relinkTop
    }

    @State private var loading = true
    @State private var repairPreview: PolishResult?
    @State private var suggestions: [SuggestTargetResult] = []
    @State private var verdict: SuggestRecommendation?
    @State private var guidanceText: String?
    @State private var errorText: String?
    @State private var busy = false

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
                Button(queuePosition == nil ? "Cancel" : "Done") { dismiss() }
                    .keyboardShortcut(.cancelAction)
                    .disabled(busy)
            }
        }
        .padding(16)
        .frame(width: 480)
        .task { await load() }
    }

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
            Label("Did you mean? (top \(min(filteredSuggestions.count, FixAllPlanner.maxRecommendations)))",
                  systemImage: "sparkle.magnifyingglass")
                .font(.subheadline).fontWeight(.medium)
            ForEach(Array(filteredSuggestions.prefix(FixAllPlanner.maxRecommendations).enumerated()), id: \.element.id) { index, cand in
                let recommended = recommendedAction == .relinkTop && index == 0
                HStack(spacing: 8) {
                    VStack(alignment: .leading, spacing: 1) {
                        HStack(spacing: 4) {
                            Text(cand.displayTitle).font(.callout)
                            if let conf = cand.confidence {
                                Text(conf)
                                    .font(.caption2)
                                    .foregroundStyle(conf == "high" ? .green : .secondary)
                            }
                        }
                        Text(cand.path).font(.caption2).foregroundStyle(.secondary).lineLimit(1)
                        if let reason = cand.reason, !reason.isEmpty {
                            Text(reason)
                                .font(.caption2)
                                .foregroundStyle(.tertiary)
                                .lineLimit(2)
                        }
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

    private func unlinkAction() {
        run("Unlinked [[\(fix.target)]].") {
            let res = try await appState.unlink(path: fix.path, target: fix.target, preview: false)
            return LinkFixOutcome.classify(result: res, target: fix.target, verb: "unlink")
        }
    }

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
        async let repairResult: PolishResult? = try? await appState.repairLinks(path: fix.path, target: fix.target, preview: true)
        if let preloaded {
            suggestions = preloaded.candidates
            verdict = preloaded.recommendation
        } else {
            if let env = try? await appState.suggestTargetVerdict(target: fix.target, sourcePath: fix.path) {
                suggestions = env.candidates
                verdict = env.recommendation
            } else {
                suggestions = (try? await appState.suggestTarget(
                    target: fix.target, sourcePath: fix.path, llm: true)) ?? []
                verdict = nil
            }
        }
        repairPreview = await repairResult
        loading = false
    }

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
                errorText = AppState.boundedRedacted((error as? CLIError)?.errorDescription ?? error.localizedDescription)
            }
        }
    }
}
