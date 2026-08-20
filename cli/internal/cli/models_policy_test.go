package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// decodePolicyResult parses the `models policy set` JSON contract.
func decodePolicyResult(t *testing.T, body []byte) policyResult {
	t.Helper()
	var res policyResult
	if err := json.Unmarshal(body, &res); err != nil {
		t.Fatalf("policy result parse: %v (body=%s)", err, truncate(body, 500))
	}
	return res
}

func TestModelsPolicy_UnknownVendorSlugListsKnown(t *testing.T) {
	_, root := newContractVault(t)

	out, err := runCLIArgs(t, root,
		"models", "policy", "set",
		"--provider", "bedrock",
		"--enable-only", "anthropic,nosuchvendor",
		"--scope", "vault")
	if err == nil {
		t.Fatalf("expected unknown-slug error, got success (out=%s)", truncate(out, 300))
	}
	if ExitCode(err) != ExitValidation {
		t.Errorf("unknown slug should exit %d (validation), got %d", ExitValidation, ExitCode(err))
	}
	msg := err.Error()
	if !strings.Contains(msg, "nosuchvendor") || !strings.Contains(msg, "known vendors:") {
		t.Errorf("error should name the bad slug and list known vendors, got: %s", msg)
	}
	if !strings.Contains(msg, "anthropic") || !strings.Contains(msg, "deepseek") {
		t.Errorf("known-vendor list should include catalog and static vocabulary slugs, got: %s", msg)
	}
	// Nothing was written.
	if _, statErr := os.Stat(filepath.Join(root, ".2ndbrain", "models-policy.yaml")); !os.IsNotExist(statErr) {
		t.Errorf("failed validation must not write the policy file (stat err=%v)", statErr)
	}
}

// TestModelsPolicy_ShowReportsKnownVendors covers the checkbox vocabulary a
// vendor-policy UI renders from: known_vendors must carry every slug the
// policy MAY name, including vendors with no row in this account's catalog —
// a policy states intent about future models, so xAI has to be checkable on
// an account that has never seen a Grok model.
func TestModelsPolicy_ShowReportsKnownVendors(t *testing.T) {
	_, root := newContractVault(t)

	if _, err := runCLIArgs(t, root,
		"models", "policy", "set",
		"--provider", "bedrock", "--enable-only", "anthropic",
		"--scope", "vault"); err != nil {
		t.Fatalf("policy set: %v", err)
	}

	got, err := runCLIArgs(t, root, "models", "policy", "show", "--provider", "bedrock", "--json")
	if err != nil {
		t.Fatalf("policy show: %v (out=%s)", err, truncate(got, 300))
	}
	var results []policyResult
	if err := json.Unmarshal(got, &results); err != nil {
		t.Fatalf("policy show parse: %v (body=%s)", err, truncate(got, 500))
	}
	if len(results) != 1 {
		t.Fatalf("expected one bedrock policy, got %d", len(results))
	}
	res := results[0]

	known := map[string]bool{}
	for _, v := range res.KnownVendors {
		known[v] = true
	}
	for _, slug := range []string{"anthropic", "amazon", "xai", "openai", "deepseek"} {
		if !known[slug] {
			t.Errorf("known_vendors missing %q: %v", slug, res.KnownVendors)
		}
	}
	// deepseek is the "no catalog row" case: it is a nameable vendor with no
	// model in the merged catalog, which is exactly why the static vocabulary
	// is unioned in.
	if _, charted := res.Effect.ByVendor["deepseek"]; charted {
		t.Errorf("deepseek unexpectedly has catalog rows; this test needs a vendor with none: %v", res.Effect.ByVendor)
	}
	// Existing fields are untouched.
	if res.Provider != "bedrock" || res.Mode != "enable_only" {
		t.Errorf("show broke its existing contract: %+v", res)
	}
	if len(res.Vendors) != 1 || res.Vendors[0] != "anthropic" {
		t.Errorf("vendors = %v, want [anthropic]", res.Vendors)
	}

	// `set` reports the same vocabulary, so a UI can render checkboxes from
	// the response it already has instead of a second round-trip.
	got, err = runCLIArgs(t, root,
		"models", "policy", "set",
		"--provider", "bedrock", "--enable-only", "anthropic,xai",
		"--scope", "vault", "--dry-run", "--json")
	if err != nil {
		t.Fatalf("policy set --dry-run: %v (out=%s)", err, truncate(got, 300))
	}
	setRes := decodePolicyResult(t, got)
	if len(setRes.KnownVendors) != len(res.KnownVendors) {
		t.Errorf("set known_vendors (%d) disagrees with show (%d)", len(setRes.KnownVendors), len(res.KnownVendors))
	}
}

// TestModelsPolicy_ShowFillsVocabularyGapsPerProvider: a policy configured
// for ONE provider must not suppress the synthetic vocabulary rows for the
// others. Previously synthetics were emitted only when zero policies existed,
// so an openrouter policy silently shrank the bedrock vendor list to the
// GUI's hardcoded fallback.
func TestModelsPolicy_ShowFillsVocabularyGapsPerProvider(t *testing.T) {
	_, root := newContractVault(t)

	if _, err := runCLIArgs(t, root,
		"models", "policy", "set",
		"--provider", "openrouter", "--enable-only", "anthropic",
		"--scope", "vault", "--json"); err != nil {
		t.Fatalf("seed openrouter policy: %v", err)
	}

	got, err := runCLIArgs(t, root, "models", "policy", "show", "--json")
	if err != nil {
		t.Fatalf("policy show: %v (out=%s)", err, truncate(got, 300))
	}
	var results []policyResult
	if err := json.Unmarshal(got, &results); err != nil {
		t.Fatalf("parse: %v (body=%s)", err, truncate(got, 500))
	}
	byProvider := map[string]policyResult{}
	for _, r := range results {
		byProvider[r.Provider] = r
	}
	or, ok := byProvider["openrouter"]
	if !ok || or.Mode != "enable_only" || len(or.Vendors) != 1 {
		t.Fatalf("configured openrouter row wrong: %+v", or)
	}
	br, ok := byProvider["bedrock"]
	if !ok {
		t.Fatalf("bedrock synthetic row missing when another provider has a policy: %+v", results)
	}
	if br.Mode == "enable_only" || len(br.Vendors) > 0 {
		t.Errorf("bedrock gap row must not look configured: %+v", br)
	}
	if len(br.KnownVendors) < 10 {
		t.Errorf("bedrock known_vendors should be the full static vocabulary, got %d: %v", len(br.KnownVendors), br.KnownVendors)
	}

	// The --provider filter keeps working for the unconfigured provider.
	got, err = runCLIArgs(t, root, "models", "policy", "show", "--provider", "bedrock", "--json")
	if err != nil {
		t.Fatal(err)
	}
	results = nil
	if err := json.Unmarshal(got, &results); err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Provider != "bedrock" || len(results[0].KnownVendors) < 10 {
		t.Fatalf("filtered gap row wrong: %+v", results)
	}
}

// TestModelsPolicy_ShowWithoutPolicyReportsKnownVendors is the first-run
// contract: with nothing configured, --json must still name the Bedrock
// vendor vocabulary (DeepSeek, Meta, Mistral, …) so the GUI does not fall
// back to a four-slug hardcoded list.
func TestModelsPolicy_ShowWithoutPolicyReportsKnownVendors(t *testing.T) {
	_, root := newContractVault(t)

	got, err := runCLIArgs(t, root, "models", "policy", "show", "--provider", "bedrock", "--json")
	if err != nil {
		t.Fatalf("policy show: %v (out=%s)", err, truncate(got, 300))
	}
	var results []policyResult
	if err := json.Unmarshal(got, &results); err != nil {
		t.Fatalf("policy show parse: %v (body=%s)", err, truncate(got, 500))
	}
	if len(results) != 1 {
		t.Fatalf("expected one synthetic bedrock row, got %d: %+v", len(results), results)
	}
	res := results[0]
	if res.Provider != "bedrock" {
		t.Errorf("provider = %q, want bedrock", res.Provider)
	}
	if res.Mode == "enable_only" || len(res.Vendors) > 0 {
		t.Errorf("synthetic row must not look configured: mode=%q vendors=%v", res.Mode, res.Vendors)
	}
	known := map[string]bool{}
	for _, v := range res.KnownVendors {
		known[v] = true
	}
	for _, slug := range []string{"anthropic", "amazon", "xai", "openai", "deepseek", "meta", "mistral"} {
		if !known[slug] {
			t.Errorf("known_vendors missing %q: %v", slug, res.KnownVendors)
		}
	}
	if len(res.KnownVendors) < 10 {
		t.Errorf("bedrock vocabulary should be the static set (~19), got %d: %v", len(res.KnownVendors), res.KnownVendors)
	}

	// Unfiltered show is also non-empty, and human output stays the empty-state
	// sentence (synthetics are JSON-only).
	human, err := runCLIArgs(t, root, "models", "policy", "show")
	if err != nil {
		t.Fatalf("human show: %v", err)
	}
	if !strings.Contains(string(human), "No vendor policies configured") {
		t.Errorf("human show should keep the empty-state sentence, got: %s", truncate(human, 300))
	}
}

func TestModelsPolicy_SetClearsSameScopeOverrides(t *testing.T) {
	_, root := newContractVault(t)

	// A stale per-model bulk disable in the vault scope, plus one override in
	// the global scope that must survive and be warned about.
	if _, err := runCLIArgs(t, root,
		"models", "disable", "amazon.titan-embed-text-v2:0", "--provider", "bedrock", "--scope", "vault"); err != nil {
		t.Fatalf("seed vault override: %v", err)
	}
	if _, err := runCLIArgs(t, root,
		"models", "disable", "amazon.nova-pro-v1:0", "--provider", "bedrock", "--scope", "global"); err != nil {
		t.Fatalf("seed global override: %v", err)
	}

	got, err := runCLIArgs(t, root,
		"models", "policy", "set",
		"--provider", "bedrock",
		"--enable-only", "anthropic",
		"--scope", "vault",
		"--json")
	if err != nil {
		t.Fatalf("policy set: %v (out=%s)", err, truncate(got, 500))
	}
	res := decodePolicyResult(t, got)
	if strings.Join(res.ClearedModelOverrides, ",") != "amazon.titan-embed-text-v2:0" {
		t.Errorf("cleared_model_overrides = %v, want the vault-scope titan override", res.ClearedModelOverrides)
	}
	foundOtherScopeWarning := false
	for _, w := range res.Warnings {
		if strings.Contains(w, "global catalog") && strings.Contains(w, "amazon.nova-pro-v1:0") {
			foundOtherScopeWarning = true
		}
	}
	if !foundOtherScopeWarning {
		t.Errorf("expected a warning about the remaining global-scope override, got %v", res.Warnings)
	}
	// The global override still decides its model: counted as overridden.
	if res.Effect.Overridden != 1 {
		t.Errorf("effect.overridden = %d, want 1 (the surviving global override)", res.Effect.Overridden)
	}

	// The vault catalog no longer carries the tri-state.
	data, err := os.ReadFile(filepath.Join(root, ".2ndbrain", "models.yaml"))
	if err != nil {
		t.Fatalf("read vault catalog: %v", err)
	}
	if strings.Contains(string(data), "enabled:") {
		t.Errorf("vault catalog should have no enabled tri-states after policy set, got:\n%s", data)
	}
	// The global catalog keeps its override.
	globalData, err := os.ReadFile(filepath.Join(os.Getenv("HOME"), ".config", "2nb", "models.yaml"))
	if err != nil {
		t.Fatalf("read global catalog: %v", err)
	}
	if !strings.Contains(string(globalData), "enabled: false") {
		t.Errorf("global override must survive a vault-scope policy set, got:\n%s", globalData)
	}
}

func TestModelsPolicy_KeepModelOverridesPreserves(t *testing.T) {
	_, root := newContractVault(t)

	if _, err := runCLIArgs(t, root,
		"models", "disable", "amazon.titan-embed-text-v2:0", "--provider", "bedrock", "--scope", "vault"); err != nil {
		t.Fatalf("seed override: %v", err)
	}

	got, err := runCLIArgs(t, root,
		"models", "policy", "set",
		"--provider", "bedrock",
		"--enable-only", "anthropic",
		"--scope", "vault",
		"--keep-model-overrides",
		"--json")
	if err != nil {
		t.Fatalf("policy set --keep-model-overrides: %v (out=%s)", err, truncate(got, 500))
	}
	res := decodePolicyResult(t, got)
	if len(res.ClearedModelOverrides) != 0 {
		t.Errorf("--keep-model-overrides must clear nothing, got %v", res.ClearedModelOverrides)
	}
	if res.Effect.Overridden != 1 {
		t.Errorf("effect.overridden = %d, want 1 (the kept override)", res.Effect.Overridden)
	}
	data, _ := os.ReadFile(filepath.Join(root, ".2ndbrain", "models.yaml"))
	if !strings.Contains(string(data), "enabled: false") {
		t.Errorf("kept override missing from vault catalog:\n%s", data)
	}
}

func TestModelsPolicy_DryRunWritesNothing(t *testing.T) {
	_, root := newContractVault(t)

	if _, err := runCLIArgs(t, root,
		"models", "disable", "amazon.titan-embed-text-v2:0", "--provider", "bedrock", "--scope", "vault"); err != nil {
		t.Fatalf("seed override: %v", err)
	}

	got, err := runCLIArgs(t, root,
		"models", "policy", "set",
		"--provider", "bedrock",
		"--enable-only", "anthropic",
		"--scope", "vault",
		"--dry-run",
		"--json")
	if err != nil {
		t.Fatalf("policy set --dry-run: %v (out=%s)", err, truncate(got, 500))
	}
	res := decodePolicyResult(t, got)
	if !res.DryRun {
		t.Error("dry_run should be true in the JSON result")
	}
	if strings.Join(res.ClearedModelOverrides, ",") != "amazon.titan-embed-text-v2:0" {
		t.Errorf("dry-run should report what it WOULD clear, got %v", res.ClearedModelOverrides)
	}
	if _, statErr := os.Stat(filepath.Join(root, ".2ndbrain", "models-policy.yaml")); !os.IsNotExist(statErr) {
		t.Errorf("dry-run must not write the policy file (stat err=%v)", statErr)
	}
	data, _ := os.ReadFile(filepath.Join(root, ".2ndbrain", "models.yaml"))
	if !strings.Contains(string(data), "enabled: false") {
		t.Errorf("dry-run must not clear overrides, catalog:\n%s", data)
	}
}

func TestModelsPolicy_ClearRoundTrip(t *testing.T) {
	_, root := newContractVault(t)

	if _, err := runCLIArgs(t, root,
		"models", "policy", "set",
		"--provider", "bedrock",
		"--enable-only", "anthropic",
		"--scope", "vault"); err != nil {
		t.Fatalf("policy set: %v", err)
	}

	got, err := runCLIArgs(t, root,
		"models", "policy", "clear", "--provider", "bedrock", "--scope", "vault", "--json")
	if err != nil {
		t.Fatalf("policy clear: %v (out=%s)", err, truncate(got, 300))
	}
	var res policyClearResult
	if err := json.Unmarshal(got, &res); err != nil {
		t.Fatalf("clear JSON parse: %v (body=%s)", err, truncate(got, 300))
	}
	if !res.Cleared || res.Provider != "bedrock" || res.Scope != "vault" {
		t.Fatalf("unexpected clear result: %+v", res)
	}
	if len(res.Warnings) != 0 {
		t.Errorf("no other-scope policy exists, warnings should be empty: %v", res.Warnings)
	}

	// Show has no configured policy, but JSON still carries synthetic
	// known_vendors rows so a first-run UI can render checkboxes.
	got, err = runCLIArgs(t, root, "models", "policy", "show", "--json")
	if err != nil {
		t.Fatalf("policy show after clear: %v", err)
	}
	var parsed []policyResult
	if err := json.Unmarshal(got, &parsed); err != nil {
		t.Fatalf("show parse: %v (body=%s)", err, truncate(got, 300))
	}
	if len(parsed) == 0 {
		t.Fatal("expected synthetic known_vendors rows after clear, got []")
	}
	for _, r := range parsed {
		if r.Mode == "enable_only" || len(r.Vendors) > 0 {
			t.Fatalf("cleared policy leaked as configured: %+v", r)
		}
	}

	// Clearing again reports cleared=false without error.
	got, err = runCLIArgs(t, root,
		"models", "policy", "clear", "--provider", "bedrock", "--scope", "vault", "--json")
	if err != nil {
		t.Fatalf("second clear: %v", err)
	}
	if err := json.Unmarshal(got, &res); err != nil {
		t.Fatalf("second clear parse: %v", err)
	}
	if res.Cleared {
		t.Error("second clear should report cleared=false")
	}
}

func TestModelsPolicy_ClearWarnsWhenOtherScopeRemains(t *testing.T) {
	_, root := newContractVault(t)

	for _, scope := range []string{"global", "vault"} {
		if _, err := runCLIArgs(t, root,
			"models", "policy", "set",
			"--provider", "bedrock",
			"--enable-only", "anthropic",
			"--scope", scope); err != nil {
			t.Fatalf("policy set %s: %v", scope, err)
		}
	}

	got, err := runCLIArgs(t, root,
		"models", "policy", "clear", "--provider", "bedrock", "--scope", "vault", "--json")
	if err != nil {
		t.Fatalf("policy clear: %v", err)
	}
	var res policyClearResult
	if err := json.Unmarshal(got, &res); err != nil {
		t.Fatalf("clear parse: %v", err)
	}
	if !res.Cleared {
		t.Fatal("vault policy should have been cleared")
	}
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "global-scope policy") {
		t.Errorf("expected a remaining-global-policy warning, got %v", res.Warnings)
	}
}

// TestModelsPolicy_BareDefaultsToShow follows the repo's parent-command
// default pattern: `2nb models policy` behaves like `models policy show`.
func TestModelsPolicy_BareDefaultsToShow(t *testing.T) {
	_, root := newContractVault(t)

	out, err := runCLIArgs(t, root, "models", "policy")
	if err != nil {
		t.Fatalf("bare models policy: %v (out=%s)", err, truncate(out, 300))
	}
	if !strings.Contains(string(out), "No vendor policies configured") {
		t.Errorf("bare `models policy` should render show's empty state, got: %s", truncate(out, 300))
	}
}

// TestModelsPolicy_VaultOverridesGlobalInShow proves provenance: with both
// scopes configured, show reports the vault policy for the provider.
func TestModelsPolicy_VaultOverridesGlobalInShow(t *testing.T) {
	_, root := newContractVault(t)

	if _, err := runCLIArgs(t, root,
		"models", "policy", "set", "--provider", "bedrock", "--enable-only", "amazon", "--scope", "global"); err != nil {
		t.Fatalf("set global: %v", err)
	}
	if _, err := runCLIArgs(t, root,
		"models", "policy", "set", "--provider", "bedrock", "--enable-only", "anthropic", "--scope", "vault"); err != nil {
		t.Fatalf("set vault: %v", err)
	}

	got, err := runCLIArgs(t, root, "models", "policy", "show", "--provider", "bedrock", "--json")
	if err != nil {
		t.Fatalf("show: %v", err)
	}
	var parsed []policyResult
	if err := json.Unmarshal(got, &parsed); err != nil {
		t.Fatalf("show parse: %v (body=%s)", err, truncate(got, 300))
	}
	if len(parsed) != 1 {
		t.Fatalf("expected exactly one merged bedrock policy, got %+v", parsed)
	}
	if parsed[0].Scope != "vault" || strings.Join(parsed[0].Vendors, ",") != "anthropic" {
		t.Errorf("vault policy should override global: %+v", parsed[0])
	}
}

// TestModelsPolicy_ActiveModelGuardWarns: a policy that excludes the active
// generation model's vendor leaves it enabled and warns.
func TestModelsPolicy_ActiveModelGuardWarns(t *testing.T) {
	_, root := newContractVault(t)

	// Default config: generation = anthropic haiku, embedding = amazon nova.
	// cohere-only excludes both active models.
	got, err := runCLIArgs(t, root,
		"models", "policy", "set",
		"--provider", "bedrock",
		"--enable-only", "cohere",
		"--scope", "vault",
		"--json")
	if err != nil {
		t.Fatalf("policy set: %v (out=%s)", err, truncate(got, 500))
	}
	res := decodePolicyResult(t, got)
	guardWarnings := 0
	for _, w := range res.Warnings {
		if strings.Contains(w, "stays enabled") {
			guardWarnings++
		}
	}
	if guardWarnings != 2 {
		t.Errorf("expected 2 active-model guard warnings (embed + gen), got %d: %v", guardWarnings, res.Warnings)
	}

	// The enabled-only dropdown list still contains both active models.
	list, err := runCLIArgs(t, root, "models", "list", "--json", "--porcelain", "--enabled-only", "--provider", "bedrock")
	if err != nil {
		t.Fatalf("models list: %v", err)
	}
	var models []struct {
		ID     string `json:"id"`
		Active bool   `json:"active"`
	}
	if err := json.Unmarshal(list, &models); err != nil {
		t.Fatalf("list parse: %v (body=%s)", err, truncate(list, 300))
	}
	activeCount := 0
	for _, m := range models {
		if m.Active {
			activeCount++
		}
		if strings.HasPrefix(m.ID, "amazon.nova-pro") || strings.HasPrefix(m.ID, "amazon.titan") {
			t.Errorf("policy-disabled model %s leaked into the enabled-only list", m.ID)
		}
	}
	if activeCount != 2 {
		t.Errorf("both active models must survive the policy in dropdowns, got %d active", activeCount)
	}
}

// Pass 5 review gap: the "no models known for provider" branch was untested.
func TestModelsPolicy_UnknownProviderErrors(t *testing.T) {
	_, root := newContractVault(t)
	out, err := runCLIArgs(t, root,
		"models", "policy", "set",
		"--provider", "nosuchprovider",
		"--enable-only", "anthropic",
		"--scope", "vault")
	if err == nil {
		t.Fatalf("expected unknown-provider error, got success (out=%s)", truncate(out, 300))
	}
	if ExitCode(err) != ExitValidation {
		t.Errorf("unknown provider should exit %d (validation), got %d", ExitValidation, ExitCode(err))
	}
	if !strings.Contains(err.Error(), "nosuchprovider") {
		t.Errorf("error should name the provider, got: %s", err.Error())
	}
}

// Pass 5 review gap: an empty or whitespace --enable-only was untested.
func TestModelsPolicy_EmptyEnableOnlyErrors(t *testing.T) {
	_, root := newContractVault(t)
	for _, val := range []string{"", " , ,"} {
		out, err := runCLIArgs(t, root,
			"models", "policy", "set",
			"--provider", "bedrock",
			"--enable-only", val,
			"--scope", "vault")
		if err == nil {
			t.Fatalf("enable-only=%q should error, got success (out=%s)", val, truncate(out, 300))
		}
		if ExitCode(err) != ExitValidation {
			t.Errorf("enable-only=%q should exit %d (validation), got %d", val, ExitValidation, ExitCode(err))
		}
	}
}

// A corrupt vault policy file must surface in BuildModelList warnings (fail
// LOUD, not open) and refuse a new set until fixed.
func TestModelsPolicy_CorruptFileWarnsAndRefusesSet(t *testing.T) {
	_, root := newContractVault(t)
	policyPath := filepath.Join(root, ".2ndbrain", "models-policy.yaml")
	if err := os.WriteFile(policyPath, []byte("{{{ not yaml"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := runCLIArgs(t, root,
		"models", "policy", "set",
		"--provider", "bedrock",
		"--enable-only", "anthropic",
		"--scope", "vault")
	if err == nil || !strings.Contains(err.Error(), "malformed") {
		t.Fatalf("set on a corrupt policy file must refuse with a malformed error, got err=%v out=%s", err, truncate(out, 200))
	}
}
