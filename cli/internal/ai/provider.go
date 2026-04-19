package ai

import "context"

// KnownProviders is the canonical list of AI providers 2nb supports.
// Used by shell completion, wizard defaults, and test assertions — when
// adding a new provider, append it here so every site stays in sync.
var KnownProviders = []string{"bedrock", "openrouter", "ollama"}

// EmbeddingProvider generates vector embeddings from text.
type EmbeddingProvider interface {
	Name() string
	Embed(ctx context.Context, texts []string) ([][]float32, error)
	Dimensions() int
	Available(ctx context.Context) bool
	ListModels(ctx context.Context) ([]ModelInfo, error)
}

// GenerationProvider generates text from prompts.
type GenerationProvider interface {
	Name() string
	Generate(ctx context.Context, prompt string, opts GenOpts) (string, error)
	Available(ctx context.Context) bool
	ListModels(ctx context.Context) ([]ModelInfo, error)
}

// GenOpts configures text generation.
type GenOpts struct {
	Temperature  float64
	MaxTokens    int
	SystemPrompt string
}

// ModelTier indicates whether a model has a verified API harness in 2nb.
type ModelTier string

const (
	// TierVerified means 2nb has tested this model's invoke format and it works.
	TierVerified ModelTier = "verified"
	// TierUserVerified means the user's own catalog (or a passing `models test --save`)
	// has recorded that this model works for them. Sits between builtin-verified
	// and unverified so users can trust their own runtime additions.
	TierUserVerified ModelTier = "user_verified"
	// TierUnverified means the vendor API lists this model but 2nb hasn't built/tested a harness for it.
	TierUnverified ModelTier = "unverified"
)

// ModelInfo describes an available model. Both json and yaml tags are set:
// JSON is used for CLI --json output and vendor API payloads; YAML is used
// for the user catalog file at ~/.config/2nb/models.yaml.
type ModelInfo struct {
	ID         string  `json:"id" yaml:"id"`
	Name       string  `json:"name" yaml:"name,omitempty"`
	Provider   string  `json:"provider" yaml:"provider"`
	Type       string  `json:"type" yaml:"type"` // "embedding" or "generation"
	Dimensions int     `json:"dimensions,omitempty" yaml:"dimensions,omitempty"`
	ContextLen int     `json:"context_length,omitempty" yaml:"context_length,omitempty"`
	// RecommendedSimilarityThreshold is the suggested minimum cosine similarity
	// for semantic search with this embedding model. Used when the vault's
	// ai.similarity_threshold isn't explicitly set. Different embedding models
	// have different baseline similarity distributions: Nova-2 clusters tight
	// (random-pair cosine ~0.55–0.64), while smaller-dim models spread wider.
	// Only meaningful for Type="embedding". Zero means "no model recommendation,
	// fall back to ai.DefaultSimilarityThreshold".
	RecommendedSimilarityThreshold float64 `json:"recommended_similarity_threshold,omitempty" yaml:"recommended_similarity_threshold,omitempty"`
	PriceIn    float64 `json:"price_input_per_million" yaml:"price_input_per_million,omitempty"`
	PriceOut   float64 `json:"price_output_per_million" yaml:"price_output_per_million,omitempty"`
	Local      bool    `json:"local" yaml:"local,omitempty"`

	// Tier indicates whether 2nb has a verified harness for this model.
	Tier ModelTier `json:"tier,omitempty" yaml:"tier,omitempty"`
	// Active is true when this model is currently configured.
	Active bool `json:"active,omitempty" yaml:"-"`
	// Reachable indicates provider connectivity: nil=unchecked, true/false=probed.
	Reachable *bool `json:"reachable,omitempty" yaml:"-"`
	// CredsOK indicates credential availability: nil=N/A or unchecked.
	CredsOK *bool `json:"credentials,omitempty" yaml:"-"`
	// ConfigHint shows how to switch to this model.
	ConfigHint string `json:"config_hint,omitempty" yaml:"config_hint,omitempty"`
	// RateLimitRPS is the known rate limit in requests per second (0=unknown).
	RateLimitRPS float64 `json:"rate_limit_rps,omitempty" yaml:"rate_limit_rps,omitempty"`
	// RateLimitTPM is the known rate limit in tokens per minute (0=unknown).
	RateLimitTPM int `json:"rate_limit_tpm,omitempty" yaml:"rate_limit_tpm,omitempty"`
	// Notes contains caveats like "different invoke format — not yet supported".
	Notes string `json:"notes,omitempty" yaml:"notes,omitempty"`
	// PriceSource records which layer supplied the pricing: "builtin", "bundled",
	// "user", "vendor". Empty when price fields are zero/unknown.
	PriceSource string `json:"price_source,omitempty" yaml:"price_source,omitempty"`
	// TestedAt is an ISO-8601 timestamp recorded when the model last passed
	// `2nb models test`. Present only on user-catalog entries.
	TestedAt string `json:"tested_at,omitempty" yaml:"tested_at,omitempty"`
}

// DefaultGenOpts returns sensible defaults for generation.
func DefaultGenOpts() GenOpts {
	return GenOpts{
		Temperature: 0.1,
		MaxTokens:   512,
	}
}
