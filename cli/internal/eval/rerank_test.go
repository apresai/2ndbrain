package eval

import (
	"context"
	"fmt"
	"os"
	"testing"

	"path/filepath"

	"github.com/apresai/2ndbrain/internal/ai"
	"github.com/apresai/2ndbrain/internal/document"
	"github.com/apresai/2ndbrain/internal/search"
	"github.com/apresai/2ndbrain/internal/ragctx"
	"github.com/apresai/2ndbrain/internal/retrieve"
	"github.com/apresai/2ndbrain/internal/vault"
)

// TestRerankRetrievalAB measures the retrieval-quality lift of the PRODUCTION
// Cohere rerank stage (retrieve.Retriever) on a real Nova-embedded vault: R@1
// and MRR@10 with rerank OFF vs ON over the ground-truth QA set. This is the
// measure-first gate for the rerank feature — it decides the documented
// recommendation, not whether to ship (the feature ships default-off either
// way). Credential-gated + manual: it makes one real Bedrock rerank call per
// question.
//
//	env 2NB_EVAL_VAULT=/path/to/vault EVAL_N=24 \
//	  go test ./internal/eval/ -run RerankRetrievalAB -v -count=1
func TestRerankRetrievalAB(t *testing.T) {
	vpath := os.Getenv("2NB_EVAL_VAULT")
	if vpath == "" {
		t.Skip("set 2NB_EVAL_VAULT to a Nova-embedded vault to run the rerank A/B")
	}
	v, err := vault.Open(vpath)
	if err != nil {
		t.Skipf("open vault %q: %v", vpath, err)
	}
	defer v.DB.Close()
	cfg := v.Config.AI
	if cfg.Provider != "bedrock" {
		t.Skipf("rerank A/B needs a bedrock vault, got provider %q", cfg.Provider)
	}
	ctx := context.Background()
	if err := ai.InitBedrock(ctx, ai.DefaultRegistry, cfg.Bedrock, cfg); err != nil {
		t.Skipf("bedrock init (creds?): %v", err)
	}
	emb, err := ai.DefaultRegistry.Embedder("bedrock")
	if err != nil || !emb.Available(ctx) {
		t.Skip("bedrock embedder not available (no creds / no Nova access)")
	}
	gen, err := ai.DefaultRegistry.Generator("bedrock")
	if err != nil {
		t.Skipf("no generator: %v", err)
	}
	reranker, err := ai.NewBedrockReranker(ctx, cfg.Bedrock, ai.DefaultRerankModel)
	if err != nil {
		t.Skipf("bedrock reranker: %v", err)
	}
	if !reranker.Available(ctx) {
		t.Skip("bedrock reranker not available (region/creds)")
	}

	n := envInt("EVAL_N", 24)
	qa, err := LoadOrGenerateQASet(ctx, v, gen, n, 0, qaCachePath())
	if err != nil {
		t.Fatalf("QA set: %v", err)
	}
	t.Logf("QA set: %d questions", len(qa))

	const k = 10
	base := retrieve.New(v).WithReranker(nil)  // force baseline off, ignore vault config
	rr := retrieve.New(v).WithReranker(reranker)

	var baseR1, rrR1 int
	var baseMRR, rrMRR float64
	for _, item := range qa {
		bres, err := base.Retrieve(ctx, retrieve.Options{Query: item.Question, Limit: k})
		if err != nil {
			t.Fatalf("baseline retrieve: %v", err)
		}
		rres, err := rr.Retrieve(ctx, retrieve.Options{Query: item.Question, Limit: k})
		if err != nil {
			t.Fatalf("reranked retrieve: %v", err)
		}
		br := rankOf(bres.Results, item.SourceID)
		rk := rankOf(rres.Results, item.SourceID)
		if br == 1 {
			baseR1++
		}
		if rk == 1 {
			rrR1++
		}
		if br > 0 {
			baseMRR += 1.0 / float64(br)
		}
		if rk > 0 {
			rrMRR += 1.0 / float64(rk)
		}
	}
	nq := float64(len(qa))
	t.Logf("RERANK A/B over %d questions (K=%d, model=%s):", len(qa), k, ai.DefaultRerankModel)
	t.Logf("  R@1:    off=%.3f  on=%.3f  (delta %+.3f)", float64(baseR1)/nq, float64(rrR1)/nq, float64(rrR1-baseR1)/nq)
	t.Logf("  MRR@%d: off=%.3f  on=%.3f  (delta %+.3f)", k, baseMRR/nq, rrMRR/nq, (rrMRR-baseMRR)/nq)
}

// TestRerankAnswerAB is the decisive answer-quality gate: does reranked CONTEXT
// produce better end-to-end answers (jury: correctness/completeness/grounding)
// than the un-reranked hybrid order, even if retrieval RANK drops? Same
// production ask pipeline (retrieve -> ragctx.Build -> ai.RAG) both ways, only
// the reranker differs. Credential-gated + manual (2 answers + 2 jury passes per
// question).
//
//	env 2NB_EVAL_VAULT=/path EVAL_N=20 \
//	  go test ./internal/eval/ -run RerankAnswerAB -v -count=1 -timeout 3600s
func TestRerankAnswerAB(t *testing.T) {
	v, cfg, _, gen, ok := openEvalVault(t)
	if !ok {
		return
	}
	defer v.DB.Close()
	ctx := context.Background()

	reranker, err := ai.NewBedrockReranker(ctx, cfg.Bedrock, ai.DefaultRerankModel)
	if err != nil {
		t.Skipf("bedrock reranker: %v", err)
	}
	if !reranker.Available(ctx) {
		t.Skip("bedrock reranker not available (region/creds)")
	}

	n := envInt("EVAL_N", 20)
	qa, err := LoadOrGenerateQASet(ctx, v, gen, n, 0, qaCachePath())
	if err != nil {
		t.Fatalf("QA set: %v", err)
	}
	judges := buildNamedJudges(ctx, t, cfg, promptJury)
	if len(judges) == 0 {
		t.Skip("no judge models reachable")
	}
	t.Logf("QA=%d judges=%v", len(qa), judgeNames(judges))

	base := retrieve.New(v).WithReranker(nil)
	rr := retrieve.New(v).WithReranker(reranker)

	genAnswer := func(r *retrieve.Retriever, q string) (string, error) {
		res, err := r.Retrieve(ctx, retrieve.Options{Query: q, Limit: ai.DefaultRAGCandidateDocs})
		if err != nil {
			return "", err
		}
		if len(res.Results) == 0 {
			return "", fmt.Errorf("no results")
		}
		chunks, _ := ragctx.Build(res.Results, v.Root, ragctx.Budget{
			TotalRunes: cfg.ResolveRAGContextBudget(),
			NoteRunes:  cfg.ResolveRAGNoteBudget(),
		})
		if len(chunks) == 0 {
			return "", fmt.Errorf("no context")
		}
		out, err := ai.RAG(ctx, gen, q, chunks)
		if err != nil {
			return "", err
		}
		return out.Answer, nil
	}

	var bC, bCorr, bCompl, bGrnd float64
	var rC, rCorr, rCompl, rGrnd float64
	var count int
	for _, item := range qa {
		ba, err := genAnswer(base, item.Question)
		if err != nil {
			continue
		}
		ra, err := genAnswer(rr, item.Question)
		if err != nil {
			continue
		}
		bs := ScoreAnswer(ctx, judges, item.Question, ba, item.SourceTitle, item.SourceBody)
		rs := ScoreAnswer(ctx, judges, item.Question, ra, item.SourceTitle, item.SourceBody)
		if bs.NJudges == 0 || rs.NJudges == 0 {
			continue
		}
		bC += bs.Composite
		bCorr += bs.Correctness
		bCompl += bs.Completeness
		bGrnd += bs.Grounding
		rC += rs.Composite
		rCorr += rs.Correctness
		rCompl += rs.Completeness
		rGrnd += rs.Grounding
		count++
	}
	if count == 0 {
		t.Skip("no scored answer pairs")
	}
	fn := float64(count)
	t.Logf("=== RERANK ANSWER-QUALITY A/B (jury, N=%d scored) ===", count)
	t.Logf("%-12s %-7s %-6s %-6s %-6s", "variant", "COMP", "corr", "compl", "grnd")
	t.Logf("%-12s %-7.3f %-6.3f %-6.3f %-6.3f", "rerank off", bC/fn, bCorr/fn, bCompl/fn, bGrnd/fn)
	t.Logf("%-12s %-7.3f %-6.3f %-6.3f %-6.3f", "rerank on", rC/fn, rCorr/fn, rCompl/fn, rGrnd/fn)
	t.Logf("composite delta (on - off): %+.3f", (rC-bC)/fn)
}

// TestRerankFullNoteRetrievalAB tests the last hypothesis: reranking on the
// FULL NOTE text (not the matched chunk) might beat both the chunk-input rerank
// and no-rerank. Over-fetches a 50-candidate pool, reranks on each note's full
// body, and compares top-10 R@1/MRR to the un-reranked RRF order.
//
//	env 2NB_EVAL_VAULT=/path EVAL_N=20 \
//	  go test ./internal/eval/ -run RerankFullNoteRetrievalAB -v -count=1 -timeout 1200s
func TestRerankFullNoteRetrievalAB(t *testing.T) {
	v, cfg, _, gen, ok := openEvalVault(t)
	if !ok {
		return
	}
	defer v.DB.Close()
	ctx := context.Background()
	reranker, err := ai.NewBedrockReranker(ctx, cfg.Bedrock, ai.DefaultRerankModel)
	if err != nil || !reranker.Available(ctx) {
		t.Skip("bedrock reranker not available")
	}
	n := envInt("EVAL_N", 20)
	qa, err := LoadOrGenerateQASet(ctx, v, gen, n, 0, qaCachePath())
	if err != nil {
		t.Fatalf("QA set: %v", err)
	}

	base := retrieve.New(v).WithReranker(nil)
	const k, pool = 10, 50
	var baseR1, rrR1 int
	var baseMRR, rrMRR float64
	for _, item := range qa {
		// Over-fetch the candidate pool once (RRF order).
		res, err := base.Retrieve(ctx, retrieve.Options{Query: item.Question, Limit: pool})
		if err != nil || len(res.Results) == 0 {
			continue
		}
		results := res.Results
		// Baseline = RRF top-k.
		top := results
		if len(top) > k {
			top = top[:k]
		}
		if r := rankOf(top, item.SourceID); r > 0 {
			if r == 1 {
				baseR1++
			}
			baseMRR += 1.0 / float64(r)
		}
		// Rerank on FULL NOTE bodies.
		texts := make([]string, len(results))
		for i, r := range results {
			doc, derr := document.ParseFile(filepath.Join(v.Root, r.Path))
			if derr == nil {
				texts[i] = r.Title + "\n\n" + doc.IndexableBody()
			} else {
				texts[i] = r.Title + "\n\n" + r.Content
			}
		}
		hits, herr := reranker.Rerank(ctx, item.Question, texts, len(results))
		if herr != nil {
			continue
		}
		reordered := make([]search.Result, 0, len(results))
		seen := make([]bool, len(results))
		for _, h := range hits {
			if h.Index >= 0 && h.Index < len(results) && !seen[h.Index] {
				seen[h.Index] = true
				reordered = append(reordered, results[h.Index])
			}
		}
		rtop := reordered
		if len(rtop) > k {
			rtop = rtop[:k]
		}
		if r := rankOf(rtop, item.SourceID); r > 0 {
			if r == 1 {
				rrR1++
			}
			rrMRR += 1.0 / float64(r)
		}
	}
	nq := float64(len(qa))
	t.Logf("=== FULL-NOTE RERANK RETRIEVAL A/B (N=%d, pool=%d, K=%d) ===", len(qa), pool, k)
	t.Logf("  R@1:    off=%.3f  on=%.3f  (delta %+.3f)", float64(baseR1)/nq, float64(rrR1)/nq, float64(rrR1-baseR1)/nq)
	t.Logf("  MRR@%d: off=%.3f  on=%.3f  (delta %+.3f)", k, baseMRR/nq, rrMRR/nq, (rrMRR-baseMRR)/nq)
}
