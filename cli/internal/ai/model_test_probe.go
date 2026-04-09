package ai

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// TestProbeResult holds the result of a model test.
type TestProbeResult struct {
	ModelID  string `json:"model_id"`
	Provider string `json:"provider"`
	Type     string `json:"type"` // "embedding" or "generation"
	OK       bool   `json:"ok"`
	Detail   string `json:"detail,omitempty"` // response snippet or error
	Latency  string `json:"latency"`
}

// TestProbeModel creates a temporary provider for the given model and runs
// a quick smoke test (embed or generate). Returns the result.
func TestProbeModel(ctx context.Context, cfg AIConfig, modelID, provider, modelType string) (*TestProbeResult, error) {
	if provider == "" {
		provider = InferProvider(modelID)
	}
	if provider == "" {
		return nil, fmt.Errorf("cannot infer provider for %q — use --provider", modelID)
	}

	if modelType == "" {
		modelType = InferModelType(modelID)
	}

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	result := &TestProbeResult{
		ModelID:  modelID,
		Provider: provider,
		Type:     modelType,
	}

	start := time.Now()
	var err error

	switch modelType {
	case "embedding":
		err = probeEmbedding(ctx, cfg, provider, modelID)
	default:
		var snippet string
		snippet, err = probeGeneration(ctx, cfg, provider, modelID)
		if err == nil {
			result.Detail = snippet
		}
	}

	result.Latency = time.Since(start).Round(time.Millisecond).String()

	if err != nil {
		result.OK = false
		result.Detail = err.Error()
	} else {
		result.OK = true
	}

	return result, nil
}

func probeEmbedding(ctx context.Context, cfg AIConfig, provider, modelID string) error {
	switch provider {
	case "bedrock":
		e, err := NewBedrockEmbedder(ctx, cfg.Bedrock, modelID, cfg.Dimensions)
		if err != nil {
			return err
		}
		vecs, err := e.Embed(ctx, []string{"test embedding probe"})
		if err != nil {
			return err
		}
		if len(vecs) == 0 || len(vecs[0]) == 0 {
			return fmt.Errorf("got empty embedding vector")
		}
		return nil

	case "openrouter":
		key, err := GetAPIKey("openrouter")
		if err != nil {
			return err
		}
		e := NewOpenRouterEmbedder(key, modelID, cfg.Dimensions)
		vecs, err := e.Embed(ctx, []string{"test embedding probe"})
		if err != nil {
			return err
		}
		if len(vecs) == 0 || len(vecs[0]) == 0 {
			return fmt.Errorf("got empty embedding vector")
		}
		return nil

	case "ollama":
		endpoint := cfg.Ollama.Endpoint
		if endpoint == "" {
			endpoint = "http://localhost:11434"
		}
		e := NewOllamaEmbedder(endpoint, modelID, cfg.Dimensions)
		vecs, err := e.Embed(ctx, []string{"test embedding probe"})
		if err != nil {
			return err
		}
		if len(vecs) == 0 || len(vecs[0]) == 0 {
			return fmt.Errorf("got empty embedding vector")
		}
		return nil

	default:
		return fmt.Errorf("unknown provider %q", provider)
	}
}

func probeGeneration(ctx context.Context, cfg AIConfig, provider, modelID string) (string, error) {
	prompt := "What is 2+2? Reply with just the number."
	opts := GenOpts{MaxTokens: 32, Temperature: 0}

	switch provider {
	case "bedrock":
		g, err := NewBedrockGenerator(ctx, cfg.Bedrock, modelID)
		if err != nil {
			return "", err
		}
		return g.Generate(ctx, prompt, opts)

	case "openrouter":
		key, err := GetAPIKey("openrouter")
		if err != nil {
			return "", err
		}
		g := NewOpenRouterGenerator(key, modelID)
		return g.Generate(ctx, prompt, opts)

	case "ollama":
		endpoint := cfg.Ollama.Endpoint
		if endpoint == "" {
			endpoint = "http://localhost:11434"
		}
		g := NewOllamaGenerator(endpoint, modelID)
		return g.Generate(ctx, prompt, opts)

	default:
		return "", fmt.Errorf("unknown provider %q", provider)
	}
}

// InferProvider guesses the provider from model ID patterns.
func InferProvider(modelID string) string {
	// Bedrock: starts with region prefix or amazon/anthropic/meta namespace
	if strings.HasPrefix(modelID, "us.") ||
		strings.HasPrefix(modelID, "eu.") ||
		strings.HasPrefix(modelID, "ap.") ||
		strings.HasPrefix(modelID, "amazon.") ||
		strings.HasPrefix(modelID, "anthropic.") ||
		strings.HasPrefix(modelID, "openai.") ||
		strings.HasPrefix(modelID, "meta.") ||
		strings.HasPrefix(modelID, "mistral.") ||
		strings.HasPrefix(modelID, "cohere.") {
		return "bedrock"
	}
	// OpenRouter: contains a slash (org/model format)
	if strings.Contains(modelID, "/") {
		return "openrouter"
	}
	// Default to Ollama for simple names
	return "ollama"
}

// InferModelType guesses embedding vs generation from model ID.
func InferModelType(modelID string) string {
	lower := strings.ToLower(modelID)
	if strings.Contains(lower, "embed") {
		return "embedding"
	}
	return "generation"
}
