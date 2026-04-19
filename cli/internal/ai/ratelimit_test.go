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
