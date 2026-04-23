package cli

import (
	"testing"

	"github.com/apresai/2ndbrain/internal/ai"
)

func TestLookupModelInfo(t *testing.T) {
	catalog := []ai.ModelInfo{
		{Provider: "bedrock", ID: "amazon.nova-micro-v1:0", Type: "generation", PriceIn: 0.035},
		{Provider: "openrouter", ID: "google/gemma-2-9b-it:free", Type: "generation", PriceSource: "builtin"},
		{Provider: "bedrock", ID: "amazon.titan-embed-text-v2:0", Type: "embedding", Dimensions: 1024},
	}

	t.Run("found", func(t *testing.T) {
		m, ok := lookupModelInfo(catalog, "bedrock", "amazon.nova-micro-v1:0")
		if !ok {
			t.Fatal("expected found=true")
		}
		if m.PriceIn != 0.035 {
			t.Errorf("PriceIn = %g, want 0.035", m.PriceIn)
		}
	})

	t.Run("wrong provider", func(t *testing.T) {
		_, ok := lookupModelInfo(catalog, "openrouter", "amazon.nova-micro-v1:0")
		if ok {
			t.Fatal("expected found=false for wrong provider")
		}
	})

	t.Run("wrong id", func(t *testing.T) {
		_, ok := lookupModelInfo(catalog, "bedrock", "does-not-exist")
		if ok {
			t.Fatal("expected found=false for missing id")
		}
	})

	t.Run("nil slice", func(t *testing.T) {
		m, ok := lookupModelInfo(nil, "bedrock", "amazon.nova-micro-v1:0")
		if ok {
			t.Fatal("expected found=false for nil slice")
		}
		if m.ID != "amazon.nova-micro-v1:0" {
			t.Errorf("zero-value ID = %q, want amazon.nova-micro-v1:0", m.ID)
		}
		if m.Provider != "bedrock" {
			t.Errorf("zero-value Provider = %q, want bedrock", m.Provider)
		}
	})

	t.Run("embedding with dimensions", func(t *testing.T) {
		m, ok := lookupModelInfo(catalog, "bedrock", "amazon.titan-embed-text-v2:0")
		if !ok {
			t.Fatal("expected found=true")
		}
		if m.Dimensions != 1024 {
			t.Errorf("Dimensions = %d, want 1024", m.Dimensions)
		}
	})
}
