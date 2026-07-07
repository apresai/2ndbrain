package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/bedrock"
	bedrocktypes "github.com/aws/aws-sdk-go-v2/service/bedrock/types"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
)

// bedrockAvailableProbe runs the control-plane ListFoundationModels call.
// This is free, doesn't consume embedding/generation tokens, and exercises
// both credentials and Bedrock enablement in the configured region. Used
// by BedrockEmbedder.Available and BedrockGenerator.Available.
//
// The probe verifies the provider is reachable and credentials are valid,
// but NOT that the user's configured model is callable — a typo'd model ID
// still lets Available() return true and fails at first real Embed/Generate.
// For full per-model validation, use `2nb models test`.
func bedrockAvailableProbe(ctx context.Context, ctrl *bedrock.Client) bool {
	if ctrl == nil {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if _, err := ctrl.ListFoundationModels(ctx, &bedrock.ListFoundationModelsInput{}); err != nil {
		slog.Debug("bedrock probe failed", "err", err)
		return false
	}
	return true
}

// bedrockBearerTokenEnv is the environment variable the AWS SDK reads for the
// Amazon Bedrock API key (bearer token).
const bedrockBearerTokenEnv = "AWS_BEARER_TOKEN_BEDROCK"

// ensureBedrockBearerToken makes a Keychain-stored Bedrock API key visible to
// the AWS SDK by exporting it as AWS_BEARER_TOKEN_BEDROCK when that var is unset.
// A GUI app that spawns 2nb has no shell environment, so without this its only
// credential source would be ~/.aws (SigV4). When the env var is already set, or
// no token is stored, this is a no-op. Accessors are injected so the logic is
// testable without touching the real environment or Keychain.
func ensureBedrockBearerToken(getenv func(string) string, setenv func(string, string) error, keychain func(string) (string, error)) {
	if getenv(bedrockBearerTokenEnv) != "" {
		return
	}
	if token, err := keychain("bedrock"); err == nil && token != "" {
		_ = setenv(bedrockBearerTokenEnv, token)
		// Make the source explicit: this overrides SigV4 for Bedrock (the SDK
		// prefers a bearer token), so a stale stored key could mask working
		// SigV4 creds. Visible in cli.log for diagnosis.
		slog.Debug("bedrock: using API key from macOS Keychain", "env", bedrockBearerTokenEnv)
	}
}

// loadBedrockAWSConfig builds an AWS config from BedrockConfig settings.
func loadBedrockAWSConfig(ctx context.Context, cfg BedrockConfig) (aws.Config, error) {
	if runtime.GOOS == "darwin" {
		ensureBedrockBearerToken(os.Getenv, os.Setenv, keychainGet)
	}
	opts := []func(*awsconfig.LoadOptions) error{
		awsconfig.WithRegion(cfg.Region),
	}
	if cfg.Profile != "" && cfg.Profile != "default" {
		opts = append(opts, awsconfig.WithSharedConfigProfile(cfg.Profile))
	}
	return awsconfig.LoadDefaultConfig(ctx, opts...)
}

// BedrockEmbedder implements EmbeddingProvider using Amazon Bedrock.
type BedrockEmbedder struct {
	client *bedrockruntime.Client
	ctrl   *bedrock.Client // control plane for lightweight Available() probe
	model  string
	dims   int
	region string
	// strategy is the model's declared InvokeStrategy from the builtin
	// catalog, resolved once at construction. Empty when the model isn't
	// in the catalog or the catalog entry doesn't declare one, in which
	// case Embed falls back to detectEmbedFormat model-ID matching.
	strategy string
	avail    availableCache
}

// NewBedrockEmbedder creates a Bedrock embedding provider.
func NewBedrockEmbedder(ctx context.Context, cfg BedrockConfig, model string, dims int) (*BedrockEmbedder, error) {
	awsCfg, err := loadBedrockAWSConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("load AWS config: %w", err)
	}

	return &BedrockEmbedder{
		client:   bedrockruntime.NewFromConfig(awsCfg),
		ctrl:     bedrock.NewFromConfig(awsCfg),
		model:    model,
		dims:     dims,
		region:   cfg.Region,
		strategy: ResolveInvokeStrategy("bedrock", model, ""),
	}, nil
}

func (b *BedrockEmbedder) Name() string    { return "bedrock" }
func (b *BedrockEmbedder) Dimensions() int { return b.dims }

func (b *BedrockEmbedder) Available(ctx context.Context) bool {
	if v, hit := b.avail.get(); hit {
		return v
	}
	ok := bedrockAvailableProbe(ctx, b.ctrl)
	b.avail.set(ok)
	return ok
}

// ── Embedding request/response structs ────────────────────────────────────

// Nova Embeddings v2
type novaEmbedRequest struct {
	TaskType              string               `json:"taskType"`
	SingleEmbeddingParams *novaEmbeddingParams `json:"singleEmbeddingParams"`
}

type novaEmbeddingParams struct {
	EmbeddingPurpose   string         `json:"embeddingPurpose"`
	EmbeddingDimension int            `json:"embeddingDimension"`
	Text               *novaTextInput `json:"text,omitempty"`
}

type novaTextInput struct {
	TruncationMode string `json:"truncationMode"`
	Value          string `json:"value"`
}

type novaEmbedResponse struct {
	Embeddings []struct {
		EmbeddingType string    `json:"embeddingType"`
		Embedding     []float32 `json:"embedding"`
		// TruncatedCharLength is set by Nova only when the tokenized input
		// exceeded the model's context and was truncated (per truncationMode);
		// it names the character after which the text was dropped. Absent on a
		// normal response.
		TruncatedCharLength *int `json:"truncatedCharLength,omitempty"`
	} `json:"embeddings"`
}

// Titan Text Embeddings v1 — fixed 1536 dims, per-text
type titanV1EmbedRequest struct {
	InputText string `json:"inputText"`
}

type titanV1EmbedResponse struct {
	Embedding []float32 `json:"embedding"`
}

// Titan Text Embeddings v2 — configurable dims (256/512/1024), per-text
type titanV2EmbedRequest struct {
	InputText      string   `json:"inputText"`
	Dimensions     int      `json:"dimensions,omitempty"`
	Normalize      bool     `json:"normalize"`
	EmbeddingTypes []string `json:"embeddingTypes"`
}

type titanV2EmbedResponse struct {
	EmbeddingsByType struct {
		Float []float32 `json:"float"`
	} `json:"embeddingsByType"`
}

// Cohere Embed — batched (≤96 texts per call), fixed 1024 dims.
// v3 response: {"embeddings": [[...],...]}
// v4 response: {"embeddings": {"float": [[...],...]}
type cohereEmbedRequest struct {
	Texts     []string `json:"texts"`
	InputType string   `json:"input_type"`
	Truncate  string   `json:"truncate,omitempty"`
}

type twelvelabs27EmbedRequest struct {
	InputType string `json:"inputType"`
	InputText string `json:"inputText"`
}

type twelvelabs30EmbedRequest struct {
	InputType string `json:"inputType"`
	Text      struct {
		InputText string `json:"inputText"`
	} `json:"text"`
}

type twelvelabsEmbedEnvelope struct {
	Embedding []float32       `json:"embedding"`
	Data      json.RawMessage `json:"data"`
}

type twelvelabsEmbedItem struct {
	Embedding []float32 `json:"embedding"`
}

// parseCohereEmbeddings decodes either the v3 flat array or the v4 typed object.
func parseCohereEmbeddings(body []byte) ([][]float32, error) {
	var v3 struct {
		Embeddings [][]float32 `json:"embeddings"`
	}
	if err := json.Unmarshal(body, &v3); err == nil && len(v3.Embeddings) > 0 {
		return v3.Embeddings, nil
	}
	var v4 struct {
		Embeddings struct {
			Float [][]float32 `json:"float"`
		} `json:"embeddings"`
	}
	if err := json.Unmarshal(body, &v4); err == nil && len(v4.Embeddings.Float) > 0 {
		return v4.Embeddings.Float, nil
	}
	return nil, fmt.Errorf("unrecognized Cohere embed response format")
}

// ── Embedding format detection ─────────────────────────────────────────────

type bedrockEmbedFmt int

const (
	fmtNova         bedrockEmbedFmt = iota
	fmtTitanV1                      // simple {"inputText":...} → {"embedding":[...]}
	fmtTitanV2                      // {"inputText":..., "dimensions":N, ...} → {"embeddingsByType":...}
	fmtTitanImage                   // image-only model, not supported for text embedding
	fmtCohere                       // batched {"texts":[...]} → {"embeddings":[[...],...]}
	fmtTwelveLabs27                 // {"inputType":"text","inputText":"..."} → {"embedding":[...]}
	fmtTwelveLabs30                 // {"inputType":"text","text":{"inputText":"..."}} → {"data":{"embedding":[...]}}
)

func detectEmbedFormat(modelID string) bedrockEmbedFmt {
	// Strip inference profile geo prefix (us./eu./ap./global.) before matching.
	lower := strings.ToLower(inferenceProfileBaseID(modelID))
	switch {
	case strings.HasPrefix(lower, "cohere.embed-"):
		return fmtCohere
	case strings.Contains(lower, "marengo-embed-2-7"):
		return fmtTwelveLabs27
	case strings.Contains(lower, "marengo-embed-3-0"):
		return fmtTwelveLabs30
	case lower == "amazon.titan-embed-text-v1":
		return fmtTitanV1
	case strings.HasPrefix(lower, "amazon.titan-embed-g1-"):
		return fmtTitanV1
	case strings.HasPrefix(lower, "amazon.titan-embed-image-"):
		return fmtTitanImage
	case strings.HasPrefix(lower, "amazon.titan-embed-"):
		return fmtTitanV2
	default:
		return fmtNova
	}
}

// ── Embed dispatch ─────────────────────────────────────────────────────────

func (b *BedrockEmbedder) Embed(ctx context.Context, texts []string, opts ...EmbedOption) ([][]float32, error) {
	cfg := ResolveEmbedOptions(opts...)
	format := b.resolvedEmbedFormat()
	switch format {
	case fmtCohere:
		return b.embedCohere(ctx, texts)
	case fmtTwelveLabs27:
		return b.embedTwelveLabs27(ctx, texts)
	case fmtTwelveLabs30:
		return b.embedTwelveLabs30(ctx, texts)
	case fmtTitanV1:
		return b.embedTitanV1(ctx, texts)
	case fmtTitanV2:
		return b.embedTitanV2(ctx, texts)
	case fmtTitanImage:
		return nil, fmt.Errorf("titan image embedding model requires image input — text embedding not supported")
	default:
		return b.embedNova(ctx, texts, cfg)
	}
}

// resolvedEmbedFormat prefers the cached catalog-declared InvokeStrategy
// when set. Titan v1 can't be distinguished from Titan v2 by strategy
// alone (both use StrategyBedrockInvokeTitanEmbed), so we fall through
// to detectEmbedFormat for the v1 exact-match path. All other strategies
// map 1:1 to a format.
func (b *BedrockEmbedder) resolvedEmbedFormat() bedrockEmbedFmt {
	if b.strategy != "" {
		if f, ok := bedrockEmbedFormatFromStrategy(b.strategy); ok {
			if f == fmtTitanV2 {
				return detectEmbedFormat(b.model)
			}
			return f
		}
	}
	return detectEmbedFormat(b.model)
}

func (b *BedrockEmbedder) invokeModel(ctx context.Context, reqBody []byte) ([]byte, error) {
	var (
		resp *bedrockruntime.InvokeModelOutput
		err  error
	)
	for attempt := 1; attempt <= maxBedrockAttempts; attempt++ {
		resp, err = b.client.InvokeModel(ctx, &bedrockruntime.InvokeModelInput{
			ModelId:     aws.String(b.model),
			ContentType: aws.String("application/json"),
			Accept:      aws.String("application/json"),
			Body:        reqBody,
		})
		if err == nil {
			return resp.Body, nil
		}
		if !isBedrockRetryable(err) || attempt == maxBedrockAttempts {
			break
		}
		// Wait the backoff, but honor cancellation: a client disconnect / timeout
		// mid-backoff should abort promptly instead of sleeping out the full delay.
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(bedrockRetryDelay(attempt)):
		}
	}
	return nil, fmt.Errorf("invoke %s: %w", b.model, err)
}

// novaEmbeddingPurpose maps a generic EmbedConfig purpose to Nova-2's
// asymmetric embedding purpose: a stored document uses GENERIC_INDEX, a
// search query uses GENERIC_RETRIEVAL.
func novaEmbeddingPurpose(purpose string) string {
	switch purpose {
	case PurposeQuery:
		return "GENERIC_RETRIEVAL"
	case PurposeQueryText:
		return "TEXT_RETRIEVAL"
	default:
		return "GENERIC_INDEX"
	}
}

// IsAsymmetricEmbeddingModel reports whether a model embeds queries and
// documents with different purposes (Nova's GENERIC_RETRIEVAL vs GENERIC_INDEX).
// For such models the search-time distribution is query->document, so a
// threshold calibrated on document<->document cosines — or carried over from the
// old symmetric behavior — does not reflect real retrieval and reads too high.
func IsAsymmetricEmbeddingModel(model string) bool {
	return strings.Contains(model, "nova-2-multimodal-embeddings")
}

func (b *BedrockEmbedder) embedNova(ctx context.Context, texts []string, cfg EmbedConfig) ([][]float32, error) {
	purpose := novaEmbeddingPurpose(cfg.Purpose)
	// Matryoshka: honor a per-call dimension override, else the vault default.
	dim := b.dims
	if cfg.Dimension > 0 {
		dim = cfg.Dimension
	}
	results := make([][]float32, len(texts))
	for i, text := range texts {
		req := novaEmbedRequest{
			TaskType: "SINGLE_EMBEDDING",
			SingleEmbeddingParams: &novaEmbeddingParams{
				EmbeddingPurpose:   purpose,
				EmbeddingDimension: dim,
				Text: &novaTextInput{
					TruncationMode: "END",
					Value:          text,
				},
			},
		}
		reqBody, err := json.Marshal(req)
		if err != nil {
			return nil, fmt.Errorf("marshal embed request: %w", err)
		}
		body, err := b.invokeModel(ctx, reqBody)
		if err != nil {
			return nil, err
		}
		var embedResp novaEmbedResponse
		if err := json.Unmarshal(body, &embedResp); err != nil {
			return nil, fmt.Errorf("unmarshal embed response: %w", err)
		}
		if len(embedResp.Embeddings) == 0 {
			return nil, fmt.Errorf("no embeddings in response for text %d", i)
		}
		// Nova truncates (per truncationMode END) when the tokenized input
		// exceeds the model's 8192-token context, dropping the tail from the
		// vector. It signals this via truncatedCharLength. Surface it: with
		// chunk-size capping in place this should never fire, so a hit is a
		// regression tripwire that an oversized chunk slipped through.
		if tc := embedResp.Embeddings[0].TruncatedCharLength; tc != nil && *tc > 0 {
			// input_chars is a rune count so it is directly comparable to Nova's
			// truncated_at_char (the character position it cut at), not bytes.
			slog.Warn("nova truncated an over-long input (tail dropped from the embedding); a chunk exceeded the 8192-token context",
				"input_chars", utf8.RuneCountInString(text), "truncated_at_char", *tc, "model", b.model)
		}
		results[i] = embedResp.Embeddings[0].Embedding
	}
	return results, nil
}

func (b *BedrockEmbedder) embedTitanV1(ctx context.Context, texts []string) ([][]float32, error) {
	results := make([][]float32, len(texts))
	for i, text := range texts {
		reqBody, err := json.Marshal(titanV1EmbedRequest{InputText: text})
		if err != nil {
			return nil, fmt.Errorf("marshal embed request: %w", err)
		}
		body, err := b.invokeModel(ctx, reqBody)
		if err != nil {
			return nil, err
		}
		var embedResp titanV1EmbedResponse
		if err := json.Unmarshal(body, &embedResp); err != nil {
			return nil, fmt.Errorf("unmarshal embed response: %w", err)
		}
		if len(embedResp.Embedding) == 0 {
			return nil, fmt.Errorf("no embedding in response for text %d", i)
		}
		results[i] = embedResp.Embedding
	}
	return results, nil
}

func (b *BedrockEmbedder) embedTitanV2(ctx context.Context, texts []string) ([][]float32, error) {
	dims := b.dims
	if dims == 0 {
		dims = 1024
	}
	results := make([][]float32, len(texts))
	for i, text := range texts {
		req := titanV2EmbedRequest{
			InputText:      text,
			Dimensions:     dims,
			Normalize:      true,
			EmbeddingTypes: []string{"float"},
		}
		reqBody, err := json.Marshal(req)
		if err != nil {
			return nil, fmt.Errorf("marshal embed request: %w", err)
		}
		body, err := b.invokeModel(ctx, reqBody)
		if err != nil {
			return nil, err
		}
		var embedResp titanV2EmbedResponse
		if err := json.Unmarshal(body, &embedResp); err != nil {
			return nil, fmt.Errorf("unmarshal embed response: %w", err)
		}
		if len(embedResp.EmbeddingsByType.Float) == 0 {
			return nil, fmt.Errorf("no embedding in response for text %d", i)
		}
		results[i] = embedResp.EmbeddingsByType.Float
	}
	return results, nil
}

const cohereBatchSize = 96

func (b *BedrockEmbedder) embedCohere(ctx context.Context, texts []string) ([][]float32, error) {
	results := make([][]float32, len(texts))
	for start := 0; start < len(texts); start += cohereBatchSize {
		end := start + cohereBatchSize
		if end > len(texts) {
			end = len(texts)
		}
		batch := texts[start:end]
		req := cohereEmbedRequest{
			Texts:     batch,
			InputType: "search_document",
			Truncate:  "END",
		}
		reqBody, err := json.Marshal(req)
		if err != nil {
			return nil, fmt.Errorf("marshal embed request: %w", err)
		}
		body, err := b.invokeModel(ctx, reqBody)
		if err != nil {
			return nil, err
		}
		embs, err := parseCohereEmbeddings(body)
		if err != nil {
			return nil, fmt.Errorf("unmarshal embed response: %w", err)
		}
		if len(embs) != len(batch) {
			return nil, fmt.Errorf("expected %d embeddings, got %d", len(batch), len(embs))
		}
		for j, emb := range embs {
			results[start+j] = emb
		}
	}
	return results, nil
}

func (b *BedrockEmbedder) embedTwelveLabsEach(ctx context.Context, texts []string, buildReq func(string) any) ([][]float32, error) {
	results := make([][]float32, len(texts))
	for i, text := range texts {
		reqBody, err := json.Marshal(buildReq(text))
		if err != nil {
			return nil, fmt.Errorf("marshal embed request: %w", err)
		}
		body, err := b.invokeModel(ctx, reqBody)
		if err != nil {
			return nil, err
		}
		embedding, err := parseTwelveLabsEmbedding(body)
		if err != nil {
			return nil, fmt.Errorf("parse embed response for text %d: %w", i, err)
		}
		results[i] = embedding
	}
	return results, nil
}

func (b *BedrockEmbedder) embedTwelveLabs27(ctx context.Context, texts []string) ([][]float32, error) {
	return b.embedTwelveLabsEach(ctx, texts, func(text string) any {
		return twelvelabs27EmbedRequest{InputType: "text", InputText: text}
	})
}

func (b *BedrockEmbedder) embedTwelveLabs30(ctx context.Context, texts []string) ([][]float32, error) {
	return b.embedTwelveLabsEach(ctx, texts, func(text string) any {
		req := twelvelabs30EmbedRequest{InputType: "text"}
		req.Text.InputText = text
		return req
	})
}

func parseTwelveLabsEmbedding(body []byte) ([]float32, error) {
	var resp twelvelabsEmbedEnvelope
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("unmarshal embed response: %w", err)
	}
	if len(resp.Embedding) > 0 {
		return resp.Embedding, nil
	}
	if len(resp.Data) == 0 {
		return nil, fmt.Errorf("no embedding in response")
	}

	var obj twelvelabsEmbedItem
	if err := json.Unmarshal(resp.Data, &obj); err == nil && len(obj.Embedding) > 0 {
		return obj.Embedding, nil
	}

	var arr []twelvelabsEmbedItem
	if err := json.Unmarshal(resp.Data, &arr); err == nil {
		for _, item := range arr {
			if len(item.Embedding) > 0 {
				return item.Embedding, nil
			}
		}
	}

	return nil, fmt.Errorf("no embedding in response")
}

func (b *BedrockEmbedder) ListModels(_ context.Context) ([]ModelInfo, error) {
	return []ModelInfo{{
		ID:          b.model,
		Name:        "Amazon Nova Embeddings v2",
		Provider:    "bedrock",
		Type:        "embedding",
		Dimensions:  b.dims,
		ContextLen:  8192,
		PriceIn:     0.135,
		PriceOut:    0,
		PriceSource: "builtin",
		Local:       false,
	}}, nil
}

// BedrockGenerator implements GenerationProvider using Amazon Bedrock.
type BedrockGenerator struct {
	client         *bedrockruntime.Client
	ctrl           *bedrock.Client
	model          string
	region         string
	avail          availableCache
	noTemperature  atomic.Bool // cached: model rejects temperature in InferenceConfiguration
	noSystemPrompt atomic.Bool // cached: model rejects system prompts
}

// BedrockGenerator reports real token usage (Converse returns it), so ask records
// actual input/output tokens rather than a chars/4 estimate.
var _ UsageGenerator = (*BedrockGenerator)(nil)

// NewBedrockGenerator creates a Bedrock generation provider.
func NewBedrockGenerator(ctx context.Context, cfg BedrockConfig, model string) (*BedrockGenerator, error) {
	awsCfg, err := loadBedrockAWSConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("load AWS config: %w", err)
	}

	return &BedrockGenerator{
		client: bedrockruntime.NewFromConfig(awsCfg),
		ctrl:   bedrock.NewFromConfig(awsCfg),
		model:  model,
		region: cfg.Region,
	}, nil
}

func (b *BedrockGenerator) Name() string { return "bedrock" }

func (b *BedrockGenerator) Available(ctx context.Context) bool {
	if v, hit := b.avail.get(); hit {
		return v
	}
	ok := bedrockAvailableProbe(ctx, b.ctrl)
	b.avail.set(ok)
	return ok
}

// Generate satisfies GenerationProvider; it delegates to GenerateWithUsage and
// drops the token usage.
func (b *BedrockGenerator) Generate(ctx context.Context, prompt string, opts GenOpts) (string, error) {
	text, _, err := b.GenerateWithUsage(ctx, prompt, opts)
	return text, err
}

// GenerateWithUsage runs the Converse call and returns the answer plus the
// Bedrock-reported input/output token usage, which `ask` records (the usage was
// previously parsed-and-discarded). Implements ai.UsageGenerator.
func (b *BedrockGenerator) GenerateWithUsage(ctx context.Context, prompt string, opts GenOpts) (string, GenUsage, error) {
	maxTokens := int32(opts.MaxTokens)
	if maxTokens == 0 {
		maxTokens = 512
	}

	inferCfg := &types.InferenceConfiguration{MaxTokens: aws.Int32(maxTokens)}
	if opts.Temperature != nil && !b.noTemperature.Load() {
		inferCfg.Temperature = aws.Float32(float32(*opts.Temperature))
	}

	input := &bedrockruntime.ConverseInput{
		ModelId: aws.String(b.model),
		Messages: []types.Message{{
			Role: types.ConversationRoleUser,
			Content: []types.ContentBlock{
				&types.ContentBlockMemberText{Value: prompt},
			},
		}},
		InferenceConfig: inferCfg,
	}

	if opts.SystemPrompt != "" && !b.noSystemPrompt.Load() {
		input.System = []types.SystemContentBlock{
			&types.SystemContentBlockMemberText{Value: opts.SystemPrompt},
		}
	}

	resp, err := b.converseWithRetry(ctx, input)
	if err != nil {
		// Some models (e.g. Claude Opus 4.7) reject temperature entirely.
		// Retry once without it and cache the result for this process.
		if opts.Temperature != nil && !b.noTemperature.Load() && isTemperatureRejected(err) {
			b.noTemperature.Store(true)
			inferCfg.Temperature = nil
			resp, err = b.converseWithRetry(ctx, input)
		}
		// Some models reject system prompts. Retry without one and cache for this process.
		if err != nil && opts.SystemPrompt != "" && !b.noSystemPrompt.Load() && isSystemPromptRejected(err) {
			b.noSystemPrompt.Store(true)
			input.System = nil
			resp, err = b.converseWithRetry(ctx, input)
		}
		if err != nil {
			return "", GenUsage{}, fmt.Errorf("converse %s: %w", b.model, err)
		}
	}

	usage := GenUsage{}
	if resp.Usage != nil {
		usage.InputTokens = int(aws.ToInt32(resp.Usage.InputTokens))
		usage.OutputTokens = int(aws.ToInt32(resp.Usage.OutputTokens))
	}

	msg, ok := resp.Output.(*types.ConverseOutputMemberMessage)
	if !ok || len(msg.Value.Content) == 0 {
		return "", usage, fmt.Errorf("empty response from %s", b.model)
	}

	// Iterate content blocks — reasoning models emit non-text blocks first.
	for _, block := range msg.Value.Content {
		if t, ok := block.(*types.ContentBlockMemberText); ok {
			return t.Value, usage, nil
		}
	}
	// Fallback: some models (e.g. DeepSeek R1) embed their answer inside a
	// reasoning content block rather than emitting a separate text block.
	for _, block := range msg.Value.Content {
		if r, ok := block.(*types.ContentBlockMemberReasoningContent); ok {
			if t, ok := r.Value.(*types.ReasoningContentBlockMemberReasoningText); ok && aws.ToString(t.Value.Text) != "" {
				return aws.ToString(t.Value.Text), usage, nil
			}
		}
	}
	return "", usage, fmt.Errorf("no text content in response from %s", b.model)
}

func (b *BedrockGenerator) converseWithRetry(ctx context.Context, input *bedrockruntime.ConverseInput) (*bedrockruntime.ConverseOutput, error) {
	var resp *bedrockruntime.ConverseOutput
	var err error
	for attempt := 1; attempt <= maxBedrockAttempts; attempt++ {
		resp, err = b.client.Converse(ctx, input)
		if err == nil || !isBedrockRetryable(err) || attempt == maxBedrockAttempts {
			break
		}
		// Honor cancellation during backoff (see invokeModel).
		select {
		case <-ctx.Done():
			return resp, ctx.Err()
		case <-time.After(bedrockRetryDelay(attempt)):
		}
	}
	return resp, err
}

// isTemperatureRejected reports whether err is a Bedrock ValidationException
// indicating the model does not accept the temperature inference parameter.
func isTemperatureRejected(err error) bool {
	var ve *types.ValidationException
	if !errors.As(err, &ve) {
		return false
	}
	msg := strings.ToLower(ve.ErrorMessage())
	return strings.Contains(msg, "temperature") ||
		strings.Contains(msg, "inferenceconfig") ||
		strings.Contains(msg, "inference_config")
}

// isSystemPromptRejected reports whether err is a Bedrock ValidationException
// indicating the model does not accept system prompts.
func isSystemPromptRejected(err error) bool {
	var ve *types.ValidationException
	if !errors.As(err, &ve) {
		return false
	}
	msg := strings.ToLower(ve.ErrorMessage())
	return strings.Contains(msg, "system message") || strings.Contains(msg, "doesn't support system")
}

func (b *BedrockGenerator) ListModels(_ context.Context) ([]ModelInfo, error) {
	return []ModelInfo{{
		ID:          b.model,
		Name:        b.model,
		Provider:    "bedrock",
		Type:        "generation",
		PriceSource: "builtin",
		Local:       false,
	}}, nil
}

// ListBedrockVendorModels discovers models available in the configured Bedrock
// region. It merges two sources:
//
//  1. System-defined inference profiles (ListInferenceProfiles) — these are the
//     correct invokable IDs for newer models (e.g. us.anthropic.claude-…). Only
//     profiles matching the user's geographic prefix (us./eu./ap.) or global.*
//     are included.
//
//  2. Foundation models (ListFoundationModels) — base model IDs. Models whose
//     base ID is already covered by an included inference profile are skipped,
//     since the base ID cannot be invoked directly for those models.
//
// Results are returned with Tier=TierUnverified.
func ListBedrockVendorModels(ctx context.Context, cfg BedrockConfig) ([]ModelInfo, error) {
	awsCfg, err := loadBedrockAWSConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("load AWS config: %w", err)
	}

	client := bedrock.NewFromConfig(awsCfg)

	resp, err := client.ListFoundationModels(ctx, &bedrock.ListFoundationModelsInput{})
	if err != nil {
		return nil, fmt.Errorf("list foundation models: %w", err)
	}

	summaries := make(map[string]bedrocktypes.FoundationModelSummary, len(resp.ModelSummaries))
	for _, fm := range resp.ModelSummaries {
		summaries[aws.ToString(fm.ModelId)] = fm
	}

	// ── Step 1: system-defined inference profiles ──────────────────────────
	geo := geoPrefix(cfg.Region)
	var inferenceProfiles []ModelInfo
	coveredBaseIDs := make(map[string]bool)

	paginator := bedrock.NewListInferenceProfilesPaginator(client, &bedrock.ListInferenceProfilesInput{
		TypeEquals: bedrocktypes.InferenceProfileTypeSystemDefined,
	})
	for paginator.HasMorePages() {
		page, pageErr := paginator.NextPage(ctx)
		if pageErr != nil {
			// Non-fatal: credentials may not have permission; fall through to
			// foundation models only.
			slog.Debug("list inference profiles failed", "err", pageErr)
			break
		}
		for _, p := range page.InferenceProfileSummaries {
			if p.Status != bedrocktypes.InferenceProfileStatusActive {
				continue
			}
			id := aws.ToString(p.InferenceProfileId)
			if !(strings.HasPrefix(id, "global.") || (geo != "" && strings.HasPrefix(id, geo))) {
				continue
			}
			baseID := inferenceProfileBaseID(id)
			modelType := InferModelType(baseID)
			if summary, ok := summaries[baseID]; ok {
				if summary.ModelLifecycle != nil && summary.ModelLifecycle.Status == bedrocktypes.FoundationModelLifecycleStatusLegacy {
					continue
				}
				if !bedrockSummaryHasTextInput(summary) {
					continue
				}
				modelType = bedrockModelTypeFromSummary(summary)
			} else {
				details, detailErr := bedrockFoundationModelDetails(ctx, client, baseID)
				if detailErr != nil {
					if isBedrockModelLifecycleBlocked(detailErr) {
						continue
					}
					slog.Debug("get foundation model failed", "model", baseID, "err", detailErr)
					continue
				} else {
					if details.ModelLifecycle != nil && details.ModelLifecycle.Status == bedrocktypes.FoundationModelLifecycleStatusLegacy {
						continue
					}
					if !bedrockDetailsHasTextInput(details) {
						continue
					}
					modelType = bedrockModelTypeFromDetails(details)
				}
			}
			if ok, _ := bedrockModelSupported(baseID, modelType); !ok {
				continue
			}
			inferenceProfiles = append(inferenceProfiles, ModelInfo{
				ID:         id,
				Name:       aws.ToString(p.InferenceProfileName),
				Provider:   "bedrock",
				Type:       modelType,
				ContextLen: bedrockContextLenHint(id),
				Local:      false,
				Tier:       TierUnverified,
				Notes:      "use 2nb models test to verify",
			})
			coveredBaseIDs[baseID] = true
		}
	}

	var models []ModelInfo
	for _, fm := range resp.ModelSummaries {
		if fm.ModelLifecycle != nil && fm.ModelLifecycle.Status == bedrocktypes.FoundationModelLifecycleStatusLegacy {
			continue
		}
		if !bedrockSummaryHasTextInput(fm) {
			continue
		}

		id := aws.ToString(fm.ModelId)
		// Skip context-window variant IDs (e.g., model:0:24k, model:1:8k, model:0:512).
		// ListFoundationModels returns these as metadata entries but they are not invokable.
		if isContextWindowVariantID(id) {
			continue
		}
		// Skip if an inference profile covers this base model — the base ID
		// cannot be invoked directly for those models.
		if coveredBaseIDs[id] {
			continue
		}

		modelType := bedrockModelTypeFromSummary(fm)
		if ok, _ := bedrockModelSupported(id, modelType); !ok {
			continue
		}

		models = append(models, ModelInfo{
			ID:         id,
			Name:       aws.ToString(fm.ModelName),
			Provider:   "bedrock",
			Type:       modelType,
			ContextLen: bedrockContextLenHint(id),
			Local:      false,
			Tier:       TierUnverified,
			Notes:      "use 2nb models test to verify",
		})
	}

	return append(models, inferenceProfiles...), nil
}

// CheckBedrockCredentials resolves AWS credentials from the SDK credential chain.
// This may make network calls (STS, IMDS) if env vars and config files are absent.
func CheckBedrockCredentials(ctx context.Context, cfg BedrockConfig) bool {
	awsCfg, err := loadBedrockAWSConfig(ctx, cfg)
	if err != nil {
		return false
	}
	creds, err := awsCfg.Credentials.Retrieve(ctx)
	return err == nil && creds.HasKeys()
}

// InitBedrock creates and registers Bedrock providers with the given registry.
func InitBedrock(ctx context.Context, reg *Registry, cfg BedrockConfig, aiCfg AIConfig) error {
	embedder, err := NewBedrockEmbedder(ctx, cfg, aiCfg.EmbeddingModel, aiCfg.Dimensions)
	if err != nil {
		return fmt.Errorf("init bedrock embedder: %w", err)
	}
	reg.RegisterEmbedder("bedrock", embedder)

	generator, err := NewBedrockGenerator(ctx, cfg, aiCfg.GenerationModel)
	if err != nil {
		return fmt.Errorf("init bedrock generator: %w", err)
	}
	reg.RegisterGenerator("bedrock", generator)

	// Register the reranker only when enabled: it's an optional stage on a
	// distinct API (bedrockagentruntime.Rerank), so a disabled rerank config
	// leaves the slot empty and retrieve keeps the RRF order.
	if aiCfg.RerankEnabled() {
		reranker, err := NewBedrockReranker(ctx, cfg, aiCfg.ResolveRerankModel())
		if err != nil {
			return fmt.Errorf("init bedrock reranker: %w", err)
		}
		reg.RegisterReranker("bedrock", reranker)
	}

	return nil
}

// ── Inference profile helpers ──────────────────────────────────────────────

// geoPrefix returns the geographic inference profile prefix for a Bedrock region.
// Returns "" for regions with no standard prefix (e.g. me-south-1, sa-east-1).
func geoPrefix(region string) string {
	switch {
	case strings.HasPrefix(region, "us-"):
		return "us."
	case strings.HasPrefix(region, "eu-"):
		return "eu."
	case strings.HasPrefix(region, "ap-"):
		return "ap."
	default:
		return ""
	}
}

// inferenceProfileBaseID strips the region prefix from an inference profile ID,
// returning the underlying foundation model ID used for deduplication.
// e.g. "us.anthropic.claude-3-5-haiku-20241022-v1:0" → "anthropic.claude-3-5-haiku-20241022-v1:0"
func inferenceProfileBaseID(id string) string {
	for _, pfx := range []string{"us.", "eu.", "ap.", "global."} {
		if strings.HasPrefix(id, pfx) {
			return id[len(pfx):]
		}
	}
	return id
}

// isContextWindowVariantID reports whether id is a non-invokable context-window
// variant returned by ListFoundationModels (e.g. "amazon.nova-lite-v1:0:24k",
// "cohere.embed-english-v3:0:512"). These have the form base:version:size and
// return 404 when invoked directly.
func isContextWindowVariantID(id string) bool {
	i := strings.LastIndex(id, ":")
	if i < 0 || !strings.Contains(id[:i], ":") {
		return false // must have at least two colons
	}
	suffix := id[i+1:]
	if suffix == "mm" {
		return true
	}
	if strings.HasSuffix(suffix, "k") {
		_, err := strconv.Atoi(suffix[:len(suffix)-1])
		return err == nil
	}
	n, err := strconv.Atoi(suffix)
	return err == nil && n > 10 // >10 avoids catching :0 :1 :2 version numbers
}
