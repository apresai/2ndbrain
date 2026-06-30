package vault

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/apresai/2ndbrain/internal/ai"
	"github.com/apresai/2ndbrain/internal/document"
	"github.com/apresai/2ndbrain/internal/embed"
)

// EmbedStats summarizes one concurrent embed pass over the documents that need
// (re-)embedding. It is the single source of truth shared by the CLI `index`
// path and the MCP `kb_index` tool, so both report identical counters.
type EmbedStats struct {
	// Attempted is the number of documents that needed embedding (the work set).
	Attempted int
	Embedded  int
	Failed    int
	// Skipped counts documents with no embeddable text (empty or
	// whitespace/comment-only bodies). These are not failures — there is simply
	// nothing to embed — and providers like Amazon Nova-2 reject empty input.
	Skipped int
	// DurationMs is the embed-phase wall time, TotalChars the total body chars
	// embedded, and Model the embedding model used — the throughput inputs for
	// the metrics observatory. Zero when nothing needed embedding.
	DurationMs int64
	TotalChars int
	Model      string
	// Cancelled is true when the caller's context was cancelled mid-pass (e.g.
	// an MCP client timeout/disconnect). The pass returns the partial results it
	// completed rather than an error, so callers can report "embedded N of M".
	Cancelled bool
}

// EmbedEventKind classifies a per-document outcome reported to EmbedOpts.OnEvent.
type EmbedEventKind int

const (
	// EmbedEmbedded: the document was embedded (Done/Total carry progress).
	EmbedEmbedded EmbedEventKind = iota
	// EmbedParseFailed: the document could not be parsed (Err set).
	EmbedParseFailed
	// EmbedFailed: the embedding call failed (Err set).
	EmbedFailed
	// EmbedSkipped: the document had no embeddable text.
	EmbedSkipped
)

// EmbedEvent is a single per-document progress event. Callers (the CLI) use it
// to render live stderr progress; the MCP path leaves OnEvent nil (silent).
type EmbedEvent struct {
	Path  string
	Kind  EmbedEventKind
	Err   error
	Done  int // monotonic embedded count, set for EmbedEmbedded
	Total int // total work set size, set for EmbedEmbedded
}

// EmbedOpts carries optional progress callbacks. Both are nil for a silent run.
type EmbedOpts struct {
	// OnStart fires once before the worker pool launches, with the work-set size
	// and the resolved concurrency. Not called when nothing needs embedding.
	OnStart func(count, concurrency int)
	// OnEvent fires once per document as it completes (any kind).
	OnEvent func(EmbedEvent)
}

// EmbedDocuments re-embeds every document whose content has changed since its
// last embed (or whose stored model differs), using a bounded worker pool sized
// by ai.embed_concurrency. This is the concurrent embed pass extracted from the
// CLI `index` command so the MCP `kb_index` tool shares it instead of its old
// sequential per-document throttle.
//
// Concurrency safety: embed.Document is safe per document (distinct docID; the
// WAL store with _txlock=immediate + RetryBusy serializes writes, and the
// vec_chunks create is mutex-guarded), so each worker writes only its own result
// slot — summed after Wait, no mutex needed. The fixed inter-document throttle is
// gone: the concurrency cap plus the provider's ThrottlingException backoff
// (isBedrockRetryable) replace fixed-rate pacing, so a throttled account
// self-corrects via retries rather than failing.
//
// Context cancellation is honored cooperatively: once ctx is done the pass stops
// launching new work and lets in-flight workers finish, returning the partial
// counts with Cancelled=true (never an error), so an MCP client that disconnects
// mid-index gets what completed rather than a hang.
func EmbedDocuments(ctx context.Context, v *Vault, cfg ai.AIConfig, embedder ai.EmbeddingProvider, opts EmbedOpts) (EmbedStats, error) {
	model := cfg.EmbeddingModel
	docs, err := v.DB.DocumentsNeedingEmbedding(model)
	if err != nil {
		return EmbedStats{}, err
	}
	stats := EmbedStats{Attempted: len(docs), Model: model}
	if len(docs) == 0 {
		slog.Debug("all embeddings up to date", "model", model)
		return stats, nil
	}

	embedStart := time.Now()
	concurrency := cfg.ResolveEmbedConcurrency(cfg.Provider)
	slog.Info("embedding documents", "count", len(docs), "model", model, "provider", cfg.Provider, "concurrency", concurrency)
	if opts.OnStart != nil {
		opts.OnStart(len(docs), concurrency)
	}

	type docResult struct{ embedded, failed, skipped, chars int }
	results := make([]docResult, len(docs))
	var completed atomic.Int64
	var cancelled atomic.Bool
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup

	for i := range docs {
		// Stop launching new work once the caller's context is done. In-flight
		// workers finish; not-yet-started docs stay un-embedded and surface via
		// Cancelled (the work set minus Embedded/Failed/Skipped).
		if ctx.Err() != nil {
			cancelled.Store(true)
			break
		}
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			if ctx.Err() != nil {
				cancelled.Store(true)
				return
			}

			doc := docs[i]
			parsed, err := document.ParseFile(v.AbsPath(doc.Path))
			if err != nil {
				results[i].failed = 1
				slog.Warn("embedding skipped: parse failed", "path", doc.Path, "err", err)
				emitEmbedEvent(opts, EmbedEvent{Path: doc.Path, Kind: EmbedParseFailed, Err: err})
				return
			}

			// Per-chunk embeddings via the shared embed path (also used by
			// `create` and the MCP write tools): each heading-bounded chunk goes
			// into vec_chunks (vec0) for chunk-level KNN, and documents.embedding
			// is the mean of the chunk vectors. n==0 means an empty doc — skip,
			// not fail.
			n, err := embed.Document(ctx, v.DB, embedder, doc.ID, parsed, model)
			if err != nil {
				// A context cancellation surfaces here as an error; report it as
				// cancellation (partial success), not a document failure. Log at
				// debug so a genuine error that races with cancellation isn't
				// fully invisible (it re-surfaces as a logged Failed next run).
				if ctx.Err() != nil {
					cancelled.Store(true)
					slog.Debug("embedding interrupted by ctx cancellation", "path", doc.Path, "err", err)
					return
				}
				results[i].failed = 1
				slog.Warn("embedding failed", "path", doc.Path, "provider", cfg.Provider, "model", model, "err", err)
				emitEmbedEvent(opts, EmbedEvent{Path: doc.Path, Kind: EmbedFailed, Err: err})
				return
			}
			if n == 0 {
				results[i].skipped = 1
				slog.Debug("embedding skipped: empty document", "path", doc.Path)
				emitEmbedEvent(opts, EmbedEvent{Path: doc.Path, Kind: EmbedSkipped})
				return
			}

			results[i].embedded = 1
			results[i].chars = len(parsed.Body)
			emitEmbedEvent(opts, EmbedEvent{
				Path:  doc.Path,
				Kind:  EmbedEmbedded,
				Done:  int(completed.Add(1)),
				Total: len(docs),
			})
		}(i)
	}
	wg.Wait()

	var totalChars int
	for _, r := range results {
		stats.Embedded += r.embedded
		stats.Failed += r.failed
		stats.Skipped += r.skipped
		totalChars += r.chars
	}
	stats.DurationMs = time.Since(embedStart).Milliseconds()
	stats.TotalChars = totalChars
	stats.Cancelled = cancelled.Load()
	slog.Info("embedding complete",
		"embedded", stats.Embedded,
		"total", len(docs),
		"failed", stats.Failed,
		"skipped", stats.Skipped,
		"cancelled", stats.Cancelled,
		"elapsed", time.Since(embedStart),
	)
	return stats, nil
}

func emitEmbedEvent(opts EmbedOpts, ev EmbedEvent) {
	if opts.OnEvent != nil {
		opts.OnEvent(ev)
	}
}
