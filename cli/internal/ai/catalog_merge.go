package ai

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"sync"
	"time"
)

// MergedListOptions controls what gets included in the model list.
type MergedListOptions struct {
	Config      AIConfig // current vault config
	Discover    bool     // include vendor-discovered models
	CheckStatus bool     // probe reachability and credentials
	// VaultRoot, if set, loads the per-vault user catalog from
	// <VaultRoot>/.2ndbrain/models.yaml in addition to the global one.
	VaultRoot string
	// EnabledOnly, when true, filters out entries whose Enabled field is
	// explicitly set to false. Nil-Enabled entries (the default) are treated
	// as visible so builtin models without an explicit user override remain
	// present. Use this to produce the subset shown in GUI dropdowns.
	EnabledOnly bool
	// IncludeDisabledProviders keeps models from providers the user has
	// silenced via ai.<provider>.disabled. The setup wizard sets this: it is
	// the surface where a user enables an opt-in provider (Ollama/OpenRouter
	// ship disabled), so hiding disabled-but-reachable providers there would
	// dead-end onboarding for anyone without Bedrock credentials.
	IncludeDisabledProviders bool
}

// MergedModelList is the output of BuildModelList.
type MergedModelList struct {
	Verified   []ModelInfo `json:"verified"`
	Unverified []ModelInfo `json:"unverified,omitempty"`
	Warnings   []string    `json:"warnings,omitempty"`
}

// BuildModelList produces a unified model catalog by merging these layers
// (lowest to highest precedence):
//
//  1. BuiltinCatalog() — hand-curated verified models in Go source
//  2. User catalog (global ~/.config/2nb/models.yaml)
//  3. User catalog (per-vault .2ndbrain/models.yaml)
//  4. Live vendor discovery (only if Discover=true and entry not already present)
//
// User-catalog entries with matching (provider, id) overlay scalar fields
// onto builtin entries but never demote the Tier (see elevateTier). Entries
// unique to the user catalog are appended as TierUserVerified.
func BuildModelList(ctx context.Context, opts MergedListOptions) (*MergedModelList, error) {
	catalog := BuiltinCatalog()
	result := &MergedModelList{}

	// Layer 2+3: overlay user catalog (global merged with per-vault).
	if user := LoadUserCatalog(opts.VaultRoot); len(user) > 0 {
		catalog = overlay(catalog, user)
	}

	// Mark active models based on current config.
	for i := range catalog {
		catalog[i].Active = isActiveModel(catalog[i], opts.Config)
	}

	// Status checks: probe credentials and reachability per provider.
	if opts.CheckStatus {
		applyStatusChecks(ctx, catalog, opts.Config)
	}

	// EnabledOnly: drop any entry that has Enabled explicitly set to false.
	// Nil means the user never touched the flag, so we treat it as visible.
	if opts.EnabledOnly {
		catalog = filterEnabled(catalog)
	}
	// Provider-level disable trumps everything: if the user silenced a
	// provider (e.g. Ollama isn't running and they don't want it in the
	// catalog), drop every entry from that provider regardless of tier
	// or Enabled state. The setup wizard opts out (IncludeDisabledProviders)
	// because it's where opt-in providers get enabled.
	if !opts.IncludeDisabledProviders {
		catalog = filterDisabledProviders(catalog, opts.Config)
	}
	result.Verified = catalog

	// Layer 4: vendor discovery. Only add entries not already in the merged catalog.
	if opts.Discover {
		vendorModels, warnings := discoverVendorModels(ctx, opts.Config)
		result.Warnings = append(result.Warnings, warnings...)
		idx := catalogIndex(catalog)
		for _, m := range vendorModels {
			if !idx[catalogKey(m.Provider, m.ID)] {
				result.Unverified = append(result.Unverified, m)
			}
		}
		if opts.EnabledOnly {
			result.Unverified = filterEnabled(result.Unverified)
		}
		if !opts.IncludeDisabledProviders {
			result.Unverified = filterDisabledProviders(result.Unverified, opts.Config)
		}
	}

	result.Verified = EnrichModelPricing(ctx, opts.Config, result.Verified)
	result.Unverified = EnrichModelPricing(ctx, opts.Config, result.Unverified)

	applyCatalogUIFields(result.Verified)
	applyCatalogUIFields(result.Unverified)

	sortModels(result.Verified)
	sortModels(result.Unverified)

	return result, nil
}

// isActiveModel returns true if the model matches the current config.
func isActiveModel(m ModelInfo, cfg AIConfig) bool {
	if m.Provider != cfg.Provider {
		return false
	}
	switch m.Type {
	case "embedding":
		return m.ID == cfg.EmbeddingModel
	case "generation":
		return m.ID == cfg.GenerationModel
	}
	return false
}

// applyStatusChecks probes credentials and reachability for each provider
// in parallel and sets CredsOK/Reachable on matching catalog entries.
func applyStatusChecks(ctx context.Context, catalog []ModelInfo, cfg AIConfig) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	var (
		bedrockOK, orOK, ollamaOK bool
		wg                        sync.WaitGroup
	)

	wg.Add(3)
	go func() {
		defer wg.Done()
		bedrockOK = CheckBedrockCredentials(ctx, cfg.Bedrock)
	}()
	go func() {
		defer wg.Done()
		orOK = HasAPIKey("openrouter")
	}()
	go func() {
		defer wg.Done()
		endpoint := cfg.Ollama.Endpoint
		if endpoint == "" {
			endpoint = "http://localhost:11434"
		}
		ollamaOK = ollamaHeartbeat(ctx, http.DefaultClient, endpoint)
	}()
	wg.Wait()

	providerCreds := map[string]*bool{
		"bedrock":    &bedrockOK,
		"openrouter": &orOK,
		"ollama":     &ollamaOK,
	}

	for i := range catalog {
		if creds, ok := providerCreds[catalog[i].Provider]; ok {
			if catalog[i].Provider == "ollama" {
				catalog[i].Reachable = creds
			} else {
				catalog[i].CredsOK = creds
			}
		}
	}
}

// discoverVendorModels queries all provider APIs for their full model catalogs
// in parallel. Errors are returned as warnings so callers can explain why a
// provider contributed no discovered rows.
func discoverVendorModels(ctx context.Context, cfg AIConfig) ([]ModelInfo, []string) {
	var (
		mu       sync.Mutex
		all      []ModelInfo
		warnings []string
		wg       sync.WaitGroup
	)
	addWarning := func(provider string, err error) {
		if err == nil {
			return
		}
		msg := fmt.Sprintf("%s discovery failed: %v", provider, err)
		slog.Warn("vendor discovery failed", "provider", provider, "err", err)
		mu.Lock()
		warnings = append(warnings, msg)
		mu.Unlock()
	}

	// Bedrock vendor discovery.
	wg.Add(1)
	go func() {
		defer wg.Done()
		models, err := ListBedrockVendorModels(ctx, cfg.Bedrock)
		if err != nil {
			addWarning("bedrock", err)
			return
		}
		mu.Lock()
		all = append(all, models...)
		mu.Unlock()
	}()

	// OpenRouter vendor discovery.
	wg.Add(1)
	go func() {
		defer wg.Done()
		key, err := GetAPIKey("openrouter")
		if err != nil || key == "" {
			if err != nil {
				addWarning("openrouter", err)
			} else {
				addWarning("openrouter", fmt.Errorf("API key not configured"))
			}
			return
		}
		models, err := ListOpenRouterModels(ctx, key, "")
		if err != nil {
			addWarning("openrouter", err)
			return
		}
		for i := range models {
			models[i].Tier = TierUnverified
			models[i].Notes = "not tested with 2nb"
		}
		mu.Lock()
		all = append(all, models...)
		mu.Unlock()
	}()

	// Ollama: discover installed models.
	wg.Add(1)
	go func() {
		defer wg.Done()
		endpoint := cfg.Ollama.Endpoint
		if endpoint == "" {
			endpoint = "http://localhost:11434"
		}
		models, err := ListOllamaModels(ctx, http.DefaultClient, endpoint)
		if err != nil {
			addWarning("ollama", err)
			return
		}
		for i := range models {
			models[i].Tier = TierUnverified
			models[i].Notes = "installed locally, not in verified catalog"
		}
		mu.Lock()
		all = append(all, models...)
		mu.Unlock()
	}()

	wg.Wait()
	return all, warnings
}

// filterEnabled removes entries that are explicitly disabled (Enabled != nil &&
// *Enabled == false). Nil-Enabled entries are treated as visible per the
// "nil means default, which is visible" rule documented on ModelInfo.Enabled.
func filterEnabled(models []ModelInfo) []ModelInfo {
	out := models[:0:0] // start fresh, same backing array length hint
	for _, m := range models {
		if m.Enabled != nil && !*m.Enabled {
			continue
		}
		out = append(out, m)
	}
	return out
}

// filterDisabledProviders removes entries whose provider is silenced by
// cfg (e.g. cfg.Bedrock.Disabled == true). Unlike filterEnabled this acts
// at the provider level, not per-model — a disabled provider shouldn't
// offer any models in the catalog or selection dropdowns.
func filterDisabledProviders(models []ModelInfo, cfg AIConfig) []ModelInfo {
	out := models[:0:0]
	for _, m := range models {
		if cfg.ProviderDisabled(m.Provider) {
			continue
		}
		out = append(out, m)
	}
	return out
}

// sortModels sorts by provider, then type (embedding first), then model ID.
func sortModels(models []ModelInfo) {
	sort.Slice(models, func(i, j int) bool {
		if models[i].Provider != models[j].Provider {
			return models[i].Provider < models[j].Provider
		}
		if models[i].Type != models[j].Type {
			return models[i].Type == "embedding" // embedding before generation
		}
		return models[i].ID < models[j].ID
	})
}

func applyCatalogUIFields(models []ModelInfo) {
	for i := range models {
		vendor := VendorOf(models[i].ID, models[i].Provider)
		models[i].Vendor = vendor.Vendor
		models[i].VendorDisplay = vendor.Display
		models[i].Family = vendor.Family
		models[i].VersionSortKey = VersionSortKey(models[i].ID)
		models[i].Compatible, models[i].CompatibilityReason = catalogCompatibility(models[i])
	}
}

func catalogCompatibility(m ModelInfo) (bool, string) {
	if m.Provider != "bedrock" {
		return true, ""
	}
	if ok, reason := bedrockModelSupported(m.ID, m.Type); !ok {
		return false, reason
	}
	return true, ""
}
