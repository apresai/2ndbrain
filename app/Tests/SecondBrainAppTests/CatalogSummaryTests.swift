import Foundation
import Testing
@testable import SecondBrain

/// Decode a CatalogModelInfo from the same JSON `2nb models list --json` emits,
/// so the summary derivation is exercised through the real decode path.
private func decodeModel(_ json: String) -> CatalogModelInfo {
    try! JSONDecoder().decode(CatalogModelInfo.self, from: Data(json.utf8))
}

private func decodePolicy(provider: String, scope: String, vendors: [String]) -> VendorPolicyResult {
    let csv = vendors.map { "\"\($0)\"" }.joined(separator: ",")
    let json = """
    {"provider":"\(provider)","mode":"enable_only","vendors":[\(csv)],"scope":"\(scope)","dry_run":false,
     "effect":{"enabled":0,"disabled":0,"overridden":0,"by_vendor":{}},"warnings":[],"cleared_model_overrides":[]}
    """
    return try! JSONDecoder().decode(VendorPolicyResult.self, from: Data(json.utf8))
}

// A mixed-vendor bedrock catalog: a validated Anthropic model, an
// access-denied Anthropic model, a disabled Amazon model, and an untested
// DeepSeek model.
private let haiku = decodeModel(#"{"id":"us.anthropic.claude-haiku-4-5-20251001-v1:0","name":"Haiku","provider":"bedrock","type":"generation","vendor":"anthropic","vendor_display":"Anthropic","tier":"verified","tested_at":"2026-07-01T00:00:00Z"}"#)
private let opus = decodeModel(#"{"id":"us.anthropic.claude-opus-4-8","name":"Opus","provider":"bedrock","type":"generation","vendor":"anthropic","vendor_display":"Anthropic","tier":"verified","tested_at":"2026-07-01T00:00:00Z","test_error":"403","test_error_code":"access_denied"}"#)
private let nova = decodeModel(#"{"id":"amazon.nova-2-multimodal-embeddings-v1:0","name":"Nova 2","provider":"bedrock","type":"embedding","vendor":"amazon","vendor_display":"Amazon","tier":"verified","enabled":false}"#)
private let deepseek = decodeModel(#"{"id":"us.deepseek.r1-v1:0","name":"DeepSeek R1","provider":"bedrock","type":"generation","vendor":"deepseek","vendor_display":"DeepSeek","tier":"unverified"}"#)

// MARK: - summarize

@Test("summarize counts total, validated, no-access, enabled, and allDisabled")
func summarizeCounts() {
    // An Anthropic group: two models, one validated, one access-denied, both
    // enabled (nil enabled is the tri-state default).
    let anthropic = CatalogSummary.summarize([haiku, opus], policyMember: true)
    #expect(anthropic.total == 2)
    #expect(anthropic.verified == 1)
    #expect(anthropic.noAccess == 1)
    #expect(anthropic.enabledCount == 2)
    #expect(anthropic.allDisabled == false)
    #expect(anthropic.policyMember == true)

    // A fully-disabled single-model group.
    let amazon = CatalogSummary.summarize([nova], policyMember: false)
    #expect(amazon.total == 1)
    #expect(amazon.verified == 0)
    #expect(amazon.enabledCount == 0)
    #expect(amazon.allDisabled == true)
    #expect(amazon.policyMember == false)

    // An empty group is never "all disabled".
    let empty = CatalogSummary.summarize([], policyMember: false)
    #expect(empty.total == 0)
    #expect(empty.allDisabled == false)
}

// MARK: - vendorBadge

@Test("vendorBadge renders validated / no-access counts, or nil when nothing to show")
func vendorBadgeText() {
    #expect(CatalogSummary.vendorBadge(CatalogSummary.summarize([haiku, opus], policyMember: false)) == "1 validated, 1 no access")
    #expect(CatalogSummary.vendorBadge(CatalogSummary.summarize([haiku], policyMember: false)) == "1 validated")
    #expect(CatalogSummary.vendorBadge(CatalogSummary.summarize([opus], policyMember: false)) == "1 no access")
    // Never tested and no access-denied: nothing worth annotating.
    #expect(CatalogSummary.vendorBadge(CatalogSummary.summarize([deepseek], policyMember: false)) == nil)
}

// MARK: - enabledBadge

@Test("enabledBadge marks fully-disabled and partially-enabled groups, else nil")
func enabledBadgeText() {
    // All enabled (nil default) -> nil.
    #expect(CatalogSummary.enabledBadge(CatalogSummary.summarize([haiku, opus], policyMember: false)) == nil)
    // Fully disabled.
    #expect(CatalogSummary.enabledBadge(CatalogSummary.summarize([nova], policyMember: false)) == "disabled")
    // Partial: one enabled, one disabled.
    #expect(CatalogSummary.enabledBadge(CatalogSummary.summarize([haiku, nova], policyMember: false)) == "1 of 2 enabled")
    // Empty group -> nil.
    #expect(CatalogSummary.enabledBadge(CatalogSummary.summarize([], policyMember: false)) == nil)
}

// MARK: - policyChip

@Test("policyChip maps slugs to display names and counts policy models + validated")
func policyChipText() {
    let models = [haiku, opus, nova, deepseek]
    let policies = [decodePolicy(provider: "bedrock", scope: "vault", vendors: ["anthropic", "deepseek"])]
    // 3 policy models (2 Anthropic + 1 DeepSeek), 1 validated (Haiku).
    #expect(CatalogSummary.policyChip(policies: policies, models: models, provider: "bedrock")
        == "Vendors: Anthropic, DeepSeek (3 models, 1 validated)")

    // Singular noun and a zero validated count.
    let onlyDeepseek = [decodePolicy(provider: "bedrock", scope: "vault", vendors: ["deepseek"])]
    #expect(CatalogSummary.policyChip(policies: onlyDeepseek, models: models, provider: "bedrock")
        == "Vendors: DeepSeek (1 model, 0 validated)")
}

@Test("policyChip is nil with no policy or no active provider")
func policyChipNilCases() {
    let models = [haiku, opus]
    #expect(CatalogSummary.policyChip(policies: [], models: models, provider: "bedrock") == nil)
    // Policy exists but for a different provider than the active one.
    let ollamaPolicy = [decodePolicy(provider: "ollama", scope: "vault", vendors: ["google"])]
    #expect(CatalogSummary.policyChip(policies: ollamaPolicy, models: models, provider: "bedrock") == nil)
    // No active provider at all.
    #expect(CatalogSummary.policyChip(policies: ollamaPolicy, models: models, provider: nil) == nil)
}

@Test("policyChip falls back to the slug for a future-intent vendor with no catalog model")
func policyChipFutureVendor() {
    let models = [haiku, opus]
    // Policy names mistral, which has no model in the catalog yet.
    let policies = [decodePolicy(provider: "bedrock", scope: "vault", vendors: ["anthropic", "mistral"])]
    // "mistral" keeps its raw slug; only the 2 present Anthropic models count.
    #expect(CatalogSummary.policyChip(policies: policies, models: models, provider: "bedrock")
        == "Vendors: Anthropic, mistral (2 models, 1 validated)")
}

// MARK: - activePolicy

@Test("activePolicy prefers the vault scope when a provider has both scopes")
func activePolicyScopePreference() {
    let policies = [
        decodePolicy(provider: "bedrock", scope: "global", vendors: ["amazon"]),
        decodePolicy(provider: "bedrock", scope: "vault", vendors: ["anthropic"]),
    ]
    #expect(CatalogSummary.activePolicy(policies, provider: "bedrock")?.scope == "vault")
    #expect(CatalogSummary.activePolicy(policies, provider: "openrouter") == nil)
}

// MARK: - displayName

@Test("displayName resolves a slug from the catalog, else returns the slug")
func displayNameLookup() {
    let models = [haiku, nova]
    #expect(CatalogSummary.displayName(for: "anthropic", in: models) == "Anthropic")
    #expect(CatalogSummary.displayName(for: "amazon", in: models) == "Amazon")
    #expect(CatalogSummary.displayName(for: "mistral", in: models) == "mistral")
}

// MARK: - defaultCollapsed (summary-first)

@Test("defaultCollapsed collapses every group by default; the user override wins")
func defaultCollapsedSummaryFirst() {
    // No override: summary-first collapses regardless of the disabled state.
    #expect(CatalogSummary.defaultCollapsed(userOverride: nil, allDisabled: false) == true)
    #expect(CatalogSummary.defaultCollapsed(userOverride: nil, allDisabled: true) == true)
    // The user's explicit chevron toggle wins in both directions.
    #expect(CatalogSummary.defaultCollapsed(userOverride: false, allDisabled: true) == false)
    #expect(CatalogSummary.defaultCollapsed(userOverride: true, allDisabled: false) == true)
}

@Test("groupCollapsed summaryFirst param is additive: default false keeps A4 behavior")
func groupCollapsedSummaryFirstParam() {
    // Default (summaryFirst omitted) keeps the A4 rule: expand unless disabled.
    #expect(CatalogVisibility.groupCollapsed(userOverride: nil, allDisabled: false) == false)
    // Opting into summary-first collapses the same group.
    #expect(CatalogVisibility.groupCollapsed(userOverride: nil, allDisabled: false, summaryFirst: true) == true)
}
