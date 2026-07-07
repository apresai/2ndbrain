package cli

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/apresai/2ndbrain/internal/ai"
	"github.com/apresai/2ndbrain/internal/output"
	"github.com/spf13/cobra"
)

// `models policy` is the declarative counterpart to `models enable/disable
// --vendor`: instead of stamping one synthetic Enabled entry per model (which
// pollutes the user catalog and misses models discovered later), a policy is a
// durable "enable only these vendors" statement per provider. BuildModelList
// applies it to every nil-Enabled model at list time, so newly discovered
// models from non-chosen vendors arrive pre-disabled forever. Storage is
// models-policy.yaml (vault .2ndbrain/ or ~/.config/2nb/), never models.yaml
// or config.yaml, which older CLIs would round-trip through typed structs and
// silently strip.

var (
	policySetProvider      string
	policySetEnableOnly    string
	policySetScope         string
	policySetDryRun        bool
	policySetKeepOverrides bool

	policyShowProvider string

	policyClearProvider string
	policyClearScope    string
)

var modelsPolicyCmd = &cobra.Command{
	Use:   "policy",
	Short: "Manage per-provider vendor policies (enable only chosen vendors)",
	Long: `A vendor policy is a durable "enable only these vendors" statement for one
provider. Every model on that provider without an explicit per-model
enable/disable gets its verdict from the policy at list time, including
models discovered in the future, which arrive pre-disabled when their vendor
is not in the list.

Precedence: explicit per-model Enabled (models enable/disable) > policy >
tier default. The active embedding/generation/rerank models are never
policy-disabled; they stay enabled with a warning instead.

Policies live in models-policy.yaml (vault .2ndbrain/ or ~/.config/2nb/); a
vault policy for a provider fully overrides the global one.`,
	Example: `  2nb models policy set --provider bedrock --enable-only anthropic,deepseek
  2nb models policy show
  2nb models policy clear --provider bedrock`,
	Args: cobra.NoArgs,
	// Default action when invoked without a subcommand: show policies.
	RunE: runModelsPolicyShow,
}

var modelsPolicySetCmd = &cobra.Command{
	Use:   "set",
	Short: "Set an enable-only vendor policy for a provider",
	Long: `Persist the policy and, by default, clear same-scope per-model
enable/disable overrides for the provider so a stale bulk toggle cannot
shadow the new policy (--keep-model-overrides opts out; overrides in the
other scope are reported, never touched). --dry-run computes the per-vendor
effect without writing anything.`,
	Example: `  2nb models policy set --provider bedrock --enable-only anthropic
  2nb models policy set --provider bedrock --enable-only anthropic,deepseek --scope global
  2nb models policy set --provider bedrock --enable-only anthropic --dry-run --json`,
	Args: cobra.NoArgs,
	RunE: runModelsPolicySet,
}

var modelsPolicyShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Show configured vendor policies and their per-vendor effect",
	Args:  cobra.NoArgs,
	RunE:  runModelsPolicyShow,
}

var modelsPolicyClearCmd = &cobra.Command{
	Use:   "clear",
	Short: "Remove the vendor policy for a provider",
	Args:  cobra.NoArgs,
	RunE:  runModelsPolicyClear,
}

func init() {
	modelsPolicySetCmd.Flags().StringVar(&policySetProvider, "provider", "", "Provider the policy applies to: bedrock, openrouter, ollama (required)")
	modelsPolicySetCmd.Flags().StringVar(&policySetEnableOnly, "enable-only", "", "Comma-separated vendor slugs to keep enabled (e.g. anthropic,deepseek) (required)")
	modelsPolicySetCmd.Flags().StringVar(&policySetScope, "scope", "vault", "Scope: vault (.2ndbrain/models-policy.yaml) or global (~/.config/2nb/models-policy.yaml)")
	modelsPolicySetCmd.Flags().BoolVar(&policySetDryRun, "dry-run", false, "Compute the effect without writing anything")
	modelsPolicySetCmd.Flags().BoolVar(&policySetKeepOverrides, "keep-model-overrides", false, "Keep same-scope per-model enable/disable overrides (default clears them so they cannot shadow the policy)")
	_ = modelsPolicySetCmd.MarkFlagRequired("provider")
	_ = modelsPolicySetCmd.MarkFlagRequired("enable-only")
	_ = modelsPolicySetCmd.RegisterFlagCompletionFunc("provider", completeProviders)
	_ = modelsPolicySetCmd.RegisterFlagCompletionFunc("scope", completeCatalogScopes)

	modelsPolicyShowCmd.Flags().StringVar(&policyShowProvider, "provider", "", "Only show the policy for this provider")
	_ = modelsPolicyShowCmd.RegisterFlagCompletionFunc("provider", completeProviders)

	modelsPolicyClearCmd.Flags().StringVar(&policyClearProvider, "provider", "", "Provider whose policy to remove (required)")
	modelsPolicyClearCmd.Flags().StringVar(&policyClearScope, "scope", "vault", "Scope: vault or global")
	_ = modelsPolicyClearCmd.MarkFlagRequired("provider")
	_ = modelsPolicyClearCmd.RegisterFlagCompletionFunc("provider", completeProviders)
	_ = modelsPolicyClearCmd.RegisterFlagCompletionFunc("scope", completeCatalogScopes)

	modelsPolicyCmd.AddCommand(modelsPolicySetCmd)
	modelsPolicyCmd.AddCommand(modelsPolicyShowCmd)
	modelsPolicyCmd.AddCommand(modelsPolicyClearCmd)
	modelsCmd.AddCommand(modelsPolicyCmd)
}

// policyVendorEffect is the per-vendor slice of a policy's effect.
type policyVendorEffect struct {
	Models int    `json:"models"`
	State  string `json:"state"` // "enabled" or "disabled" (the policy's verdict for the vendor)
}

// policyEffect summarizes what a policy does to the provider's merged
// catalog: final enabled/disabled counts, how many models an explicit
// per-model override decided instead of the policy, and the per-vendor view.
type policyEffect struct {
	Enabled    int                           `json:"enabled"`
	Disabled   int                           `json:"disabled"`
	Overridden int                           `json:"overridden"`
	ByVendor   map[string]policyVendorEffect `json:"by_vendor"`
}

// policyResult is the JSON contract for `models policy set` and each element
// of `models policy show`.
type policyResult struct {
	Provider              string       `json:"provider"`
	Mode                  string       `json:"mode"`
	Vendors               []string     `json:"vendors"`
	Scope                 string       `json:"scope"`
	DryRun                bool         `json:"dry_run"`
	Effect                policyEffect `json:"effect"`
	Warnings              []string     `json:"warnings"`
	ClearedModelOverrides []string     `json:"cleared_model_overrides"`
}

// policyClearResult is the JSON contract for `models policy clear`.
type policyClearResult struct {
	Provider string   `json:"provider"`
	Scope    string   `json:"scope"`
	Cleared  bool     `json:"cleared"`
	Warnings []string `json:"warnings"`
}

func runModelsPolicySet(cmd *cobra.Command, args []string) error {
	scope, err := parsePolicyScope(policySetScope)
	if err != nil {
		return err
	}
	vendors := ai.NormalizeVendorSlugs(strings.Split(policySetEnableOnly, ","))
	if len(vendors) == 0 {
		return exitWithError(ExitValidation, "--enable-only requires at least one vendor slug (e.g. --enable-only anthropic,deepseek)")
	}

	vaultRoot, cfg, err := resolvePolicyVault(scope)
	if err != nil {
		return err
	}

	ctx := context.Background()
	providerModels, err := policyProviderModels(ctx, cfg, vaultRoot, policySetProvider)
	if err != nil {
		return err
	}

	// Vendor slugs validate against VendorOf over the merged catalog plus the
	// provider's static vendor vocabulary; an unknown slug fails loud with the
	// full known list so a typo cannot silently disable everything.
	known := ai.KnownVendorSlugs(policySetProvider, providerModels)
	var unknown []string
	for _, v := range vendors {
		if !known[v] {
			unknown = append(unknown, v)
		}
	}
	if len(unknown) > 0 {
		return exitWithError(ExitValidation, fmt.Sprintf(
			"unknown vendor slug(s) %s for provider %s\nknown vendors: %s",
			strings.Join(unknown, ", "), policySetProvider, strings.Join(sortedSlugs(known), ", ")))
	}

	// Overrides: same-scope ones are cleared by default (a stale bulk disable
	// must not shadow the new policy); other-scope ones are reported but never
	// touched. Effect is computed over the overrides that will remain.
	sameScope, err := ai.ModelEnabledOverrides(scope, vaultRoot, policySetProvider)
	if err != nil {
		return err
	}
	otherScope, err := otherScopeOverrides(scope, vaultRoot, policySetProvider)
	if err != nil {
		return err
	}
	remaining := map[string]bool{}
	var cleared []string
	if policySetKeepOverrides {
		// Effective merge: vault wins over global for the same model.
		if scope == ai.ScopeVault {
			mergeOverrides(remaining, otherScope, sameScope)
		} else {
			mergeOverrides(remaining, sameScope, otherScope)
		}
	} else {
		for id := range sameScope {
			cleared = append(cleared, id)
		}
		sort.Strings(cleared)
		mergeOverrides(remaining, otherScope)
	}

	vendorSet := map[string]bool{}
	for _, v := range vendors {
		vendorSet[v] = true
	}
	effect, warnings := computePolicyEffect(providerModels, policySetProvider, vendorSet, remaining, ai.VendorPolicyActiveGuard(cfg))
	if len(otherScope) > 0 {
		otherName := ai.ScopeGlobal
		if scope == ai.ScopeGlobal {
			otherName = ai.ScopeVault
		}
		warnings = append(warnings, fmt.Sprintf(
			"%d per-model override(s) remain in the %s catalog and take precedence over the policy: %s",
			len(otherScope), otherName, strings.Join(sortedOverrideIDs(otherScope), ", ")))
	}

	policy := ai.VendorPolicy{Provider: policySetProvider, Mode: ai.VendorPolicyModeEnableOnly, Vendors: vendors}
	if !policySetDryRun {
		if !policySetKeepOverrides && len(cleared) > 0 {
			if _, err := ai.ClearModelEnabledOverrides(scope, vaultRoot, policySetProvider); err != nil {
				return fmt.Errorf("clear per-model overrides: %w", err)
			}
		}
		if err := ai.SaveVendorPolicy(scope, vaultRoot, policy); err != nil {
			return fmt.Errorf("save policy: %w", err)
		}
		slog.Info("models policy set", "provider", policy.Provider, "vendors", strings.Join(vendors, ","), "scope", string(scope), "cleared_overrides", len(cleared))
	}

	res := policyResult{
		Provider:              policy.Provider,
		Mode:                  policy.Mode,
		Vendors:               vendors,
		Scope:                 string(scope),
		DryRun:                policySetDryRun,
		Effect:                effect,
		Warnings:              warnings,
		ClearedModelOverrides: cleared,
	}
	ensurePolicyResultSlices(&res)

	if format := getFormat(cmd); format != "" {
		return output.Write(os.Stdout, format, res)
	}

	if policySetDryRun {
		fmt.Printf("Dry run, nothing written. Policy for %s (%s scope): enable only %s\n",
			res.Provider, res.Scope, strings.Join(vendors, ", "))
	} else {
		fmt.Printf("Vendor policy for %s saved to %s scope: enable only %s\n",
			res.Provider, res.Scope, strings.Join(vendors, ", "))
	}
	printPolicyEffect(res.Effect)
	if len(cleared) > 0 {
		verb := "Cleared"
		if policySetDryRun {
			verb = "Would clear"
		}
		fmt.Printf("%s %d same-scope per-model override(s): %s\n", verb, len(cleared), strings.Join(cleared, ", "))
	}
	printPolicyWarnings(res.Warnings)
	return nil
}

func runModelsPolicyShow(cmd *cobra.Command, args []string) error {
	vaultRoot, cfg, err := resolvePolicyVault(ai.ScopeGlobal) // vault optional for show
	if err != nil {
		return err
	}

	policies := ai.LoadVendorPolicies(vaultRoot)
	if policyShowProvider != "" {
		filtered := policies[:0]
		for _, p := range policies {
			if p.Provider == policyShowProvider {
				filtered = append(filtered, p)
			}
		}
		policies = filtered
	}

	ctx := context.Background()
	guard := ai.VendorPolicyActiveGuard(cfg)
	results := make([]policyResult, 0, len(policies))
	for _, p := range policies {
		res := policyResult{
			Provider: p.Provider,
			Mode:     p.Mode,
			Vendors:  append([]string{}, p.Vendors...),
			Scope:    string(p.Scope),
		}
		if p.Mode == ai.VendorPolicyModeEnableOnly {
			providerModels, err := policyProviderModelsLenient(ctx, cfg, vaultRoot, p.Provider)
			if err != nil {
				return err
			}
			overrides := map[string]bool{}
			if globalOv, err := ai.ModelEnabledOverrides(ai.ScopeGlobal, vaultRoot, p.Provider); err == nil {
				mergeOverrides(overrides, globalOv)
			}
			if vaultRoot != "" {
				if vaultOv, err := ai.ModelEnabledOverrides(ai.ScopeVault, vaultRoot, p.Provider); err == nil {
					mergeOverrides(overrides, vaultOv)
				}
			}
			vendorSet := map[string]bool{}
			for _, v := range p.Vendors {
				vendorSet[v] = true
			}
			res.Effect, res.Warnings = computePolicyEffect(providerModels, p.Provider, vendorSet, overrides, guard)
		} else {
			res.Warnings = []string{fmt.Sprintf("unsupported mode %q, this policy is ignored (written by a newer 2nb?)", p.Mode)}
		}
		ensurePolicyResultSlices(&res)
		results = append(results, res)
	}

	if format := getFormat(cmd); format != "" {
		return output.Write(os.Stdout, format, results)
	}

	if len(results) == 0 {
		if policyShowProvider != "" {
			fmt.Printf("No vendor policy configured for %s.\n", policyShowProvider)
		} else {
			fmt.Println("No vendor policies configured.")
		}
		fmt.Println("Set one with: 2nb models policy set --provider bedrock --enable-only anthropic")
		return nil
	}
	for i, res := range results {
		if i > 0 {
			fmt.Println()
		}
		fmt.Printf("%s: enable only %s (%s scope)\n", res.Provider, strings.Join(res.Vendors, ", "), res.Scope)
		printPolicyEffect(res.Effect)
		printPolicyWarnings(res.Warnings)
	}
	return nil
}

func runModelsPolicyClear(cmd *cobra.Command, args []string) error {
	scope, err := parsePolicyScope(policyClearScope)
	if err != nil {
		return err
	}
	vaultRoot, _, err := resolvePolicyVault(scope)
	if err != nil {
		return err
	}

	cleared, err := ai.ClearVendorPolicy(scope, vaultRoot, policyClearProvider)
	if err != nil {
		return fmt.Errorf("clear policy: %w", err)
	}
	slog.Info("models policy clear", "provider", policyClearProvider, "scope", string(scope), "cleared", cleared)

	// If the other scope still holds a policy for this provider, the
	// effective policy has not actually gone away: say so.
	var warnings []string
	for _, p := range ai.LoadVendorPolicies(vaultRoot) {
		if p.Provider == policyClearProvider {
			warnings = append(warnings, fmt.Sprintf(
				"a %s-scope policy for %s remains in effect (enable only %s)",
				p.Scope, p.Provider, strings.Join(p.Vendors, ", ")))
		}
	}

	res := policyClearResult{
		Provider: policyClearProvider,
		Scope:    string(scope),
		Cleared:  cleared,
		Warnings: warnings,
	}
	if res.Warnings == nil {
		res.Warnings = []string{}
	}

	if format := getFormat(cmd); format != "" {
		return output.Write(os.Stdout, format, res)
	}
	if cleared {
		fmt.Printf("Cleared vendor policy for %s from %s scope\n", res.Provider, res.Scope)
	} else {
		fmt.Printf("No vendor policy for %s at %s scope\n", res.Provider, res.Scope)
	}
	printPolicyWarnings(res.Warnings)
	return nil
}

// parsePolicyScope validates a --scope value, failing with ExitValidation so
// scripts can distinguish bad input from a missing vault.
func parsePolicyScope(scopeStr string) (ai.UserCatalogScope, error) {
	scope := ai.UserCatalogScope(scopeStr)
	if scope != ai.ScopeGlobal && scope != ai.ScopeVault {
		return "", exitWithError(ExitValidation, fmt.Sprintf("--scope must be %q or %q, got %q", ai.ScopeGlobal, ai.ScopeVault, scopeStr))
	}
	return scope, nil
}

// resolvePolicyVault opens the vault for its root and AI config. Vault scope
// requires one; global scope degrades to no vault root and the default config
// (the active-model guard then protects the default models), so a global
// policy can be managed outside any vault.
func resolvePolicyVault(scope ai.UserCatalogScope) (string, ai.AIConfig, error) {
	v, err := openVault()
	if err != nil {
		if scope == ai.ScopeVault {
			return "", ai.AIConfig{}, fmt.Errorf("vault scope: %w", err)
		}
		return "", ai.DefaultAIConfig(), nil
	}
	defer v.Close()
	setupFileLogging(v)
	return v.Root, v.Config.AI, nil
}

// policyProviderModels enumerates the merged catalog (builtin + user, no
// network) for one provider, including disabled providers so a policy can be
// managed for a silenced provider. Unknown providers fail with the known list.
func policyProviderModels(ctx context.Context, cfg ai.AIConfig, vaultRoot, provider string) ([]ai.ModelInfo, error) {
	models, providers, err := policyCatalog(ctx, cfg, vaultRoot, provider)
	if err != nil {
		return nil, err
	}
	if len(models) == 0 {
		return nil, exitWithError(ExitValidation, fmt.Sprintf(
			"no models known for provider %q (known providers: %s)", provider, strings.Join(providers, ", ")))
	}
	return models, nil
}

// policyProviderModelsLenient is policyProviderModels for `show`: a policy
// referencing a provider with no cataloged models renders an empty effect
// instead of erroring, so show never refuses to display what is configured.
func policyProviderModelsLenient(ctx context.Context, cfg ai.AIConfig, vaultRoot, provider string) ([]ai.ModelInfo, error) {
	models, _, err := policyCatalog(ctx, cfg, vaultRoot, provider)
	return models, err
}

func policyCatalog(ctx context.Context, cfg ai.AIConfig, vaultRoot, provider string) ([]ai.ModelInfo, []string, error) {
	list, err := ai.BuildModelList(ctx, ai.MergedListOptions{
		Config:                   cfg,
		VaultRoot:                vaultRoot,
		IncludeDisabledProviders: true,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("load model catalog: %w", err)
	}
	providerSet := map[string]bool{}
	var models []ai.ModelInfo
	for _, m := range list.Verified {
		providerSet[m.Provider] = true
		if m.Provider == provider {
			models = append(models, m)
		}
	}
	providers := make([]string, 0, len(providerSet))
	for p := range providerSet {
		providers = append(providers, p)
	}
	sort.Strings(providers)
	return models, providers, nil
}

// computePolicyEffect applies the policy rules to the provider's models
// without writing anything: explicit overrides win (counted as overridden),
// then the vendor verdict, with the active-model guard as the floor. The
// per-model Enabled values in the input list are deliberately ignored: they
// may already carry a PERSISTED policy's verdict, and the effect must reflect
// the PROPOSED policy plus the overrides map the caller computed.
func computePolicyEffect(models []ai.ModelInfo, provider string, vendorSet, overrides map[string]bool, guard func(ai.ModelInfo) bool) (policyEffect, []string) {
	eff := policyEffect{ByVendor: map[string]policyVendorEffect{}}
	var warnings []string
	for _, m := range models {
		vendor := ai.VendorOf(m.ID, provider).Vendor
		ve := eff.ByVendor[vendor]
		ve.Models++
		ve.State = "disabled"
		if vendorSet[vendor] {
			ve.State = "enabled"
		}
		eff.ByVendor[vendor] = ve

		var enabled bool
		switch {
		case hasOverride(overrides, m.ID):
			enabled = overrides[m.ID]
			eff.Overridden++
		case vendorSet[vendor]:
			enabled = true
		case guard != nil && guard(m):
			enabled = true
			warnings = append(warnings, fmt.Sprintf(
				"active %s model %s stays enabled although vendor %q is not in the enable-only list",
				m.Type, m.ID, vendor))
		}
		if enabled {
			eff.Enabled++
		} else {
			eff.Disabled++
		}
	}
	return eff, warnings
}

func hasOverride(overrides map[string]bool, id string) bool {
	_, ok := overrides[id]
	return ok
}

// otherScopeOverrides reads the per-model overrides in the scope NOT being
// written. The vault side is skipped when no vault is open.
func otherScopeOverrides(scope ai.UserCatalogScope, vaultRoot, provider string) (map[string]bool, error) {
	if scope == ai.ScopeVault {
		return ai.ModelEnabledOverrides(ai.ScopeGlobal, vaultRoot, provider)
	}
	if vaultRoot == "" {
		return map[string]bool{}, nil
	}
	return ai.ModelEnabledOverrides(ai.ScopeVault, vaultRoot, provider)
}

// mergeOverrides copies each map into dst in order, so later maps win.
func mergeOverrides(dst map[string]bool, srcs ...map[string]bool) {
	for _, src := range srcs {
		for id, v := range src {
			dst[id] = v
		}
	}
}

func sortedSlugs(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for s := range set {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

func sortedOverrideIDs(overrides map[string]bool) []string {
	out := make([]string, 0, len(overrides))
	for id := range overrides {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// ensurePolicyResultSlices keeps the JSON contract free of nulls: warnings,
// cleared_model_overrides, and vendors always serialize as arrays.
func ensurePolicyResultSlices(res *policyResult) {
	if res.Vendors == nil {
		res.Vendors = []string{}
	}
	if res.Warnings == nil {
		res.Warnings = []string{}
	}
	if res.ClearedModelOverrides == nil {
		res.ClearedModelOverrides = []string{}
	}
	if res.Effect.ByVendor == nil {
		res.Effect.ByVendor = map[string]policyVendorEffect{}
	}
}

func printPolicyEffect(eff policyEffect) {
	if len(eff.ByVendor) > 0 {
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "VENDOR\tMODELS\tSTATE")
		for _, vendor := range sortedVendorKeys(eff.ByVendor) {
			ve := eff.ByVendor[vendor]
			fmt.Fprintf(w, "%s\t%d\t%s\n", vendor, ve.Models, ve.State)
		}
		_ = w.Flush()
	}
	fmt.Printf("Effect: %d enabled, %d disabled, %d overridden (explicit per-model settings win)\n",
		eff.Enabled, eff.Disabled, eff.Overridden)
}

func sortedVendorKeys(byVendor map[string]policyVendorEffect) []string {
	out := make([]string, 0, len(byVendor))
	for v := range byVendor {
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

func printPolicyWarnings(warnings []string) {
	for _, w := range warnings {
		fmt.Fprintf(os.Stderr, "warning: %s\n", w)
	}
}

// preserveScopeEnabled returns the explicit Enabled tri-state already
// recorded at the destination scope for (provider, id), or nil. Persistence
// paths that start from a BuildModelList-derived ModelInfo (models test
// --save, models verify, bench summaries) must route the Enabled field
// through this before SaveUserCatalogEntry: the merged list carries
// policy-derived tri-states, and persisting one would freeze the policy's
// verdict as a per-model user override that shadows every future policy
// change.
func preserveScopeEnabled(scope ai.UserCatalogScope, vaultRoot, provider, id string) *bool {
	overrides, err := ai.ModelEnabledOverrides(scope, vaultRoot, provider)
	if err != nil {
		return nil
	}
	if v, ok := overrides[id]; ok {
		return ai.Ptr(v)
	}
	return nil
}
