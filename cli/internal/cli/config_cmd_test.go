package cli

import (
	"strings"
	"testing"

	"github.com/apresai/2ndbrain/internal/ai"
)

func TestSetConfigValue_SimilarityThreshold(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		wantErr string // empty = success
		wantVal float64
	}{
		{"valid mid-range", "0.65", "", 0.65},
		{"valid zero (reset)", "0", "", 0},
		{"valid one", "1", "", 1},
		{"out of range high", "1.5", "between 0 and 1", 0},
		{"out of range negative", "-0.1", "between 0 and 1", 0},
		{"non-numeric", "abc", "must be a float", 0},
		{"empty", "", "must be a float", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := ai.AIConfig{}
			err := setConfigValue(&cfg, "ai.similarity_threshold", tt.value)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if cfg.SimilarityThreshold != tt.wantVal {
					t.Errorf("cfg.SimilarityThreshold = %v, want %v", cfg.SimilarityThreshold, tt.wantVal)
				}
			} else {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("error = %q, want to contain %q", err.Error(), tt.wantErr)
				}
			}
		})
	}
}

func TestGetConfigValue_SimilarityThreshold(t *testing.T) {
	tests := []struct {
		name string
		val  float64
		want string
	}{
		{"zero prints as 0", 0, "0"},
		{"mid-range", 0.65, "0.65"},
		{"one", 1, "1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := ai.AIConfig{SimilarityThreshold: tt.val}
			got, err := getConfigValue(cfg, "ai.similarity_threshold")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("getConfigValue(threshold=%v) = %q, want %q", tt.val, got, tt.want)
			}
		})
	}
}

func TestSettableConfigKeys_includesThreshold(t *testing.T) {
	var found bool
	for _, k := range settableConfigKeys {
		if k == "ai.similarity_threshold" {
			found = true
			break
		}
	}
	if !found {
		t.Error("ai.similarity_threshold missing from settableConfigKeys — shell completion + error messages will be wrong")
	}
}
