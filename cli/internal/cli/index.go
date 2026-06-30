package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/apresai/2ndbrain/internal/ai"
	"github.com/apresai/2ndbrain/internal/document"
	"github.com/apresai/2ndbrain/internal/embed"
	"github.com/apresai/2ndbrain/internal/metrics"
	"github.com/apresai/2ndbrain/internal/output"
	"github.com/apresai/2ndbrain/internal/vault"
	"github.com/spf13/cobra"
)

var indexCmd = &cobra.Command{
	Use:   "index",
	Short: "Build or rebuild the vault search index",
	Long:  "Builds the BM25 keyword index and, if AI is configured, the embedding index for semantic search. Safe to run repeatedly — only changed documents are re-embedded.",
	Example: `  2nb index                              # build / update the whole vault
  2nb index --doc my-note.md             # re-index one file (editors use this on save)
  2nb index --force-reembed              # invalidate all embeddings (use after switching providers)`,
	RunE: runIndex,
}

var (
	indexDocFlag      string
	indexForceReembed bool
)

func init() {
	indexCmd.GroupID = "ai"
	indexCmd.Flags().StringVar(&indexDocFlag, "doc", "", "Re-index a single document (relative or absolute path) instead of the whole vault")
	indexCmd.Flags().BoolVar(&indexForceReembed, "force-reembed", false, "Re-embed every document (use after intentionally switching AI providers)")
	_ = indexCmd.RegisterFlagCompletionFunc("doc", completeDocPaths)
	rootCmd.AddCommand(indexCmd)
}

// IndexDocResult is the JSON summary returned by `2nb index --doc <path>`.
// Editors use this to know whether a save triggered real re-embedding work.
type IndexDocResult struct {
	Path       string `json:"path"`
	Embedded   bool   `json:"embedded"`
	DurationMs int64  `json:"duration_ms"`
}

type embeddingRunStats struct {
	Attempted int
	Embedded  int
	Failed    int
	// Skipped counts documents with no embeddable text (empty or
	// whitespace/comment-only bodies). These are not failures — there is
	// simply nothing to embed — and embedding providers like Amazon Nova-2
	// reject empty input (minLength: 1), so we never send them.
	Skipped int
	// Throughput inputs for the metrics observatory, set by
	// embedDocumentsWithProvider: the embed-phase wall time, the total body
	// chars embedded, and the model used. Zero when nothing needed embedding.
	DurationMs int64
	TotalChars int
	Model      string
}

// indexOperation builds the metrics-observatory row for a full index run (or a
// reembed when force is set). For a reembed the index counts reflect the full
// index that runs before re-embedding, since `index --force-reembed` does both.
func indexOperation(force bool, start time.Time, ix vault.IndexStats, es embeddingRunStats, cfg ai.AIConfig, opErr error) metrics.Operation {
	op := metrics.Operation{
		Operation:      metrics.OpIndex,
		DurationMs:     time.Since(start).Milliseconds(),
		OK:             opErr == nil,
		Error:          errString(opErr),
		FilesScanned:   ix.FilesScanned,
		DocsIndexed:    ix.DocsIndexed,
		ChunksCreated:  ix.ChunksCreated,
		LinksFound:     ix.LinksFound,
		Embedded:       es.Embedded,
		EmbedSkipped:   es.Skipped,
		EmbedFailed:    es.Failed,
		EmbedMs:        es.DurationMs,
		TotalChars:     es.TotalChars,
		EmbeddingModel: es.Model,
		EmbeddingDims:  cfg.Dimensions,
		// Embeddings have no provider-reported usage; estimate input tokens at
		// chars/4 (no output tokens). Same heuristic as the cost estimate.
		InputTokens: es.TotalChars / 4,
	}
	if force {
		op.Operation = metrics.OpReembed
	}
	return op
}

func runIndex(cmd *cobra.Command, args []string) error {
	v, err := openVaultAndSetActive()
	if err != nil {
		return err
	}
	defer v.Close()
	setupFileLogging(v)

	if indexDocFlag != "" {
		return runIndexSingleDoc(cmd, v, indexDocFlag)
	}

	startTime := time.Now()
	slog.Info("index started", "vault", v.Root)

	if !flagPorcelain {
		fmt.Fprintln(os.Stderr, "Indexing vault...")
	}

	stats, err := vault.IndexVault(v, func(path string) {
		slog.Debug("indexed file", "path", path)
		if !flagPorcelain {
			fmt.Fprintf(os.Stderr, "  %s\n", path)
		}
	})
	if err != nil {
		// Record the failed build (best-effort) before bailing so the
		// observatory shows the failure, not just a gap.
		recordMetric(v, indexOperation(indexForceReembed, startTime, vault.IndexStats{}, embeddingRunStats{}, v.Config.AI, err))
		slog.Error("index failed", "error", err, "elapsed", time.Since(startTime))
		return fmt.Errorf("index vault: %w", err)
	}

	slog.Info("index complete",
		"docs", stats.DocsIndexed,
		"chunks", stats.ChunksCreated,
		"links", stats.LinksFound,
		"elapsed", time.Since(startTime),
	)

	// Generate embeddings if a provider is available
	initAIProviders(v)
	ctx := context.Background()
	cfg := v.Config.AI

	var embedStats embeddingRunStats
	if indexForceReembed {
		embedStats, err = forceReembedDocuments(ctx, v, cfg)
		if err != nil {
			recordMetric(v, indexOperation(true, startTime, *stats, embedStats, cfg, err))
			slog.Error("force-reembed failed", "error", err)
			return err
		}
	} else if es, eerr := embedDocuments(ctx, v, cfg); eerr != nil {
		slog.Debug("embedding skipped", "reason", eerr.Error())
		if !flagPorcelain {
			// When no provider is configured at all, guide the user
			// directly to `2nb ai setup` instead of printing a raw
			// registry-lookup error — that's the "just works"
			// onboarding path for receivers of a shipped vault.
			if cfg.Provider == "" {
				fmt.Fprintln(os.Stderr, "  no AI provider configured — run '2nb ai setup' to enable semantic search (BM25 index built)")
			} else {
				fmt.Fprintf(os.Stderr, "  embedding skipped: %v\n", eerr)
			}
		}
	} else {
		embedStats = es
		if es.Failed > 0 {
			slog.Warn("embedding completed with document failures", "embedded", es.Embedded, "attempted", es.Attempted, "failed", es.Failed)
		}
	}

	recordMetric(v, indexOperation(indexForceReembed, startTime, *stats, embedStats, cfg, nil))

	format := getFormat(cmd)
	if format != "" {
		return output.Write(os.Stdout, format, stats)
	}

	if !flagPorcelain {
		fmt.Printf("Indexed %d files, %d chunks, %d links\n", stats.DocsIndexed, stats.ChunksCreated, stats.LinksFound)
		if stats.DocsIndexed > 0 {
			fmt.Fprintln(os.Stderr, "\nReady to search:")
			fmt.Fprintln(os.Stderr, "  2nb search \"your query\"")
			fmt.Fprintln(os.Stderr, "  2nb ask \"your question\"")
		}
	}
	return nil
}

func runIndexSingleDoc(cmd *cobra.Command, v *vault.Vault, docArg string) (err error) {
	start := time.Now()

	// Record the single-doc reindex (best-effort) on every return path —
	// success or failure. This is the high-frequency operation: editors and the
	// macOS app shell `2nb index --doc` on each note save.
	var embedStats embeddingRunStats
	defer func() {
		recordMetric(v, metrics.Operation{
			Operation:      metrics.OpIndexDoc,
			DurationMs:     time.Since(start).Milliseconds(),
			OK:             err == nil,
			Error:          errString(err),
			DocsIndexed:    1,
			Embedded:       embedStats.Embedded,
			EmbedSkipped:   embedStats.Skipped,
			EmbedFailed:    embedStats.Failed,
			EmbedMs:        embedStats.DurationMs,
			TotalChars:     embedStats.TotalChars,
			EmbeddingModel: embedStats.Model,
			EmbeddingDims:  v.Config.AI.Dimensions,
		})
	}()

	absPath := docArg
	if !filepath.IsAbs(absPath) {
		absPath = filepath.Join(v.Root, docArg)
	}
	if _, statErr := os.Stat(absPath); statErr != nil {
		return fmt.Errorf("resolve doc path: %w", statErr)
	}

	if idxErr := vault.IndexSingleFile(v, absPath); idxErr != nil {
		return idxErr
	}

	// Re-run embeddings. DocumentsNeedingEmbedding will only include docs
	// whose content_hash changed since the last embed, so this is cheap when
	// nothing actually changed.
	initAIProviders(v)
	ctx := context.Background()
	cfg := v.Config.AI
	embedded := false
	if stats, embErr := embedDocuments(ctx, v, cfg); embErr != nil {
		slog.Debug("incremental embed skipped", "reason", embErr.Error())
	} else {
		embedStats = stats
		embedded = stats.Embedded > 0
	}

	result := IndexDocResult{
		Path:       v.RelPath(absPath),
		Embedded:   embedded,
		DurationMs: time.Since(start).Milliseconds(),
	}

	if getFormat(cmd) == output.FormatJSON {
		data, err := json.Marshal(result)
		if err != nil {
			return err
		}
		fmt.Println(string(data))
		return nil
	}

	if !flagPorcelain {
		fmt.Printf("Indexed %s in %dms\n", result.Path, result.DurationMs)
	}
	return nil
}

func validateEmbeddingProvider(ctx context.Context, cfg ai.AIConfig) (ai.EmbeddingProvider, error) {
	embedder, err := ai.DefaultRegistry.Embedder(cfg.Provider)
	if err != nil {
		return nil, fmt.Errorf("no embedding provider %q configured — run `2nb ai setup`", cfg.Provider)
	}

	if !embedder.Available(ctx) {
		return nil, fmt.Errorf("embedding provider %q is not ready (check credentials) — run `2nb ai setup`", cfg.Provider)
	}
	return embedder, nil
}

func forceReembedDocuments(ctx context.Context, v *vault.Vault, cfg ai.AIConfig) (embeddingRunStats, error) {
	embedder, err := validateEmbeddingProvider(ctx, cfg)
	if err != nil {
		return embeddingRunStats{}, fmt.Errorf("force-reembed preflight: %w", err)
	}

	snapshot, err := v.DB.SnapshotEmbeddings()
	if err != nil {
		return embeddingRunStats{}, err
	}

	// --force-reembed clears embedding_hash on every embedded row so
	// DocumentsNeedingEmbedding returns all of them. Used when the
	// user intentionally switches providers and wants a full rebuild
	// immediately instead of per-document drift re-embedding.
	n, err := v.DB.InvalidateAllEmbeddings()
	if err != nil {
		return embeddingRunStats{}, fmt.Errorf("invalidate embeddings: %w", err)
	}
	slog.Info("force-reembed: invalidated embeddings", "count", n)
	if !flagPorcelain {
		fmt.Fprintf(os.Stderr, "  force-reembed: invalidated %d embeddings, re-embedding...\n", n)
	}

	stats, err := embedDocumentsWithProvider(ctx, v, cfg, embedder)
	// Skipped (empty) documents are not embeddable, so "complete" means every
	// non-skipped document embedded. Without subtracting Skipped here a vault
	// with even one blank note would always report force-reembed as incomplete.
	if err == nil && (stats.Failed > 0 || stats.Embedded < stats.Attempted-stats.Skipped) {
		err = fmt.Errorf("force-reembed incomplete: embedded %d/%d documents (%d failed)", stats.Embedded, stats.Attempted-stats.Skipped, stats.Failed)
	}
	if err == nil {
		return stats, nil
	}

	slog.Warn("force-reembed failed; restoring previous embeddings", "error", err, "embedded", stats.Embedded, "attempted", stats.Attempted, "failed", stats.Failed)
	if restoreErr := v.DB.RestoreEmbeddings(snapshot); restoreErr != nil {
		return stats, fmt.Errorf("%w; failed to restore previous embeddings: %v", err, restoreErr)
	}
	if !flagPorcelain {
		fmt.Fprintln(os.Stderr, "  force-reembed failed; restored previous embeddings")
	}
	return stats, err
}

func embedDocuments(ctx context.Context, v *vault.Vault, cfg ai.AIConfig) (embeddingRunStats, error) {
	embedder, err := validateEmbeddingProvider(ctx, cfg)
	if err != nil {
		return embeddingRunStats{}, err
	}
	return embedDocumentsWithProvider(ctx, v, cfg, embedder)
}

func embedDocumentsWithProvider(ctx context.Context, v *vault.Vault, cfg ai.AIConfig, embedder ai.EmbeddingProvider) (embeddingRunStats, error) {
	model := cfg.EmbeddingModel
	docs, err := v.DB.DocumentsNeedingEmbedding(model)
	if err != nil {
		return embeddingRunStats{}, err
	}
	stats := embeddingRunStats{Attempted: len(docs), Model: model}

	if len(docs) == 0 {
		slog.Debug("all embeddings up to date", "model", model)
		if !flagPorcelain {
			fmt.Fprintln(os.Stderr, "  all embeddings up to date")
		}
		return stats, nil
	}

	embedStart := time.Now()
	concurrency := cfg.ResolveEmbedConcurrency(cfg.Provider)
	slog.Info("embedding documents", "count", len(docs), "model", model, "provider", cfg.Provider, "concurrency", concurrency)
	if !flagPorcelain {
		fmt.Fprintf(os.Stderr, "  embedding %d documents (concurrency %d)...\n", len(docs), concurrency)
	}

	// Bounded worker pool: each doc is embedded concurrently, up to `concurrency`
	// in flight (the sem + WaitGroup pattern from models.go). embed.Document is
	// concurrency-safe per doc (distinct docID; the WAL store serializes writes),
	// so the only per-doc state is each worker's own result slot — written by
	// exactly one goroutine and summed after Wait, so no mutex is needed. The
	// fixed inter-doc ThrottleDelay is gone: the concurrency cap plus the
	// ThrottlingException backoff (isBedrockRetryable) replace fixed-rate pacing,
	// and a throttled account self-corrects via retries rather than failing.
	type docResult struct{ embedded, failed, skipped, chars int }
	results := make([]docResult, len(docs))
	var completed atomic.Int64
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup

	for i := range docs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			doc := docs[i]
			absPath := filepath.Join(v.Root, doc.Path)
			parsed, err := document.ParseFile(absPath)
			if err != nil {
				results[i].failed = 1
				slog.Warn("embedding skipped: parse failed", "path", doc.Path, "err", err)
				if !flagPorcelain {
					fmt.Fprintf(os.Stderr, "  skip %s: %v\n", doc.Path, err)
				}
				return
			}

			// Per-chunk embeddings via the shared embed path (also used by
			// `create` and the MCP write tools): each heading-bounded chunk goes
			// into vec_chunks (vec0) for chunk-level KNN, and documents.embedding
			// is the mean of the chunk vectors. n==0 means an empty doc — skip,
			// not fail.
			n, err := embed.Document(ctx, v.DB, embedder, doc.ID, parsed, model)
			if err != nil {
				results[i].failed = 1
				slog.Warn("embedding failed", "path", doc.Path, "provider", cfg.Provider, "model", model, "err", err)
				if !flagPorcelain {
					fmt.Fprintf(os.Stderr, "  embed error %s: %v\n", doc.Path, err)
				}
				return
			}
			if n == 0 {
				results[i].skipped = 1
				slog.Debug("embedding skipped: empty document", "path", doc.Path)
				if !flagPorcelain {
					fmt.Fprintf(os.Stderr, "  skip %s: empty (nothing to embed)\n", doc.Path)
				}
				return
			}

			results[i].embedded = 1
			results[i].chars = len(parsed.Body)
			if !flagPorcelain {
				// Monotonic completion counter; the path order printed is
				// non-deterministic under concurrency.
				fmt.Fprintf(os.Stderr, "  embedded %d/%d: %s\n", completed.Add(1), len(docs), doc.Path)
			}
		}(i)
	}
	wg.Wait()

	var totalChars, embedded int
	for _, r := range results {
		stats.Embedded += r.embedded
		stats.Failed += r.failed
		stats.Skipped += r.skipped
		totalChars += r.chars
		embedded += r.embedded
	}

	// Show cost estimate for non-free providers
	if !flagPorcelain && embedded > 0 {
		var modelInfo ai.ModelInfo
		if models, err := loadVerifiedModelCatalog(ctx, cfg, v.Root); err == nil {
			modelInfo, _ = lookupModelInfo(models, cfg.Provider, model)
		} else {
			slog.Debug("cost estimate skipped: catalog load failed", "err", err)
		}
		estimatedTokens := float64(totalChars) / 4.0 // rough chars→tokens estimate
		if ai.IsExplicitlyFree(modelInfo) {
			fmt.Fprintf(os.Stderr, "  cost: free (%s)\n", model)
		} else if cost, ok := ai.EstimateInputCost(modelInfo, estimatedTokens, embedded); ok {
			fmt.Fprintf(os.Stderr, "  cost estimate: $%.4f (%s)\n", cost, model)
			monthlyCost := cost * 4 // assume ~4 re-indexes per month
			if monthlyCost > 3.0 {
				fmt.Fprintf(os.Stderr, "  estimated monthly cost: ~$%.2f\n", monthlyCost)
				fmt.Fprintf(os.Stderr, "  tip: run `2nb ai setup` to configure free local AI with Ollama\n")
			}
		}
	}

	stats.DurationMs = time.Since(embedStart).Milliseconds()
	stats.TotalChars = totalChars
	slog.Info("embedding complete", "embedded", embedded, "total", len(docs), "failed", stats.Failed, "skipped", stats.Skipped, "elapsed", time.Since(embedStart))
	if !flagPorcelain && stats.Skipped > 0 {
		fmt.Fprintf(os.Stderr, "  skipped %d empty document(s)\n", stats.Skipped)
	}
	return stats, nil
}
