package ai

import (
	"testing"
	"time"
)

func TestProviderRPSDefault(t *testing.T) {
	tests := []struct {
		provider string
		want     float64
	}{
		{"bedrock", 10},
		{"openrouter", 5},
		{"ollama", 0},
		{"unknown", 0},
		{"", 0},
	}
	for _, tt := range tests {
		t.Run(tt.provider, func(t *testing.T) {
			got := ProviderRPSDefault(tt.provider)
			if got != tt.want {
				t.Errorf("ProviderRPSDefault(%q) = %v, want %v", tt.provider, got, tt.want)
			}
		})
	}
}

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

func TestThrottleDelay(t *testing.T) {
	tests := []struct {
		name string
		rps  float64
		want time.Duration
	}{
		{"zero is no throttle", 0, 0},
		{"negative is no throttle", -1, 0},
		{"10 rps is 100ms", 10, 100 * time.Millisecond},
		{"5 rps is 200ms", 5, 200 * time.Millisecond},
		{"1 rps is 1s", 1, time.Second},
		{"fractional rps", 2.5, 400 * time.Millisecond},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ThrottleDelay(tt.rps)
			if got != tt.want {
				t.Errorf("ThrottleDelay(%v) = %v, want %v", tt.rps, got, tt.want)
			}
		})
	}
}
