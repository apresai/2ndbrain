import Foundation

// MARK: - CLI JSON decoders (match the Go contracts in internal/cli + internal/mcp)

/// One check from any `2nb * doctor` report (shared shape with config doctor).
struct DoctorCheckInfo: Codable, Identifiable {
    var id: String { name }
    let name: String
    let ok: Bool
    let warn: Bool?
    let detail: String
    let fix: String?
}

/// `2nb mcp doctor --json`.
struct MCPDoctorInfo: Codable {
    let ok: Bool
    let configured: Bool
    let scope: String?
    let configPath: String
    let toolCount: Int
    let toolsExercised: [String]
    let instructionsPresent: Bool
    let walBytes: Int64
    let dbBytes: Int64
    let aliveServers: Int
    let staleServers: Int
    let checks: [DoctorCheckInfo]

    enum CodingKeys: String, CodingKey {
        case ok, configured, scope, checks
        case configPath = "config_path"
        case toolCount = "tool_count"
        case toolsExercised = "tools_exercised"
        case instructionsPresent = "instructions_present"
        case walBytes = "wal_bytes"
        case dbBytes = "db_bytes"
        case aliveServers = "alive_servers"
        case staleServers = "stale_servers"
    }
}

/// `2nb skills doctor --json` (flat: InstallStatus fields + verification fields).
struct SkillDoctorInfo: Codable {
    let slug: String
    let name: String
    let installed: Bool
    let userInstalled: Bool
    let projectInstalled: Bool
    let binaryOnPath: Bool
    let binaryOK: Bool
    let binaryVersion: String?
    let selfPath: String?
    let ok: Bool
    let checks: [DoctorCheckInfo]

    enum CodingKeys: String, CodingKey {
        case slug, name, installed, ok, checks
        case userInstalled = "user_installed"
        case projectInstalled = "project_installed"
        case binaryOnPath = "binary_on_path"
        case binaryOK = "binary_ok"
        case binaryVersion = "binary_version"
        case selfPath = "self_path"
    }
}

/// `2nb config doctor --json`.
struct ConfigDoctorInfo: Codable {
    let ok: Bool
    let checks: [DoctorCheckInfo]
}

/// `2nb mcp install --json` (every field is always emitted by Go — non-optional).
struct MCPInstallInfo: Codable {
    let client: String
    let configPath: String
    let configured: Bool
    let changed: Bool
    let backupPath: String
    let serverKey: String
    let scope: String

    enum CodingKeys: String, CodingKey {
        case client, configured, changed, scope
        case configPath = "config_path"
        case backupPath = "backup_path"
        case serverKey = "server_key"
    }
}

/// One per-client entry of `2nb setup --json` (mirrors the Go `SetupClientResult`
/// in cli/internal/cli/setup_cmd.go). `2nb setup` exits 0 even when a client step
/// fails (`error` set) or only needs a manual step (`instructions` set — e.g. the
/// `codex` CLI isn't installed so its MCP can't be wired and `configured` is
/// false), so the app must inspect these fields rather than trust the exit code.
/// All string fields are `omitempty` on the Go side, hence optional here.
struct SetupClientResult: Codable {
    let client: String
    let skillSlug: String?
    let skillPath: String?
    let skillBackup: String?
    let skillSkipped: String?
    let mcpConfigPath: String?
    let mcpBackup: String?
    let mcpChanged: Bool
    let configured: Bool
    let instructions: String?
    let error: String?

    enum CodingKeys: String, CodingKey {
        case client, configured, instructions, error
        case skillSlug = "skill_slug"
        case skillPath = "skill_path"
        case skillBackup = "skill_backup"
        case skillSkipped = "skill_skipped"
        case mcpConfigPath = "mcp_config_path"
        case mcpBackup = "mcp_backup"
        case mcpChanged = "mcp_changed"
    }
}

/// `2nb vault checkpoint --json`.
struct VaultCheckpointResult: Codable {
    let walBytesBefore: Int64
    let walBytesAfter: Int64
    let dbBytes: Int64
    let pagesCheckpointed: Int
    let pagesTotal: Int
    let busy: Bool

    enum CodingKeys: String, CodingKey {
        case walBytesBefore = "wal_bytes_before"
        case walBytesAfter = "wal_bytes_after"
        case dbBytes = "db_bytes"
        case pagesCheckpointed = "pages_checkpointed"
        case pagesTotal = "pages_total"
        case busy
    }
}

struct ReapedServerInfo: Codable, Identifiable {
    var id: Int { pid }
    let pid: Int
    let age: String
}

struct SkippedServerInfo: Codable, Identifiable {
    var id: Int { pid }
    let pid: Int
    let reason: String
}

/// `2nb mcp reap --json`.
struct MCPReapResult: Codable {
    let reaped: [ReapedServerInfo]
    let skipped: [SkippedServerInfo]
    let threshold: String
    let dryRun: Bool

    enum CodingKeys: String, CodingKey {
        case reaped, skipped, threshold
        case dryRun = "dry_run"
    }
}

// MARK: - Health checklist model + pure mapping

enum HealthState: Equatable {
    case running, pass, warn, fail
}

struct HealthCheck: Identifiable {
    let id = UUID()
    let group: String
    let name: String
    let state: HealthState
    let detail: String
    let fix: String?
}

/// Pure mapping from CLI doctor results to the GUI checklist. Kept free of any
/// view/AppState dependency so it's unit-tested directly.
enum ClaudeCodeHealth {
    static func state(ok: Bool, warn: Bool) -> HealthState {
        if !ok { return .fail }
        return warn ? .warn : .pass
    }

    /// Map a doctor's `checks[]` into a group's HealthChecks. A nil report (the
    /// CLI call failed — e.g. an old binary without the command) becomes one
    /// failing row rather than silently vanishing.
    static func checks(group: String, from doctorChecks: [DoctorCheckInfo]?) -> [HealthCheck] {
        guard let doctorChecks else {
            return [HealthCheck(group: group, name: group, state: .fail,
                                detail: "couldn't run — update the 2nb the app uses", fix: nil)]
        }
        return doctorChecks.map { c in
            HealthCheck(group: group, name: c.name, state: state(ok: c.ok, warn: c.warn ?? false),
                        detail: c.detail, fix: c.fix)
        }
    }

    /// Map a `models test` probe into a single AI HealthCheck.
    static func modelCheck(_ probe: AIProbeResult?, label: String) -> HealthCheck {
        guard let probe else {
            return HealthCheck(group: "AI", name: "\(label) model", state: .fail,
                               detail: "model test did not run", fix: "check 2nb ai status")
        }
        return HealthCheck(group: "AI", name: "\(label): \(probe.modelID)",
                           state: probe.ok ? .pass : .fail,
                           detail: probe.detail ?? "latency \(probe.latency)",
                           fix: probe.ok ? nil : "verify provider credentials (2nb ai status)")
    }

    /// The cross-dependency callout: Claude Code integration needs BOTH the skill
    /// and the MCP server. Returns a message when exactly one is set up.
    static func crossDependency(skillInstalled: Bool, mcpConfigured: Bool) -> String? {
        switch (skillInstalled, mcpConfigured) {
        case (true, false):
            return "The skill is installed but the MCP server isn't configured — Claude Code needs both. Configure it below."
        case (false, true):
            return "The MCP server is configured but the skill isn't installed — install it above so Claude Code knows when to use 2nb."
        default:
            return nil
        }
    }

    /// Overall worst state across a set of checks (for a summary glyph).
    static func overall(_ checks: [HealthCheck]) -> HealthState {
        if checks.contains(where: { $0.state == .fail }) { return .fail }
        if checks.contains(where: { $0.state == .running }) { return .running }
        if checks.contains(where: { $0.state == .warn }) { return .warn }
        return .pass
    }
}
