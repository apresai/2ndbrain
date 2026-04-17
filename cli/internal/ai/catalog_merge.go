package ai

import (
	"context"
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
}

// MergedModelList is the output of BuildModelList.
type MergedModelList struct {
	Verified   []ModelInfo `json:"verified"`
	Unverified []ModelInfo `json:"unverified,omitempty"`
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

	result.Verified = catalog

	// Layer 4: vendor discovery. Only add entries not already in the merged catalog.
	if opts.Discover {
		vendorModels := discoverVendorModels(ctx, opts.Config)
		idx := catalogIndex(catalog)
		for _, m := range vendorModels {
			if !idx[catalogKey(m.Provider, m.ID)] {
				result.Unverified = append(result.Unverified, m)
			}
		}
	}

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
// in parallel. Errors are silently ignored (provider may be unreachable or lack credentials).
func discoverVendorModels(ctx context.Context, cfg AIConfig) []ModelInfo {
	var (
		mu  sync.Mutex
		all []ModelInfo
		wg  sync.WaitGroup
	)

	// Bedrock vendor discovery.
	wg.Add(1)
	go func() {
		defer wg.Done()
		if models, err := ListBedrockVendorModels(ctx, cfg.Bedrock); err == nil {
			mu.Lock()
			all = append(all, models...)
			mu.Unlock()
		}
	}()

	// OpenRouter vendor discovery.
	wg.Add(1)
	go func() {
		defer wg.Done()
		key, err := GetAPIKey("openrouter")
		if err != nil || key == "" {
			return
		}
		models, err := ListOpenRouterModels(ctx, key, "")
		if err != nil {
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
	return all
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
