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
	ollamaDefaultEndpoint   = "http://localhost:11434"
	ollamaDefaultEmbedModel = "embeddinggemma"
	ollamaDefaultGenModel   = "gemma3:4b"
)

// OllamaEmbedder implements EmbeddingProvider using the Ollama REST API.
type OllamaEmbedder struct {
	endpoint  string
	model     string
	dims      int
	client    *http.Client
	available *bool
}

// NewOllamaEmbedder creates an Ollama embedding provider.
func NewOllamaEmbedder(endpoint, model string, dims int) *OllamaEmbedder {
	if endpoint == "" {
		endpoint = ollamaDefaultEndpoint
	}
	if model == "" {
		model = ollamaDefaultEmbedModel
	}
	return &OllamaEmbedder{
		endpoint: endpoint,
		model:    model,
		dims:     dims,
		client:   &http.Client{Timeout: 120 * time.Second},
	}
}

func (o *OllamaEmbedder) Name() string   { return "ollama" }
func (o *OllamaEmbedder) Dimensions() int { return o.dims }

func (o *OllamaEmbedder) Available(ctx context.Context) bool {
	if o.available != nil {
		return *o.available
	}
	result := ollamaHeartbeat(ctx, o.client, o.endpoint)
	o.available = &result
	return result
}

type ollamaEmbedRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type ollamaEmbedResponse struct {
	Embeddings [][]float64 `json:"embeddings"`
}

func (o *OllamaEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	req := ollamaEmbedRequest{
		Model: o.model,
		Input: texts,
	}
	reqBody, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal embed request: %w", err)
	}

	respBody, err := ollamaPost(ctx, o.client, o.endpoint+"/api/embed", reqBody)
	if err != nil {
		return nil, err
	}

	var resp ollamaEmbedResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("unmarshal embed response: %w", err)
	}
	if len(resp.Embeddings) == 0 {
		return nil, fmt.Errorf("no embeddings in response")
	}

	// Convert float64 to float32
	results := make([][]float32, len(resp.Embeddings))
	for i, emb := range resp.Embeddings {
		vec := make([]float32, len(emb))
		for j, v := range emb {
			vec[j] = float32(v)
		}
		results[i] = vec
	}
	return results, nil
}

func (o *OllamaEmbedder) ListModels(ctx context.Context) ([]ModelInfo, error) {
	return ListOllamaModels(ctx, o.client, o.endpoint)
}

// OllamaGenerator implements GenerationProvider using the Ollama REST API.
type OllamaGenerator struct {
	endpoint  string
	model     string
	client    *http.Client
	available *bool
}

// NewOllamaGenerator creates an Ollama generation provider.
func NewOllamaGenerator(endpoint, model string) *OllamaGenerator {
	if endpoint == "" {
		endpoint = ollamaDefaultEndpoint
	}
	if model == "" {
		model = ollamaDefaultGenModel
	}
	return &OllamaGenerator{
		endpoint: endpoint,
		model:    model,
		client:   &http.Client{Timeout: 120 * time.Second},
	}
}

func (o *OllamaGenerator) Name() string { return "ollama" }

func (o *OllamaGenerator) Available(ctx context.Context) bool {
	if o.available != nil {
		return *o.available
	}
	result := ollamaHeartbeat(ctx, o.client, o.endpoint)
	o.available = &result
	return result
}

type ollamaGenerateRequest struct {
	Model   string              `json:"model"`
	Prompt  string              `json:"prompt"`
	System  string              `json:"system,omitempty"`
	Stream  bool                `json:"stream"`
	Options *ollamaGenOptions   `json:"options,omitempty"`
}

type ollamaGenOptions struct {
	Temperature float64 `json:"temperature,omitempty"`
	NumPredict  int     `json:"num_predict,omitempty"`
}

type ollamaGenerateResponse struct {
	Response string `json:"response"`
	Done     bool   `json:"done"`
}

func (o *OllamaGenerator) Generate(ctx context.Context, prompt string, opts GenOpts) (string, error) {
	req := ollamaGenerateRequest{
		Model:  o.model,
		Prompt: prompt,
		System: opts.SystemPrompt,
		Stream: false,
	}

	genOpts := &ollamaGenOptions{}
	if opts.Temperature > 0 {
		genOpts.Temperature = opts.Temperature
	}
	if opts.MaxTokens > 0 {
		genOpts.NumPredict = opts.MaxTokens
	}
	if genOpts.Temperature > 0 || genOpts.NumPredict > 0 {
		req.Options = genOpts
	}

	reqBody, err := json.Marshal(req)
	if err != nil {
		return "", fmt.Errorf("marshal generate request: %w", err)
	}

	respBody, err := ollamaPost(ctx, o.client, o.endpoint+"/api/generate", reqBody)
	if err != nil {
		return "", err
	}

	var resp ollamaGenerateResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return "", fmt.Errorf("unmarshal generate response: %w", err)
	}

	if resp.Response == "" {
		return "", fmt.Errorf("empty response from %s", o.model)
	}
	return resp.Response, nil
}

func (o *OllamaGenerator) ListModels(ctx context.Context) ([]ModelInfo, error) {
	return ListOllamaModels(ctx, o.client, o.endpoint)
}

// ollamaHeartbeat checks if Ollama is running by hitting the root endpoint.
func ollamaHeartbeat(ctx context.Context, client *http.Client, endpoint string) bool {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"/", nil)
	if err != nil {
		return false
	}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// ollamaPost sends a POST request to an Ollama endpoint and returns the response body.
func ollamaPost(ctx context.Context, client *http.Client, url string, body []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ollama POST %s: %w", url, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ollama %s: status %d: %s", url, resp.StatusCode, string(respBody))
	}
	return respBody, nil
}

// InitOllama creates and registers Ollama providers with the given registry.
func InitOllama(_ context.Context, reg *Registry, cfg OllamaConfig, aiCfg AIConfig) error {
	endpoint := cfg.Endpoint
	if endpoint == "" {
		endpoint = ollamaDefaultEndpoint
	}

	embedder := NewOllamaEmbedder(endpoint, aiCfg.EmbeddingModel, aiCfg.Dimensions)
	reg.RegisterEmbedder("ollama", embedder)

	generator := NewOllamaGenerator(endpoint, aiCfg.GenerationModel)
	reg.RegisterGenerator("ollama", generator)

	return nil
}
