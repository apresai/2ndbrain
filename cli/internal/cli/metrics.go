package cli

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"time"

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

// MetricsReport is the full `2nb metrics --json` payload (the macOS app decodes
// this single object). last_build is null when no index run has been recorded.
type MetricsReport struct {
	LastBuild  *metrics.Operation           `json:"last_build"`
	Gauges     MetricsGauges                `json:"gauges"`
	Recent     []metrics.Operation          `json:"recent"`
	Aggregates map[string]metrics.Aggregate `json:"aggregates"`
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
	for i := range recent {
		recent[i] = recent[i].WithRates()
	}
	aggregates, err := mdb.Aggregates()
	if err != nil {
		return MetricsReport{}, err
	}
	return MetricsReport{
		LastBuild:  lastBuild,
		Gauges:     vaultGauges(v, lastBuild),
		Recent:     recent,
		Aggregates: aggregates,
	}, nil
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
	fmt.Fprintf(os.Stderr, "Cleared %d recorded operation(s).\n", n)
	return nil
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

	if len(r.Aggregates) > 0 {
		fmt.Println("\nPer-operation (recent window):")
		for _, op := range []string{metrics.OpIndex, metrics.OpReembed, metrics.OpIndexDoc, metrics.OpSearch, metrics.OpAsk} {
			a, ok := r.Aggregates[op]
			if !ok {
				continue
			}
			line := fmt.Sprintf("  %-10s n=%-4d avg=%-8s p50=%s", op, a.Count, metricsDurationF(a.AvgMs), metricsDuration(a.P50Ms))
			if a.AvgDocsPerSec > 0 {
				line += fmt.Sprintf("  (%.1f docs/sec)", a.AvgDocsPerSec)
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
