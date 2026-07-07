import Foundation

/// Pure derivations for the AI Hub's summary-first Catalog. The catalog opens
/// on compact per-vendor summary rows (vendor, model count, validated /
/// no-access counts, enabled state) that drill IN to the existing per-model
/// rows, instead of rendering every model row up front. A policy chip surfaces
/// the declared enable-only vendor intent at a glance. Split from the SwiftUI
/// view so the counting and text are unit-testable without AppKit or a live CLI.
enum CatalogSummary {

    /// A per-vendor rollup for a collapsed summary row: how many models the
    /// vendor has, how many are validated (a probe passed) vs classified as
    /// access-denied, how many are enabled, and whether the vendor is named by
    /// the active enable-only policy.
    struct VendorSummary: Equatable {
        let total: Int
        let verified: Int
        let noAccess: Int
        let enabledCount: Int
        let policyMember: Bool

        /// True when the group has models but every one is disabled.
        var allDisabled: Bool { total > 0 && enabledCount == 0 }
    }

    /// Rolls a vendor group's models into a `VendorSummary`. `policyMember` says
    /// whether this vendor is in the active provider's enable-only policy.
    static func summarize(_ models: [CatalogModelInfo], policyMember: Bool) -> VendorSummary {
        VendorSummary(
            total: models.count,
            verified: models.filter { validated($0) }.count,
            noAccess: models.filter { $0.testErrorCode == "access_denied" }.count,
            enabledCount: models.filter { $0.enabled != false }.count,
            policyMember: policyMember
        )
    }

    /// A model counts as validated when a probe was recorded and it did not
    /// error (the same rule VendorPolicyBuilder uses for its vendor rows).
    static func validated(_ m: CatalogModelInfo) -> Bool {
        (m.testedAt?.isEmpty == false) && (m.testError ?? "").isEmpty
    }

    /// The compact caption for a collapsed vendor summary row, e.g. "3
    /// validated, 1 no access". Returns nil when there is nothing worth
    /// annotating (no validation results yet), so the row stays clean.
    static func vendorBadge(_ s: VendorSummary) -> String? {
        var parts: [String] = []
        if s.verified > 0 { parts.append("\(s.verified) validated") }
        if s.noAccess > 0 { parts.append("\(s.noAccess) no access") }
        return parts.isEmpty ? nil : parts.joined(separator: ", ")
    }

    /// The enabled-state caption for a vendor summary row: "disabled" when the
    /// whole group is off, "N of M enabled" when only some are, else nil (all
    /// enabled is the common case and needs no annotation).
    static func enabledBadge(_ s: VendorSummary) -> String? {
        if s.total == 0 { return nil }
        if s.enabledCount == 0 { return "disabled" }
        if s.enabledCount < s.total { return "\(s.enabledCount) of \(s.total) enabled" }
        return nil
    }

    // MARK: - Policy chip

    /// One-line summary of the active provider's enable-only vendor policy for
    /// the Catalog chip, e.g. "Vendors: Anthropic, DeepSeek (14 models, 9
    /// validated)". Returns nil when the provider has no policy, so the chip
    /// only shows when the user has declared an intent. `models` is the loaded
    /// catalog, used to map vendor slugs to display names and count models.
    static func policyChip(policies: [VendorPolicyResult], models: [CatalogModelInfo], provider: String?) -> String? {
        guard let provider,
              let policy = activePolicy(policies, provider: provider),
              !policy.vendors.isEmpty else { return nil }
        let providerModels = models.filter { $0.provider == provider }
        let displays = policy.vendors.map { displayName(for: $0, in: providerModels) }
        let policyModels = providerModels.filter { policy.vendors.contains($0.vendor ?? "other") }
        let validatedCount = policyModels.filter { validated($0) }.count
        let noun = policyModels.count == 1 ? "model" : "models"
        return "Vendors: \(displays.joined(separator: ", ")) (\(policyModels.count) \(noun), \(validatedCount) validated)"
    }

    /// The enable-only policy that applies to a provider, preferring the vault
    /// scope when both a vault and a global policy exist (mirrors the Hub's
    /// `policyFor`).
    static func activePolicy(_ policies: [VendorPolicyResult], provider: String) -> VendorPolicyResult? {
        let matching = policies.filter { $0.provider == provider && $0.mode == "enable_only" }
        return matching.first { $0.scope == "vault" } ?? matching.first
    }

    /// Vendor display name for a slug from the loaded models, falling back to
    /// the slug itself when no catalog model carries it yet (a future-intent
    /// vendor named by the policy but not yet discovered).
    static func displayName(for slug: String, in models: [CatalogModelInfo]) -> String {
        models.first { ($0.vendor ?? "other") == slug }?.vendorDisplay ?? slug
    }

    /// Summary-first collapse default: every group starts collapsed so the
    /// catalog opens on compact summary rows, with the user's explicit chevron
    /// toggle winning. A thin, intent-named wrapper over CatalogVisibility.
    static func defaultCollapsed(userOverride: Bool?, allDisabled: Bool) -> Bool {
        CatalogVisibility.groupCollapsed(userOverride: userOverride, allDisabled: allDisabled, summaryFirst: true)
    }
}
