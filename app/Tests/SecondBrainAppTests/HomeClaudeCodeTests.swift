import Foundation
import Testing
@testable import SecondBrain

// Pure presentation-logic tests for the Home "Claude Code" card rows
// (skill installed + MCP server configured), mirroring HomeInstallerTests.

// MARK: - test fixtures

private func skill(user: Bool, project: Bool) -> SkillStatusInfo {
    SkillStatusInfo(
        slug: "claude-code",
        name: "Claude Code",
        projectPath: ".claude/skills/2nb/SKILL.md",
        userPath: "~/.claude/skills/2nb/SKILL.md",
        projectInstalled: project,
        userInstalled: user,
        note: nil
    )
}

private func mcp(configured: Bool, scope: String?) -> MCPConfiguredInfo {
    MCPConfiguredInfo(
        client: "claude-code",
        configPath: "~/.claude.json",
        configured: configured,
        scope: scope,
        serverKey: configured ? "2ndbrain" : nil,
        vaultPath: "/Users/test/dev/obsidian"
    )
}

// MARK: - HomeSkill

@Test("HomeSkill.rowState: unknown when nil, Install when not installed, no button when installed")
func homeSkillRowState() {
    // nil status (slug not found, or a pre-0.8.1 CLI without `skills list`)
    // reads "unknown" with no button, not a misleading "not installed".
    let unknown = HomeSkill.rowState(nil)
    #expect(unknown.label == "unknown")
    #expect(unknown.button == nil)

    let notInstalled = HomeSkill.rowState(skill(user: false, project: false))
    #expect(notInstalled.label == "not installed")
    #expect(notInstalled.button == "Install")

    let userInstalled = HomeSkill.rowState(skill(user: true, project: false))
    #expect(userInstalled.label == "installed (user)")
    #expect(userInstalled.button == nil)

    let projectInstalled = HomeSkill.rowState(skill(user: false, project: true))
    #expect(projectInstalled.label == "installed (project)")
    #expect(projectInstalled.button == nil)

    // User scope wins the displayed label when both are installed (the app
    // installs at user scope).
    let both = HomeSkill.rowState(skill(user: true, project: true))
    #expect(both.label == "installed (user)")
    #expect(both.button == nil)
}

@Test("HomeSkill.successMessage mentions user scope and the next-session caveat")
func homeSkillSuccessMessage() {
    let msg = HomeSkill.successMessage()
    #expect(msg.contains("user"))
    #expect(msg.contains("Claude Code"))
}

// MARK: - HomeMCPConfigured

@Test("HomeMCPConfigured.rowState: unknown when nil, Show setup when not configured, scope label when configured")
func homeMCPConfiguredRowState() {
    let unknown = HomeMCPConfigured.rowState(nil)
    #expect(unknown.label == "unknown")
    #expect(unknown.button == nil)

    let notConfigured = HomeMCPConfigured.rowState(mcp(configured: false, scope: nil))
    #expect(notConfigured.label == "not configured")
    #expect(notConfigured.button == "Show setup")

    let userScope = HomeMCPConfigured.rowState(mcp(configured: true, scope: "user"))
    #expect(userScope.label == "configured (user scope)")
    #expect(userScope.button == nil)

    let projectScope = HomeMCPConfigured.rowState(mcp(configured: true, scope: "project"))
    #expect(projectScope.label == "configured (project scope)")
    #expect(projectScope.button == nil)

    // Configured but no scope reported → plain "configured", still no button.
    let noScope = HomeMCPConfigured.rowState(mcp(configured: true, scope: nil))
    #expect(noScope.label == "configured")
    #expect(noScope.button == nil)
}
