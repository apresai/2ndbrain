package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/bedrock"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
)

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
	client    *bedrockruntime.Client
	model     string
	dims      int
	region    string
	available *bool // cached availability (H1 fix)
}

// NewBedrockEmbedder creates a Bedrock embedding provider.
func NewBedrockEmbedder(ctx context.Context, cfg BedrockConfig, model string, dims int) (*BedrockEmbedder, error) {
	awsCfg, err := loadBedrockAWSConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("load AWS config: %w", err)
	}

	return &BedrockEmbedder{
		client: bedrockruntime.NewFromConfig(awsCfg),
		model:  model,
		dims:   dims,
		region: cfg.Region,
	}, nil
}

func (b *BedrockEmbedder) Name() string      { return "bedrock" }
func (b *BedrockEmbedder) Dimensions() int    { return b.dims }

func (b *BedrockEmbedder) Available(ctx context.Context) bool {
	if b.available != nil {
		return *b.available
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	_, err := b.Embed(ctx, []string{"test"})
	result := err == nil
	b.available = &result
	return result
}

// novaEmbedRequest is the request body for Nova Embeddings v2.
type novaEmbedRequest struct {
	TaskType             string                `json:"taskType"`
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

// novaEmbedResponse is the response from Nova Embeddings v2.
type novaEmbedResponse struct {
	Embeddings []struct {
		EmbeddingType string    `json:"embeddingType"`
		Embedding     []float32 `json:"embedding"`
	} `json:"embeddings"`
}

func (b *BedrockEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
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

		resp, err := b.client.InvokeModel(ctx, &bedrockruntime.InvokeModelInput{
			ModelId:     aws.String(b.model),
			ContentType: aws.String("application/json"),
			Accept:      aws.String("application/json"),
			Body:        reqBody,
		})
		if err != nil {
			return nil, fmt.Errorf("invoke %s: %w", b.model, err)
		}

		var embedResp novaEmbedResponse
		if err := json.Unmarshal(resp.Body, &embedResp); err != nil {
			return nil, fmt.Errorf("unmarshal embed response: %w", err)
		}
		if len(embedResp.Embeddings) == 0 {
			return nil, fmt.Errorf("no embeddings in response for text %d", i)
		}
		results[i] = embedResp.Embeddings[0].Embedding
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
	client    *bedrockruntime.Client
	model     string
	region    string
	available *bool // cached availability (H1 fix)
}

// NewBedrockGenerator creates a Bedrock generation provider.
func NewBedrockGenerator(ctx context.Context, cfg BedrockConfig, model string) (*BedrockGenerator, error) {
	awsCfg, err := loadBedrockAWSConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("load AWS config: %w", err)
	}

	return &BedrockGenerator{
		client: bedrockruntime.NewFromConfig(awsCfg),
		model:  model,
		region: cfg.Region,
	}, nil
}

func (b *BedrockGenerator) Name() string { return "bedrock" }

func (b *BedrockGenerator) Available(ctx context.Context) bool {
	if b.available != nil {
		return *b.available
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	_, err := b.Generate(ctx, "hi", GenOpts{MaxTokens: 1, Temperature: 0})
	result := err == nil
	b.available = &result
	return result
}

func (b *BedrockGenerator) Generate(ctx context.Context, prompt string, opts GenOpts) (string, error) {
	maxTokens := int32(opts.MaxTokens)
	if maxTokens == 0 {
		maxTokens = 512
	}
	temp := float32(opts.Temperature)

	input := &bedrockruntime.ConverseInput{
		ModelId: aws.String(b.model),
		Messages: []types.Message{{
			Role: types.ConversationRoleUser,
			Content: []types.ContentBlock{
				&types.ContentBlockMemberText{Value: prompt},
			},
		}},
		InferenceConfig: &types.InferenceConfiguration{
			MaxTokens:   aws.Int32(maxTokens),
			Temperature: aws.Float32(temp),
		},
	}

	if opts.SystemPrompt != "" {
		input.System = []types.SystemContentBlock{
			&types.SystemContentBlockMemberText{Value: opts.SystemPrompt},
		}
	}

	resp, err := b.client.Converse(ctx, input)
	if err != nil {
		return "", fmt.Errorf("converse %s: %w", b.model, err)
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

func (b *BedrockGenerator) ListModels(_ context.Context) ([]ModelInfo, error) {
	return []ModelInfo{{
		ID:       b.model,
		Name:     b.model,
		Provider: "bedrock",
		Type:     "generation",
		Local:    false,
	}}, nil
}

// ListBedrockVendorModels calls the Bedrock ListFoundationModels API to discover
// all models available in the configured region. Results are returned with
// Tier=TierUnverified since 2nb may not have a harness for their invoke format.
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

		modelType := "generation"
		for _, m := range fm.OutputModalities {
			if strings.EqualFold(string(m), "EMBEDDING") {
				modelType = "embedding"
				break
			}
		}

		id := aws.ToString(fm.ModelId)
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
	return models, nil
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
