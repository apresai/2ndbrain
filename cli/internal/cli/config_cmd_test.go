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

func TestSetConfigValue_Provider(t *testing.T) {
	// A valid provider sets the value and clears that provider's disabled flag,
	// so the CLI and GUI agree the active provider is "on".
	cfg := ai.DefaultAIConfig() // ollama ships disabled
	if !cfg.Ollama.Disabled {
		t.Fatal("precondition: default ollama should ship disabled")
	}
	if err := setConfigValue(&cfg, "ai.provider", "ollama"); err != nil {
		t.Fatalf("setConfigValue(ai.provider, ollama): %v", err)
	}
	if cfg.Provider != "ollama" {
		t.Errorf("cfg.Provider = %q, want ollama", cfg.Provider)
	}
	if cfg.Ollama.Disabled {
		t.Error("activating ollama did not clear ai.ollama.disabled")
	}

	// An unknown provider (typo) is rejected with the valid list, not saved.
	err := setConfigValue(&cfg, "ai.provider", "bedrok")
	if err == nil {
		t.Fatal("setConfigValue(ai.provider, bedrok) = nil, want error")
	}
	if !strings.Contains(err.Error(), "valid providers") {
		t.Errorf("error = %q, want to mention valid providers", err.Error())
	}
}

func TestConfigSetEmbeddingModel_SyncsDimensions(t *testing.T) {
	_, root := newContractVault(t)

	// Put the vault on a non-default (but valid Matryoshka) dimension — the
	// state a model switch that failed to sync dims would leave behind. (256 is
	// a supported Nova-2 width; an unsupported value is now refused at set time.)
	if _, err := runCLIArgs(t, root, "config", "set", "ai.dimensions", "256"); err != nil {
		t.Fatalf("config set ai.dimensions 256: %v", err)
	}

	// Re-selecting the catalog's default Nova-2 (1024-dim) embedding model must
	// resync ai.dimensions to 1024. Regression guard: deleting the dims-sync
	// block in runConfigSet leaves the dimension stuck at 256 and this fails.
	nova := ai.DefaultAIConfig().EmbeddingModel
	if _, err := runCLIArgs(t, root, "config", "set", "ai.embedding_model", nova); err != nil {
		t.Fatalf("config set ai.embedding_model: %v", err)
	}

	out, err := runCLIArgs(t, root, "config", "get", "ai.dimensions")
	if err != nil {
		t.Fatalf("config get ai.dimensions: %v", err)
	}
	if got := strings.TrimSpace(string(out)); got != "1024" {
		t.Errorf("ai.dimensions after embedding-model switch = %q, want 1024 (dims-sync did not run)", got)
	}
}

func TestConfigSetProvider_WarnsButSavesAndRejectsUnknown(t *testing.T) {
	_, root := newContractVault(t)

	// Switching ai.provider to openrouter orphans the (bedrock) embedding model,
	// so Validate() warns — but the write must still SUCCEED (warn, don't
	// refuse) so a step-by-step reconfigure isn't blocked midway.
	if _, err := runCLIArgs(t, root, "config", "set", "ai.provider", "openrouter"); err != nil {
		t.Fatalf("config set ai.provider openrouter must warn, not error: %v", err)
	}
	out, err := runCLIArgs(t, root, "config", "get", "ai.provider")
	if err != nil {
		t.Fatalf("config get ai.provider: %v", err)
	}
	if got := strings.TrimSpace(string(out)); got != "openrouter" {
		t.Errorf("ai.provider = %q, want openrouter (a warning must not block the save)", got)
	}

	// An unknown provider is rejected end-to-end.
	if _, err := runCLIArgs(t, root, "config", "set", "ai.provider", "bedrok"); err == nil {
		t.Error("config set ai.provider bedrok should error (unknown provider)")
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

// dummyConfigValue returns a value that setConfigValue will accept for the
// given key. Boolean keys want "true", the threshold wants a 0..1 float,
// dimensions wants an integer, and the rest are free-form strings (the
// provider key needs a real provider name to pass validation).
func dummyConfigValue(key string) string {
	switch key {
	case "ai.provider":
		return "bedrock"
	case "ai.dimensions":
		return "1024"
	case "ai.similarity_threshold":
		return "0.5"
	case "ai.bm25_weight", "ai.vector_weight":
		return "1.5"
	}
	if strings.HasSuffix(key, ".disabled") {
		return "true"
	}
	return "x"
}

// TestConfigGetSetKeyParity asserts that every key in the source-of-truth
// settableConfigKeys list is accepted by BOTH getConfigValue and
// setConfigValue. A key readable but not writable (or vice versa) is a silent
// asymmetry: completion offers it, but one of get/set rejects it with "unknown
// config key". This is a pure-logic guard, no provider needed.
func TestConfigGetSetKeyParity(t *testing.T) {
	if len(settableConfigKeys) == 0 {
		t.Fatal("settableConfigKeys is empty")
	}
	for _, key := range settableConfigKeys {
		t.Run(key, func(t *testing.T) {
			// get must accept the key (no "unknown config key" error).
			if _, err := getConfigValue(ai.AIConfig{}, key); err != nil {
				t.Errorf("getConfigValue(%q) rejected a settable key: %v", key, err)
			}
			// set must accept the key with a type-appropriate dummy value.
			cfg := ai.DefaultAIConfig()
			if err := setConfigValue(&cfg, key, dummyConfigValue(key)); err != nil {
				t.Errorf("setConfigValue(%q, %q) rejected a settable key: %v", key, dummyConfigValue(key), err)
			}
		})
	}

	// The reverse direction: any key getConfigValue or setConfigValue accepts
	// must be in settableConfigKeys, so completion and the error message stay
	// in sync with the switches. We probe with a fabricated key that neither
	// switch should know about and assert both reject it; the per-key loop
	// above already proves the forward direction.
	const bogus = "ai.this_key_does_not_exist"
	if _, err := getConfigValue(ai.AIConfig{}, bogus); err == nil {
		t.Errorf("getConfigValue(%q) accepted a key absent from settableConfigKeys", bogus)
	}
	cfg := ai.DefaultAIConfig()
	if err := setConfigValue(&cfg, bogus, "x"); err == nil {
		t.Errorf("setConfigValue(%q) accepted a key absent from settableConfigKeys", bogus)
	}
}
