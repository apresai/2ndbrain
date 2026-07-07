package ai

import "sort"

// SortModelsBest orders models for the opt-in `models list --sort best`
// presentation: types stay grouped (embedding, then generation, then rerank),
// and within a type the ranking prefers measured evidence over static trust:
// benchmark quality (retrieval ground truth, embedding models only), then a
// passing user test, then curation, then tier, then benchmark latency, then
// ID for stability. Bench data influences ranking exactly where it exists and
// degrades to tier+tested where it doesn't; it never changes default-model
// SELECTION (that stays with the recommended flag and the wizard).
func SortModelsBest(models []ModelInfo) {
	sort.SliceStable(models, func(i, j int) bool {
		a, b := models[i], models[j]
		if ta, tb := typeRank(a.Type), typeRank(b.Type); ta != tb {
			return ta < tb
		}
		aq, bq := benchQuality(a), benchQuality(b)
		if (aq > 0) != (bq > 0) {
			return aq > 0
		}
		if aq != bq {
			return aq > bq
		}
		at, bt := testedPassing(a), testedPassing(b)
		if at != bt {
			return at
		}
		if a.Recommended != b.Recommended {
			return a.Recommended
		}
		if ra, rb := tierRankBest(a.Tier), tierRankBest(b.Tier); ra != rb {
			return ra < rb
		}
		al, bl := benchLatency(a), benchLatency(b)
		if al != bl {
			return al < bl
		}
		return a.ID < b.ID
	})
}

func typeRank(t string) int {
	switch t {
	case "embedding":
		return 0
	case "generation":
		return 1
	default:
		return 2
	}
}

func benchQuality(m ModelInfo) float64 {
	if m.Benchmark == nil {
		return 0
	}
	return m.Benchmark.QualityScore
}

// benchLatency returns the benchmark average latency, falling back to the
// last test-probe latency, with "no data" sorting last.
func benchLatency(m ModelInfo) int64 {
	if m.Benchmark != nil && m.Benchmark.AvgLatencyMs > 0 {
		return m.Benchmark.AvgLatencyMs
	}
	if m.TestLatencyMs > 0 {
		return m.TestLatencyMs
	}
	return 1 << 62
}

func testedPassing(m ModelInfo) bool {
	return m.TestedAt != "" && m.TestError == ""
}

func tierRankBest(t ModelTier) int {
	switch t {
	case TierVerified:
		return 0
	case TierUserVerified:
		return 1
	case TierUnverified:
		return 2
	default:
		return 3
	}
}
