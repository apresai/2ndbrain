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
                Button("Done") { isPresented = false }
                    .keyboardShortcut(.defaultAction)
            }
            .padding()

            Divider()

            if appState.mcpStatuses.isEmpty {
                VStack(spacing: 8) {
                    Image(systemName: "network.slash")
                        .font(.system(size: 32))
                        .foregroundStyle(.secondary)
                    Text("No MCP servers connected")
                        .font(.headline)
                    Text("Configure your AI client to run `2nb mcp-server` against this vault to see live activity here.")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                        .multilineTextAlignment(.center)
                        .padding(.horizontal, 40)
                    Button("Connect AI Tools...") {
                        isPresented = false
                        appState.showMCPSetup = true
                    }
                    .padding(.top, 4)
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
        .frame(width: 640, height: 480)
        .onAppear {
            Task { await appState.refreshMCPStatus() }
        }
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
