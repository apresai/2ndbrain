import SwiftUI

struct MCPServerStatusInfo: Codable, Identifiable {
    let pid: Int
    let startedAt: String
    let parentPid: Int?
    let lastInvocation: String?
    let invocations: [MCPToolInvocationInfo]

    var id: Int { pid }

    enum CodingKeys: String, CodingKey {
        case pid
        case startedAt = "started_at"
        case parentPid = "parent_pid"
        case lastInvocation = "last_invocation"
        case invocations
    }
}

struct MCPToolInvocationInfo: Codable, Identifiable {
    let tool: String
    let timestamp: String
    let ok: Bool
    let durationMs: Int
    let error: String?

    var id: String { "\(timestamp)-\(tool)" }

    enum CodingKeys: String, CodingKey {
        case tool
        case timestamp
        case ok
        case durationMs = "duration_ms"
        case error
    }
}

struct MCPStatusView: View {
    @Environment(AppState.self) var appState
    @Binding var isPresented: Bool
    var isInline: Bool = false
    @State private var expanded: Set<Int> = []

    var body: some View {
        VStack(spacing: 0) {
            HStack {
                Text("MCP Server Status")
                    .font(.title2.bold())
                Spacer()
                Button {
                    Task { await appState.refreshMCPStatus() }
                } label: {
                    Image(systemName: "arrow.clockwise")
                }
                .buttonStyle(.plain)
                if !isInline {
                    Button("Done") { isPresented = false }
                        .keyboardShortcut(.defaultAction)
                }
            }
            .padding()

            Divider()

            configuredBanner

            if appState.mcpStatuses.isEmpty {
                VStack(spacing: 8) {
                    Image(systemName: "network.slash")
                        .font(.system(size: 32))
                        .foregroundStyle(.secondary)
                    Text("No MCP servers running")
                        .font(.headline)
                    Text(emptyStateMessage)
                        .font(.caption)
                        .foregroundStyle(.secondary)
                        .multilineTextAlignment(.center)
                        .padding(.horizontal, 40)
                    // The not-configured setup action lives in `configuredBanner`
                    // above; no second button here (it would duplicate it).
                }
                .frame(maxWidth: .infinity, maxHeight: .infinity)
                .padding()
            } else {
                List {
                    ForEach(appState.mcpStatuses) { status in
                        serverRow(status)
                    }
                }
                .listStyle(.inset)
            }
        }
        .frame(width: isInline ? nil : 640, height: isInline ? nil : 480)
        .onAppear {
            Task {
                await appState.refreshMCPStatus()
                await appState.refreshMCPConfigured()
            }
        }
    }

    /// Durable "is the server set up?" line above the live-activity list. The
    /// server is launched on demand by the AI client, so the list below is
    /// empty whenever the client is closed; this banner answers "will my AI
    /// tool find this vault?" independent of whether a server is running now.
    @ViewBuilder
    private var configuredBanner: some View {
        let state = HomeMCPConfigured.rowState(appState.mcpConfigured)
        let isConfigured = appState.mcpConfigured?.configured ?? false
        HStack(spacing: 8) {
            Image(systemName: isConfigured ? "checkmark.seal.fill" : "questionmark.circle")
                .foregroundStyle(isConfigured ? Color.green : Color.secondary)
            Text("Configured in ~/.claude.json: \(state.label)")
                .font(.callout)
            Spacer()
            if !isConfigured, !isInline {
                Button("Show setup") {
                    Task {
                        await appState.loadMCPSetup()
                        appState.showMCPSetup = true
                    }
                }
                .controlSize(.small)
            }
        }
        .padding(.horizontal)
        .padding(.vertical, 8)
    }

    @ViewBuilder
    private func serverRow(_ status: MCPServerStatusInfo) -> some View {
        let isOpen = expanded.contains(status.pid)
        VStack(alignment: .leading, spacing: 4) {
            HStack(spacing: 8) {
                Circle()
                    .fill(.green)
                    .frame(width: 8, height: 8)
                Text("PID \(status.pid)")
                    .font(.body.monospacedDigit())
                if let parentPid = status.parentPid, parentPid > 0 {
                    Text("parent \(parentPid)")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
                Spacer()
                Text("\(status.invocations.count) invocations")
                    .font(.caption)
                    .foregroundStyle(.secondary)
                Button {
                    if isOpen {
                        expanded.remove(status.pid)
                    } else {
                        expanded.insert(status.pid)
                    }
                } label: {
                    Image(systemName: isOpen ? "chevron.down" : "chevron.right")
                        .font(.caption)
                }
                .buttonStyle(.plain)
            }
            HStack(spacing: 12) {
                Text("Started \(formatTimestamp(status.startedAt))")
                    .font(.caption)
                    .foregroundStyle(.secondary)
                if let last = status.lastInvocation, !last.isEmpty {
                    Text("Last \(formatTimestamp(last))")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
            }
            if isOpen {
                Divider().padding(.vertical, 4)
                if status.invocations.isEmpty {
                    Text("No tool invocations yet.")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                        .padding(.vertical, 4)
                } else {
                    VStack(alignment: .leading, spacing: 2) {
                        ForEach(status.invocations.reversed()) { inv in
                            HStack(spacing: 8) {
                                Circle()
                                    .fill(inv.ok ? Color.green : Color.red)
                                    .frame(width: 6, height: 6)
                                Text(inv.tool)
                                    .font(.system(.caption, design: .monospaced))
                                Spacer()
                                Text("\(inv.durationMs) ms")
                                    .font(.caption)
                                    .foregroundStyle(.secondary)
                                Text(formatTimestamp(inv.timestamp))
                                    .font(.caption)
                                    .foregroundStyle(.tertiary)
                            }
                            if let err = inv.error, !err.isEmpty {
                                Text(err)
                                    .font(.caption2)
                                    .foregroundStyle(.red)
                                    .padding(.leading, 14)
                            }
                        }
                    }
                }
            }
        }
        .padding(.vertical, 4)
    }

    /// When the server is configured but nothing is running, that's the normal
    /// resting state (the client starts it on demand); say so, rather than
    /// implying it isn't set up. When it isn't configured, point at setup.
    private var emptyStateMessage: String {
        if appState.mcpConfigured?.configured ?? false {
            return "The server is configured. Your AI client launches it on demand, so live activity appears here only while the client is connected to this vault."
        }
        return "Configure your AI client to run `2nb mcp-server` against this vault to see live activity here."
    }

    private func formatTimestamp(_ raw: String) -> String {
        let iso = ISO8601DateFormatter()
        iso.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        if let date = iso.date(from: raw) ?? ISO8601DateFormatter().date(from: raw) {
            let fmt = DateFormatter()
            fmt.dateStyle = .none
            fmt.timeStyle = .medium
            return fmt.string(from: date)
        }
        return raw
    }
}
