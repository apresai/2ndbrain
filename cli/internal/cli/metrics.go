package cli

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/apresai/2ndbrain/internal/ai"
	"github.com/apresai/2ndbrain/internal/metrics"
	"github.com/apresai/2ndbrain/internal/vault"
	"github.com/spf13/cobra"
)

// metricsStaleDays is the gauge cutoff for "stale" documents; matches the
// default of `2nb stale`.
const metricsStaleDays = 90

var metricsRecentLimit int

var metricsCmd = &cobra.Command{
	Use:   "metrics",
	Short: "Vault performance observatory: index timing, throughput, and recent operations",
	Long: `Show local performance metrics recorded in .2ndbrain/metrics.db: the last
index build (duration, docs/sec), live vault gauges (counts, embedding coverage,
index size), recent operations, and per-operation aggregates.

Metrics are recorded automatically as a side effect of index/search/ask; with no
subcommand this is the same as 'metrics show'.`,
	RunE: runMetricsShow,
}

var metricsShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Show the latest metrics (default)",
	RunE:  runMetricsShow,
}

var metricsClearCmd = &cobra.Command{
	Use:   "clear",
	Short: "Delete all recorded metrics history for this vault",
	RunE:  runMetricsClear,
}

func init() {
	metricsCmd.GroupID = "quality"
	metricsCmd.Flags().IntVar(&metricsRecentLimit, "limit", 20, "Number of recent operations to include")
	metricsShowCmd.Flags().IntVar(&metricsRecentLimit, "limit", 20, "Number of recent operations to include")
	metricsCmd.AddCommand(metricsShowCmd, metricsClearCmd)
	rootCmd.AddCommand(metricsCmd)
}

// MetricsGauges is the current-state snapshot shown beside the timing history.
type MetricsGauges struct {
	DocCount          int     `json:"doc_count"`
	EmbeddedCount     int     `json:"embedded_count"`
	EmbeddingCoverage float64 `json:"embedding_coverage"` // 0..1
	ChunkCount        int     `json:"chunk_count"`
	StaleCount        int     `json:"stale_count"`
	IndexDBBytes      int64   `json:"index_db_bytes"`
	WALBytes          int64   `json:"wal_bytes"`
	LastIndexAt       string  `json:"last_index_at,omitempty"`
	EmbeddingModel    string  `json:"embedding_model,omitempty"`
	EmbeddingDims     int     `json:"embedding_dims,omitempty"`
}

// AIUsageWindowSummary aggregates AI calls, tokens, and cloud spend over a
// rolling UTC window (today, 7d, 30d, or all-time).
type AIUsageWindowSummary struct {
	CloudCostUSD      float64 `json:"cloud_cost_usd"`
	KnownPricing      bool    `json:"known_pricing"`
	CloudCalls        int     `json:"cloud_calls"`
	LocalCalls        int     `json:"local_calls"`
	CloudInputTokens  int     `json:"cloud_input_tokens"`
	CloudOutputTokens int     `json:"cloud_output_tokens"`
	LocalTokens       int     `json:"local_tokens"`
	UnpricedCalls     int     `json:"unpriced_calls"`
}

// AIModelUsageRow is a lifetime rollup for one (provider, model, slot) pair.
type AIModelUsageRow struct {
	Provider         string  `json:"provider"`
	Model            string  `json:"model"`
	Slot             string  `json:"slot"`
	IsCloud          bool    `json:"is_cloud"`
	Calls            int     `json:"calls"`
	InputTokens      int     `json:"input_tokens"`
	OutputTokens     int     `json:"output_tokens"`
	EstimatedCostUSD float64 `json:"estimated_cost_usd"`
	KnownPricing     bool    `json:"known_pricing"`
	PriceLabel       string  `json:"price_label"`
}

// AIUsageSummary holds windowed spend totals and per-model breakdowns from the
// unpruned ai_usage_ledger table.
type AIUsageSummary struct {
	Today   AIUsageWindowSummary `json:"today"`
	Last7d  AIUsageWindowSummary `json:"last_7d"`
	Last30d AIUsageWindowSummary `json:"last_30d"`
	AllTime AIUsageWindowSummary `json:"all_time"`
	ByModel []AIModelUsageRow    `json:"by_model"`
}

// ReembedEstimate projects full-vault re-embedding volume, runtime (from
// metrics.db history or calibrated baselines), and cloud USD cost from index.db.
type ReembedEstimate struct {
	EmbeddableDocs   int     `json:"embeddable_docs"`
	TotalChunks      int     `json:"total_chunks"`
	TotalChars       int     `json:"total_chars"`
	EstimatedTokens  int     `json:"estimated_tokens"`
	EstimatedSeconds float64 `json:"estimated_seconds"`
	SpeedSource      string  `json:"speed_source"` // history | baseline | none
	DocsPerSec       float64 `json:"docs_per_sec"`
	Provider         string  `json:"provider"`
	Model            string  `json:"model"`
	IsCloud          bool    `json:"is_cloud"`
	EstimatedCostUSD float64 `json:"estimated_cost_usd"`
	KnownPricing     bool    `json:"known_pricing"`
}

// MetricsReport is the full `2nb metrics --json` payload (the macOS app decodes
// this single object). last_build is null when no index run has been recorded.
type MetricsReport struct {
	LastBuild       *metrics.Operation           `json:"last_build"`
	Gauges          MetricsGauges                `json:"gauges"`
	Recent          []metrics.Operation          `json:"recent"`
	Aggregates      map[string]metrics.Aggregate `json:"aggregates"`
	TokensIn        int                          `json:"total_input_tokens"`
	TokensOut       int                          `json:"total_output_tokens"`
	AIUsage         *AIUsageSummary              `json:"ai_usage"`
	ReembedEstimate *ReembedEstimate             `json:"reembed_estimate"`
}

func openMetricsDB(v *vault.Vault) (*metrics.DB, error) {
	return metrics.Open(filepath.Join(v.DotDir, "metrics.db"))
}

func buildMetricsReport(v *vault.Vault, limit int) (MetricsReport, error) {
	mdb, err := openMetricsDB(v)
	if err != nil {
		return MetricsReport{}, err
	}
	defer mdb.Close()

	lastBuild, err := mdb.LastByOp(metrics.OpIndex, metrics.OpReembed)
	if err != nil {
		return MetricsReport{}, err
	}
	recent, err := mdb.Recent(limit)
	if err != nil {
		return MetricsReport{}, err
	}
	// Emit `[]`, not `null`, for an empty vault: Recent returns a nil slice when
	// there are no rows, and a nil Go slice marshals to JSON null, which a typed
	// consumer (the macOS app's non-optional array) can't decode. Aggregates is
	// already a non-nil map, so it always renders `{}`.
	if recent == nil {
		recent = []metrics.Operation{}
	}
	for i := range recent {
		recent[i] = recent[i].WithRates()
	}
	aggregates, err := mdb.Aggregates()
	if err != nil {
		return MetricsReport{}, err
	}
	var tokIn, tokOut int
	for _, a := range aggregates {
		tokIn += a.TokensIn
		tokOut += a.TokensOut
	}
	aiUsage := buildAIUsageSummary(v, mdb)
	reembedEst := buildReembedEstimate(v, mdb)
	return MetricsReport{
		LastBuild:       lastBuild,
		Gauges:          vaultGauges(v, lastBuild),
		Recent:          recent,
		Aggregates:      aggregates,
		TokensIn:        tokIn,
		TokensOut:       tokOut,
		AIUsage:         &aiUsage,
		ReembedEstimate: &reembedEst,
	}, nil
}

func buildAIUsageSummary(v *vault.Vault, mdb *metrics.DB) AIUsageSummary {
	summary := AIUsageSummary{
		Today:   AIUsageWindowSummary{KnownPricing: true},
		Last7d:  AIUsageWindowSummary{KnownPricing: true},
		Last30d: AIUsageWindowSummary{KnownPricing: true},
		AllTime: AIUsageWindowSummary{KnownPricing: true},
		ByModel: []AIModelUsageRow{},
	}
	rows, err := mdb.UsageEntries("")
	if err != nil {
		return summary
	}

	now := time.Now().UTC()
	todayStr := now.Format("2006-01-02")
	d7Str := now.AddDate(0, 0, -6).Format("2006-01-02")
	d30Str := now.AddDate(0, 0, -29).Format("2006-01-02")

	type modelKey struct {
		provider string
		model    string
		slot     string
	}
	modelAcc := make(map[modelKey]*AIModelUsageRow)
	priceCache := make(map[modelKey]ai.ModelInfo)

	lookupInfo := func(provider, model, slot string) ai.ModelInfo {
		k := modelKey{provider: provider, model: model, slot: slot}
		if info, ok := priceCache[k]; ok {
			return info
		}
		var root string
		if v != nil {
			root = v.Root
		}
		info := ai.LookupLocalModelPricing(root, provider, model)
		priceCache[k] = info
		return info
	}

	addWindow := func(w *AIUsageWindowSummary, isCloud bool, calls, inTok, outTok int, costUSD float64, unpricedCalls int) {
		if isCloud {
			w.CloudCalls += calls
			w.CloudInputTokens += inTok
			w.CloudOutputTokens += outTok
			w.CloudCostUSD += costUSD
			if unpricedCalls > 0 {
				w.KnownPricing = false
				w.UnpricedCalls += unpricedCalls
			}
		} else {
			w.LocalCalls += calls
			w.LocalTokens += inTok + outTok
		}
	}

	for _, r := range rows {
		info := lookupInfo(r.Provider, r.Model, r.Slot)
		isCloud := r.IsCloud
		rowCost := r.CostUSD
		unpriced := r.UnpricedCalls
		if isCloud && unpriced > 0 && rowCost == 0 && ai.HasKnownPricing(info) {
			if computed, _, ok := ai.EstimateUsageCost(info, r.InputTokens, r.OutputTokens, r.Calls); ok {
				rowCost = computed
				unpriced = 0
			}
		}

		addWindow(&summary.AllTime, isCloud, r.Calls, r.InputTokens, r.OutputTokens, rowCost, unpriced)
		if r.Day >= d30Str {
			addWindow(&summary.Last30d, isCloud, r.Calls, r.InputTokens, r.OutputTokens, rowCost, unpriced)
		}
		if r.Day >= d7Str {
			addWindow(&summary.Last7d, isCloud, r.Calls, r.InputTokens, r.OutputTokens, rowCost, unpriced)
		}
		if r.Day == todayStr {
			addWindow(&summary.Today, isCloud, r.Calls, r.InputTokens, r.OutputTokens, rowCost, unpriced)
		}

		k := modelKey{provider: r.Provider, model: r.Model, slot: r.Slot}
		entry, ok := modelAcc[k]
		if !ok {
			entry = &AIModelUsageRow{
				Provider:     r.Provider,
				Model:        r.Model,
				Slot:         r.Slot,
				IsCloud:      isCloud,
				KnownPricing: true,
				PriceLabel:   ai.VerbosePriceLabel(info),
			}
			modelAcc[k] = entry
		}
		entry.Calls += r.Calls
		entry.InputTokens += r.InputTokens
		entry.OutputTokens += r.OutputTokens
		entry.EstimatedCostUSD += rowCost
		if isCloud && unpriced > 0 {
			entry.KnownPricing = false
		}
	}

	for _, entry := range modelAcc {
		summary.ByModel = append(summary.ByModel, *entry)
	}
	sort.Slice(summary.ByModel, func(i, j int) bool {
		a, b := summary.ByModel[i], summary.ByModel[j]
		if a.EstimatedCostUSD != b.EstimatedCostUSD {
			return a.EstimatedCostUSD > b.EstimatedCostUSD
		}
		totA := a.InputTokens + a.OutputTokens
		totB := b.InputTokens + b.OutputTokens
		if totA != totB {
			return totA > totB
		}
		if a.Provider != b.Provider {
			return a.Provider < b.Provider
		}
		if a.Slot != b.Slot {
			return a.Slot < b.Slot
		}
		return a.Model < b.Model
	})
	return summary
}

func buildReembedEstimate(v *vault.Vault, mdb *metrics.DB) ReembedEstimate {
	cfg := v.Config.AI
	embedProvider := cfg.EffectiveEmbeddingProvider()
	model := cfg.EmbeddingModel
	embeddableDocs, totalChunks, totalChars, _ := v.DB.VaultEmbedCorpusSizing()
	histDPS, histCPS, _ := mdb.HistoricalEmbedThroughput(embedProvider, model)
	concurrency := cfg.ResolveEmbedConcurrency(embedProvider)
	estSec, speedSource := vault.EstimateInitialEmbedSeconds(embeddableDocs, totalChars, embedProvider, concurrency, histDPS, histCPS)
	var docsPerSec float64
	if estSec > 0 && embeddableDocs > 0 {
		docsPerSec = math.Round((float64(embeddableDocs)/estSec)*100) / 100
	}
	estTokens := totalChars / 4
	if totalChars > 0 && estTokens == 0 {
		estTokens = 1
	}
	modelInfo := ai.LookupLocalModelPricing(v.Root, embedProvider, model)
	costUSD, isCloud, knownPricing := ai.EstimateUsageCost(modelInfo, estTokens, 0, embeddableDocs)
	return ReembedEstimate{
		EmbeddableDocs:   embeddableDocs,
		TotalChunks:      totalChunks,
		TotalChars:       totalChars,
		EstimatedTokens:  estTokens,
		EstimatedSeconds: math.Round(estSec*100) / 100,
		SpeedSource:      speedSource,
		DocsPerSec:       docsPerSec,
		Provider:         embedProvider,
		Model:            model,
		IsCloud:          isCloud,
		EstimatedCostUSD: costUSD,
		KnownPricing:     knownPricing,
	}
}

// vaultGauges reads current-state counts directly from the index. Each read is
// independently best-effort so one failure doesn't blank the whole panel.
func vaultGauges(v *vault.Vault, lastBuild *metrics.Operation) MetricsGauges {
	g := MetricsGauges{
		EmbeddingModel: v.Config.AI.EmbeddingModel,
		EmbeddingDims:  v.Config.AI.Dimensions,
	}
	if total, embedded, _, err := v.DB.EmbeddingCounts(); err == nil {
		g.DocCount = total
		g.EmbeddedCount = embedded
		if total > 0 {
			g.EmbeddingCoverage = math.Round(float64(embedded)/float64(total)*1000) / 1000
		}
	}
	_ = v.DB.Conn().QueryRow(`SELECT COUNT(*) FROM chunks`).Scan(&g.ChunkCount)
	cutoff := time.Now().AddDate(0, 0, -metricsStaleDays).Format(time.RFC3339)
	_ = v.DB.Conn().QueryRow(
		`SELECT COUNT(*) FROM documents WHERE modified_at < ? AND modified_at != ''`, cutoff,
	).Scan(&g.StaleCount)
	if dim, err := v.DB.SampleEmbeddingDim(); err == nil && dim > 0 {
		g.EmbeddingDims = dim
	}
	g.IndexDBBytes = fileBytes(v.DB.Path())
	g.WALBytes = fileBytes(v.DB.WALPath())
	if lastBuild != nil {
		g.LastIndexAt = lastBuild.Timestamp
	}
	return g
}

func runMetricsShow(cmd *cobra.Command, args []string) error {
	v, err := openVault()
	if err != nil {
		return err
	}
	defer v.Close()

	report, err := buildMetricsReport(v, metricsRecentLimit)
	if err != nil {
		return err
	}

	if format := getFormat(cmd); format != "" {
		return writeOut(cmd, format, report)
	}
	printMetricsReport(report)
	return nil
}

func runMetricsClear(cmd *cobra.Command, args []string) error {
	v, err := openVault()
	if err != nil {
		return err
	}
	defer v.Close()

	mdb, err := openMetricsDB(v)
	if err != nil {
		return err
	}
	defer mdb.Close()

	n, err := mdb.Clear()
	if err != nil {
		return err
	}
	// A WRITE, so it emits the same shape every other write emits: what
	// changed, and the count. It wrote ZERO BYTES to stdout for every format,
	// so `metrics clear --json` reported success with nothing to parse even
	// when it had just cleared a non-empty history.
	if done, err := emitStructured(cmd, MetricsClearResult{Cleared: n}); done {
		return err
	}
	fmt.Fprintf(os.Stderr, "Cleared %d recorded operation(s).\n", n)
	return nil
}

// MetricsClearResult is the structured record for `metrics clear`.
type MetricsClearResult struct {
	Cleared int64 `json:"cleared"`
}

func printMetricsReport(r MetricsReport) {
	if r.LastBuild != nil {
		b := *r.LastBuild
		fmt.Printf("Last %s build — %s\n", b.Operation, b.Timestamp)
		fmt.Printf("  duration:    %s\n", metricsDuration(b.DurationMs))
		if b.DocsPerSec > 0 {
			line := fmt.Sprintf("  throughput:  %.1f docs/sec", b.DocsPerSec)
			if b.EmbeddingsPerSec > 0 {
				line += fmt.Sprintf(", %.1f embeddings/sec", b.EmbeddingsPerSec)
			}
			fmt.Println(line)
		}
		fmt.Printf("  indexed:     %d docs, %d chunks, %d links\n", b.DocsIndexed, b.ChunksCreated, b.LinksFound)
		if b.Embedded > 0 || b.EmbedFailed > 0 {
			fmt.Printf("  embedded:    %d (%d failed, %d skipped)", b.Embedded, b.EmbedFailed, b.EmbedSkipped)
			if b.EmbeddingModel != "" {
				fmt.Printf(" via %s", b.EmbeddingModel)
			}
			fmt.Println()
		}
		if b.EmbedRetries > 0 {
			// The number that separates "the model is slow" from "the account
			// is throttled"; wall time alone cannot tell them apart.
			fmt.Printf("  retries:     %d provider retries\n", b.EmbedRetries)
		}
		if !b.OK {
			fmt.Printf("  status:      FAILED — %s\n", b.Error)
		}
	} else {
		fmt.Println("No index runs recorded yet. Run `2nb index` to build the vault.")
	}

	g := r.Gauges
	fmt.Println("\nVault gauges:")
	fmt.Printf("  documents:   %d  (%d embedded, %.0f%% coverage)\n", g.DocCount, g.EmbeddedCount, g.EmbeddingCoverage*100)
	fmt.Printf("  chunks:      %d\n", g.ChunkCount)
	fmt.Printf("  stale (%dd+): %d\n", metricsStaleDays, g.StaleCount)
	fmt.Printf("  index.db:    %s  (+%s WAL)\n", humanBytes(g.IndexDBBytes), humanBytes(g.WALBytes))
	if g.EmbeddingModel != "" {
		fmt.Printf("  embedding:   %s  (%d dims)\n", g.EmbeddingModel, g.EmbeddingDims)
	}
	if r.TokensIn > 0 || r.TokensOut > 0 {
		fmt.Printf("  tokens:      %d in / %d out  (recent window; ask actual, embed/search estimated)\n", r.TokensIn, r.TokensOut)
	}

	if re := r.ReembedEstimate; re != nil && re.EmbeddableDocs > 0 {
		costStr := "free (on-device)"
		if re.IsCloud {
			if re.KnownPricing {
				costStr = fmt.Sprintf("~%s", ai.FormatUSD(re.EstimatedCostUSD))
			} else {
				costStr = "unknown pricing"
			}
		}
		fmt.Printf("\nFull re-embed estimate (%s · %s):\n", re.Provider, re.Model)
		fmt.Printf("  scope:       %d docs, %d chunks (~%s tokens)\n",
			re.EmbeddableDocs, re.TotalChunks, formatTokenEstimate(re.EstimatedTokens))
		fmt.Printf("  eta & cost:  ~%s (%.1f docs/s via %s) · %s\n",
			formatETADuration(re.EstimatedSeconds), re.DocsPerSec, re.SpeedSource, costStr)
	}

	if u := r.AIUsage; u != nil && (len(u.ByModel) > 0 || u.AllTime.CloudCalls > 0 || u.AllTime.LocalCalls > 0) {
		fmt.Println("\nAI usage & estimated cloud cost:")
		fmt.Printf("  today:       %s cloud (%d cloud / %d local calls)\n",
			ai.FormatUSD(u.Today.CloudCostUSD), u.Today.CloudCalls, u.Today.LocalCalls)
		fmt.Printf("  last 30d:    %s cloud (%s in / %s out cloud tok, %s local tok)\n",
			ai.FormatUSD(u.Last30d.CloudCostUSD),
			formatTokenEstimate(u.Last30d.CloudInputTokens),
			formatTokenEstimate(u.Last30d.CloudOutputTokens),
			formatTokenEstimate(u.Last30d.LocalTokens))
		fmt.Printf("  all-time:    %s cloud (%d calls total)\n",
			ai.FormatUSD(u.AllTime.CloudCostUSD), u.AllTime.CloudCalls+u.AllTime.LocalCalls)
		for _, m := range u.ByModel {
			costStr := "free (on-device)"
			if m.IsCloud {
				if m.KnownPricing {
					costStr = ai.FormatUSD(m.EstimatedCostUSD)
				} else {
					costStr = "unknown"
				}
			}
			fmt.Printf("    %-11s %-26s %-10s %4d calls  %7s tok  %s\n",
				m.Provider, m.Model, m.Slot, m.Calls,
				formatTokenEstimate(m.InputTokens+m.OutputTokens), costStr)
		}
	}

	if len(r.Aggregates) > 0 {
		fmt.Println("\nPer-operation (recent window):")
		for _, op := range []string{metrics.OpIndex, metrics.OpReembed, metrics.OpIndexDoc, metrics.OpSearch, metrics.OpAsk, metrics.OpChat, metrics.OpPolish, metrics.OpFix, metrics.OpSuggest, metrics.OpProbe} {
			a, ok := r.Aggregates[op]
			if !ok {
				continue
			}
			line := fmt.Sprintf("  %-10s n=%-4d avg=%-8s p50=%s", op, a.Count, metricsDurationF(a.AvgMs), metricsDuration(a.P50Ms))
			if a.AvgDocsPerSec > 0 {
				line += fmt.Sprintf("  (%.1f docs/sec)", a.AvgDocsPerSec)
			}
			if a.TokensIn > 0 || a.TokensOut > 0 {
				line += fmt.Sprintf("  %d/%d tok", a.TokensIn, a.TokensOut)
			}
			if a.EmbedRetries > 0 {
				line += fmt.Sprintf("  %d retries", a.EmbedRetries)
			}
			fmt.Println(line)
		}
	}

	if len(r.Recent) > 0 {
		fmt.Printf("\nRecent operations (%d):\n", len(r.Recent))
		for _, o := range r.Recent {
			status := "ok"
			if !o.OK {
				status = "ERR"
			}
			var detail string
			switch o.Operation {
			case metrics.OpSearch, metrics.OpAsk:
				detail = fmt.Sprintf("%d results", o.ResultCount)
			default:
				detail = fmt.Sprintf("%d docs", o.DocsIndexed)
			}
			fmt.Printf("  %s  %-10s %-9s %-12s %s\n", o.Timestamp, o.Operation, metricsDuration(o.DurationMs), detail, status)
		}
	}
}

// metricsDuration renders a millisecond count as a compact human string.
func metricsDuration(ms int64) string { return metricsDurationF(float64(ms)) }

func metricsDurationF(ms float64) string {
	switch {
	case ms < 1000:
		return fmt.Sprintf("%dms", int64(math.Round(ms)))
	case ms < 60000:
		return fmt.Sprintf("%.1fs", ms/1000)
	default:
		m := int(ms) / 60000
		s := (int(ms) % 60000) / 1000
		return fmt.Sprintf("%dm%02ds", m, s)
	}
}

// embedRetryAdvice returns the one-line concurrency advice, or "" when there is
// nothing to say. It fires only when the LAST build actually rode out retries
// AND the user has raised ai.embed_concurrency above the per-provider automatic
// value: a throttled account at the automatic setting is the provider's quota,
// not a setting the user chose, and telling them to lower a number they never
// raised is noise.
//
// Best-effort throughout: the observatory is telemetry, and a missing or
// unreadable metrics.db means no advice, never an error.
func embedRetryAdvice(v *vault.Vault) string {
	cfg := v.Config.AI
	configured := cfg.EmbedConcurrency
	automatic := ai.ProviderEmbedConcurrencyDefault(cfg.EffectiveEmbeddingProvider())
	if configured <= automatic {
		return ""
	}
	mdb, err := openMetricsDB(v)
	if err != nil {
		return ""
	}
	defer mdb.Close()
	last, err := mdb.LastByOp(metrics.OpIndex, metrics.OpReembed)
	if err != nil || last == nil || last.EmbedRetries == 0 {
		return ""
	}
	return fmt.Sprintf(
		"%d throttled retries in the last index (embed_concurrency is %d, automatic is %d); consider lowering ai.embed_concurrency",
		last.EmbedRetries, configured, automatic)
}
