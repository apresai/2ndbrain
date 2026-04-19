package ai

// AIConfig holds AI provider configuration from vault config.yaml.
type AIConfig struct {
	Provider        string           `yaml:"provider" json:"provider"` // ollama, bedrock, openrouter
	EmbeddingModel  string           `yaml:"embedding_model" json:"embedding_model"`
	GenerationModel string           `yaml:"generation_model" json:"generation_model"`
	Dimensions      int              `yaml:"dimensions" json:"dimensions"`
	// SimilarityThreshold is the minimum cosine similarity for a vector
	// search hit to be included in results. Below this, results are
	// treated as noise and dropped from the RRF merge so they don't pad
	// the output with low-quality neighbors. Default: 0.20.
	SimilarityThreshold float64          `yaml:"similarity_threshold" json:"similarity_threshold"`
	Ollama              OllamaConfig     `yaml:"ollama,omitempty" json:"ollama,omitempty"`
	Bedrock             BedrockConfig    `yaml:"bedrock,omitempty" json:"bedrock,omitempty"`
	OpenRouter          OpenRouterConfig `yaml:"openrouter,omitempty" json:"openrouter,omitempty"`
}

// OllamaConfig configures the local Ollama provider.
type OllamaConfig struct {
	Endpoint string `yaml:"endpoint" json:"endpoint"` // default: http://localhost:11434
}

// BedrockConfig configures the AWS Bedrock provider.
type BedrockConfig struct {
	Profile string `yaml:"profile" json:"profile"` // AWS profile name
	Region  string `yaml:"region" json:"region"`   // AWS region
}

// OpenRouterConfig configures the OpenRouter provider.
type OpenRouterConfig struct {
	APIKeyEnv string `yaml:"api_key_env" json:"api_key_env"` // env var name
}

// DefaultSimilarityThreshold is the conservative floor for semantic search.
// Cosine similarity below this value usually indicates an unrelated neighbor
// rather than a real match, and padding the result list with those hurts more
// than it helps. Tuned by watching the raw cosine reported in search output
// against real vaults — raise it (to ~0.35) if too many irrelevant hits slip
// through, lower it (to ~0.15) if legitimate matches are being cut.
const DefaultSimilarityThreshold = 0.20

// DefaultAIConfig returns sensible defaults. SimilarityThreshold is left at
// zero so ResolveSimilarityThresholdFull falls through to the active
// embedding model's RecommendedSimilarityThreshold (e.g., Nova-2 → 0.65)
// instead of permanently shadowing it with the conservative global default.
func DefaultAIConfig() AIConfig {
	return AIConfig{
		Provider:        "bedrock",
		EmbeddingModel:  "amazon.nova-2-multimodal-embeddings-v1:0",
		GenerationModel: "us.anthropic.claude-haiku-4-5-20251001-v1:0",
		Dimensions:      1024,
		Ollama: OllamaConfig{
			Endpoint: "http://localhost:11434",
		},
		Bedrock: BedrockConfig{
			Profile: "default",
			Region:  "us-east-1",
		},
		OpenRouter: OpenRouterConfig{
			APIKeyEnv: "OPENROUTER_API_KEY",
		},
	}
}

// ResolvedThresholdSource labels where ResolveSimilarityThreshold picked its
// value from — useful for surfacing the reason in `2nb ai status` so a user
// who can't figure out why search is noisy/empty can trace the setting.
type ResolvedThresholdSource string

const (
	ThresholdSourceVaultConfig     ResolvedThresholdSource = "vault config"
	ThresholdSourceUserCalibration ResolvedThresholdSource = "user calibration"
	ThresholdSourceModel           ResolvedThresholdSource = "model recommendation"
	ThresholdSourceDefault         ResolvedThresholdSource = "default"
)

// ResolveSimilarityThresholdFull returns the active minimum cosine similarity
// for semantic search along with the provenance of the chosen value:
//
//  1. Vault config (ai.similarity_threshold > 0)     → ThresholdSourceVaultConfig
//  2. User catalog (global or per-vault models.yaml) → ThresholdSourceUserCalibration
//  3. Builtin catalog recommendation                  → ThresholdSourceModel
//  4. DefaultSimilarityThreshold                      → ThresholdSourceDefault
//
// Pass vaultRoot="" when there's no open vault; the user-catalog layer is
// skipped in that case.
func (c AIConfig) ResolveSimilarityThresholdFull(vaultRoot string) (float64, ResolvedThresholdSource) {
	if c.SimilarityThreshold > 0 {
		return c.SimilarityThreshold, ThresholdSourceVaultConfig
	}
	if vaultRoot != "" {
		if t := userCatalogSimilarityThreshold(vaultRoot, c.Provider, c.EmbeddingModel); t > 0 {
			return t, ThresholdSourceUserCalibration
		}
	}
	if t := RecommendedSimilarityThresholdFor(c.Provider, c.EmbeddingModel); t > 0 {
		return t, ThresholdSourceModel
	}
	return DefaultSimilarityThreshold, ThresholdSourceDefault
}

// RecommendedSimilarityThresholdFor returns the builtin catalog's recommended
// threshold for (provider, modelID). Returns 0 when the model isn't in the
// catalog or has no recommendation. User-added catalog entries are NOT
// consulted here — use ResolveSimilarityThresholdFull for that.
func RecommendedSimilarityThresholdFor(provider, modelID string) float64 {
	if provider == "" || modelID == "" {
		return 0
	}
	for _, m := range BuiltinCatalog() {
		if m.Type == "embedding" && m.Provider == provider && m.ID == modelID {
			return m.RecommendedSimilarityThreshold
		}
	}
	return 0
}

// userCatalogSimilarityThreshold scans the user catalog (global + per-vault)
// for an entry matching the active embedding model. Zero means "not set by
// the user" — callers fall through to the builtin catalog.
func userCatalogSimilarityThreshold(vaultRoot, provider, modelID string) float64 {
	if provider == "" || modelID == "" {
		return 0
	}
	for _, m := range LoadUserCatalog(vaultRoot) {
		if m.Type == "embedding" && m.Provider == provider && m.ID == modelID && m.RecommendedSimilarityThreshold > 0 {
			return m.RecommendedSimilarityThreshold
		}
	}
	return 0
}
