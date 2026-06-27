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

private func setupResult(
    client: String,
    configured: Bool = true,
    instructions: String? = nil,
    error: String? = nil
) -> SetupClientResult {
    SetupClientResult(
        client: client,
        skillSlug: nil,
        skillPath: nil,
        skillBackup: nil,
        skillSkipped: nil,
        mcpConfigPath: configured ? "~/.claude.json" : nil,
        mcpBackup: nil,
        mcpChanged: configured,
        configured: configured,
        instructions: instructions,
        error: error
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

@Test("configureConfirm(.warp) is a plain skill+MCP confirm, no restart/absolute copy")
func configureConfirmWarp() {
    let confirm = ClientConfig.configureConfirm(.warp)
    #expect(confirm.title.contains("Warp"))
    #expect(confirm.info.contains("skill"))
    #expect(!confirm.info.contains("absolute"))
    #expect(!confirm.info.contains("Quit"))
}

@Test("successMessage(.codex) mentions the skill and needs no restart nudge")
func successMessageCodex() {
    let msg = ClientConfig.successMessage(.codex)
    #expect(msg.contains("Codex"))
    #expect(msg.contains("skill"))
    #expect(!msg.contains("Quit and reopen"))
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

// MARK: - ClientConfig.configureOutcome (false-success guard)

@Test("configureOutcome: success only when configured with no error/instructions")
func configureOutcomeSuccess() {
    let outcome = ClientConfig.configureOutcome(.claudeCode, result: setupResult(client: "claude-code"))
    #expect(outcome == .success(ClientConfig.successMessage(.claudeCode)))
}

@Test("configureOutcome: instructions (no error) is a manual step, not success")
func configureOutcomeManual() {
    // The worst case from the review: Codex with no `codex` CLI returns
    // instructions + configured=false, exit 0 — must NOT read as configured.
    let result = setupResult(
        client: "codex",
        configured: false,
        instructions: "Run: codex mcp add 2ndbrain -- 2nb mcp-server"
    )
    let outcome = ClientConfig.configureOutcome(.codex, result: result)
    guard case let .manual(msg) = outcome else {
        Issue.record("expected .manual, got \(outcome)")
        return
    }
    #expect(msg.contains("codex mcp add"))
    #expect(msg.contains("manual step"))
}

@Test("configureOutcome: a non-empty error is a failure, not success")
func configureOutcomeError() {
    let result = setupResult(client: "warp", configured: false, error: "mcp: permission denied")
    let outcome = ClientConfig.configureOutcome(.warp, result: result)
    guard case let .failure(msg) = outcome else {
        Issue.record("expected .failure, got \(outcome)")
        return
    }
    #expect(msg.contains("permission denied"))
}

@Test("configureOutcome: error wins over instructions")
func configureOutcomeErrorBeatsInstructions() {
    let result = setupResult(
        client: "codex",
        configured: false,
        instructions: "Run: codex mcp add ...",
        error: "mcp: codex add failed"
    )
    if case .failure = ClientConfig.configureOutcome(.codex, result: result) {
        // expected
    } else {
        Issue.record("error must take precedence over instructions")
    }
}

@Test("configureOutcome: a missing result entry is a failure, never a false success")
func configureOutcomeNil() {
    if case .failure = ClientConfig.configureOutcome(.codex, result: nil) {
        // expected
    } else {
        Issue.record("a nil result must be a failure")
    }
}

// MARK: - [SetupClientResult] array decode (2nb setup --json --porcelain)

@Test("[SetupClientResult] decodes the setup --json array and the codex manual case maps to .manual")
func setupClientResultDecode() throws {
    // Mirrors `2nb setup --all --json`: claude-code fully configured; codex with
    // no `codex` CLI emits instructions + configured=false at exit 0.
    let json = """
    [
      {"client":"claude-code","skill_slug":"claude-code","skill_path":"~/.claude/skills/2nb/SKILL.md","mcp_config_path":"~/.claude.json","mcp_changed":true,"configured":true},
      {"client":"codex","skill_slug":"codex","skill_path":"~/.codex/skills/2nb/SKILL.md","mcp_changed":false,"configured":false,"instructions":"Run: codex mcp add 2ndbrain -- 2nb mcp-server"}
    ]
    """
    let decoded = try JSONDecoder().decode([SetupClientResult].self, from: Data(json.utf8))
    #expect(decoded.count == 2)

    let cc = try #require(decoded.first { $0.client == "claude-code" })
    #expect(cc.configured)
    #expect(cc.mcpChanged)
    #expect(cc.error == nil)
    #expect(cc.instructions == nil)
    #expect(ClientConfig.configureOutcome(.claudeCode, result: cc) == .success(ClientConfig.successMessage(.claudeCode)))

    let codex = try #require(decoded.first { $0.client == "codex" })
    #expect(!codex.configured)
    #expect(codex.instructions?.contains("codex mcp add") == true)
    // The whole point: exit 0 + configured=false must surface as a manual step.
    if case .manual = ClientConfig.configureOutcome(.codex, result: codex) {
        // expected
    } else {
        Issue.record("codex instructions case must map to .manual")
    }
}
