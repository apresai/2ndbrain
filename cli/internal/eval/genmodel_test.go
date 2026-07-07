package eval

import (
	"context"
	"os"
	"sort"
	"testing"

	"github.com/apresai/2ndbrain/internal/ai"
	"github.com/apresai/2ndbrain/internal/vault"
)

// candidateGenerators are the answer-side models compared head to head. None of
// them is a jury model (below), so no model grades its own answer.
var candidateGenerators = []struct{ name, id string }{
	{"haiku4.5", "us.anthropic.claude-haiku-4-5-20251001-v1:0"}, // current default
	{"sonnet4.6", "global.anthropic.claude-sonnet-4-6"},
	{"nova-pro", "amazon.nova-pro-v1:0"},
	{"gemma3-27b", "google.gemma-3-27b-it"},
}

// genJury is a FIXED panel disjoint from candidateGenerators, so a generator is
// never its own judge. CAVEAT: opus4.6 is Anthropic, same family as two of the
// candidates (haiku/sonnet), so a family-preference could inflate the
// Anthropic-vs-{nova,gemma} gap; it affects haiku and sonnet symmetrically, so
// the sonnet>haiku ordering is unaffected. Add a third non-Anthropic juror if
// you need the cross-family gaps to be tighter.
var genJury = []struct{ name, id string }{
	{"opus4.6", "global.anthropic.claude-opus-4-6-v1"},
	{"deepseek", "deepseek.v3.2"},
}

// TestGenModelSweep_Bedrock holds retrieval at the current shipped config
// (GENERIC_RETRIEVAL, 1:1, threshold 0.25 — the jury winner of the retrieval
// sweep) and varies only the ANSWER generator, to isolate how much of answer
// quality is set by the generator rather than the retrieval knobs. Gated:
//
//	env 2NB_EVAL_VAULT=/path EVAL_N=30 \
//	  go test ./internal/eval/ -run GenModelSweep -v -count=1 -timeout 3600s
func TestGenModelSweep_Bedrock(t *testing.T) {
	vpath := os.Getenv("2NB_EVAL_VAULT")
	if vpath == "" {
		t.Skip("set 2NB_EVAL_VAULT to a Nova-embedded vault to run the generation-model sweep")
	}
	v, err := vault.Open(vpath)
	if err != nil {
		t.Skipf("open vault %q: %v", vpath, err)
	}
	defer v.DB.Close()
	cfg := v.Config.AI
	if cfg.Provider != "bedrock" {
		t.Skipf("eval needs a bedrock vault, got provider %q", cfg.Provider)
	}
	ctx := context.Background()
	if err := ai.InitBedrock(ctx, ai.DefaultRegistry, cfg.Bedrock, cfg, v.Root); err != nil {
		t.Skipf("bedrock init (creds?): %v", err)
	}
	emb, err := ai.DefaultRegistry.Embedder("bedrock")
	if err != nil || !emb.Available(ctx) {
		t.Skip("bedrock embedder not available")
	}
	gen0, err := ai.DefaultRegistry.Generator("bedrock")
	if err != nil {
		t.Skipf("no generator for QA gen: %v", err)
	}

	n := envInt("EVAL_N", 24)
	qa, err := LoadOrGenerateQASet(ctx, v, gen0, n, 0, qaCachePath())
	if err != nil {
		t.Fatalf("QA set: %v", err)
	}

	// Hold retrieval at the current shipped config; build the corpus once.
	current := SweepConfig{Name: "current", QueryPurpose: ai.PurposeQuery, BM25Weight: 1, VectorWeight: 1, Threshold: 0.25}
	corp, err := loadCorpus(ctx, v, emb, qa, []string{ai.PurposeQuery})
	if err != nil {
		t.Fatalf("load corpus: %v", err)
	}

	judges := buildNamedJudges(ctx, t, cfg, genJury)
	if len(judges) == 0 {
		t.Skip("no judge models reachable")
	}
	t.Logf("QA=%d  retrieval=current(GEN 1:1 t0.25)  judges=%v", len(qa), judgeNames(judges))
	t.Logf("=== GENERATION-MODEL SWEEP (answer quality, retrieval held constant) ===")

	type row struct {
		name string
		mean float64
		n    int
	}
	var rows []row
	for _, cg := range candidateGenerators {
		g, err := ai.NewBedrockGenerator(ctx, cfg.Bedrock, cg.id)
		if err != nil {
			t.Logf("generator %s unavailable: %v", cg.name, err)
			continue
		}
		var sum float64
		var count int
		for i, item := range qa {
			ans, _, err := GenerateAnswer(ctx, v, corp, g, current, i, item.Question)
			if err != nil {
				continue
			}
			sc := ScoreAnswer(ctx, judges, item.Question, ans, item.SourceTitle, item.SourceBody)
			if sc.NJudges == 0 {
				continue
			}
			sum += sc.Composite
			count++
		}
		r := row{name: cg.name, n: count}
		if count > 0 {
			r.mean = sum / float64(count)
		}
		rows = append(rows, r)
		t.Logf("  %-12s answer-jury=%.3f (n=%d)", cg.name, r.mean, r.n)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].mean > rows[j].mean })
	if len(rows) > 0 {
		t.Logf("=== BEST GENERATOR: %s  answer-jury=%.3f/5 ===", rows[0].name, rows[0].mean)
	}
}

// buildNamedJudges constructs and probes a specific judge list.
func buildNamedJudges(ctx context.Context, t *testing.T, cfg ai.AIConfig, models []struct{ name, id string }) []Judge {
	var judges []Judge
	for _, jm := range models {
		g, err := ai.NewBedrockGenerator(ctx, cfg.Bedrock, jm.id)
		if err != nil {
			t.Logf("judge %s unavailable: %v", jm.name, err)
			continue
		}
		if _, err := g.Generate(ctx, "Reply with the digit 3.", ai.GenOpts{MaxTokens: 4, Temperature: ai.Ptr(0.0)}); err != nil {
			t.Logf("judge %s probe failed: %v", jm.name, err)
			continue
		}
		judges = append(judges, Judge{Name: jm.name, Gen: g})
	}
	return judges
}
