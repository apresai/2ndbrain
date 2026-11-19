package cli

import (
	"fmt"
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
	// state a model switch that failed to sync dims would leave behind. (1024 is
	// a supported gemini-embedding-2 / gemini-embedding-001 width; an unsupported value is now refused at set time.)
	if _, err := runCLIArgs(t, root, "config", "set", "ai.dimensions", "1024"); err != nil {
		t.Fatalf("config set ai.dimensions 1024: %v", err)
	}

	// Re-selecting the catalog's default embedding model must
	// resync ai.dimensions to its declared dimensions. Regression guard: deleting the dims-sync
	// block in runConfigSet leaves the dimension stuck at 256 and this fails.
	embedModel := ai.DefaultAIConfig().EmbeddingModel
	expectedDims := fmt.Sprintf("%d", ai.DefaultAIConfig().Dimensions)
	if _, err := runCLIArgs(t, root, "config", "set", "ai.embedding_model", embedModel); err != nil {
		t.Fatalf("config set ai.embedding_model: %v", err)
	}

	out, err := runCLIArgs(t, root, "config", "get", "ai.dimensions")
	if err != nil {
		t.Fatalf("config get ai.dimensions: %v", err)
	}
	if got := strings.TrimSpace(string(out)); got != expectedDims {
		t.Errorf("ai.dimensions after embedding-model switch = %q, want %s (dims-sync did not run)", got, expectedDims)
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

// TestSetConfigValue_ReasoningEffort pins the accepted vocabulary for the
// thinking-depth setting. Only the four provider-recognized depths (plus empty,
// which clears it) may be stored, and they are normalized to lowercase so
// ResolveReasoningEffort and the provider request agree on the spelling.
func TestSetConfigValue_ReasoningEffort(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		wantErr string // empty = success
		wantVal string
	}{
		{"none", "none", "", "none"},
		{"low", "low", "", "low"},
		{"medium", "medium", "", "medium"},
		{"high", "high", "", "high"},
		{"xhigh", "xhigh", "", "xhigh"},
		{"uppercase normalizes", "HIGH", "", "high"},
		{"surrounding space trimmed", "  medium  ", "", "medium"},
		{"empty clears", "", "", ""},
		{"whitespace-only clears", "   ", "", ""},
		{"unknown depth", "maximum", "must be one of", ""},
		{"numeric", "3", "must be one of", ""},
		{"typo", "hgih", "must be one of", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := ai.AIConfig{ReasoningEffort: "low"} // pre-set, so a clear is observable
			err := setConfigValue(&cfg, "ai.reasoning_effort", tt.value)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil (stored %q)", tt.wantErr, cfg.ReasoningEffort)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("error = %q, want to contain %q", err.Error(), tt.wantErr)
				}
				if cfg.ReasoningEffort != "low" {
					t.Errorf("rejected value must leave the config untouched, got %q", cfg.ReasoningEffort)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if cfg.ReasoningEffort != tt.wantVal {
				t.Errorf("cfg.ReasoningEffort = %q, want %q", cfg.ReasoningEffort, tt.wantVal)
			}
			// Whatever set stored must round-trip through get and resolve.
			got, err := getConfigValue(cfg, "ai.reasoning_effort")
			if err != nil {
				t.Fatalf("getConfigValue: %v", err)
			}
			if got != tt.wantVal {
				t.Errorf("getConfigValue = %q, want %q", got, tt.wantVal)
			}
			if resolved := cfg.ResolveReasoningEffort(); resolved != tt.wantVal {
				t.Errorf("ResolveReasoningEffort() = %q, want %q", resolved, tt.wantVal)
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

// dummyConfigValue returns a value that setConfigValue will accept for the
// given key. Boolean keys want "true", the threshold wants a 0..1 float,
// dimensions wants an integer, and the rest are free-form strings (the
// provider key needs a real provider name to pass validation).
func dummyConfigValue(key string) string {
	switch key {
	case "ai.provider", "ai.embedding_provider", "ai.generation_provider", "ai.rerank.provider":
		return "bedrock"
	case "ai.dimensions":
		return "1024"
	case "ai.task_preset", "ai.llama.task_prefix":
		return "code"
	case "ai.similarity_threshold":
		return "0.5"
	case "ai.bm25_weight", "ai.vector_weight":
		return "1.5"
	case "ai.rag_context_budget", "ai.rag_note_budget":
		return "30000"
	case "ai.embed_concurrency":
		return "8"
	case "ai.reasoning_effort":
		return "medium"
	case "ai.rerank.enabled", "ai.gemini.use_vertex_ai":
		return "true"
	case "ai.rerank.candidate_docs":
		return "40"
	case "ai.generation_plane":
		return "mantle"
	case "ai.embedding_plane", "ai.rerank.plane":
		// Not "mantle": that plane is generation-only in 2nb, and these
		// slots reject it by design.
		return "classic"
	case "ai.generation_region", "ai.embedding_region", "ai.rerank.region":
		return "us-west-2"
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

func TestReadAPIKeyLine(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr string
	}{
		{"with newline", "AIza-test-key\n", "AIza-test-key", ""},
		{"without newline EOF", "AIza-test-key", "AIza-test-key", ""},
		{"padded CRLF", "  AIza-test-key \r\n", "AIza-test-key", ""},
		{"empty EOF", "", "", "read key: EOF"},
		{"whitespace newline", "   \n", "", "empty key"},
		{"whitespace EOF", "   ", "", "empty key"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := readAPIKeyLine(strings.NewReader(tt.input))
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("readAPIKeyLine(%q) unexpected error: %v", tt.input, err)
				}
				if got != tt.want {
					t.Fatalf("readAPIKeyLine(%q) = %q, want %q", tt.input, got, tt.want)
				}
				return
			}
			if err == nil {
				t.Fatalf("readAPIKeyLine(%q) = %q, want error containing %q", tt.input, got, tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("readAPIKeyLine(%q) error = %q, want substring %q", tt.input, err.Error(), tt.wantErr)
			}
		})
	}
}

func TestSettableConfigKeys_includesTaskPreset(t *testing.T) {
	var foundPreset, foundPrefix bool
	for _, k := range settableConfigKeys {
		if k == "ai.task_preset" {
			foundPreset = true
		}
		if k == "ai.llama.task_prefix" {
			foundPrefix = true
		}
	}
	if !foundPreset {
		t.Error("ai.task_preset missing from settableConfigKeys — shell completion + error messages will be wrong")
	}
	if !foundPrefix {
		t.Error("ai.llama.task_prefix missing from settableConfigKeys — GUI compatibility alias missing")
	}
}

func TestSetConfigValue_TaskPreset(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		wantErr string
		wantVal string
	}{
		{"search default", "search", "", "search"},
		{"code retrieval", "code", "", "code"},
		{"question answering", "qa", "", "qa"},
		{"none raw", "none", "", "none"},
		{"symmetric alias normalizes to none", "symmetric", "", "none"},
		{"uppercase normalizes", "CODE", "", "code"},
		{"surrounding space trimmed", "  qa  ", "", "qa"},
		{"empty clears", "", "", ""},
		{"whitespace clears", "   ", "", ""},
		{"unknown preset rejected", "summarize", "must be one of", ""},
		{"numeric rejected", "123", "must be one of", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := ai.AIConfig{TaskPreset: "search"}
			err := setConfigValue(&cfg, "ai.task_preset", tt.value)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("error = %q, want to contain %q", err.Error(), tt.wantErr)
				}
				if cfg.TaskPreset != "search" {
					t.Errorf("rejected value must leave config untouched, got %q", cfg.TaskPreset)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if cfg.TaskPreset != tt.wantVal {
				t.Errorf("cfg.TaskPreset = %q, want %q", cfg.TaskPreset, tt.wantVal)
			}
			got, err := getConfigValue(cfg, "ai.task_preset")
			if err != nil {
				t.Fatalf("getConfigValue: %v", err)
			}
			wantGot := tt.wantVal
			if wantGot == "" {
				wantGot = ai.DefaultTaskPreset
			}
			if got != wantGot {
				t.Errorf("getConfigValue = %q, want %q", got, wantGot)
			}
		})
	}
}

func TestSetConfigValue_TaskPrefixAlias(t *testing.T) {
	cfg := ai.AIConfig{}
	if err := setConfigValue(&cfg, "ai.llama.task_prefix", "code"); err != nil {
		t.Fatalf("setConfigValue(ai.llama.task_prefix): %v", err)
	}
	if cfg.TaskPreset != "code" {
		t.Errorf("cfg.TaskPreset = %q, want code", cfg.TaskPreset)
	}
	got, err := getConfigValue(cfg, "ai.llama.task_prefix")
	if err != nil {
		t.Fatalf("getConfigValue(ai.llama.task_prefix): %v", err)
	}
	if got != "code" {
		t.Errorf("getConfigValue = %q, want code", got)
	}
}

func TestConfigSetTaskPreset_RoundTripAndValidation(t *testing.T) {
	_, root := newContractVault(t)

	// 1. Set valid preset "code"
	if _, err := runCLIArgs(t, root, "config", "set", "ai.task_preset", "code"); err != nil {
		t.Fatalf("config set ai.task_preset code: %v", err)
	}
	out, err := runCLIArgs(t, root, "config", "get", "ai.task_preset")
	if err != nil {
		t.Fatalf("config get ai.task_preset: %v", err)
	}
	if got := strings.TrimSpace(string(out)); got != "code" {
		t.Errorf("config get ai.task_preset = %q, want code", got)
	}

	// 2. Reject invalid preset
	if _, err := runCLIArgs(t, root, "config", "set", "ai.task_preset", "unsupported"); err == nil {
		t.Error("config set ai.task_preset unsupported should error")
	}

	// 3. Switch to "qa" using GUI alias
	if _, err := runCLIArgs(t, root, "config", "set", "ai.llama.task_prefix", "qa"); err != nil {
		t.Fatalf("config set ai.llama.task_prefix qa: %v", err)
	}
	out, err = runCLIArgs(t, root, "config", "get", "ai.task_preset")
	if err != nil {
		t.Fatalf("config get ai.task_preset: %v", err)
	}
	if got := strings.TrimSpace(string(out)); got != "qa" {
		t.Errorf("config get ai.task_preset = %q, want qa", got)
	}
}

func TestConfigSetPerSlotProviders_ComboAndReset(t *testing.T) {
	_, root := newContractVault(t)

	if _, err := runCLIArgs(t, root, "config", "set", "ai.embedding_provider", "llama-local"); err != nil {
		t.Fatalf("config set ai.embedding_provider llama-local: %v", err)
	}
	if _, err := runCLIArgs(t, root, "config", "set", "ai.embedding_model", "embeddinggemma-2"); err != nil {
		t.Fatalf("config set ai.embedding_model embeddinggemma-2: %v", err)
	}
	if _, err := runCLIArgs(t, root, "config", "set", "ai.generation_provider", "gemini"); err != nil {
		t.Fatalf("config set ai.generation_provider gemini: %v", err)
	}
	if _, err := runCLIArgs(t, root, "config", "set", "ai.generation_model", "gemini-4-hydrogen"); err != nil {
		t.Fatalf("config set ai.generation_model gemini-4-hydrogen: %v", err)
	}

	checkGet := func(key, want string) {
		t.Helper()
		out, err := runCLIArgs(t, root, "config", "get", key)
		if err != nil {
			t.Fatalf("config get %s: %v", key, err)
		}
		if got := strings.TrimSpace(string(out)); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
	checkGet("ai.embedding_provider", "llama-local")
	checkGet("ai.embedding_model", "embeddinggemma-2")
	checkGet("ai.generation_provider", "gemini")
	checkGet("ai.generation_model", "gemini-4-hydrogen")

	// Clearing a per-slot override via "" inherits ai.provider again.
	if _, err := runCLIArgs(t, root, "config", "set", "ai.embedding_provider", ""); err != nil {
		t.Fatalf("clear ai.embedding_provider: %v", err)
	}
	checkGet("ai.embedding_provider", "gemini")

	// Re-apply mixed override, then verify setting unified ai.provider clears all per-slot overrides.
	if _, err := runCLIArgs(t, root, "config", "set", "ai.embedding_provider", "llama-local"); err != nil {
		t.Fatalf("re-set ai.embedding_provider: %v", err)
	}
	if _, err := runCLIArgs(t, root, "config", "set", "ai.provider", "ollama"); err != nil {
		t.Fatalf("set unified ai.provider ollama: %v", err)
	}
	checkGet("ai.provider", "ollama")
	checkGet("ai.embedding_provider", "ollama")
	checkGet("ai.generation_provider", "ollama")

	// Unknown provider rejected on slot key.
	if _, err := runCLIArgs(t, root, "config", "set", "ai.embedding_provider", "not-a-provider"); err == nil {
		t.Error("expected error for unknown provider on ai.embedding_provider")
	}
}
