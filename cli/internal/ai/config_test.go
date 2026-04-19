package ai

import (
	"os"
	"path/filepath"
	"testing"
)

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
	if cfg.SimilarityThreshold != 0 {
		t.Errorf("default SimilarityThreshold = %v, want 0 (so the resolution chain falls through to the per-model recommendation)", cfg.SimilarityThreshold)
	}
}

func TestResolveSimilarityThreshold(t *testing.T) {
	tests := []struct {
		name       string
		cfg        AIConfig
		want       float64
		wantSource ResolvedThresholdSource
	}{
		{
			name:       "vault config overrides everything",
			cfg:        AIConfig{Provider: "bedrock", EmbeddingModel: "amazon.nova-2-multimodal-embeddings-v1:0", SimilarityThreshold: 0.42},
			want:       0.42,
			wantSource: ThresholdSourceVaultConfig,
		},
		{
			name:       "nova-2 catalog recommendation when config unset",
			cfg:        AIConfig{Provider: "bedrock", EmbeddingModel: "amazon.nova-2-multimodal-embeddings-v1:0"},
			want:       0.65,
			wantSource: ThresholdSourceModel,
		},
		{
			name:       "default when model not in catalog",
			cfg:        AIConfig{Provider: "ollama", EmbeddingModel: "some-custom-model"},
			want:       DefaultSimilarityThreshold,
			wantSource: ThresholdSourceDefault,
		},
		{
			name:       "nomic uses its 0.50 recommendation",
			cfg:        AIConfig{Provider: "ollama", EmbeddingModel: "nomic-embed-text"},
			want:       0.50,
			wantSource: ThresholdSourceModel,
		},
		{
			name:       "all-minilm uses its 0.35 recommendation (small-dim spread)",
			cfg:        AIConfig{Provider: "ollama", EmbeddingModel: "all-minilm"},
			want:       0.35,
			wantSource: ThresholdSourceModel,
		},
		{
			name:       "empty config falls back to default",
			cfg:        AIConfig{},
			want:       DefaultSimilarityThreshold,
			wantSource: ThresholdSourceDefault,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, source := tt.cfg.ResolveSimilarityThresholdFull("")
			if got != tt.want || source != tt.wantSource {
				t.Errorf("ResolveSimilarityThresholdFull(\"\") = (%v, %q), want (%v, %q)", got, source, tt.want, tt.wantSource)
			}
		})
	}
}

func TestRecommendedSimilarityThresholdFor(t *testing.T) {
	tests := []struct {
		name     string
		provider string
		modelID  string
		want     float64
	}{
		{"nova-2 has measured recommendation", "bedrock", "amazon.nova-2-multimodal-embeddings-v1:0", 0.65},
		{"nemotron has estimate", "openrouter", "nvidia/llama-nemotron-embed-vl-1b-v2:free", 0.60},
		{"nomic has estimate", "ollama", "nomic-embed-text", 0.50},
		{"mxbai has estimate", "ollama", "mxbai-embed-large", 0.55},
		{"all-minilm small-dim estimate", "ollama", "all-minilm", 0.35},
		{"unknown model returns zero", "bedrock", "nonexistent-model", 0},
		{"empty provider returns zero", "", "amazon.nova-2-multimodal-embeddings-v1:0", 0},
		{"empty model returns zero", "bedrock", "", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RecommendedSimilarityThresholdFor(tt.provider, tt.modelID)
			if got != tt.want {
				t.Errorf("RecommendedSimilarityThresholdFor(%q, %q) = %v, want %v", tt.provider, tt.modelID, got, tt.want)
			}
		})
	}
}

func TestResolveSimilarityThresholdFull_UserCatalogOverride(t *testing.T) {
	vault := t.TempDir()
	dot := filepath.Join(vault, dotDirName)
	if err := os.MkdirAll(dot, 0o755); err != nil {
		t.Fatal(err)
	}

	// Calibration saved: user catalog overrides the builtin Nova-2 recommendation.
	entry := ModelInfo{
		ID:                             "amazon.nova-2-multimodal-embeddings-v1:0",
		Provider:                       "bedrock",
		Type:                           "embedding",
		Tier:                           TierUserVerified,
		RecommendedSimilarityThreshold: 0.72,
	}
	if err := SaveUserCatalogEntry(ScopeVault, vault, entry); err != nil {
		t.Fatalf("save calibration: %v", err)
	}

	cfg := AIConfig{Provider: "bedrock", EmbeddingModel: "amazon.nova-2-multimodal-embeddings-v1:0"}
	got, source := cfg.ResolveSimilarityThresholdFull(vault)
	if got != 0.72 {
		t.Errorf("threshold = %v, want 0.72 (user calibration should override builtin 0.65)", got)
	}
	if source != ThresholdSourceUserCalibration {
		t.Errorf("source = %q, want %q", source, ThresholdSourceUserCalibration)
	}

	// With explicit vault config, that wins over the user catalog.
	cfg.SimilarityThreshold = 0.50
	got, source = cfg.ResolveSimilarityThresholdFull(vault)
	if got != 0.50 || source != ThresholdSourceVaultConfig {
		t.Errorf("vault config should beat user catalog: got (%v, %q), want (0.50, %q)", got, source, ThresholdSourceVaultConfig)
	}

	// No user entry for a different model — falls through to builtin.
	cfg2 := AIConfig{Provider: "ollama", EmbeddingModel: "nomic-embed-text"}
	got, source = cfg2.ResolveSimilarityThresholdFull(vault)
	if got != 0.50 || source != ThresholdSourceModel {
		t.Errorf("other model should use builtin recommendation: got (%v, %q), want (0.50, %q)", got, source, ThresholdSourceModel)
	}

	// Empty vaultRoot bypasses user-catalog lookup entirely.
	cfg3 := AIConfig{Provider: "bedrock", EmbeddingModel: "amazon.nova-2-multimodal-embeddings-v1:0"}
	got, source = cfg3.ResolveSimilarityThresholdFull("")
	if got != 0.65 || source != ThresholdSourceModel {
		t.Errorf("empty vaultRoot should skip user catalog: got (%v, %q), want (0.65, %q)", got, source, ThresholdSourceModel)
	}
}

func TestMergeFields_OverlaysThreshold(t *testing.T) {
	base := ModelInfo{
		Provider:                       "bedrock",
		ID:                             "amazon.nova-2-multimodal-embeddings-v1:0",
		Type:                           "embedding",
		RecommendedSimilarityThreshold: 0.65,
	}
	// Overlay with a higher threshold: top wins.
	top := ModelInfo{
		Provider:                       "bedrock",
		ID:                             "amazon.nova-2-multimodal-embeddings-v1:0",
		RecommendedSimilarityThreshold: 0.72,
	}
	out := mergeFields(base, top)
	if out.RecommendedSimilarityThreshold != 0.72 {
		t.Errorf("merged threshold = %v, want 0.72", out.RecommendedSimilarityThreshold)
	}

	// Overlay with zero preserves the base value.
	top2 := ModelInfo{Provider: "bedrock", ID: "amazon.nova-2-multimodal-embeddings-v1:0"}
	out2 := mergeFields(base, top2)
	if out2.RecommendedSimilarityThreshold != 0.65 {
		t.Errorf("zero overlay should not wipe base threshold: got %v, want 0.65", out2.RecommendedSimilarityThreshold)
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
