package ai

import "context"

// KnownProviders is the canonical list of AI providers 2nb supports.
// Used by shell completion, wizard defaults, and test assertions — when
// adding a new provider, append it here so every site stays in sync.
var KnownProviders = []string{"bedrock", "openrouter", "ollama"}

// IsKnownProvider reports whether name is one of the providers 2nb supports.
func IsKnownProvider(name string) bool {
	for _, p := range KnownProviders {
		if p == name {
			return true
		}
	}
	return false
}

// Embedding purposes. Models like Amazon Nova-2 are asymmetric: a stored
// document and a search query are embedded differently. PurposeIndex is the
// default (stored content); PurposeQuery is for the query side of retrieval.
// Providers that don't distinguish (Ollama, OpenRouter) ignore the purpose.
const (
	PurposeIndex = "index"
	PurposeQuery = "query"
)

// EmbedConfig is the resolved set of per-request embedding options.
// Zero value = stored-document defaults (PurposeIndex, model's default dimension).
type EmbedConfig struct {
	Purpose   string // PurposeIndex (default) or PurposeQuery
	Dimension int    // 0 = the model/config default
}

// EmbedOption tunes a single Embed call (functional-options pattern so the
// common Embed(ctx, texts) call sites stay unchanged).
type EmbedOption func(*EmbedConfig)

// WithPurpose sets the embedding purpose (PurposeIndex / PurposeQuery).
func WithPurpose(p string) EmbedOption { return func(c *EmbedConfig) { c.Purpose = p } }

// WithDimension overrides the output embedding dimension for this call
// (Matryoshka models like Nova-2 support 256/384/1024/3072). 0 = default.
func WithDimension(d int) EmbedOption { return func(c *EmbedConfig) { c.Dimension = d } }

// ResolveEmbedOptions folds options into an EmbedConfig with PurposeIndex default.
func ResolveEmbedOptions(opts ...EmbedOption) EmbedConfig {
	cfg := EmbedConfig{Purpose: PurposeIndex}
	for _, opt := range opts {
		opt(&cfg)
	}
	return cfg
}

// EmbeddingProvider generates vector embeddings from text.
type EmbeddingProvider interface {
	Name() string
	Embed(ctx context.Context, texts []string, opts ...EmbedOption) ([][]float32, error)
	Dimensions() int
	Available(ctx context.Context) bool
	ListModels(ctx context.Context) ([]ModelInfo, error)
}

// GenerationProvider generates text from prompts.
type GenerationProvider interface {
	Name() string
	Generate(ctx context.Context, prompt string, opts GenOpts) (string, error)
	Available(ctx context.Context) bool
	ListModels(ctx context.Context) ([]ModelInfo, error)
}

// Ptr returns a pointer to v. Use for optional GenOpts fields like Temperature.
func Ptr[T any](v T) *T { return &v }

// GenOpts configures text generation.
type GenOpts struct {
	Temperature  *float64 // nil = omit (model uses its default); non-nil = send this value
	MaxTokens    int
	SystemPrompt string
}

// ModelTier indicates whether a model has a verified API harness in 2nb.
type ModelTier string

const (
	// TierVerified means 2nb has tested this model's invoke format and it works.
	TierVerified ModelTier = "verified"
	// TierUserVerified means the user's own catalog (or a passing `models test --save`)
	// has recorded that this model works for them. Sits between builtin-verified
	// and unverified so users can trust their own runtime additions.
	TierUserVerified ModelTier = "user_verified"
	// TierUnverified means the vendor API lists this model but 2nb hasn't built/tested a harness for it.
	TierUnverified ModelTier = "unverified"
)

// ModelInfo describes an available model. Both json and yaml tags are set:
// JSON is used for CLI --json output and vendor API payloads; YAML is used
// for the user catalog file at ~/.config/2nb/models.yaml.
type ModelInfo struct {
	ID       string `json:"id" yaml:"id"`
	Name     string `json:"name" yaml:"name,omitempty"`
	Provider string `json:"provider" yaml:"provider"`
	Type     string `json:"type" yaml:"type"` // "embedding" or "generation"
	// UI identity fields are derived at catalog-list time from ID+Provider.
	// They are JSON-only so user catalogs keep the canonical minimal schema.
	Vendor         string `json:"vendor,omitempty" yaml:"-"`
	VendorDisplay  string `json:"vendor_display,omitempty" yaml:"-"`
	Family         string `json:"family,omitempty" yaml:"-"`
	VersionSortKey string `json:"version_sort_key,omitempty" yaml:"-"`
	Dimensions     int    `json:"dimensions,omitempty" yaml:"dimensions,omitempty"`
	ContextLen     int    `json:"context_length,omitempty" yaml:"context_length,omitempty"`
	// SupportedDimensions lists every output dimension a Matryoshka embedding
	// model accepts (e.g. Nova-2: 256/384/1024/3072). Empty = only Dimensions.
	SupportedDimensions []int `json:"supported_dimensions,omitempty" yaml:"supported_dimensions,omitempty"`
	// Modalities lists the input modalities an embedding model accepts
	// (e.g. Nova-2: text/image/video/audio). Empty = text only.
	Modalities []string `json:"modalities,omitempty" yaml:"modalities,omitempty"`
	// RecommendedSimilarityThreshold is the suggested minimum cosine similarity
	// for semantic search with this embedding model. Used when the vault's
	// ai.similarity_threshold isn't explicitly set. Different embedding models
	// have different baseline similarity distributions: Nova-2 clusters tight
	// (random-pair cosine ~0.55–0.64), while smaller-dim models spread wider.
	// Only meaningful for Type="embedding". Zero means "no model recommendation,
	// fall back to ai.DefaultSimilarityThreshold".
	RecommendedSimilarityThreshold float64 `json:"recommended_similarity_threshold,omitempty" yaml:"recommended_similarity_threshold,omitempty"`
	PriceIn                        float64 `json:"price_input_per_million" yaml:"price_input_per_million,omitempty"`
	PriceOut                       float64 `json:"price_output_per_million" yaml:"price_output_per_million,omitempty"`
	PriceRequest                   float64 `json:"price_per_request,omitempty" yaml:"price_per_request,omitempty"`
	Local                          bool    `json:"local" yaml:"local,omitempty"`

	// Tier indicates whether 2nb has a verified harness for this model.
	Tier ModelTier `json:"tier,omitempty" yaml:"tier,omitempty"`
	// Active is true when this model is currently configured.
	Active bool `json:"active,omitempty" yaml:"-"`
	// Reachable indicates provider connectivity: nil=unchecked, true/false=probed.
	Reachable *bool `json:"reachable,omitempty" yaml:"-"`
	// CredsOK indicates credential availability: nil=N/A or unchecked.
	CredsOK *bool `json:"credentials,omitempty" yaml:"-"`
	// ConfigHint shows how to switch to this model.
	ConfigHint string `json:"config_hint,omitempty" yaml:"config_hint,omitempty"`
	// RateLimitRPS is the known rate limit in requests per second (0=unknown).
	RateLimitRPS float64 `json:"rate_limit_rps,omitempty" yaml:"rate_limit_rps,omitempty"`
	// RateLimitTPM is the known rate limit in tokens per minute (0=unknown).
	RateLimitTPM int `json:"rate_limit_tpm,omitempty" yaml:"rate_limit_tpm,omitempty"`
	// Notes contains caveats like "different invoke format — not yet supported".
	Notes string `json:"notes,omitempty" yaml:"notes,omitempty"`
	// PriceSource records which layer supplied the pricing: "builtin", "bundled",
	// "user", "vendor". Empty when price fields are zero/unknown.
	PriceSource string `json:"price_source,omitempty" yaml:"price_source,omitempty"`
	// PriceOverride is set when a user explicitly wants their price fields to
	// override builtin or vendor pricing, including explicit zero-cost entries.
	// Absent legacy user-catalog entries with zero prices are treated as
	// unpriced so live vendor pricing can recover automatically.
	PriceOverride bool `json:"price_override,omitempty" yaml:"price_override,omitempty"`
	// TestedAt is an ISO-8601 timestamp recorded when the model last passed
	// `2nb models test`. Present only on user-catalog entries.
	TestedAt string `json:"tested_at,omitempty" yaml:"tested_at,omitempty"`

	// InvokeStrategy names the API dialect used to call this model (see the
	// Strategy* constants). Empty means "use the provider's default path",
	// which preserves back-compat with catalog entries written before this
	// field existed.
	InvokeStrategy string `json:"invoke_strategy,omitempty" yaml:"invoke_strategy,omitempty"`

	// TestLatencyMs is the latency of the last passing test probe, in
	// milliseconds. Paired with TestedAt; 0 when no test has succeeded.
	TestLatencyMs int64 `json:"test_latency_ms,omitempty" yaml:"test_latency_ms,omitempty"`

	// TestError holds the failure reason from the most recent test attempt.
	// Non-empty iff the last attempt failed; TestedAt then reflects the
	// failure time. Consumers should treat a non-empty value as "this model
	// is NOT known to work right now".
	TestError string `json:"test_error,omitempty" yaml:"test_error,omitempty"`

	// Benchmark is the most-recent benchmark summary for this model. Nil
	// when the model has never been benchmarked. The full history still
	// lives in <vault>/.2ndbrain/bench.db; this field lets dropdowns and
	// the wizard render latency / quality without a DB join.
	Benchmark *BenchmarkSummary `json:"benchmark,omitempty" yaml:"benchmark,omitempty"`

	// Enabled controls whether this model appears in selection dropdowns.
	// Nil ("unset") defers to the tier default (verified / user_verified =
	// visible, unverified = hidden). Explicit false hides the model from
	// dropdowns but keeps it in `2nb models list` for power users.
	Enabled *bool `json:"enabled,omitempty" yaml:"enabled,omitempty"`

	// Compatible reports whether this binary knows how to invoke the model's
	// declared provider/type path. CompatibilityReason explains false values.
	Compatible          bool   `json:"compatible" yaml:"-"`
	CompatibilityReason string `json:"compatibility_reason,omitempty" yaml:"-"`
}

// BenchmarkSummary is the most-recent benchmark snapshot for a model,
// stored inline in the catalog for fast dropdown / wizard rendering.
type BenchmarkSummary struct {
	// RanAt is an ISO-8601 timestamp of the benchmark run.
	RanAt string `json:"ran_at,omitempty" yaml:"ran_at,omitempty"`
	// AvgLatencyMs is the mean latency across probes in this run.
	AvgLatencyMs int64 `json:"avg_latency_ms,omitempty" yaml:"avg_latency_ms,omitempty"`
	// QualityScore is a 0..1 retrieval-quality score from the wikilink
	// ground-truth probe (embedding models only). 0 when the probe wasn't
	// run or the vault had too few links to compute a score.
	QualityScore float64 `json:"quality_score,omitempty" yaml:"quality_score,omitempty"`
	// VaultDocCount records how many documents the run was calibrated
	// against, so comparisons across runs are apples-to-apples.
	VaultDocCount int `json:"vault_doc_count,omitempty" yaml:"vault_doc_count,omitempty"`
}

// DefaultGenOpts returns sensible defaults for generation.
func DefaultGenOpts() GenOpts {
	return GenOpts{
		Temperature: Ptr(0.1),
		MaxTokens:   512,
	}
}
