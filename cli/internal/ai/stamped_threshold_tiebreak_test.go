package ai

import "testing"

// Two stamped calibrations for one model is a real state: `models calibrate
// --save` writes per ROUTE, so a user who calibrated the same model on two
// endpoints has two stamped rows and exactly one of them decides what the merged
// view shows. The rule is betterFactDonor's: the richer row, and on a tie the
// LOWER route key. Untested, that ordering could flip on a refactor and the
// displayed threshold would change with nothing to catch it.

func stampedRow(id string, plane Plane, region string, threshold float64, name string, ctxLen int) ModelInfo {
	return ModelInfo{
		ID: id, Provider: "bedrock", Type: "embedding",
		Plane: plane, Region: region,
		RecommendedSimilarityThreshold: threshold,
		ThresholdSource:                ThresholdSourceUser,
		Name:                           name,
		ContextLen:                     ctxLen,
	}
}

// TestStampedThresholdTieBreakPrefersTheRicherRow: the primary rule, asserted in
// BOTH input orders so the result cannot depend on which row was seen first.
func TestStampedThresholdTieBreakPrefersTheRicherRow(t *testing.T) {
	const id = "amazon.nova-2-multimodal-embeddings-v1:0"
	// The us-west-2 row sorts LATER by route key but carries more facts, so the
	// richness rule must beat the ordering rule.
	rich := stampedRow(id, PlaneClassic, "us-west-2", 0.42, "Rich Row", 8192)
	sparse := stampedRow(id, PlaneClassic, "us-east-1", 0.31, "", 0)

	for _, tc := range []struct {
		name string
		rows []ModelInfo
	}{
		{"rich first", []ModelInfo{rich, sparse}},
		{"sparse first", []ModelInfo{sparse, rich}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := stampedThresholdByModel(tc.rows)
			row, ok := got[catalogKey("bedrock", id)]
			if !ok {
				t.Fatalf("no stamped row resolved for %s", id)
			}
			if row.RecommendedSimilarityThreshold != 0.42 {
				t.Errorf("threshold = %v, want the richer row's 0.42 whatever the input order",
					row.RecommendedSimilarityThreshold)
			}
		})
	}
}

// TestStampedThresholdTieBreakFallsBackToTheLowerRouteKey: equally rich rows are
// decided by route key, ascending, so map iteration order cannot change the
// merged view between runs.
func TestStampedThresholdTieBreakFallsBackToTheLowerRouteKey(t *testing.T) {
	const id = "amazon.nova-2-multimodal-embeddings-v1:0"
	east := stampedRow(id, PlaneClassic, "us-east-1", 0.31, "Same", 8192)
	west := stampedRow(id, PlaneClassic, "us-west-2", 0.42, "Same", 8192)
	if modelFactScore(east) != modelFactScore(west) {
		t.Fatalf("the two rows are no longer equally rich (%d vs %d); this test cannot exercise the tie-break",
			modelFactScore(east), modelFactScore(west))
	}
	if routeKey(east.Route()) >= routeKey(west.Route()) {
		t.Fatalf("us-east-1 no longer sorts before us-west-2 (%q vs %q)",
			routeKey(east.Route()), routeKey(west.Route()))
	}

	for _, tc := range []struct {
		name string
		rows []ModelInfo
	}{
		{"east first", []ModelInfo{east, west}},
		{"west first", []ModelInfo{west, east}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			row := stampedThresholdByModel(tc.rows)[catalogKey("bedrock", id)]
			if row.RecommendedSimilarityThreshold != 0.31 {
				t.Errorf("threshold = %v, want us-east-1's 0.31 (the lower route key) whatever the input order",
					row.RecommendedSimilarityThreshold)
			}
		})
	}
}

// TestStampedThresholdIgnoresUnstampedRows: the predicate is stamp-only, so an
// unstamped row never wins a tie-break it should not be in.
func TestStampedThresholdIgnoresUnstampedRows(t *testing.T) {
	const id = "amazon.nova-2-multimodal-embeddings-v1:0"
	unstamped := stampedRow(id, PlaneClassic, "us-east-1", 0.65, "Rich", 8192)
	unstamped.ThresholdSource = ""
	stamped := stampedRow(id, PlaneClassic, "us-west-2", 0.31, "", 0)

	got := stampedThresholdByModel([]ModelInfo{unstamped, stamped})
	row, ok := got[catalogKey("bedrock", id)]
	if !ok {
		t.Fatal("the stamped row was dropped")
	}
	if row.RecommendedSimilarityThreshold != 0.31 {
		t.Errorf("threshold = %v, want the STAMPED 0.31; an unstamped row is nobody's calibration however rich it is",
			row.RecommendedSimilarityThreshold)
	}
}
