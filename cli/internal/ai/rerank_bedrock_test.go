package ai

import (
	"context"
	"testing"
)

// TestBedrockReranker_Live exercises the real Bedrock Rerank API (no mocks, per
// the project policy). It skips when Bedrock isn't reachable (no credentials /
// wrong region). Cohere Rerank 3.5 is us-east-1 in-region only.
func TestBedrockReranker_Live(t *testing.T) {
	ctx := context.Background()
	rr, err := NewBedrockReranker(ctx, BedrockConfig{Region: "us-east-1"}, DefaultRerankModel)
	if err != nil {
		t.Skipf("bedrock config unavailable: %v", err)
	}
	if !rr.Available(ctx) {
		t.Skip("bedrock not reachable (no credentials); skipping live rerank test")
	}

	// The auth-relevant doc (index 1) should rank first for an auth query — a
	// property a cross-encoder gets right regardless of lexical overlap.
	docs := []string{
		"Bananas are a yellow tropical fruit rich in potassium.",
		"User authentication is handled with JWT tokens and OAuth 2.0 flows.",
		"The weather today is sunny with a light breeze.",
	}
	hits, err := rr.Rerank(ctx, "how does login and authentication work?", docs, 3)
	if err != nil {
		t.Fatalf("rerank: %v", err)
	}
	if len(hits) == 0 {
		t.Fatal("expected rerank hits, got none")
	}
	if hits[0].Index != 1 {
		t.Errorf("expected the auth doc (index 1) ranked first, got index %d (hits=%+v)", hits[0].Index, hits)
	}
	// Scores are Bedrock-normalized to [0,1] and returned descending.
	for i := 1; i < len(hits); i++ {
		if hits[i].Score > hits[i-1].Score {
			t.Errorf("hits not in descending score order at %d: %v", i, hits)
		}
	}
}
