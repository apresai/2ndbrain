package ai

import (
	"context"
	"strings"
	"testing"
)

// TestRerankRegion_PrefersModelPin verifies the region routing (no AWS calls):
// the Cohere reranker's builtin us-east-1 pin wins over a differently
// configured ai.bedrock.region, an unpinned model keeps the configured
// region, and the chosen region lands in the foundation-model ARN.
func TestRerankRegion_PrefersModelPin(t *testing.T) {
	setupHome(t) // isolate from any real user catalog

	// The builtin catalog pins Cohere Rerank 3.5 to us-east-1, so a vault
	// configured for another region must still route rerank calls there.
	got := rerankRegion(BedrockConfig{Region: "us-west-2"}, DefaultRerankModel)
	if got != "us-east-1" {
		t.Errorf("pinned model: rerankRegion = %q, want us-east-1", got)
	}

	// A model with no catalog pin keeps the configured region.
	if got := rerankRegion(BedrockConfig{Region: "us-west-2"}, "vendor.custom-rerank-v1:0"); got != "us-west-2" {
		t.Errorf("unpinned model: rerankRegion = %q, want us-west-2", got)
	}

	// The chosen region is what modelARN embeds.
	rr := &BedrockReranker{
		model:  DefaultRerankModel,
		region: rerankRegion(BedrockConfig{Region: "us-west-2"}, DefaultRerankModel),
	}
	arn := rr.modelARN()
	if !strings.Contains(arn, ":us-east-1:") {
		t.Errorf("modelARN should carry the pinned region, got %q", arn)
	}

	// A full ARN passes through untouched regardless of region config.
	passthrough := &BedrockReranker{
		model:  "arn:aws:bedrock:eu-west-1::foundation-model/cohere.rerank-v3-5:0",
		region: "us-west-2",
	}
	if got := passthrough.modelARN(); got != passthrough.model {
		t.Errorf("full ARN should pass through, got %q", got)
	}
}

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
