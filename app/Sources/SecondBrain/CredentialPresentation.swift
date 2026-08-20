import Foundation

/// Decides whether persisted model-access verdicts predate the CURRENT API
/// key. Both inputs are RFC3339 strings from the CLI
/// (`model_access.last_verified_at`, `config bedrock`'s `token_updated_at`).
/// Nil, empty, or unparseable on either side means NOT stale — an older CLI
/// without the stamp must degrade to "no banner", never to a false alarm.
enum StaleVerdicts {
    static func isStale(lastVerifiedAt: String?, tokenUpdatedAt: String?) -> Bool {
        guard let verified = parse(lastVerifiedAt), let updated = parse(tokenUpdatedAt) else {
            return false
        }
        return updated > verified
    }

    private static func parse(_ s: String?) -> Date? {
        guard let s, !s.isEmpty else { return nil }
        return ISO8601DateFormatter().date(from: s)
    }

    static let bannerText = "These results predate your current API key. Validate to refresh them."
}

/// The Models-tab header chip summarizing key state, driven by
/// `2nb config bedrock --json` (vault-free, so it renders even before a vault
/// binds). Pure and unit-tested.
enum CredentialChipPresentation {
    struct Chip: Equatable {
        let label: String
        /// True renders the chip in a warning tone (no key configured).
        let warning: Bool
    }

    static func chip(_ status: BedrockMachineStatus?) -> Chip {
        guard let status else {
            return Chip(label: "API key…", warning: false)
        }
        guard status.tokenSet else {
            return Chip(label: "API key · not set", warning: true)
        }
        let identity = status.tokenSuffix.isEmpty ? "set" : "····\(status.tokenSuffix)"
        return Chip(label: "API key \(identity) (\(status.tokenSource))", warning: false)
    }
}

/// Toggle math for the "Also verify in" region include list. The primary
/// region is always included by the CLI and never appears in this list; this
/// type manages only the ADDITIONAL regions stored in bedrock.json.
enum RegionIncludeSelection {
    /// The selectable choices: the common regions minus the primary, keeping
    /// any already-stored region (including a custom one) selectable so an
    /// unknown stored value is never silently undisplayable.
    static func choices(common: [String], primary: String, stored: [String]) -> [String] {
        var out: [String] = []
        for r in common + stored where r != primary && !out.contains(r) {
            out.append(r)
        }
        return out
    }

    static func toggling(_ selected: [String], region: String) -> [String] {
        if selected.contains(region) {
            return selected.filter { $0 != region }
        }
        return selected + [region]
    }

    /// Caption under the picker: honest about scope and cost.
    static func summary(primary: String, included: [String]) -> String {
        guard !included.isEmpty else {
            return "Verification probes only \(primary)."
        }
        return "Verification probes \(primary) first, then \(included.joined(separator: ", ")) for models that fail there. Machine-wide; more regions can add wall time to Validate."
    }

    /// The Validate-button caption suffix on the Models tab.
    static func validateSuffix(regionCount: Int) -> String {
        regionCount > 1 ? " across \(regionCount) regions" : ""
    }
}
