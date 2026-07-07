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
	return resolveCatalogString(provider, modelID, vaultRoot, func(m ModelInfo) string {
		return m.InvokeStrategy
	})
}

// ResolveModelRegion returns the per-model AWS region pin for (provider,
// modelID), resolved through the same user-catalog-over-builtin chain as
// ResolveInvokeStrategy. Returns "" when no catalog entry pins a region,
// meaning "use the provider default (ai.bedrock.region)".
func ResolveModelRegion(provider, modelID, vaultRoot string) string {
	return resolveCatalogString(provider, modelID, vaultRoot, func(m ModelInfo) string {
		return m.Region
	})
}

// ResolveModelEndpoint returns the per-model endpoint URL override for
// (provider, modelID), resolved through the same user-catalog-over-builtin
// chain as ResolveInvokeStrategy. Returns "" when no catalog entry pins an
// endpoint, meaning "derive the endpoint from the model's Region".
func ResolveModelEndpoint(provider, modelID, vaultRoot string) string {
	return resolveCatalogString(provider, modelID, vaultRoot, func(m ModelInfo) string {
		return m.Endpoint
	})
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
