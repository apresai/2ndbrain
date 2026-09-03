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
	setupHome(t) // the ai package has no TestMain: without this the test reads the developer's real ~/.config/2nb/models.yaml
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
			want:       0.25,
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
		{"nova-2 has measured recommendation", "bedrock", "amazon.nova-2-multimodal-embeddings-v1:0", 0.25},
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
	setupHome(t) // the ai package has no TestMain: without this the test reads the developer's real ~/.config/2nb/models.yaml
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
		t.Errorf("threshold = %v, want 0.72 (user calibration should override builtin 0.25)", got)
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
	if got != 0.25 || source != ThresholdSourceModel {
		t.Errorf("empty vaultRoot should skip user catalog: got (%v, %q), want (0.25, %q)", got, source, ThresholdSourceModel)
	}
}

// writeVaultCatalogYAML drops a literal models.yaml into a vault's sidecar.
// The rows are written as TEXT on purpose: provenance is decided from what is
// stored on disk, and a row written before ThresholdSource existed simply has
// no threshold_source key, which no ModelInfo literal can express.
func writeVaultCatalogYAML(t *testing.T, vaultRoot, body string) {
	t.Helper()
	dir := filepath.Join(vaultRoot, dotDirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "models.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A user-catalog threshold is a CALIBRATION only when the user authored it.
// Eight save paths seeded their row from the builtin-merged catalog, so the
// builtin's own 0.25 got written into the user file and every vault on the
// machine then reported "user calibration" for a number nobody measured.
func TestResolveSimilarityThresholdFull_MirroredBuiltinIsNotACalibration(t *testing.T) {
	setupHome(t)
	const nova = "amazon.nova-2-multimodal-embeddings-v1:0"
	cfg := AIConfig{Provider: "bedrock", EmbeddingModel: nova}

	cases := []struct {
		name       string
		row        string
		want       float64
		wantSource ResolvedThresholdSource
	}{
		{
			// The contaminated shape: unstamped, and exactly the builtin value.
			name:       "unstamped mirror of the builtin reads as the model recommendation",
			row:        "    recommended_similarity_threshold: 0.25\n",
			want:       0.25,
			wantSource: ThresholdSourceModel,
		},
		{
			// Written before the stamp existed, but a real measurement: the
			// value is not the builtin's, so it survives.
			name:       "unstamped value that differs from the builtin is kept",
			row:        "    recommended_similarity_threshold: 0.4\n",
			want:       0.4,
			wantSource: ThresholdSourceUserCalibration,
		},
		{
			// `models calibrate --save` may legitimately land on the builtin
			// number; the stamp is what keeps it a calibration.
			name:       "stamped value equal to the builtin is still a calibration",
			row:        "    recommended_similarity_threshold: 0.25\n    threshold_source: user\n",
			want:       0.25,
			wantSource: ThresholdSourceUserCalibration,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			vaultRoot := t.TempDir()
			writeVaultCatalogYAML(t, vaultRoot, "version: 1\nmodels:\n"+
				"  - id: "+nova+"\n"+
				"    provider: bedrock\n"+
				"    type: embedding\n"+
				tc.row)

			got, source := cfg.ResolveSimilarityThresholdFull(vaultRoot)
			if got != tc.want || source != tc.wantSource {
				t.Errorf("ResolveSimilarityThresholdFull = (%v, %q), want (%v, %q)",
					got, source, tc.want, tc.wantSource)
			}
		})
	}
}

// IsUserThreshold is the single predicate both the resolver and the save paths
// consult, so pin its edges directly.
func TestIsUserThreshold(t *testing.T) {
	const nova = "amazon.nova-2-multimodal-embeddings-v1:0"
	cases := []struct {
		name string
		m    ModelInfo
		want bool
	}{
		{"zero threshold is never the user's", ModelInfo{Provider: "bedrock", ID: nova}, false},
		{"stamped is the user's", ModelInfo{Provider: "bedrock", ID: nova, RecommendedSimilarityThreshold: 0.25, ThresholdSource: ThresholdSourceUser}, true},
		{"unstamped mirror is not", ModelInfo{Provider: "bedrock", ID: nova, RecommendedSimilarityThreshold: 0.25}, false},
		{"unstamped different value is", ModelInfo{Provider: "bedrock", ID: nova, RecommendedSimilarityThreshold: 0.4}, true},
		{"model with no builtin recommendation has nothing to mirror", ModelInfo{Provider: "bedrock", ID: "not-in-any-catalog", RecommendedSimilarityThreshold: 0.25}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsUserThreshold(tc.m); got != tc.want {
				t.Errorf("IsUserThreshold = %v, want %v", got, tc.want)
			}
		})
	}
}

// The stamp names who authored ONE number, so it must move with that number
// and never outlive it.
func TestMergeFields_ThresholdSourceMovesWithTheValue(t *testing.T) {
	const nova = "amazon.nova-2-multimodal-embeddings-v1:0"
	base := ModelInfo{Provider: "bedrock", ID: nova, RecommendedSimilarityThreshold: 0.25}
	top := ModelInfo{Provider: "bedrock", ID: nova, RecommendedSimilarityThreshold: 0.4, ThresholdSource: ThresholdSourceUser}
	if out := mergeFields(base, top); out.ThresholdSource != ThresholdSourceUser {
		t.Errorf("overlay stamp lost: ThresholdSource = %q, want %q", out.ThresholdSource, ThresholdSourceUser)
	}

	stamped := ModelInfo{Provider: "bedrock", ID: nova, RecommendedSimilarityThreshold: 0.4, ThresholdSource: ThresholdSourceUser}
	plain := ModelInfo{Provider: "bedrock", ID: nova, RecommendedSimilarityThreshold: 0.25}
	out := mergeFields(stamped, plain)
	if out.RecommendedSimilarityThreshold != 0.25 || out.ThresholdSource != "" {
		t.Errorf("an unstamped overlay must replace both fields: got (%v, %q), want (0.25, \"\")",
			out.RecommendedSimilarityThreshold, out.ThresholdSource)
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
		{"bedrock", "AWS_BEARER_TOKEN_BEDROCK"},
		{"custom", "CUSTOM_API_KEY"},
	}
	for _, tt := range tests {
		got := envVarName(tt.provider)
		if got != tt.want {
			t.Errorf("envVarName(%q) = %q, want %q", tt.provider, got, tt.want)
		}
	}
}

// Provenance is a property of a STORED row, so it has to be judged per scope on
// raw rows. Judging it on LoadUserCatalog's merged view lost real calibrations:
// mergeFields lets any positive vault value overwrite the global one, so an
// unstamped vault mirror of the builtin replaced a stamped global calibration
// and then failed IsUserThreshold, and the resolver fell through to the builtin
// while the user's real measurement sat in the global file, ignored.
func TestUserThresholdRow_PerScopeOnRawRows(t *testing.T) {
	const nova = "amazon.nova-2-multimodal-embeddings-v1:0"
	cfg := AIConfig{Provider: "bedrock", EmbeddingModel: nova}

	t.Run("an unstamped vault mirror does not hide a stamped global calibration", func(t *testing.T) {
		setupHome(t)
		vaultRoot := t.TempDir()
		if err := SaveUserCatalogEntry(ScopeGlobal, "", ModelInfo{
			ID: nova, Provider: "bedrock", Type: "embedding",
			RecommendedSimilarityThreshold: 0.30, ThresholdSource: ThresholdSourceUser,
		}); err != nil {
			t.Fatalf("seed global: %v", err)
		}
		writeVaultCatalogYAML(t, vaultRoot, "version: 1\nmodels:\n"+
			"  - id: "+nova+"\n    provider: bedrock\n    type: embedding\n"+
			"    recommended_similarity_threshold: 0.25\n")

		got, source := cfg.ResolveSimilarityThresholdFull(vaultRoot)
		if got != 0.30 || source != ThresholdSourceUserCalibration {
			t.Errorf("ResolveSimilarityThresholdFull = (%v, %q), want (0.3, %q)", got, source, ThresholdSourceUserCalibration)
		}
		row, scope, ok := UserThresholdRow(vaultRoot, "bedrock", nova)
		if !ok || scope != ScopeGlobal || row.RecommendedSimilarityThreshold != 0.30 {
			t.Errorf("UserThresholdRow = (%v, %q, %v), want the stamped global 0.3", row.RecommendedSimilarityThreshold, scope, ok)
		}
	})

	t.Run("a stamped vault value still beats a stamped global one", func(t *testing.T) {
		setupHome(t)
		vaultRoot := t.TempDir()
		if err := SaveUserCatalogEntry(ScopeGlobal, "", ModelInfo{
			ID: nova, Provider: "bedrock", Type: "embedding",
			RecommendedSimilarityThreshold: 0.30, ThresholdSource: ThresholdSourceUser,
		}); err != nil {
			t.Fatalf("seed global: %v", err)
		}
		if err := SaveUserCatalogEntry(ScopeVault, vaultRoot, ModelInfo{
			ID: nova, Provider: "bedrock", Type: "embedding",
			RecommendedSimilarityThreshold: 0.42, ThresholdSource: ThresholdSourceUser,
		}); err != nil {
			t.Fatalf("seed vault: %v", err)
		}

		got, source := cfg.ResolveSimilarityThresholdFull(vaultRoot)
		if got != 0.42 || source != ThresholdSourceUserCalibration {
			t.Errorf("ResolveSimilarityThresholdFull = (%v, %q), want (0.42, %q)", got, source, ThresholdSourceUserCalibration)
		}
		if _, scope, _ := UserThresholdRow(vaultRoot, "bedrock", nova); scope != ScopeVault {
			t.Errorf("scope = %q, want %q: the vault scope overlays the global one", scope, ScopeVault)
		}
	})

	t.Run("two stored routes: the qualifying one is found, not refused as ambiguous", func(t *testing.T) {
		setupHome(t)
		vaultRoot := t.TempDir()
		writeVaultCatalogYAML(t, vaultRoot, "version: 1\nmodels:\n"+
			"  - id: "+nova+"\n    provider: bedrock\n    type: embedding\n"+
			"    plane: classic\n    region: us-east-1\n"+
			"    recommended_similarity_threshold: 0.25\n"+
			"  - id: "+nova+"\n    provider: bedrock\n    type: embedding\n"+
			"    plane: classic\n    region: us-west-2\n"+
			"    recommended_similarity_threshold: 0.31\n    threshold_source: user\n")

		got, source := cfg.ResolveSimilarityThresholdFull(vaultRoot)
		if got != 0.31 || source != ThresholdSourceUserCalibration {
			t.Errorf("ResolveSimilarityThresholdFull = (%v, %q), want (0.31, %q)", got, source, ThresholdSourceUserCalibration)
		}
		row, scope, ok := UserThresholdRow(vaultRoot, "bedrock", nova)
		if !ok || scope != ScopeVault || row.RecommendedSimilarityThreshold != 0.31 {
			t.Errorf("UserThresholdRow = (%v, %q, %v), want the stamped 0.31 in the vault scope",
				row.RecommendedSimilarityThreshold, scope, ok)
		}
	})
}
