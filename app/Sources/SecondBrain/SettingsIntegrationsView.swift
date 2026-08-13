import SwiftUI
#if canImport(AppKit)
import AppKit
#endif

/// Integrations: which AI tools can reach this vault.
///
/// Lifted out of the Home dashboard, where it sat below the fold as one card
/// among five. It is configuration — it writes `~/.claude.json`, `~/.warp/.mcp.json`,
/// and the clients' memory files — so it belongs in the Settings window with
/// the rest of the configuration.
///
/// Every status row and the Configure action reuse the existing ClientConfig
/// mappers and `2nb setup --client`, so a row here reads identically to the one
/// on Home and there is no second place for the wording to drift.
struct SettingsIntegrationsView: View {
    @Environment(AppState.self) var appState

    @State private var busyClient: String?
    @State private var message: String?

    var body: some View {
        Form {
            Section {
                Text("Each tool needs the 2ndbrain MCP server wired into its config before it can search or answer from this vault. Configure writes that entry, backing up anything it touches.")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }

            ForEach(ClientDescriptor.all) { client in
                Section(client.displayName) {
                    clientBody(client)
                }
            }

            if let message {
                Section {
                    Text(message)
                        .font(.caption)
                        .foregroundStyle(.secondary)
                        .textSelection(.enabled)
                }
            }
        }
        .formStyle(.grouped)
        .task { await reload() }
    }

    /// One client's rows. Carries everything the Home card used to, so removing
    /// that card loses nothing: the written config path, Claude Code's
    /// cross-dependency callout, the setup-snippet fallback, and the end-to-end
    /// Verify panel.
    @ViewBuilder
    private func clientBody(_ client: ClientDescriptor) -> some View {
        let mcp = appState.mcpConfigured(forClient: client.mcpClientKey)
        let mcpState = ClientConfig.mcpRow(mcp)

        if let slug = client.skillSlug {
            statusRow("Agent skill", ClientConfig.skillRow(appState.skillStatus(forSlug: slug)))
        }
        statusRow("MCP server", mcpState)
        if mcpState.ok, let path = mcp?.configPath, !path.isEmpty {
            Text(path)
                .font(.caption.monospaced())
                .foregroundStyle(.secondary)
                .textSelection(.enabled)
        }
        if let instructions = appState.globalInstructions(forClient: client.mcpClientKey) {
            statusRow("Global instructions", ClientConfig.globalInstructionsRow(instructions))
        }
        if let note = client.note, !note.isEmpty {
            Text(note).font(.caption).foregroundStyle(.secondary)
        }

        Button(busyClient == client.id ? "Configuring…" : "Configure") {
            Task { await configure(client) }
        }
        .disabled(busyClient != nil || appState.vault == nil)

        // Claude Code keeps the richer affordances: it is the only client that
        // needs BOTH a skill and an MCP entry, so a half-configured state is
        // worth calling out, and it is the one the Verify panel exercises.
        if client.id == ClientDescriptor.claudeCode.id {
            if let dep = crossDepMessage {
                Label(dep, systemImage: "link")
                    .font(.caption)
                    .foregroundStyle(.orange)
            }
            Button("Show setup snippet") {
                Task {
                    await appState.loadMCPSetup()
                    appState.showMCPSetup = true
                }
            }
            .controlSize(.small)
            ClaudeCodeHealthView()
        }
    }

    /// Warns when exactly one of {skill, MCP} is configured for Claude Code:
    /// either alone looks configured but does not work. The wording lives in
    /// ClaudeCodeHealth so this reads identically to the Verify panel below it.
    private var crossDepMessage: String? {
        let skill = appState.skillStatus(forSlug: "claude-code")
        let skillInstalled = (skill?.userInstalled ?? false) || (skill?.projectInstalled ?? false)
        let mcpConfigured = ClientConfig.mcpRow(appState.mcpConfigured(forClient: "claude-code")).ok
        return ClaudeCodeHealth.crossDependency(skillInstalled: skillInstalled, mcpConfigured: mcpConfigured)
    }

    @ViewBuilder
    private func statusRow(_ label: String, _ state: (label: String, ok: Bool)) -> some View {
        LabeledContent(label) {
            HStack(spacing: 6) {
                Image(systemName: state.ok ? "checkmark.circle.fill" : "circle")
                    .foregroundStyle(state.ok ? .green : .secondary)
                Text(state.label).foregroundStyle(.secondary)
            }
        }
    }

    private func reload() async {
        await appState.refreshSkillStatus()
        await appState.refreshMCPConfigured()
        await appState.refreshGlobalInstructions()
    }

    private func configure(_ client: ClientDescriptor) async {
        #if canImport(AppKit)
        let confirm = ClientConfig.configureConfirm(client)
        let alert = NSAlert()
        alert.messageText = confirm.title
        alert.informativeText = confirm.info
        alert.addButton(withTitle: "Configure")
        alert.addButton(withTitle: "Cancel")
        guard alert.runModal() == .alertFirstButtonReturn else { return }
        #endif

        busyClient = client.id
        defer { busyClient = nil }
        do {
            let results = try await appState.setupClient(client.mcpClientKey)
            let outcome = ClientConfig.configureOutcome(
                client,
                result: results.first { $0.client == client.mcpClientKey } ?? results.first
            )
            // All three cases carry their own copy; a manual step must not read
            // as success (Codex with no `codex` CLI is the case that matters).
            switch outcome {
            case .success(let text), .manual(let text), .failure(let text):
                message = text
            }
            await reload()
        } catch {
            message = "Configuring \(client.displayName) failed: \(error.localizedDescription)"
        }
    }
}
