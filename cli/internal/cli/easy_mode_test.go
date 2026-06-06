package cli

import (
	"testing"

	"github.com/apresai/2ndbrain/internal/ai"
)

// TestEasyModeDefaults_BedrockMatchesDefault guards the bug where Bedrock
// easy-mode generation was amazon.nova-micro-v1:0 instead of Haiku 4.5. Easy
// mode must mirror DefaultAIConfig() exactly (single source of truth).
func TestEasyModeDefaults_BedrockMatchesDefault(t *testing.T) {
	embed, gen, dims := easyModeDefaults("bedrock")
	d := ai.DefaultAIConfig()

	if gen != d.GenerationModel {
		t.Errorf("bedrock easy-mode generation = %q, want %q (Haiku 4.5, not nova-micro)", gen, d.GenerationModel)
	}
	if embed != d.EmbeddingModel {
		t.Errorf("bedrock easy-mode embedding = %q, want %q (Nova-2)", embed, d.EmbeddingModel)
	}
	if dims != d.Dimensions {
		t.Errorf("bedrock easy-mode dims = %d, want %d", dims, d.Dimensions)
	}
}
