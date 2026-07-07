import Foundation
import Testing
@testable import SecondBrain

/// Decode a CatalogModelInfo from the same JSON `2nb models list --json` emits,
/// so vendor grouping is exercised through the real decode path.
private func decodeModel(_ json: String) -> CatalogModelInfo {
    try! JSONDecoder().decode(CatalogModelInfo.self, from: Data(json.utf8))
}

// A small mixed-vendor bedrock catalog plus one ollama model, enough to test
// grouping, counts, the access summary, and provider ordering.
private let anthropicHaiku = decodeModel(#"{"id":"us.anthropic.claude-haiku-4-5-20251001-v1:0","name":"Haiku","provider":"bedrock","type":"generation","vendor":"anthropic","vendor_display":"Anthropic","tier":"verified","tested_at":"2026-07-01T00:00:00Z"}"#)
private let anthropicOpus = decodeModel(#"{"id":"us.anthropic.claude-opus-4-8","name":"Opus","provider":"bedrock","type":"generation","vendor":"anthropic","vendor_display":"Anthropic","tier":"verified","tested_at":"2026-07-01T00:00:00Z","test_error":"403","test_error_code":"access_denied"}"#)
private let amazonNova = decodeModel(#"{"id":"amazon.nova-2-multimodal-embeddings-v1:0","name":"Nova 2","provider":"bedrock","type":"embedding","vendor":"amazon","vendor_display":"Amazon","tier":"verified","enabled":false}"#)
private let deepseek = decodeModel(#"{"id":"us.deepseek.r1-v1:0","name":"DeepSeek R1","provider":"bedrock","type":"generation","vendor":"deepseek","vendor_display":"DeepSeek","tier":"unverified"}"#)
private let ollamaGemma = decodeModel(#"{"id":"gemma3:4b","name":"Gemma 3 4B","provider":"ollama","type":"generation","vendor":"google","vendor_display":"Google","tier":"verified"}"#)

// MARK: - VendorPolicyResult / effect decode

@Test("VendorPolicyResult decodes the models policy set contract, incl. effect + by_vendor")
func vendorPolicyResultDecodes() throws {
    let json = """
    {
      "provider":"bedrock",
      "mode":"enable_only",
      "vendors":["anthropic","deepseek"],
      "scope":"vault",
      "dry_run":false,
      "effect":{
        "enabled":14,
        "disabled":38,
        "overridden":2,
        "by_vendor":{
          "anthropic":{"models":5,"state":"enabled"},
          "amazon":{"models":9,"state":"disabled"}
        }
      },
      "warnings":["active generation model X stays enabled"],
      "cleared_model_overrides":["amazon.titan-embed-text-v2:0"]
    }
    """
    let res = try JSONDecoder().decode(VendorPolicyResult.self, from: Data(json.utf8))
    #expect(res.provider == "bedrock")
    #expect(res.mode == "enable_only")
    #expect(res.vendors == ["anthropic", "deepseek"])
    #expect(res.scope == "vault")
    #expect(res.dryRun == false)
    #expect(res.effect.enabled == 14)
    #expect(res.effect.disabled == 38)
    #expect(res.effect.overridden == 2)
    #expect(res.effect.byVendor["anthropic"] == VendorEffectEntry(models: 5, state: "enabled"))
    #expect(res.effect.byVendor["amazon"]?.state == "disabled")
    #expect(res.warnings.count == 1)
    #expect(res.clearedModelOverrides == ["amazon.titan-embed-text-v2:0"])
    // Two scopes for one provider stay distinct via the id.
    #expect(res.id == "bedrock|vault")
}

@Test("models policy show decodes as an array, and the empty case is an empty array")
func vendorPolicyShowDecodesArray() throws {
    let json = """
    [
      {"provider":"bedrock","mode":"enable_only","vendors":["anthropic"],"scope":"vault","dry_run":false,
       "effect":{"enabled":5,"disabled":10,"overridden":0,"by_vendor":{"anthropic":{"models":5,"state":"enabled"}}},
       "warnings":[],"cleared_model_overrides":[]}
    ]
    """
    let arr = try JSONDecoder().decode([VendorPolicyResult].self, from: Data(json.utf8))
    #expect(arr.count == 1)
    #expect(arr[0].vendors == ["anthropic"])

    let empty = try JSONDecoder().decode([VendorPolicyResult].self, from: Data("[]".utf8))
    #expect(empty.isEmpty)
}

// MARK: - VendorPolicyBuilder.providerSections / vendorRows

@Test("providerSections groups by provider then vendor with counts and access summary")
func providerSectionsGrouping() {
    let models = [anthropicHaiku, anthropicOpus, amazonNova, deepseek, ollamaGemma]
    let sections = VendorPolicyBuilder.providerSections(from: models)

    // Bedrock ordered before ollama.
    #expect(sections.map(\.provider) == ["bedrock", "ollama"])

    let bedrock = sections[0]
    // Vendors sorted by display name: Amazon, Anthropic, DeepSeek.
    #expect(bedrock.vendors.map(\.display) == ["Amazon", "Anthropic", "DeepSeek"])

    let anthropic = bedrock.vendors.first { $0.vendor == "anthropic" }!
    #expect(anthropic.modelCount == 2)
    // Haiku passed (tested, no error); Opus tested but failed with a code.
    #expect(anthropic.verified == 1)
    #expect(anthropic.noAccess == 1)

    let amazon = bedrock.vendors.first { $0.vendor == "amazon" }!
    #expect(amazon.modelCount == 1)
    #expect(amazon.verified == 0)   // never tested
    #expect(amazon.noAccess == 0)

    #expect(sections[1].vendors.map(\.vendor) == ["google"])
}

@Test("vendorRows falls back to 'other' / 'Other' when a model carries no vendor")
func vendorRowsFallback() {
    let noVendor = decodeModel(#"{"id":"x.y","name":"XY","provider":"bedrock","type":"generation"}"#)
    let rows = VendorPolicyBuilder.vendorRows([noVendor])
    #expect(rows.count == 1)
    #expect(rows[0].vendor == "other")
    #expect(rows[0].display == "Other")
    #expect(rows[0].modelCount == 1)
}

// MARK: - VendorPolicyBuilder.initialCheckedVendors

@Test("initialCheckedVendors uses the policy vendors when a policy exists, else all vendors")
func initialCheckedVendorsRules() throws {
    let sections = VendorPolicyBuilder.providerSections(from: [anthropicHaiku, amazonNova, deepseek])
    let bedrock = sections[0]

    // No policy: every vendor starts checked (nothing restricted).
    let allChecked = VendorPolicyBuilder.initialCheckedVendors(section: bedrock, policy: nil)
    #expect(allChecked == Set(["anthropic", "amazon", "deepseek"]))

    // Policy present: only its vendors start checked.
    let policy = try JSONDecoder().decode(VendorPolicyResult.self, from: Data(#"""
    {"provider":"bedrock","mode":"enable_only","vendors":["anthropic"],"scope":"vault","dry_run":false,
     "effect":{"enabled":1,"disabled":2,"overridden":0,"by_vendor":{}},"warnings":[],"cleared_model_overrides":[]}
    """#.utf8))
    let policyChecked = VendorPolicyBuilder.initialCheckedVendors(section: bedrock, policy: policy)
    #expect(policyChecked == Set(["anthropic"]))
}

// MARK: - VendorPolicyBuilder.vendorsToApply

@Test("vendorsToApply keeps future-intent policy vendors that have no catalog row")
func vendorsToApplyPreservesFutureVendors() throws {
    // Existing policy names deepseek, which has no model in the catalog yet.
    let policy = try JSONDecoder().decode(VendorPolicyResult.self, from: Data(#"""
    {"provider":"bedrock","mode":"enable_only","vendors":["anthropic","deepseek"],"scope":"vault","dry_run":false,
     "effect":{"enabled":0,"disabled":0,"overridden":0,"by_vendor":{}},"warnings":[],"cleared_model_overrides":[]}
    """#.utf8))
    // The user checks anthropic (deepseek isn't even shown, no row).
    let applied = VendorPolicyBuilder.vendorsToApply(
        checked: ["anthropic"],
        catalogVendors: ["anthropic", "amazon"],
        existingPolicy: policy
    )
    // deepseek survives because it is a future-intent vendor with no row.
    #expect(applied == ["anthropic", "deepseek"])

    // With no existing policy, only the checked vendors are applied.
    let fresh = VendorPolicyBuilder.vendorsToApply(
        checked: ["anthropic"], catalogVendors: ["anthropic", "amazon"], existingPolicy: nil)
    #expect(fresh == ["anthropic"])
}

// MARK: - CatalogVisibility.hideDisabled

@Test("hideDisabled removes explicitly-disabled models from the curated view and counts them")
func hideDisabledFilter() {
    let models = [anthropicHaiku, amazonNova]  // amazonNova is enabled:false
    let hidden = CatalogVisibility.hideDisabled(models, showDisabled: false)
    #expect(hidden.visible.map(\.modelID) == [anthropicHaiku.modelID])
    #expect(hidden.hiddenDisabled == 1)

    // Revealed: both visible, count still reports the disabled one.
    let shown = CatalogVisibility.hideDisabled(models, showDisabled: true)
    #expect(shown.visible.count == 2)
    #expect(shown.hiddenDisabled == 1)

    // A nil `enabled` is treated as enabled (tri-state default), never hidden.
    #expect(CatalogVisibility.hideDisabled([anthropicHaiku], showDisabled: false).hiddenDisabled == 0)
}

// MARK: - CatalogVisibility.groupCollapsed

@Test("groupCollapsed auto-collapses a fully-disabled group unless the user overrides it")
func groupCollapsedRule() {
    // No user override: disabled group collapses, enabled group expands.
    #expect(CatalogVisibility.groupCollapsed(userOverride: nil, allDisabled: true) == true)
    #expect(CatalogVisibility.groupCollapsed(userOverride: nil, allDisabled: false) == false)
    // A user override wins in both directions.
    #expect(CatalogVisibility.groupCollapsed(userOverride: false, allDisabled: true) == false)
    #expect(CatalogVisibility.groupCollapsed(userOverride: true, allDisabled: false) == true)
}

// MARK: - AppState argv construction (no subprocess)

@Test("vendorPolicy argv builders match the CLI contract")
func vendorPolicyArgvConstruction() {
    #expect(AppState.vendorPolicyShowArgs() == ["models", "policy", "show", "--json", "--porcelain"])

    #expect(AppState.vendorPolicySetArgs(provider: "bedrock", vendors: ["anthropic", "deepseek"], scope: "vault")
        == ["models", "policy", "set", "--provider", "bedrock",
            "--enable-only", "anthropic,deepseek", "--scope", "vault", "--json", "--porcelain"])

    // A single vendor produces no trailing comma.
    #expect(AppState.vendorPolicySetArgs(provider: "openrouter", vendors: ["anthropic"], scope: "global")
        == ["models", "policy", "set", "--provider", "openrouter",
            "--enable-only", "anthropic", "--scope", "global", "--json", "--porcelain"])

    #expect(AppState.vendorPolicyClearArgs(provider: "bedrock", scope: "vault")
        == ["models", "policy", "clear", "--provider", "bedrock", "--scope", "vault", "--json", "--porcelain"])
}
