package ai

import "strings"

// ResolveInvokeStrategy returns the declared invoke strategy for (provider,
// modelID) by consulting the builtin catalog and — when a vault root is
// available — the user catalog layered on top.
//
// Returns "" when no catalog entry declares a strategy. Callers should
// fall back to their existing per-model-ID detection in that case, which
// preserves behavior for catalogs that predate the strategy field.
//
// Phase-2 callers typically invoke this once at provider construction and
// cache the result on the client struct. Calling it per request is safe
// but wasteful: BuiltinCatalog is a static slice and LoadUserCatalog
// re-reads YAML files from disk.
func ResolveInvokeStrategy(provider, modelID, vaultRoot string) string {
	field := func(m ModelInfo) string { return m.InvokeStrategy }
	// Exact id first, user over builtin.
	if s := findCatalogStringExact(LoadUserCatalog(vaultRoot), provider, modelID, field); s != "" {
		return s
	}
	if s := findCatalogStringExact(BuiltinCatalog(), provider, modelID, field); s != "" {
		return s
	}
	// The profile-stripped base fallback keeps a us./eu./global. variant on
	// its base entry's dialect — EXCEPT the mantle strategy: cross-region
	// profiles exist only on the CLASSIC plane, so a profile-prefixed id can
	// never be mantle, and inheriting it dispatches Converse traffic to a
	// mantle 404 (observed live 2026-08-20 when the classic us.xai.grok-4.6
	// base-matched a mantle xai.grok-4.6 user entry).
	s := resolveCatalogString(provider, modelID, vaultRoot, field)
	if s == StrategyBedrockMantleResponses &&
		!strings.EqualFold(inferenceProfileBaseID(modelID), modelID) {
		return ""
	}
	return s
}

// ResolveModelRegion returns the per-model AWS region pin for (provider,
// modelID), user catalog over builtin. Returns "" when no catalog entry pins
// a region, meaning "use the provider default (ai.bedrock.region)".
//
// EXACT id matching only — deliberately not resolveCatalogString's
// profile-stripped base fallback. Region pins are always authored against
// exact ids (the rerank and mantle builtins, persisted verify passes), and
// base matching let one PLANE's pin bleed onto the other: the classic
// `us.xai.grok-4.6` Converse profile base-matched a mantle `xai.grok-4.6`
// user entry and inherited its us-west-2 mantle pin, routing Converse to a
// region where the profile 404s (observed live 2026-08-20).
func ResolveModelRegion(provider, modelID, vaultRoot string) string {
	field := func(m ModelInfo) string { return m.Region }
	if user := findCatalogStringExact(LoadUserCatalog(vaultRoot), provider, modelID, field); user != "" {
		return user
	}
	return findCatalogStringExact(BuiltinCatalog(), provider, modelID, field)
}

// findCatalogStringExact is findCatalogString without the
// inference-profile-stripped fallback: only an entry whose ID equals modelID
// matches.
func findCatalogStringExact(catalog []ModelInfo, provider, modelID string, field func(ModelInfo) string) string {
	for _, m := range catalog {
		if m.Provider == provider && m.ID == modelID {
			if s := field(m); s != "" {
				return s
			}
		}
	}
	return ""
}

// EffectiveBedrockRegion returns the region a Bedrock client for modelID
// should call: an in-memory RegionOverride wins (multi-region verify sets it
// per attempt), then a catalog Region pin on the model (the rerankRegion
// pattern generalized — a model verified in a non-primary region routes
// there), then the configured region via ResolveBedrockConfig.
func EffectiveBedrockRegion(cfg BedrockConfig, modelID, vaultRoot string) string {
	if cfg.RegionOverride != "" {
		return cfg.RegionOverride
	}
	if pinned := ResolveModelRegion("bedrock", modelID, vaultRoot); pinned != "" {
		return pinned
	}
	return ResolveBedrockConfig(cfg).Region
}

// carryVaultRegionPin copies a vault-scoped catalog Region pin for modelID
// into cfg.RegionOverride when no override is already set. It exists because
// the classic constructors resolve pins with vaultRoot "" (builtin + global
// catalog only); callers that DO hold a vault root use this to hand the
// vault-scoped pin down without a signature change.
func carryVaultRegionPin(cfg *BedrockConfig, modelID, vaultRoot string) {
	if cfg.RegionOverride == "" {
		if pinned := ResolveModelRegion("bedrock", modelID, vaultRoot); pinned != "" {
			cfg.RegionOverride = pinned
		}
	}
}

// ResolveModelEndpoint returns the per-model endpoint URL override for
// (provider, modelID), user catalog over builtin. Returns "" when no catalog
// entry pins an endpoint, meaning "derive the endpoint from the model's
// Region".
//
// EXACT id matching only, like ResolveModelRegion and for the same reason:
// endpoint pins are authored against exact ids (mantle user entries), and
// the profile-stripped base fallback would let a mantle entry's endpoint
// bleed onto the classic us./global. profile forms — the same cross-plane
// class of bug fixed for Region and InvokeStrategy. Not reachable as a live
// defect (the only caller sits behind the mantle strategy gate in
// bedrock_mantle.go, and builtins declare no endpoints); hardened so the
// three routing resolvers share one matching rule.
func ResolveModelEndpoint(provider, modelID, vaultRoot string) string {
	field := func(m ModelInfo) string { return m.Endpoint }
	if user := findCatalogStringExact(LoadUserCatalog(vaultRoot), provider, modelID, field); user != "" {
		return user
	}
	return findCatalogStringExact(BuiltinCatalog(), provider, modelID, field)
}

// resolveCatalogString resolves one string-valued catalog field for
// (provider, modelID): builtin is the base layer, overlaid with the user
// catalog so a user override can correct a builtin that's wrong or
// newly-supported. Returns "" when no catalog entry declares the field.
func resolveCatalogString(provider, modelID, vaultRoot string, field func(ModelInfo) string) string {
	if modelID == "" {
		return ""
	}
	normalized := strings.ToLower(inferenceProfileBaseID(modelID))
	if s := findCatalogString(BuiltinCatalog(), provider, modelID, normalized, field); s != "" {
		if user := findCatalogString(LoadUserCatalog(vaultRoot), provider, modelID, normalized, field); user != "" {
			return user
		}
		return s
	}
	return findCatalogString(LoadUserCatalog(vaultRoot), provider, modelID, normalized, field)
}

func findCatalogString(catalog []ModelInfo, provider, modelID, normalizedLower string, field func(ModelInfo) string) string {
	for _, m := range catalog {
		if m.Provider != provider {
			continue
		}
		if m.ID == modelID {
			return field(m)
		}
		// Match inference-profile-stripped form so "us.anthropic.claude..."
		// resolves against the base "anthropic.claude..." entry.
		if strings.ToLower(inferenceProfileBaseID(m.ID)) == normalizedLower {
			return field(m)
		}
	}
	return ""
}

// bedrockEmbedFormatFromStrategy maps an InvokeStrategy constant to the
// internal Bedrock embed-format enum. The boolean return reports whether
// the strategy is recognized as a Bedrock embedding strategy (so callers
// can distinguish "use this format" from "strategy is unrelated, fall
// back to detection").
func bedrockEmbedFormatFromStrategy(strategy string) (bedrockEmbedFmt, bool) {
	switch strategy {
	case StrategyBedrockInvokeNovaEmbed:
		return fmtNova, true
	case StrategyBedrockInvokeTitanEmbed:
		// Titan v1 vs v2 is a runtime choice made by the existing detection
		// logic based on the exact model ID. The strategy constant covers
		// both — callers should still consult detectEmbedFormat for v1/v2
		// disambiguation. Return v2 as the modern default; v1 needs the
		// "amazon.titan-embed-text-v1" exact-match path in detection.
		return fmtTitanV2, true
	case StrategyBedrockInvokeCohereEmbed:
		return fmtCohere, true
	case StrategyBedrockInvokeMarengo27:
		return fmtTwelveLabs27, true
	case StrategyBedrockInvokeMarengo30:
		return fmtTwelveLabs30, true
	}
	return 0, false
}
