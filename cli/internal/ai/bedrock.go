package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
)

// BedrockEmbedder implements EmbeddingProvider using Amazon Bedrock.
type BedrockEmbedder struct {
	client *bedrockruntime.Client
	model  string
	dims   int
	region string
}

// NewBedrockEmbedder creates a Bedrock embedding provider.
func NewBedrockEmbedder(ctx context.Context, cfg BedrockConfig, model string, dims int) (*BedrockEmbedder, error) {
	opts := []func(*awsconfig.LoadOptions) error{
		awsconfig.WithRegion(cfg.Region),
	}
	if cfg.Profile != "" && cfg.Profile != "default" {
		opts = append(opts, awsconfig.WithSharedConfigProfile(cfg.Profile))
	}

	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, opts...)
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
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	// Try a minimal embed to check connectivity
	_, err := b.Embed(ctx, []string{"test"})
	return err == nil
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
	client *bedrockruntime.Client
	model  string
	region string
}

// NewBedrockGenerator creates a Bedrock generation provider.
func NewBedrockGenerator(ctx context.Context, cfg BedrockConfig, model string) (*BedrockGenerator, error) {
	opts := []func(*awsconfig.LoadOptions) error{
		awsconfig.WithRegion(cfg.Region),
	}
	if cfg.Profile != "" && cfg.Profile != "default" {
		opts = append(opts, awsconfig.WithSharedConfigProfile(cfg.Profile))
	}

	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, opts...)
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
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, err := b.Generate(ctx, "hi", GenOpts{MaxTokens: 1, Temperature: 0})
	return err == nil
}

// claudeRequest is the Anthropic Messages API format for Bedrock.
type claudeRequest struct {
	AnthropicVersion string          `json:"anthropic_version"`
	MaxTokens        int             `json:"max_tokens"`
	Temperature      float64         `json:"temperature,omitempty"`
	System           string          `json:"system,omitempty"`
	Messages         []claudeMessage `json:"messages"`
}

type claudeMessage struct {
	Role    string         `json:"role"`
	Content []claudeBlock  `json:"content"`
}

type claudeBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type claudeResponse struct {
	Content []struct {
		Text string `json:"text"`
	} `json:"content"`
}

func (b *BedrockGenerator) Generate(ctx context.Context, prompt string, opts GenOpts) (string, error) {
	maxTokens := opts.MaxTokens
	if maxTokens == 0 {
		maxTokens = 512
	}

	req := claudeRequest{
		AnthropicVersion: "bedrock-2023-05-31",
		MaxTokens:        maxTokens,
		Temperature:      opts.Temperature,
		System:           opts.SystemPrompt,
		Messages: []claudeMessage{{
			Role: "user",
			Content: []claudeBlock{{
				Type: "text",
				Text: prompt,
			}},
		}},
	}

	reqBody, err := json.Marshal(req)
	if err != nil {
		return "", fmt.Errorf("marshal generate request: %w", err)
	}

	resp, err := b.client.InvokeModel(ctx, &bedrockruntime.InvokeModelInput{
		ModelId:     aws.String(b.model),
		ContentType: aws.String("application/json"),
		Accept:      aws.String("application/json"),
		Body:        reqBody,
	})
	if err != nil {
		return "", fmt.Errorf("invoke %s: %w", b.model, err)
	}

	var claudeResp claudeResponse
	if err := json.Unmarshal(resp.Body, &claudeResp); err != nil {
		return "", fmt.Errorf("unmarshal generate response: %w", err)
	}

	if len(claudeResp.Content) == 0 {
		return "", fmt.Errorf("empty response from %s", b.model)
	}
	return claudeResp.Content[0].Text, nil
}

func (b *BedrockGenerator) ListModels(_ context.Context) ([]ModelInfo, error) {
	return []ModelInfo{{
		ID:         b.model,
		Name:       "Claude Haiku 4.5",
		Provider:   "bedrock",
		Type:       "generation",
		ContextLen: 200000,
		PriceIn:    0.80,
		PriceOut:   4.00,
		Local:      false,
	}}, nil
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
