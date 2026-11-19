package cli

import (
	"testing"

	"github.com/apresai/2ndbrain/internal/ai"
)

// TestEasyModeDefaults_GeminiMatchesDefault ensures Gemini easy mode mirrors
// DefaultAIConfig() exactly (single source of truth).
func TestEasyModeDefaults_GeminiMatchesDefault(t *testing.T) {
	embed, gen, dims := easyModeDefaults("gemini")
	d := ai.DefaultAIConfig()

	if gen != d.GenerationModel {
		t.Errorf("gemini easy-mode generation = %q, want %q", gen, d.GenerationModel)
	}
	if embed != d.EmbeddingModel {
		t.Errorf("gemini easy-mode embedding = %q, want %q", embed, d.EmbeddingModel)
	}
	if dims != d.Dimensions {
		t.Errorf("gemini easy-mode dims = %d, want %d", dims, d.Dimensions)
	}
}

func TestEasyModeDefaults_BedrockMatchesDefault(t *testing.T) {
	embed, gen, dims := easyModeDefaults("bedrock")

	if gen != "us.anthropic.claude-haiku-4-5-20251001-v1:0" {
		t.Errorf("bedrock easy-mode generation = %q, want Haiku 4.5", gen)
	}
	if embed != "amazon.nova-2-multimodal-embeddings-v1:0" {
		t.Errorf("bedrock easy-mode embedding = %q, want Nova-2", embed)
	}
	if dims != 1024 {
		t.Errorf("bedrock easy-mode dims = %d, want 1024", dims)
	}
}
