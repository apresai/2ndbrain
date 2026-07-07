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
so re-running the scorecard afterward skips QA generation (only the query embeds
cost cents). The QA questions are derived from your notes and never leave your
machine except via the same AI calls indexing already makes.`,
	Example: `  2nb eval                    # scorecard for your current config
  2nb eval --n 40             # a larger QA set (more cost, more stable)
  2nb eval --regenerate       # refresh the QA set after big vault changes
  2nb eval --json             # machine-readable EvalReport`,
	Args: cobra.NoArgs,
	RunE: runEval,
}

func init() {
	// Persistent: the QA-set acquisition flags are shared with the `tune`
	// subcommand (both go through ensureQASet).
	evalCmd.PersistentFlags().IntVar(&evalN, "n", 20, "Number of ground-truth questions to score against")
	evalCmd.PersistentFlags().BoolVar(&evalRegen, "regenerate", false, "Discard the cached QA set and generate a fresh one")
	evalCmd.PersistentFlags().Float64Var(&evalCostCap, "cost-cap", 0.25, "Abort if the estimated QA-generation cost exceeds this many USD")
	evalCmd.PersistentFlags().BoolVar(&evalYes, "yes", false, "Skip the cost-confirmation prompt")
	evalCmd.PersistentFlags().Int64Var(&evalSeed, "seed", 0, "Deterministic QA-set sampling seed")
	evalCmd.GroupID = "ai"
	rootCmd.AddCommand(evalCmd)
}

// evalQAPath is the persistent, gitignored QA-set cache in the vault sidecar.
func evalQAPath(v *vault.Vault) string {
	return filepath.Join(v.Root, ".2ndbrain", "eval", "qa.json")
}

// loadQACache reads and parses the cached QA set, returning nil when the cache
// is absent or unreadable.
func loadQACache(path string) []eval.QAItem {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var items []eval.QAItem
	if json.Unmarshal(data, &items) != nil {
		return nil
	}
	return items
}

// qaCacheHas reports whether the cache holds at least n usable QA items.
func qaCacheHas(path string, n int) bool {
	return len(loadQACache(path)) >= n
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

	embedder, qa, cached, err := ensureQASet(ctx, cmd, v)
	if err != nil {
		return err
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

// ensureQASet acquires the ground-truth QA set for retrieval scoring (shared
// by `eval` and `eval tune`): validates the embedder + stored embeddings,
// reuses the authoritative cache when it holds a usable set (>= 2 items,
// regardless of --n), and otherwise cost-gates and generates a fresh one.
// Returns the ready embedder, the QA items, and whether the cache was used.
func ensureQASet(ctx context.Context, cmd *cobra.Command, v *vault.Vault) (ai.EmbeddingProvider, []eval.QAItem, bool, error) {
	cfg := v.Config.AI

	// Retrieval scoring always needs the embedder; QA GENERATION additionally
	// needs the generator, so that check is deferred to the cache-miss branch.
	if embCount, cerr := v.DB.EmbeddingCount(); cerr != nil {
		return nil, nil, false, fmt.Errorf("check embeddings: %w", cerr)
	} else if embCount == 0 {
		return nil, nil, false, fmt.Errorf("no embeddings yet — run `2nb index` first so search has vectors to score")
	}
	embedder, eerr := ai.DefaultRegistry.Embedder(cfg.Provider)
	if eerr != nil {
		return nil, nil, false, fmt.Errorf("embedding provider not available: %w", eerr)
	}
	if !embedder.Available(ctx) {
		return nil, nil, false, fmt.Errorf("embedding provider %q is not available — check `2nb ai status`", cfg.Provider)
	}

	if evalN < 1 {
		evalN = 1
	}
	qaPath := evalQAPath(v)
	if evalRegen {
		_ = os.Remove(qaPath)
	}

	// The cache is authoritative: any usable cached set (>= 2 items) is reused
	// regardless of --n. A vault that can't produce the full --n (small vault, or
	// generation skips) therefore does NOT re-pay for generation on every run —
	// use --regenerate to rebuild (e.g. after growing --n or changing the vault).
	cachedItems := loadQACache(qaPath)
	cached := len(cachedItems) >= 2

	var qa []eval.QAItem
	if cached {
		qa = cachedItems
		if len(qa) > evalN {
			qa = qa[:evalN]
		}
		if !flagPorcelain {
			fmt.Fprintf(os.Stderr, "Using cached QA set of %d questions (%s)\n", len(qa), qaPath)
		}
	} else {
		generator, gerr := ai.DefaultRegistry.Generator(cfg.Provider)
		if gerr != nil {
			return nil, nil, false, fmt.Errorf("a generation provider is required to build the QA set: %w", gerr)
		}
		if !generator.Available(ctx) {
			return nil, nil, false, fmt.Errorf("generation provider %q is not available — check `2nb ai status`", cfg.Provider)
		}
		// Cost preview + confirm (the cap aborts identically interactive or piped).
		if err := evalConfirmCost(ctx, cmd, cfg, v.Root, evalN); err != nil {
			return nil, nil, false, err
		}
		if err := os.MkdirAll(filepath.Dir(qaPath), 0o755); err != nil {
			return nil, nil, false, fmt.Errorf("create eval cache dir: %w", err)
		}
		// The QA cache embeds note bodies; ensure it's gitignored (even in a vault
		// created before .2ndbrain/eval/ was in the default list) BEFORE writing it.
		ensureVaultIgnores(v.Root, ".2ndbrain/eval/")
		if !flagPorcelain {
			fmt.Fprintf(os.Stderr, "Generating a %d-question QA set from your notes...\n", evalN)
		}
		var err error
		qa, err = eval.LoadOrGenerateQASet(ctx, v, generator, evalN, evalSeed, qaPath)
		if err != nil {
			return nil, nil, false, fmt.Errorf("build QA set: %w", err)
		}
	}
	if len(qa) == 0 {
		return nil, nil, false, fmt.Errorf("could not build a QA set — the vault needs some substantial notes (500+ chars) to generate questions from")
	}
	return embedder, qa, cached, nil
}

// evalReadout turns the numbers into a one-line plain-English read + a nudge.
func evalReadout(m eval.ConfigMetrics) string {
	switch {
	case m.RecallAtK >= 0.95 && m.RecallAt1 >= 0.65:
		return "Search is strong: the right note almost always surfaces, and usually first. No tuning needed."
	case m.RecallAtK >= 0.90:
		return "Recall is high (the right note is nearly always in the top results), but its #1 rank could be sharper. Run `2nb eval tune` to sweep threshold/weight combinations and get suggested settings."
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
	total, genUSD, embUSD := estimateEvalCostUSD(genM, embM, n)

	if getFormat(cmd) != output.FormatJSON && !flagPorcelain {
		fmt.Fprintf(os.Stderr,
			"Estimated one-time cost to build a %d-question QA set: ~$%.4f (generation ~$%.4f + embeds ~$%.4f). Later runs reuse the cache and only re-embed the queries (cents).\n",
			n, total, genUSD, embUSD)
	}
	if err := evalCostGate(total, evalCostCap); err != nil {
		return err
	}
	if !evalYes && stderrIsTTY() && getFormat(cmd) != output.FormatJSON {
		if !promptYesNo(os.Stderr, "Generate the QA set now?", true) {
			return fmt.Errorf("aborted")
		}
	}
	return nil
}

// estimateEvalCostUSD projects the one-time cost of building an n-question QA set
// (read a note ~1500 tok + write a question ~80 tok per item on the generation
// model) plus the n short query-embeds scoring makes each run.
func estimateEvalCostUSD(genM, embM ai.ModelInfo, n int) (total, gen, emb float64) {
	g := ai.EstimateCostWithSpec(genM, ai.ProbeBenchGen,
		ai.ProbeSpec{InputTokens: 1500 * n, OutputTokens: 80 * n, Requests: n})
	e := ai.EstimateCostWithSpec(embM, ai.ProbeBenchEmbed,
		ai.ProbeSpec{InputTokens: 20 * n, Requests: n})
	return g.USD + e.USD, g.USD, e.USD
}

// evalCostGate returns an error when the estimate exceeds the cap. Split out so
// the abort is unit-testable without a live catalog fetch.
func evalCostGate(total, cap float64) error {
	if total > cap {
		return fmt.Errorf("estimated cost $%.4f exceeds --cost-cap $%.2f; raise --cost-cap or lower --n", total, cap)
	}
	return nil
}
