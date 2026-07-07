package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/apresai/2ndbrain/internal/ai"
	"github.com/apresai/2ndbrain/internal/eval"
)

func TestTuneGrid(t *testing.T) {
	configs := tuneGrid(0.25, 1, 1)

	names := map[string]bool{}
	for _, c := range configs {
		names[c.Name] = true
		if !c.BM25Only && c.QueryPurpose != ai.PurposeQuery {
			t.Errorf("%s: sweep must use the production asymmetric query purpose", c.Name)
		}
	}
	if !names["current"] || !names["bm25-only"] {
		t.Fatalf("grid must include current + bm25-only baseline, got %v", names)
	}
	// 5 thresholds x 5 weights = 25, minus the one that coincides with
	// current (0.25/1/1), plus current + baseline.
	if len(configs) != 26 {
		t.Fatalf("grid size = %d, want 26", len(configs))
	}
	// The coincident grid point is deduped.
	if names["t0.25 b1.0 v1.0"] {
		t.Error("grid point equal to the current config must be deduped")
	}
}

func TestTuneSuggestion(t *testing.T) {
	mk := func(name string, mrr float64, bm25Only bool) eval.ConfigMetrics {
		return eval.ConfigMetrics{
			Config: eval.SweepConfig{Name: name, Threshold: 0.20, BM25Weight: 1.5, VectorWeight: 1, BM25Only: bm25Only},
			MRRAtK: mrr,
		}
	}
	current := mk("current", 0.90, false)

	// A clear winner produces the three config set commands.
	got := tuneSuggestion(mk("t0.20 b1.5 v1.0", 0.95, false), current)
	if len(got) != 3 {
		t.Fatalf("want 3 commands, got %v", got)
	}
	for _, cmd := range got {
		if !strings.Contains(cmd, "config set ai.") {
			t.Errorf("suggestion %q is not a config set command", cmd)
		}
	}

	// Within the noise margin: no suggestion (oscillation erodes trust).
	if got := tuneSuggestion(mk("x", 0.905, false), current); got != nil {
		t.Errorf("marginal win must not suggest, got %v", got)
	}

	// BM25-only winning is a diagnosis, not a setting.
	if got := tuneSuggestion(mk("bm25-only", 0.99, true), current); got != nil {
		t.Errorf("bm25-only winner must not suggest, got %v", got)
	}
}

// TestContract_EvalTune_CredGated drives the full tune pipeline against real
// providers: seed notes, real index/embeddings, QA generation (--n 3, cents),
// and the sweep. Skips without AWS credentials.
func TestContract_EvalTune_CredGated(t *testing.T) {
	if testing.Short() {
		t.Skip("cred-gated e2e")
	}
	if !ai.CheckBedrockCredentials(t.Context(), ai.BedrockConfig{Profile: "default", Region: "us-east-1"}) {
		t.Skip("AWS credentials not configured")
	}
	_, root := newContractVault(t)

	body := strings.Repeat("The reservation service stores bookings in DynamoDB using single-table design with entity prefixes. ", 8)
	topics := []string{"Reservation Service Design", "Auth Flow Overview", "Deploy Runbook Details", "Search Architecture Notes"}
	for _, title := range topics {
		if _, err := runCLIArgs(t, root, "create", "--type", "note", "--title", title, "--content", title+". "+body); err != nil {
			t.Fatalf("seed %q: %v", title, err)
		}
	}
	if out, err := runCLIArgs(t, root, "index"); err != nil {
		t.Fatalf("index: %v\n%s", err, truncate(out, 400))
	}

	out, err := runCLIArgs(t, root, "eval", "tune", "--n", "3", "--yes", "--json", "--porcelain")
	if err != nil {
		t.Fatalf("eval tune: %v\n%s", err, truncate(out, 800))
	}
	var report TuneReport
	if err := json.Unmarshal(out, &report); err != nil {
		t.Fatalf("TuneReport parse: %v\n%s", err, truncate(out, 400))
	}
	if report.N < 2 || len(report.Configs) < 20 {
		t.Fatalf("unexpected report shape: n=%d configs=%d", report.N, len(report.Configs))
	}
	for _, c := range report.Configs {
		if c.MRRAtK < 0 || c.MRRAtK > 1 || c.RecallAtK < 0 || c.RecallAtK > 1 {
			t.Fatalf("metric out of range: %+v", c)
		}
	}
	// Ranked best-first.
	if report.Configs[0].MRRAtK != report.Best.MRRAtK {
		t.Fatalf("best (%v) is not the top-ranked config (%v)", report.Best, report.Configs[0])
	}
	t.Logf("tune: n=%d best=%s mrr=%.3f current-mrr=%.3f suggestions=%d",
		report.N, report.Best.Name, report.Best.MRRAtK, report.Current.MRRAtK, len(report.Suggestion))
}
