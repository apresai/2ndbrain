// Package retrieve owns the one shared "embed query -> hybrid (BM25 + vector,
// RRF) -> BM25 fallback" pipeline that `2nb search`, `2nb ask`, and the MCP
// kb_search / kb_ask tools all need. It exists so those paths cannot drift
// (the same reason internal/ragctx is shared by the ask surfaces): before this
// package each site hand-rolled the embed + HybridSearch + fallback sequence,
// so a fix to one silently skipped the others.
//
// A Retriever gates the vector channel once (VectorCompat), loads the embedding
// corpus lazily, and CACHES it, so `2nb ask`'s rewrite-then-fallback double
// retrieval pays the corpus load a single time. The MCP server injects its own
// cross-request corpus cache via WithCorpusLoader.
package retrieve

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/apresai/2ndbrain/internal/ai"
	"github.com/apresai/2ndbrain/internal/search"
	"github.com/apresai/2ndbrain/internal/vault"
)

// CorpusLoader returns the embedding corpus (doc IDs + whole-doc vectors). It is
// the brute-force fallback source and the vec0 coverage target for HybridSearch.
// Called at most once per Retriever and cached. The CLI passes
// v.DB.AllEmbeddings; the long-lived MCP server passes its own cache so a busy
// session does not re-decode every vector per tool call.
type CorpusLoader func() ([]string, [][]float32, error)

// Options are the per-query knobs. Filters + limit map straight onto
// search.Options. Threshold and the RRF weights default from vault config when
// left zero, so callers only set what they override (e.g. search's --threshold).
type Options struct {
	Query     string
	Type      string
	Status    string
	Tag       string
	Limit     int
	BM25Only  bool
	Threshold float64 // MinVectorScore; 0 resolves to ai.similarity_threshold
}

// Result is one retrieval pass: the ranked results, the mode actually used
// (hybrid vs keyword), and any non-fatal degradation warnings for the caller to
// surface (stderr for the CLI, the JSON envelope for MCP/app clients).
type Result struct {
	Results  []search.Result
	Mode     search.SearchMode
	Warnings []string
}

// Retriever runs the shared retrieval pipeline. Construct one per logical
// operation (one CLI command, one MCP tool call); the vector-readiness gate and
// corpus load run once and are reused across Retrieve calls on the same value.
type Retriever struct {
	v            *vault.Vault
	engine       *search.Engine
	embedder     ai.EmbeddingProvider
	reranker     ai.RerankProvider // nil unless ai.rerank.enabled and registered
	loadCorpus   CorpusLoader
	embedTimeout time.Duration

	// Lazily initialized by ensureReady, then reused.
	inited bool
	vecOK  bool
	ids    []string
	vecs   [][]float32
}

// New builds a Retriever for a vault. It resolves the embedder from the active
// provider and defaults the corpus source to v.DB.AllEmbeddings; override the
// corpus with WithCorpusLoader (MCP) and the per-embed timeout with
// WithEmbedTimeout.
func New(v *vault.Vault) *Retriever {
	embedder, _ := ai.DefaultRegistry.Embedder(v.Config.AI.Provider)
	r := &Retriever{
		v:          v,
		engine:     search.NewEngine(v.DB.Conn()),
		embedder:   embedder,
		loadCorpus: v.DB.AllEmbeddings,
	}
	// The rerank stage is optional (default off): resolve it only when enabled,
	// so a disabled config leaves the RRF order untouched and adds no cloud call.
	if v.Config.AI.RerankEnabled() {
		r.reranker, _ = ai.DefaultRegistry.Reranker(v.Config.AI.Provider)
	}
	return r
}

// WithReranker injects a rerank provider. The CLI/MCP resolve it from the
// registry via New when ai.rerank.enabled; this is for tests and explicit use.
func (r *Retriever) WithReranker(rr ai.RerankProvider) *Retriever {
	r.reranker = rr
	return r
}

// WithCorpusLoader overrides where the embedding corpus comes from (the MCP
// server passes its cross-request cache).
func (r *Retriever) WithCorpusLoader(fn CorpusLoader) *Retriever {
	if fn != nil {
		r.loadCorpus = fn
	}
	return r
}

// WithEmbedTimeout bounds the query-embedding call (the MCP tools cap it at 60s
// so a stuck provider can't hang a client). Zero means no extra deadline.
func (r *Retriever) WithEmbedTimeout(d time.Duration) *Retriever {
	r.embedTimeout = d
	return r
}

// ensureReady runs the one-time vector gate + corpus load. It returns an init
// warning ONCE (subsequent calls return ""), so ask's double retrieval does not
// duplicate the VectorCompat message.
func (r *Retriever) ensureReady(ctx context.Context) (warn string) {
	if r.inited {
		return ""
	}
	r.inited = true

	ready, msg := VectorCompat(ctx, r.v, r.embedder)
	if !ready {
		r.vecOK = false
		if msg != "" {
			// Also log to the persistent file log: the CLI surfaces msg on
			// stderr and the --json envelope, but stderr isn't captured in
			// .2ndbrain/logs/cli.log, and MCP kb_search drops it entirely.
			slog.Warn("retrieve: vector channel unavailable, degrading to BM25", "reason", msg)
		}
		return msg // may be "" (a zero-embedding vault degrades silently)
	}
	ids, vecs, err := r.loadCorpus()
	if err != nil {
		slog.Warn("retrieve: load embeddings failed", "err", err)
		r.vecOK = false
		return fmt.Sprintf("semantic search disabled: failed to load embeddings (%v)", err)
	}
	r.ids, r.vecs, r.vecOK = ids, vecs, true
	return ""
}

// Retrieve runs one hybrid-with-fallback pass. It attempts the vector channel
// only when the query is non-empty, BM25Only is unset, and the vault's
// embeddings are compatible with the active provider (VectorCompat); any embed
// or hybrid failure degrades to BM25 with a warning rather than erroring. A nil
// (never an empty-but-non-nil) hybrid result triggers the BM25 fallback, so a
// legitimately empty hybrid result set is preserved.
func (r *Retriever) Retrieve(ctx context.Context, opts Options) (Result, error) {
	cfg := r.v.Config.AI

	threshold := opts.Threshold
	if threshold == 0 {
		threshold, _ = cfg.ResolveSimilarityThresholdFull(r.v.Root)
	}

	// When the rerank stage is active, over-fetch a larger candidate pool for the
	// cross-encoder to reorder, then trim to the caller's limit after reranking.
	rerankActive := r.reranker != nil && opts.Query != ""
	fetchLimit := opts.Limit
	if rerankActive {
		if cand := cfg.ResolveRerankCandidateDocs(); cand > fetchLimit {
			fetchLimit = cand
		}
	}

	sopts := search.Options{
		Query:          opts.Query,
		Type:           opts.Type,
		Status:         opts.Status,
		Tag:            opts.Tag,
		Limit:          fetchLimit,
		BM25Only:       opts.BM25Only,
		MinVectorScore: threshold,
	}
	sopts.BM25Weight, sopts.VectorWeight = cfg.ResolveHybridWeights()

	var warnings []string
	var results []search.Result
	var mode search.SearchMode

	if !opts.BM25Only && opts.Query != "" {
		if w := r.ensureReady(ctx); w != "" {
			warnings = append(warnings, w)
		}
		if r.vecOK {
			results, mode, warnings = r.hybrid(ctx, sopts, warnings)
		}
	}

	// Fall back to BM25 when hybrid did not run or failed (results stays nil).
	if results == nil {
		res, err := r.engine.Search(sopts)
		if err != nil {
			return Result{}, fmt.Errorf("search: %w", err)
		}
		results, mode = res, search.ModeKeyword
	}

	// Optional cross-encoder rerank: reorder the candidate pool by relevance,
	// then the caller's limit trims. Any rerank failure keeps the RRF order.
	if rerankActive && len(results) > 1 {
		var w []string
		results, w = r.rerankResults(ctx, opts.Query, results)
		warnings = append(warnings, w...)
	}
	if opts.Limit > 0 && len(results) > opts.Limit {
		results = results[:opts.Limit]
	}
	return Result{Results: results, Mode: mode, Warnings: warnings}, nil
}

// rerankResults reorders results by cross-encoder relevance. On any failure it
// returns the input unchanged (keeping the hybrid RRF order) plus a warning, so
// reranking can never make search worse than not reranking.
func (r *Retriever) rerankResults(ctx context.Context, query string, results []search.Result) ([]search.Result, []string) {
	hits, err := r.reranker.Rerank(ctx, query, r.candidateTexts(results), len(results))
	if err != nil {
		slog.Warn("retrieve: rerank failed, keeping hybrid order", "err", err)
		return results, []string{fmt.Sprintf("rerank disabled: %v", err)}
	}
	if len(hits) == 0 {
		return results, nil // defensive: never drop a non-empty candidate set
	}
	reordered := make([]search.Result, 0, len(results))
	seen := make([]bool, len(results))
	for _, h := range hits {
		if h.Index < 0 || h.Index >= len(results) || seen[h.Index] {
			continue
		}
		seen[h.Index] = true
		reordered = append(reordered, results[h.Index])
	}
	// Append any candidate the reranker omitted so nothing is silently dropped.
	for i, res := range results {
		if !seen[i] {
			reordered = append(reordered, res)
		}
	}
	return reordered, nil
}

// candidateTexts returns the text the reranker scores per result: the winning
// chunk's full body (fetched by ChunkID), title-prefixed for context. Vector-
// only results carry no Content and BM25 results carry only a truncated FTS
// snippet, so the full chunk body from the store is the right cross-encoder input.
func (r *Retriever) candidateTexts(results []search.Result) []string {
	ids := make([]string, 0, len(results))
	for _, res := range results {
		if res.ChunkID != "" {
			ids = append(ids, res.ChunkID)
		}
	}
	byChunk, err := r.v.DB.ChunkContentByID(ids)
	if err != nil {
		slog.Warn("retrieve: rerank chunk-text backfill failed", "err", err)
	}
	texts := make([]string, len(results))
	for i, res := range results {
		text := byChunk[res.ChunkID]
		if strings.TrimSpace(text) == "" {
			text = res.Content // fall back to the FTS snippet when no chunk body
		}
		if res.Title != "" {
			text = res.Title + "\n\n" + text
		}
		texts[i] = text
	}
	return texts
}

// hybrid embeds the query and runs HybridSearch. On any failure it appends a
// warning and returns nil results, letting the caller fall back to BM25.
func (r *Retriever) hybrid(ctx context.Context, sopts search.Options, warnings []string) ([]search.Result, search.SearchMode, []string) {
	ectx := ctx
	if r.embedTimeout > 0 {
		var cancel context.CancelFunc
		ectx, cancel = context.WithTimeout(ctx, r.embedTimeout)
		defer cancel()
	}

	queryVecs, err := r.embedder.Embed(ectx, []string{sopts.Query}, ai.WithPurpose(ai.PurposeQuery))
	if err != nil {
		slog.Warn("retrieve: query embed failed", "err", err)
		return nil, "", append(warnings, fmt.Sprintf("semantic search disabled: embedder returned error (%v)", err))
	}
	if len(queryVecs) == 0 {
		// Zero vectors without an error is a provider contract violation; log it
		// so the otherwise-silent BM25 fallback is diagnosable.
		slog.Warn("retrieve: embedder returned no query vectors, degrading to BM25")
		return nil, "", warnings
	}

	res, mode, herr := r.engine.HybridSearch(sopts, queryVecs[0], r.ids, r.vecs)
	if herr != nil {
		slog.Warn("retrieve: hybrid search failed", "err", herr)
		return nil, "", append(warnings, fmt.Sprintf("semantic search disabled: hybrid search failed (%v)", herr))
	}
	return res, mode, warnings
}
