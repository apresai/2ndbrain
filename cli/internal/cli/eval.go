package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/apresai/2ndbrain/internal/ai"
	"github.com/apresai/2ndbrain/internal/eval"
	"github.com/apresai/2ndbrain/internal/output"
	"github.com/apresai/2ndbrain/internal/vault"
	"github.com/spf13/cobra"
)

var (
	evalN       int
	evalRegen   bool
	evalCostCap float64
	evalYes     bool
	evalSeed    int64
)

// EvalConfig is the retrieval configuration a scorecard was measured under.
type EvalConfig struct {
	Threshold    float64 `json:"threshold"`
	BM25Weight   float64 `json:"bm25_weight"`
	VectorWeight float64 `json:"vector_weight"`
}

// EvalReport is the `2nb eval --json` shape: how well hybrid search ranks the
// right note over a ground-truth QA set generated from the user's own notes.
type EvalReport struct {
	N           int        `json:"n"`
	K           int        `json:"k"`
	Config      EvalConfig `json:"config"`
	RecallAtK   float64    `json:"recall_at_k"`
	RecallAt1   float64    `json:"recall_at_1"`
	MRRAtK      float64    `json:"mrr_at_k"`
	QACached    bool       `json:"qa_cached"`
	GeneratedAt string     `json:"generated_at"`
}

var evalCmd = &cobra.Command{
	Use:   "eval",
	Short: "Score search quality on your vault (retrieval scorecard)",
	Long: `Measure how well hybrid search ranks the right note for questions
generated from your OWN notes, under your current AI config: Recall@10, R@1
(ranked #1), and MRR@10.

It generates a ground-truth Q&A set once (cached in .2ndbrain/eval/, gitignored),
so re-running the scorecard afterward is free. The QA questions are derived from
your notes and never leave your machine except via the same AI calls indexing
already makes.`,
	Example: `  2nb eval                    # scorecard for your current config
  2nb eval --n 40             # a larger QA set (more cost, more stable)
  2nb eval --regenerate       # refresh the QA set after big vault changes
  2nb eval --json             # machine-readable EvalReport`,
	Args: cobra.NoArgs,
	RunE: runEval,
}

func init() {
	evalCmd.Flags().IntVar(&evalN, "n", 20, "Number of ground-truth questions to score against")
	evalCmd.Flags().BoolVar(&evalRegen, "regenerate", false, "Discard the cached QA set and generate a fresh one")
	evalCmd.Flags().Float64Var(&evalCostCap, "cost-cap", 0.25, "Abort if the estimated QA-generation cost exceeds this many USD")
	evalCmd.Flags().BoolVar(&evalYes, "yes", false, "Skip the cost-confirmation prompt")
	evalCmd.Flags().Int64Var(&evalSeed, "seed", 0, "Deterministic QA-set sampling seed")
	evalCmd.GroupID = "ai"
	rootCmd.AddCommand(evalCmd)
}

// evalQAPath is the persistent, gitignored QA-set cache in the vault sidecar.
func evalQAPath(v *vault.Vault) string {
	return filepath.Join(v.Root, ".2ndbrain", "eval", "qa.json")
}

// qaCacheHas reports whether the cache holds at least n usable QA items (the
// same condition LoadOrGenerateQASet reuses vs regenerates on).
func qaCacheHas(path string, n int) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var items []eval.QAItem
	return json.Unmarshal(data, &items) == nil && len(items) >= n
}

func runEval(cmd *cobra.Command, args []string) error {
	v, err := openVault()
	if err != nil {
		return err
	}
	defer v.Close()
	setupFileLogging(v)
	initAIProviders(v)
	ctx := context.Background()
	cfg := v.Config.AI

	// Retrieval scoring needs embeddings; QA generation needs a generator.
	if embCount, _ := v.DB.EmbeddingCount(); embCount == 0 {
		return fmt.Errorf("no embeddings yet — run `2nb index` first so search has vectors to score")
	}
	embedder, err := ai.DefaultRegistry.Embedder(cfg.Provider)
	if err != nil || !embedder.Available(ctx) {
		return fmt.Errorf("embedding provider %q is not available — check `2nb ai status`", cfg.Provider)
	}
	generator, gerr := ai.DefaultRegistry.Generator(cfg.Provider)
	if gerr != nil || !generator.Available(ctx) {
		return fmt.Errorf("a generation provider is required to write the QA set — check `2nb ai status`")
	}

	if evalN < 1 {
		evalN = 1
	}
	qaPath := evalQAPath(v)
	if evalRegen {
		_ = os.Remove(qaPath)
	}
	cached := qaCacheHas(qaPath, evalN)

	// Cost preview + confirm only when we'd actually generate (a cached QA set
	// is free to reuse). The cap aborts identically whether interactive or piped.
	if !cached {
		if err := evalConfirmCost(ctx, cmd, cfg, v.Root, evalN); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(filepath.Dir(qaPath), 0o755); err != nil {
		return fmt.Errorf("create eval cache dir: %w", err)
	}
	// The QA cache embeds note bodies; ensure it's gitignored even in a vault
	// created before .2ndbrain/eval/ was added to the default ignore list.
	ensureVaultIgnores(v.Root, ".2ndbrain/eval/")

	if !flagPorcelain {
		if cached {
			fmt.Fprintf(os.Stderr, "Using cached QA set (%s)\n", qaPath)
		} else {
			fmt.Fprintf(os.Stderr, "Generating a %d-question QA set from your notes...\n", evalN)
		}
	}
	qa, err := eval.LoadOrGenerateQASet(ctx, v, generator, evalN, evalSeed, qaPath)
	if err != nil {
		return fmt.Errorf("build QA set: %w", err)
	}
	if len(qa) == 0 {
		return fmt.Errorf("could not build a QA set — the vault needs some substantial notes to generate questions from")
	}

	// Score the user's CURRENT config so the scorecard reflects their real setup.
	threshold, _ := cfg.ResolveSimilarityThresholdFull(v.Root)
	bm25W, vecW := cfg.ResolveHybridWeights()
	current := eval.SweepConfig{
		Name:         "current",
		QueryPurpose: ai.PurposeQuery,
		BM25Weight:   bm25W,
		VectorWeight: vecW,
		Threshold:    threshold,
	}
	if !flagPorcelain {
		fmt.Fprintf(os.Stderr, "Scoring retrieval over %d questions...\n", len(qa))
	}
	metrics, _, err := eval.RunRetrievalSweep(ctx, v, embedder, qa, []eval.SweepConfig{current}, 10)
	if err != nil {
		return fmt.Errorf("retrieval scoring: %w", err)
	}
	if len(metrics) == 0 {
		return fmt.Errorf("retrieval scoring produced no metrics")
	}
	m := metrics[0]

	report := EvalReport{
		N: m.N, K: m.K,
		Config:      EvalConfig{Threshold: threshold, BM25Weight: bm25W, VectorWeight: vecW},
		RecallAtK:   m.RecallAtK,
		RecallAt1:   m.RecallAt1,
		MRRAtK:      m.MRRAtK,
		QACached:    cached,
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
	}

	if format := getFormat(cmd); format != "" {
		return writeOut(cmd, format, report)
	}

	fmt.Printf("Vault search quality — %d questions, current config (threshold %.2f, bm25 %.1f / vector %.1f)\n\n",
		report.N, threshold, bm25W, vecW)
	fmt.Printf("  Recall@%d:  %3.0f%%   the right note reaches the top %d\n", m.K, 100*m.RecallAtK, m.K)
	fmt.Printf("  R@1:       %3.0f%%   the right note is ranked #1\n", 100*m.RecallAt1)
	fmt.Printf("  MRR@%d:     %.3f\n\n", m.K, m.MRRAtK)
	fmt.Println(evalReadout(m))
	return nil
}

// evalReadout turns the numbers into a one-line plain-English read + a nudge.
func evalReadout(m eval.ConfigMetrics) string {
	switch {
	case m.RecallAtK >= 0.95 && m.RecallAt1 >= 0.65:
		return "Search is strong: the right note almost always surfaces, and usually first. No tuning needed."
	case m.RecallAtK >= 0.90:
		return "Recall is high (the right note is nearly always in the top results), but its #1 rank could be sharper. `2nb eval tune` (coming soon) can suggest threshold/weight tweaks."
	default:
		return "Recall is lower than ideal — some answers may be missed. Check embeddings are current (`2nb ai status`), try `--n` larger for a steadier read, and consider `2nb config set ai.similarity_threshold` lower."
	}
}

// evalConfirmCost estimates the QA-generation + query-embedding cost, prints a
// preview, aborts above --cost-cap, and asks for confirmation on a TTY.
func evalConfirmCost(ctx context.Context, cmd *cobra.Command, cfg ai.AIConfig, root string, n int) error {
	models, err := loadVerifiedModelCatalog(ctx, cfg, root)
	if err != nil {
		models = ai.BuiltinCatalog() // builtin pricing is enough for an estimate
	}
	genM, _ := lookupModelInfo(models, cfg.Provider, cfg.GenerationModel)
	embM, _ := lookupModelInfo(models, cfg.Provider, cfg.EmbeddingModel)

	// QA generation: read a note (~1500 tok) + write a question (~80 tok) per item.
	genEst := ai.EstimateCostWithSpec(genM, ai.ProbeBenchGen,
		ai.ProbeSpec{InputTokens: 1500 * n, OutputTokens: 80 * n, Requests: n})
	// Query embeds: one short (~20 tok) embed per question during scoring.
	embEst := ai.EstimateCostWithSpec(embM, ai.ProbeBenchEmbed,
		ai.ProbeSpec{InputTokens: 20 * n, Requests: n})
	total := genEst.USD + embEst.USD

	if getFormat(cmd) != output.FormatJSON && !flagPorcelain {
		fmt.Fprintf(os.Stderr,
			"Estimated cost to build a %d-question QA set: ~$%.4f (generation ~$%.4f + embeds ~$%.4f). Re-runs are free (cached).\n",
			n, total, genEst.USD, embEst.USD)
	}
	if total > evalCostCap {
		return fmt.Errorf("estimated cost $%.4f exceeds --cost-cap $%.2f; raise --cost-cap or lower --n", total, evalCostCap)
	}
	if !evalYes && stderrIsTTY() && getFormat(cmd) != output.FormatJSON {
		if !promptYesNo(os.Stderr, "Generate the QA set now?", true) {
			return fmt.Errorf("aborted")
		}
	}
	return nil
}
