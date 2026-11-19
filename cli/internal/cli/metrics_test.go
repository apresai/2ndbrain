package cli

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/apresai/2ndbrain/internal/ai"
	"github.com/apresai/2ndbrain/internal/metrics"
	"github.com/apresai/2ndbrain/internal/testutil"
	"github.com/apresai/2ndbrain/internal/vault"
)

// TestIndexOperation locks the stat→Operation mapping the index instrumentation
// depends on (the part a shadowed/wrong captured var in runIndex would corrupt):
// IndexStats + embeddingRunStats + config flow into the right fields, force flips
// index→reembed, and an error sets OK=false + Error.
func TestIndexOperation(t *testing.T) {
	ix := vault.IndexStats{FilesScanned: 3, DocsIndexed: 2, ChunksCreated: 5, LinksFound: 4}
	es := embeddingRunStats{Embedded: 2, Skipped: 1, Failed: 0, DurationMs: 500, TotalChars: 100, Model: "nova-2"}
	cfg := ai.AIConfig{Dimensions: 1024}
	start := time.Now().Add(-time.Second)

	op := indexOperation(false, start, ix, es, cfg, nil)
	if op.Operation != metrics.OpIndex {
		t.Errorf("operation = %q, want index", op.Operation)
	}
	if op.FilesScanned != 3 || op.DocsIndexed != 2 || op.ChunksCreated != 5 || op.LinksFound != 4 {
		t.Errorf("index counts mismapped: %+v", op)
	}
	if op.Embedded != 2 || op.EmbedSkipped != 1 || op.EmbedMs != 500 || op.TotalChars != 100 || op.EmbeddingModel != "nova-2" {
		t.Errorf("embed stats mismapped: %+v", op)
	}
	if op.EmbeddingDims != 1024 || !op.OK || op.DurationMs <= 0 {
		t.Errorf("dims/ok/duration wrong: dims=%d ok=%v dur=%d", op.EmbeddingDims, op.OK, op.DurationMs)
	}

	// force=true → reembed; an error → OK=false with the message captured.
	op2 := indexOperation(true, start, ix, es, cfg, errors.New("boom"))
	if op2.Operation != metrics.OpReembed {
		t.Errorf("force operation = %q, want reembed", op2.Operation)
	}
	if op2.OK || op2.Error != "boom" {
		t.Errorf("error not captured: ok=%v err=%q", op2.OK, op2.Error)
	}
}

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
	// recent and ai_usage.by_model must marshal as [] not null — a nil Go slice
	// becomes JSON null, which typed Swift decoding cannot accept.
	blob, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("marshal report: %v", err)
	}
	if !strings.Contains(string(blob), `"recent":[]`) {
		t.Errorf("empty-vault JSON must contain \"recent\":[] (not null); got: %s", blob)
	}
	if !strings.Contains(string(blob), `"by_model":[]`) {
		t.Errorf("empty-vault JSON must contain \"by_model\":[] (not null); got: %s", blob)
	}
}

func TestMetricsReport_ReembedEstimateAndHybridCostUsage(t *testing.T) {
	v := testutil.NewTestVault(t)
	v.Config.AI.Provider = "gemini"
	v.Config.AI.EmbeddingProvider = "llama-local"
	v.Config.AI.EmbeddingModel = "embeddinggemma-2"
	v.Config.AI.GenerationProvider = "gemini"
	v.Config.AI.GenerationModel = "gemini-4-hydrogen"
	if err := v.Config.Save(v.DotDir); err != nil {
		t.Fatal(err)
	}
	testutil.CreateAndIndex(t, v, "Note One", "note", "first note body with substantive markdown content")
	testutil.CreateAndIndex(t, v, "Note Two", "note", "second note body with enough text to size chunks")

	// Record a hybrid ask (free local embedding + paid cloud generation) and a local reembed.
	askOp := metrics.Operation{Operation: metrics.OpAsk, DurationMs: 500, OK: true}
	attachUsageLegs(&askOp,
		buildUsageLeg(v.Root, "llama-local", "embeddinggemma-2", "embedding", metrics.OpAsk, 1, 200, 0),
		buildUsageLeg(v.Root, "gemini", "gemini-4-hydrogen", "generation", metrics.OpAsk, 1, 1_000_000, 100_000),
	)
	recordMetric(v, askOp)

	reembedOp := metrics.Operation{Operation: metrics.OpReembed, DurationMs: 500, EmbedMs: 500, Embedded: 5, TotalChars: 5000, OK: true}
	attachUsageLegs(&reembedOp,
		buildUsageLeg(v.Root, "llama-local", "embeddinggemma-2", "embedding", metrics.OpReembed, 5, 1250, 0),
	)
	recordMetric(v, reembedOp)

	report, err := buildMetricsReport(v, 20)
	if err != nil {
		t.Fatalf("buildMetricsReport: %v", err)
	}
	if report.ReembedEstimate == nil || report.ReembedEstimate.EmbeddableDocs != 2 || report.ReembedEstimate.TotalChunks < 2 {
		t.Fatalf("ReembedEstimate mismatch: %+v", report.ReembedEstimate)
	}
	if report.ReembedEstimate.IsCloud || report.ReembedEstimate.EstimatedCostUSD != 0 {
		t.Errorf("local embedding reembed should be free ($0.00, IsCloud=false), got %+v", report.ReembedEstimate)
	}
	if report.ReembedEstimate.SpeedSource != "history" {
		t.Errorf("SpeedSource = %q, want history (from recorded >=3 doc reembed)", report.ReembedEstimate.SpeedSource)
	}
	if report.AIUsage == nil {
		t.Fatal("AIUsage = nil")
	}
	// gemini-4-hydrogen: 1M in @ $1.50 + 100k out @ $7.50/M ($0.75) = $2.25; local embed = $0.
	if report.AIUsage.AllTime.CloudCostUSD < 2.24 || report.AIUsage.AllTime.CloudCostUSD > 2.26 {
		t.Errorf("AllTime.CloudCostUSD = %v, want ~2.25", report.AIUsage.AllTime.CloudCostUSD)
	}
	if report.AIUsage.AllTime.LocalTokens != 1450 || report.AIUsage.AllTime.CloudInputTokens != 1_000_000 {
		t.Errorf("token split wrong: %+v", report.AIUsage.AllTime)
	}
	if len(report.AIUsage.ByModel) != 2 {
		t.Errorf("ByModel len = %d, want 2: %+v", len(report.AIUsage.ByModel), report.AIUsage.ByModel)
	}
}
