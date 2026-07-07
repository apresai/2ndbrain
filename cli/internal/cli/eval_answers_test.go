package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/apresai/2ndbrain/internal/ai"
)

func TestEstimateAnswersCostUSD(t *testing.T) {
	gen := ai.ModelInfo{ID: "g", Provider: "bedrock", Type: "generation", PriceIn: 1.0, PriceOut: 5.0, PriceSource: "builtin"}
	judge := ai.ModelInfo{ID: "j", Provider: "bedrock", Type: "generation", PriceIn: 3.0, PriceOut: 15.0, PriceSource: "builtin"}

	// n=10 answers on gen: 25000 in + 5120 out tokens = 0.025 + 0.0256 USD.
	// One judge: 12000 in + 400 out = 0.036 + 0.006 USD.
	got := estimateAnswersCostUSD(gen, []ai.ModelInfo{judge}, 10)
	want := 0.025 + 0.0256 + 0.036 + 0.006
	if diff := got - want; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("estimate = %v, want %v", got, want)
	}

	// Two judges double the grading share.
	got2 := estimateAnswersCostUSD(gen, []ai.ModelInfo{judge, judge}, 10)
	if got2 <= got {
		t.Fatalf("second judge must increase the estimate: %v vs %v", got2, got)
	}
}

func TestAnswersReportJSONShape(t *testing.T) {
	report := AnswersReport{
		N: 5, Answered: 4, Failed: 1,
		Correctness: 4.5, Completeness: 4.0, Grounding: 4.8, Composite: 4.4,
		NJudges: 1, SelfJudged: true, Judges: []string{"m"},
		QACached: true, GeneratedAt: "2026-07-07T00:00:00Z",
	}
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"n":5`, `"answered":4`, `"failed":1`, `"composite":4.4`, `"self_judged":true`, `"qa_cached":true`, `"n_judges":1`} {
		if !strings.Contains(string(data), key) {
			t.Errorf("AnswersReport JSON missing %s:\n%s", key, data)
		}
	}
}

// TestContract_EvalAnswers_CredGated drives the full jury pipeline against
// real providers with a tiny QA set (cents). Skips without AWS credentials.
func TestContract_EvalAnswers_CredGated(t *testing.T) {
	if testing.Short() {
		t.Skip("cred-gated e2e")
	}
	if !ai.CheckBedrockCredentials(t.Context(), ai.BedrockConfig{Profile: "default", Region: "us-east-1"}) {
		t.Skip("AWS credentials not configured")
	}
	_, root := newContractVault(t)

	body := strings.Repeat("The billing pipeline reconciles Stripe events into DynamoDB nightly and alerts on drift. ", 8)
	for _, title := range []string{"Billing Pipeline Notes", "Alerting Runbook Draft", "Stripe Reconciliation Design"} {
		if _, err := runCLIArgs(t, root, "create", "--type", "note", "--title", title, "--content", title+". "+body); err != nil {
			t.Fatalf("seed %q: %v", title, err)
		}
	}
	if out, err := runCLIArgs(t, root, "index"); err != nil {
		t.Fatalf("index: %v\n%s", err, truncate(out, 400))
	}

	out, err := runCLIArgs(t, root, "eval", "answers", "--n", "2", "--yes", "--json", "--porcelain")
	if err != nil {
		t.Fatalf("eval answers: %v\n%s", err, truncate(out, 800))
	}
	var report AnswersReport
	if err := json.Unmarshal(out, &report); err != nil {
		t.Fatalf("AnswersReport parse: %v\n%s", err, truncate(out, 400))
	}
	if report.Answered == 0 {
		t.Fatalf("no answers judged: %+v", report)
	}
	if report.Composite <= 0 || report.Composite > 5 {
		t.Fatalf("composite out of range: %+v", report)
	}
	if report.NJudges < 1 || !report.SelfJudged {
		t.Fatalf("expected self-judged single-judge default: %+v", report)
	}
	t.Logf("answers: n=%d answered=%d composite=%.2f grounding=%.2f", report.N, report.Answered, report.Composite, report.Grounding)
}
