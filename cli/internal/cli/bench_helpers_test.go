package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/apresai/2ndbrain/internal/ai"
	benchpkg "github.com/apresai/2ndbrain/internal/bench"
	"github.com/apresai/2ndbrain/internal/testutil"
)

func TestBenchmarkSummaryUsesSuccessfulResultsAndVaultDocCount(t *testing.T) {
	summary := benchmarkSummary("2026-04-25T00:00:00Z", 3, []benchpkg.ProbeResult{
		{Probe: "generate", OK: true, LatencyMs: 100},
		{Probe: "search", OK: false, LatencyMs: 900},
		{Probe: "retrieval", OK: true, LatencyMs: 50, QualityScore: 0.75, VaultDocCount: 12},
	})

	if summary.RanAt != "2026-04-25T00:00:00Z" {
		t.Fatalf("RanAt = %q", summary.RanAt)
	}
	if summary.AvgLatencyMs != 75 {
		t.Fatalf("AvgLatencyMs = %d, want 75", summary.AvgLatencyMs)
	}
	if summary.QualityScore != 0.75 {
		t.Fatalf("QualityScore = %v, want 0.75", summary.QualityScore)
	}
	if summary.VaultDocCount != 12 {
		t.Fatalf("VaultDocCount = %d, want 12", summary.VaultDocCount)
	}
}

func TestResolveBenchSummaryScope(t *testing.T) {
	scope, root, err := resolveBenchSummaryScope("/vault", "global")
	if err != nil {
		t.Fatalf("global scope: %v", err)
	}
	if scope != ai.ScopeGlobal || root != "" {
		t.Fatalf("global scope = (%q, %q), want (%q, empty)", scope, root, ai.ScopeGlobal)
	}

	scope, root, err = resolveBenchSummaryScope("/vault", "vault")
	if err != nil {
		t.Fatalf("vault scope: %v", err)
	}
	if scope != ai.ScopeVault || root != "/vault" {
		t.Fatalf("vault scope = (%q, %q), want (%q, /vault)", scope, root, ai.ScopeVault)
	}

	if _, _, err := resolveBenchSummaryScope("/vault", "bad"); err == nil {
		t.Fatal("bad scope should return an error")
	}
}

func TestBenchVaultDocCountReturnsScanError(t *testing.T) {
	v := testutil.NewTestVault(t)
	if err := v.DB.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}
	if _, err := benchVaultDocCount(v.DB.Conn()); err == nil {
		t.Fatal("benchVaultDocCount on closed DB should return error")
	}
}

func TestRunBenchProbesUnknownProbe(t *testing.T) {
	_, err := runBenchProbes(benchpkg.ProbeOpts{}, "nope", nil)
	if err == nil || !strings.Contains(err.Error(), "unknown probe") {
		t.Fatalf("runBenchProbes unknown probe error = %v", err)
	}
}

func TestRunBenchProbesEmitsJSONEvents(t *testing.T) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	results, err := runBenchProbes(benchpkg.ProbeOpts{}, "search", enc)
	if err != nil {
		t.Fatalf("runBenchProbes: %v", err)
	}
	if len(results) != 1 || results[0].Probe != "search" {
		t.Fatalf("results = %+v, want one search probe", results)
	}
	out := buf.String()
	if !strings.Contains(out, `"event":"probe_start"`) || !strings.Contains(out, `"event":"probe_result"`) {
		t.Fatalf("expected probe_start and probe_result events, got:\n%s", out)
	}
}

func TestOpenBenchDBCreatesDatabase(t *testing.T) {
	db, err := openBenchDB(t.TempDir())
	if err != nil {
		t.Fatalf("openBenchDB: %v", err)
	}
	defer db.Close()
	if err := db.AddFavorite("bedrock", "model", "generation"); err != nil {
		t.Fatalf("bench DB not usable: %v", err)
	}
}

func TestSaveBenchmarkSummaryCreatesCatalogEntry(t *testing.T) {
	setupModelsAddHome(t)
	summary := &ai.BenchmarkSummary{RanAt: "2026-04-25T00:00:00Z", AvgLatencyMs: 123, QualityScore: 0.5, VaultDocCount: 7}
	if err := saveBenchmarkSummary(
		context.Background(),
		ai.DefaultAIConfig(),
		ai.ScopeGlobal,
		"",
		"bedrock",
		"bench.model",
		"generation",
		summary,
	); err != nil {
		t.Fatalf("saveBenchmarkSummary: %v", err)
	}
	models := ai.LoadUserCatalog("")
	if len(models) != 1 || models[0].Benchmark == nil || models[0].Benchmark.AvgLatencyMs != 123 {
		t.Fatalf("saved catalog models = %+v", models)
	}
}
