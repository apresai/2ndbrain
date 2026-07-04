package eval

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"testing"

	"github.com/apresai/2ndbrain/internal/ai"
	"github.com/apresai/2ndbrain/internal/vault"
)

// judgeModels are the jury: strong models from DIFFERENT families than the Haiku
// answer generator, to avoid a single model's self-preference skewing the grade.
var judgeModels = []struct{ name, id string }{
	{"opus4.6", "global.anthropic.claude-opus-4-6-v1"},
	{"deepseek", "deepseek.v3.2"},
	{"nova-pro", "amazon.nova-pro-v1:0"},
}

// TestConfigSweep_Bedrock sweeps the retrieval config space (query purpose x
// hybrid weights x threshold) over a real Nova-embedded vault, ranking configs by
// how well they retrieve each question's ground-truth source note, then LLM-juries
// the end-to-end answers of the top configs. Gated on a vault + real Bedrock:
//
//	source ~/.secrets/shell.zsh
//	env 2NB_EVAL_VAULT=/path/to/vault EVAL_N=24 EVAL_JURY_TOP=4 \
//	  go test ./internal/eval/ -run ConfigSweep -v -count=1 -timeout 3600s
func TestConfigSweep_Bedrock(t *testing.T) {
	vpath := os.Getenv("2NB_EVAL_VAULT")
	if vpath == "" {
		t.Skip("set 2NB_EVAL_VAULT to a Nova-embedded vault to run the config sweep")
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
	if err := ai.InitBedrock(ctx, ai.DefaultRegistry, cfg.Bedrock, cfg); err != nil {
		t.Skipf("bedrock init (creds?): %v", err)
	}
	emb, err := ai.DefaultRegistry.Embedder("bedrock")
	if err != nil || !emb.Available(ctx) {
		t.Skip("bedrock embedder not available (no creds / no Nova access)")
	}
	gen, err := ai.DefaultRegistry.Generator("bedrock")
	if err != nil {
		t.Skipf("no generator: %v", err)
	}

	n := envInt("EVAL_N", 24)
	juryTop := envInt("EVAL_JURY_TOP", 4)

	// Ground-truth QA set, cached in the OS temp dir (not the vault, so the eval
	// never writes vault-derived questions into a user's notes tree). Override
	// with EVAL_QA_CACHE to pin a stable path across runs.
	qaPath := qaCachePath()
	qa, err := LoadOrGenerateQASet(ctx, v, gen, n, 0, qaPath)
	if err != nil {
		t.Fatalf("QA set: %v", err)
	}
	t.Logf("QA set: %d questions (cache %s)", len(qa), qaPath)

	grid := buildGrid()
	metrics, corp, err := RunRetrievalSweep(ctx, v, emb, qa, grid, 10)
	if err != nil {
		t.Fatalf("retrieval sweep: %v", err)
	}

	t.Logf("=== RETRIEVAL SWEEP (source-note in top-10; N=%d) ===", len(qa))
	t.Logf("%-26s  %-8s %-6s %-6s", "config", "MRR@10", "R@1", "R@10")
	for _, m := range metrics {
		t.Logf("%-26s  %.4f   %.3f  %.3f", m.Config.Name, m.MRRAtK, m.RecallAt1, m.RecallAtK)
	}

	if juryTop <= 0 {
		t.Log("EVAL_JURY_TOP<=0: retrieval-only run, skipping the LLM-jury phase")
		return
	}
	// Jury a curated CONTRAST set that isolates each variable (purpose, vector
	// weighting, keyword floor) rather than near-duplicate top-K configs — the
	// retrieval sweep already showed the top configs cluster at 1:3.
	toJury := contrastConfigs()
	_ = juryTop
	judges := buildJudges(ctx, t, cfg)
	if len(judges) == 0 {
		t.Log("no judge models reachable — skipping the LLM-jury phase (retrieval sweep above still stands)")
		return
	}
	t.Logf("=== LLM JURY (mean of %d judges: %v) on end-to-end answers ===", len(judges), judgeNames(judges))

	type juryScore struct {
		cfg      SweepConfig
		mean     float64
		perJudge map[string]float64
		answered int
	}
	var scored []juryScore
	for _, jc := range toJury {
		var sum float64
		var count int
		perJudge := map[string]float64{}
		perJudgeN := map[string]int{}
		for i, item := range qa {
			ans, _, err := GenerateAnswer(ctx, v, corp, gen, jc, i, item.Question)
			if err != nil {
				continue
			}
			mean, judgments := ScoreAnswer(ctx, judges, item.Question, ans, item.SourceTitle, item.SourceBody)
			if mean == 0 {
				continue
			}
			sum += mean
			count++
			for _, jd := range judgments {
				if jd.Score >= 1 {
					perJudge[jd.Judge] += float64(jd.Score)
					perJudgeN[jd.Judge]++
				}
			}
		}
		js := juryScore{cfg: jc, answered: count}
		if count > 0 {
			js.mean = sum / float64(count)
		}
		for name, s := range perJudge {
			if perJudgeN[name] > 0 {
				perJudge[name] = s / float64(perJudgeN[name])
			}
		}
		js.perJudge = perJudge
		scored = append(scored, js)
		t.Logf("  %-26s jury=%.3f (n=%d)  per-judge=%v", jc.Name, js.mean, js.answered, fmtPerJudge(perJudge))
	}
	sort.Slice(scored, func(i, j int) bool { return scored[i].mean > scored[j].mean })

	if len(scored) > 0 {
		best := scored[0]
		t.Logf("=== WINNER by jury: %q  jury=%.3f/5 ===", best.cfg.Name, best.mean)
		t.Logf("    purpose=%s weights=%.0f:%.0f threshold=%.2f", best.cfg.QueryPurpose, best.cfg.BM25Weight, best.cfg.VectorWeight, best.cfg.Threshold)
	}
}

// buildGrid enumerates the cheap-sweep configs (no re-embed): 2 query purposes x
// 4 weight ratios x per-purpose thresholds, plus a BM25-only keyword baseline.
func buildGrid() []SweepConfig {
	purposes := []struct{ tag, purpose string }{
		{"GEN", ai.PurposeQuery},     // GENERIC_RETRIEVAL (shipped)
		{"TXT", ai.PurposeQueryText}, // TEXT_RETRIEVAL (candidate)
	}
	weights := []struct {
		tag  string
		b, v float64
	}{{"1:1", 1, 1}, {"1:2", 1, 2}, {"1:3", 1, 3}, {"2:1", 2, 1}}
	thrByPurpose := map[string][]float64{
		ai.PurposeQuery:     {0.0, 0.20, 0.25},
		ai.PurposeQueryText: {0.0, 0.45, 0.53},
	}
	var grid []SweepConfig
	for _, p := range purposes {
		for _, w := range weights {
			for _, thr := range thrByPurpose[p.purpose] {
				grid = append(grid, SweepConfig{
					Name:         fmt.Sprintf("%s %s t%.2f", p.tag, w.tag, thr),
					QueryPurpose: p.purpose, BM25Weight: w.b, VectorWeight: w.v, Threshold: thr,
				})
			}
		}
	}
	grid = append(grid, SweepConfig{Name: "BM25-only", BM25Only: true})
	return grid
}

// contrastConfigs is the hand-picked set the jury grades end-to-end. Each pair
// isolates one variable so the jury deltas are interpretable:
//   - GEN 1:1 t0.25  = the current shipped default (reference)
//   - TXT 1:1 t0.53  = purpose flip only (GENERIC->TEXT), held at 1:1
//   - GEN 1:3 t0.25  = weighting only (1:1->1:3 vector-heavy), held on GENERIC
//   - TXT 1:3 t0.53  = both (TEXT + vector-heavy), the retrieval front-runner
//   - BM25-only      = keyword floor
func contrastConfigs() []SweepConfig {
	return []SweepConfig{
		{Name: "GEN 1:1 t0.25 (current)", QueryPurpose: ai.PurposeQuery, BM25Weight: 1, VectorWeight: 1, Threshold: 0.25},
		{Name: "TXT 1:1 t0.53", QueryPurpose: ai.PurposeQueryText, BM25Weight: 1, VectorWeight: 1, Threshold: 0.53},
		{Name: "GEN 1:3 t0.25", QueryPurpose: ai.PurposeQuery, BM25Weight: 1, VectorWeight: 3, Threshold: 0.25},
		{Name: "TXT 1:3 t0.53", QueryPurpose: ai.PurposeQueryText, BM25Weight: 1, VectorWeight: 3, Threshold: 0.53},
		{Name: "BM25-only", BM25Only: true},
	}
}

func buildJudges(ctx context.Context, t *testing.T, cfg ai.AIConfig) []Judge {
	var judges []Judge
	for _, jm := range judgeModels {
		g, err := ai.NewBedrockGenerator(ctx, cfg.Bedrock, jm.id)
		if err != nil {
			t.Logf("judge %s unavailable: %v", jm.name, err)
			continue
		}
		// Probe: a judge that errors on a trivial prompt is dropped.
		if _, err := g.Generate(ctx, "Reply with the digit 3.", ai.GenOpts{MaxTokens: 4, Temperature: ai.Ptr(0.0)}); err != nil {
			t.Logf("judge %s probe failed (no model access?): %v", jm.name, err)
			continue
		}
		judges = append(judges, Judge{Name: jm.name, Gen: g})
	}
	return judges
}

func judgeNames(judges []Judge) []string {
	out := make([]string, len(judges))
	for i, j := range judges {
		out[i] = j.Name
	}
	return out
}

func fmtPerJudge(m map[string]float64) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	s := ""
	for _, k := range keys {
		s += fmt.Sprintf("%s=%.2f ", k, m[k])
	}
	return s
}

// qaCachePath returns where the generated QA set is cached: EVAL_QA_CACHE if set,
// else a fixed file in the OS temp dir. Never inside the vault, so vault-derived
// content is never written into a user's notes tree.
func qaCachePath() string {
	if p := os.Getenv("EVAL_QA_CACHE"); p != "" {
		return p
	}
	return filepath.Join(os.TempDir(), "2nb-eval-qaset.json")
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}
