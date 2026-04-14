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

// DefaultAIConfig returns sensible defaults.
func DefaultAIConfig() AIConfig {
	return AIConfig{
		Provider:            "bedrock",
		EmbeddingModel:      "amazon.nova-2-multimodal-embeddings-v1:0",
		GenerationModel:     "us.anthropic.claude-haiku-4-5-20251001-v1:0",
		Dimensions:          1024,
		SimilarityThreshold: DefaultSimilarityThreshold,
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

// ResolveSimilarityThreshold returns the configured threshold, falling back to
// the default when the value hasn't been set (zero-valued config from an old
// vault or unset field). Pass the result to search.VectorSearchThreshold.
func (c AIConfig) ResolveSimilarityThreshold() float64 {
	if c.SimilarityThreshold <= 0 {
		return DefaultSimilarityThreshold
	}
	return c.SimilarityThreshold
}
