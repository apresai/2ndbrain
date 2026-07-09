import Foundation
import Testing
@testable import SecondBrain

// MARK: - Decoder contracts (must match the Go JSON)

@Test("MCPDoctorInfo decodes the mcp doctor contract")
func mcpDoctorDecodes() throws {
    let json = """
    {
      "ok": true, "configured": true, "scope": "user", "config_path": "~/.claude.json",
      "tool_count": 22, "tools_exercised": ["kb_info","kb_list","kb_search"],
      "instructions_present": true, "wal_bytes": 4096, "db_bytes": 131072,
      "alive_servers": 1, "stale_servers": 0,
      "checks": [ {"name":"kb_info round-trip","ok":true,"detail":"kb_info answered"},
                  {"name":"mcp configured (~/.claude.json)","ok":true,"warn":false,"detail":"configured (user scope) in ~/.claude.json","fix":"run `2nb mcp install`"} ]
    }
    """.data(using: .utf8)!
    let r = try JSONDecoder().decode(MCPDoctorInfo.self, from: json)
    #expect(r.ok && r.configured && r.scope == "user")
    #expect(r.toolCount == 22 && r.instructionsPresent && r.toolsExercised.count == 3)
    #expect(r.checks.count == 2 && r.checks[0].name == "kb_info round-trip")
}

@Test("SkillDoctorInfo decodes the flat skills doctor contract")
func skillDoctorDecodes() throws {
    let json = """
    { "slug":"claude-code","name":"Claude Code","project_path":".claude/skills/2nb/SKILL.md",
      "user_path":"~/.claude/skills/2nb/SKILL.md","project_installed":false,"user_installed":true,
      "installed":true,"file_nonempty":true,"parses":true,"binary_on_path":true,"binary_ok":true,
      "binary_version":"2nb version 0.9.10","self_path":"/x/2nb","ok":true,
      "checks":[{"name":"skill installed","ok":true,"detail":"installed (user scope)"}] }
    """.data(using: .utf8)!
    let r = try JSONDecoder().decode(SkillDoctorInfo.self, from: json)
    #expect(r.installed && r.userInstalled && r.binaryOnPath && r.binaryOK)
    #expect(r.binaryVersion == "2nb version 0.9.10")
}

@Test("MCPInstallInfo decodes all 7 non-optional fields")
func mcpInstallDecodes() throws {
    let json = """
    { "client":"claude-code","config_path":"~/.claude.json","configured":true,"changed":true,
      "backup_path":"/x/.claude.json.bak","server_key":"2ndbrain","scope":"user" }
    """.data(using: .utf8)!
    let r = try JSONDecoder().decode(MCPInstallInfo.self, from: json)
    #expect(r.configured && r.changed && r.scope == "user" && r.serverKey == "2ndbrain")
    #expect(r.backupPath == "/x/.claude.json.bak")
}

@Test("Checkpoint + reap results decode")
func reliabilityResultsDecode() throws {
    let ckpt = """
    {"wal_bytes_before":4194304,"wal_bytes_after":0,"db_bytes":372736,"pages_checkpointed":5,"pages_total":5,"busy":false}
    """.data(using: .utf8)!
    let c = try JSONDecoder().decode(VaultCheckpointResult.self, from: ckpt)
    #expect(c.walBytesBefore == 4194304 && c.walBytesAfter == 0 && !c.busy)

    let reap = """
    {"reaped":[{"pid":123,"age":"9h0m0s","last_invocation":"2020-01-01T00:00:00Z"}],"skipped":[{"pid":456,"reason":"current process"}],"threshold":"6h0m0s","dry_run":true}
    """.data(using: .utf8)!
    let rr = try JSONDecoder().decode(MCPReapResult.self, from: reap)
    #expect(rr.dryRun && rr.reaped.first?.pid == 123 && rr.skipped.first?.reason == "current process")
}

// MARK: - Pure mapping

@Test("health state maps ok/warn to pass/warn/fail")
func healthStateMapping() {
    #expect(ClaudeCodeHealth.state(ok: true, warn: false) == .pass)
    #expect(ClaudeCodeHealth.state(ok: true, warn: true) == .warn)
    #expect(ClaudeCodeHealth.state(ok: false, warn: false) == .fail)
}

@Test("a nil doctor report becomes a single failing row, not nothing")
func nilDoctorBecomesFail() {
    let checks = ClaudeCodeHealth.checks(group: "MCP engine", from: nil)
    #expect(checks.count == 1 && checks[0].state == .fail && checks[0].group == "MCP engine")
}

@Test("doctor checks map into grouped HealthChecks")
func doctorChecksMap() {
    let dc = [
        DoctorCheckInfo(name: "a", ok: true, warn: nil, detail: "ok", fix: nil),
        DoctorCheckInfo(name: "b", ok: true, warn: true, detail: "warn", fix: "do x"),
        DoctorCheckInfo(name: "c", ok: false, warn: nil, detail: "bad", fix: "fix it"),
    ]
    let checks = ClaudeCodeHealth.checks(group: "Skill", from: dc)
    #expect(checks.map(\.state) == [.pass, .warn, .fail])
    #expect(checks.allSatisfy { $0.group == "Skill" })
}

@Test("cross-dependency warns when exactly one of skill/MCP is set up")
func crossDependency() {
    #expect(ClaudeCodeHealth.crossDependency(skillInstalled: true, mcpConfigured: false) != nil)
    #expect(ClaudeCodeHealth.crossDependency(skillInstalled: false, mcpConfigured: true) != nil)
    #expect(ClaudeCodeHealth.crossDependency(skillInstalled: true, mcpConfigured: true) == nil)
    #expect(ClaudeCodeHealth.crossDependency(skillInstalled: false, mcpConfigured: false) == nil)
}

@Test("modelCheck maps a probe; nil probe is a fail")
func modelCheckMapping() {
    let ok = AIProbeResult(modelID: "m", provider: "bedrock", modelType: "embedding", ok: true, detail: "fine", latency: "120ms", errorCode: nil, remediation: nil, invokeStrategy: nil)
    #expect(ClaudeCodeHealth.modelCheck(ok, label: "Embedding").state == .pass)
    #expect(ClaudeCodeHealth.modelCheck(nil, label: "Generation").state == .fail)
}
