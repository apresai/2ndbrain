package ai

import (
	"math"
	"testing"
)

func approxEq(a, b float64) bool {
	return math.Abs(a-b) < 1e-9
}

func TestEstimateCost_EmbeddingKnownPrice(t *testing.T) {
	m := ModelInfo{
		ID: "m", Provider: "bedrock", Type: "embedding",
		PriceIn: 0.135, PriceSource: "builtin",
	}
	est := EstimateCost(m, ProbeBenchEmbed)
	// 10 tokens × $0.135 / 1_000_000 = 1.35e-6
	want := 10.0 * 0.135 / 1_000_000
	if !approxEq(est.USD, want) {
		t.Errorf("USD = %v, want %v", est.USD, want)
	}
	if !est.KnownPricing {
		t.Error("KnownPricing should be true for priced entry")
	}
}

func TestEstimateCost_LocalIsKnownFree(t *testing.T) {
	m := ModelInfo{
		ID: "nomic-embed-text", Provider: "ollama", Type: "embedding",
		Local: true,
	}
	est := EstimateCost(m, ProbeBenchEmbed)
	if est.USD != 0 {
		t.Errorf("USD for local = %v, want 0", est.USD)
	}
	if !est.KnownPricing {
		t.Error("Local model should report KnownPricing=true (known free)")
	}
}

func TestEstimateCost_UnknownPricing(t *testing.T) {
	// No Local flag, all prices zero, no PriceSource → unknown.
	m := ModelInfo{ID: "mystery", Provider: "openrouter", Type: "generation"}
	est := EstimateCost(m, ProbeTest)
	if est.USD != 0 {
		t.Errorf("USD = %v, want 0", est.USD)
	}
	if est.KnownPricing {
		t.Error("KnownPricing should be false when no price data is present")
	}
}

func TestEstimateCost_ExplicitZeroPriceIsKnownFree(t *testing.T) {
	// Free-tier OpenRouter entry: zero prices + PriceSource set ⇒ known-free.
	m := ModelInfo{
		ID: "google/gemma-3-4b-it:free", Provider: "openrouter", Type: "generation",
		PriceSource: "builtin",
	}
	est := EstimateCost(m, ProbeTest)
	if est.USD != 0 {
		t.Errorf("USD = %v, want 0", est.USD)
	}
	if !est.KnownPricing {
		t.Error("Explicit zero price with source should be KnownPricing=true")
	}
}

func TestEstimateCost_GenerationSplitsInOutPricing(t *testing.T) {
	m := ModelInfo{
		ID: "gen", Provider: "bedrock", Type: "generation",
		PriceIn: 3.0, PriceOut: 15.0, PriceSource: "builtin",
	}
	est := EstimateCost(m, ProbeBenchGen)
	// Bench gen: 20 in, 128 out
	want := 20.0*3.0/1_000_000 + 128.0*15.0/1_000_000
	if !approxEq(est.USD, want) {
		t.Errorf("USD = %v, want %v", est.USD, want)
	}
}

func TestEstimateCost_PerRequestPricing(t *testing.T) {
	m := ModelInfo{
		ID: "marengo", Provider: "bedrock", Type: "embedding",
		PriceRequest: 0.0077, PriceSource: "user",
	}
	est := EstimateCost(m, ProbeBenchEmbed)
	// 1 request × $0.0077 = 0.0077 (no token price)
	if !approxEq(est.USD, 0.0077) {
		t.Errorf("USD = %v, want 0.0077", est.USD)
	}
}

func TestEstimateCosts_SkipsMismatchedType(t *testing.T) {
	models := []ModelInfo{
		{ID: "embed", Provider: "bedrock", Type: "embedding", PriceIn: 0.1, PriceSource: "builtin"},
		{ID: "gen", Provider: "bedrock", Type: "generation", PriceIn: 3, PriceOut: 15, PriceSource: "builtin"},
	}
	// bench_gen only applies to generation models
	ests, total := EstimateCosts(models, ProbeBenchGen)
	if len(ests) != 1 || ests[0].ModelID != "gen" {
		t.Errorf("expected only gen model, got %+v", ests)
	}
	if total <= 0 {
		t.Errorf("total should be >0, got %v", total)
	}
}

func TestEstimateCosts_SumsCorrectly(t *testing.T) {
	models := []ModelInfo{
		{ID: "a", Provider: "bedrock", Type: "embedding", PriceIn: 0.1, PriceSource: "builtin"},
		{ID: "b", Provider: "bedrock", Type: "embedding", PriceIn: 0.2, PriceSource: "builtin"},
	}
	ests, total := EstimateCosts(models, ProbeBenchEmbed)
	if len(ests) != 2 {
		t.Fatalf("got %d ests, want 2", len(ests))
	}
	if !approxEq(total, ests[0].USD+ests[1].USD) {
		t.Errorf("total %v != sum %v", total, ests[0].USD+ests[1].USD)
	}
}

func TestDefaultProbeSpec_UnknownKindIsZero(t *testing.T) {
	spec := DefaultProbeSpec("bogus")
	if spec.InputTokens != 0 || spec.Requests != 0 || spec.AppliesToEmbedding || spec.AppliesToGeneration {
		t.Errorf("unknown kind should yield zero-valued spec, got %+v", spec)
	}
}

// TestProbeAppliesToModel_Rerank: a rerank model has no dedicated probe, so it
// is excluded from every probe's cost preview (not treated as generation).
func TestProbeAppliesToModel_Rerank(t *testing.T) {
	rr := ModelInfo{ID: "cohere.rerank-v3-5:0", Type: "rerank"}
	for _, p := range []ProbeKind{ProbeBenchGen, ProbeBenchEmbed, ProbeBenchRAG, ProbeTest} {
		if probeAppliesToModel(rr, DefaultProbeSpec(p)) {
			t.Errorf("rerank model should not apply to probe %q", p)
		}
	}
}
