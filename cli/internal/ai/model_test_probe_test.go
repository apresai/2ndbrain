package ai

import "testing"

func TestInferProvider(t *testing.T) {
	tests := []struct {
		modelID  string
		expected string
	}{
		{"us.anthropic.claude-haiku-4-5-20251001-v1:0", "bedrock"},
		{"amazon.nova-2-multimodal-embeddings-v1:0", "bedrock"},
		{"eu.anthropic.claude-sonnet-4-20250514-v1:0", "bedrock"},
		{"anthropic/claude-haiku-4-5", "openrouter"},
		{"google/gemma-4-31b-it:free", "openrouter"},
		{"openai/gpt-4o", "openrouter"},
		{"gemma3:4b", "ollama"},
		{"nomic-embed-text", "ollama"},
		{"qwen2.5:0.5b", "ollama"},
	}
	for _, tt := range tests {
		t.Run(tt.modelID, func(t *testing.T) {
			got := InferProvider(tt.modelID)
			if got != tt.expected {
				t.Errorf("InferProvider(%q) = %q, want %q", tt.modelID, got, tt.expected)
			}
		})
	}
}

func TestInferModelType(t *testing.T) {
	tests := []struct {
		modelID  string
		expected string
	}{
		{"amazon.nova-2-multimodal-embeddings-v1:0", "embedding"},
		{"nomic-embed-text", "embedding"},
		{"nvidia/llama-nemotron-embed-vl-1b-v2:free", "embedding"},
		{"us.anthropic.claude-haiku-4-5-20251001-v1:0", "generation"},
		{"google/gemma-4-31b-it:free", "generation"},
		{"gemma3:4b", "generation"},
	}
	for _, tt := range tests {
		t.Run(tt.modelID, func(t *testing.T) {
			got := InferModelType(tt.modelID)
			if got != tt.expected {
				t.Errorf("InferModelType(%q) = %q, want %q", tt.modelID, got, tt.expected)
			}
		})
	}
}
