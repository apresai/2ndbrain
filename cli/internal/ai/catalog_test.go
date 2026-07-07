package ai

import (
	"strings"
	"testing"
)

func TestBuiltinCatalog_NoDuplicates(t *testing.T) {
	catalog := BuiltinCatalog()
	seen := map[string]bool{}
	for _, m := range catalog {
		key := m.Provider + "\x00" + m.ID
		if seen[key] {
			t.Errorf("duplicate model: provider=%s id=%s", m.Provider, m.ID)
		}
		seen[key] = true
	}
}

func TestBuiltinCatalog_RequiredFields(t *testing.T) {
	for _, m := range BuiltinCatalog() {
		if m.ID == "" {
			t.Error("model with empty ID")
		}
		if m.Name == "" {
			t.Errorf("model %s has empty Name", m.ID)
		}
		if m.Provider == "" {
			t.Errorf("model %s has empty Provider", m.ID)
		}
		if m.Type != "embedding" && m.Type != "generation" && m.Type != "rerank" {
			t.Errorf("model %s has invalid Type: %q", m.ID, m.Type)
		}
		if m.Tier != TierVerified {
			t.Errorf("model %s in catalog should be TierVerified, got %q", m.ID, m.Tier)
		}
		if m.ConfigHint == "" {
			t.Errorf("model %s has empty ConfigHint", m.ID)
		}
	}
}

func TestBuiltinCatalog_EmbeddingDimensions(t *testing.T) {
	for _, m := range BuiltinCatalog() {
		if m.Type == "embedding" && m.Dimensions == 0 {
			t.Errorf("embedding model %s has zero dimensions", m.ID)
		}
	}
}

func TestBuiltinCatalog_AllProvidersPresent(t *testing.T) {
	providers := map[string]bool{}
	for _, m := range BuiltinCatalog() {
		providers[m.Provider] = true
	}
	for _, p := range []string{"bedrock", "openrouter", "ollama"} {
		if !providers[p] {
			t.Errorf("no models for provider %s in catalog", p)
		}
	}
}

func TestCatalogIndex(t *testing.T) {
	catalog := BuiltinCatalog()
	idx := catalogIndex(catalog)
	if len(idx) != len(catalog) {
		t.Errorf("catalogIndex size %d != catalog size %d (duplicates?)", len(idx), len(catalog))
	}
	// Spot check one entry exists.
	if !idx["bedrock\x00amazon.nova-2-multimodal-embeddings-v1:0"] {
		t.Error("expected bedrock nova embeddings in index")
	}
}

// TestBuiltinCatalog_RerankModels asserts the reranker models surface in the
// catalog (so `models list` / the GUI can list + select them), with the rerank
// type and a config hint that targets ai.rerank.*.
func TestBuiltinCatalog_RerankModels(t *testing.T) {
	var cohere, bge bool
	for _, m := range BuiltinCatalog() {
		if m.Type != "rerank" {
			continue
		}
		switch {
		case m.ID == "cohere.rerank-v3-5:0" && m.Provider == "bedrock":
			cohere = true
		case m.ID == "bge-reranker-v2-m3" && m.Provider == llamaProviderName:
			bge = true
		}
	}
	if !cohere {
		t.Error("BuiltinCatalog is missing the Cohere rerank model (cohere.rerank-v3-5:0 / bedrock)")
	}
	if !bge {
		t.Error("BuiltinCatalog is missing the local bge reranker (bge-reranker-v2-m3 / llama-local)")
	}
}

func TestConfigHint_Rerank(t *testing.T) {
	h := configHint("bedrock", "rerank", "cohere.rerank-v3-5:0")
	if !strings.Contains(h, "ai.rerank.model") || !strings.Contains(h, "ai.rerank.enabled") {
		t.Errorf("rerank config hint = %q, want it to set ai.rerank.model + ai.rerank.enabled", h)
	}
}

// TestBuiltinCatalog_RecommendedCoverage verifies curation is usable: every
// provider has at least one recommended embedding and one recommended
// generation model, so a "recommended only" view is never empty.
func TestBuiltinCatalog_RecommendedCoverage(t *testing.T) {
	type key struct{ provider, typ string }
	got := map[key]bool{}
	for _, m := range BuiltinCatalog() {
		if m.Recommended {
			got[key{m.Provider, m.Type}] = true
		}
	}
	for _, p := range KnownProviders {
		for _, typ := range []string{"embedding", "generation"} {
			if !got[key{p, typ}] {
				t.Errorf("provider %s has no recommended %s model", p, typ)
			}
		}
	}
}

// TestBuiltinCatalog_AnthropicLine pins the curated Anthropic policy: the
// tested default plus current Sonnet and Opus versions, with Opus 4.7 and
// Fable 5 intentionally absent (superseded / premium+different API semantics;
// both stay reachable via discovery).
func TestBuiltinCatalog_AnthropicLine(t *testing.T) {
	want := map[string]bool{
		"us.anthropic.claude-haiku-4-5-20251001-v1:0": true,
		"us.anthropic.claude-sonnet-4-6":              true,
		"us.anthropic.claude-sonnet-5":                true,
		"us.anthropic.claude-opus-4-6-v1":             false, // present but not recommended
		"us.anthropic.claude-opus-4-8":                true,
	}
	seen := map[string]bool{}
	for _, m := range BuiltinCatalog() {
		// The 4.7/Fable exclusion applies across ALL providers: the OpenRouter
		// Anthropic line mirrors the Bedrock policy.
		if strings.Contains(m.ID, "opus-4-7") || strings.Contains(strings.ToLower(m.ID), "fable") {
			t.Errorf("%s must not be in the curated builtin catalog", m.ID)
		}
		if m.Provider != "bedrock" || m.Type != "generation" {
			continue
		}
		if strings.Contains(m.ID, "anthropic") {
			seen[m.ID] = true
			wantRec, ok := want[m.ID]
			if !ok {
				t.Errorf("unexpected builtin Anthropic entry %s (policy: haiku-4-5, sonnet-4-6, sonnet-5, opus-4-6, opus-4-8 only)", m.ID)
				continue
			}
			if m.Recommended != wantRec {
				t.Errorf("%s recommended = %v, want %v", m.ID, m.Recommended, wantRec)
			}
		}
	}
	for id := range want {
		if !seen[id] {
			t.Errorf("builtin catalog missing Anthropic entry %s", id)
		}
	}
}
