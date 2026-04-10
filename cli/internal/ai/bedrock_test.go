package ai

import (
	"context"
	"testing"
)

func requireBedrock(t *testing.T) (*BedrockEmbedder, *BedrockGenerator) {
	t.Helper()
	ctx := context.Background()
	cfg := BedrockConfig{Profile: "default", Region: "us-east-1"}

	embedder, err := NewBedrockEmbedder(ctx, cfg, "amazon.nova-2-multimodal-embeddings-v1:0", 1024)
	if err != nil {
		t.Skipf("AWS credentials not configured: %v", err)
	}

	gen, err := NewBedrockGenerator(ctx, cfg, "us.anthropic.claude-haiku-4-5-20251001-v1:0")
	if err != nil {
		t.Skipf("AWS credentials not configured: %v", err)
	}

	return embedder, gen
}

func TestBedrockEmbed(t *testing.T) {
	embedder, _ := requireBedrock(t)

	vecs, err := embedder.Embed(context.Background(), []string{
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
	if len(vecs[0]) != 1024 {
		t.Errorf("dimensions = %d, want 1024", len(vecs[0]))
	}
	t.Logf("embedding dimensions: %d", len(vecs[0]))
	t.Logf("first 5 values: %v", vecs[0][:5])
}

func TestBedrockGenerate(t *testing.T) {
	_, gen := requireBedrock(t)

	resp, err := gen.Generate(context.Background(), "What is 2+2? Reply with just the number.", GenOpts{
		MaxTokens:   10,
		Temperature: 0,
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if resp == "" {
		t.Error("empty response")
	}
	t.Logf("response: %q", resp)
}

func TestBedrockGenerateWithSystemPrompt(t *testing.T) {
	_, gen := requireBedrock(t)

	resp, err := gen.Generate(context.Background(), "What color is the sky?", GenOpts{
		MaxTokens:    50,
		Temperature:  0,
		SystemPrompt: "Reply with exactly one word.",
	})
	if err != nil {
		t.Fatalf("Generate with system prompt: %v", err)
	}
	if resp == "" {
		t.Error("empty response")
	}
	t.Logf("response: %q", resp)
}

func TestBedrockGenerateNovaMicro(t *testing.T) {
	ctx := context.Background()
	cfg := BedrockConfig{Profile: "default", Region: "us-east-1"}

	gen, err := NewBedrockGenerator(ctx, cfg, "amazon.nova-micro-v1:0")
	if err != nil {
		t.Skipf("AWS credentials not configured: %v", err)
	}

	resp, err := gen.Generate(ctx, "What is 2+2? Reply with just the number.", GenOpts{
		MaxTokens:    10,
		Temperature:  0,
		SystemPrompt: "You are a calculator.",
	})
	if err != nil {
		t.Fatalf("Generate (Nova Micro): %v", err)
	}
	if resp == "" {
		t.Error("empty response from Nova Micro")
	}
	t.Logf("Nova Micro response: %q", resp)
}

func TestBedrockAvailable(t *testing.T) {
	embedder, gen := requireBedrock(t)

	// Available() uses a 5s timeout internally, but Bedrock cold starts
	// can be slow. Test by calling Embed/Generate first (which proves
	// the provider works), then checking Available() returns the cached true.
	if _, err := embedder.Embed(context.Background(), []string{"test"}); err != nil {
		t.Fatalf("embed failed: %v", err)
	}
	if !embedder.Available(context.Background()) {
		t.Error("embedder should be available after successful embed")
	}

	if _, err := gen.Generate(context.Background(), "hi", GenOpts{MaxTokens: 1}); err != nil {
		t.Fatalf("generate failed: %v", err)
	}
	if !gen.Available(context.Background()) {
		t.Error("generator should be available after successful generate")
	}
}

func TestBedrockListModels(t *testing.T) {
	embedder, gen := requireBedrock(t)

	embedModels, err := embedder.ListModels(context.Background())
	if err != nil {
		t.Fatalf("embedder ListModels: %v", err)
	}
	if len(embedModels) == 0 {
		t.Error("no embed models")
	}
	if embedModels[0].Type != "embedding" {
		t.Errorf("type = %q, want embedding", embedModels[0].Type)
	}

	genModels, err := gen.ListModels(context.Background())
	if err != nil {
		t.Fatalf("generator ListModels: %v", err)
	}
	if len(genModels) == 0 {
		t.Error("no gen models")
	}
	if genModels[0].Type != "generation" {
		t.Errorf("type = %q, want generation", genModels[0].Type)
	}
}
