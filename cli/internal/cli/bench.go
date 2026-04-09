package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/apresai/2ndbrain/internal/ai"
	"github.com/apresai/2ndbrain/internal/bench"
	"github.com/apresai/2ndbrain/internal/output"
	"github.com/spf13/cobra"
)

var (
	benchModelFlag    string
	benchProbeFlag    string
	benchProviderFlag string
	benchHistoryLimit int
)

var benchCmd = &cobra.Command{
	Use:   "bench",
	Short: "Benchmark AI models against your vault",
	Long:  "Runs embed, generate, search, and RAG probes against favorited models. Results are stored in .2ndbrain/bench.db for historical comparison.",
	RunE:  runBench,
}

var benchFavCmd = &cobra.Command{
	Use:   "fav <model-id>",
	Short: "Add a model to benchmark favorites",
	Args:  cobra.ExactArgs(1),
	RunE:  runBenchFav,
}

var benchUnfavCmd = &cobra.Command{
	Use:   "unfav <model-id>",
	Short: "Remove a model from benchmark favorites",
	Args:  cobra.ExactArgs(1),
	RunE:  runBenchUnfav,
}

var benchFavsCmd = &cobra.Command{
	Use:   "favs",
	Short: "List benchmark favorites",
	RunE:  runBenchFavs,
}

var benchHistoryCmd = &cobra.Command{
	Use:   "history",
	Short: "Show past benchmark runs",
	RunE:  runBenchHistory,
}

var benchCompareCmd = &cobra.Command{
	Use:   "compare",
	Short: "Side-by-side comparison of latest benchmark runs per model",
	RunE:  runBenchCompare,
}

func init() {
	benchCmd.Flags().StringVar(&benchModelFlag, "model", "", "Bench a specific model instead of favorites")
	benchCmd.Flags().StringVar(&benchProbeFlag, "probe", "", "Run only a specific probe: embed, generate, search, rag")
	benchCmd.Flags().StringVar(&benchProviderFlag, "provider", "", "Provider override (auto-detected if omitted)")
	benchHistoryCmd.Flags().IntVar(&benchHistoryLimit, "limit", 20, "Number of runs to show")

	benchCmd.AddCommand(benchFavCmd)
	benchCmd.AddCommand(benchUnfavCmd)
	benchCmd.AddCommand(benchFavsCmd)
	benchCmd.AddCommand(benchHistoryCmd)
	benchCmd.AddCommand(benchCompareCmd)

	modelsCmd.AddCommand(benchCmd)
}

func openBenchDB(dotDir string) (*bench.DB, error) {
	return bench.Open(filepath.Join(dotDir, "bench.db"))
}

func runBench(cmd *cobra.Command, args []string) error {
	v, err := openVault()
	if err != nil {
		return err
	}
	defer v.Close()

	bdb, err := openBenchDB(v.DotDir)
	if err != nil {
		return err
	}
	defer bdb.Close()

	ctx := context.Background()
	cfg := v.Config.AI
	var docCount int
	v.DB.Conn().QueryRow("SELECT COUNT(*) FROM documents").Scan(&docCount)

	// Determine which models to bench.
	type target struct {
		provider, modelID, modelType string
	}
	var targets []target

	if benchModelFlag != "" {
		provider := benchProviderFlag
		if provider == "" {
			provider = ai.InferProvider(benchModelFlag)
		}
		modelType := ai.InferModelType(benchModelFlag)
		targets = append(targets, target{provider, benchModelFlag, modelType})
	} else {
		favs, err := bdb.ListFavorites()
		if err != nil {
			return err
		}
		if len(favs) == 0 {
			// Fall back to active config.
			targets = append(targets,
				target{cfg.Provider, cfg.EmbeddingModel, "embedding"},
				target{cfg.Provider, cfg.GenerationModel, "generation"},
			)
		} else {
			for _, f := range favs {
				targets = append(targets, target{f.Provider, f.ModelID, f.ModelType})
			}
		}
	}

	ts := time.Now().UTC().Format(time.RFC3339)

	for _, t := range targets {
		fmt.Printf("\nBenchmarking %s (%s/%s)...\n", t.modelID, t.provider, t.modelType)

		opts := bench.ProbeOpts{
			Ctx:       ctx,
			AICfg:     cfg,
			Provider:  t.provider,
			ModelID:   t.modelID,
			ModelType: t.modelType,
			SearchDB:  v.DB.Conn(),
			VaultRoot: v.Root,
		}

		var results []bench.ProbeResult
		if benchProbeFlag != "" {
			switch benchProbeFlag {
			case "embed":
				results = []bench.ProbeResult{bench.RunEmbed(opts)}
			case "generate":
				results = []bench.ProbeResult{bench.RunGenerate(opts)}
			case "search":
				results = []bench.ProbeResult{bench.RunSearch(opts)}
			case "rag":
				results = []bench.ProbeResult{bench.RunRAG(opts)}
			default:
				return fmt.Errorf("unknown probe %q (use: embed, generate, search, rag)", benchProbeFlag)
			}
		} else {
			results = bench.RunAll(opts)
		}

		for _, r := range results {
			status := "PASS"
			if !r.OK {
				status = "FAIL"
			}
			fmt.Printf("  %-10s %s  %dms", r.Probe, status, r.LatencyMs)
			if r.Detail != "" {
				detail := r.Detail
				if len(detail) > 80 {
					detail = detail[:80] + "..."
				}
				fmt.Printf("  %s", detail)
			}
			fmt.Println()

			// Store result.
			bdb.InsertRun(&bench.Run{
				Timestamp:     ts,
				Provider:      t.provider,
				ModelID:       t.modelID,
				Probe:         r.Probe,
				LatencyMs:     r.LatencyMs,
				OK:            r.OK,
				Detail:        r.Detail,
				VaultDocCount: docCount,
			})
		}
	}
	fmt.Println()
	return nil
}

func runBenchFav(cmd *cobra.Command, args []string) error {
	v, err := openVault()
	if err != nil {
		return err
	}
	defer v.Close()

	bdb, err := openBenchDB(v.DotDir)
	if err != nil {
		return err
	}
	defer bdb.Close()

	modelID := args[0]
	provider := benchProviderFlag
	if provider == "" {
		provider = ai.InferProvider(modelID)
	}
	modelType := ai.InferModelType(modelID)

	if err := bdb.AddFavorite(provider, modelID, modelType); err != nil {
		return err
	}
	fmt.Printf("Added %s/%s (%s) to bench favorites\n", provider, modelID, modelType)
	return nil
}

func runBenchUnfav(cmd *cobra.Command, args []string) error {
	v, err := openVault()
	if err != nil {
		return err
	}
	defer v.Close()

	bdb, err := openBenchDB(v.DotDir)
	if err != nil {
		return err
	}
	defer bdb.Close()

	modelID := args[0]
	provider := benchProviderFlag
	if provider == "" {
		provider = ai.InferProvider(modelID)
	}

	if err := bdb.RemoveFavorite(provider, modelID); err != nil {
		return err
	}
	fmt.Printf("Removed %s/%s from bench favorites\n", provider, modelID)
	return nil
}

func runBenchFavs(cmd *cobra.Command, args []string) error {
	v, err := openVault()
	if err != nil {
		return err
	}
	defer v.Close()

	bdb, err := openBenchDB(v.DotDir)
	if err != nil {
		return err
	}
	defer bdb.Close()

	favs, err := bdb.ListFavorites()
	if err != nil {
		return err
	}

	format := getFormat(cmd)
	if format != "" {
		return output.Write(os.Stdout, format, favs)
	}

	if len(favs) == 0 {
		fmt.Println("No favorites yet. Add one with: 2nb models bench fav <model-id>")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "PROVIDER\tMODEL\tTYPE\tADDED")
	for _, f := range favs {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", f.Provider, f.ModelID, f.ModelType, f.AddedAt[:10])
	}
	return w.Flush()
}

func runBenchHistory(cmd *cobra.Command, args []string) error {
	v, err := openVault()
	if err != nil {
		return err
	}
	defer v.Close()

	bdb, err := openBenchDB(v.DotDir)
	if err != nil {
		return err
	}
	defer bdb.Close()

	runs, err := bdb.ListRuns(benchHistoryLimit)
	if err != nil {
		return err
	}

	format := getFormat(cmd)
	if format != "" {
		return output.Write(os.Stdout, format, runs)
	}

	if len(runs) == 0 {
		fmt.Println("No benchmark runs yet. Run: 2nb models bench")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "TIMESTAMP\tPROVIDER\tMODEL\tPROBE\tLATENCY\tSTATUS\tDOCS")
	for _, r := range runs {
		status := "OK"
		if !r.OK {
			status = "FAIL"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%dms\t%s\t%d\n",
			r.Timestamp[:19], r.Provider, r.ModelID, r.Probe, r.LatencyMs, status, r.VaultDocCount)
	}
	return w.Flush()
}

func runBenchCompare(cmd *cobra.Command, args []string) error {
	v, err := openVault()
	if err != nil {
		return err
	}
	defer v.Close()

	bdb, err := openBenchDB(v.DotDir)
	if err != nil {
		return err
	}
	defer bdb.Close()

	runs, err := bdb.LatestRunsPerModel()
	if err != nil {
		return err
	}

	format := getFormat(cmd)
	if format != "" {
		return output.Write(os.Stdout, format, runs)
	}

	if len(runs) == 0 {
		fmt.Println("No benchmark runs yet. Run: 2nb models bench")
		return nil
	}

	// Group by probe type.
	groups := map[string][]bench.Run{}
	probeOrder := []string{"embed", "generate", "search", "rag"}
	for _, r := range runs {
		groups[r.Probe] = append(groups[r.Probe], r)
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	for _, probe := range probeOrder {
		probeRuns, ok := groups[probe]
		if !ok {
			continue
		}
		label := probe
		if probe == "rag" {
			label = "rag (end-to-end)"
		}
		fmt.Fprintf(w, "\n%s\n", strings.ToUpper(label))
		fmt.Fprintln(w, "MODEL\tPROVIDER\tLATENCY\tSTATUS")
		for _, r := range probeRuns {
			status := "OK"
			if !r.OK {
				status = "FAIL"
			}
			detail := ""
			if r.Detail != "" {
				d := r.Detail
				if len(d) > 60 {
					d = d[:60] + "..."
				}
				detail = " (" + d + ")"
			}
			fmt.Fprintf(w, "%s\t%s\t%dms\t%s%s\n", r.ModelID, r.Provider, r.LatencyMs, status, detail)
		}
	}
	return w.Flush()
}
