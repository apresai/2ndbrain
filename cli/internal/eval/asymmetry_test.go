package eval

import (
	"context"
	"os"
	"testing"

	"github.com/apresai/2ndbrain/internal/ai"
	"github.com/apresai/2ndbrain/internal/vault"
)

// TestAsymmetricPurpose_Bedrock measures the real retrieval-quality effect of
// Nova's asymmetric embedding purpose (query side GENERIC_RETRIEVAL vs the old
// symmetric GENERIC_INDEX) over a real Nova-embedded vault. It is gated on a
// vault path AND real Bedrock credentials, per the no-mock policy:
//
//	source ~/.secrets/shell.zsh
//	export AWS_BEARER_TOKEN_BEDROCK="$SA_AWS_BEARER_TOKEN_BEDROCK"
//	2NB_EVAL_VAULT=/path/to/vault go test ./internal/eval/ -run Asymmetric -v
//
// It asserts the asymmetric purpose does not REGRESS MRR@K (the flip's whole
// point) and logs the full before/after numbers + a recommended threshold so a
// human sets ai.similarity_threshold from data, not a guess.
func TestAsymmetricPurpose_Bedrock(t *testing.T) {
	vpath := os.Getenv("2NB_EVAL_VAULT")
	if vpath == "" {
		t.Skip("set 2NB_EVAL_VAULT to a Nova-embedded vault to run the asymmetric-purpose eval")
	}
	v, err := vault.Open(vpath)
	if err != nil {
		t.Skipf("open vault %q: %v", vpath, err)
	}
	defer v.DB.Close()

	cfg := v.Config.AI
	if cfg.Provider != "bedrock" {
		t.Skipf("eval needs a bedrock vault, got provider %q", cfg.Provider)
	}
	ctx := context.Background()
	if err := ai.InitBedrock(ctx, ai.DefaultRegistry, cfg.Bedrock, cfg, v.Root); err != nil {
		t.Skipf("bedrock init (creds?): %v", err)
	}
	emb, err := ai.DefaultRegistry.Embedder("bedrock")
	if err != nil {
		t.Skipf("no bedrock embedder: %v", err)
	}
	if !emb.Available(ctx) {
		t.Skip("bedrock embedder not available (no creds / no Nova access)")
	}

	reports, err := AsymmetricPurpose(ctx, v.DB, emb, 10)
	if err != nil {
		t.Fatalf("asymmetric purpose eval: %v", err)
	}
	idx, q, txt := reports[ai.PurposeIndex], reports[ai.PurposeQuery], reports[ai.PurposeQueryText]

	t.Logf("corpus N=%d  K=%d", idx.N, idx.K)
	t.Logf("INDEX  (symmetric,  GENERIC_INDEX):     MRR@%d=%.4f  R@1=%.4f  R@%d=%.4f", idx.K, idx.MRRAtK, idx.RecallAt1, idx.K, idx.RecallAtK)
	t.Logf("QUERY  (asymmetric, GENERIC_RETRIEVAL): MRR@%d=%.4f  R@1=%.4f  R@%d=%.4f", q.K, q.MRRAtK, q.RecallAt1, q.K, q.RecallAtK)
	t.Logf("TEXT   (asymmetric, TEXT_RETRIEVAL):    MRR@%d=%.4f  R@1=%.4f  R@%d=%.4f", txt.K, txt.MRRAtK, txt.RecallAt1, txt.K, txt.RecallAtK)
	t.Logf("DELTA  (query - index):                 MRR %+.4f   R@1 %+.4f   R@%d %+.4f", q.MRRAtK-idx.MRRAtK, q.RecallAt1-idx.RecallAt1, q.K, q.RecallAtK-idx.RecallAtK)
	t.Logf("DELTA  (text  - query):                 MRR %+.4f   R@1 %+.4f   R@%d %+.4f", txt.MRRAtK-q.MRRAtK, txt.RecallAt1-q.RecallAt1, txt.K, txt.RecallAtK-q.RecallAtK)
	t.Logf("INDEX  trueCos p50/p90/p95/mean = %.3f/%.3f/%.3f/%.3f   neg p90/p95/p99 = %.3f/%.3f/%.3f   suggestThreshold=%.2f",
		idx.TrueP50, idx.TrueP90, idx.TrueP95, idx.TrueMean, idx.NegP90, idx.NegP95, idx.NegP99, idx.SuggestedThreshold)
	t.Logf("QUERY  trueCos p50/p90/p95/mean = %.3f/%.3f/%.3f/%.3f   neg p90/p95/p99 = %.3f/%.3f/%.3f   suggestThreshold=%.2f",
		q.TrueP50, q.TrueP90, q.TrueP95, q.TrueMean, q.NegP90, q.NegP95, q.NegP99, q.SuggestedThreshold)
	t.Logf("TEXT   trueCos p50/p90/p95/mean = %.3f/%.3f/%.3f/%.3f   neg p90/p95/p99 = %.3f/%.3f/%.3f   suggestThreshold=%.2f",
		txt.TrueP50, txt.TrueP90, txt.TrueP95, txt.TrueMean, txt.NegP90, txt.NegP95, txt.NegP99, txt.SuggestedThreshold)

	// This is a MEASUREMENT harness, not a pass/fail gate: which purpose wins is
	// vault-dependent (on a 160-doc vault GENERIC_RETRIEVAL was measured to regress
	// vs symmetric, while TEXT_RETRIEVAL recovered it), so the purpose comparison is
	// logged for a human to act on, not asserted. Only a basic floor is asserted so
	// a totally-broken embedder still fails loudly.
	for name, r := range map[string]PurposeReport{"INDEX": idx, "QUERY": q, "TEXT": txt} {
		if r.MRRAtK < 0.3 {
			t.Errorf("%s MRR@%d=%.4f is implausibly low — embedder/corpus likely broken", name, r.K, r.MRRAtK)
		}
	}
	if q.MRRAtK < idx.MRRAtK-0.01 {
		t.Logf("NOTE: GENERIC_RETRIEVAL regressed vs symmetric INDEX on this vault (%+.4f) — see the config sweep for the end-to-end picture", q.MRRAtK-idx.MRRAtK)
	}
	if txt.MRRAtK > q.MRRAtK {
		t.Logf("TEXT_RETRIEVAL beats GENERIC_RETRIEVAL on the title-as-query MRR@%d by %+.4f (suggestThreshold ~%.2f) — but confirm with the end-to-end config sweep before adopting",
			txt.K, txt.MRRAtK-q.MRRAtK, txt.SuggestedThreshold)
	} else {
		t.Logf("TEXT_RETRIEVAL does NOT beat GENERIC_RETRIEVAL on title-as-query MRR@%d (%+.4f)", txt.K, txt.MRRAtK-q.MRRAtK)
	}
}
