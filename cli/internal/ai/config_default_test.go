package ai

import "testing"

// TestDefaultAIConfig_BedrockHaikuNova pins the opinionated default: AWS Bedrock
// with Claude Haiku 4.5 + Nova-2 embeddings, and Ollama/OpenRouter opt-in.
func TestDefaultAIConfig_BedrockHaikuNova(t *testing.T) {
	c := DefaultAIConfig()

	if c.Provider != "bedrock" {
		t.Errorf("provider = %q, want bedrock", c.Provider)
	}
	if c.GenerationModel != "us.anthropic.claude-haiku-4-5-20251001-v1:0" {
		t.Errorf("generation = %q, want Haiku 4.5", c.GenerationModel)
	}
	if c.EmbeddingModel != "amazon.nova-2-multimodal-embeddings-v1:0" {
		t.Errorf("embedding = %q, want Nova-2", c.EmbeddingModel)
	}
	if c.Dimensions != 1024 {
		t.Errorf("dimensions = %d, want 1024", c.Dimensions)
	}
	if c.Bedrock.Disabled {
		t.Error("Bedrock should be enabled by default")
	}
	if !c.Ollama.Disabled || !c.OpenRouter.Disabled {
		t.Errorf("Ollama and OpenRouter should be opt-in (disabled) by default; ollama=%v openrouter=%v",
			c.Ollama.Disabled, c.OpenRouter.Disabled)
	}
}
