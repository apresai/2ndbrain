package cli

import (
	"testing"

	"github.com/apresai/2ndbrain/internal/ai"
)

// TestSetConfigValue_DimensionValidation locks the Matryoshka dimension guard:
// `config set ai.dimensions` must refuse a width the model can't emit (which the
// provider would reject at embed time, silently breaking search) while accepting
// the declared widths and leaving unconstrained models alone.
func TestSetConfigValue_DimensionValidation(t *testing.T) {
	cfg := ai.AIConfig{Provider: "bedrock", EmbeddingModel: "amazon.nova-2-multimodal-embeddings-v1:0"}

	for _, d := range []string{"256", "384", "1024", "3072"} {
		if err := setConfigValue(&cfg, "ai.dimensions", d); err != nil {
			t.Errorf("setConfigValue(ai.dimensions, %s) = %v, want nil (supported width)", d, err)
		}
		if cfg.Dimensions == 0 {
			t.Errorf("dimensions not applied for %s", d)
		}
	}

	// An unsupported width is refused, and the prior value is left intact.
	cfg.Dimensions = 1024
	if err := setConfigValue(&cfg, "ai.dimensions", "500"); err == nil {
		t.Error("setConfigValue(ai.dimensions, 500) = nil, want error (unsupported)")
	}
	if cfg.Dimensions != 1024 {
		t.Errorf("rejected value mutated config: dimensions = %d, want 1024", cfg.Dimensions)
	}

	// Non-positive is refused.
	if err := setConfigValue(&cfg, "ai.dimensions", "0"); err == nil {
		t.Error("setConfigValue(ai.dimensions, 0) = nil, want error")
	}

	// A model that declares no Matryoshka set is unconstrained.
	cfg2 := ai.AIConfig{Provider: "bedrock", EmbeddingModel: "amazon.titan-embed-text-v2:0"}
	if err := setConfigValue(&cfg2, "ai.dimensions", "512"); err != nil {
		t.Errorf("setConfigValue(ai.dimensions, 512) for unconstrained model = %v, want nil", err)
	}
}
