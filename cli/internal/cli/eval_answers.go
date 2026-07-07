package cli

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/apresai/2ndbrain/internal/ai"
	"github.com/apresai/2ndbrain/internal/eval"
	"github.com/apresai/2ndbrain/internal/output"
	"github.com/spf13/cobra"
)

var evalJudges []string

// AnswersReport is the `2nb eval answers --json` shape: LLM-jury grades of
// end-to-end RAG answers over the ground-truth QA set.
type AnswersReport struct {
	N            int      `json:"n"`
	Answered     int      `json:"answered"`
	Failed       int      `json:"failed"` // items where retrieval/generation errored
	Correctness  float64  `json:"correctness"`
	Completeness float64  `json:"completeness"`
	Grounding    float64  `json:"grounding"`
	Composite    float64  `json:"composite"`
	NJudges      int      `json:"n_judges"`
	SelfJudged   bool     `json:"self_judged"`
	Judges       []string `json:"judges"`
	QACached     bool     `json:"qa_cached"`
	GeneratedAt  string   `json:"generated_at"`
}

var evalAnswersCmd = &cobra.Command{
	Use:   "answers",
	Short: "Grade end-to-end RAG answer quality with an LLM jury",
	Long: `Generate a real RAG answer for each ground-truth question (the same cached QA
set 2nb eval uses, via the production ask pipeline under your current config)
and have an LLM jury grade each answer 1-5 on correctness, completeness, and
grounding against the source note.

By default the jury is your configured generation model judging its own
answers: cheap and useful for RELATIVE comparisons (before/after a config
change), but biased as an absolute score; the output labels it self-judged.
Pass --judges with one or more other model IDs for a real panel.

This is the costly eval (n answers + n x judges gradings); the estimate is
gated by the shared --cost-cap.`,
	Example: `  2nb eval answers                 # self-judged scorecard
  2nb eval answers --n 10 --yes
  2nb eval answers --judges us.anthropic.claude-sonnet-4-6
  2nb eval answers --json          # machine-readable AnswersReport`,
	Args: cobra.NoArgs,
	RunE: runEvalAnswers,
}

func init() {
	evalAnswersCmd.Flags().StringSliceVar(&evalJudges, "judges", nil, "Judge model IDs (comma-separated or repeated); default is the configured generation model judging itself")
	evalCmd.AddCommand(evalAnswersCmd)
}

// estimateAnswersCostUSD projects the per-run cost: one RAG answer per item
// (parent-document context ~2500 tok in / ~512 out) plus one grading per
// judge per item (~1200 in / ~40 out).
func estimateAnswersCostUSD(genM ai.ModelInfo, judges []ai.ModelInfo, n int) float64 {
	total := ai.EstimateCostWithSpec(genM, ai.ProbeBenchRAG,
		ai.ProbeSpec{InputTokens: 2500 * n, OutputTokens: 512 * n, Requests: n}).USD
	for _, j := range judges {
		total += ai.EstimateCostWithSpec(j, ai.ProbeBenchGen,
			ai.ProbeSpec{InputTokens: 1200 * n, OutputTokens: 40 * n, Requests: n}).USD
	}
	return total
}

// judgeGenerator builds a generation provider for a judge model ID, mirroring
// the test-probe constructors (bedrock / openrouter / ollama by inference).
func judgeGenerator(ctx context.Context, cfg ai.AIConfig, modelID string) (ai.GenerationProvider, error) {
	switch provider := ai.InferProvider(modelID); provider {
	case "bedrock":
		return ai.NewBedrockGenerator(ctx, cfg.Bedrock, modelID)
	case "openrouter":
		key, err := ai.GetAPIKey("openrouter")
		if err != nil {
			return nil, err
		}
		return ai.NewOpenRouterGenerator(key, modelID), nil
	case "ollama":
		endpoint := cfg.Ollama.Endpoint
		if endpoint == "" {
			endpoint = "http://localhost:11434"
		}
		return ai.NewOllamaGenerator(endpoint, modelID), nil
	default:
		return nil, fmt.Errorf("cannot infer provider for judge %q", modelID)
	}
}

func runEvalAnswers(cmd *cobra.Command, args []string) error {
	v, err := openVault()
	if err != nil {
		return err
	}
	defer v.Close()
	setupFileLogging(v)
	initAIProviders(v)
	ctx := context.Background()
	cfg := v.Config.AI

	embedder, qa, cached, err := ensureQASet(ctx, cmd, v)
	if err != nil {
		return err
	}

	generator, gerr := ai.DefaultRegistry.Generator(cfg.Provider)
	if gerr != nil {
		return fmt.Errorf("generation provider not available: %w", gerr)
	}
	if !generator.Available(ctx) {
		return fmt.Errorf("generation provider %q is not available — check `2nb ai status`", cfg.Provider)
	}

	// Assemble the jury: explicit --judges, or the active model self-judging.
	selfJudged := len(evalJudges) == 0
	var judges []eval.Judge
	var judgeNames []string
	if selfJudged {
		judges = []eval.Judge{{Name: cfg.GenerationModel, Gen: generator}}
		judgeNames = []string{cfg.GenerationModel}
	} else {
		for _, id := range evalJudges {
			g, jerr := judgeGenerator(ctx, cfg, id)
			if jerr != nil {
				return fmt.Errorf("judge %s: %w", id, jerr)
			}
			judges = append(judges, eval.Judge{Name: id, Gen: g})
			judgeNames = append(judgeNames, id)
		}
	}

	// Cost gate over the whole run (answers + gradings).
	models, merr := loadVerifiedModelCatalog(ctx, cfg, v.Root)
	if merr != nil {
		models = ai.BuiltinCatalog()
	}
	genM, _ := lookupModelInfo(models, cfg.Provider, cfg.GenerationModel)
	judgeModels := make([]ai.ModelInfo, 0, len(judgeNames))
	for _, id := range judgeNames {
		jm, _ := lookupModelInfo(models, ai.InferProvider(id), id)
		judgeModels = append(judgeModels, jm)
	}
	total := estimateAnswersCostUSD(genM, judgeModels, len(qa))
	if getFormat(cmd) != output.FormatJSON && !flagPorcelain {
		fmt.Fprintf(cmd.ErrOrStderr(),
			"Estimated cost for %d answers judged by %d model(s): ~$%.4f\n", len(qa), len(judges), total)
	}
	if err := evalCostGate(total, evalCostCap); err != nil {
		return err
	}
	if !evalYes && stderrIsTTY() && getFormat(cmd) != output.FormatJSON {
		if !promptYesNo(cmd.ErrOrStderr(), "Generate and grade the answers now?", true) {
			return fmt.Errorf("aborted")
		}
	}

	// One sweep run of the CURRENT config yields the shared corpus (each
	// question embedded once) that answer generation reuses.
	threshold, _ := cfg.ResolveSimilarityThresholdFull(v.Root)
	bm25W, vecW := cfg.ResolveHybridWeights()
	current := eval.SweepConfig{
		Name: "current", QueryPurpose: ai.PurposeQuery,
		Threshold: threshold, BM25Weight: bm25W, VectorWeight: vecW,
	}
	_, corp, err := eval.RunRetrievalSweep(ctx, v, embedder, qa, []eval.SweepConfig{current}, 10)
	if err != nil {
		return fmt.Errorf("retrieval: %w", err)
	}

	var sumCorr, sumComp, sumGround, sumComposite float64
	answered, failed := 0, 0
	for i, item := range qa {
		if !flagPorcelain && getFormat(cmd) == "" {
			fmt.Fprintf(cmd.ErrOrStderr(), "[%d/%d] answering + judging...\n", i+1, len(qa))
		}
		ans, _, aerr := eval.GenerateAnswer(ctx, v, corp, generator, current, i, item.Question)
		if aerr != nil {
			failed++
			continue
		}
		score := eval.ScoreAnswer(ctx, judges, item.Question, ans, item.SourceTitle, item.SourceBody)
		if score.NJudges == 0 {
			failed++
			continue
		}
		answered++
		sumCorr += score.Correctness
		sumComp += score.Completeness
		sumGround += score.Grounding
		sumComposite += score.Composite
	}
	if answered == 0 {
		return fmt.Errorf("no answers could be generated and judged (%d failures) — check `2nb ai status`", failed)
	}

	report := AnswersReport{
		N:            len(qa),
		Answered:     answered,
		Failed:       failed,
		Correctness:  sumCorr / float64(answered),
		Completeness: sumComp / float64(answered),
		Grounding:    sumGround / float64(answered),
		Composite:    sumComposite / float64(answered),
		NJudges:      len(judges),
		SelfJudged:   selfJudged,
		Judges:       judgeNames,
		QACached:     cached,
		GeneratedAt:  time.Now().UTC().Format(time.RFC3339),
	}

	if format := getFormat(cmd); format != "" {
		return writeOut(cmd, format, report)
	}

	label := "panel: " + strings.Join(judgeNames, ", ")
	if selfJudged {
		label = "self-judged by " + cfg.GenerationModel + " (relative signal; biased as an absolute score)"
	}
	fmt.Printf("Answer quality — %d questions answered (%d failed), %s\n\n", answered, failed, label)
	fmt.Printf("  Correctness:   %.2f / 5\n", report.Correctness)
	fmt.Printf("  Completeness:  %.2f / 5\n", report.Completeness)
	fmt.Printf("  Grounding:     %.2f / 5\n", report.Grounding)
	fmt.Printf("  Composite:     %.2f / 5\n\n", report.Composite)
	fmt.Println("Compare runs before/after a config change; the absolute numbers matter less than the delta.")
	return nil
}
