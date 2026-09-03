package ai

import "context"

// KnownProviders is the canonical list of AI providers 2nb supports.
// Used by shell completion, wizard defaults, and test assertions — when
// adding a new provider, append it here so every site stays in sync.
var KnownProviders = []string{"bedrock", "openrouter", "ollama", "llama-local"}

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
	// PurposeQueryText is the query side for a TEXT-ONLY store: it maps to Nova's
	// TEXT_RETRIEVAL, documented as optimal for "a repository containing only text
	// embeddings", whereas PurposeQuery maps to GENERIC_RETRIEVAL (for a
	// mixed-modality store). 2nb embeds only text, so the text-specific purpose is
	// the closer match; the eval package measures the difference before any switch.
	PurposeQueryText = "query_text"
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

// RerankHit is one reranked candidate: the index into the input document slice
// and the model's relevance score (Bedrock Cohere Rerank normalizes to 0..1),
// best-first.
type RerankHit struct {
	Index int
	Score float64
}

// RerankProvider reorders a candidate document set by relevance to a query with
// a cross-encoder (query and doc scored jointly, so it catches negation and
// paraphrase a bi-encoder's separate-vector cosine can't). Unlike
// EmbeddingProvider it consumes raw TEXT, not vectors, so it is fully decoupled
// from the embedder. topN bounds how many hits come back (0 = all).
type RerankProvider interface {
	Name() string
	Rerank(ctx context.Context, query string, docs []string, topN int) ([]RerankHit, error)
	Available(ctx context.Context) bool
}

// GenUsage is the token usage of one generation, as reported by the provider.
type GenUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// UsageGenerator is an optional extension a GenerationProvider may implement to
// report a generation's real token usage (e.g. Bedrock Converse returns it).
// `ask`/RAG records it for the observatory; providers that don't implement it
// fall back to a chars/4 estimate, so this is additive — not all providers need
// it.
type UsageGenerator interface {
	GenerateWithUsage(ctx context.Context, prompt string, opts GenOpts) (string, GenUsage, error)
}

// AvailabilityReporter is an OPTIONAL interface a provider may implement
// alongside Available: it answers the same question and additionally reports
// WHY an unavailable provider is unavailable, as a TestErrorCode.
//
// It exists as an optional interface rather than a wider Available signature
// because Available appears in three provider interfaces with a dozen
// implementations and roughly fifty call sites, nearly all of which only want
// the bool. A caller that needs the reason type-asserts for this and falls back
// to the plain bool when a provider does not implement it.
//
// The reason matters where 2nb reports a readiness failure to a user: a
// timeout and a rejected credential are the same false, and telling someone to
// "check credentials" when their network blipped sends them to fix the wrong
// thing.
type AvailabilityReporter interface {
	AvailableDetail(ctx context.Context) (bool, TestErrorCode)
}

// Availability asks a provider whether it is ready, and why not when it can
// say. It is the one place the optional-interface fallback lives, so callers
// ask ONCE and carry the answer, instead of testing Available() and then asking
// again to find out what went wrong. The second answer can disagree with the
// first, and when the cached verdict has lapsed (which a transient failure does
// quickly, by design) it costs another live round trip.
//
// The code is "" for a ready provider and for one that cannot explain itself.
func Availability(ctx context.Context, p any) (bool, TestErrorCode) {
	if r, ok := p.(AvailabilityReporter); ok {
		return r.AvailableDetail(ctx)
	}
	if a, ok := p.(interface {
		Available(context.Context) bool
	}); ok {
		return a.Available(ctx), ""
	}
	return false, ""
}

// Ptr returns a pointer to v. Use for optional GenOpts fields like Temperature.
func Ptr[T any](v T) *T { return &v }

// GenOpts configures text generation.
type GenOpts struct {
	Temperature  *float64 // nil = omit (model uses its default); non-nil = send this value
	MaxTokens    int
	SystemPrompt string
	// ReasoningEffort tunes reasoning-model effort ("none" | "low" | "medium"
	// | "high"). Only the bedrock-mantle client reads it today; other
	// providers ignore it. A smoke probe sets "none" so a short answer is not
	// starved when reasoning-on-by-default would consume the token budget.
	ReasoningEffort string
}

// GenOption tunes the GenOpts a pipeline helper builds internally (RAG,
// CondenseQuestion), so a caller can pass through a user setting without the
// helper growing a parameter per knob or exposing its measured prompt/token
// choices to override.
type GenOption func(*GenOpts)

// WithReasoningEffort sets the reasoning depth of a pipeline generation. An
// empty effort is a no-op, leaving the reasoning field unset so the model's
// default applies — so callers pass AIConfig.ResolveReasoningEffort()
// unconditionally without special-casing "unset".
func WithReasoningEffort(effort string) GenOption {
	return func(o *GenOpts) {
		if effort != "" {
			o.ReasoningEffort = effort
		}
	}
}

// applyGenOptions applies opts to a base GenOpts and returns the result.
func applyGenOptions(base GenOpts, opts []GenOption) GenOpts {
	for _, o := range opts {
		if o != nil {
			o(&base)
		}
	}
	return base
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
	Type     string `json:"type" yaml:"type"` // "embedding", "generation", or "rerank"
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
	// Recommended marks a curated pick: the short list of models 2nb suggests
	// per provider. Orthogonal to Tier (harness trust): a builtin entry can be
	// verified but not recommended (a superseded version), and a recommended
	// frontier entry may still be blocked for a given account (AWS staged
	// rollout), so a test probe is still the invokability check. UIs show
	// recommended models by default and put the long tail behind a toggle.
	Recommended bool `json:"recommended,omitempty" yaml:"recommended,omitempty"`
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
	// ThresholdSource records who authored RecommendedSimilarityThreshold on a
	// user-catalog row. The only value ever written is ThresholdSourceUser, and
	// only `models calibrate --save` and `models add --similarity-threshold`
	// write it: every other save path seeds its row from the MERGED catalog, so
	// without this stamp a copied builtin recommendation was indistinguishable
	// from a measurement the user actually took. Empty on a builtin row, and on
	// a user row written before the stamp existed, whose value is ignored (see
	// IsUserThreshold).
	ThresholdSource string `json:"threshold_source,omitempty" yaml:"threshold_source,omitempty"`
	// FactSource records who authored this row's MODEL FACTS: Name, Dimensions
	// and ContextLen. The only value ever written is FactSourceUser, and only
	// `models add --name/--dimensions/--context-length` writes it. Every other
	// save path seeds its row from the MERGED catalog, so without this stamp a
	// copied builtin fact was indistinguishable from a fact the user typed, and
	// the copy then shadowed every later builtin correction. Empty on a builtin
	// row and on any row a probe wrote.
	FactSource string `json:"fact_source,omitempty" yaml:"fact_source,omitempty"`
	// TestedAt is an ISO-8601 timestamp recorded when the model last passed
	// `2nb models test`. Present only on user-catalog entries.
	TestedAt string `json:"tested_at,omitempty" yaml:"tested_at,omitempty"`

	// InvokeStrategy names the API dialect used to call this model (see the
	// Strategy* constants). Empty means "use the provider's default path",
	// which preserves back-compat with catalog entries written before this
	// field existed.
	InvokeStrategy string `json:"invoke_strategy,omitempty" yaml:"invoke_strategy,omitempty"`

	// Plane names the Bedrock invocation plane this row rides: PlaneClassic
	// or PlaneMantle. Together with Provider, ID, and Region it forms the
	// row's IDENTITY (see RouteKey in route.go), not a preference: the same
	// model id can be served on both planes with different entitlement and a
	// different wire dialect. Empty on every non-Bedrock provider.
	Plane Plane `json:"plane,omitempty" yaml:"plane,omitempty"`

	// Region is the AWS region whose endpoint this route calls. Part of the
	// row's IDENTITY: Bedrock entitlement and model availability are both
	// per-region, so the same model in two regions is two routes that can
	// independently succeed or fail. Empty on non-Bedrock rows, and on a
	// legacy row that predates route identity (such a row means "whatever
	// ai.bedrock.region resolves to" and is superseded by the concrete
	// per-region rows the next discovery walk produces).
	Region string `json:"region,omitempty" yaml:"region,omitempty"`

	// Endpoint is a full endpoint URL override for forward-compat with
	// invocation planes whose host isn't derivable from Region alone.
	// Normally empty: clients derive the endpoint from Region.
	Endpoint string `json:"endpoint,omitempty" yaml:"endpoint,omitempty"`

	// TestLatencyMs is the latency of the last passing test probe, in
	// milliseconds. Paired with TestedAt; 0 when no test has succeeded.
	TestLatencyMs int64 `json:"test_latency_ms,omitempty" yaml:"test_latency_ms,omitempty"`

	// TestError holds the failure reason from the most recent test attempt.
	// Non-empty iff the last attempt failed; TestedAt then reflects the
	// failure time. Consumers should treat a non-empty value as "this model
	// is NOT known to work right now".
	TestError string `json:"test_error,omitempty" yaml:"test_error,omitempty"`

	// TestErrorCode classifies TestError into the stable TestErrorCode
	// vocabulary (access_denied, bad_credentials, throttled, ...) so UIs can
	// render actionable guidance instead of parsing the raw string. Empty
	// when the last test passed or the model was never tested. Moves as a
	// unit with TestedAt/TestError.
	TestErrorCode string `json:"test_error_code,omitempty" yaml:"test_error_code,omitempty"`

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

	// Working is the "this account can actually use it" flag: a model with a
	// PASSING probe on record that is not disabled and not statically
	// incompatible, plus UNTESTED active embedding/generation models so a
	// picker is never empty on a freshly bound vault. A failed probe
	// (TestError set) is never working, even when the model is active —
	// "current" and "working" are different; the GUI keeps actives
	// selectable separately. A builtin tier=verified entry alone is NOT
	// enough — verified means 2nb has a harness, not that this account is
	// entitled (AWS's staged frontier rollout lists models the account
	// still 403s on). Derived at list time like Vendor/Compatible, so it
	// is never persisted to models.yaml. Always serialized (true and
	// false), matching Compatible: omitempty would make working==nil
	// mean both "old CLI" and "not working".
	Working bool `json:"working" yaml:"-"`
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

// DefaultGenOpts returns sensible defaults for generation. MaxTokens leaves
// reasoning headroom for classic-plane always-reasoning models (which bill
// reasoning against the budget with no off switch): a budget bounds runaway
// cost, it must never fail a working model.
func DefaultGenOpts() GenOpts {
	return GenOpts{
		Temperature: Ptr(0.1),
		MaxTokens:   1024,
	}
}

// ThresholdSourceUser is the only value ever written to
// ModelInfo.ThresholdSource. It marks a recommended_similarity_threshold the
// user authored: `models calibrate --save` measured it, or `models add
// --similarity-threshold` was told it. Distinct from ThresholdSourceUserCalibration,
// which is the label ResolveSimilarityThresholdFull reports to a human.
const ThresholdSourceUser = "user"

// FactSourceUser is the only value ever written to ModelInfo.FactSource. It
// marks a row whose Name, Dimensions and ContextLen the user typed, via
// `models add --name/--dimensions/--context-length`.
const FactSourceUser = "user"

// IsUserThreshold reports whether a RAW user-catalog row's
// recommended_similarity_threshold is the user's own calibration rather than a
// copy of the builtin recommendation.
//
// A stamp is a stamp: stamped ThresholdSourceUser is the user's, anything
// unstamped is builtin-owned and ignored, so the resolver falls through to the
// builtin recommendation and every already-contaminated catalog self-heals with
// no migration.
//
// The comparison heuristic this replaces ("unstamped and different from the
// builtin is a pre-stamp calibration") misfired on a real vault: the row
// carried 0.65, which is the builtin value 2nb itself recommended for Nova
// until June 2026, and because 0.65 no longer equals the current 0.25 it read
// as a calibration 2.6x the recommendation. That threshold rejects every real
// match on an asymmetric embedding, so search silently degraded to BM25 with
// `ai status` reporting a measurement nobody took. A value the user really did
// calibrate before the stamp existed is dropped in favor of the builtin
// recommendation, which is the safe default, and `ai status` names the file and
// value it ignored so the user can re-save it.
func IsUserThreshold(m ModelInfo) bool {
	return m.RecommendedSimilarityThreshold > 0 && m.ThresholdSource == ThresholdSourceUser
}
