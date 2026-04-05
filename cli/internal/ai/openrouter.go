package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const (
	openrouterBaseURL          = "https://openrouter.ai/api/v1"
	openrouterDefaultEmbedModel = "nvidia/llama-nemotron-embed-vl-1b-v2:free"
)

// OpenRouterEmbedder implements EmbeddingProvider using OpenRouter's OpenAI-compatible API.
type OpenRouterEmbedder struct {
	apiKey    string
	model     string
	dims      int
	baseURL   string       // overridable for tests
	client    *http.Client // overridable for tests
	available *bool
}

// NewOpenRouterEmbedder creates an OpenRouter embedding provider.
func NewOpenRouterEmbedder(apiKey, model string, dims int) *OpenRouterEmbedder {
	if model == "" {
		model = openrouterDefaultEmbedModel
	}
	return &OpenRouterEmbedder{
		apiKey:  apiKey,
		model:   model,
		dims:    dims,
		baseURL: openrouterBaseURL,
		client:  http.DefaultClient,
	}
}

func (e *OpenRouterEmbedder) Name() string   { return "openrouter" }
func (e *OpenRouterEmbedder) Dimensions() int { return e.dims }

func (e *OpenRouterEmbedder) Available(ctx context.Context) bool {
	if e.available != nil {
		return *e.available
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	_, err := e.Embed(ctx, []string{"test"})
	result := err == nil
	e.available = &result
	return result
}

type orEmbedRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type orEmbedResponse struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
		Index     int       `json:"index"`
	} `json:"data"`
}

func (e *OpenRouterEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	req := orEmbedRequest{
		Model: e.model,
		Input: texts,
	}
	reqBody, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal embed request: %w", err)
	}

	respBody, err := doOpenRouterRequest(ctx, e.client, e.baseURL+"/embeddings", e.apiKey, reqBody)
	if err != nil {
		return nil, err
	}

	var resp orEmbedResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("unmarshal embed response: %w", err)
	}
	if len(resp.Data) == 0 {
		return nil, fmt.Errorf("no embeddings in response")
	}

	// Sort by index to match input order
	results := make([][]float32, len(texts))
	for _, d := range resp.Data {
		if d.Index < 0 || d.Index >= len(texts) {
			return nil, fmt.Errorf("invalid embedding index %d", d.Index)
		}
		results[d.Index] = d.Embedding
	}
	return results, nil
}

func (e *OpenRouterEmbedder) ListModels(_ context.Context) ([]ModelInfo, error) {
	return []ModelInfo{{
		ID:         e.model,
		Name:       "Nemotron Embed VL 1B v2",
		Provider:   "openrouter",
		Type:       "embedding",
		Dimensions: e.dims,
		ContextLen: 4096,
		PriceIn:    0,
		PriceOut:   0,
		Local:      false,
	}}, nil
}

// OpenRouterGenerator implements GenerationProvider using OpenRouter's OpenAI-compatible API.
type OpenRouterGenerator struct {
	apiKey    string
	model     string
	baseURL   string
	client    *http.Client
	available *bool
}

// NewOpenRouterGenerator creates an OpenRouter generation provider.
func NewOpenRouterGenerator(apiKey, model string) *OpenRouterGenerator {
	return &OpenRouterGenerator{
		apiKey:  apiKey,
		model:   model,
		baseURL: openrouterBaseURL,
		client:  http.DefaultClient,
	}
}

func (g *OpenRouterGenerator) Name() string { return "openrouter" }

func (g *OpenRouterGenerator) Available(ctx context.Context) bool {
	if g.available != nil {
		return *g.available
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	_, err := g.Generate(ctx, "hi", GenOpts{MaxTokens: 1, Temperature: 0})
	result := err == nil
	g.available = &result
	return result
}

type orChatRequest struct {
	Model       string      `json:"model"`
	MaxTokens   int         `json:"max_tokens,omitempty"`
	Temperature float64     `json:"temperature,omitempty"`
	Messages    []orMessage `json:"messages"`
}

type orMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type orChatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

func (g *OpenRouterGenerator) Generate(ctx context.Context, prompt string, opts GenOpts) (string, error) {
	maxTokens := opts.MaxTokens
	if maxTokens == 0 {
		maxTokens = 512
	}

	messages := []orMessage{}
	if opts.SystemPrompt != "" {
		messages = append(messages, orMessage{Role: "system", Content: opts.SystemPrompt})
	}
	messages = append(messages, orMessage{Role: "user", Content: prompt})

	req := orChatRequest{
		Model:       g.model,
		MaxTokens:   maxTokens,
		Temperature: opts.Temperature,
		Messages:    messages,
	}

	reqBody, err := json.Marshal(req)
	if err != nil {
		return "", fmt.Errorf("marshal generate request: %w", err)
	}

	respBody, err := doOpenRouterRequest(ctx, g.client, g.baseURL+"/chat/completions", g.apiKey, reqBody)
	if err != nil {
		return "", err
	}

	var resp orChatResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return "", fmt.Errorf("unmarshal generate response: %w", err)
	}

	if len(resp.Choices) == 0 {
		return "", fmt.Errorf("empty response from %s", g.model)
	}
	return resp.Choices[0].Message.Content, nil
}

func (g *OpenRouterGenerator) ListModels(_ context.Context) ([]ModelInfo, error) {
	return []ModelInfo{{
		ID:         g.model,
		Name:       g.model,
		Provider:   "openrouter",
		Type:       "generation",
		ContextLen: 128000,
		PriceIn:    0,
		PriceOut:   0,
		Local:      false,
	}}, nil
}

// doOpenRouterRequest sends an HTTP POST to OpenRouter and returns the response body.
func doOpenRouterRequest(ctx context.Context, client *http.Client, url, apiKey string, body []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("HTTP-Referer", "https://github.com/apresai/2ndbrain")
	req.Header.Set("X-Title", "2ndbrain")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("openrouter POST %s: %w", url, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("openrouter %s: status %d: %s", url, resp.StatusCode, string(respBody))
	}
	return respBody, nil
}

// InitOpenRouter creates and registers OpenRouter providers with the given registry.
func InitOpenRouter(ctx context.Context, reg *Registry, cfg OpenRouterConfig, aiCfg AIConfig) error {
	apiKey, err := GetAPIKey("openrouter")
	if err != nil {
		return fmt.Errorf("init openrouter: %w", err)
	}

	embedder := NewOpenRouterEmbedder(apiKey, aiCfg.EmbeddingModel, aiCfg.Dimensions)
	reg.RegisterEmbedder("openrouter", embedder)

	generator := NewOpenRouterGenerator(apiKey, aiCfg.GenerationModel)
	reg.RegisterGenerator("openrouter", generator)

	return nil
}
