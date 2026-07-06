import Foundation

/// A supported AI client the dashboard can configure (skill + MCP server) in one
/// click. Pure data — no SwiftUI — so the "AI Clients" card just iterates these
/// and the per-row label/confirm logic (`ClientConfig`) is unit-tested directly.
///
/// `mcpClientKey` is the `--client` value passed to `2nb setup`/`mcp configured`
/// (claude-code, warp, claude-desktop, codex). `skillSlug` is the `2nb skills`
/// slug, or nil when a client has no skill of its own (Claude Desktop shares
/// Claude Code's `~/.claude/skills`, so only its MCP is configured).
struct ClientDescriptor: Identifiable, Equatable, Sendable {
    let id: String
    let displayName: String
    let systemImage: String
    let mcpClientKey: String
    let skillSlug: String?
    let note: String?
    let requiresRestart: Bool
    let usesAbsoluteCLIPath: Bool

    static let claudeCode = ClientDescriptor(
        id: "claude-code",
        displayName: "Claude Code",
        systemImage: "terminal",
        mcpClientKey: "claude-code",
        skillSlug: "claude-code",
        note: nil,
        requiresRestart: false,
        usesAbsoluteCLIPath: false
    )

    static let warp = ClientDescriptor(
        id: "warp",
        displayName: "Warp",
        systemImage: "command",
        mcpClientKey: "warp",
        skillSlug: "warp",
        note: nil,
        requiresRestart: false,
        usesAbsoluteCLIPath: false
    )

    static let claudeDesktop = ClientDescriptor(
        id: "claude-desktop",
        displayName: "Claude Desktop",
        systemImage: "menubar.dock.rectangle",
        mcpClientKey: "claude-desktop",
        skillSlug: nil,
        note: "Claude Desktop shares Claude Code's skills folder, so only its MCP server is configured.",
        requiresRestart: true,
        usesAbsoluteCLIPath: true
    )

    static let codex = ClientDescriptor(
        id: "codex",
        displayName: "Codex",
        systemImage: "chevron.left.forwardslash.chevron.right",
        mcpClientKey: "codex",
        skillSlug: "codex",
        note: "Codex's MCP server is wired via `codex mcp add` (the exact command is printed if the Codex CLI isn't installed).",
        requiresRestart: false,
        usesAbsoluteCLIPath: false
    )

    /// The clients the AI Clients card lists, in display order. Claude Code first
    /// (the primary integration, with the Verify panel under it), then Warp,
    /// Claude Desktop, and Codex.
    static let all: [ClientDescriptor] = [claudeCode, warp, claudeDesktop, codex]
}

/// Pure presentation logic for the per-client rows of the Home "AI Clients"
/// card, extracted (like `HomeSkill`/`HomeMCPConfigured`) so the label/confirm
/// mapping is unit-testable without any SwiftUI rendering. Labels are
/// single-sourced through the existing `HomeSkill.rowState`/
/// `HomeMCPConfigured.rowState` mappers so a Claude Code row reads identically to
/// the per-client rows.
enum ClientConfig {
    /// The skill row's label and whether the skill is installed (green) for this
    /// client. `ok` is true only when the skill is actually present at user or
    /// project scope; an "unknown" (nil status) reads as not-ok.
    static func skillRow(_ status: SkillStatusInfo?) -> (label: String, ok: Bool) {
        let label = HomeSkill.rowState(status).label
        let ok = (status?.userInstalled ?? false) || (status?.projectInstalled ?? false)
        return (label, ok)
    }

    /// The MCP row's label and whether the server is configured (green) for this
    /// client. `ok` mirrors `configured`; an "unknown" (nil status) reads as
    /// not-ok.
    static func mcpRow(_ status: MCPConfiguredInfo?) -> (label: String, ok: Bool) {
        let label = HomeMCPConfigured.rowState(status).label
        let ok = status?.configured ?? false
        return (label, ok)
    }

    /// The global-instructions row's label and whether the always-loaded 2nb
    /// block is present and current (green) in the client's memory file. A
    /// hand-edited or out-of-date block reads as installed-but-not-green; a nil
    /// status (client has no memory file, or not refreshed) is not-ok.
    static func globalInstructionsRow(_ status: GlobalInstructionsInfo?) -> (label: String, ok: Bool) {
        guard let status, status.installed else {
            return ("not installed", false)
        }
        if status.modified {
            return ("installed (hand-edited)", false)
        }
        if !status.upToDate {
            return ("installed (out of date)", false)
        }
        return ("installed", true)
    }

    /// The confirmation copy shown before `2nb setup --client <key>` runs (it
    /// edits an external config; a backup is saved). Claude Desktop's variant
    /// calls out that it writes an absolute path to `2nb` and that you must quit
    /// and reopen the app; each client's `note` (e.g. Codex's `codex mcp add`) is
    /// appended.
    static func configureConfirm(_ client: ClientDescriptor) -> (title: String, info: String) {
        let title = "Configure \(client.displayName)?"
        var info: String
        if client.skillSlug != nil {
            info = "This installs the 2ndbrain skill and configures the MCP server for \(client.displayName) for this vault."
        } else {
            info = "This configures the 2ndbrain MCP server for \(client.displayName) for this vault."
        }
        info += " A backup of any file it changes is saved first, and your other settings are preserved."
        if client.usesAbsoluteCLIPath {
            info += " It writes an absolute path to the 2nb binary so \(client.displayName) can launch it. Quit and reopen \(client.displayName) afterward for the change to take effect."
        }
        if let note = client.note, !note.isEmpty {
            info += " " + note
        }
        return (title, info)
    }

    /// The success message after a zero-exit `2nb setup --client <key>`. Mentions
    /// the skill only for clients that have one, and adds the quit-and-reopen
    /// nudge for clients that need a restart (Claude Desktop).
    static func successMessage(_ client: ClientDescriptor) -> String {
        var msg = "Configured \(client.displayName)."
        if client.skillSlug != nil {
            msg += " The skill and MCP server are set up for this vault."
        } else {
            msg += " The MCP server is set up for this vault."
        }
        if client.requiresRestart {
            msg += " Quit and reopen \(client.displayName) to pick it up."
        }
        return msg
    }

    /// The outcome of a `2nb setup --client <key>` run for one client, used to
    /// pick the message and its styling. `2nb setup` always exits 0, so this is
    /// derived from the returned `SetupClientResult`, never the process exit code.
    enum ConfigureOutcome: Equatable {
        /// Skill + MCP genuinely wired up.
        case success(String)
        /// No hard error, but a manual step is still required (e.g. Codex with no
        /// `codex` CLI returns `instructions` + `configured` false). Not a
        /// failure, but not a plain success.
        case manual(String)
        /// A step failed (`error` set) — must not read as success.
        case failure(String)
    }

    /// Map this client's `SetupClientResult` (or its absence) to a
    /// `ConfigureOutcome`. Precedence: a non-empty `error` is a failure; else a
    /// non-empty `instructions` is a manual step; else success. A nil result
    /// (the client's entry is missing from the array) is a failure, so a missing
    /// entry can never be misreported as configured.
    static func configureOutcome(_ client: ClientDescriptor, result: SetupClientResult?) -> ConfigureOutcome {
        guard let result else {
            return .failure("Setup returned no result for \(client.displayName).")
        }
        if let err = result.error, !err.isEmpty {
            return .failure("Configuring \(client.displayName) failed: \(err)")
        }
        if let instructions = result.instructions, !instructions.isEmpty {
            return .manual("\(client.displayName) needs a manual step:\n\(instructions)")
        }
        return .success(successMessage(client))
    }
}
