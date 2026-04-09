package ai

import "testing"

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
		if m.Type != "embedding" && m.Type != "generation" {
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
