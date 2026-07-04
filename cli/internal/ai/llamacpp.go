package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"time"

	"github.com/apresai/2ndbrain/internal/llama"
)

// llamaProviderName is the registry key for the bundled-engine provider.
const llamaProviderName = "llama-local"

// The llama-local provider drives a local llama.cpp `llama-server` over its
// OpenAI-compatible HTTP API. The server processes are supervised out-of-band by
// the internal/llama manager (an always-on launchd agent); this provider only
// resolves the current per-role endpoint at request time and speaks HTTP to it.
// The Go binary never links llama.cpp, so it stays CGO-free.

// LlamaEmbedder implements EmbeddingProvider against llama-server /v1/embeddings.
type LlamaEmbedder struct {
	model    string
	dims     int
	endpoint string // fixed override (config/tests); empty = resolve via the manager
	client   *http.Client
	avail    availableCache
}

// NewLlamaEmbedder builds the embedding client. endpoint may be empty, in which
// case the base URL is resolved from the running engine's sidecar at call time.
func NewLlamaEmbedder(model string, dims int, endpoint string) *LlamaEmbedder {
	return &LlamaEmbedder{
		model:    model,
		dims:     dims,
		endpoint: endpoint,
		client:   &http.Client{Timeout: 120 * time.Second},
	}
}

func (e *LlamaEmbedder) Name() string    { return llamaProviderName }
func (e *LlamaEmbedder) Dimensions() int { return e.dims }

func (e *LlamaEmbedder) baseURL(ctx context.Context) (string, bool) {
	if e.endpoint != "" {
		return e.endpoint, true
	}
	return llama.EndpointFor(ctx, llama.RoleEmbed)
}

func (e *LlamaEmbedder) Available(ctx context.Context) bool {
	if v, hit := e.avail.get(); hit {
		return v
	}
	_, ok := e.baseURL(ctx)
	e.avail.set(ok)
	return ok
}

// Embed generates embeddings. It honors WithDimension via client-side Matryoshka
// truncation (EmbeddingGemma is trained so a leading prefix of the vector is a
// valid lower-dimensional embedding): the vector is sliced to the requested
// width and L2-renormalized. WithPurpose is currently ignored (EmbeddingGemma is
// a symmetric bi-encoder; task-prefix handling is a tracked follow-up).
func (e *LlamaEmbedder) Embed(ctx context.Context, texts []string, opts ...EmbedOption) ([][]float32, error) {
	base, ok := e.baseURL(ctx)
	if !ok {
		return nil, fmt.Errorf("llama-local embedding engine not running (start it: 2nb ai engine start)")
	}
	cfg := ResolveEmbedOptions(opts...)

	reqBody, err := json.Marshal(orEmbedRequest{Model: e.model, Input: texts})
	if err != nil {
		return nil, fmt.Errorf("marshal embed request: %w", err)
	}
	respBody, err := llamaPost(ctx, e.client, base+"/v1/embeddings", reqBody)
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

	results := make([][]float32, len(texts))
	for _, d := range resp.Data {
		if d.Index < 0 || d.Index >= len(texts) {
			return nil, fmt.Errorf("invalid embedding index %d", d.Index)
		}
		results[d.Index] = d.Embedding
	}

	dim := cfg.Dimension
	if dim == 0 {
		dim = e.dims
	}
	if dim > 0 {
		for i, v := range results {
			results[i] = truncateNormalize(v, dim)
		}
	}
	return results, nil
}

func (e *LlamaEmbedder) ListModels(_ context.Context) ([]ModelInfo, error) {
	return []ModelInfo{{
		ID:         e.model,
		Name:       e.model,
		Provider:   llamaProviderName,
		Type:       "embedding",
		Dimensions: e.dims,
		Local:      true,
	}}, nil
}

// LlamaGenerator implements GenerationProvider (and UsageGenerator) against
// llama-server /v1/chat/completions. llama-server reports OpenAI-style token
// usage, so the RAG observatory records real counts rather than a chars/4
// estimate.
type LlamaGenerator struct {
	model    string
	endpoint string
	client   *http.Client
	avail    availableCache
}

// NewLlamaGenerator builds the generation client.
func NewLlamaGenerator(model, endpoint string) *LlamaGenerator {
	return &LlamaGenerator{
		model:    model,
		endpoint: endpoint,
		client:   &http.Client{Timeout: 120 * time.Second},
	}
}

func (g *LlamaGenerator) Name() string { return llamaProviderName }

func (g *LlamaGenerator) baseURL(ctx context.Context) (string, bool) {
	if g.endpoint != "" {
		return g.endpoint, true
	}
	return llama.EndpointFor(ctx, llama.RoleGen)
}

func (g *LlamaGenerator) Available(ctx context.Context) bool {
	if v, hit := g.avail.get(); hit {
		return v
	}
	_, ok := g.baseURL(ctx)
	g.avail.set(ok)
	return ok
}

type llamaChatRequest struct {
	Model       string      `json:"model"`
	MaxTokens   int         `json:"max_tokens,omitempty"`
	Temperature *float64    `json:"temperature,omitempty"`
	Messages    []orMessage `json:"messages"`
}

type llamaChatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
}

func (g *LlamaGenerator) Generate(ctx context.Context, prompt string, opts GenOpts) (string, error) {
	text, _, err := g.generate(ctx, prompt, opts)
	return text, err
}

// GenerateWithUsage implements UsageGenerator.
func (g *LlamaGenerator) GenerateWithUsage(ctx context.Context, prompt string, opts GenOpts) (string, GenUsage, error) {
	return g.generate(ctx, prompt, opts)
}

func (g *LlamaGenerator) generate(ctx context.Context, prompt string, opts GenOpts) (string, GenUsage, error) {
	base, ok := g.baseURL(ctx)
	if !ok {
		return "", GenUsage{}, fmt.Errorf("llama-local generation engine not running (start it: 2nb ai engine start)")
	}

	maxTokens := opts.MaxTokens
	if maxTokens == 0 {
		maxTokens = 512
	}
	messages := make([]orMessage, 0, 2)
	if opts.SystemPrompt != "" {
		messages = append(messages, orMessage{Role: "system", Content: opts.SystemPrompt})
	}
	messages = append(messages, orMessage{Role: "user", Content: prompt})

	reqBody, err := json.Marshal(llamaChatRequest{
		Model:       g.model,
		MaxTokens:   maxTokens,
		Temperature: opts.Temperature,
		Messages:    messages,
	})
	if err != nil {
		return "", GenUsage{}, fmt.Errorf("marshal generate request: %w", err)
	}

	respBody, err := llamaPost(ctx, g.client, base+"/v1/chat/completions", reqBody)
	if err != nil {
		return "", GenUsage{}, err
	}

	var resp llamaChatResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return "", GenUsage{}, fmt.Errorf("unmarshal generate response: %w", err)
	}
	if len(resp.Choices) == 0 {
		return "", GenUsage{}, fmt.Errorf("empty response from %s", g.model)
	}
	usage := GenUsage{InputTokens: resp.Usage.PromptTokens, OutputTokens: resp.Usage.CompletionTokens}
	return resp.Choices[0].Message.Content, usage, nil
}

func (g *LlamaGenerator) ListModels(_ context.Context) ([]ModelInfo, error) {
	return []ModelInfo{{
		ID:       g.model,
		Name:     g.model,
		Provider: llamaProviderName,
		Type:     "generation",
		Local:    true,
	}}, nil
}

// InitLlama creates and registers the llama-local providers.
func InitLlama(_ context.Context, reg *Registry, cfg LlamaConfig, aiCfg AIConfig) error {
	embedder := NewLlamaEmbedder(aiCfg.EmbeddingModel, aiCfg.Dimensions, cfg.EmbedEndpoint)
	reg.RegisterEmbedder(llamaProviderName, embedder)

	generator := NewLlamaGenerator(aiCfg.GenerationModel, cfg.GenEndpoint)
	reg.RegisterGenerator(llamaProviderName, generator)

	return nil
}

// llamaPost sends a JSON POST to a localhost llama-server endpoint (no auth) and
// returns the response body.
func llamaPost(ctx context.Context, client *http.Client, url string, body []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("llama-local POST %s: %w", url, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("llama-local %s: status %d: %s", url, resp.StatusCode, string(respBody))
	}
	return respBody, nil
}

// truncateNormalize returns the first dim components of v, L2-renormalized. For
// dim >= len(v) it returns v unchanged (a Matryoshka model can't grow a vector).
// Cosine similarity is scale-invariant, but renormalizing keeps stored vectors
// unit-length so any future consumer stays correct.
func truncateNormalize(v []float32, dim int) []float32 {
	if dim <= 0 || dim >= len(v) {
		return v
	}
	out := make([]float32, dim)
	copy(out, v[:dim])
	var norm float64
	for _, x := range out {
		norm += float64(x) * float64(x)
	}
	if norm == 0 {
		return out
	}
	inv := float32(1.0 / math.Sqrt(norm))
	for i := range out {
		out[i] *= inv
	}
	return out
}
