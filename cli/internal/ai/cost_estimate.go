package ai

// ProbeKind categorizes a probe scenario for cost estimation. The token
// counts below are upper bounds baked into our probe implementations so
// `2nb models cost-preview` stays deterministic. They intentionally don't
// depend on the user's vault contents.
type ProbeKind string

const (
	// ProbeTest is a single smoke-test probe — matches TestProbeModel.
	ProbeTest ProbeKind = "test"
	// ProbeBenchGen is a single generation benchmark probe.
	ProbeBenchGen ProbeKind = "bench_gen"
	// ProbeBenchRAG is a single RAG benchmark probe (search + generate
	// against ~5 vault chunks). BM25 search itself doesn't cost tokens.
	ProbeBenchRAG ProbeKind = "bench_rag"
	// ProbeBenchEmbed is a single embedding benchmark probe.
	ProbeBenchEmbed ProbeKind = "bench_embed"
	// ProbeRetrievalQuality is the wikilink-ground-truth embedding probe
	// added in phase 4. Cost depends on the number of unique anchor docs
	// embedded during the probe; callers override InputTokens via
	// ProbeSpec.InputTokens when invoking EstimateCostWithSpec.
	ProbeRetrievalQuality ProbeKind = "retrieval"
)

// ProbeSpec captures the deterministic token / request budget of one
// probe invocation. Values are upper bounds; real probes often finish
// below these totals.
type ProbeSpec struct {
	// InputTokens estimated per probe invocation.
	InputTokens int
	// OutputTokens budget per probe invocation (generation probes only).
	OutputTokens int
	// Requests is the number of API round-trips per probe.
	Requests int
	// AppliesToEmbedding is true when this probe is meaningful for
	// embedding models; generation-only probes set it to false so
	// filtered cost previews skip embedders cleanly.
	AppliesToEmbedding bool
	// AppliesToGeneration is the symmetric flag for generation models.
	AppliesToGeneration bool
}

// DefaultProbeSpec returns the budget for a standard probe kind. The
// ProbeRetrievalQuality kind needs a caller-supplied InputTokens (via
// EstimateCostWithSpec) because its cost scales with the vault's
// unique anchor count; this function returns a zero-cost placeholder
// so naive callers don't crash.
func DefaultProbeSpec(p ProbeKind) ProbeSpec {
	switch p {
	case ProbeTest:
		// TestProbeModel uses a 10-ish-token embed or a 20-ish-token
		// generate with MaxTokens=32. Bucket the upper bound for both.
		return ProbeSpec{InputTokens: 20, OutputTokens: 32, Requests: 1, AppliesToEmbedding: true, AppliesToGeneration: true}
	case ProbeBenchEmbed:
		return ProbeSpec{InputTokens: 10, Requests: 1, AppliesToEmbedding: true}
	case ProbeBenchGen:
		return ProbeSpec{InputTokens: 20, OutputTokens: 128, Requests: 1, AppliesToGeneration: true}
	case ProbeBenchRAG:
		// RAG probe pulls ~5 chunks × ~500 tokens ≈ 2500 tokens of context,
		// plus a short prompt; bound output at 512.
		return ProbeSpec{InputTokens: 2500, OutputTokens: 512, Requests: 1, AppliesToGeneration: true}
	case ProbeRetrievalQuality:
		return ProbeSpec{Requests: 0, AppliesToEmbedding: true}
	}
	return ProbeSpec{}
}

// CostEstimate projects the cost of running a probe against one model.
// Zero KnownPricing means we have no price data for this model; USD is
// 0 but callers should surface "unknown" rather than "free".
type CostEstimate struct {
	ModelID      string    `json:"model_id"`
	Provider     string    `json:"provider"`
	Probe        ProbeKind `json:"probe"`
	Requests     int       `json:"requests"`
	InputTokens  int       `json:"input_tokens"`
	OutputTokens int       `json:"output_tokens"`
	USD          float64   `json:"usd"`
	// KnownPricing is true when the model is either priced explicitly
	// (any non-zero price field) or known to be free (Local=true or an
	// explicit zero-price entry with a PriceSource set). It's false for
	// entries with no pricing metadata at all, so the wizard can nudge
	// the user to check vendor pricing before committing.
	KnownPricing bool `json:"known_pricing"`
}

// EstimateCost projects a probe's cost for a single model using the
// model's current PriceIn / PriceOut / PriceRequest. Price fields are
// "per million tokens" except PriceRequest which is per call.
func EstimateCost(m ModelInfo, probe ProbeKind) CostEstimate {
	return EstimateCostWithSpec(m, probe, DefaultProbeSpec(probe))
}

// EstimateCostWithSpec is the same as EstimateCost but lets the caller
// override the probe's token budget — used by the retrieval-quality
// probe where input tokens scale with the vault.
func EstimateCostWithSpec(m ModelInfo, probe ProbeKind, spec ProbeSpec) CostEstimate {
	usd := float64(spec.InputTokens)*m.PriceIn/1_000_000 +
		float64(spec.OutputTokens)*m.PriceOut/1_000_000 +
		float64(spec.Requests)*m.PriceRequest
	return CostEstimate{
		ModelID:      m.ID,
		Provider:     m.Provider,
		Probe:        probe,
		Requests:     spec.Requests,
		InputTokens:  spec.InputTokens,
		OutputTokens: spec.OutputTokens,
		USD:          usd,
		KnownPricing: HasKnownPricing(m),
	}
}

// EstimateCosts batches EstimateCost across a slice of models, returning
// per-model estimates and the sum. Models whose type doesn't match the
// probe (e.g., embedding model against ProbeBenchGen) are skipped.
func EstimateCosts(models []ModelInfo, probe ProbeKind) ([]CostEstimate, float64) {
	spec := DefaultProbeSpec(probe)
	out := make([]CostEstimate, 0, len(models))
	var total float64
	for _, m := range models {
		if !probeAppliesToModel(m, spec) {
			continue
		}
		est := EstimateCostWithSpec(m, probe, spec)
		out = append(out, est)
		total += est.USD
	}
	return out, total
}

func probeAppliesToModel(m ModelInfo, spec ProbeSpec) bool {
	switch m.Type {
	case "embedding":
		return spec.AppliesToEmbedding
	case "generation":
		return spec.AppliesToGeneration
	case "rerank":
		// Rerank models have no dedicated probe (embed/gen/rag), so they don't
		// belong in any probe's cost preview.
		return false
	}
	// Unknown model type: apply the probe so the user sees the entry
	// rather than silently dropping it.
	return true
}

