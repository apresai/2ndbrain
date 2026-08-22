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
// Thin wrapper over TestProbeModelInfo with a hint-less candidate, so a bare
// model id (models test <id>) still resolves routing from the catalogs only.
func TestProbeModel(ctx context.Context, cfg AIConfig, modelID, provider, modelType, vaultRoot string) (*TestProbeResult, error) {
	return TestProbeModelInfo(ctx, cfg, ModelInfo{ID: modelID, Provider: provider, Type: modelType}, vaultRoot)
}

// effectiveInvokeStrategy resolves the invoke strategy for a probe
// candidate. Precedence: EXACT catalog declarations (user over builtin) are
// authored intent and always win; the candidate's own discovery-time mantle
// hint comes next; the catalog's profile-stripped base-match INFERENCE
// (ResolveInvokeStrategy's fallback) comes last. The hint must beat the base
// match because base matching exists so profile VARIANTS inherit a base
// entry's dialect — run first, it would let the classic us.xai.grok-4.6
// builtin drag the mantle-LISTED bare xai.grok-4.6 onto Converse and record
// a spurious FAIL, when the /v1/models listing is authoritative that the
// exact bare id lives on the mantle plane. A profile-prefixed id never takes
// the mantle hint (cross-region profiles exist only on the classic plane,
// the same guard ResolveInvokeStrategy applies to its own fallback).
func effectiveInvokeStrategy(provider string, m ModelInfo, vaultRoot string) string {
	field := func(mi ModelInfo) string { return mi.InvokeStrategy }
	if s := findCatalogStringExact(LoadUserCatalog(vaultRoot), provider, m.ID, field); s != "" {
		return s
	}
	if s := findCatalogStringExact(BuiltinCatalog(), provider, m.ID, field); s != "" {
		return s
	}
	if m.InvokeStrategy == StrategyBedrockMantleResponses {
		if strings.EqualFold(inferenceProfileBaseID(m.ID), m.ID) {
			return StrategyBedrockMantleResponses
		}
		// A profile-prefixed id can never be mantle: drop the hint and let
		// the catalog's base-match inference speak.
		return ResolveInvokeStrategy(provider, m.ID, vaultRoot)
	}
	if s := ResolveInvokeStrategy(provider, m.ID, vaultRoot); s != "" {
		return s
	}
	return m.InvokeStrategy
}

// TestProbeModelInfo is the hinted probe core: the candidate's own
// InvokeStrategy/Region fill in when the catalogs declare nothing, so a
// mantle-DISCOVERED model (no catalog entry anywhere) probes over the mantle
// plane in its listing region instead of classic-Converse-ing into a 404.
// Catalog resolution always wins over the hints (effectiveInvokeStrategy /
// the ResolveModelRegion gate below), so a user entry keeps full authority.
func TestProbeModelInfo(ctx context.Context, cfg AIConfig, m ModelInfo, vaultRoot string) (*TestProbeResult, error) {
	provider := m.Provider
	if provider == "" {
		provider = InferProvider(m.ID)
	}
	if provider == "" {
		return nil, fmt.Errorf("cannot infer provider for %q — use --provider", m.ID)
	}

	modelType := m.Type
	if modelType == "" {
		modelType = InferModelType(m.ID)
	}

	// The effective strategy drives the probe route, the strategy-aware
	// deadline below, the mantle-aware failure remediation, the GUI's
	// console-link suppression, and (via the result) persistProbedRegion's
	// mantle exclusion. Resolved BEFORE the context so the deadline can be
	// route-aware.
	strategy := effectiveInvokeStrategy(provider, m, vaultRoot)

	// The probe deadline contains the resolved route's full transport worst
	// case plus slack (ProbeDeadline, timeouts.go), so the innermost transport
	// bound always fires first and a timeout names the transport, never the
	// probe. The old flat 30s here sat INSIDE the mantle client's own retry
	// budget and killed working cold-start reasoning models. Deadlines bound
	// hangs; a slow-but-working model always passes.
	ctx, cancel := context.WithTimeout(ctx, ProbeDeadline(provider, strategy, modelType))
	defer cancel()

	// A hinted mantle candidate routes to its listing region on the existing
	// RegionOverride carrier: ResolveBedrockConfig maps the override onto
	// Region before mantleBaseURL's config-region fallback reads it, so no
	// client change is needed. Only when nothing else already routes the
	// call — an in-memory override (multi-region verify) or a catalog Region
	// pin (a persisted verify pass, a builtin) always wins.
	if provider == "bedrock" && strategy == StrategyBedrockMantleResponses &&
		m.Region != "" && cfg.Bedrock.RegionOverride == "" &&
		ResolveModelRegion("bedrock", m.ID, vaultRoot) == "" {
		cfg.Bedrock.RegionOverride = m.Region
	}

	result := &TestProbeResult{
		ModelID:  m.ID,
		Provider: provider,
		Type:     modelType,
	}
	if provider == "bedrock" {
		result.Region = EffectiveBedrockRegion(cfg.Bedrock, m.ID, vaultRoot)
	}

	start := time.Now()
	var err error

	switch modelType {
	case "embedding":
		err = probeEmbedding(ctx, cfg, provider, m.ID, vaultRoot)
	case "rerank":
		// No rerank probe exists; don't generation-probe a reranker (which would
		// fail confusingly). Enable a reranker with `2nb config set ai.rerank.enabled true`.
		err = fmt.Errorf("rerank models aren't testable via `2nb models test` yet")
	default:
		var snippet string
		snippet, err = probeGeneration(ctx, cfg, provider, m.ID, vaultRoot, strategy)
		if err == nil {
			result.Detail = snippet
		}
	}

	result.Latency = time.Since(start).Round(time.Millisecond).String()

	// The model's invoke strategy tailors failure guidance (a mantle model's
	// access_denied points at AWS Sales, not the Bedrock console) and rides
	// out on the result so a UI can do the same.
	result.Strategy = strategy

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

// probeGenMaxTokens is the generation smoke probe's output budget. 1024, not
// 256: always-reasoning models on the CLASSIC plane (grok-4.6) bill their
// reasoning against the output budget (a live "what is 2+2" cost 180 output
// tokens, with plenty of headroom to spare), but 256 leaves little margin
// for a deeper reasoning-by-default model to also emit answer text, and a
// cap that is too low truncates mid-reasoning with no answer text, failing a
// working model. The probe measures entitlement (can this account invoke
// the model at all), not budget (what a real answer should cost), so the
// constant errs generous: at least mantleMinOutputTokens, the mantle
// client's own floor for the same reasoning-overhead cause, with headroom
// above it rather than an exact match. The ProbeTest cost spec quotes this
// constant so the estimate can never under-report what a probe may bill (a
// wide `--discover` run scaling to 1024 output tokens per candidate may
// deliberately trip the default $0.50 verify cost cap, which is the cap
// working as intended); the offline TestProbeBudgetConstantsPinned holds
// all three together (the live guard, TestLiveGrok46ClassicConverse_CredGated
// in internal/cli, is credential-gated and invisible to plain CI).
const probeGenMaxTokens = 1024

// probeGeneration runs the generation smoke probe. strategy is the EFFECTIVE
// invoke strategy from effectiveInvokeStrategy (catalog first, candidate hint
// filling empty): a mantle-strategy probe skips the classic-control-plane
// preflight (the static allowlist and GetFoundationModel are blind to the
// plane — a hinted discovery would be misclassified incompatible) and routes
// construction through the strategy-aware dispatcher.
func probeGeneration(ctx context.Context, cfg AIConfig, provider, modelID, vaultRoot, strategy string) (string, error) {
	prompt := "What is 2+2? Reply with just the number."
	// ReasoningEffort "none" keeps a smoke probe deterministic: a mantle model
	// reasons by default and, with reasoning on, non-deterministically consumes
	// the whole output budget and returns a reasoning-only "incomplete"
	// response (no answer text). Turning reasoning off for the probe yields a
	// reliable short answer; non-mantle providers ignore the field. MaxTokens
	// is still floored internally by the mantle client. This deliberately
	// ignores cfg.ReasoningEffort: a user's thinking-depth preference applies to
	// real answers, not to an access check whose only job is to come back.
	opts := GenOpts{MaxTokens: probeGenMaxTokens, SystemPrompt: "You are a helpful assistant. Be concise.", ReasoningEffort: "none"}

	switch provider {
	case "bedrock":
		carryVaultRegionPin(&cfg.Bedrock, modelID, vaultRoot)
		if strategy != StrategyBedrockMantleResponses {
			if err := BedrockPreflightModel(ctx, cfg.Bedrock, modelID, "generation", vaultRoot); err != nil {
				return "", err
			}
		}
		g, err := NewBedrockGenerationRouted(ctx, cfg.Bedrock, modelID, vaultRoot, strategy)
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
// retrying them only burns full probe deadlines.
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
// stopping at the first pass. Thin wrapper over TestProbeModelInfoInRegions
// with a hint-less candidate.
func TestProbeModelInRegions(ctx context.Context, cfg AIConfig, modelID, provider, modelType, vaultRoot string, regions []string) (*TestProbeResult, error) {
	return TestProbeModelInfoInRegions(ctx, cfg, ModelInfo{ID: modelID, Provider: provider, Type: modelType}, vaultRoot, regions)
}

// TestProbeModelInfoInRegions probes the candidate across the included region
// set, stopping at the first pass. Region 0 (the primary) always goes first,
// and a model carrying a catalog Region pin gets the pin appended to the
// attempt list — so a pinned model re-checks primary on EVERY probe
// (self-heal: a primary pass clears the pin via persistProbedRegion) while
// remaining reachable in its pinned region. Only classic-plane Bedrock models
// fan out: non-bedrock providers have no AWS region, mantle models are
// endpoint-pinned per model, and an unpinned single-region probe keeps the
// plain path. The mantle exclusion uses the EFFECTIVE strategy (catalog or
// candidate hint) — a hinted, failing mantle discovery must not fan out
// across classic regions it can never pass in.
//
// On exhaustion the PRIMARY region's failure is returned — keeping the
// reported classification stable for existing consumers — with the other
// attempted regions noted in Detail.
func TestProbeModelInfoInRegions(ctx context.Context, cfg AIConfig, m ModelInfo, vaultRoot string, regions []string) (*TestProbeResult, error) {
	if m.Provider == "" {
		m.Provider = InferProvider(m.ID)
	}
	if m.Provider != "bedrock" ||
		effectiveInvokeStrategy(m.Provider, m, vaultRoot) == StrategyBedrockMantleResponses {
		return TestProbeModelInfo(ctx, cfg, m, vaultRoot)
	}
	if len(regions) == 0 {
		regions = []string{ResolveBedrockConfig(cfg.Bedrock).Region}
	}
	attempts := regionAttempts(regions, ResolveModelRegion("bedrock", m.ID, vaultRoot))
	if attempts == nil {
		return TestProbeModelInfo(ctx, cfg, m, vaultRoot)
	}

	var primary *TestProbeResult
	var alsoFailed []string
	for i, region := range attempts {
		attempt := cfg
		attempt.Bedrock.RegionOverride = region
		result, err := TestProbeModelInfo(ctx, attempt, m, vaultRoot)
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
