package cli

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/apresai/2ndbrain/internal/ai"
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

// IndexResult is the JSON summary of a whole-vault `2nb index`. It is the walk's
// counters PLUS the embed pass's, because the two phases each discover their own
// unparseable notes and a consumer reading only one of them sees a partial
// truth: `index --force-reembed --json` never surfaced an embed-phase failure at
// all, since the envelope was built from the walk's stats alone.
//
// Every list is always present and never null, and every counter is always
// emitted, so an agent never has to tell "absent" apart from "zero".
type IndexResult struct {
	FilesScanned   int `json:"files_scanned"`
	DocsIndexed    int `json:"docs_indexed"`
	ChunksCreated  int `json:"chunks_created"`
	LinksFound     int `json:"links_found"`
	Errors         int `json:"errors"`
	ExcludedPurged int `json:"excluded_purged"`
	// Embed-phase counters, previously visible only in the human summary.
	Embedded     int `json:"embedded"`
	EmbedFailed  int `json:"embed_failed"`
	EmbedSkipped int `json:"embed_skipped"`
	EmbedRetries int `json:"embed_retries"`

	Unparseable []vault.UnparseableDoc `json:"unparseable"`
	Unreadable  []vault.UnparseableDoc `json:"unreadable"`
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
	// Failed counts embedding CALLS that failed. A note that could not be
	// parsed is in Unparseable instead, and one that could not be opened is in
	// Unreadable: no request was made for either.
	Failed int
	// Unparseable names the notes whose content would not parse.
	Unparseable []vault.UnparseableDoc
	// Unreadable names the notes the pass could not open. They keep whatever
	// embedding they already had, so the run is incomplete without being a
	// reason to throw away every embedding it just paid for.
	Unreadable []vault.UnparseableDoc
	// Retries is how many provider retries the pass rode out, read from the
	// context's counter so every caller records it the same way.
	Retries int
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
		EmbedRetries:   es.Retries,
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
	// The bulk pass gets a counter but NO notice: its per-document progress
	// printer already owns the terminal line, and the count is what the
	// observatory needs.
	ctx := ai.WithRetryCounter(context.Background(), &ai.RetryCounter{})
	cfg := v.Config.AI

	// Capture pre-embed state so the stamping below can tell whether the vault's
	// embeddings all end up at the current logic generation (see vault.StampAfterIndex).
	embeddingCountBefore, _ := v.DB.EmbeddingCount()
	priorEmbedGen := vault.PriorEmbedGeneration(v.DB)

	var embedStats embeddingRunStats
	// Set when the run has work left but nothing to undo: it is reported
	// through the normal summary and returned at the end, so the exit code is
	// non-zero without the report being skipped.
	var deferredErr error
	if indexForceReembed {
		embedStats, err = forceReembedDocuments(ctx, v, cfg)
		if err != nil {
			if !errors.Is(err, errReembedUnreadable) {
				recordMetric(v, indexOperation(true, startTime, *stats, embedStats, cfg, err))
				slog.Error("force-reembed failed", "error", err)
				return err
			}
			deferredErr = err
			slog.Warn("force-reembed left notes unembedded", "error", err, "embedded", embedStats.Embedded, "unreadable", len(embedStats.Unreadable))
		}
	} else if es, eerr := embedDocuments(ctx, v, cfg, withEmbedProgress); eerr != nil {
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

	recordMetric(v, indexOperation(indexForceReembed, startTime, *stats, embedStats, cfg, deferredErr))

	// Stamp which indexing/embedding LOGIC generation this build achieved, so a
	// future 2nb that changes that logic can detect this vault is stale and prompt
	// a reindex/re-embed (vault status / ai status / config doctor). Best-effort —
	// never fail the index over it. StampAfterIndex advances the embed generation
	// only when all stored embeddings are current-gen (a full re-embed, or a fresh/
	// already-current vault with everything embedded).
	// The shortfall is every document that needed embedding and did not get it:
	// failed calls PLUS notes the pass could not open. An unreadable note keeps
	// its OLD vector, which InvalidateAllEmbeddings does not clear, so counting
	// only Failed would let a force-reembed stamp the vault as fully current-gen
	// while one note's vector is still from the previous generation.
	if serr := vault.StampAfterIndex(v.DB, Version, indexForceReembed, embedStats.Failed+len(embedStats.Unreadable), embeddingCountBefore, priorEmbedGen); serr != nil {
		slog.Warn("stamp index generation failed", "err", serr)
	}

	// One merged truth for both surfaces. A note that fails to parse can be
	// discovered by the walk or by the embed pass, and until now the JSON carried
	// only the walk's list while the human summary printed whichever list the
	// code path happened to hold.
	unparseable := mergeUnparseable(stats.Unparseable, embedStats.Unparseable)
	// Both phases meet the same unopenable note: the walk keeps its row and
	// names it, then the embed pass tries to re-read it and names it again. One
	// note to fix is one entry.
	unreadable := mergeUnparseable(stats.Unreadable, embedStats.Unreadable)

	format := getFormat(cmd)
	if format != "" {
		if werr := output.Write(os.Stdout, format, IndexResult{
			FilesScanned:   stats.FilesScanned,
			DocsIndexed:    stats.DocsIndexed,
			ChunksCreated:  stats.ChunksCreated,
			LinksFound:     stats.LinksFound,
			Errors:         stats.Errors,
			ExcludedPurged: stats.ExcludedPurged,
			Embedded:       embedStats.Embedded,
			EmbedFailed:    embedStats.Failed,
			EmbedSkipped:   embedStats.Skipped,
			EmbedRetries:   embedStats.Retries,
			Unparseable:    unparseable,
			Unreadable:     unreadable,
		}); werr != nil {
			return werr
		}
		return deferredErr
	}

	if !flagPorcelain {
		// The "Indexed N files, N chunks, N links" line is a contract the macOS
		// app parses; anything new goes beside it, on stderr, never inside it.
		fmt.Printf("Indexed %d files, %d chunks, %d links\n", stats.DocsIndexed, stats.ChunksCreated, stats.LinksFound)
		reportUnparseable(unparseable)
		reportUnreadable(unreadable)
		if stats.DocsIndexed > 0 {
			fmt.Fprintln(os.Stderr, "\nReady to search:")
			fmt.Fprintln(os.Stderr, "  2nb search \"your query\"")
			fmt.Fprintln(os.Stderr, "  2nb ask \"your question\"")
		}
	}
	return deferredErr
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
			EmbedRetries:   embedStats.Retries,
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
	// The high-frequency path: editors and the macOS app run this on every
	// save, so a throttled account shows up here first. The pass runs SILENT
	// here: the notice owns the terminal line, and for one document its own
	// progress lines say nothing the notice does not.
	ctx, stopNotice := slowCallNotice(ctx, "embedding note")
	stats, embErr := embedDocuments(ctx, v, cfg, withoutEmbedProgress)
	stopNotice()
	if embErr != nil {
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

	if done, err := emitStructured(cmd, result); done {
		return err
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

	// One ask, and carry its answer. Re-asking for the reason can disagree with
	// the first answer, and for a TRANSIENT failure (held only briefly, see
	// failureTTL) the entry can lapse in between, so the second ask pays a fresh
	// probe on exactly the path this reporting exists to explain.
	if ready, code := ai.Availability(ctx, embedder); !ready {
		return nil, embedderNotReadyError(cfg.Provider, code)
	}
	return embedder, nil
}

// errReembedUnreadable marks a force-reembed that embedded every note it could
// open but could not open all of them. It is the one incomplete outcome that
// must NOT roll the vault back: the notes it did embed were paid for, and the
// unreadable ones keep the embedding they already had. runIndex recognizes it,
// prints the full summary, and returns it so the exit code still says the run
// has work left.
var errReembedUnreadable = errors.New("force-reembed incomplete")

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

	stats, err := embedDocumentsWithProvider(ctx, v, cfg, embedder, withEmbedProgress)
	// Reporting is the caller's: it is the only place that knows BOTH phases'
	// lists, and one merged summary is the whole point.
	// A note that is skipped (empty), unparseable, or unreadable is not
	// embeddable this run, so "complete" means every remaining document
	// embedded. Without subtracting them a vault with one blank note, one note
	// with bad frontmatter, or one note the run could not open reports
	// force-reembed as incomplete and throws away every embedding it just paid
	// for: that is exactly what happened three times in a row on a 314-note
	// vault where a single note had an empty frontmatter block.
	embeddable := stats.Attempted - stats.Skipped - len(stats.Unparseable) - len(stats.Unreadable)
	if err == nil && (stats.Failed > 0 || stats.Embedded < embeddable) {
		err = fmt.Errorf("force-reembed incomplete: embedded %d/%d documents (%d failed)", stats.Embedded, embeddable, stats.Failed)
	}
	if err == nil {
		if len(stats.Unreadable) > 0 {
			// Every note that could be opened was embedded. Keeping those
			// embeddings and naming what is left to do beats rolling a whole
			// vault back over a permission bit, so this error travels WITHOUT a
			// restore: the caller still prints the summary and exits non-zero.
			return stats, fmt.Errorf("%w: %d note(s) could not be read", errReembedUnreadable, len(stats.Unreadable))
		}
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

// unparseableSummaryLimit bounds how many paths the human summary names before
// it defers to -v. Five is enough to recognize a pattern (one folder, one
// template set) without turning a 300-note vault's output into a wall.
const unparseableSummaryLimit = 5

// mergeUnparseable concatenates per-phase lists, keeping the FIRST entry for
// each path. A note can be reported by the walk and again by the embed pass
// (the walk drops its row, a later phase re-reads it), and listing it twice
// would overstate how many notes need fixing.
func mergeUnparseable(lists ...[]vault.UnparseableDoc) []vault.UnparseableDoc {
	out := []vault.UnparseableDoc{}
	seen := map[string]bool{}
	for _, list := range lists {
		for _, d := range list {
			if seen[d.Path] {
				continue
			}
			seen[d.Path] = true
			out = append(out, d)
		}
	}
	return out
}

// reportUnparseable prints the count and the notes to fix. It names up to
// unparseableSummaryLimit paths and points at -v ONLY when it actually elided
// some: the previous version printed the whole list and told the reader to run
// -v to see it, which is a hint to run a command that shows them nothing new.
// Silent under --porcelain and when there is nothing to report.
func reportUnparseable(docs []vault.UnparseableDoc) {
	if flagPorcelain || len(docs) == 0 {
		return
	}
	fmt.Fprintf(os.Stderr, "  skipped %d unparseable note(s):\n", len(docs))
	shown := docs
	if !flagVerbose && len(shown) > unparseableSummaryLimit {
		shown = shown[:unparseableSummaryLimit]
	}
	for _, d := range shown {
		fmt.Fprintf(os.Stderr, "    %s (%s)\n", d.Path, d.Err)
	}
	if elided := len(docs) - len(shown); elided > 0 {
		fmt.Fprintf(os.Stderr, "    ... and %d more (run with -v to list them all)\n", elided)
	}
}

// reportUnreadable names the notes the run could not open. They keep whatever
// index entry they already had, which is the part a reader needs to know.
func reportUnreadable(docs []vault.UnparseableDoc) {
	if flagPorcelain || len(docs) == 0 {
		return
	}
	fmt.Fprintf(os.Stderr, "  could not read %d note(s); their existing index entries were kept:\n", len(docs))
	shown := docs
	if !flagVerbose && len(shown) > unparseableSummaryLimit {
		shown = shown[:unparseableSummaryLimit]
	}
	for _, d := range shown {
		fmt.Fprintf(os.Stderr, "    %s (%s)\n", d.Path, d.Err)
	}
	if elided := len(docs) - len(shown); elided > 0 {
		fmt.Fprintf(os.Stderr, "    ... and %d more (run with -v to list them all)\n", elided)
	}
}

// embedProgress selects whether the pass prints its per-document stderr
// progress. It exists because that printer and the slow-call notice both write
// to stderr WITHOUT a newline: run together they interleave on one line, and
// the notice's erase then wipes whatever the printer left there. Only one of
// them may own the terminal line at a time.
type embedProgress bool

const (
	// withEmbedProgress: the pass owns the line. Used where there is real
	// per-document progress worth watching (a whole-vault index or reembed).
	withEmbedProgress embedProgress = true
	// withoutEmbedProgress: the caller owns the line. Used by `index --doc`,
	// whose slow-call notice is the thing worth watching; for ONE document
	// "embedding 1 documents (concurrency 4)" and "embedded 1/1" are noise
	// anyway.
	withoutEmbedProgress embedProgress = false
)

// embedProgressOpts builds the pass's callbacks. Nil callbacks mean a silent
// pass, which is also what --porcelain gets.
func embedProgressOpts(p embedProgress) vault.EmbedOpts {
	if p == withoutEmbedProgress || flagPorcelain {
		return vault.EmbedOpts{}
	}
	return vault.EmbedOpts{
		OnStart: func(count, concurrency int) {
			fmt.Fprintf(os.Stderr, "  embedding %d documents (concurrency %d)...\n", count, concurrency)
		},
		OnEvent: func(ev vault.EmbedEvent) {
			switch ev.Kind {
			case vault.EmbedParseFailed:
				fmt.Fprintf(os.Stderr, "  skip %s: %v\n", ev.Path, ev.Err)
			case vault.EmbedReadFailed:
				fmt.Fprintf(os.Stderr, "  skip %s: could not read it (%v)\n", ev.Path, ev.Err)
			case vault.EmbedFailed:
				fmt.Fprintf(os.Stderr, "  embed error %s: %v\n", ev.Path, ev.Err)
			case vault.EmbedSkipped:
				fmt.Fprintf(os.Stderr, "  skip %s: empty (nothing to embed)\n", ev.Path)
			case vault.EmbedEmbedded:
				// Monotonic completion counter; the path order printed is
				// non-deterministic under concurrency.
				fmt.Fprintf(os.Stderr, "  embedded %d/%d: %s\n", ev.Done, ev.Total, ev.Path)
			}
		},
	}
}

func embedDocuments(ctx context.Context, v *vault.Vault, cfg ai.AIConfig, progress embedProgress) (embeddingRunStats, error) {
	embedder, err := validateEmbeddingProvider(ctx, cfg)
	if err != nil {
		return embeddingRunStats{}, err
	}
	return embedDocumentsWithProvider(ctx, v, cfg, embedder, progress)
}

// embedDocumentsWithProvider runs the shared concurrent embed pass
// (vault.EmbedDocuments) and layers the CLI's stderr progress + cost estimate on
// top. The worker-pool logic itself lives in internal/vault so the MCP kb_index
// tool shares it; here we only translate the pass's events into the CLI's
// !flagPorcelain output and convert the result into embeddingRunStats.
func embedDocumentsWithProvider(ctx context.Context, v *vault.Vault, cfg ai.AIConfig, embedder ai.EmbeddingProvider, progress embedProgress) (embeddingRunStats, error) {
	quiet := embedProgressOpts(progress).OnEvent == nil

	es, err := vault.EmbedDocuments(ctx, v, cfg, embedder, embedProgressOpts(progress))
	// Read the counter AFTER the pass: every worker reports into the one the
	// caller attached to ctx, so this is the whole pass's retry total.
	retries := ai.RetriesFrom(ctx)
	stats := embeddingRunStats{
		Retries:     retries,
		Attempted:   es.Attempted,
		Embedded:    es.Embedded,
		Failed:      es.Failed,
		Unparseable: es.Unparseable,
		Unreadable:  es.Unreadable,
		Skipped:     es.Skipped,
		DurationMs:  es.DurationMs,
		TotalChars:  es.TotalChars,
		Model:       es.Model,
	}
	if err != nil {
		return stats, err
	}

	if es.Attempted == 0 {
		if !flagPorcelain && !quiet {
			fmt.Fprintln(os.Stderr, "  all embeddings up to date")
		}
		return stats, nil
	}

	// Show cost estimate for non-free providers (CLI-only UX, after the pass).
	if !flagPorcelain && !quiet && es.Embedded > 0 {
		var modelInfo ai.ModelInfo
		if models, err := loadVerifiedModelCatalog(ctx, cfg, v.Root); err == nil {
			modelInfo, _ = lookupModelInfo(models, cfg.Provider, es.Model)
		} else {
			slog.Debug("cost estimate skipped: catalog load failed", "err", err)
		}
		estimatedTokens := float64(es.TotalChars) / 4.0 // rough chars→tokens estimate
		if ai.IsExplicitlyFree(modelInfo) {
			fmt.Fprintf(os.Stderr, "  cost: free (%s)\n", es.Model)
		} else if cost, ok := ai.EstimateInputCost(modelInfo, estimatedTokens, es.Embedded); ok {
			fmt.Fprintf(os.Stderr, "  cost estimate: $%.4f (%s)\n", cost, es.Model)
			monthlyCost := cost * 4 // assume ~4 re-indexes per month
			if monthlyCost > 3.0 {
				fmt.Fprintf(os.Stderr, "  estimated monthly cost: ~$%.2f\n", monthlyCost)
				fmt.Fprintf(os.Stderr, "  tip: run `2nb ai setup` to configure free local AI with Ollama\n")
			}
		}
	}

	if !flagPorcelain && !quiet && stats.Skipped > 0 {
		fmt.Fprintf(os.Stderr, "  skipped %d empty document(s)\n", stats.Skipped)
	}
	return stats, nil
}
