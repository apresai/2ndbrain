package ai

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
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
	case "rerank":
		switch {
		case strings.HasPrefix(lower, "cohere.rerank"):
			return true, "" // Cohere Rerank via the Bedrock Rerank API (bedrockagentruntime.Rerank)
		default:
			return false, "2nb supports only Cohere Rerank on Bedrock"
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
			return false, "cohere.rerank is a reranker (set it via ai.rerank.model), not a generation model"
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
// a lifecycle lookup before a probe invokes the model. vaultRoot scopes the
// user-catalog strategy lookup so a VAULT-scoped mantle entry is bypassed
// too; pass "" when no vault is open (builtin + global entries still resolve).
func BedrockPreflightModel(ctx context.Context, cfg BedrockConfig, modelID, modelType, vaultRoot string) error {
	// Mantle-plane models are invisible to the classic control plane: the
	// static allowlist doesn't know them and GetFoundationModel would 404,
	// so both checks are skipped. The real invoke probe is the only check
	// that means anything for these models.
	if ResolveInvokeStrategy("bedrock", modelID, vaultRoot) == StrategyBedrockMantleResponses {
		slog.Debug("bedrock preflight: mantle strategy, skipping control-plane checks", "model", modelID)
		return nil
	}
	if ok, reason := bedrockModelSupported(modelID, modelType); !ok {
		slog.Debug("bedrock preflight: static blocklist", "model", modelID, "type", modelType, "reason", reason)
		return &IncompatibleModelError{Reason: reason}
	}
	legacy, err := bedrockModelIsLegacy(ctx, cfg, modelID)
	if err == nil && legacy {
		slog.Debug("bedrock preflight: lifecycle blocked", "model", modelID)
		return &IncompatibleModelError{Reason: fmt.Sprintf("model %s is legacy or end-of-life and excluded from 2nb discovery", modelID)}
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

// maxBedrockAttempts bounds the per-call retry loop (invokeModel,
// converseWithRetry). Raised from 3 to 5 because the concurrent embed path makes
// ThrottlingException likely, and a throttled worker should ride out a couple of
// backoff rounds rather than fail the whole re-embed.
const maxBedrockAttempts = 5

// isBedrockRetryable reports whether a Bedrock error is worth retrying with
// backoff. It covers server faults (InternalServerException,
// ServiceUnavailableException) AND ThrottlingException / ModelTimeoutException —
// the latter is a CLIENT fault (HTTP 429), so the FaultServer check alone misses
// it. Retrying throttling with backoff is what makes the concurrent embed pool
// self-correcting when an account's RPM quota is lower than the chosen
// concurrency.
func isBedrockRetryable(err error) bool {
	var internal *runtimetypes.InternalServerException
	if errors.As(err, &internal) {
		return true
	}
	var throttle *runtimetypes.ThrottlingException
	if errors.As(err, &throttle) {
		return true
	}
	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	if apiErr.ErrorFault() == smithy.FaultServer {
		return true
	}
	// Match by code too, so a throttle/timeout surfaced only as a generic
	// smithy.APIError (not the typed struct) is still retried.
	switch {
	case strings.EqualFold(apiErr.ErrorCode(), "InternalServerException"),
		strings.EqualFold(apiErr.ErrorCode(), "ThrottlingException"),
		strings.EqualFold(apiErr.ErrorCode(), "TooManyRequestsException"),
		strings.EqualFold(apiErr.ErrorCode(), "ServiceUnavailableException"),
		strings.EqualFold(apiErr.ErrorCode(), "ModelTimeoutException"):
		return true
	}
	return false
}

// bedrockRetryDelay returns the backoff before retry `attempt`, with equal
// jitter: at least half the exponential base plus a random portion of the other
// half. The jitter de-syncs concurrent workers that all throttle at the same
// instant, avoiding a thundering-herd retry. Base is 200ms * 2^(attempt-1),
// capped at 10s.
func bedrockRetryDelay(attempt int) time.Duration {
	if attempt < 1 {
		return 0
	}
	base := 200 * time.Millisecond * time.Duration(1<<(attempt-1))
	if base > 10*time.Second {
		base = 10 * time.Second
	}
	half := base / 2
	return half + time.Duration(rand.Int63n(int64(half)+1))
}

// bedrockContextLenHint returns a static context-window hint (in tokens) for
// a discovered Bedrock model, keyed by model family. Bedrock's control-plane
// APIs (ListFoundationModels / GetFoundationModel) do not expose the context
// window, so this map is the only offline source for discovered entries.
// Conservative by design: only families with well-known values are listed,
// and 0 ("unknown", rendered as "-") is the honest default for the rest.
func bedrockContextLenHint(modelID string) int {
	lower := strings.ToLower(inferenceProfileBaseID(modelID))
	switch {
	// Anthropic. 200K: Haiku 4.5, the Claude 3.x line, and the pre-4.6
	// versions (Sonnet 4/4.5 via their date-suffixed IDs, e.g.
	// claude-sonnet-4-20250514 matches the "sonnet-4-2" prefix; Opus
	// 4.1/4.5). 1M: Sonnet 4.6+, Sonnet 5, Opus 4.6+, Fable 5 — matched by
	// the broader prefixes below AFTER the specific pre-4.6 cases.
	case strings.HasPrefix(lower, "anthropic.claude-haiku-4-5"),
		strings.HasPrefix(lower, "anthropic.claude-3"):
		return 200_000
	case strings.HasPrefix(lower, "anthropic.claude-sonnet-4-5"),
		strings.HasPrefix(lower, "anthropic.claude-sonnet-4-2"),
		strings.HasPrefix(lower, "anthropic.claude-opus-4-1"),
		strings.HasPrefix(lower, "anthropic.claude-opus-4-5"),
		strings.HasPrefix(lower, "anthropic.claude-opus-4-2"):
		return 200_000
	case strings.HasPrefix(lower, "anthropic.claude-sonnet"),
		strings.HasPrefix(lower, "anthropic.claude-opus"),
		strings.HasPrefix(lower, "anthropic.claude-fable"):
		return 1_000_000
	case strings.HasPrefix(lower, "amazon.nova-micro"):
		return 128_000
	case strings.HasPrefix(lower, "amazon.nova-lite"),
		strings.HasPrefix(lower, "amazon.nova-pro"):
		return 300_000
	case strings.HasPrefix(lower, "amazon.nova-premier"):
		return 1_000_000
	case strings.HasPrefix(lower, "amazon.titan-embed"):
		return 8192
	case strings.HasPrefix(lower, "amazon.nova-2-multimodal-embeddings"):
		return 8192
	case strings.HasPrefix(lower, "cohere.embed"):
		return 512
	case strings.HasPrefix(lower, "meta.llama4"),
		strings.HasPrefix(lower, "meta.llama3-3"),
		strings.HasPrefix(lower, "meta.llama3-2"),
		strings.HasPrefix(lower, "meta.llama3-1"):
		return 128_000
	case strings.HasPrefix(lower, "mistral.mistral-large"),
		strings.HasPrefix(lower, "mistral.pixtral"):
		return 128_000
	default:
		return 0
	}
}
