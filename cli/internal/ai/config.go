package ai

// AIConfig holds AI provider configuration from vault config.yaml.
type AIConfig struct {
	Provider        string           `yaml:"provider" json:"provider"`                 // ollama, bedrock, openrouter
	EmbeddingModel  string           `yaml:"embedding_model" json:"embedding_model"`
	GenerationModel string           `yaml:"generation_model" json:"generation_model"`
	Dimensions      int              `yaml:"dimensions" json:"dimensions"`
	Ollama          OllamaConfig     `yaml:"ollama,omitempty" json:"ollama,omitempty"`
	Bedrock         BedrockConfig    `yaml:"bedrock,omitempty" json:"bedrock,omitempty"`
	OpenRouter      OpenRouterConfig `yaml:"openrouter,omitempty" json:"openrouter,omitempty"`
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

// DefaultAIConfig returns sensible defaults.
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
