package eval

import (
	"context"
	"testing"

	"github.com/apresai/2ndbrain/internal/ai"
)

// TestNovaCrossLingual_Bedrock demonstrates Nova-2's multilingual shared
// embedding space (its 200-language strength): the same concept across six
// languages embeds far closer to the English anchor than an unrelated concept
// does. Credential-gated (real Bedrock, no mocks):
//
//	source ~/.secrets/shell.zsh
//	export AWS_BEARER_TOKEN_BEDROCK="$SA_AWS_BEARER_TOKEN_BEDROCK"
//	go test ./internal/eval/ -run CrossLingual -v
func TestNovaCrossLingual_Bedrock(t *testing.T) {
	cfg := ai.DefaultAIConfig()
	ctx := context.Background()
	if err := ai.InitBedrock(ctx, ai.DefaultRegistry, cfg.Bedrock, cfg); err != nil {
		t.Skipf("bedrock init (creds?): %v", err)
	}
	emb, err := ai.DefaultRegistry.Embedder("bedrock")
	if err != nil {
		t.Skipf("no bedrock embedder: %v", err)
	}
	if !emb.Available(ctx) {
		t.Skip("bedrock embedder not available (no creds / no Nova access)")
	}

	// Same concept, six languages (English anchor first).
	concept := []string{
		"machine learning and artificial intelligence",
		"apprentissage automatique et intelligence artificielle", // French
		"aprendizaje automático e inteligencia artificial",       // Spanish
		"maschinelles Lernen und künstliche Intelligenz",         // German
		"機械学習と人工知能",                                              // Japanese
		"机器学习与人工智能",                                              // Chinese
	}
	unrelated := "a recipe for baking sourdough bread"

	vecs, err := emb.Embed(ctx, concept, ai.WithPurpose(ai.PurposeIndex))
	if err != nil {
		t.Fatalf("embed concept: %v", err)
	}
	neg, err := emb.Embed(ctx, []string{unrelated}, ai.WithPurpose(ai.PurposeIndex))
	if err != nil {
		t.Fatalf("embed unrelated: %v", err)
	}

	anchor, an := vecs[0], l2(vecs[0])
	negCos := cosine(anchor, neg[0], an, l2(neg[0]))
	t.Logf("EN <-> unrelated baseline: cos=%.3f", negCos)
	for i := 1; i < len(concept); i++ {
		c := cosine(anchor, vecs[i], an, l2(vecs[i]))
		t.Logf("EN <-> lang[%d]: cos=%.3f", i, c)
		if c <= negCos {
			t.Errorf("cross-lingual cos %.3f (lang %d) not above unrelated baseline %.3f — multilingual space failed", c, i, negCos)
		}
	}
}
