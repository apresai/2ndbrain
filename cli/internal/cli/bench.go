package cli

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/apresai/2ndbrain/internal/ai"
	"github.com/apresai/2ndbrain/internal/bench"
	"github.com/apresai/2ndbrain/internal/output"
	"github.com/apresai/2ndbrain/internal/vault"
	"github.com/spf13/cobra"
)

var (
	benchModelFlag    string
	benchProbeFlag    string
	benchProviderFlag string
	benchSummaryScope string
	benchHistoryLimit int
)

var benchCmd = &cobra.Command{
	Use:   "bench",
	Short: "Benchmark AI models against your vault",
	Long:  "Runs embed, generate, retrieval, search, and RAG probes against favorited models. Results are stored in .2ndbrain/bench.db for historical comparison.",
	RunE:  runBench,
}

var benchFavCmd = &cobra.Command{
	Use:               "fav <model-id>",
	Short:             "Add a model to benchmark favorites",
	Args:              cobra.ExactArgs(1),
	ValidArgsFunction: completeModelIDs,
	RunE:              runBenchFav,
}

var benchUnfavCmd = &cobra.Command{
	Use:               "unfav <model-id>",
	Short:             "Remove a model from benchmark favorites",
	Args:              cobra.ExactArgs(1),
	ValidArgsFunction: completeModelIDs,
	RunE:              runBenchUnfav,
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
	benchCmd.Flags().StringVar(&benchProbeFlag, "probe", "", "Run only a specific probe: embed, generate, retrieval, search, rag")
	benchCmd.PersistentFlags().StringVar(&benchProviderFlag, "provider", "", "Provider override (auto-detected if omitted)")
	benchCmd.Flags().StringVar(&benchSummaryScope, "summary-scope", "global", "Where to save the per-model benchmark summary: global or vault. Run history (.2ndbrain/bench.db) is unaffected.")
	benchHistoryCmd.Flags().IntVar(&benchHistoryLimit, "limit", 20, "Number of runs to show")
	_ = benchCmd.RegisterFlagCompletionFunc("model", completeModelIDs)
	_ = benchCmd.RegisterFlagCompletionFunc("provider", completeProviders)
	_ = benchCmd.RegisterFlagCompletionFunc("probe", completeBenchProbes)
	_ = benchCmd.RegisterFlagCompletionFunc("summary-scope", completeCatalogScopes)

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

func openVaultBenchDB(v *vault.Vault) (*bench.DB, error) {
	db, err := openBenchDB(v.DotDir)
	if err != nil {
		return nil, err
	}
	cfg := v.Config.AI
	if err := db.BackfillMissingRoutes(func(provider, modelID string) (string, string) {
		tmp := cfg
		tmp.Provider = provider
		r := ai.ResolveMeasurementRoute(tmp, modelID, v.Root).Route
		return string(r.Plane), r.Region
	}); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func benchVaultDocCount(db *sql.DB) (int, error) {
	var docCount int
	if err := db.QueryRow("SELECT COUNT(*) FROM documents").Scan(&docCount); err != nil {
		return 0, err
	}
	return docCount, nil
}

func runBench(cmd *cobra.Command, args []string) error {
	// Before the vault and bench DB open: openVaultBenchDB backfills routes,
	// which WRITES bench.db, and a run this format cannot render should change
	// nothing.
	if err := refuseNonJSONStream(cmd, "models bench"); err != nil {
		return err
	}
	v, err := openVault()
	if err != nil {
		return err
	}
	defer v.Close()
	setupFileLogging(v)

	bdb, err := openVaultBenchDB(v)
	if err != nil {
		return err
	}
	defer bdb.Close()

	ctx := context.Background()
	cfg := v.Config.AI
	docCount, err := benchVaultDocCount(v.DB.Conn())
	if err != nil {
		return fmt.Errorf("count indexed documents: %w", err)
	}

	// Determine which models to bench.
	type target struct {
		provider, modelID, modelType string
		plane                        ai.Plane
		region                       string
	}
	var targets []target

	if benchModelFlag != "" {
		ref, err := parseModelRef(benchModelFlag, benchProviderFlag)
		if err != nil {
			return err
		}
		provider := ref.Provider
		if provider == "" {
			provider = ai.InferProvider(ref.ID)
		}
		modelType := ai.InferModelType(ref.ID)
		targets = append(targets, target{provider, ref.ID, modelType, ref.Plane, ref.Region})
	} else {
		favs, err := bdb.ListFavorites()
		if err != nil {
			return err
		}
		if len(favs) == 0 {
			// Fall back to active config.
			targets = append(targets,
				target{provider: cfg.Provider, modelID: cfg.EmbeddingModel, modelType: "embedding"},
				target{provider: cfg.Provider, modelID: cfg.GenerationModel, modelType: "generation"},
			)
		} else {
			for _, f := range favs {
				targets = append(targets, target{provider: f.Provider, modelID: f.ModelID, modelType: f.ModelType})
			}
		}
	}

	ts := time.Now().UTC().Format(time.RFC3339)
	jsonMode := getFormat(cmd) == output.FormatJSON
	var enc *json.Encoder
	if jsonMode {
		enc = json.NewEncoder(os.Stdout)
	}

	for _, t := range targets {
		if jsonMode {
			emitBenchEvent(enc, benchEvent{
				Event:    "model_start",
				ModelID:  t.modelID,
				Provider: t.provider,
				Type:     t.modelType,
				Message:  "benchmark started",
			})
		} else {
			fmt.Printf("\nBenchmarking %s (%s/%s)...\n", t.modelID, t.provider, t.modelType)
		}
		slog.Info("models bench start", "provider", t.provider, "model", t.modelID, "type", t.modelType, "probe", benchProbeFlag)

		opts := bench.ProbeOpts{
			Ctx:       ctx,
			AICfg:     cfg,
			Provider:  t.provider,
			ModelID:   t.modelID,
			ModelType: t.modelType,
			SearchDB:  v.DB.Conn(),
			VaultRoot: v.Root,
		}

		results, err := runBenchProbes(opts, benchProbeFlag, enc)
		if err != nil {
			return err
		}

		for _, r := range results {
			status := "PASS"
			if !r.OK {
				status = "FAIL"
			}
			if r.Skipped {
				status = "SKIP"
			}
			if !jsonMode {
				fmt.Printf("  %-10s %s  %dms", r.Probe, status, r.LatencyMs)
				if r.Detail != "" {
					detail := r.Detail
					if len(detail) > 80 {
						detail = detail[:80] + "..."
					}
					fmt.Printf("  %s", detail)
				}
				fmt.Println()
			}

			// Store result.
			runDocCount := docCount
			if r.VaultDocCount > 0 {
				runDocCount = r.VaultDocCount
			}
			plane, region := string(t.plane), t.region
			if plane == "" && region == "" {
				route := ai.ResolveMeasurementRoute(cfg, t.modelID, v.Root).Route
				plane, region = string(route.Plane), route.Region
			}
			if err := bdb.InsertRun(&bench.Run{
				Timestamp:     ts,
				Provider:      t.provider,
				ModelID:       t.modelID,
				Probe:         r.Probe,
				LatencyMs:     r.LatencyMs,
				OK:            r.OK,
				Detail:        r.Detail,
				VaultDocCount: runDocCount,
				Plane:         plane,
				Region:        region,
			}); err != nil {
				// A transient bench.db failure (WAL busy, disk full)
				// shouldn't abort the run and discard subsequent
				// models' summaries — log and continue.
				slog.Error("store benchmark run failed", "provider", t.provider, "model", t.modelID, "probe", r.Probe, "err", err)
			}
			slog.Info("models bench result", "provider", t.provider, "model", t.modelID, "probe", r.Probe, "ok", r.OK, "skipped", r.Skipped, "latency_ms", r.LatencyMs)
		}

		summary := benchmarkSummary(ts, docCount, results)
		summaryScope, summaryRoot, err := resolveBenchSummaryScope(v.Root, benchSummaryScope)
		if err != nil {
			return err
		}
		if err := saveBenchmarkSummary(cfg, summaryScope, summaryRoot, t.provider, t.modelID, t.modelType, summary); err != nil {
			return fmt.Errorf("save benchmark summary: %w", err)
		}
		if jsonMode {
			emitBenchEvent(enc, benchEvent{
				Event:     "summary",
				ModelID:   t.modelID,
				Provider:  t.provider,
				Type:      t.modelType,
				Benchmark: summary,
				Message:   "benchmark summary saved",
			})
		}
	}
	if jsonMode {
		emitBenchEvent(enc, benchEvent{Event: "done", Message: "benchmark complete"})
	} else {
		fmt.Println()
	}
	return nil
}

type benchEvent struct {
	Event     string               `json:"event"`
	ModelID   string               `json:"model_id,omitempty"`
	Provider  string               `json:"provider,omitempty"`
	Type      string               `json:"type,omitempty"`
	Probe     string               `json:"probe,omitempty"`
	Result    *bench.ProbeResult   `json:"result,omitempty"`
	Benchmark *ai.BenchmarkSummary `json:"benchmark,omitempty"`
	Message   string               `json:"message,omitempty"`
}

func emitBenchEvent(enc *json.Encoder, e benchEvent) {
	if enc != nil {
		_ = enc.Encode(e)
	}
}

func runBenchProbes(opts bench.ProbeOpts, only string, enc *json.Encoder) ([]bench.ProbeResult, error) {
	runOne := func(probe string, fn func(bench.ProbeOpts) bench.ProbeResult) bench.ProbeResult {
		emitBenchEvent(enc, benchEvent{
			Event:    "probe_start",
			ModelID:  opts.ModelID,
			Provider: opts.Provider,
			Type:     opts.ModelType,
			Probe:    probe,
			Message:  "probe started",
		})
		// Bench previously ran with NO wall clock at any level: a hung
		// provider parked the whole run forever. Each probe now gets its own
		// deadline derived from the transport ceiling (2x, because the rag
		// probe chains an embed, a search, and a generation). This bounds
		// hangs; a slow-but-working model is never failed by it.
		probeOpts := opts
		parent := opts.Ctx
		if parent == nil {
			parent = context.Background()
		}
		probeCtx, cancel := context.WithTimeout(parent, 2*ai.MaxProbeDeadline())
		probeOpts.Ctx = probeCtx
		result := fn(probeOpts)
		cancel()
		emitBenchEvent(enc, benchEvent{
			Event:    "probe_result",
			ModelID:  opts.ModelID,
			Provider: opts.Provider,
			Type:     opts.ModelType,
			Probe:    result.Probe,
			Result:   &result,
		})
		return result
	}

	if only != "" {
		switch only {
		case "embed":
			return []bench.ProbeResult{runOne("embed", bench.RunEmbed)}, nil
		case "generate":
			return []bench.ProbeResult{runOne("generate", bench.RunGenerate)}, nil
		case "retrieval":
			return []bench.ProbeResult{runOne("retrieval", bench.RunRetrievalQuality)}, nil
		case "search":
			return []bench.ProbeResult{runOne("search", bench.RunSearch)}, nil
		case "rag":
			return []bench.ProbeResult{runOne("rag", bench.RunRAG)}, nil
		default:
			return nil, fmt.Errorf("unknown probe %q (use: embed, generate, retrieval, search, rag)", only)
		}
	}

	var results []bench.ProbeResult
	if opts.ModelType == "embedding" {
		results = append(results, runOne("embed", bench.RunEmbed))
		results = append(results, runOne("retrieval", bench.RunRetrievalQuality))
		return results, nil
	}
	results = append(results, runOne("generate", bench.RunGenerate))
	results = append(results, runOne("search", bench.RunSearch))
	results = append(results, runOne("rag", bench.RunRAG))
	return results, nil
}

func benchmarkSummary(ts string, docCount int, results []bench.ProbeResult) *ai.BenchmarkSummary {
	var totalLatency int64
	var counted int64
	var quality float64
	for _, r := range results {
		if r.OK && !r.Skipped {
			totalLatency += r.LatencyMs
			counted++
		}
		if r.QualityScore > 0 {
			quality = r.QualityScore
		}
		if r.VaultDocCount > 0 {
			docCount = r.VaultDocCount
		}
	}
	if counted == 0 {
		for _, r := range results {
			totalLatency += r.LatencyMs
			counted++
		}
	}
	var avg int64
	if counted > 0 {
		avg = totalLatency / counted
	}
	return &ai.BenchmarkSummary{
		RanAt:         ts,
		AvgLatencyMs:  avg,
		QualityScore:  quality,
		VaultDocCount: docCount,
	}
}

// resolveBenchSummaryScope picks the user-catalog scope for `models bench`
// summary persistence. Unlike resolveCatalogScope it accepts the
// already-open vault root so we don't reopen the vault for a one-line save.
func resolveBenchSummaryScope(vaultRoot, scope string) (ai.UserCatalogScope, string, error) {
	switch ai.UserCatalogScope(scope) {
	case ai.ScopeGlobal:
		return ai.ScopeGlobal, "", nil
	case ai.ScopeVault:
		return ai.ScopeVault, vaultRoot, nil
	default:
		return "", "", fmt.Errorf("--summary-scope must be %q or %q, got %q", ai.ScopeGlobal, ai.ScopeVault, scope)
	}
}

func saveBenchmarkSummary(cfg ai.AIConfig, scope ai.UserCatalogScope, vaultRoot, provider, modelID, modelType string, summary *ai.BenchmarkSummary) error {
	// Record the summary against the route that was BENCHMARKED. The probe
	// picks its endpoint with ResolveMeasurementRoute, so looking the base row
	// up route-blind filed the number on a sibling: measured
	// @classic/us-west-2, recorded on @classic/us-east-1. models list --sort
	// best is bench-informed, so the wrong row then ranks.
	route := ai.ResolveMeasurementRoute(cfg, modelID, vaultRoot).Route
	// The base row is the RAW stored one for this route, the way calibrate
	// --save reads its base. findModelInfoForRoute returns a row from the
	// MERGED catalog, so every bench run copied the builtin's own facts into the
	// user file: with --summary-scope defaulting to global, one run in one vault
	// wrote the builtin similarity threshold into ~/.config/2nb/models.yaml and
	// every vault on the machine then reported it as a user calibration.
	entry, ok := ai.UserCatalogEntry(scope, vaultRoot, route)
	if !ok {
		entry = ai.ModelInfo{
			ID:       modelID,
			Provider: provider,
			Type:     modelType,
			Tier:     ai.TierUserVerified,
		}
	}
	entry.ID = modelID
	entry.Provider = provider
	entry.Type = modelType
	entry.Plane = route.Plane
	entry.Region = route.Region
	entry.Benchmark = summary
	entry.Enabled = preserveScopeEnabled(scope, vaultRoot, provider, modelID)
	if entry.Tier == "" {
		entry.Tier = ai.TierUserVerified
	}
	// RMW on the raw stored row for this route, then stamp Benchmark. A site
	// that constructed a fresh row here would erase the stored verdict, prices,
	// and enabled pointer; this is the same read calibrate --save does, so the
	// two agree about what "the existing row" means.
	preserveUserThreshold(scope, vaultRoot, &entry)
	if err := ai.SaveUserCatalogEntry(scope, vaultRoot, entry); err != nil {
		return err
	}
	slog.Info("models bench summary saved", "scope", string(scope), "vault_root", vaultRoot, "provider", provider, "model", modelID, "avg_latency_ms", summary.AvgLatencyMs, "quality_score", summary.QualityScore, "vault_doc_count", summary.VaultDocCount)
	return nil
}

func runBenchFav(cmd *cobra.Command, args []string) error {
	v, err := openVault()
	if err != nil {
		return err
	}
	defer v.Close()

	bdb, err := openVaultBenchDB(v)
	if err != nil {
		return err
	}
	defer bdb.Close()

	ref, err := parseModelRef(args[0], benchProviderFlag)
	if err != nil {
		return err
	}
	provider := ref.Provider
	if provider == "" {
		provider = ai.InferProvider(ref.ID)
	}
	modelType := ai.InferModelType(ref.ID)

	if err := bdb.AddFavorite(provider, ref.ID, modelType); err != nil {
		return err
	}
	// The confirmation went to STDOUT as prose for every format, so
	// `models bench fav <id> --json` printed a sentence where a record belongs.
	if done, err := emitStructured(cmd, BenchFavoriteResult{
		Action: cmd.Name(), Provider: provider, Model: ref.ID, Type: modelType,
	}); done {
		return err
	}
	fmt.Printf("Added %s/%s (%s) to bench favorites\n", provider, ref.ID, modelType)
	if ref.Plane != "" || ref.Region != "" {
		fmt.Fprintf(cmd.ErrOrStderr(), "(favorites are the model, every route)\n")
	}
	return nil
}

// BenchFavoriteResult is the structured record for `models bench fav` and
// `models bench unfav`. A favorite is the MODEL, every route, so it carries no
// plane or region.
type BenchFavoriteResult struct {
	Action   string `json:"action"`
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Type     string `json:"model_type,omitempty"`
}

func runBenchUnfav(cmd *cobra.Command, args []string) error {
	v, err := openVault()
	if err != nil {
		return err
	}
	defer v.Close()

	bdb, err := openVaultBenchDB(v)
	if err != nil {
		return err
	}
	defer bdb.Close()

	ref, err := parseModelRef(args[0], benchProviderFlag)
	if err != nil {
		return err
	}
	provider := ref.Provider
	if provider == "" {
		provider = ai.InferProvider(ref.ID)
	}

	if err := bdb.RemoveFavorite(provider, ref.ID); err != nil {
		return err
	}
	if done, err := emitStructured(cmd, BenchFavoriteResult{
		Action: cmd.Name(), Provider: provider, Model: ref.ID,
	}); done {
		return err
	}
	fmt.Printf("Removed %s/%s from bench favorites\n", provider, ref.ID)
	return nil
}

func runBenchFavs(cmd *cobra.Command, args []string) error {
	v, err := openVault()
	if err != nil {
		return err
	}
	defer v.Close()

	bdb, err := openVaultBenchDB(v)
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
		// jsonSafeList, so an empty favorites list is `[]` and not the bare
		// token `null`, which is what `jq '.[]'` needs and what every other
		// listing in this CLI already emits.
		return output.Write(os.Stdout, format, jsonSafeList(format, favs))
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

	bdb, err := openVaultBenchDB(v)
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

	bdb, err := openVaultBenchDB(v)
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
