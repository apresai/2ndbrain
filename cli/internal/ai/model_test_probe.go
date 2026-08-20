package ai

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// TestProbeResult holds the result of a model test.
type TestProbeResult struct {
	ModelID  string `json:"model_id"`
	Provider string `json:"provider"`
	Type     string `json:"type"` // "embedding" or "generation"
	OK       bool   `json:"ok"`
	Detail   string `json:"detail,omitempty"` // response snippet or error
	Latency  string `json:"latency"`
	// Code classifies a failure into the stable TestErrorCode vocabulary
	// (access_denied, bad_credentials, throttled, ...). Empty on success.
	Code TestErrorCode `json:"code,omitempty"`
	// Remediation is a user-actionable fix hint matching Code. Empty when
	// there is no better advice than Detail.
	Remediation string `json:"remediation,omitempty"`
	// Strategy is the model's resolved InvokeStrategy (e.g.
	// bedrock_mantle_responses). Additive; lets a UI tailor guidance —
	// notably suppress the Bedrock "Model access" console link for mantle
	// models, which that page doesn't govern. Empty when undeclared.
	Strategy string `json:"invoke_strategy,omitempty"`
	// Region is the AWS region this probe actually called (additive; Bedrock
	// only). Bedrock model entitlement is per-region, so a verdict without
	// its region is ambiguous the moment more than one region is in play.
	Region string `json:"region,omitempty"`
}

// TestProbeModel creates a temporary provider for the given model and runs
// a quick smoke test (embed or generate). Returns the result. vaultRoot
// scopes user-catalog invoke-strategy/region lookups (so a vault-scoped
// mantle entry probes over the mantle plane); pass "" when no vault is open.
func TestProbeModel(ctx context.Context, cfg AIConfig, modelID, provider, modelType, vaultRoot string) (*TestProbeResult, error) {
	if provider == "" {
		provider = InferProvider(modelID)
	}
	if provider == "" {
		return nil, fmt.Errorf("cannot infer provider for %q — use --provider", modelID)
	}

	if modelType == "" {
		modelType = InferModelType(modelID)
	}

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	result := &TestProbeResult{
		ModelID:  modelID,
		Provider: provider,
		Type:     modelType,
	}
	if provider == "bedrock" {
		result.Region = EffectiveBedrockRegion(cfg.Bedrock, modelID, vaultRoot)
	}

	start := time.Now()
	var err error

	switch modelType {
	case "embedding":
		err = probeEmbedding(ctx, cfg, provider, modelID, vaultRoot)
	case "rerank":
		// No rerank probe exists; don't generation-probe a reranker (which would
		// fail confusingly). Enable a reranker with `2nb config set ai.rerank.enabled true`.
		err = fmt.Errorf("rerank models aren't testable via `2nb models test` yet")
	default:
		var snippet string
		snippet, err = probeGeneration(ctx, cfg, provider, modelID, vaultRoot)
		if err == nil {
			result.Detail = snippet
		}
	}

	result.Latency = time.Since(start).Round(time.Millisecond).String()

	// The model's invoke strategy tailors failure guidance (a mantle model's
	// access_denied points at AWS Sales, not the Bedrock console) and rides
	// out on the result so a UI can do the same. Resolved through the same
	// user-catalog-over-builtin chain the dispatcher uses.
	result.Strategy = ResolveInvokeStrategy(provider, modelID, vaultRoot)

	if err != nil {
		result.OK = false
		result.Detail = err.Error()
		result.Code = ClassifyProbeError(provider, err)
		result.Remediation = RemediationFor(result.Code, provider, result.Strategy)
	} else {
		result.OK = true
	}

	return result, nil
}

func probeEmbedding(ctx context.Context, cfg AIConfig, provider, modelID, vaultRoot string) error {
	switch provider {
	case "bedrock":
		carryVaultRegionPin(&cfg.Bedrock, modelID, vaultRoot)
		if err := BedrockPreflightModel(ctx, cfg.Bedrock, modelID, "embedding", vaultRoot); err != nil {
			return err
		}
		e, err := NewBedrockEmbedder(ctx, cfg.Bedrock, modelID, cfg.Dimensions)
		if err != nil {
			return err
		}
		vecs, err := e.Embed(ctx, []string{"test embedding probe"})
		if err != nil {
			return err
		}
		if len(vecs) == 0 || len(vecs[0]) == 0 {
			return fmt.Errorf("got empty embedding vector")
		}
		return nil

	case "openrouter":
		key, err := GetAPIKey("openrouter")
		if err != nil {
			return err
		}
		e := NewOpenRouterEmbedder(key, modelID, cfg.Dimensions)
		vecs, err := e.Embed(ctx, []string{"test embedding probe"})
		if err != nil {
			return err
		}
		if len(vecs) == 0 || len(vecs[0]) == 0 {
			return fmt.Errorf("got empty embedding vector")
		}
		return nil

	case "ollama":
		endpoint := cfg.Ollama.Endpoint
		if endpoint == "" {
			endpoint = "http://localhost:11434"
		}
		e := NewOllamaEmbedder(endpoint, modelID, cfg.Dimensions)
		vecs, err := e.Embed(ctx, []string{"test embedding probe"})
		if err != nil {
			return err
		}
		if len(vecs) == 0 || len(vecs[0]) == 0 {
			return fmt.Errorf("got empty embedding vector")
		}
		return nil

	default:
		return fmt.Errorf("unknown provider %q", provider)
	}
}

func probeGeneration(ctx context.Context, cfg AIConfig, provider, modelID, vaultRoot string) (string, error) {
	prompt := "What is 2+2? Reply with just the number."
	// ReasoningEffort "none" keeps a smoke probe deterministic: a mantle model
	// reasons by default and, with reasoning on, non-deterministically consumes
	// the whole output budget and returns a reasoning-only "incomplete"
	// response (no answer text). Turning reasoning off for the probe yields a
	// reliable short answer; non-mantle providers ignore the field. MaxTokens
	// is still floored internally by the mantle client. This deliberately
	// ignores cfg.ReasoningEffort: a user's thinking-depth preference applies to
	// real answers, not to an access check whose only job is to come back.
	opts := GenOpts{MaxTokens: 32, SystemPrompt: "You are a helpful assistant. Be concise.", ReasoningEffort: "none"}

	switch provider {
	case "bedrock":
		carryVaultRegionPin(&cfg.Bedrock, modelID, vaultRoot)
		if err := BedrockPreflightModel(ctx, cfg.Bedrock, modelID, "generation", vaultRoot); err != nil {
			return "", err
		}
		g, err := NewBedrockGeneration(ctx, cfg.Bedrock, modelID, vaultRoot)
		if err != nil {
			return "", err
		}
		return g.Generate(ctx, prompt, opts)

	case "openrouter":
		key, err := GetAPIKey("openrouter")
		if err != nil {
			return "", err
		}
		g := NewOpenRouterGenerator(key, modelID)
		return g.Generate(ctx, prompt, opts)

	case "ollama":
		endpoint := cfg.Ollama.Endpoint
		if endpoint == "" {
			endpoint = "http://localhost:11434"
		}
		g := NewOllamaGenerator(endpoint, modelID)
		return g.Generate(ctx, prompt, opts)

	default:
		return "", fmt.Errorf("unknown provider %q", provider)
	}
}

// regionRetryable reports whether a failure code is worth retrying in
// another region. Bedrock model entitlement, listings, and inference-profile
// coverage are all per-region, so not_found / invalid_request / access_denied
// can flip elsewhere. Credential, throttle, and transport failures cannot —
// retrying them only burns 30s timeouts.
func regionRetryable(code TestErrorCode) bool {
	switch code {
	case TestErrNotFound, TestErrInvalidRequest, TestErrAccessDenied:
		return true
	}
	return false
}

// regionAttempts builds the ordered attempt list for a region-aware probe:
// the included regions (primary first), with the model's catalog Region pin
// appended when it is not already present. Including the pin keeps a
// pinned-but-working model reachable, and putting primary FIRST is the
// self-heal: a pinned model always re-checks primary, even when the included
// set has collapsed back to a single region, so a stale pin cannot outlive
// the entitlement gap that created it. A nil result means "no fan-out".
func regionAttempts(regions []string, pinned string) []string {
	if pinned != "" {
		found := false
		for _, r := range regions {
			if r == pinned {
				found = true
				break
			}
		}
		if !found {
			regions = append(append([]string{}, regions...), pinned)
		}
	}
	if len(regions) <= 1 {
		return nil
	}
	return regions
}

// TestProbeModelInRegions probes modelID across the included region set,
// stopping at the first pass. Region 0 (the primary) always goes first, and a
// model carrying a catalog Region pin gets the pin appended to the attempt
// list — so a pinned model re-checks primary on EVERY probe (self-heal: a
// primary pass clears the pin via persistProbedRegion) while remaining
// reachable in its pinned region. Only classic-plane Bedrock models fan out:
// non-bedrock providers have no AWS region, mantle models are endpoint-pinned
// per model, and an unpinned single-region probe keeps the plain path.
//
// On exhaustion the PRIMARY region's failure is returned — keeping the
// reported classification stable for existing consumers — with the other
// attempted regions noted in Detail.
func TestProbeModelInRegions(ctx context.Context, cfg AIConfig, modelID, provider, modelType, vaultRoot string, regions []string) (*TestProbeResult, error) {
	if provider == "" {
		provider = InferProvider(modelID)
	}
	if provider != "bedrock" ||
		ResolveInvokeStrategy(provider, modelID, vaultRoot) == StrategyBedrockMantleResponses {
		return TestProbeModel(ctx, cfg, modelID, provider, modelType, vaultRoot)
	}
	if len(regions) == 0 {
		regions = []string{ResolveBedrockConfig(cfg.Bedrock).Region}
	}
	attempts := regionAttempts(regions, ResolveModelRegion("bedrock", modelID, vaultRoot))
	if attempts == nil {
		return TestProbeModel(ctx, cfg, modelID, provider, modelType, vaultRoot)
	}

	var primary *TestProbeResult
	var alsoFailed []string
	for i, region := range attempts {
		attempt := cfg
		attempt.Bedrock.RegionOverride = region
		result, err := TestProbeModel(ctx, attempt, modelID, provider, modelType, vaultRoot)
		if err != nil {
			return result, err
		}
		if result.OK {
			return result, nil
		}
		if i == 0 {
			primary = result
		} else {
			alsoFailed = append(alsoFailed, region)
		}
		if !regionRetryable(result.Code) {
			break
		}
	}
	if len(alsoFailed) > 0 {
		primary.Detail += fmt.Sprintf(" (also failed in %s)", strings.Join(alsoFailed, ", "))
	}
	return primary, nil
}

// InferProvider guesses the provider from model ID patterns.
func InferProvider(modelID string) string {
	// Bedrock: starts with region prefix or amazon/anthropic/meta namespace
	if strings.HasPrefix(modelID, "us.") ||
		strings.HasPrefix(modelID, "eu.") ||
		strings.HasPrefix(modelID, "ap.") ||
		strings.HasPrefix(modelID, "global.") ||
		strings.HasPrefix(modelID, "amazon.") ||
		strings.HasPrefix(modelID, "anthropic.") ||
		strings.HasPrefix(modelID, "openai.") ||
		strings.HasPrefix(modelID, "xai.") ||
		strings.HasPrefix(modelID, "meta.") ||
		strings.HasPrefix(modelID, "mistral.") ||
		strings.HasPrefix(modelID, "cohere.") {
		return "bedrock"
	}
	// OpenRouter: contains a slash (org/model format)
	if strings.Contains(modelID, "/") {
		return "openrouter"
	}
	// Default to Ollama for simple names
	return "ollama"
}

// InferModelType guesses embedding vs generation from model ID.
func InferModelType(modelID string) string {
	lower := strings.ToLower(modelID)
	if strings.Contains(lower, "embed") {
		return "embedding"
	}
	return "generation"
}
