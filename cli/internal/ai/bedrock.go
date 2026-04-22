package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"
	"time"

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

// loadBedrockAWSConfig builds an AWS config from BedrockConfig settings.
func loadBedrockAWSConfig(ctx context.Context, cfg BedrockConfig) (aws.Config, error) {
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
	avail  availableCache
}

// NewBedrockEmbedder creates a Bedrock embedding provider.
func NewBedrockEmbedder(ctx context.Context, cfg BedrockConfig, model string, dims int) (*BedrockEmbedder, error) {
	awsCfg, err := loadBedrockAWSConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("load AWS config: %w", err)
	}

	return &BedrockEmbedder{
		client: bedrockruntime.NewFromConfig(awsCfg),
		ctrl:   bedrock.NewFromConfig(awsCfg),
		model:  model,
		dims:   dims,
		region: cfg.Region,
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

// Cohere Embed v3 — batched (≤96 texts per call), fixed 1024 dims
type cohereEmbedRequest struct {
	Texts     []string `json:"texts"`
	InputType string   `json:"input_type"`
	Truncate  string   `json:"truncate,omitempty"`
}

type cohereEmbedResponse struct {
	Embeddings [][]float32 `json:"embeddings"`
}

// ── Embedding format detection ─────────────────────────────────────────────

type bedrockEmbedFmt int

const (
	fmtNova    bedrockEmbedFmt = iota
	fmtTitanV1
	fmtTitanV2
	fmtCohere
)

func detectEmbedFormat(modelID string) bedrockEmbedFmt {
	lower := strings.ToLower(modelID)
	switch {
	case strings.HasPrefix(lower, "cohere.embed-"):
		return fmtCohere
	case lower == "amazon.titan-embed-text-v1":
		return fmtTitanV1
	case strings.HasPrefix(lower, "amazon.titan-embed-"):
		return fmtTitanV2
	default:
		return fmtNova
	}
}

// ── Embed dispatch ─────────────────────────────────────────────────────────

func (b *BedrockEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	switch detectEmbedFormat(b.model) {
	case fmtCohere:
		return b.embedCohere(ctx, texts)
	case fmtTitanV1:
		return b.embedTitanV1(ctx, texts)
	case fmtTitanV2:
		return b.embedTitanV2(ctx, texts)
	default:
		return b.embedNova(ctx, texts)
	}
}

func (b *BedrockEmbedder) invokeModel(ctx context.Context, reqBody []byte) ([]byte, error) {
	resp, err := b.client.InvokeModel(ctx, &bedrockruntime.InvokeModelInput{
		ModelId:     aws.String(b.model),
		ContentType: aws.String("application/json"),
		Accept:      aws.String("application/json"),
		Body:        reqBody,
	})
	if err != nil {
		return nil, fmt.Errorf("invoke %s: %w", b.model, err)
	}
	return resp.Body, nil
}

func (b *BedrockEmbedder) embedNova(ctx context.Context, texts []string) ([][]float32, error) {
	results := make([][]float32, len(texts))
	for i, text := range texts {
		req := novaEmbedRequest{
			TaskType: "SINGLE_EMBEDDING",
			SingleEmbeddingParams: &novaEmbeddingParams{
				EmbeddingPurpose:   "GENERIC_INDEX",
				EmbeddingDimension: b.dims,
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
		var embedResp cohereEmbedResponse
		if err := json.Unmarshal(body, &embedResp); err != nil {
			return nil, fmt.Errorf("unmarshal embed response: %w", err)
		}
		if len(embedResp.Embeddings) != len(batch) {
			return nil, fmt.Errorf("expected %d embeddings, got %d", len(batch), len(embedResp.Embeddings))
		}
		for j, emb := range embedResp.Embeddings {
			results[start+j] = emb
		}
	}
	return results, nil
}

func (b *BedrockEmbedder) ListModels(_ context.Context) ([]ModelInfo, error) {
	return []ModelInfo{{
		ID:         b.model,
		Name:       "Amazon Nova Embeddings v2",
		Provider:   "bedrock",
		Type:       "embedding",
		Dimensions: b.dims,
		ContextLen: 2048,
		PriceIn:    0.135,
		PriceOut:   0,
		Local:      false,
	}}, nil
}

// BedrockGenerator implements GenerationProvider using Amazon Bedrock.
type BedrockGenerator struct {
	client        *bedrockruntime.Client
	ctrl          *bedrock.Client
	model         string
	region        string
	avail         availableCache
	noTemperature atomic.Bool // cached: model rejects temperature in InferenceConfiguration
}

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

func (b *BedrockGenerator) Generate(ctx context.Context, prompt string, opts GenOpts) (string, error) {
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

	if opts.SystemPrompt != "" {
		input.System = []types.SystemContentBlock{
			&types.SystemContentBlockMemberText{Value: opts.SystemPrompt},
		}
	}

	resp, err := b.client.Converse(ctx, input)
	if err != nil {
		// Some models (e.g. Claude Opus 4.7) reject temperature entirely.
		// Retry once without it and cache the result for this process.
		if opts.Temperature != nil && !b.noTemperature.Load() && isTemperatureRejected(err) {
			b.noTemperature.Store(true)
			inferCfg.Temperature = nil
			resp, err = b.client.Converse(ctx, input)
		}
		if err != nil {
			return "", fmt.Errorf("converse %s: %w", b.model, err)
		}
	}

	msg, ok := resp.Output.(*types.ConverseOutputMemberMessage)
	if !ok || len(msg.Value.Content) == 0 {
		return "", fmt.Errorf("empty response from %s", b.model)
	}

	text, ok := msg.Value.Content[0].(*types.ContentBlockMemberText)
	if !ok {
		return "", fmt.Errorf("unexpected content type from %s", b.model)
	}

	return text.Value, nil
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

func (b *BedrockGenerator) ListModels(_ context.Context) ([]ModelInfo, error) {
	return []ModelInfo{{
		ID:       b.model,
		Name:     b.model,
		Provider: "bedrock",
		Type:     "generation",
		Local:    false,
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
			// Include only profiles for this region's geography or global.
			if (geo != "" && strings.HasPrefix(id, geo)) || strings.HasPrefix(id, "global.") {
				inferenceProfiles = append(inferenceProfiles, ModelInfo{
					ID:       id,
					Name:     aws.ToString(p.InferenceProfileName),
					Provider: "bedrock",
					// AWS has not shipped embedding inference profiles as of 2025-04.
					// If that changes, OutputModalities on the profile would be needed here.
					Type:  "generation",
					Local:    false,
					Tier:     TierUnverified,
					Notes:    "use 2nb models test to verify",
				})
				coveredBaseIDs[inferenceProfileBaseID(id)] = true
			}
		}
	}

	// ── Step 2: foundation models not covered by an inference profile ──────
	resp, err := client.ListFoundationModels(ctx, &bedrock.ListFoundationModelsInput{})
	if err != nil {
		return nil, fmt.Errorf("list foundation models: %w", err)
	}

	var models []ModelInfo
	for _, fm := range resp.ModelSummaries {
		// Skip models without text input capability.
		hasText := false
		for _, m := range fm.InputModalities {
			if strings.EqualFold(string(m), "TEXT") {
				hasText = true
				break
			}
		}
		if !hasText {
			continue
		}

		id := aws.ToString(fm.ModelId)
		// Skip if an inference profile covers this base model — the base ID
		// cannot be invoked directly for those models.
		if coveredBaseIDs[id] {
			continue
		}

		modelType := "generation"
		for _, m := range fm.OutputModalities {
			if strings.EqualFold(string(m), "EMBEDDING") {
				modelType = "embedding"
				break
			}
		}

		models = append(models, ModelInfo{
			ID:       id,
			Name:     aws.ToString(fm.ModelName),
			Provider: "bedrock",
			Type:     modelType,
			Local:    false,
			Tier:     TierUnverified,
			Notes:    "use 2nb models test to verify",
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
