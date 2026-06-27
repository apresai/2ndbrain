import Foundation
import Testing
@testable import SecondBrain

// Pure presentation-logic tests for the Home "AI Clients" card: the
// ClientDescriptor catalog and the ClientConfig label/confirm mappers, mirroring
// HomeClaudeCodeTests. No SwiftUI rendering — all logic lives in the pure types.

// MARK: - test fixtures

private func skill(slug: String, user: Bool, project: Bool) -> SkillStatusInfo {
    SkillStatusInfo(
        slug: slug,
        name: slug,
        projectPath: ".claude/skills/2nb/SKILL.md",
        userPath: "~/.claude/skills/2nb/SKILL.md",
        projectInstalled: project,
        userInstalled: user,
        note: nil
    )
}

private func mcp(client: String, configured: Bool, scope: String?, configPath: String) -> MCPConfiguredInfo {
    MCPConfiguredInfo(
        client: client,
        configPath: configPath,
        configured: configured,
        scope: scope,
        serverKey: configured ? "2ndbrain" : nil,
        vaultPath: "/Users/test/dev/obsidian"
    )
}

// MARK: - ClientDescriptor catalog

@Test("ClientDescriptor.all lists the four clients in display order")
func clientDescriptorAllOrder() {
    #expect(ClientDescriptor.all.map(\.id) == ["claude-code", "warp", "claude-desktop", "codex"])
}

@Test("Claude Desktop has no skill, needs a restart, and uses an absolute CLI path")
func clientDescriptorClaudeDesktop() {
    let cd = ClientDescriptor.claudeDesktop
    #expect(cd.skillSlug == nil)
    #expect(cd.requiresRestart)
    #expect(cd.usesAbsoluteCLIPath)
    #expect(cd.mcpClientKey == "claude-desktop")
}

@Test("Codex carries the codex skill slug and no restart requirement")
func clientDescriptorCodex() {
    let codex = ClientDescriptor.codex
    #expect(codex.skillSlug == "codex")
    #expect(!codex.requiresRestart)
    #expect(!codex.usesAbsoluteCLIPath)
    #expect(codex.mcpClientKey == "codex")
}

@Test("Claude Code and Warp carry their own skill slugs and no restart")
func clientDescriptorSkillClients() {
    #expect(ClientDescriptor.claudeCode.skillSlug == "claude-code")
    #expect(ClientDescriptor.warp.skillSlug == "warp")
    #expect(!ClientDescriptor.claudeCode.requiresRestart)
    #expect(!ClientDescriptor.warp.requiresRestart)
}

// MARK: - ClientConfig.skillRow / mcpRow

@Test("ClientConfig.skillRow: unknown when nil, not-installed, and installed combos")
func clientConfigSkillRow() {
    let unknown = ClientConfig.skillRow(nil)
    #expect(unknown.label == "unknown")
    #expect(!unknown.ok)

    let notInstalled = ClientConfig.skillRow(skill(slug: "warp", user: false, project: false))
    #expect(notInstalled.label == "not installed")
    #expect(!notInstalled.ok)

    let userInstalled = ClientConfig.skillRow(skill(slug: "warp", user: true, project: false))
    #expect(userInstalled.label == "installed (user)")
    #expect(userInstalled.ok)

    let projectInstalled = ClientConfig.skillRow(skill(slug: "warp", user: false, project: true))
    #expect(projectInstalled.label == "installed (project)")
    #expect(projectInstalled.ok)
}

@Test("ClientConfig.mcpRow: unknown when nil, not-configured, and configured-with-scope combos")
func clientConfigMCPRow() {
    let unknown = ClientConfig.mcpRow(nil)
    #expect(unknown.label == "unknown")
    #expect(!unknown.ok)

    let notConfigured = ClientConfig.mcpRow(mcp(client: "warp", configured: false, scope: nil, configPath: "~/.warp/.mcp.json"))
    #expect(notConfigured.label == "not configured")
    #expect(!notConfigured.ok)

    let userScope = ClientConfig.mcpRow(mcp(client: "claude-code", configured: true, scope: "user", configPath: "~/.claude.json"))
    #expect(userScope.label == "configured (user scope)")
    #expect(userScope.ok)

    let noScope = ClientConfig.mcpRow(mcp(client: "codex", configured: true, scope: nil, configPath: "~/.codex/config.toml"))
    #expect(noScope.label == "configured")
    #expect(noScope.ok)
}

// MARK: - ClientConfig.configureConfirm / successMessage

@Test("configureConfirm(.claudeDesktop) calls out the absolute path and the quit-and-reopen step")
func configureConfirmClaudeDesktop() {
    let confirm = ClientConfig.configureConfirm(.claudeDesktop)
    #expect(confirm.title.contains("Claude Desktop"))
    #expect(confirm.info.contains("absolute"))
    #expect(confirm.info.contains("Quit"))
}

@Test("configureConfirm(.codex) mentions codex mcp add")
func configureConfirmCodex() {
    let confirm = ClientConfig.configureConfirm(.codex)
    #expect(confirm.info.contains("codex mcp add"))
}

@Test("configureConfirm(.claudeCode) is a plain confirm without restart/absolute copy")
func configureConfirmClaudeCode() {
    let confirm = ClientConfig.configureConfirm(.claudeCode)
    #expect(confirm.info.contains("skill"))
    #expect(!confirm.info.contains("absolute"))
    #expect(!confirm.info.contains("Quit"))
}

@Test("successMessage adds the restart nudge only for clients that need one")
func successMessageRestart() {
    #expect(ClientConfig.successMessage(.claudeDesktop).contains("Quit and reopen"))
    #expect(!ClientConfig.successMessage(.claudeCode).contains("Quit and reopen"))
    // Skill-bearing clients mention the skill; MCP-only ones don't.
    #expect(ClientConfig.successMessage(.warp).contains("skill"))
    #expect(!ClientConfig.successMessage(.claudeDesktop).contains("skill"))
}

// MARK: - [MCPConfiguredInfo] array decode + AppState.mcpConfigured(forClient:)

@Test("[MCPConfiguredInfo] decodes the 5-client --all array and mcpConfigured(forClient:) selects correctly")
@MainActor
func mcpConfiguredAllDecodeAndSelect() throws {
    // The `2nb mcp configured --all --json` contract: one entry per client.
    let json = """
    [
      {"client":"claude-code","config_path":"~/.claude.json","configured":true,"scope":"user","server_key":"2ndbrain","vault_path":"/v"},
      {"client":"claude-desktop","config_path":"~/Library/Application Support/Claude/claude_desktop_config.json","configured":false,"scope":null,"server_key":null,"vault_path":"/v"},
      {"client":"warp","config_path":"~/.warp/.mcp.json","configured":true,"scope":"user","server_key":"2ndbrain","vault_path":"/v"},
      {"client":"agents","config_path":"~/.config/agents/mcp.json","configured":false,"scope":null,"server_key":null,"vault_path":"/v"},
      {"client":"codex","config_path":"~/.codex/config.toml","configured":true,"scope":null,"server_key":"2ndbrain","vault_path":"/v"}
    ]
    """
    let decoded = try JSONDecoder().decode([MCPConfiguredInfo].self, from: Data(json.utf8))
    #expect(decoded.count == 5)
    #expect(decoded.map(\.client) == ["claude-code", "claude-desktop", "warp", "agents", "codex"])

    let state = AppState()
    state.mcpConfiguredAll = decoded

    // Per-client selection picks the matching entry.
    #expect(state.mcpConfigured(forClient: "warp")?.configured == true)
    #expect(state.mcpConfigured(forClient: "claude-desktop")?.configured == false)
    #expect(state.mcpConfigured(forClient: "codex")?.configPath == "~/.codex/config.toml")
    // An unknown client selects nothing.
    #expect(state.mcpConfigured(forClient: "nonexistent") == nil)
    // The back-compat accessor returns the claude-code entry.
    #expect(state.mcpConfigured?.client == "claude-code")
}
