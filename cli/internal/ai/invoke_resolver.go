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
	if modelID == "" {
		return ""
	}
	normalized := strings.ToLower(inferenceProfileBaseID(modelID))
	if s := findStrategy(BuiltinCatalog(), provider, modelID, normalized); s != "" {
		// Builtin is the base layer — overlay with user catalog so a user
		// override can correct a builtin that's wrong or newly-supported.
		if user := findStrategy(LoadUserCatalog(vaultRoot), provider, modelID, normalized); user != "" {
			return user
		}
		return s
	}
	return findStrategy(LoadUserCatalog(vaultRoot), provider, modelID, normalized)
}

func findStrategy(catalog []ModelInfo, provider, modelID, normalizedLower string) string {
	for _, m := range catalog {
		if m.Provider != provider {
			continue
		}
		if m.ID == modelID {
			return m.InvokeStrategy
		}
		// Match inference-profile-stripped form so "us.anthropic.claude..."
		// resolves against the base "anthropic.claude..." entry.
		if strings.ToLower(inferenceProfileBaseID(m.ID)) == normalizedLower {
			return m.InvokeStrategy
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
