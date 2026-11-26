package ai

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"os"
	"strings"
	"sync"
	"time"

	"google.golang.org/genai"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	// DefaultGeminiEmbeddingModel is the standard multimodal + text embedding model.
	DefaultGeminiEmbeddingModel = "gemini-embedding-2"
	// DefaultGeminiGenerationModel is the default Gemini 4 multimodal reasoning model.
	DefaultGeminiGenerationModel = "gemini-4-hydrogen"
	// DefaultGeminiDimensions is the default embedding dimension (768).
	DefaultGeminiDimensions = 768
	// DefaultGeminiLocation is the default GCP region.
	DefaultGeminiLocation = "us-central1"
	// DefaultGeminiAppLabel is the mandatory cost-attribution label for Vertex AI.
	DefaultGeminiAppLabel = "2nb"

	// Gemini EmbedContent limits: Gemini Developer API caps batches at 100
	// requests per call, and Vertex AI enforces a 20,000 input token ceiling per
	// call. Sub-batching to <=100 items and <=48 KB keeps single large-note
	// embeddings safe on both backends while preserving 1:1 chunk vector order.
	geminiEmbedMaxBatchItems = 100
	geminiEmbedMaxBatchChars = 48000

	// gemini4MinOutputTokens floors MaxOutputTokens on Gemini 4 models. Input,
	// thinking, and output tokens share one 1M context window, and Gemini 4
	// defaults to thinking enabled (HIGH on Hydrogen, MEDIUM on Argon/Carbon);
	// Google's migration guide recommends >= 32,768 so reasoning overhead never
	// starves or truncates the answer.
	gemini4MinOutputTokens = 32768
	// geminiThinkingLevelXHigh requests extended reasoning depth on gemini-4-hydrogen.
	geminiThinkingLevelXHigh genai.ThinkingLevel = "XHIGH"
)

// ResolveGCPProject resolves the active GCP project ID from config or environment.
// Precedence: cfgProject -> GOOGLE_CLOUD_PROJECT -> GCLOUD_PROJECT -> CLOUDSDK_CORE_PROJECT.
func ResolveGCPProject(cfgProject string) string {
	if cfgProject != "" {
		return cfgProject
	}
	for _, env := range []string{"GOOGLE_CLOUD_PROJECT", "GCLOUD_PROJECT", "CLOUDSDK_CORE_PROJECT"} {
		if val := os.Getenv(env); val != "" {
			return val
		}
	}
	return ""
}

// normalizeL2 normalizes a vector to unit length (L2 norm = 1.0).
// Includes epsilon protection against division by zero.
func normalizeL2(vec []float32) []float32 {
	var sum float64
	for _, v := range vec {
		sum += float64(v) * float64(v)
	}
	norm := math.Sqrt(sum)
	const epsilon = 1e-12
	if norm < epsilon {
		return vec
	}
	normF32 := float32(norm)
	out := make([]float32, len(vec))
	for i, v := range vec {
		out[i] = v / normF32
	}
	return out
}

// ResolveGCPLocation resolves the Vertex AI location. Precedence: cfgLocation
// -> GOOGLE_CLOUD_LOCATION -> CLOUD_ML_REGION -> DefaultGeminiLocation.
func ResolveGCPLocation(cfgLocation string) string {
	if v := strings.TrimSpace(cfgLocation); v != "" {
		return v
	}
	for _, env := range []string{"GOOGLE_CLOUD_LOCATION", "CLOUD_ML_REGION"} {
		if val := strings.TrimSpace(os.Getenv(env)); val != "" {
			return val
		}
	}
	return DefaultGeminiLocation
}

// geminiProbeModeEnv lets a diagnostic caller (`2nb doctor` from the macOS app)
// pin WHICH Gemini credential path is under test, so a typed API key is judged
// as an API key and a typed project as Vertex AI, instead of whichever one the
// ambient config happens to select.
const geminiProbeModeEnv = "2NB_DOCTOR_PROBE_GEMINI_MODE"

// geminiProbeMode returns "apikey", "vertex", or "" (no pin).
func geminiProbeMode() string {
	switch m := strings.ToLower(strings.TrimSpace(os.Getenv(geminiProbeModeEnv))); m {
	case "apikey", "vertex":
		return m
	}
	return ""
}

// geminiUsesVertex reports whether cfg selects Vertex AI, honoring the probe
// mode pin: "apikey" ignores UseVertexAI/Project/GOOGLE_CLOUD_PROJECT entirely.
func geminiUsesVertex(cfg GeminiConfig) bool {
	switch geminiProbeMode() {
	case "apikey":
		return false
	case "vertex":
		return true
	}
	return cfg.UseVertexAI || ResolveGCPProject(cfg.Project) != ""
}

func geminiVertexClientConfig(project, location string) *genai.ClientConfig {
	return &genai.ClientConfig{
		Backend:     genai.BackendVertexAI,
		Project:     project,
		Location:    location,
		HTTPOptions: genai.HTTPOptions{APIVersion: "v1"},
	}
}

func geminiAPIClientConfig(apiKey string) *genai.ClientConfig {
	return &genai.ClientConfig{
		Backend:     genai.BackendGeminiAPI,
		APIKey:      apiKey,
		HTTPOptions: genai.HTTPOptions{APIVersion: "v1beta"},
	}
}

// NewGeminiClient initializes a Google GenAI SDK client for either Vertex AI
// or Gemini API. Strictly enforces project rules: GOOGLE_API_KEY is prohibited.
func NewGeminiClient(ctx context.Context, cfg GeminiConfig) (*genai.Client, error) {
	if os.Getenv("GOOGLE_API_KEY") != "" {
		return nil, fmt.Errorf("GOOGLE_API_KEY is prohibited by project policy; unset it and use GEMINI_API_KEY or Vertex AI ADC")
	}

	// If Vertex AI is requested (or pinned) OR a GCP project is configured, use Vertex AI.
	if geminiUsesVertex(cfg) {
		project := ResolveGCPProject(cfg.Project)
		if project == "" {
			return nil, fmt.Errorf("no GCP project configured. Set ai.gemini.project with '2nb config set ai.gemini.project <your-gcp-project-id>' or set GOOGLE_CLOUD_PROJECT")
		}
		return genai.NewClient(ctx, geminiVertexClientConfig(project, ResolveGCPLocation(cfg.Location)))
	}

	// Smooth BYOK ergonomics: fallback to Gemini API (API key)
	keyEnv := cfg.APIKeyEnv
	if keyEnv == "" || geminiProbeMode() == "apikey" {
		keyEnv = "GEMINI_API_KEY"
	}
	apiKey := os.Getenv(keyEnv)
	if apiKey == "" {
		var err error
		apiKey, err = GetAPIKey("gemini")
		if err != nil || apiKey == "" {
			return nil, fmt.Errorf("%s is not set: set %s or run `2nb config set-key gemini` (or configure Vertex AI with `2nb config set ai.gemini.project <project-id>`)", keyEnv, keyEnv)
		}
	}

	return genai.NewClient(ctx, geminiAPIClientConfig(apiKey))
}

// CheckGeminiCredentials reports whether valid Google / Gemini credentials exist.
func CheckGeminiCredentials(ctx context.Context, cfg GeminiConfig) bool {
	if os.Getenv("GOOGLE_API_KEY") != "" {
		return false
	}
	if geminiUsesVertex(cfg) {
		if ResolveGCPProject(cfg.Project) == "" {
			return false
		}
		client, err := NewGeminiClient(ctx, cfg)
		return err == nil && client != nil
	}

	keyEnv := cfg.APIKeyEnv
	if keyEnv == "" || geminiProbeMode() == "apikey" {
		keyEnv = "GEMINI_API_KEY"
	}
	if os.Getenv(keyEnv) != "" {
		return true
	}
	return HasAPIKey("gemini")
}

// GeminiProvider implements EmbeddingProvider, GenerationProvider, UsageGenerator,
// and AvailabilityReporter using google.golang.org/genai.
type GeminiProvider struct {
	mu         sync.Mutex
	client     *genai.Client
	lazyInit   bool
	cfg        GeminiConfig
	embedModel string
	genModel   string
	dims       int
	avail      availableCache
}

func (p *GeminiProvider) ensureClient(ctx context.Context) (*genai.Client, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.client != nil {
		return p.client, nil
	}
	if !p.lazyInit {
		return nil, fmt.Errorf("gemini client not initialized: missing credentials")
	}
	client, err := NewGeminiClient(ctx, p.cfg)
	if err != nil {
		return nil, err
	}
	p.client = client
	return client, nil
}

// NewGeminiProvider creates a Gemini provider with configured models.
func NewGeminiProvider(ctx context.Context, cfg GeminiConfig, embedModel, genModel string, dims int) (*GeminiProvider, error) {
	client, err := NewGeminiClient(ctx, cfg)
	if err != nil {
		return nil, err
	}
	return NewGeminiProviderWithClient(client, cfg, embedModel, genModel, dims), nil
}

// NewGeminiProviderWithClient wraps an existing GenAI client.
func NewGeminiProviderWithClient(client *genai.Client, cfg GeminiConfig, embedModel, genModel string, dims int) *GeminiProvider {
	if embedModel == "" {
		embedModel = DefaultGeminiEmbeddingModel
	}
	if genModel == "" {
		genModel = DefaultGeminiGenerationModel
	}
	if dims <= 0 {
		dims = DefaultGeminiDimensions
	}
	return &GeminiProvider{
		client:     client,
		cfg:        cfg,
		embedModel: embedModel,
		genModel:   genModel,
		dims:       dims,
	}
}

// NewGeminiEmbedder creates an embedder-focused Gemini provider instance.
func NewGeminiEmbedder(ctx context.Context, cfg GeminiConfig, model string, dims int) (*GeminiProvider, error) {
	if model == "" {
		model = DefaultGeminiEmbeddingModel
	}
	if dims <= 0 {
		dims = DefaultGeminiDimensions
	}
	return NewGeminiProvider(ctx, cfg, model, DefaultGeminiGenerationModel, dims)
}

// NewGeminiGenerator creates a generator-focused Gemini provider instance.
func NewGeminiGenerator(ctx context.Context, cfg GeminiConfig, model string) (*GeminiProvider, error) {
	if model == "" {
		model = DefaultGeminiGenerationModel
	}
	return NewGeminiProvider(ctx, cfg, DefaultGeminiEmbeddingModel, model, DefaultGeminiDimensions)
}

func (p *GeminiProvider) Name() string    { return "gemini" }
func (p *GeminiProvider) Dimensions() int { return p.dims }

func (p *GeminiProvider) isVertexAI() bool {
	return geminiUsesVertex(p.cfg)
}

// Embed computes vector embeddings using Google GenAI SDK.
func (p *GeminiProvider) Embed(ctx context.Context, texts []string, opts ...EmbedOption) ([][]float32, error) {
	client, err := p.ensureClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("gemini embedder: %w", err)
	}
	if len(texts) == 0 {
		return nil, nil
	}

	resolved := ResolveEmbedOptions(opts...)
	dims := p.dims
	if resolved.Dimension > 0 {
		dims = resolved.Dimension
	}

	config := &genai.EmbedContentConfig{}
	if dims > 0 {
		config.OutputDimensionality = genai.Ptr[int32](int32(dims))
	}

	switch resolved.Purpose {
	case PurposeIndex:
		config.TaskType = "RETRIEVAL_DOCUMENT"
		if resolved.Title != "" {
			config.Title = resolved.Title
		}
	case PurposeQuery, PurposeQueryText:
		config.TaskType = "RETRIEVAL_QUERY"
	default:
		config.TaskType = "RETRIEVAL_DOCUMENT"
	}

	// Sub-batch to stay inside Gemini API's 100-request cap and Vertex AI's
	// per-call input token ceiling while preserving 1:1 order with texts.
	batches := splitGeminiEmbedBatches(texts, geminiEmbedMaxBatchItems, geminiEmbedMaxBatchChars)
	results := make([][]float32, 0, len(texts))
	for _, batch := range batches {
		batchVecs, err := p.embedBatch(ctx, client, batch, config, dims)
		if err != nil {
			return nil, err
		}
		results = append(results, batchVecs...)
	}

	return results, nil
}

func (p *GeminiProvider) embedBatch(ctx context.Context, client *genai.Client, batch []string, config *genai.EmbedContentConfig, dims int) ([][]float32, error) {
	// Distinct Content objects for each chunk in the sub-batch (preserves individual vectors).
	contents := make([]*genai.Content, len(batch))
	for i, t := range batch {
		contents[i] = &genai.Content{
			Parts: []*genai.Part{genai.NewPartFromText(t)},
		}
	}

	var resp *genai.EmbedContentResponse
	var lastErr error

	for attempt := 0; attempt < geminiMaxAttempts; attempt++ {
		attemptCtx, cancel := context.WithTimeout(ctx, GeminiAttemptTimeout)
		resp, lastErr = client.Models.EmbedContent(attemptCtx, p.embedModel, contents, config)
		cancel()

		if lastErr == nil {
			break
		}

		if isGeminiRetryable(lastErr) && attempt < geminiMaxAttempts-1 {
			wait := geminiRetryDelay(attempt, lastErr)
			noteGeminiRetry(ctx, "embed", attempt+1, geminiMaxAttempts, wait, lastErr)
			select {
			case <-time.After(wait):
				continue
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		return nil, fmt.Errorf("gemini embed: %w", lastErr)
	}

	if resp == nil || len(resp.Embeddings) == 0 {
		return nil, fmt.Errorf("gemini embed: empty embeddings returned")
	}
	if len(resp.Embeddings) != len(batch) {
		return nil, fmt.Errorf("gemini embed: got %d embeddings for %d inputs", len(resp.Embeddings), len(batch))
	}

	results := make([][]float32, len(resp.Embeddings))
	for i, emb := range resp.Embeddings {
		vec := emb.Values
		// Ensure unit L2 norm across Matryoshka truncated widths (<3072 dims).
		if dims > 0 && dims < 3072 {
			vec = normalizeL2(vec)
		}
		results[i] = vec
	}

	return results, nil
}

// splitGeminiEmbedBatches partitions texts into ordered sub-batches bounded by
// both maxItems and maxChars (UTF-8 bytes). A single item exceeding maxChars
// stays intact as its own 1-item batch so forward progress is always guaranteed.
func splitGeminiEmbedBatches(texts []string, maxItems, maxChars int) [][]string {
	if len(texts) == 0 {
		return nil
	}
	if maxItems <= 0 {
		maxItems = geminiEmbedMaxBatchItems
	}
	if maxChars <= 0 {
		maxChars = geminiEmbedMaxBatchChars
	}
	var batches [][]string
	start := 0
	batchChars := 0
	for i, t := range texts {
		if i > start && (i-start >= maxItems || batchChars+len(t) > maxChars) {
			batches = append(batches, texts[start:i])
			start = i
			batchChars = 0
		}
		batchChars += len(t)
	}
	if start < len(texts) {
		batches = append(batches, texts[start:])
	}
	return batches
}

// Generate produces text completion using the specified generation model.
func (p *GeminiProvider) Generate(ctx context.Context, prompt string, opts GenOpts) (string, error) {
	text, _, err := p.GenerateWithUsage(ctx, prompt, opts)
	return text, err
}

// GenerateWithUsage produces text completion and returns token usage metadata.
func (p *GeminiProvider) GenerateWithUsage(ctx context.Context, prompt string, opts GenOpts) (string, GenUsage, error) {
	client, err := p.ensureClient(ctx)
	if err != nil {
		return "", GenUsage{}, fmt.Errorf("gemini generator: %w", err)
	}

	config := buildGeminiGenerateConfig(p.genModel, p.isVertexAI(), p.cfg.AppLabel, opts)

	contents := []*genai.Content{
		{
			Parts: []*genai.Part{genai.NewPartFromText(prompt)},
		},
	}

	var resp *genai.GenerateContentResponse
	var lastErr error

	for attempt := 0; attempt < geminiMaxAttempts; attempt++ {
		attemptCtx, cancel := context.WithTimeout(ctx, GeminiAttemptTimeout)
		resp, lastErr = client.Models.GenerateContent(attemptCtx, p.genModel, contents, config)
		cancel()

		if lastErr == nil {
			break
		}

		if isGeminiRetryable(lastErr) && attempt < geminiMaxAttempts-1 {
			wait := geminiRetryDelay(attempt, lastErr)
			noteGeminiRetry(ctx, "generate", attempt+1, geminiMaxAttempts, wait, lastErr)
			select {
			case <-time.After(wait):
				continue
			case <-ctx.Done():
				return "", GenUsage{}, ctx.Err()
			}
		}
		return "", GenUsage{}, fmt.Errorf("gemini generate: %w", lastErr)
	}

	if resp == nil {
		return "", GenUsage{}, fmt.Errorf("gemini generate: nil response")
	}

	text := resp.Text()
	var usage GenUsage
	if resp.UsageMetadata != nil {
		usage.InputTokens = int(resp.UsageMetadata.PromptTokenCount)
		usage.OutputTokens = int(resp.UsageMetadata.CandidatesTokenCount + resp.UsageMetadata.ThoughtsTokenCount)
	}
	if strings.TrimSpace(text) == "" {
		return "", usage, fmt.Errorf("gemini generate: empty output from %s; reasoning or finish constraint likely consumed the budget", p.genModel)
	}

	return text, usage, nil
}

func isGeminiThinkingLevelModel(modelID string) bool {
	m := strings.ToLower(strings.TrimSpace(modelID))
	return strings.Contains(m, "gemini-3") || strings.Contains(m, "gemini-4")
}

func isGemini4Model(modelID string) bool {
	return strings.Contains(strings.ToLower(strings.TrimSpace(modelID)), "gemini-4")
}

// buildGeminiGenerateConfig constructs GenerateContentConfig with Gemini 4
// parameter rules: omits sampler params (Temperature) on thinking-level models,
// floors MaxOutputTokens at gemini4MinOutputTokens on Gemini 4 so default-on
// reasoning cannot starve answers, attaches Vertex AI attribution labels, and
// applies ThinkingConfig.
func buildGeminiGenerateConfig(modelID string, isVertexAI bool, appLabel string, opts GenOpts) *genai.GenerateContentConfig {
	config := &genai.GenerateContentConfig{}

	if opts.SystemPrompt != "" {
		config.SystemInstruction = &genai.Content{
			Parts: []*genai.Part{genai.NewPartFromText(opts.SystemPrompt)},
		}
	}
	if opts.Temperature != nil && !isGeminiThinkingLevelModel(modelID) {
		config.Temperature = genai.Ptr[float32](float32(*opts.Temperature))
	}
	maxTokens := opts.MaxTokens
	if isGemini4Model(modelID) && maxTokens < gemini4MinOutputTokens {
		maxTokens = gemini4MinOutputTokens
	}
	if maxTokens > 0 {
		config.MaxOutputTokens = int32(maxTokens)
	}

	// Request labeling for cost attribution on Vertex AI
	if isVertexAI {
		if appLabel == "" {
			appLabel = DefaultGeminiAppLabel
		}
		config.Labels = map[string]string{"app": appLabel}
	}

	if tc := thinkingConfigForModel(modelID, opts.ReasoningEffort); tc != nil {
		config.ThinkingConfig = tc
	}
	return config
}

// thinkingConfigForModel maps 2nb's provider-neutral ReasoningEffort into a Gemini
// ThinkingConfig. Returns nil when no explicit effort was requested so the model
// uses its native default thinking level.
//
// On Gemini 4 (and custom Gemini 3.x) models, ThinkingLevelMinimal is rejected
// with HTTP 400 except on Flash-Lite variants, so effort="none" maps to
// ThinkingLevelLow on Gemini 4. "xhigh" is supported only on gemini-4-hydrogen
// and caps at ThinkingLevelHigh on Argon / Carbon.
func thinkingConfigForModel(modelID, effort string) *genai.ThinkingConfig {
	e := strings.ToLower(strings.TrimSpace(effort))
	if e == "" {
		return nil
	}
	m := strings.ToLower(strings.TrimSpace(modelID))
	if strings.Contains(m, "gemini-2.5") {
		if e == "none" {
			return &genai.ThinkingConfig{
				ThinkingBudget: genai.Ptr[int32](0),
			}
		}
		return nil
	}
	if isGeminiThinkingLevelModel(m) {
		switch e {
		case "none":
			if strings.Contains(m, "flash-lite") {
				return &genai.ThinkingConfig{ThinkingLevel: genai.ThinkingLevelMinimal}
			}
			return &genai.ThinkingConfig{ThinkingLevel: genai.ThinkingLevelLow}
		case "low":
			return &genai.ThinkingConfig{ThinkingLevel: genai.ThinkingLevelLow}
		case "medium":
			return &genai.ThinkingConfig{ThinkingLevel: genai.ThinkingLevelMedium}
		case "high":
			return &genai.ThinkingConfig{ThinkingLevel: genai.ThinkingLevelHigh}
		case "xhigh":
			if strings.Contains(m, "hydrogen") {
				return &genai.ThinkingConfig{ThinkingLevel: geminiThinkingLevelXHigh}
			}
			return &genai.ThinkingConfig{ThinkingLevel: genai.ThinkingLevelHigh}
		}
	}
	return nil
}

// Available checks whether the provider can be reached.
func (p *GeminiProvider) Available(ctx context.Context) bool {
	ok, _ := p.AvailableDetail(ctx)
	return ok
}

// AvailableDetail checks whether the provider is available and reports detailed classification.
func (p *GeminiProvider) AvailableDetail(ctx context.Context) (bool, TestErrorCode) {
	client, err := p.ensureClient(ctx)
	if err != nil {
		return false, ClassifyProbeError("gemini", err)
	}
	if v, code, hit := p.avail.getWithCode(); hit {
		return v, code
	}

	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	modelToCheck := p.genModel
	if modelToCheck == "" {
		modelToCheck = p.embedModel
	}
	_, err = client.Models.Get(probeCtx, modelToCheck, nil)
	return availabilityFromProbe("gemini", &p.avail, err == nil, err)
}

// ListModels returns the verified Gemini models from the builtin catalog.
func (p *GeminiProvider) ListModels(_ context.Context) ([]ModelInfo, error) {
	var models []ModelInfo
	for _, m := range BuiltinCatalog() {
		if m.Provider == "gemini" {
			models = append(models, m)
		}
	}
	return models, nil
}

func asGenaiAPIError(err error) (*genai.APIError, bool) {
	var ptr *genai.APIError
	if errors.As(err, &ptr) && ptr != nil {
		return ptr, true
	}
	var val genai.APIError
	if errors.As(err, &val) {
		return &val, true
	}
	return nil, false
}

// isGeminiRateLimited determines if an error represents an HTTP 429 / RESOURCE_EXHAUSTED.
func isGeminiRateLimited(err error) bool {
	if err == nil {
		return false
	}
	if apiErr, ok := asGenaiAPIError(err); ok {
		if apiErr.Code == 429 || strings.EqualFold(apiErr.Status, "RESOURCE_EXHAUSTED") {
			return true
		}
	}
	if s, ok := status.FromError(err); ok {
		if s.Code() == codes.ResourceExhausted {
			return true
		}
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "429") ||
		strings.Contains(msg, "resource_exhausted") ||
		strings.Contains(msg, "quota exceeded")
}

// isGeminiRetryable reports whether err is a rate limit or transient Google
// server fault (500 INTERNAL / 503 UNAVAILABLE) safe to retry.
func isGeminiRetryable(err error) bool {
	if err == nil {
		return false
	}
	if isGeminiRateLimited(err) {
		return true
	}
	if apiErr, ok := asGenaiAPIError(err); ok {
		if apiErr.Code == 500 || apiErr.Code == 503 ||
			strings.EqualFold(apiErr.Status, "INTERNAL") ||
			strings.EqualFold(apiErr.Status, "UNAVAILABLE") {
			return true
		}
	}
	if s, ok := status.FromError(err); ok {
		if s.Code() == codes.Internal || s.Code() == codes.Unavailable {
			return true
		}
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "status: unavailable") ||
		strings.Contains(msg, "status: internal") ||
		strings.Contains(msg, "error 503") ||
		strings.Contains(msg, "error 500")
}

// parseGeminiRetryAfter extracts a server-advised retry delay from either the
// structured Google RPC RetryInfo detail ("retryDelay": "12s") or the
// human-readable "Please retry in 12.405s." suffix in err.Error().
func parseGeminiRetryAfter(err error) time.Duration {
	if err == nil {
		return 0
	}
	var best time.Duration
	if apiErr, ok := asGenaiAPIError(err); ok {
		for _, detail := range apiErr.Details {
			raw, hasDelay := detail["retryDelay"]
			if !hasDelay {
				continue
			}
			if s, ok := raw.(string); ok {
				if d, perr := time.ParseDuration(strings.TrimSpace(s)); perr == nil && d > best {
					best = d
				}
			}
		}
	}
	msg := err.Error()
	lower := strings.ToLower(msg)
	const marker = "please retry in "
	if idx := strings.Index(lower, marker); idx >= 0 {
		rest := strings.TrimSpace(msg[idx+len(marker):])
		if fields := strings.Fields(rest); len(fields) > 0 {
			tok := strings.TrimRight(fields[0], ".,;:")
			if d, perr := time.ParseDuration(tok); perr == nil && d > best {
				best = d
			}
		}
	}
	return best
}

// geminiRetryDelay returns the backoff wait before retrying 0-based attempt+1,
// honoring any Google RetryInfo / "Please retry in" hint plus small jitter,
// clamped at geminiMaxRetryDelay so attempts stay bounded by GeminiWorstCase.
func geminiRetryDelay(attempt int, err error) time.Duration {
	wait := retryBackoff(attempt)
	if advised := parseGeminiRetryAfter(err); advised > 0 {
		advised += 250 * time.Millisecond
		if advised > wait {
			wait = advised
		}
	}
	jitter := time.Duration(rand.Int64N(int64(250 * time.Millisecond)))
	wait += jitter
	if wait > geminiMaxRetryDelay {
		wait = geminiMaxRetryDelay
	}
	return wait
}

// InitGemini creates and registers Gemini providers with the given registry.
func InitGemini(ctx context.Context, reg *Registry, cfg GeminiConfig, aiCfg AIConfig) error {
	embedModel, embedDims := DefaultGeminiEmbeddingModel, DefaultGeminiDimensions
	if ep := aiCfg.EffectiveEmbeddingProvider(); ep == "" || ep == "gemini" {
		if aiCfg.EmbeddingModel != "" {
			embedModel = aiCfg.EmbeddingModel
		}
		if aiCfg.Dimensions > 0 {
			embedDims = aiCfg.Dimensions
		}
	}
	genModel := DefaultGeminiGenerationModel
	if gp := aiCfg.EffectiveGenerationProvider(); gp == "" || gp == "gemini" {
		if aiCfg.GenerationModel != "" {
			genModel = aiCfg.GenerationModel
		}
	}

	provider, err := NewGeminiProvider(ctx, cfg, embedModel, genModel, embedDims)
	if err != nil {
		// Register a lazy provider (matching InitBedrock) so missing ambient
		// credentials do not emit stderr warnings on non-AI JSON commands;
		// readiness probes and invocations classify the credential failure
		// cleanly via AvailableDetail and ensureClient.
		provider = NewGeminiProviderWithClient(nil, cfg, embedModel, genModel, embedDims)
		provider.lazyInit = true
	}
	reg.RegisterEmbedder("gemini", provider)
	reg.RegisterGenerator("gemini", provider)
	return nil
}
