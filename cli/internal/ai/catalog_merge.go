package ai

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

// MergedListOptions controls what gets included in the model list.
type MergedListOptions struct {
	Config      AIConfig // current vault config
	Discover    bool     // include vendor-discovered models
	CheckStatus bool     // probe reachability and credentials
	// DiscoverCached serves Bedrock discovery from the 24h disk cache when
	// one is warm (see discovery_cache.go). Callers that run discovery
	// repeatedly on a hot path set it — `models verify --discover` does, so
	// a validation pass doesn't re-walk the whole Bedrock control plane. A
	// caller whose entire purpose is showing the current vendor catalog
	// (`models list --discover`) leaves it off and gets a live listing.
	DiscoverCached bool
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

	// Vendor policy: give every nil-Enabled model on a policied provider an
	// explicit tri-state (enable-only vendors stay visible, everything else
	// is disabled) BEFORE filterEnabled runs, so dropdowns, the Hub catalog,
	// and every other consumer honor the policy through this one hook.
	// Explicit per-model Enabled from the user catalog wins; the active
	// embedding/generation/rerank models are never policy-disabled.
	policies, policyWarnings := LoadVendorPolicies(opts.VaultRoot)
	result.Warnings = append(result.Warnings, policyWarnings...)
	policyGuard := VendorPolicyActiveGuard(opts.Config)
	result.Warnings = append(result.Warnings, applyVendorPolicy(catalog, policies, policyGuard)...)

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
		vendorModels, warnings := discoverVendorModels(ctx, opts.Config, opts.DiscoverCached)
		result.Warnings = append(result.Warnings, warnings...)
		result.Unverified = mergeDiscovered(catalog, vendorModels)
		// Freshly discovered entries get the same policy verdict as the
		// merged catalog: models from non-chosen vendors arrive pre-disabled.
		result.Warnings = append(result.Warnings, applyVendorPolicy(result.Unverified, policies, policyGuard)...)
		if opts.EnabledOnly {
			result.Unverified = filterEnabled(result.Unverified)
		}
		if !opts.IncludeDisabledProviders {
			result.Unverified = filterDisabledProviders(result.Unverified, opts.Config)
		}
	}

	result.Verified = EnrichModelPricing(ctx, opts.Config, result.Verified)
	result.Unverified = EnrichModelPricing(ctx, opts.Config, result.Unverified)

	applyCatalogUIFields(result.Verified, opts.Config)
	applyCatalogUIFields(result.Unverified, opts.Config)

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
func discoverVendorModels(ctx context.Context, cfg AIConfig, useCache bool) ([]ModelInfo, []string) {
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

	// Bedrock vendor discovery. ListFoundationModels is a regional API, so
	// with additional included regions configured the listing is the union
	// across them (sequential — extra regions are 24h-cached after the first
	// walk), deduped by ID with the primary region's row winning.
	wg.Add(1)
	go func() {
		defer wg.Done()
		list := ListBedrockVendorModels
		if useCache {
			list = ListBedrockVendorModelsCached
		}
		var models []ModelInfo
		seen := map[string]bool{}
		var firstErr error
		var failedRegions []string
		succeeded := false
		for _, region := range ResolveBedrockRegions(cfg.Bedrock) {
			regionCfg := cfg.Bedrock
			regionCfg.RegionOverride = region
			regionModels, err := list(ctx, regionCfg)
			if err != nil {
				if firstErr == nil {
					firstErr = err
				}
				failedRegions = append(failedRegions, region)
				continue
			}
			succeeded = true
			for _, m := range regionModels {
				if !seen[m.ID] {
					seen[m.ID] = true
					models = append(models, m)
				}
			}
		}
		if !succeeded {
			addWarning("bedrock", firstErr)
			return
		}
		// A PARTIAL failure (some regions listed, some did not) must still
		// warn in the recognized "<source> discovery failed" shape: the
		// discover diff's GONE shield (FailedDiscoveryProviders) and the
		// seen-snapshot save key off these warnings, and a silently missing
		// region would otherwise report that region's whole catalog as GONE
		// and drop it from the seen state (review finding on the discover
		// command).
		if len(failedRegions) > 0 {
			addWarning("bedrock", fmt.Errorf("partial listing: region(s) %s failed: %v", strings.Join(failedRegions, ", "), firstErr))
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

	// Bedrock mantle-plane discovery: GET /v1/models per documented mantle
	// region (bedrock_mantle_models.go). The plane is bearer-token only and a
	// SigV4-only setup is legitimate, so a missing token skips silently; a
	// listing failure WITH a token is a real warning. First listing of an id
	// wins, so the primary-first region ordering pins each row's Region to
	// the user's primary region whenever that region serves the model.
	wg.Add(1)
	go func() {
		defer wg.Done()
		if resolveMantleBearerToken() == "" {
			slog.Debug("bedrock mantle discovery skipped: no bearer token (SigV4-only setup)")
			return
		}
		mlist := ListBedrockMantleModels
		if useCache {
			mlist = ListBedrockMantleModelsCached
		}
		var models []ModelInfo
		seen := map[string]bool{}
		var firstErr error
		var failedRegions []string
		succeeded := false
		for _, region := range mantleDiscoveryRegions(cfg.Bedrock) {
			regionModels, err := mlist(ctx, cfg.Bedrock, region)
			if err != nil {
				if firstErr == nil {
					firstErr = err
				}
				failedRegions = append(failedRegions, region)
				continue
			}
			succeeded = true
			for _, m := range regionModels {
				if !seen[m.ID] {
					seen[m.ID] = true
					models = append(models, m)
				}
			}
		}
		if !succeeded {
			addWarning("bedrock-mantle", firstErr)
			return
		}
		// Same partial-failure rule as the classic loop: per-region catalogs
		// genuinely differ on the mantle plane (grok-4.6 lists only in
		// us-west-2), so one silently missing region makes its exclusive
		// models look GONE.
		if len(failedRegions) > 0 {
			addWarning("bedrock-mantle", fmt.Errorf("partial listing: region(s) %s failed: %v", strings.Join(failedRegions, ", "), firstErr))
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
	return dedupeDiscoveredBedrock(all), warnings
}

// dedupeDiscoveredBedrock collapses exact-id collisions between the two
// Bedrock discovery planes: a model listed by BOTH the classic control plane
// and a mantle /v1/models listing keeps its classic (empty-strategy) row,
// because the classic listing carries richer metadata and the classic route
// needs no bearer token. Order-independent — the goroutines append in
// nondeterministic order, so "classic wins" is decided by InvokeStrategy,
// not arrival. Non-bedrock rows and distinct ids pass through untouched;
// dated variants (openai.gpt-5.5-2026-04-23) are distinct ids and survive.
func dedupeDiscoveredBedrock(models []ModelInfo) []ModelInfo {
	firstAt := make(map[string]int)
	out := make([]ModelInfo, 0, len(models))
	for _, m := range models {
		if m.Provider != "bedrock" {
			out = append(out, m)
			continue
		}
		if i, ok := firstAt[m.ID]; ok {
			if out[i].InvokeStrategy == StrategyBedrockMantleResponses && m.InvokeStrategy == "" {
				out[i] = m
			}
			continue
		}
		firstAt[m.ID] = len(out)
		out = append(out, m)
	}
	return out
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

func applyCatalogUIFields(models []ModelInfo, cfg AIConfig) {
	for i := range models {
		vendor := VendorOf(models[i].ID, models[i].Provider)
		models[i].Vendor = vendor.Vendor
		models[i].VendorDisplay = vendor.Display
		models[i].Family = vendor.Family
		models[i].VersionSortKey = VersionSortKey(models[i].ID)
		models[i].Compatible, models[i].CompatibilityReason = catalogCompatibility(models[i])
		// Working reads Compatible, so it must come after the line above.
		models[i].Working = catalogWorking(models[i], cfg)
	}
}

// catalogWorking decides membership in the working set: models this account
// has PROVEN it can invoke. A passing probe on record (TestedAt with no
// TestError) is the evidence; an explicit disable or a static incompatibility
// removes it. Tier is deliberately not consulted — a builtin verified entry
// only means 2nb has a harness for the model, which says nothing about
// whether AWS has entitled this account to it.
//
// Untested active embedding/generation models are members so a working-set
// picker is never empty on a freshly bound vault. A FAILED probe is not:
// working means "this account can invoke it", and access_denied on the
// active slot must not look like success. The GUI keeps actives selectable
// separately from the working flag.
func catalogWorking(m ModelInfo, cfg AIConfig) bool {
	if m.TestError != "" {
		return false
	}
	if m.Active || isActiveModel(m, cfg) {
		return true
	}
	if m.TestedAt == "" {
		return false
	}
	if m.Enabled != nil && !*m.Enabled {
		return false
	}
	return m.Compatible
}

func catalogCompatibility(m ModelInfo) (bool, string) {
	if m.Provider != "bedrock" {
		return true, ""
	}
	// Mantle-plane models bypass the classic-Bedrock allowlist entirely:
	// they invoke over the bedrock-mantle Responses API, which 2nb supports
	// for generation only. m.InvokeStrategy is already the resolved value
	// here (the merged catalog overlays user entries onto builtins before
	// applyCatalogUIFields runs).
	if m.InvokeStrategy == StrategyBedrockMantleResponses {
		if m.Type == "generation" {
			return true, ""
		}
		return false, "mantle models are generation-only in 2nb"
	}
	if ok, reason := bedrockModelSupported(m.ID, m.Type); !ok {
		return false, reason
	}
	return true, ""
}

// AdoptRoutingHints fills entry's routing metadata from `from` wherever entry
// is empty: InvokeStrategy, Endpoint, and ContextLen when unset, and Region
// only when the entry's (possibly just-filled) strategy is the mantle plane,
// because classic Region pins are owned by the probe's region persistence
// (persistProbedRegion) and the two owners must never fight over the field.
// Authored values always win: a non-empty field is never overwritten.
//
// Shared by two call sites so the rule cannot drift: the discovery merge
// graft in mergeDiscovered (a routing-EMPTY user row adopts a discovered
// row's hints, so a row whose routing was stripped by a pre-0.19.0 save
// clobber self-heals on the next `verify --discover` instead of shadowing
// its own cure — live incident 2026-08-21, xai.grok-4.6), and the CLI probe
// save path (adoptCandidateRouting).
func AdoptRoutingHints(entry *ModelInfo, from ModelInfo) {
	if entry.InvokeStrategy == "" && from.InvokeStrategy != "" {
		entry.InvokeStrategy = from.InvokeStrategy
	}
	if entry.Endpoint == "" && from.Endpoint != "" {
		entry.Endpoint = from.Endpoint
	}
	if entry.ContextLen == 0 && from.ContextLen != 0 {
		entry.ContextLen = from.ContextLen
	}
	if entry.Region == "" && from.Region != "" &&
		entry.InvokeStrategy == StrategyBedrockMantleResponses {
		entry.Region = from.Region
	}
}

// mergeDiscovered folds vendor-discovered rows into the model list: a row
// whose exact (provider, id) is absent from the merged catalog joins the
// returned unverified slice; a row that IS already merged grafts its routing
// hints onto the catalog row's empty fields via AdoptRoutingHints instead of
// being dropped silently. Authored routing beats the hint, but the ABSENCE
// of routing does not: dropping the hints let a strategy-stripped user row
// shadow its own cure, probing a mantle-only id over classic Converse until
// the row was manually removed. Mutates catalog rows in place (in-memory
// only; nothing is persisted until a probe save).
func mergeDiscovered(catalog []ModelInfo, discovered []ModelInfo) []ModelInfo {
	idx := make(map[string]int, len(catalog))
	for i, c := range catalog {
		idx[catalogKey(c.Provider, c.ID)] = i
	}
	var unverified []ModelInfo
	for _, m := range discovered {
		if i, ok := idx[catalogKey(m.Provider, m.ID)]; ok {
			AdoptRoutingHints(&catalog[i], m)
			continue
		}
		unverified = append(unverified, m)
	}
	return unverified
}
