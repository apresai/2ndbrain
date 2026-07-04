package eval

import (
	"context"
	"fmt"
	"sort"

	"github.com/apresai/2ndbrain/internal/ai"
	"github.com/apresai/2ndbrain/internal/ragctx"
	"github.com/apresai/2ndbrain/internal/search"
	"github.com/apresai/2ndbrain/internal/vault"
)

// SweepConfig is one point in the retrieval config grid. QueryPurpose varies the
// Nova query-side embedding (GENERIC_RETRIEVAL vs TEXT_RETRIEVAL); the weights and
// threshold feed the same production RRF fusion as `2nb search`/`ask`.
type SweepConfig struct {
	Name         string
	QueryPurpose string // ai.PurposeQuery | ai.PurposeQueryText
	BM25Weight   float64
	VectorWeight float64
	Threshold    float64
	BM25Only     bool
}

// ConfigMetrics is a config's retrieval quality over the QA set: does it rank each
// question's source note high? RecallAtK / RecallAt1 / MRR are computed against the
// ground-truth SourceID, using the SAME engine.HybridSearch the product runs.
type ConfigMetrics struct {
	Config    SweepConfig
	N         int
	K         int
	RecallAtK float64
	RecallAt1 float64
	MRRAtK    float64
}

// corpus is the loaded embedding index, shared across the sweep.
type corpus struct {
	engine    *search.Engine
	ids       []string
	vecs      [][]float32
	questions []string               // aligned to qa order (feeds the BM25 channel)
	queryVe   map[string][][]float32 // purpose -> query vectors aligned to qa order
}

// loadCorpus loads the embedding index and pre-embeds every question once per
// distinct purpose, so the (free) grid sweep never re-embeds. Query embedding is
// the only API cost here: len(purposes) * len(qa) calls.
func loadCorpus(ctx context.Context, v *vault.Vault, emb ai.EmbeddingProvider, qa []QAItem, purposes []string) (*corpus, error) {
	ids, vecs, err := v.DB.AllEmbeddings()
	if err != nil {
		return nil, fmt.Errorf("load embeddings: %w", err)
	}
	questions := make([]string, len(qa))
	for i, item := range qa {
		questions[i] = item.Question
	}
	qv := make(map[string][][]float32, len(purposes))
	for _, p := range purposes {
		v, err := emb.Embed(ctx, questions, ai.WithPurpose(p))
		if err != nil {
			return nil, fmt.Errorf("embed queries (%s): %w", p, err)
		}
		qv[p] = v
	}
	return &corpus{engine: search.NewEngine(v.DB.Conn()), ids: ids, vecs: vecs, questions: questions, queryVe: qv}, nil
}

// RunRetrievalSweep scores each config's ability to rank the ground-truth source
// note for each question. K bounds the candidate list; source-in-top-K = a hit.
func RunRetrievalSweep(ctx context.Context, v *vault.Vault, emb ai.EmbeddingProvider, qa []QAItem, configs []SweepConfig, k int) ([]ConfigMetrics, *corpus, error) {
	if k <= 0 {
		k = 10
	}
	purposes := distinctPurposes(configs)
	c, err := loadCorpus(ctx, v, emb, qa, purposes)
	if err != nil {
		return nil, nil, err
	}

	out := make([]ConfigMetrics, 0, len(configs))
	for _, cfg := range configs {
		m := ConfigMetrics{Config: cfg, N: len(qa), K: k}
		var sumRR, hit1, hitK float64
		for i, item := range qa {
			results := c.search(cfg, i, k)
			rank := rankOf(results, item.SourceID)
			if rank == 1 {
				hit1++
			}
			if rank >= 1 && rank <= k {
				hitK++
				sumRR += 1.0 / float64(rank)
			}
		}
		m.RecallAt1 = hit1 / float64(len(qa))
		m.RecallAtK = hitK / float64(len(qa))
		m.MRRAtK = sumRR / float64(len(qa))
		out = append(out, m)
	}
	// Rank best-first by MRR, then Recall@K.
	sort.Slice(out, func(i, j int) bool {
		if out[i].MRRAtK != out[j].MRRAtK {
			return out[i].MRRAtK > out[j].MRRAtK
		}
		return out[i].RecallAtK > out[j].RecallAtK
	})
	return out, c, nil
}

// search runs one production hybrid retrieval for qa item i under cfg and returns
// the ranked doc ids. Reuses the pre-embedded query vector for cfg.QueryPurpose.
func (c *corpus) search(cfg SweepConfig, i, k int) []search.Result {
	limit := k
	if limit < ai.DefaultRAGCandidateDocs {
		limit = ai.DefaultRAGCandidateDocs
	}
	opts := search.Options{
		Limit:          limit,
		MinVectorScore: cfg.Threshold,
		BM25Weight:     cfg.BM25Weight,
		VectorWeight:   cfg.VectorWeight,
		BM25Only:       cfg.BM25Only,
	}
	return c.searchOpts(cfg, opts, i)
}

func (c *corpus) searchOpts(cfg SweepConfig, opts search.Options, i int) []search.Result {
	// Always drive both channels off the aligned QA item: opts.Query feeds BM25,
	// the cached qvec feeds the vector channel. They MUST correspond, so set Query
	// from the same index as the query vector.
	opts.Query = c.questions[i]
	qv := c.queryVe[cfg.QueryPurpose] // nil for a BM25-only config (no purpose)
	if opts.BM25Only || qv == nil || qv[i] == nil {
		res, _ := c.engine.Search(opts)
		return res
	}
	res, _, err := c.engine.HybridSearch(opts, qv[i], c.ids, c.vecs)
	if err != nil {
		return nil
	}
	return res
}

// GenerateAnswer runs the full production ask pipeline (retrieve -> ragctx.Build ->
// RAGWithHistory) for one config + question, returning the answer and its source
// doc ids. Used to feed the LLM jury the real end-to-end answer per config.
func GenerateAnswer(ctx context.Context, v *vault.Vault, c *corpus, gen ai.GenerationProvider, cfg SweepConfig, qi int, question string) (string, []string, error) {
	opts := search.Options{
		Limit:          ai.DefaultRAGCandidateDocs,
		MinVectorScore: cfg.Threshold,
		BM25Weight:     cfg.BM25Weight,
		VectorWeight:   cfg.VectorWeight,
		BM25Only:       cfg.BM25Only,
	}
	results := c.searchOpts(cfg, opts, qi) // searchOpts sets opts.Query from the QA item
	if len(results) == 0 {
		return "", nil, fmt.Errorf("no results")
	}
	chunks, _ := ragctx.Build(results, v.Root, ragctx.Budget{
		TotalRunes: v.Config.AI.ResolveRAGContextBudget(),
		NoteRunes:  v.Config.AI.ResolveRAGNoteBudget(),
	})
	if len(chunks) == 0 {
		return "", nil, fmt.Errorf("no context")
	}
	res, err := ai.RAG(ctx, gen, question, chunks)
	if err != nil {
		return "", nil, err
	}
	srcs := make([]string, 0, len(results))
	for _, r := range results {
		srcs = append(srcs, r.DocID)
	}
	return res.Answer, srcs, nil
}

func distinctPurposes(configs []SweepConfig) []string {
	seen := map[string]bool{}
	var out []string
	for _, c := range configs {
		if c.BM25Only {
			continue
		}
		if !seen[c.QueryPurpose] {
			seen[c.QueryPurpose] = true
			out = append(out, c.QueryPurpose)
		}
	}
	if len(out) == 0 { // all BM25-only; still need one purpose key to avoid nil map panics
		out = []string{ai.PurposeQuery}
	}
	return out
}

// rankOf returns the 1-based position of docID in results, or 0 if absent.
func rankOf(results []search.Result, docID string) int {
	for i, r := range results {
		if r.DocID == docID {
			return i + 1
		}
	}
	return 0
}
