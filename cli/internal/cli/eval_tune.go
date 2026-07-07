package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/apresai/2ndbrain/internal/ai"
	"github.com/apresai/2ndbrain/internal/eval"
	"github.com/spf13/cobra"
)

// TuneEntry is one swept configuration's scorecard.
type TuneEntry struct {
	Name         string  `json:"name"`
	Threshold    float64 `json:"threshold"`
	BM25Weight   float64 `json:"bm25_weight"`
	VectorWeight float64 `json:"vector_weight"`
	BM25Only     bool    `json:"bm25_only,omitempty"`
	RecallAtK    float64 `json:"recall_at_k"`
	RecallAt1    float64 `json:"recall_at_1"`
	MRRAtK       float64 `json:"mrr_at_k"`
}

// TuneReport is the `2nb eval tune --json` shape.
type TuneReport struct {
	N           int         `json:"n"`
	K           int         `json:"k"`
	Current     TuneEntry   `json:"current"`
	Configs     []TuneEntry `json:"configs"` // ranked best-first
	Best        TuneEntry   `json:"best"`
	Suggestion  []string    `json:"suggestion,omitempty"` // config set commands, empty when current is already best
	QACached    bool        `json:"qa_cached"`
	GeneratedAt string      `json:"generated_at"`
}

// tuneMRRMargin is the minimum MRR improvement over the current config before
// tune suggests a change: below this the difference is noise on a small QA
// set, and oscillating suggestions between runs erode trust.
const tuneMRRMargin = 0.01

var evalTuneCmd = &cobra.Command{
	Use:   "tune",
	Short: "Sweep threshold/weight combinations and suggest better settings",
	Long: `Score a grid of similarity-threshold and hybrid-weight combinations over the
same cached ground-truth QA set 2nb eval uses, rank them, and print the exact
config set commands for the winner when it beats your current config.

Near-zero cost: the sweep embeds each question once and scores every
combination locally against your stored embeddings. Suggest-only: nothing is
written; apply a suggestion with the printed commands. A small QA set can
overfit, so prefer --n 40 before acting on a marginal win.`,
	Example: `  2nb eval tune            # sweep and suggest
  2nb eval tune --n 40     # steadier read (regenerates only if uncached)
  2nb eval tune --json     # machine-readable TuneReport`,
	Args: cobra.NoArgs,
	RunE: runEvalTune,
}

func init() {
	evalCmd.AddCommand(evalTuneCmd)
}

// tuneGrid builds the sweep: the current config, a BM25-only baseline, and
// the threshold x weight grid (all under the production asymmetric query
// purpose). Deduplicates entries that coincide with the current config.
func tuneGrid(threshold, bm25W, vecW float64) []eval.SweepConfig {
	configs := []eval.SweepConfig{
		{Name: "current", QueryPurpose: ai.PurposeQuery, Threshold: threshold, BM25Weight: bm25W, VectorWeight: vecW},
		{Name: "bm25-only", QueryPurpose: ai.PurposeQuery, BM25Only: true},
	}
	thresholds := []float64{0.15, 0.20, 0.25, 0.30, 0.35}
	weights := [][2]float64{{1, 1}, {1.5, 1}, {1, 1.5}, {2, 1}, {1, 2}}
	for _, t := range thresholds {
		for _, w := range weights {
			if t == threshold && w[0] == bm25W && w[1] == vecW {
				continue // already covered by "current"
			}
			configs = append(configs, eval.SweepConfig{
				Name:         fmt.Sprintf("t%.2f b%.1f v%.1f", t, w[0], w[1]),
				QueryPurpose: ai.PurposeQuery,
				Threshold:    t,
				BM25Weight:   w[0],
				VectorWeight: w[1],
			})
		}
	}
	return configs
}

// tuneSuggestion returns the config set commands for best, or nil when the
// current config is already within the noise margin of the winner (or the
// winner is the BM25-only baseline, which is a diagnosis, not a setting).
func tuneSuggestion(best, current eval.ConfigMetrics) []string {
	if best.Config.BM25Only {
		return nil
	}
	if best.MRRAtK-current.MRRAtK <= tuneMRRMargin {
		return nil
	}
	return []string{
		fmt.Sprintf("2nb config set ai.similarity_threshold %g", best.Config.Threshold),
		fmt.Sprintf("2nb config set ai.bm25_weight %g", best.Config.BM25Weight),
		fmt.Sprintf("2nb config set ai.vector_weight %g", best.Config.VectorWeight),
	}
}

func tuneEntry(m eval.ConfigMetrics) TuneEntry {
	return TuneEntry{
		Name:         m.Config.Name,
		Threshold:    m.Config.Threshold,
		BM25Weight:   m.Config.BM25Weight,
		VectorWeight: m.Config.VectorWeight,
		BM25Only:     m.Config.BM25Only,
		RecallAtK:    m.RecallAtK,
		RecallAt1:    m.RecallAt1,
		MRRAtK:       m.MRRAtK,
	}
}

func runEvalTune(cmd *cobra.Command, args []string) error {
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

	threshold, _ := cfg.ResolveSimilarityThresholdFull(v.Root)
	bm25W, vecW := cfg.ResolveHybridWeights()
	configs := tuneGrid(threshold, bm25W, vecW)

	if !flagPorcelain {
		fmt.Fprintf(cmd.ErrOrStderr(), "Sweeping %d configurations over %d questions (one embed batch, scored locally)...\n", len(configs), len(qa))
	}
	metrics, _, err := eval.RunRetrievalSweep(ctx, v, embedder, qa, configs, 10)
	if err != nil {
		return fmt.Errorf("retrieval sweep: %w", err)
	}
	if len(metrics) == 0 {
		return fmt.Errorf("sweep produced no metrics")
	}

	// RunRetrievalSweep returns metrics ranked best-first.
	var current eval.ConfigMetrics
	for _, m := range metrics {
		if m.Config.Name == "current" {
			current = m
			break
		}
	}
	best := metrics[0]
	suggestion := tuneSuggestion(best, current)

	report := TuneReport{
		N:           len(qa),
		K:           best.K,
		Current:     tuneEntry(current),
		Best:        tuneEntry(best),
		Suggestion:  suggestion,
		QACached:    cached,
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
	}
	for _, m := range metrics {
		report.Configs = append(report.Configs, tuneEntry(m))
	}

	if format := getFormat(cmd); format != "" {
		return writeOut(cmd, format, report)
	}

	fmt.Printf("Retrieval tuning sweep — %d questions, %d configurations\n\n", len(qa), len(configs))
	fmt.Printf("%-18s %-10s %-6s %-6s %8s %8s %8s\n", "NAME", "THRESHOLD", "BM25", "VEC", "R@10", "R@1", "MRR@10")
	for i, m := range metrics {
		marker := " "
		if m.Config.Name == "current" {
			marker = "*"
		}
		name := m.Config.Name
		if i == 0 {
			name = name + " (best)"
		}
		fmt.Printf("%s %-18s %-10.2f %-6.1f %-6.1f %7.0f%% %7.0f%% %8.3f\n",
			marker, name, m.Config.Threshold, m.Config.BM25Weight, m.Config.VectorWeight,
			100*m.RecallAtK, 100*m.RecallAt1, m.MRRAtK)
	}
	fmt.Println()
	switch {
	case len(suggestion) > 0:
		fmt.Printf("Best config beats current by %.3f MRR. Apply it with:\n", best.MRRAtK-current.MRRAtK)
		for _, cmdLine := range suggestion {
			fmt.Printf("  %s\n", cmdLine)
		}
	case best.Config.BM25Only:
		fmt.Println("The BM25-only baseline won: the semantic channel is hurting ranking on this QA set. Check embeddings are current (`2nb ai status`) before changing weights.")
	default:
		fmt.Println("Your current config is already within noise of the best swept configuration. Nothing to change.")
	}
	if len(qa) < 20 {
		fmt.Printf("Caveat: only %d questions; small sets overfit. Prefer `--n 40 --regenerate` before acting on marginal wins.\n", len(qa))
	}
	return nil
}
