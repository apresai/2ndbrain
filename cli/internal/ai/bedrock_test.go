package ai

import (
	"context"
	"os"
	"testing"
)

// TestBedrockEmbedLive tests the actual Bedrock Nova Embeddings API.
// Skipped unless BEDROCK_LIVE_TEST=1 is set.
func TestBedrockEmbedLive(t *testing.T) {
	if os.Getenv("BEDROCK_LIVE_TEST") != "1" {
		t.Skip("set BEDROCK_LIVE_TEST=1 to run live Bedrock tests")
	}

	ctx := context.Background()
	cfg := BedrockConfig{Profile: "default", Region: "us-east-1"}
	embedder, err := NewBedrockEmbedder(ctx, cfg, "amazon.nova-2-multimodal-embeddings-v1:0", 1024)
	if err != nil {
		t.Fatalf("NewBedrockEmbedder: %v", err)
	}

	vecs, err := embedder.Embed(ctx, []string{
		"How does authentication work in our system?",
		"Database schema for user management",
	})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}

	if len(vecs) != 2 {
		t.Fatalf("got %d vectors, want 2", len(vecs))
	}
	if len(vecs[0]) == 0 {
		t.Error("first vector is empty")
	}
	t.Logf("embedding dimensions: %d", len(vecs[0]))
	t.Logf("first 5 values: %v", vecs[0][:5])
}

// TestBedrockGenerateLive tests the actual Bedrock Claude API.
// Skipped unless BEDROCK_LIVE_TEST=1 is set.
func TestBedrockGenerateLive(t *testing.T) {
	if os.Getenv("BEDROCK_LIVE_TEST") != "1" {
		t.Skip("set BEDROCK_LIVE_TEST=1 to run live Bedrock tests")
	}

	ctx := context.Background()
	cfg := BedrockConfig{Profile: "default", Region: "us-east-1"}
	gen, err := NewBedrockGenerator(ctx, cfg, "us.anthropic.claude-haiku-4-5-20251001-v1:0")
	if err != nil {
		t.Fatalf("NewBedrockGenerator: %v", err)
	}

	resp, err := gen.Generate(ctx, "What is 2+2? Reply with just the number.", GenOpts{
		MaxTokens:   10,
		Temperature: 0,
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	t.Logf("response: %q", resp)
	if resp == "" {
		t.Error("empty response")
	}
}
