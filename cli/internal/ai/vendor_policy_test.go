package ai

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mustSavePolicy(t *testing.T, scope UserCatalogScope, vaultRoot string, p VendorPolicy) {
	t.Helper()
	if err := SaveVendorPolicy(scope, vaultRoot, p); err != nil {
		t.Fatalf("save policy: %v", err)
	}
}

func findPolicy(policies []ScopedVendorPolicy, provider string) (ScopedVendorPolicy, bool) {
	for _, p := range policies {
		if p.Provider == provider {
			return p, true
		}
	}
	return ScopedVendorPolicy{}, false
}

func TestVendorPolicy_SaveLoadClear_RoundTrip(t *testing.T) {
	setupHome(t)
	vaultRoot := t.TempDir()

	mustSavePolicy(t, ScopeGlobal, "", VendorPolicy{
		Provider: "bedrock", Mode: VendorPolicyModeEnableOnly, Vendors: []string{"amazon"},
	})
	mustSavePolicy(t, ScopeGlobal, "", VendorPolicy{
		Provider: "openrouter", Mode: VendorPolicyModeEnableOnly, Vendors: []string{"nvidia"},
	})
	// Vault policy for bedrock fully overrides the global one.
	mustSavePolicy(t, ScopeVault, vaultRoot, VendorPolicy{
		Provider: "bedrock", Mode: VendorPolicyModeEnableOnly, Vendors: []string{"Anthropic", " deepseek ", "anthropic"},
	})

	policies := LoadVendorPolicies(vaultRoot)
	if len(policies) != 2 {
		t.Fatalf("expected 2 merged policies, got %d: %+v", len(policies), policies)
	}
	bedrock, ok := findPolicy(policies, "bedrock")
	if !ok {
		t.Fatal("bedrock policy missing")
	}
	if bedrock.Scope != ScopeVault {
		t.Errorf("bedrock provenance = %q, want vault (vault overrides global)", bedrock.Scope)
	}
	// Vendors were normalized: trimmed, lower-cased, deduped, sorted.
	if got := strings.Join(bedrock.Vendors, ","); got != "anthropic,deepseek" {
		t.Errorf("bedrock vendors = %q, want anthropic,deepseek", got)
	}
	or, ok := findPolicy(policies, "openrouter")
	if !ok || or.Scope != ScopeGlobal {
		t.Errorf("openrouter policy = %+v ok=%v, want global scope", or, ok)
	}

	// Clearing the vault entry re-exposes the global one.
	removed, err := ClearVendorPolicy(ScopeVault, vaultRoot, "bedrock")
	if err != nil || !removed {
		t.Fatalf("clear vault bedrock: removed=%v err=%v", removed, err)
	}
	bedrock, ok = findPolicy(LoadVendorPolicies(vaultRoot), "bedrock")
	if !ok || bedrock.Scope != ScopeGlobal || strings.Join(bedrock.Vendors, ",") != "amazon" {
		t.Fatalf("after vault clear expected global amazon policy, got %+v ok=%v", bedrock, ok)
	}

	// Clearing the global entry removes it entirely; a second clear is a no-op.
	if removed, err := ClearVendorPolicy(ScopeGlobal, "", "bedrock"); err != nil || !removed {
		t.Fatalf("clear global bedrock: removed=%v err=%v", removed, err)
	}
	if _, ok := findPolicy(LoadVendorPolicies(vaultRoot), "bedrock"); ok {
		t.Fatal("bedrock policy should be gone after clearing both scopes")
	}
	if removed, err := ClearVendorPolicy(ScopeGlobal, "", "bedrock"); err != nil || removed {
		t.Fatalf("second clear should be a no-op: removed=%v err=%v", removed, err)
	}
}

func TestVendorPolicy_SaveValidates(t *testing.T) {
	setupHome(t)
	cases := []VendorPolicy{
		{Provider: "", Mode: VendorPolicyModeEnableOnly, Vendors: []string{"anthropic"}},
		{Provider: "bedrock", Mode: "block_only", Vendors: []string{"anthropic"}},
		{Provider: "bedrock", Mode: VendorPolicyModeEnableOnly, Vendors: nil},
		{Provider: "bedrock", Mode: VendorPolicyModeEnableOnly, Vendors: []string{"  ", ""}},
	}
	for _, p := range cases {
		if err := SaveVendorPolicy(ScopeGlobal, "", p); err == nil {
			t.Errorf("expected validation error for %+v", p)
		}
	}
}

func TestVendorPolicy_CorruptFileIsQuarantined(t *testing.T) {
	setupHome(t)
	path := globalVendorPolicyPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{{{ not yaml"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := LoadVendorPolicies(""); len(got) != 0 {
		t.Fatalf("corrupt file should load as empty, got %+v", got)
	}
	if _, err := os.Stat(path + ".bak"); err != nil {
		t.Fatalf("corrupt file should be quarantined to .bak: %v", err)
	}
}

func TestApplyVendorPolicy_Precedence(t *testing.T) {
	policies := []ScopedVendorPolicy{{
		VendorPolicy: VendorPolicy{Provider: "bedrock", Mode: VendorPolicyModeEnableOnly, Vendors: []string{"anthropic"}},
		Scope:        ScopeVault,
	}}
	models := []ModelInfo{
		// Explicit per-model tri-states beat the policy in both directions.
		{ID: "us.anthropic.claude-haiku-4-5-20251001-v1:0", Provider: "bedrock", Type: "generation", Enabled: Ptr(false)},
		{ID: "amazon.nova-pro-v1:0", Provider: "bedrock", Type: "generation", Enabled: Ptr(true)},
		// Nil tri-states get the policy verdict.
		{ID: "us.anthropic.claude-sonnet-4-6", Provider: "bedrock", Type: "generation"},
		{ID: "amazon.titan-embed-text-v2:0", Provider: "bedrock", Type: "embedding"},
		// Other providers are untouched (nil stays nil = tier default).
		{ID: "nvidia/llama-nemotron-embed-vl-1b-v2:free", Provider: "openrouter", Type: "embedding"},
	}

	warnings := applyVendorPolicy(models, policies, nil)
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}

	if models[0].Enabled == nil || *models[0].Enabled {
		t.Error("explicit Enabled=false must survive an enable_only policy for its vendor")
	}
	if models[1].Enabled == nil || !*models[1].Enabled {
		t.Error("explicit Enabled=true must survive a policy that excludes its vendor")
	}
	if models[2].Enabled == nil || !*models[2].Enabled {
		t.Error("nil-Enabled anthropic model should flip to enabled")
	}
	if models[3].Enabled == nil || *models[3].Enabled {
		t.Error("nil-Enabled amazon model should flip to disabled")
	}
	if models[4].Enabled != nil {
		t.Error("model on a provider without a policy must keep its nil tri-state")
	}
}

func TestApplyVendorPolicy_ActiveGuard(t *testing.T) {
	cfg := DefaultAIConfig() // bedrock: haiku generation + nova embed, both amazon/anthropic
	policies := []ScopedVendorPolicy{{
		VendorPolicy: VendorPolicy{Provider: "bedrock", Mode: VendorPolicyModeEnableOnly, Vendors: []string{"cohere"}},
		Scope:        ScopeVault,
	}}
	models := []ModelInfo{
		{ID: cfg.GenerationModel, Provider: "bedrock", Type: "generation", Active: true},
		{ID: cfg.EmbeddingModel, Provider: "bedrock", Type: "embedding"}, // active per config even without the mark
		{ID: "amazon.nova-pro-v1:0", Provider: "bedrock", Type: "generation"},
	}

	warnings := applyVendorPolicy(models, policies, VendorPolicyActiveGuard(cfg))
	if models[0].Enabled == nil || !*models[0].Enabled {
		t.Error("active generation model must never be policy-disabled")
	}
	if models[1].Enabled == nil || !*models[1].Enabled {
		t.Error("active embedding model (matched via config) must never be policy-disabled")
	}
	if models[2].Enabled == nil || *models[2].Enabled {
		t.Error("non-active excluded-vendor model should be disabled")
	}
	if len(warnings) != 2 {
		t.Fatalf("expected 2 active-model warnings, got %d: %v", len(warnings), warnings)
	}
	for _, w := range warnings {
		if !strings.Contains(w, "stays enabled") {
			t.Errorf("warning should explain the model stays enabled: %q", w)
		}
	}
}

func TestApplyVendorPolicy_RerankGuard(t *testing.T) {
	cfg := DefaultAIConfig()
	cfg.Rerank.Enabled = true // active rerank slot resolves to the default Cohere model
	policies := []ScopedVendorPolicy{{
		VendorPolicy: VendorPolicy{Provider: "bedrock", Mode: VendorPolicyModeEnableOnly, Vendors: []string{"anthropic"}},
		Scope:        ScopeVault,
	}}
	models := []ModelInfo{
		{ID: cfg.ResolveRerankModel(), Provider: "bedrock", Type: "rerank"},
	}
	warnings := applyVendorPolicy(models, policies, VendorPolicyActiveGuard(cfg))
	if models[0].Enabled == nil || !*models[0].Enabled {
		t.Error("active rerank model must never be policy-disabled while rerank is enabled")
	}
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning, got %v", warnings)
	}
}

// TestApplyVendorPolicy_DiscoveredEntriesDisabled exercises the exact pass
// BuildModelList runs over the Unverified (discovery) slice: fresh entries
// from a non-policy vendor arrive pre-disabled, policy-vendor ones enabled.
// (Live discovery needs vendor credentials, so this feeds the same function
// the synthetic shape discovery produces: TierUnverified, nil Enabled.)
func TestApplyVendorPolicy_DiscoveredEntriesDisabled(t *testing.T) {
	policies := []ScopedVendorPolicy{{
		VendorPolicy: VendorPolicy{Provider: "bedrock", Mode: VendorPolicyModeEnableOnly, Vendors: []string{"anthropic", "deepseek"}},
		Scope:        ScopeVault,
	}}
	discovered := []ModelInfo{
		{ID: "meta.llama4-scout-17b-instruct-v1:0", Provider: "bedrock", Type: "generation", Tier: TierUnverified},
		{ID: "deepseek.v3-v1:0", Provider: "bedrock", Type: "generation", Tier: TierUnverified},
		{ID: "us.anthropic.claude-opus-4-7-v1", Provider: "bedrock", Type: "generation", Tier: TierUnverified},
	}
	if warnings := applyVendorPolicy(discovered, policies, nil); len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	if discovered[0].Enabled == nil || *discovered[0].Enabled {
		t.Error("discovered meta model should arrive pre-disabled")
	}
	if discovered[1].Enabled == nil || !*discovered[1].Enabled {
		t.Error("discovered deepseek model should arrive enabled (vendor is in the policy)")
	}
	if discovered[2].Enabled == nil || !*discovered[2].Enabled {
		t.Error("discovered anthropic model should arrive enabled")
	}
}

func TestApplyVendorPolicy_UnknownModeIgnoredWithWarning(t *testing.T) {
	policies := []ScopedVendorPolicy{{
		VendorPolicy: VendorPolicy{Provider: "bedrock", Mode: "future_mode", Vendors: []string{"anthropic"}},
		Scope:        ScopeVault,
	}}
	models := []ModelInfo{{ID: "amazon.nova-pro-v1:0", Provider: "bedrock", Type: "generation"}}
	warnings := applyVendorPolicy(models, policies, nil)
	if models[0].Enabled != nil {
		t.Error("unknown-mode policy must not touch any model")
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "unsupported mode") {
		t.Fatalf("expected one unsupported-mode warning, got %v", warnings)
	}
}

func TestBuildModelList_VendorPolicy_EndToEnd(t *testing.T) {
	setupHome(t)
	vaultRoot := t.TempDir()
	mustSavePolicy(t, ScopeVault, vaultRoot, VendorPolicy{
		Provider: "bedrock", Mode: VendorPolicyModeEnableOnly, Vendors: []string{"anthropic"},
	})

	cfg := DefaultAIConfig() // active: nova embed (amazon) + haiku (anthropic)
	result, err := BuildModelList(context.Background(), MergedListOptions{Config: cfg, VaultRoot: vaultRoot})
	if err != nil {
		t.Fatalf("BuildModelList: %v", err)
	}

	byID := map[string]ModelInfo{}
	for _, m := range result.Verified {
		if m.Provider == "bedrock" {
			byID[m.ID] = m
		}
	}
	if m := byID["amazon.titan-embed-text-v2:0"]; m.Enabled == nil || *m.Enabled {
		t.Errorf("titan (amazon) should be policy-disabled, got %v", m.Enabled)
	}
	if m := byID["us.anthropic.claude-sonnet-4-6"]; m.Enabled == nil || !*m.Enabled {
		t.Errorf("sonnet (anthropic) should be policy-enabled, got %v", m.Enabled)
	}
	// The active embedding model is amazon (excluded) but must stay enabled,
	// with a warning surfaced through the list result.
	if m := byID[cfg.EmbeddingModel]; m.Enabled == nil || !*m.Enabled {
		t.Errorf("active embedding model must stay enabled, got %v", m.Enabled)
	}
	foundWarning := false
	for _, w := range result.Warnings {
		if strings.Contains(w, cfg.EmbeddingModel) && strings.Contains(w, "stays enabled") {
			foundWarning = true
		}
	}
	if !foundWarning {
		t.Errorf("expected an active-model warning naming %s, got %v", cfg.EmbeddingModel, result.Warnings)
	}

	// EnabledOnly (the GUI dropdown path) drops the policy-disabled models
	// but keeps policy-enabled and guarded-active ones.
	filtered, err := BuildModelList(context.Background(), MergedListOptions{Config: cfg, VaultRoot: vaultRoot, EnabledOnly: true})
	if err != nil {
		t.Fatalf("BuildModelList enabled-only: %v", err)
	}
	for _, m := range filtered.Verified {
		if m.Provider != "bedrock" {
			continue
		}
		if m.ID == "amazon.titan-embed-text-v2:0" {
			t.Error("policy-disabled titan leaked into the enabled-only list")
		}
	}
	seen := map[string]bool{}
	for _, m := range filtered.Verified {
		seen[m.ID] = true
	}
	if !seen["us.anthropic.claude-sonnet-4-6"] {
		t.Error("policy-enabled sonnet missing from the enabled-only list")
	}
	if !seen[cfg.EmbeddingModel] {
		t.Error("guarded active embedding model missing from the enabled-only list")
	}
}

func TestBuildModelList_VendorPolicy_ExplicitOverrideWins(t *testing.T) {
	setupHome(t)
	vaultRoot := t.TempDir()
	mustSavePolicy(t, ScopeVault, vaultRoot, VendorPolicy{
		Provider: "bedrock", Mode: VendorPolicyModeEnableOnly, Vendors: []string{"anthropic"},
	})
	// The user explicitly enabled an amazon model: that beats the policy.
	if err := SaveUserCatalogEntry(ScopeVault, vaultRoot, ModelInfo{
		ID: "amazon.titan-embed-text-v2:0", Provider: "bedrock", Tier: TierUserVerified, Enabled: Ptr(true),
	}); err != nil {
		t.Fatalf("save override: %v", err)
	}

	result, err := BuildModelList(context.Background(), MergedListOptions{Config: DefaultAIConfig(), VaultRoot: vaultRoot, EnabledOnly: true})
	if err != nil {
		t.Fatalf("BuildModelList: %v", err)
	}
	found := false
	for _, m := range result.Verified {
		if m.Provider == "bedrock" && m.ID == "amazon.titan-embed-text-v2:0" {
			found = true
		}
	}
	if !found {
		t.Error("explicitly enabled titan must survive an anthropic-only policy in the enabled-only list")
	}
}

func TestModelEnabledOverrides_And_Clear(t *testing.T) {
	setupHome(t)
	vaultRoot := t.TempDir()

	entries := []ModelInfo{
		{ID: "amazon.nova-pro-v1:0", Provider: "bedrock", Tier: TierUserVerified, Enabled: Ptr(false), TestedAt: "2026-07-01T00:00:00Z"},
		{ID: "us.anthropic.claude-sonnet-4-6", Provider: "bedrock", Tier: TierUserVerified, Enabled: Ptr(true)},
		{ID: "cohere.embed-english-v3", Provider: "bedrock", Tier: TierUserVerified}, // no tri-state
		{ID: "nvidia/llama-nemotron-embed-vl-1b-v2:free", Provider: "openrouter", Tier: TierUserVerified, Enabled: Ptr(false)},
	}
	for _, e := range entries {
		if err := SaveUserCatalogEntry(ScopeVault, vaultRoot, e); err != nil {
			t.Fatalf("save %s: %v", e.ID, err)
		}
	}

	overrides, err := ModelEnabledOverrides(ScopeVault, vaultRoot, "bedrock")
	if err != nil {
		t.Fatalf("overrides: %v", err)
	}
	if len(overrides) != 2 {
		t.Fatalf("expected 2 bedrock overrides, got %v", overrides)
	}
	if v, ok := overrides["amazon.nova-pro-v1:0"]; !ok || v {
		t.Errorf("nova-pro override = %v/%v, want false", v, ok)
	}

	cleared, err := ClearModelEnabledOverrides(ScopeVault, vaultRoot, "bedrock")
	if err != nil {
		t.Fatalf("clear: %v", err)
	}
	if strings.Join(cleared, ",") != "amazon.nova-pro-v1:0,us.anthropic.claude-sonnet-4-6" {
		t.Fatalf("cleared = %v", cleared)
	}

	after, err := ModelEnabledOverrides(ScopeVault, vaultRoot, "bedrock")
	if err != nil || len(after) != 0 {
		t.Fatalf("expected no bedrock overrides after clear, got %v (err=%v)", after, err)
	}
	// Entries survive with their other fields; only the tri-state is dropped.
	keptTested := false
	for _, m := range LoadUserCatalog(vaultRoot) {
		if m.ID == "amazon.nova-pro-v1:0" && m.TestedAt != "" {
			keptTested = true
		}
	}
	if !keptTested {
		t.Error("clearing overrides must keep the entry's other fields (tested_at)")
	}
	// The openrouter override was out of scope for the clear.
	orOverrides, _ := ModelEnabledOverrides(ScopeVault, vaultRoot, "openrouter")
	if len(orOverrides) != 1 {
		t.Errorf("openrouter override should be untouched, got %v", orOverrides)
	}
	// A second clear is a no-op.
	if cleared, err := ClearModelEnabledOverrides(ScopeVault, vaultRoot, "bedrock"); err != nil || cleared != nil {
		t.Fatalf("second clear should be a no-op: %v err=%v", cleared, err)
	}
}

func TestKnownVendorSlugs_UnionsCatalogAndStaticVocabulary(t *testing.T) {
	known := KnownVendorSlugs("bedrock", BuiltinCatalog())
	for _, want := range []string{"anthropic", "amazon", "cohere", "deepseek", "meta"} {
		if !known[want] {
			t.Errorf("expected %q in known bedrock vendor slugs", want)
		}
	}
	// Non-bedrock providers get only catalog-derived slugs.
	orKnown := KnownVendorSlugs("openrouter", BuiltinCatalog())
	if orKnown["deepseek"] {
		t.Error("openrouter should not inherit the static bedrock vocabulary")
	}
	if len(orKnown) == 0 {
		t.Error("openrouter should derive at least one slug from the builtin catalog")
	}
}
