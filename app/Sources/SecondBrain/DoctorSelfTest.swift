import Foundation

/// Swift side of `2nb doctor --json`: the self-test payload, plus the
/// translation from the CLI's classified codes into a sentence a person can act
/// on.
///
/// The CLI already does the hard part — it distinguishes "your key was
/// rejected" from "your key is fine, this account cannot reach that model", a
/// distinction that only a real invoke can make. This layer's only job is to
/// not throw that away by collapsing both into "AI failed".

/// One check line. Mirrors the Go `DoctorCheck`, shared with config/mcp doctor.
struct SelfTestCheck: Codable, Identifiable, Equatable {
    var id: String { name }
    let name: String
    let ok: Bool
    let warn: Bool?
    let detail: String
    let fix: String?

    var isWarning: Bool { ok && (warn ?? false) }
}

/// The `selftest` block of `2nb doctor --json`.
struct SelfTestReport: Codable, Equatable {
    let ok: Bool
    let vaultBound: Bool
    let vaultPath: String?
    let provider: String
    let credentials: String
    let checks: [SelfTestCheck]

    enum CodingKeys: String, CodingKey {
        case ok
        case vaultBound = "vault_bound"
        case vaultPath = "vault_path"
        case provider
        case credentials
        case checks
    }
}

/// The full `2nb doctor --json` payload. SuiteStatus's fields stay at the top
/// level (the Go struct embeds it), so this decodes them alongside.
struct DoctorReport: Codable {
    let ok: Bool
    let selftest: SelfTestReport?
}

/// Credential verdict values, matching the CLI's vocabulary exactly.
enum CredentialVerdict: String {
    case accepted
    case rejected
    case unreachable
    case unknown

    init(_ raw: String) {
        self = CredentialVerdict(rawValue: raw) ?? .unknown
    }
}

enum SelfTestPresentation {
    /// The one-line headline above the check list.
    ///
    /// Phrased around the key, because that is the question a user opening this
    /// page is actually asking. "Rejected" and "works but not entitled" get
    /// different sentences on purpose: sending someone to replace a working key
    /// is the most expensive wrong answer this screen can give.
    static func headline(_ report: SelfTestReport) -> String {
        switch CredentialVerdict(report.credentials) {
        case .accepted:
            return report.ok
                ? "Everything works."
                : "Your API key works, but something else is wrong — see below."
        case .rejected:
            return "Your API key was rejected by \(report.provider)."
        case .unreachable:
            return "Could not reach \(report.provider) to check. Network?"
        case .unknown:
            return "Could not confirm your API key — no model answered."
        }
    }

    /// Whether the headline should read as a failure.
    static func isFailure(_ report: SelfTestReport) -> Bool { !report.ok }

    /// SF Symbol for a check row.
    static func symbol(for check: SelfTestCheck) -> String {
        if !check.ok { return "xmark.circle.fill" }
        if check.isWarning { return "exclamationmark.triangle.fill" }
        return "checkmark.circle.fill"
    }

    /// Checks worth surfacing when collapsed: anything not plainly passing.
    static func problems(_ report: SelfTestReport) -> [SelfTestCheck] {
        report.checks.filter { !$0.ok || $0.isWarning }
    }
}
