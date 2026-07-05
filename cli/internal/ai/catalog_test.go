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
