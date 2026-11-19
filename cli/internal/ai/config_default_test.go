package ai

import "testing"

// TestDefaultAIConfig_Gemini pins the opinionated default: Google Gemini
// with Gemini 4 Hydrogen + Gemini Embedding 2, and Ollama/OpenRouter opt-in.
func TestDefaultAIConfig_Gemini(t *testing.T) {
	c := DefaultAIConfig()

	if c.Provider != "gemini" {
		t.Errorf("provider = %q, want gemini", c.Provider)
	}
	if c.GenerationModel != "gemini-4-hydrogen" {
		t.Errorf("generation = %q, want gemini-4-hydrogen", c.GenerationModel)
	}
	if c.EmbeddingModel != "gemini-embedding-2" {
		t.Errorf("embedding = %q, want gemini-embedding-2", c.EmbeddingModel)
	}
	if c.Dimensions != 768 {
		t.Errorf("dimensions = %d, want 768", c.Dimensions)
	}
	if c.Gemini.Disabled {
		t.Error("Gemini should be enabled by default")
	}
	if !c.Ollama.Disabled || !c.OpenRouter.Disabled {
		t.Errorf("Ollama and OpenRouter should be opt-in (disabled) by default; ollama=%v openrouter=%v",
			c.Ollama.Disabled, c.OpenRouter.Disabled)
	}
}
