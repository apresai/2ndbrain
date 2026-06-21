import SwiftUI
#if canImport(AppKit)
import AppKit
#endif

/// The Claude Code "Verify" health panel: a one-click real end-to-end self-test
/// (skill + MCP engine + AI + reliability), shown inline under the Claude Code
/// card, with the reliability one-click fixes (Checkpoint WAL / Reap stale
/// servers).
struct ClaudeCodeHealthView: View {
    @Environment(AppState.self) private var appState
    @State private var actionMessage: String?
    @State private var actionIsError = false
    @State private var busyAction = false

    private static let groupOrder = ["Skill", "MCP engine", "AI"]

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack(spacing: 8) {
                Button(appState.verifying ? "Verifying…" : "Verify setup") {
                    Task { await appState.verifyClaudeCode() }
                }
                .controlSize(.small)
                .disabled(appState.verifying || appState.vault == nil)
                if appState.verifying {
                    ProgressView().controlSize(.small)
                }
                Spacer()
            }

            if let actionMessage {
                Label(actionMessage, systemImage: actionIsError ? "exclamationmark.triangle.fill" : "checkmark.circle.fill")
                    .font(.caption)
                    .foregroundStyle(actionIsError ? .orange : .green)
            }

            if !appState.healthChecks.isEmpty {
                ForEach(Self.groupOrder, id: \.self) { group in
                    let checks = appState.healthChecks.filter { $0.group == group }
                    if !checks.isEmpty {
                        Text(group)
                            .font(.caption).fontWeight(.semibold).foregroundStyle(.secondary)
                            .padding(.top, 2)
                        ForEach(checks) { checkRow($0) }
                    }
                }

                Divider().padding(.vertical, 2)
                Text("Reliability")
                    .font(.caption).fontWeight(.semibold).foregroundStyle(.secondary)
                HStack(spacing: 8) {
                    Button("Checkpoint WAL") { Task { await checkpoint() } }
                        .controlSize(.small).disabled(busyAction)
                    Button("Reap stale servers") { Task { await reap() } }
                        .controlSize(.small).disabled(busyAction)
                    if busyAction { ProgressView().controlSize(.small) }
                }
            }
        }
    }

    private func checkRow(_ c: HealthCheck) -> some View {
        HStack(alignment: .top, spacing: 6) {
            Image(systemName: glyph(c.state))
                .foregroundStyle(color(c.state))
                .font(.caption)
            VStack(alignment: .leading, spacing: 1) {
                Text(c.name).font(.caption)
                Text(c.detail).font(.caption2).foregroundStyle(.secondary)
                if c.state != .pass, let fix = c.fix {
                    Text(fix).font(.caption2).foregroundStyle(.tertiary)
                }
            }
            Spacer()
        }
    }

    private func glyph(_ s: HealthState) -> String {
        switch s {
        case .pass: return "checkmark.circle.fill"
        case .warn: return "exclamationmark.triangle.fill"
        case .fail: return "xmark.octagon.fill"
        case .running: return "circle.dotted"
        }
    }

    private func color(_ s: HealthState) -> Color {
        switch s {
        case .pass: return .green
        case .warn: return .orange
        case .fail: return .red
        case .running: return .secondary
        }
    }

    // MARK: - Reliability actions

    private func checkpoint() async {
        busyAction = true
        defer { busyAction = false }
        do {
            let r = try await appState.checkpointWAL()
            let reclaimed = r.walBytesBefore - r.walBytesAfter
            if reclaimed > 0 {
                setMessage("Checkpointed the WAL — reclaimed \(humanBytes(reclaimed)).", error: false)
            } else if r.busy {
                setMessage("A reader is active; the WAL couldn't be fully truncated. Try again when idle.", error: false)
            } else {
                setMessage("The index WAL is already compact.", error: false)
            }
        } catch {
            setMessage("Checkpoint failed: \(error.localizedDescription)", error: true)
        }
    }

    private func reap() async {
        busyAction = true
        defer { busyAction = false }
        do {
            let preview = try await appState.reapServers(dryRun: true)
            if preview.reaped.isEmpty {
                setMessage("No stale mcp-server processes to reap.", error: false)
                return
            }
            #if canImport(AppKit)
            guard confirm(
                title: "Reap \(preview.reaped.count) stale mcp-server process(es)?",
                info: "These servers have been idle past the threshold and will be sent SIGTERM (they exit cleanly). Your AI client respawns one on the next call."
            ) else { return }
            #endif
            let res = try await appState.reapServers(dryRun: false)
            setMessage("Reaped \(res.reaped.count) stale server(s).", error: false)
        } catch {
            setMessage("Reap failed: \(error.localizedDescription)", error: true)
        }
    }

    private func setMessage(_ msg: String, error: Bool) {
        actionMessage = msg
        actionIsError = error
    }

    #if canImport(AppKit)
    @MainActor private func confirm(title: String, info: String) -> Bool {
        let alert = NSAlert()
        alert.messageText = title
        alert.informativeText = info
        alert.addButton(withTitle: "Continue")
        alert.addButton(withTitle: "Cancel")
        return alert.runModal() == .alertFirstButtonReturn
    }
    #endif

    private func humanBytes(_ n: Int64) -> String {
        if n >= 1 << 20 { return String(format: "%.1f MB", Double(n) / Double(1 << 20)) }
        if n >= 1 << 10 { return String(format: "%.1f KB", Double(n) / Double(1 << 10)) }
        return "\(n) B"
    }
}
