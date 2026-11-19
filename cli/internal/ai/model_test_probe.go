package ai

import (
	"context"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/apresai/2ndbrain/internal/llama"
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
	// Plane is the invocation plane this probe actually used. With Region it
	// completes the ROUTE the verdict belongs to, which is what the save path
	// keys on: without it, a probe of one endpoint could write its result
	// onto a different endpoint's row.
	Plane Plane `json:"plane,omitempty"`
}

// Route returns the endpoint this probe actually called.
func (r TestProbeResult) Route() RouteKey {
	return RouteKey{Provider: r.Provider, ID: r.ModelID, Plane: r.Plane, Region: r.Region}
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
// (resolveInvokeStrategy's fallback) comes last. The hint must beat the base
// match because base matching exists so profile VARIANTS inherit a base
// entry's dialect — run first, it would let the classic us.xai.grok-4.6
// builtin drag the mantle-LISTED bare xai.grok-4.6 onto Converse and record
// a spurious FAIL, when the /v1/models listing is authoritative that the
// exact bare id lives on the mantle plane. A profile-prefixed id never takes
// the mantle hint (cross-region profiles exist only on the classic plane,
// the same guard resolveInvokeStrategy applies to its own fallback).
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
		return resolveInvokeStrategy(provider, m.ID, vaultRoot)
	}
	if s := resolveInvokeStrategy(provider, m.ID, vaultRoot); s != "" {
		return s
	}
	return m.InvokeStrategy
}

// TestProbeModelInfo is the hinted probe core: the candidate's own
// InvokeStrategy/Region fill in when the catalogs declare nothing, so a
// mantle-DISCOVERED model (no catalog entry anywhere) probes over the mantle
// plane in its listing region instead of classic-Converse-ing into a 404.
// Catalog resolution always wins over the hints (effectiveInvokeStrategy /
// the resolveModelRegion gate below), so a user entry keeps full authority.
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

	// A candidate that NAMES its plane dispatches there, exactly as the invoke
	// path does (NewBedrockGenerationForRoute). effectiveInvokeStrategy is
	// exact-id and plane-blind, so without this a classic candidate for a
	// dual-plane id was billed against the MANTLE endpoint and its verdict
	// filed under a route that does not exist: asked classic/us-east-1, called
	// bedrock-mantle.us-west-2, recorded classic/us-west-2. Roughly a fifth of
	// discovered Bedrock ids are dual-plane, so this was not exotic.
	if provider == "bedrock" && m.Plane != "" {
		switch m.Plane {
		case PlaneMantle:
			strategy = StrategyBedrockMantleResponses
		case PlaneClassic:
			// Keep a classic dialect the catalog declared (Converse, the
			// InvokeModel variants); only a mantle answer is contradicted.
			if strategy == StrategyBedrockMantleResponses {
				strategy = m.InvokeStrategy
				if strategy == StrategyBedrockMantleResponses {
					strategy = ""
				}
			}
		}
	}

	// The probe deadline contains the resolved route's full transport worst
	// case plus slack (ProbeDeadline, timeouts.go), so the innermost transport
	// bound always fires first and a timeout names the transport, never the
	// probe. The old flat 30s here sat INSIDE the mantle client's own retry
	// budget and killed working cold-start reasoning models. Deadlines bound
	// hangs; a slow-but-working model always passes.
	ctx, cancel := context.WithTimeout(ctx, ProbeDeadline(provider, strategy, modelType))
	defer cancel()

	// A candidate that NAMES its region is probed there, on either plane.
	//
	// This used to apply only to mantle candidates, and only when the catalog
	// declared no pin of its own — so a CLASSIC candidate naming us-east-1 was
	// probed in whatever region an exact-id, plane-blind resolveModelRegion
	// lookup returned, and the verdict was recorded against that region. Once
	// verify selects a specific route to probe, deferring to the catalog
	// discards the selection.
	//
	// An explicit in-memory override still wins: that is the multi-region
	// fan-out naming the region it is currently attempting.
	if provider == "bedrock" && m.Region != "" && cfg.Bedrock.RegionOverride == "" {
		cfg.Bedrock.RegionOverride = m.Region
	}

	result := &TestProbeResult{
		ModelID:  m.ID,
		Provider: provider,
		Type:     modelType,
	}
	if provider == "bedrock" {
		if cfg.Bedrock.RegionOverride != "" {
			result.Region = cfg.Bedrock.RegionOverride
		} else {
			result.Region = ResolveBedrockConfig(cfg.Bedrock).Region
		}
		// Derive the plane from the strategy ACTUALLY dispatched, never from
		// the candidate directly. The two now agree by construction (the
		// candidate's plane is applied to the strategy above), and deriving
		// from the dispatch is what keeps them honest: a verdict must name the
		// endpoint that was called, not the one that was requested.
		result.Plane = planeForStrategy(strategy)
	}

	start := time.Now()
	var err error

	switch modelType {
	case "embedding":
		var embedDetail string
		embedDetail, err = probeEmbedding(ctx, cfg, provider, m.ID, vaultRoot, strategy)
		if err == nil {
			result.Detail = embedDetail
		}
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

// probeEmbeddingText is the fixed input every embedding smoke probe sends.
const probeEmbeddingText = "test embedding probe"

// resolveProbeDimensions picks the dimension an embedding probe requests
// (probeDim) and the dimension the returned vector MUST have (strictExpectedDim;
// 0 means "do not enforce a width").
//
// A probe is the proof that a model answers, so when the probed model is the
// configured embedding slot the user's own ai.dimensions is honored and (when
// it is a width the catalog says the model can emit) enforced. A manual
// override outside the catalog's declared widths is still requested but not
// enforced, so a clamping provider does not turn into a false failure. For any
// other model the probe uses the catalog default and enforces it.
func resolveProbeDimensions(cfg AIConfig, provider, modelID, vaultRoot string) (probeDim int, strictExpectedDim int) {
	defDim := EmbeddingDimensionsFor(vaultRoot, provider, modelID)
	supported := SupportedDimensionsFor(vaultRoot, provider, modelID)
	validWidth := func(d int) bool {
		return d == defDim || slices.Contains(supported, d)
	}

	if cfg.Dimensions > 0 && cfg.EffectiveEmbeddingProvider() == provider && cfg.EmbeddingModel == modelID {
		probeDim = cfg.Dimensions
		if defDim > 0 && validWidth(cfg.Dimensions) {
			strictExpectedDim = cfg.Dimensions
		}
		return probeDim, strictExpectedDim
	}

	if cfg.Dimensions > 0 && validWidth(cfg.Dimensions) {
		probeDim = cfg.Dimensions
	} else {
		probeDim = defDim
	}
	if probeDim > 0 {
		strictExpectedDim = probeDim
	}
	return probeDim, strictExpectedDim
}

// validateEmbeddingProbeVector turns a returned embedding into evidence: it
// must be non-empty, the expected width (when enforced), finite, and carry a
// non-degenerate L2 norm. The detail keeps "dims=%d" intact because
// parseDimsFromDetail reads it back out.
func validateEmbeddingProbeVector(modelID string, vecs [][]float32, strictExpectedDim int) (string, error) {
	if len(vecs) == 0 || len(vecs[0]) == 0 {
		return "", fmt.Errorf("got empty embedding vector")
	}
	vec := vecs[0]
	if strictExpectedDim > 0 && len(vec) != strictExpectedDim {
		return "", fmt.Errorf("embedding dimension mismatch from %s: got %dd, expected %dd", modelID, len(vec), strictExpectedDim)
	}
	var sum float64
	for i, v := range vec {
		f := float64(v)
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return "", fmt.Errorf("embedding from %s contains a non-finite value at index %d", modelID, i)
		}
		sum += f * f
	}
	norm := math.Sqrt(sum)
	if norm <= 1e-6 || math.IsNaN(norm) || math.IsInf(norm, 0) {
		return "", fmt.Errorf("embedding from %s has zero/degenerate L2 norm", modelID)
	}
	return fmt.Sprintf("dims=%d, L2=%.2f", len(vec), norm), nil
}

func probeEmbedding(ctx context.Context, cfg AIConfig, provider, modelID, vaultRoot, strategy string) (string, error) {
	probeDim, strictDim := resolveProbeDimensions(cfg, provider, modelID, vaultRoot)
	texts := []string{probeEmbeddingText}

	switch provider {
	case "gemini":
		e, err := NewGeminiEmbedder(ctx, cfg.Gemini, modelID, probeDim)
		if err != nil {
			return "", err
		}
		vecs, err := e.Embed(ctx, texts)
		if err != nil {
			return "", err
		}
		return validateEmbeddingProbeVector(modelID, vecs, strictDim)

	case "bedrock":
		if err := BedrockPreflightModel(ctx, cfg.Bedrock, modelID, "embedding", vaultRoot); err != nil {
			return "", err
		}
		e, err := newBedrockEmbedder(ctx, cfg.Bedrock, modelID, probeDim, strategy)
		if err != nil {
			return "", err
		}
		// Belt: if the constructor ignored the strategy argument, overwrite
		// the catalog lookup. The test mutates assignResolvedEmbedStrategy.
		assignResolvedEmbedStrategy(e, strategy)
		vecs, err := e.Embed(ctx, texts)
		if err != nil {
			return "", err
		}
		return validateEmbeddingProbeVector(modelID, vecs, strictDim)

	case "openrouter":
		key, err := GetAPIKey("openrouter")
		if err != nil {
			return "", err
		}
		e := NewOpenRouterEmbedder(key, modelID, probeDim)
		vecs, err := e.Embed(ctx, texts)
		if err != nil {
			return "", err
		}
		return validateEmbeddingProbeVector(modelID, vecs, strictDim)

	case "ollama":
		endpoint := cfg.Ollama.Endpoint
		if endpoint == "" {
			endpoint = "http://localhost:11434"
		}
		e := NewOllamaEmbedder(endpoint, modelID, probeDim)
		vecs, err := e.Embed(ctx, texts)
		if err != nil {
			return "", err
		}
		return validateEmbeddingProbeVector(modelID, vecs, strictDim)

	case "llama-local":
		// An explicit endpoint is dialed directly (it names a server the caller
		// vouches for); only the default path needs cached weights to start one.
		if cfg.Llama.EmbedEndpoint == "" && modelID != "" && !llama.RoleCanStart(llama.RoleEmbed, modelID) {
			return "", fmt.Errorf("local model %q not available for embedding (download weights or specify valid model id)", modelID)
		}
		e := NewLlamaEmbedder(modelID, probeDim, cfg.ResolveTaskPreset(), cfg.Llama.EmbedEndpoint)
		vecs, err := e.Embed(ctx, texts)
		if err != nil {
			return "", err
		}
		return validateEmbeddingProbeVector(modelID, vecs, strictDim)

	default:
		return "", fmt.Errorf("unknown provider %q", provider)
	}
}

// truncateProbeRunes shortens s to at most max runes (never splitting a rune),
// appending an ellipsis when it cut.
func truncateProbeRunes(s string, max int) string {
	if max <= 0 || utf8.RuneCountInString(s) <= max {
		return s
	}
	r := []rune(s)
	return string(r[:max]) + "…"
}

// validateGenerationProbeResponse requires the model to actually answer the
// probe prompt ("What is 2+2?") rather than merely return bytes: a model that
// replies with nothing, or with something unrelated, has not been proven.
func validateGenerationProbeResponse(modelID, raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", fmt.Errorf("empty response from %s", modelID)
	}
	cleaned := strings.Join(strings.Fields(trimmed), " ")
	if !strings.Contains(cleaned, "4") && !strings.Contains(strings.ToLower(cleaned), "four") {
		return "", fmt.Errorf("unexpected response from %s (expected 4, got %q)", modelID, truncateProbeRunes(cleaned, 80))
	}
	return truncateProbeRunes(cleaned, 80), nil
}

// assignResolvedEmbedStrategy writes the probe's already-resolved strategy
// onto the embedder so Embed does not re-derive the format from a catalog
// lookup of the id. Empty strategy leaves the constructor's value.
func assignResolvedEmbedStrategy(e *BedrockEmbedder, strategy string) {
	if e == nil || strategy == "" {
		return
	}
	e.strategy = strategy
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
	text, err := probeGenerationRaw(ctx, cfg, provider, modelID, vaultRoot, strategy)
	if err != nil {
		return "", err
	}
	return validateGenerationProbeResponse(modelID, text)
}

func probeGenerationRaw(ctx context.Context, cfg AIConfig, provider, modelID, vaultRoot, strategy string) (string, error) {
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
	// The one exception: probing the configured Gemini generation model honors
	// the user's reasoning_effort, so the probe proves the model works at the
	// depth it will actually be asked to run at.
	if provider == "gemini" && cfg.EffectiveGenerationProvider() == "gemini" && modelID == cfg.GenerationModel && strings.TrimSpace(cfg.ReasoningEffort) != "" {
		opts.ReasoningEffort = cfg.ReasoningEffort
	}

	switch provider {
	case "gemini":
		g, err := NewGeminiGenerator(ctx, cfg.Gemini, modelID)
		if err != nil {
			return "", err
		}
		return g.Generate(ctx, prompt, opts)

	case "bedrock":
		if strategy != StrategyBedrockMantleResponses {
			if err := BedrockPreflightModel(ctx, cfg.Bedrock, modelID, "generation", vaultRoot); err != nil {
				return "", err
			}
		}
		// Dispatch on the RESOLVED plane, the same way the invoke path does.
		// Passing the strategy as a hint to NewBedrockGenerationRouted let
		// effectiveInvokeStrategy re-derive it plane-blind and overrule the
		// caller, so a probe could bill the mantle endpoint for a classic
		// candidate and then file the verdict under a route that was never
		// called.
		g, err := NewBedrockGenerationForRoute(ctx, cfg.Bedrock, SlotRoute{
			Route:    RouteKey{Provider: "bedrock", ID: modelID, Plane: planeForStrategy(strategy)},
			Strategy: strategy,
		}, vaultRoot)
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

	case "llama-local":
		if cfg.Llama.GenEndpoint == "" && modelID != "" && !llama.RoleCanStart(llama.RoleGen, modelID) {
			return "", fmt.Errorf("local model %q not available for generation (download weights or specify valid model id)", modelID)
		}
		g := NewLlamaGenerator(modelID, cfg.Llama.GenEndpoint)
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
// a pinned model re-checks primary first so a stale pin cannot outlive
// the entitlement gap that created it. NOTE: persistProbedRegion no longer
// CLEARS a pin on a primary pass (region is identity now, so clearing it
// wrote a second row); the self-heal is that the primary route records its own
// fresh verdict, which PreferRoutes ranks above its siblings.
// A nil result means "no fan-out".
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
// (a primary pass records its own fresh verdict, which PreferRoutes ranks
// above the pinned sibling; it no longer CLEARS the pin) while
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
	// A candidate that NAMES its region is probed there and nowhere else, on
	// EITHER plane.
	//
	// Region fan-out predates routes: it existed to discover which region
	// serves a model when the row could not say. A row carrying its own region
	// already answers that, and each region is now its own candidate, so
	// fanning out would probe endpoints the caller did not ask about, bill for
	// them, and — because the fan-out returns the PRIMARY region's verdict on
	// exhaustion — report a result for a different endpoint than the one
	// named. A route-less candidate keeps the old behavior exactly.
	//
	// This has to precede the mantle early return below. When it sat after it,
	// a mantle candidate never received the override and fell back to the
	// plane-blind resolveModelRegion pin, so `verify <id>@mantle/us-east-2`
	// probed us-west-2 — on exactly the plane where per-region entitlement
	// differs most, and which is region-pinned per model by design.
	if m.Provider == "bedrock" && m.Region != "" {
		cfg.Bedrock.RegionOverride = m.Region
		return TestProbeModelInfo(ctx, cfg, m, vaultRoot)
	}
	if !usesClassicRegionFanOut(m, vaultRoot) {
		return TestProbeModelInfo(ctx, cfg, m, vaultRoot)
	}
	if len(regions) == 0 {
		regions = []string{ResolveBedrockConfig(cfg.Bedrock).Region}
	}
	pin := ""
	if m.Plane == "" {
		pin = resolveModelRegion("bedrock", m.ID, vaultRoot)
	}
	attempts := regionAttempts(regions, pin)
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

// usesClassicRegionFanOut is true when the candidate should be probed across
// included classic regions. A named mantle plane, or a route-less id whose
// effective strategy is mantle, must not fan out: the catalog lookup that
// used to answer "is this mantle?" is plane-blind and first-match, so a
// @classic candidate of a dual-plane id skipped classic region retry.
func usesClassicRegionFanOut(m ModelInfo, vaultRoot string) bool {
	if m.Provider != "bedrock" || m.Region != "" {
		return false
	}
	if m.Plane == PlaneMantle {
		return false
	}
	if m.Plane == "" && effectiveInvokeStrategy(m.Provider, m, vaultRoot) == StrategyBedrockMantleResponses {
		return false
	}
	return true
}

// InferProvider guesses the provider from model ID patterns.
func InferProvider(modelID string) string {
	if strings.HasPrefix(modelID, "gemini-") ||
		strings.HasPrefix(modelID, "gemini/") ||
		strings.HasPrefix(modelID, "google.") {
		return "gemini"
	}
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
	// llama-local: local Gemma models and local cross-encoder models
	if strings.HasPrefix(modelID, "embeddinggemma-") ||
		strings.HasPrefix(modelID, "gemma4-") ||
		strings.HasPrefix(modelID, "bge-reranker-") {
		return "llama-local"
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
	if strings.Contains(lower, "rerank") {
		return "rerank"
	}
	if strings.Contains(lower, "embed") {
		return "embedding"
	}
	return "generation"
}
