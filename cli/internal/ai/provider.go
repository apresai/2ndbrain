package ai

import "context"

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

// ModelInfo describes an available model.
type ModelInfo struct {
	ID         string  `json:"id"`
	Name       string  `json:"name"`
	Provider   string  `json:"provider"`
	Type       string  `json:"type"` // "embedding" or "generation"
	Dimensions int     `json:"dimensions,omitempty"`
	ContextLen int     `json:"context_length,omitempty"`
	PriceIn    float64 `json:"price_input_per_million"` // per 1M tokens, 0 = free
	PriceOut   float64 `json:"price_output_per_million"`
	Local      bool    `json:"local"`
}

// DefaultGenOpts returns sensible defaults for generation.
func DefaultGenOpts() GenOpts {
	return GenOpts{
		Temperature: 0.1,
		MaxTokens:   512,
	}
}
