package ai

import "testing"

func TestProviderEmbedConcurrencyDefault(t *testing.T) {
	tests := map[string]int{"bedrock": 4, "openrouter": 3, "ollama": 2, "unknown": 4, "": 4}
	for provider, want := range tests {
		if got := ProviderEmbedConcurrencyDefault(provider); got != want {
			t.Errorf("ProviderEmbedConcurrencyDefault(%q) = %d, want %d", provider, got, want)
		}
	}
}

func TestResolveEmbedConcurrency(t *testing.T) {
	// Unset (0) → the per-provider default.
	if got := (AIConfig{}).ResolveEmbedConcurrency("bedrock"); got != 4 {
		t.Errorf("unset bedrock = %d, want 4 (default)", got)
	}
	// A configured positive value wins.
	if got := (AIConfig{EmbedConcurrency: 12}).ResolveEmbedConcurrency("bedrock"); got != 12 {
		t.Errorf("configured = %d, want 12", got)
	}
	// Negative is treated as unset → default; the result is always >= 1.
	if got := (AIConfig{EmbedConcurrency: -5}).ResolveEmbedConcurrency("ollama"); got != 2 {
		t.Errorf("negative ollama = %d, want 2 (default)", got)
	}
}
