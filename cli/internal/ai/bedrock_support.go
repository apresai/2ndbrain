package ai

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrock"
	bedrocktypes "github.com/aws/aws-sdk-go-v2/service/bedrock/types"
	runtimetypes "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"github.com/aws/smithy-go"
)

func bedrockSummaryHasTextInput(fm bedrocktypes.FoundationModelSummary) bool {
	for _, m := range fm.InputModalities {
		if strings.EqualFold(string(m), "TEXT") {
			return true
		}
	}
	return false
}

func bedrockDetailsHasTextInput(fm *bedrocktypes.FoundationModelDetails) bool {
	if fm == nil {
		return false
	}
	for _, m := range fm.InputModalities {
		if strings.EqualFold(string(m), "TEXT") {
			return true
		}
	}
	return false
}

func bedrockModelTypeFromSummary(fm bedrocktypes.FoundationModelSummary) string {
	for _, m := range fm.OutputModalities {
		if strings.EqualFold(string(m), "EMBEDDING") {
			return "embedding"
		}
	}
	return "generation"
}

func bedrockModelTypeFromDetails(fm *bedrocktypes.FoundationModelDetails) string {
	if fm == nil {
		return "generation"
	}
	for _, m := range fm.OutputModalities {
		if strings.EqualFold(string(m), "EMBEDDING") {
			return "embedding"
		}
	}
	return "generation"
}

func bedrockModelSupported(modelID, modelType string) (bool, string) {
	lower := strings.ToLower(inferenceProfileBaseID(modelID))
	switch modelType {
	case "embedding":
		switch {
		case strings.Contains(lower, "marengo-embed-"):
			return true, ""
		case strings.HasPrefix(lower, "cohere.embed-"):
			return true, ""
		case strings.HasPrefix(lower, "amazon.titan-embed-image-"):
			return false, "titan image embedding model requires image input — text embedding not supported"
		case lower == "amazon.titan-embed-text-v1":
			return true, ""
		case strings.HasPrefix(lower, "amazon.titan-embed-g1-"):
			return true, ""
		case strings.HasPrefix(lower, "amazon.titan-embed-"):
			return true, ""
		case strings.HasPrefix(lower, "amazon.nova-2-multimodal-embeddings-"):
			return true, ""
		default:
			return false, "2nb doesn't support this Bedrock embedding invoke format yet"
		}
	default:
		switch {
		case strings.HasPrefix(lower, "amazon.nova-canvas"):
			return false, "2nb doesn't support Bedrock image-generation models"
		case strings.HasPrefix(lower, "amazon.nova-reel"):
			return false, "2nb doesn't support Bedrock video-generation models"
		case strings.HasPrefix(lower, "stability.stable-image"):
			return false, "2nb doesn't support Bedrock image-generation models"
		case strings.HasPrefix(lower, "amazon.titan-image-generator"):
			return false, "2nb doesn't support Bedrock image-generation models"
		case strings.HasPrefix(lower, "cohere.rerank"):
			return false, "2nb doesn't support Bedrock reranking models"
		case strings.Contains(lower, ".pegasus-") || strings.HasPrefix(lower, "twelvelabs.pegasus-"):
			return false, "2nb doesn't support Bedrock video-understanding models"
		case strings.HasPrefix(lower, "writer.palmyra-vision-7b"):
			return false, "2nb doesn't support Bedrock Palmyra Vision probe requirements"
		case strings.HasPrefix(lower, "anthropic.claude"):
			return true, ""
		case strings.HasPrefix(lower, "amazon.nova-micro"),
			strings.HasPrefix(lower, "amazon.nova-lite"),
			strings.HasPrefix(lower, "amazon.nova-pro"),
			strings.HasPrefix(lower, "amazon.nova-premier"):
			return true, ""
		case strings.HasPrefix(lower, "amazon.titan-tg1"):
			return true, ""
		case strings.HasPrefix(lower, "ai21.jamba"):
			return true, ""
		case strings.HasPrefix(lower, "cohere.command"):
			return true, ""
		case strings.HasPrefix(lower, "deepseek"):
			return true, ""
		case strings.HasPrefix(lower, "meta.llama"):
			return true, ""
		case strings.HasPrefix(lower, "mistral"):
			return true, ""
		case strings.HasPrefix(lower, "pixtral"):
			return true, ""
		case strings.HasPrefix(lower, "writer.palmyra-x4"),
			strings.HasPrefix(lower, "writer.palmyra-x5"):
			return true, ""
		case strings.HasPrefix(lower, "google.gemma"):
			return true, ""
		case strings.HasPrefix(lower, "openai.gpt-oss"):
			return true, ""
		case strings.HasPrefix(lower, "qwen.qwen"):
			return true, ""
		case strings.HasPrefix(lower, "zai.glm"):
			return true, ""
		case strings.HasPrefix(lower, "moonshot"):
			return true, ""
		case strings.HasPrefix(lower, "minimax"):
			return true, ""
		case strings.HasPrefix(lower, "nvidia.nemotron"):
			return true, ""
		default:
			return false, "2nb doesn't support this Bedrock Converse model path yet"
		}
	}
}

// BedrockPreflightModel performs deterministic local compatibility checks and
// a lifecycle lookup before a probe invokes the model.
func BedrockPreflightModel(ctx context.Context, cfg BedrockConfig, modelID, modelType string) error {
	if ok, reason := bedrockModelSupported(modelID, modelType); !ok {
		slog.Debug("bedrock preflight: static blocklist", "model", modelID, "type", modelType, "reason", reason)
		return errors.New(reason)
	}
	legacy, err := bedrockModelIsLegacy(ctx, cfg, modelID)
	if err == nil && legacy {
		slog.Debug("bedrock preflight: lifecycle blocked", "model", modelID)
		return fmt.Errorf("model %s is legacy or end-of-life and excluded from 2nb discovery", modelID)
	}
	return nil
}

func bedrockModelIsLegacy(ctx context.Context, cfg BedrockConfig, modelID string) (bool, error) {
	awsCfg, err := loadBedrockAWSConfig(ctx, cfg)
	if err != nil {
		return false, err
	}
	client := bedrock.NewFromConfig(awsCfg)
	return bedrockFoundationModelBlocked(ctx, client, modelID)
}

func bedrockFoundationModelDetails(ctx context.Context, client *bedrock.Client, modelID string) (*bedrocktypes.FoundationModelDetails, error) {
	resp, err := client.GetFoundationModel(ctx, &bedrock.GetFoundationModelInput{
		ModelIdentifier: aws.String(inferenceProfileBaseID(modelID)),
	})
	if err != nil {
		return nil, err
	}
	return resp.ModelDetails, nil
}

func bedrockFoundationModelBlocked(ctx context.Context, client *bedrock.Client, modelID string) (bool, error) {
	details, err := bedrockFoundationModelDetails(ctx, client, modelID)
	if err != nil {
		if isBedrockModelLifecycleBlocked(err) {
			return true, nil
		}
		return false, err
	}
	return details != nil &&
		details.ModelLifecycle != nil &&
		details.ModelLifecycle.Status == bedrocktypes.FoundationModelLifecycleStatusLegacy, nil
}

func isBedrockModelLifecycleBlocked(err error) bool {
	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	if !strings.EqualFold(apiErr.ErrorCode(), "ResourceNotFoundException") {
		return false
	}
	msg := strings.ToLower(apiErr.ErrorMessage())
	return strings.Contains(msg, "end of its life") ||
		strings.Contains(msg, "marked as legacy")
}

func isBedrockRetryable(err error) bool {
	var internal *runtimetypes.InternalServerException
	if errors.As(err, &internal) {
		return true
	}
	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	return apiErr.ErrorFault() == smithy.FaultServer || strings.EqualFold(apiErr.ErrorCode(), "InternalServerException")
}

func bedrockRetryDelay(attempt int) time.Duration {
	if attempt < 1 {
		return 0
	}
	return 200 * time.Millisecond * time.Duration(1<<(attempt-1))
}
