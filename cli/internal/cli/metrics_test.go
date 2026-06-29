package cli

import (
	"testing"

	"github.com/apresai/2ndbrain/internal/metrics"
	"github.com/apresai/2ndbrain/internal/testutil"
)

// TestMetricsReport_RecordAndBuild exercises the full Phase-1 path with no AI
// provider: record a build + a query via the best-effort recorder, then build
// the report the `2nb metrics --json` command emits and assert the headline,
// derived rates, live gauges (read from the real index), recent list, and
// aggregates all line up.
func TestMetricsReport_RecordAndBuild(t *testing.T) {
	v := testutil.NewTestVault(t)
	testutil.CreateAndIndex(t, v, "Auth Notes", "note", "we use oauth and jwt for authentication")
	testutil.CreateAndIndex(t, v, "Deploy Runbook", "note", "deploy via lambda and cloudfront")

	recordMetric(v, metrics.Operation{
		Operation: metrics.OpIndex, DurationMs: 1000, DocsIndexed: 2, ChunksCreated: 2, OK: true,
	})
	recordMetric(v, metrics.Operation{
		Operation: metrics.OpSearch, DurationMs: 40, ResultCount: 1, Mode: "hybrid", OK: true,
	})

	report, err := buildMetricsReport(v, 20)
	if err != nil {
		t.Fatalf("buildMetricsReport: %v", err)
	}

	// Headline: the recorded index build, with derived docs/sec filled in.
	if report.LastBuild == nil {
		t.Fatal("last_build is nil; expected the recorded index op")
	}
	if report.LastBuild.Operation != metrics.OpIndex || report.LastBuild.DocsIndexed != 2 {
		t.Errorf("last_build = %+v, want index with 2 docs", report.LastBuild)
	}
	if report.LastBuild.DurationMs <= 0 {
		t.Errorf("last_build duration_ms = %d, want > 0", report.LastBuild.DurationMs)
	}
	if report.LastBuild.DocsPerSec != 2 { // 2 docs / 1s
		t.Errorf("last_build docs_per_sec = %v, want 2", report.LastBuild.DocsPerSec)
	}

	// Gauges read the real index — both docs were created and indexed.
	if report.Gauges.DocCount != 2 {
		t.Errorf("gauges.doc_count = %d, want 2", report.Gauges.DocCount)
	}
	if report.Gauges.IndexDBBytes <= 0 {
		t.Errorf("gauges.index_db_bytes = %d, want > 0 (real index.db)", report.Gauges.IndexDBBytes)
	}

	// Recent includes both ops; the search aggregate is present.
	if len(report.Recent) != 2 {
		t.Errorf("recent = %d, want 2", len(report.Recent))
	}
	if _, ok := report.Aggregates[metrics.OpSearch]; !ok {
		t.Errorf("aggregates missing %q", metrics.OpSearch)
	}
	if idx, ok := report.Aggregates[metrics.OpIndex]; !ok || idx.Count != 1 {
		t.Errorf("index aggregate = %+v, want count 1", idx)
	}
}

// TestMetricsReport_EmptyVault: a vault with no recorded ops yields a nil
// last_build (not an error) and zeroed gauges that still read the index.
func TestMetricsReport_EmptyVault(t *testing.T) {
	v := testutil.NewTestVault(t)
	report, err := buildMetricsReport(v, 20)
	if err != nil {
		t.Fatalf("buildMetricsReport on empty vault: %v", err)
	}
	if report.LastBuild != nil {
		t.Errorf("last_build = %+v, want nil for a vault with no recorded builds", report.LastBuild)
	}
	if len(report.Recent) != 0 {
		t.Errorf("recent = %d, want 0", len(report.Recent))
	}
}
