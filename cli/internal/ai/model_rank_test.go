package ai

import "testing"

func TestSortModelsBest(t *testing.T) {
	bench := func(q float64, lat int64) *BenchmarkSummary {
		return &BenchmarkSummary{QualityScore: q, AvgLatencyMs: lat}
	}
	models := []ModelInfo{
		{ID: "gen-untested", Type: "generation", Tier: TierUnverified},
		{ID: "embed-benched-low", Type: "embedding", Tier: TierVerified, Benchmark: bench(0.70, 300)},
		{ID: "gen-benched", Type: "generation", Tier: TierVerified, Benchmark: bench(0, 500)},
		{ID: "embed-benched-high", Type: "embedding", Tier: TierVerified, Benchmark: bench(0.95, 400)},
		{ID: "gen-tested", Type: "generation", Tier: TierUserVerified, TestedAt: "2026-07-01T00:00:00Z"},
		{ID: "gen-recommended", Type: "generation", Tier: TierVerified, Recommended: true},
		{ID: "gen-failed", Type: "generation", Tier: TierVerified, TestedAt: "2026-07-01T00:00:00Z", TestError: "403"},
		{ID: "embed-tested", Type: "embedding", Tier: TierVerified, TestedAt: "2026-07-01T00:00:00Z"},
	}
	SortModelsBest(models)

	got := make([]string, len(models))
	for i, m := range models {
		got[i] = m.ID
	}
	want := []string{
		// Embeddings first, quality-benched best-first, then tested.
		"embed-benched-high",
		"embed-benched-low",
		"embed-tested",
		// Generations: tested-passing is stronger evidence than a latency-only
		// benchmark summary (which may include failed probes), then
		// recommended, then tier with latency tiebreak, then unverified.
		"gen-tested",
		"gen-recommended",
		"gen-benched",
		"gen-failed",
		"gen-untested",
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order mismatch at %d: got %v, want %v", i, got, want)
		}
	}
}

func TestSortModelsBest_StableOnTies(t *testing.T) {
	models := []ModelInfo{
		{ID: "a", Type: "generation", Tier: TierVerified},
		{ID: "b", Type: "generation", Tier: TierVerified},
	}
	SortModelsBest(models)
	if models[0].ID != "a" || models[1].ID != "b" {
		t.Fatalf("tie should fall back to ID order: %+v", models)
	}
}
