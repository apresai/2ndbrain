package eval

import (
	"context"
	"os"
	"sort"
	"testing"
	"unicode/utf8"

	"github.com/apresai/2ndbrain/internal/ai"
	"github.com/apresai/2ndbrain/internal/vault"
)

// promptVariants isolate the two generation-side levers the config study pointed
// at: the "concisely" wording (which may cap the completeness the jury grades)
// and inline source citation. Retrieval + generator are held constant.
// P0 pins the OLD "concisely" wording explicitly (not defaultRAGInstruction, which
// now tracks the shipped neutral prompt) so this A/B stays a valid old-vs-shipped
// comparison. P2 == the shipped default; P1/P3/P4 are the refuted alternatives
// (thorough / citations both regressed grounding).
var promptVariants = []PromptVariant{
	{Name: "P0 concise (was shipped)", Instruction: "Answer concisely based only on the provided documents. If the documents don't contain the answer, say so."},
	{Name: "P1 thorough", Instruction: "Answer thoroughly and completely, including all relevant details from the documents. If the documents don't contain the answer, say so."},
	{Name: "P2 neutral (now shipped)", Instruction: defaultRAGInstruction},
	{Name: "P3 thorough+cite", Instruction: "Answer thoroughly and completely, including all relevant details from the documents. If the documents don't contain the answer, say so.", Cite: true},
	{Name: "P4 concise+cite", Instruction: "Answer concisely based only on the provided documents. If the documents don't contain the answer, say so.", Cite: true},
}

// promptJury adds a non-Anthropic third juror (llama3-70b) to the opus4.6+deepseek
// panel, so the panel isn't Anthropic-leaning while grading Haiku-generated answers.
var promptJury = []struct{ name, id string }{
	{"opus4.6", "global.anthropic.claude-opus-4-6-v1"},
	{"deepseek", "deepseek.v3.2"},
	{"llama3-70b", "meta.llama3-70b-instruct-v1:0"},
}

// TestPromptSweep_Bedrock A/B-tests RAG answer-PROMPT variants with retrieval held
// at the measured-optimal config and the default Haiku generator, scoring each
// end-to-end answer on correctness/completeness/grounding (multi-axis) + answer
// length. Gated:
//
//	env 2NB_EVAL_VAULT=/path EVAL_N=30 \
//	  go test ./internal/eval/ -run PromptSweep -v -count=1 -timeout 3600s
func TestPromptSweep_Bedrock(t *testing.T) {
	v, cfg, emb, gen, ok := openEvalVault(t)
	if !ok {
		return
	}
	defer v.DB.Close()
	ctx := context.Background()

	n := envInt("EVAL_N", 24)
	qa, err := LoadOrGenerateQASet(ctx, v, gen, n, 0, qaCachePath())
	if err != nil {
		t.Fatalf("QA set: %v", err)
	}
	current := SweepConfig{Name: "current", QueryPurpose: ai.PurposeQuery, BM25Weight: 1, VectorWeight: 1, Threshold: 0.25}
	corp, err := loadCorpus(ctx, v, emb, qa, []string{ai.PurposeQuery})
	if err != nil {
		t.Fatalf("load corpus: %v", err)
	}
	judges := buildNamedJudges(ctx, t, cfg, promptJury)
	if len(judges) == 0 {
		t.Skip("no judge models reachable")
	}
	t.Logf("QA=%d  retrieval=current(GEN 1:1 t0.25)  generator=haiku4.5  judges=%v", len(qa), judgeNames(judges))
	t.Logf("=== PROMPT SWEEP (multi-axis answer quality; retrieval+generator held) ===")
	t.Logf("%-22s  %-7s %-6s %-6s %-6s %-7s", "variant", "COMP", "corr", "compl", "grnd", "len")

	type row struct {
		pv                         PromptVariant
		comp, corr, compl, grnd    float64
		meanLen                    float64
		n                          int
	}
	variants := promptVariants
	if os.Getenv("EVAL_PROMPT_CONFIRM") != "" {
		// Confirmation run: only the baseline P0 and the front-runner P2, at a
		// larger N on fresh questions, to tighten the borderline P2-vs-P0 margin.
		variants = []PromptVariant{promptVariants[0], promptVariants[2]}
	}
	var rows []row
	for _, pv := range variants {
		var sc, scc, scp, scg, lenSum float64
		var count int
		for i, item := range qa {
			ans, err := GenerateAnswerVariant(ctx, v, corp, gen, current, pv, i, item.Question)
			if err != nil {
				continue
			}
			s := ScoreAnswer(ctx, judges, item.Question, ans, item.SourceTitle, item.SourceBody)
			if s.NJudges == 0 {
				continue
			}
			sc += s.Composite
			scc += s.Correctness
			scp += s.Completeness
			scg += s.Grounding
			lenSum += float64(utf8.RuneCountInString(ans))
			count++
		}
		r := row{pv: pv, n: count}
		if count > 0 {
			fn := float64(count)
			r.comp, r.corr, r.compl, r.grnd, r.meanLen = sc/fn, scc/fn, scp/fn, scg/fn, lenSum/fn
		}
		rows = append(rows, r)
		t.Logf("%-22s  %.3f   %.2f   %.2f   %.2f   %.0f (n=%d)", pv.Name, r.comp, r.corr, r.compl, r.grnd, r.meanLen, r.n)
	}

	// Decision rule: the winner is the highest composite that does NOT regress
	// grounding vs P0 (a grounding drop = more hallucination = disqualifying).
	var p0 row
	for _, r := range rows {
		if r.pv.Name == promptVariants[0].Name {
			p0 = r
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].comp > rows[j].comp })
	for _, r := range rows {
		grounded := r.grnd >= p0.grnd-0.05
		lead := r.comp - p0.comp
		flag := "eligible"
		if !grounded {
			flag = "DISQUALIFIED (grounding regressed)"
		}
		t.Logf("  %-22s comp %+.3f vs P0, grounding %+.3f vs P0, len %+.0f -> %s", r.pv.Name, lead, r.grnd-p0.grnd, r.meanLen-p0.meanLen, flag)
	}
	for _, r := range rows {
		if r.grnd >= p0.grnd-0.05 {
			t.Logf("=== BEST eligible: %q  composite=%.3f (P0=%.3f, %+.3f)  grounding=%.2f  meanLen=%.0f ===",
				r.pv.Name, r.comp, p0.comp, r.comp-p0.comp, r.grnd, r.meanLen)
			break
		}
	}
}

// openEvalVault is the shared gating boilerplate for the credential + vault gated
// eval tests: opens 2NB_EVAL_VAULT, inits Bedrock, returns the embedder + default
// generator. ok=false (with t.Skip already called) when prerequisites are absent.
func openEvalVault(t *testing.T) (*vault.Vault, ai.AIConfig, ai.EmbeddingProvider, ai.GenerationProvider, bool) {
	t.Helper()
	vpath := os.Getenv("2NB_EVAL_VAULT")
	if vpath == "" {
		t.Skip("set 2NB_EVAL_VAULT to a Nova-embedded vault to run this eval")
		return nil, ai.AIConfig{}, nil, nil, false
	}
	v, err := vault.Open(vpath)
	if err != nil {
		t.Skipf("open vault %q: %v", vpath, err)
		return nil, ai.AIConfig{}, nil, nil, false
	}
	cfg := v.Config.AI
	if cfg.Provider != "bedrock" {
		v.DB.Close()
		t.Skipf("eval needs a bedrock vault, got provider %q", cfg.Provider)
		return nil, ai.AIConfig{}, nil, nil, false
	}
	ctx := context.Background()
	if err := ai.InitBedrock(ctx, ai.DefaultRegistry, cfg.Bedrock, cfg, v.Root); err != nil {
		v.DB.Close()
		t.Skipf("bedrock init (creds?): %v", err)
		return nil, ai.AIConfig{}, nil, nil, false
	}
	emb, err := ai.DefaultRegistry.Embedder("bedrock")
	if err != nil || !emb.Available(ctx) {
		v.DB.Close()
		t.Skip("bedrock embedder not available")
		return nil, ai.AIConfig{}, nil, nil, false
	}
	gen, err := ai.DefaultRegistry.Generator("bedrock")
	if err != nil {
		v.DB.Close()
		t.Skipf("no generator: %v", err)
		return nil, ai.AIConfig{}, nil, nil, false
	}
	return v, cfg, emb, gen, true
}
