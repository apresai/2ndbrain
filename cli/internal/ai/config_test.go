package ai

import "testing"

func TestDefaultAIConfig(t *testing.T) {
	cfg := DefaultAIConfig()

	if cfg.Provider != "bedrock" {
		t.Errorf("default provider = %q, want bedrock", cfg.Provider)
	}
	if cfg.Dimensions != 1024 {
		t.Errorf("default dimensions = %d, want 1024", cfg.Dimensions)
	}
	if cfg.Bedrock.Region != "us-east-1" {
		t.Errorf("default region = %q, want us-east-1", cfg.Bedrock.Region)
	}
	if cfg.Ollama.Endpoint != "http://localhost:11434" {
		t.Errorf("default ollama endpoint = %q", cfg.Ollama.Endpoint)
	}
	if cfg.OpenRouter.APIKeyEnv != "OPENROUTER_API_KEY" {
		t.Errorf("default openrouter key env = %q", cfg.OpenRouter.APIKeyEnv)
	}
	if cfg.EmbeddingModel == "" {
		t.Error("default embedding model is empty")
	}
	if cfg.GenerationModel == "" {
		t.Error("default generation model is empty")
	}
}

func TestEnvVarName(t *testing.T) {
	tests := []struct {
		provider string
		want     string
	}{
		{"openrouter", "OPENROUTER_API_KEY"},
		{"bedrock", ""},
		{"custom", "CUSTOM_API_KEY"},
	}
	for _, tt := range tests {
		got := envVarName(tt.provider)
		if got != tt.want {
			t.Errorf("envVarName(%q) = %q, want %q", tt.provider, got, tt.want)
		}
	}
}
