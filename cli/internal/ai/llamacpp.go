package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/apresai/2ndbrain/internal/llama"
)

// llamaProviderName is the registry key for the bundled-engine provider.
const llamaProviderName = "llama-local"

// DefaultLlamaEmbedModel is the local embedder used when ai.embedding_model is
// unset on the llama-local provider.
const DefaultLlamaEmbedModel = llama.DefaultEmbedModelID

// DefaultLlamaRerankModel is the local cross-encoder used when ai.rerank.model
// is unset on the llama-local provider (the global DefaultRerankModel is the
// Bedrock Cohere id, which llama-server cannot serve).
const DefaultLlamaRerankModel = "bge-reranker-v2-m3"

// The llama-local provider drives a local llama.cpp `llama-server` over its
// OpenAI-compatible HTTP API. The server processes are supervised out-of-band by
// the internal/llama manager (an always-on launchd agent); this provider only
// resolves the current per-role endpoint at request time and speaks HTTP to it.
// The Go binary never links llama.cpp, so it stays CGO-free.

// LlamaEmbedder implements EmbeddingProvider against llama-server /v1/embeddings.
type LlamaEmbedder struct {
	model      string
	dims       int
	taskPreset string
	endpoint   string // fixed override (config/tests); empty = resolve via the manager
	client     *http.Client
	avail      availableCache
}

// NewLlamaEmbedder builds the embedding client. endpoint may be empty, in which
// case the base URL is resolved from the running engine's sidecar at call time.
func NewLlamaEmbedder(model string, dims int, taskPreset string, endpoint string) *LlamaEmbedder {
	return &LlamaEmbedder{
		model:      model,
		dims:       dims,
		taskPreset: strings.ToLower(strings.TrimSpace(taskPreset)),
		endpoint:   endpoint,
		client:     &http.Client{Timeout: localAttemptTimeout},
	}
}

func (e *LlamaEmbedder) Name() string       { return llamaProviderName }
func (e *LlamaEmbedder) Dimensions() int    { return e.dims }
func (e *LlamaEmbedder) TaskPreset() string { return e.taskPreset }

func probeEndpointURL(ctx context.Context, endpoint string) bool {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	url := strings.TrimRight(endpoint, "/") + "/health"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func (e *LlamaEmbedder) baseURL(ctx context.Context) (string, bool) {
	if e.endpoint != "" {
		return e.endpoint, true
	}
	return llama.EndpointFor(ctx, llama.RoleEmbed)
}

func (e *LlamaEmbedder) ensureBaseURL(ctx context.Context) (string, error) {
	if e.endpoint != "" {
		return e.endpoint, nil
	}
	model := e.model
	if model == "" {
		model = DefaultLlamaEmbedModel
	}
	return llama.EnsureEndpointFor(ctx, llama.RoleEmbed, model)
}

func (e *LlamaEmbedder) Available(ctx context.Context) bool {
	if v, hit := e.avail.get(); hit {
		return v
	}
	if e.endpoint != "" {
		ok := probeEndpointURL(ctx, e.endpoint)
		e.avail.set(ok)
		return ok
	}
	if _, ok := llama.EndpointFor(ctx, llama.RoleEmbed); ok {
		e.avail.set(true)
		return true
	}
	model := e.model
	if model == "" {
		model = DefaultLlamaEmbedModel
	}
	ok := llama.RoleCanStart(llama.RoleEmbed, model)
	e.avail.set(ok)
	return ok
}

// formatLlamaEmbedText applies EmbeddingGemma task instruction prefixes.
// - Query (PurposeQuery or PurposeQueryText): applies active taskPreset (search, code, qa, none, symmetric).
//   Avoids double prefixing only if input starts with "task:" AND contains " | query:".
// - Document (PurposeIndex or default): "title: " + (title != "" ? title : "none") + " | text: " + chunk.
//   Avoids double formatting only if input starts with "title:" AND contains " | text:".
func formatLlamaEmbedText(text string, cfg EmbedConfig, taskPreset string) string {
	trimmed := strings.TrimSpace(text)
	switch cfg.Purpose {
	case PurposeQuery, PurposeQueryText:
		if strings.HasPrefix(strings.ToLower(trimmed), "task:") && strings.Contains(text, " | query:") {
			return text
		}
		preset := strings.ToLower(strings.TrimSpace(taskPreset))
		switch preset {
		case "code":
			return "task: code retrieval | query: " + text
		case "qa":
			return "task: question answering | query: " + text
		case "none", "symmetric":
			return text
		case "search", "":
			fallthrough
		default:
			return "task: search result | query: " + text
		}
	case PurposeIndex:
		fallthrough
	default:
		if strings.HasPrefix(strings.ToLower(trimmed), "title:") && strings.Contains(text, " | text:") {
			return text
		}
		title := cfg.Title
		if title == "" {
			title = "none"
		}
		return "title: " + title + " | text: " + text
	}
}

const (
	llamaEmbedMaxBatchSize  = 8
	maxLlamaEmbedInputRunes = 4500
	maxLlamaEmbedBatchRunes = 4500
)

// capLlamaEmbedRunes truncates s cleanly on a UTF-8 rune boundary only when
// utf8.RuneCountInString(s) > maxRunes, preventing oversized chunks from
// overflowing llama-server's embedding context.
func capLlamaEmbedRunes(s string, maxRunes int) string {
	if maxRunes <= 0 || utf8.RuneCountInString(s) <= maxRunes {
		return s
	}
	count := 0
	for i := range s {
		if count == maxRunes {
			return s[:i]
		}
		count++
	}
	return s
}

// partitionLlamaEmbedBatches returns contiguous [start, end) ranges over
// formatted so each batch has at most maxItems items and total rune count
// <= maxRunes. Each batch always advances by at least one item so an
// individually long input still runs.
func partitionLlamaEmbedBatches(formatted []string, maxItems, maxRunes int) [][2]int {
	if len(formatted) == 0 {
		return nil
	}
	if maxItems <= 0 {
		maxItems = llamaEmbedMaxBatchSize
	}
	if maxRunes <= 0 {
		maxRunes = maxLlamaEmbedBatchRunes
	}
	var batches [][2]int
	start := 0
	batchRunes := 0
	for i, s := range formatted {
		runes := utf8.RuneCountInString(s)
		if i > start && (i-start >= maxItems || batchRunes+runes > maxRunes) {
			batches = append(batches, [2]int{start, i})
			start = i
			batchRunes = 0
		}
		batchRunes += runes
	}
	if start < len(formatted) {
		batches = append(batches, [2]int{start, len(formatted)})
	}
	return batches
}

type llamaPostError struct {
	err error
}

func (e *llamaPostError) Error() string { return e.err.Error() }
func (e *llamaPostError) Unwrap() error { return e.err }

func (e *LlamaEmbedder) embedBatch(ctx context.Context, embedURL string, batch []string, dst [][]float32) error {
	reqBody, err := json.Marshal(orEmbedRequest{Model: e.model, Input: batch})
	if err != nil {
		return fmt.Errorf("marshal embed request: %w", err)
	}
	respBody, err := llamaPost(ctx, e.client, embedURL, reqBody)
	if err != nil {
		return &llamaPostError{err: err}
	}

	var resp orEmbedResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return fmt.Errorf("unmarshal embed response: %w", err)
	}
	if len(resp.Data) == 0 {
		return fmt.Errorf("no embeddings in response")
	}

	for _, d := range resp.Data {
		if d.Index < 0 || d.Index >= len(batch) {
			return fmt.Errorf("invalid embedding index %d in batch", d.Index)
		}
	}
	for _, d := range resp.Data {
		dst[d.Index] = d.Embedding
	}
	return nil
}

// Embed generates embeddings. It formats task instruction prefixes
// (search query vs. document), caps overly long inputs on rune boundaries,
// partitions batches within llama-server's item and rune budgets (falling
// back one-by-one if a multi-item batch overflows on dense tokens), maps
// responses 1:1 back into original order, and honors WithDimension via
// client-side Matryoshka truncation with L2 re-normalization.
func (e *LlamaEmbedder) Embed(ctx context.Context, texts []string, opts ...EmbedOption) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	base, err := e.ensureBaseURL(ctx)
	if err != nil {
		return nil, err
	}
	llama.TouchRoleActivity(llama.RoleEmbed)
	cfg := ResolveEmbedOptions(opts...)

	formatted := make([]string, len(texts))
	for i, t := range texts {
		formatted[i] = capLlamaEmbedRunes(formatLlamaEmbedText(t, cfg, e.taskPreset), maxLlamaEmbedInputRunes)
	}

	results := make([][]float32, len(texts))
	embedURL := base + "/v1/embeddings"

	for _, b := range partitionLlamaEmbedBatches(formatted, llamaEmbedMaxBatchSize, maxLlamaEmbedBatchRunes) {
		start, end := b[0], b[1]
		if err := e.embedBatch(ctx, embedURL, formatted[start:end], results[start:end]); err != nil {
			var postErr *llamaPostError
			if end-start > 1 && errors.As(err, &postErr) && ctx.Err() == nil {
				for i := start; i < end; i++ {
					if singleErr := e.embedBatch(ctx, embedURL, formatted[i:i+1], results[i:i+1]); singleErr != nil {
						return nil, singleErr
					}
				}
				continue
			}
			return nil, err
		}
	}

	// A well-behaved server returns one vector per input; guard against a short
	// or gappy response so we never store a nil/empty embedding (which would
	// silently corrupt the index).
	for i, v := range results {
		if v == nil {
			return nil, fmt.Errorf("embedding server returned incomplete vectors for %d inputs (missing index %d)", len(texts), i)
		}
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
		client:   &http.Client{Timeout: localAttemptTimeout},
	}
}

func (g *LlamaGenerator) Name() string { return llamaProviderName }

func (g *LlamaGenerator) baseURL(ctx context.Context) (string, bool) {
	if g.endpoint != "" {
		return g.endpoint, true
	}
	return llama.EndpointFor(ctx, llama.RoleGen)
}

func (g *LlamaGenerator) ensureBaseURL(ctx context.Context) (string, error) {
	if g.endpoint != "" {
		return g.endpoint, nil
	}
	model := g.model
	if model == "" {
		model = "gemma4-e2b"
	}
	return llama.EnsureEndpointFor(ctx, llama.RoleGen, model)
}

func (g *LlamaGenerator) Available(ctx context.Context) bool {
	if v, hit := g.avail.get(); hit {
		return v
	}
	if g.endpoint != "" {
		ok := probeEndpointURL(ctx, g.endpoint)
		g.avail.set(ok)
		return ok
	}
	if _, ok := llama.EndpointFor(ctx, llama.RoleGen); ok {
		g.avail.set(true)
		return true
	}
	model := g.model
	if model == "" {
		model = "gemma4-e2b"
	}
	ok := llama.RoleCanStart(llama.RoleGen, model)
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
			Content          string `json:"content"`
			ReasoningContent string `json:"reasoning_content"`
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
	base, err := g.ensureBaseURL(ctx)
	if err != nil {
		return "", GenUsage{}, err
	}
	llama.TouchRoleActivity(llama.RoleGen)

	maxTokens := opts.MaxTokens
	if maxTokens == 0 {
		maxTokens = 1024
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
	msg := resp.Choices[0].Message
	content := msg.Content
	if strings.TrimSpace(content) == "" && strings.TrimSpace(msg.ReasoningContent) != "" {
		content = msg.ReasoningContent
	}
	if strings.TrimSpace(content) == "" {
		return "", GenUsage{}, fmt.Errorf("empty response from %s", g.model)
	}
	usage := GenUsage{InputTokens: resp.Usage.PromptTokens, OutputTokens: resp.Usage.CompletionTokens}
	return content, usage, nil
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

// LlamaReranker implements ai.RerankProvider against llama-server /v1/rerank (a
// server started with --reranking --pooling rank). A cross-encoder scores the
// query and each document jointly, so it consumes raw text (decoupled from the
// embedder). It is the LOCAL counterpart to BedrockReranker; internal/retrieve
// resolves it from the active provider, so it only runs when the vault is on the
// llama-local provider with ai.rerank.enabled.
type LlamaReranker struct {
	model    string
	endpoint string
	client   *http.Client
	avail    availableCache
}

// NewLlamaReranker builds the rerank client.
func NewLlamaReranker(model, endpoint string) *LlamaReranker {
	return &LlamaReranker{
		model:    model,
		endpoint: endpoint,
		client:   &http.Client{Timeout: localAttemptTimeout},
	}
}

func (r *LlamaReranker) Name() string { return llamaProviderName }

func (r *LlamaReranker) baseURL(ctx context.Context) (string, bool) {
	if r.endpoint != "" {
		return r.endpoint, true
	}
	return llama.EndpointFor(ctx, llama.RoleRerank)
}

func (r *LlamaReranker) ensureBaseURL(ctx context.Context) (string, error) {
	if r.endpoint != "" {
		return r.endpoint, nil
	}
	model := r.model
	if model == "" {
		model = DefaultLlamaRerankModel
	}
	return llama.EnsureEndpointFor(ctx, llama.RoleRerank, model)
}

func (r *LlamaReranker) Available(ctx context.Context) bool {
	if v, hit := r.avail.get(); hit {
		return v
	}
	if r.endpoint != "" {
		ok := probeEndpointURL(ctx, r.endpoint)
		r.avail.set(ok)
		return ok
	}
	if _, ok := llama.EndpointFor(ctx, llama.RoleRerank); ok {
		r.avail.set(true)
		return true
	}
	model := r.model
	if model == "" {
		model = DefaultLlamaRerankModel
	}
	ok := llama.RoleCanStart(llama.RoleRerank, model)
	r.avail.set(ok)
	return ok
}

type llamaRerankRequest struct {
	Model     string   `json:"model"`
	Query     string   `json:"query"`
	Documents []string `json:"documents"`
	TopN      int      `json:"top_n,omitempty"`
}

type llamaRerankResponse struct {
	Results []struct {
		Index          int     `json:"index"`
		RelevanceScore float64 `json:"relevance_score"`
	} `json:"results"`
}

// Rerank scores each document against the query with the cross-encoder and
// returns the hits (topN <= 0 means all). Indices refer back into docs.
func (r *LlamaReranker) Rerank(ctx context.Context, query string, docs []string, topN int) ([]RerankHit, error) {
	if len(docs) == 0 {
		return nil, nil
	}
	base, err := r.ensureBaseURL(ctx)
	if err != nil {
		return nil, err
	}
	llama.TouchRoleActivity(llama.RoleRerank)
	body, err := json.Marshal(llamaRerankRequest{Model: r.model, Query: query, Documents: docs, TopN: topN})
	if err != nil {
		return nil, fmt.Errorf("marshal rerank request: %w", err)
	}
	respBody, err := llamaPost(ctx, r.client, base+"/v1/rerank", body)
	if err != nil {
		return nil, err
	}
	var resp llamaRerankResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("unmarshal rerank response: %w", err)
	}
	hits := make([]RerankHit, 0, len(resp.Results))
	for _, d := range resp.Results {
		if d.Index < 0 || d.Index >= len(docs) {
			return nil, fmt.Errorf("invalid rerank index %d", d.Index)
		}
		hits = append(hits, RerankHit{Index: d.Index, Score: d.RelevanceScore})
	}
	return hits, nil
}

var _ RerankProvider = (*LlamaReranker)(nil)

// llamaRerankModel resolves the local rerank model: the configured id, else the
// local default (never the Bedrock DefaultRerankModel, which llama-server can't
// serve).
func llamaRerankModel(aiCfg AIConfig) string {
	if aiCfg.Rerank.Model != "" {
		return aiCfg.Rerank.Model
	}
	return DefaultLlamaRerankModel
}

// InitLlama creates and registers the llama-local providers.
func InitLlama(_ context.Context, reg *Registry, cfg LlamaConfig, aiCfg AIConfig) error {
	embedModel, embedDims := DefaultLlamaEmbedModel, 768
	if ep := aiCfg.EffectiveEmbeddingProvider(); ep == "" || ep == llamaProviderName {
		if aiCfg.EmbeddingModel != "" {
			embedModel = aiCfg.EmbeddingModel
		}
		if aiCfg.Dimensions > 0 {
			embedDims = aiCfg.Dimensions
		}
	}
	embedder := NewLlamaEmbedder(embedModel, embedDims, aiCfg.ResolveTaskPreset(), cfg.EmbedEndpoint)
	reg.RegisterEmbedder(llamaProviderName, embedder)

	genModel := "gemma4-e2b"
	if gp := aiCfg.EffectiveGenerationProvider(); gp == "" || gp == llamaProviderName {
		if aiCfg.GenerationModel != "" {
			genModel = aiCfg.GenerationModel
		}
	}
	generator := NewLlamaGenerator(genModel, cfg.GenEndpoint)
	reg.RegisterGenerator(llamaProviderName, generator)

	// Register the local reranker only when reranking is enabled and routed to
	// llama-local, mirroring InitBedrock. Default to the local cross-encoder,
	// not the shared DefaultRerankModel (a Bedrock id).
	if aiCfg.RerankEnabled() {
		if rp := aiCfg.EffectiveRerankProvider(); rp == "" || rp == llamaProviderName {
			reg.RegisterReranker(llamaProviderName, NewLlamaReranker(llamaRerankModel(aiCfg), cfg.RerankEndpoint))
		}
	}

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
