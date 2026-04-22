package ai

import (
	"context"
	"strings"
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
		MaxTokens: 10,
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

// ── Pure logic tests — no API calls ───────────────────────────────────────

func TestDetectEmbedFormat(t *testing.T) {
	tests := []struct {
		modelID string
		want    bedrockEmbedFmt
	}{
		{"amazon.nova-2-multimodal-embeddings-v1:0", fmtNova},
		{"amazon.nova-pro-v1:0", fmtNova},
		{"amazon.titan-embed-text-v1", fmtTitanV1},
		{"amazon.titan-embed-text-v2:0", fmtTitanV2},
		{"amazon.titan-embed-image-v1", fmtTitanImage},
		{"amazon.titan-embed-image-v1:0", fmtTitanImage},
		{"amazon.titan-embed-g1-text-02", fmtTitanV1},
		{"cohere.embed-english-v3", fmtCohere},
		{"cohere.embed-multilingual-v3", fmtCohere},
		{"cohere.embed-english-v4", fmtCohere},
	}
	for _, tt := range tests {
		got := detectEmbedFormat(tt.modelID)
		if got != tt.want {
			t.Errorf("detectEmbedFormat(%q) = %d, want %d", tt.modelID, got, tt.want)
		}
	}
}

func TestGeoPrefix(t *testing.T) {
	tests := []struct {
		region string
		want   string
	}{
		{"us-east-1", "us."},
		{"us-west-2", "us."},
		{"eu-west-1", "eu."},
		{"eu-central-1", "eu."},
		{"ap-southeast-1", "ap."},
		{"ap-northeast-1", "ap."},
		{"me-south-1", ""},
		{"sa-east-1", ""},
		{"ca-central-1", ""},
	}
	for _, tt := range tests {
		got := geoPrefix(tt.region)
		if got != tt.want {
			t.Errorf("geoPrefix(%q) = %q, want %q", tt.region, got, tt.want)
		}
	}
}

func TestInferenceProfileBaseID(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"us.anthropic.claude-3-5-haiku-20241022-v1:0", "anthropic.claude-3-5-haiku-20241022-v1:0"},
		{"eu.meta.llama3-8b-instruct-v1:0", "meta.llama3-8b-instruct-v1:0"},
		{"ap.amazon.nova-pro-v1:0", "amazon.nova-pro-v1:0"},
		{"global.anthropic.claude-haiku-4-5-20251001-v1:0", "anthropic.claude-haiku-4-5-20251001-v1:0"},
		{"amazon.nova-micro-v1:0", "amazon.nova-micro-v1:0"}, // no prefix → unchanged
	}
	for _, tt := range tests {
		got := inferenceProfileBaseID(tt.input)
		if got != tt.want {
			t.Errorf("inferenceProfileBaseID(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestInferProviderGlobal(t *testing.T) {
	tests := []string{
		"global.anthropic.claude-haiku-4-5-20251001-v1:0",
		"global.anthropic.claude-sonnet-4-5-20250929-v1:0",
	}
	for _, id := range tests {
		got := InferProvider(id)
		if got != "bedrock" {
			t.Errorf("InferProvider(%q) = %q, want bedrock", id, got)
		}
	}
}

func TestDetectEmbedFormatGeoPrefix(t *testing.T) {
	tests := []struct {
		modelID string
		want    bedrockEmbedFmt
	}{
		{"us.cohere.embed-v4:0", fmtCohere},
		{"global.cohere.embed-v4:0", fmtCohere},
		{"eu.cohere.embed-multilingual-v3", fmtCohere},
		{"us.amazon.titan-embed-text-v2:0", fmtTitanV2},
		{"ap.amazon.nova-2-multimodal-embeddings-v1:0", fmtNova},
	}
	for _, tt := range tests {
		got := detectEmbedFormat(tt.modelID)
		if got != tt.want {
			t.Errorf("detectEmbedFormat(%q) = %d, want %d", tt.modelID, got, tt.want)
		}
	}
}

func TestIsContextWindowVariantID(t *testing.T) {
	yes := []string{
		"amazon.nova-lite-v1:0:24k",
		"amazon.nova-micro-v1:0:128k",
		"amazon.nova-premier-v1:0:1000k",
		"amazon.nova-premier-v1:0:mm",
		"amazon.titan-embed-text-v1:2:8k",
		"amazon.titan-embed-text-v2:0:8k",
		"cohere.embed-english-v3:0:512",
		"anthropic.claude-3-haiku-20240307-v1:0:200k",
		"anthropic.claude-3-haiku-20240307-v1:0:48k",
	}
	no := []string{
		"amazon.nova-lite-v1:0",
		"amazon.titan-embed-text-v2:0",
		"anthropic.claude-3-haiku-20240307-v1:0",
		"cohere.embed-english-v3",
		"us.anthropic.claude-haiku-4-5-20251001-v1:0",
		"amazon.nova-micro-v1:0",
	}
	for _, id := range yes {
		if !isContextWindowVariantID(id) {
			t.Errorf("isContextWindowVariantID(%q) = false, want true", id)
		}
	}
	for _, id := range no {
		if isContextWindowVariantID(id) {
			t.Errorf("isContextWindowVariantID(%q) = true, want false", id)
		}
	}
}

// ── Integration tests — require AWS credentials ────────────────────────────

func TestBedrockEmbedTitanV2(t *testing.T) {
	ctx := context.Background()
	cfg := BedrockConfig{Profile: "default", Region: "us-east-1"}

	embedder, err := NewBedrockEmbedder(ctx, cfg, "amazon.titan-embed-text-v2:0", 1024)
	if err != nil {
		t.Skipf("AWS credentials not configured: %v", err)
	}

	vecs, err := embedder.Embed(ctx, []string{
		"The quick brown fox jumps over the lazy dog.",
		"Semantic search with Titan v2 embeddings.",
	})
	if err != nil {
		t.Fatalf("Embed (Titan v2): %v", err)
	}
	if len(vecs) != 2 {
		t.Fatalf("got %d vectors, want 2", len(vecs))
	}
	if len(vecs[0]) != 1024 {
		t.Errorf("dims = %d, want 1024", len(vecs[0]))
	}
	allZero := true
	for _, v := range vecs[0] {
		if v != 0 {
			allZero = false
			break
		}
	}
	if allZero {
		t.Error("embedding is all zeros")
	}
	t.Logf("Titan v2 embedding dims: %d", len(vecs[0]))
}

func TestBedrockEmbedCohereEnglish(t *testing.T) {
	ctx := context.Background()
	cfg := BedrockConfig{Profile: "default", Region: "us-east-1"}

	embedder, err := NewBedrockEmbedder(ctx, cfg, "cohere.embed-english-v3", 1024)
	if err != nil {
		t.Skipf("AWS credentials not configured: %v", err)
	}

	// Test batching with more texts than a single call would handle.
	texts := []string{
		"First document for Cohere embedding test.",
		"Second document about semantic search.",
		"Third document for batching verification.",
	}
	vecs, err := embedder.Embed(ctx, texts)
	if err != nil {
		t.Fatalf("Embed (Cohere): %v", err)
	}
	if len(vecs) != len(texts) {
		t.Fatalf("got %d vectors, want %d", len(vecs), len(texts))
	}
	if len(vecs[0]) != 1024 {
		t.Errorf("dims = %d, want 1024", len(vecs[0]))
	}
	t.Logf("Cohere embed dims: %d", len(vecs[0]))
}

func TestListBedrockVendorModelsInferenceProfiles(t *testing.T) {
	ctx := context.Background()
	cfg := BedrockConfig{Profile: "default", Region: "us-east-1"}

	models, err := ListBedrockVendorModels(ctx, cfg)
	if err != nil {
		t.Skipf("AWS credentials not configured: %v", err)
	}
	if len(models) == 0 {
		t.Fatal("no models returned")
	}

	// Should include us.anthropic.* inference profile IDs.
	hasUsProfile := false
	hasBareAnthropic := false
	for _, m := range models {
		if strings.HasPrefix(m.ID, "us.anthropic.") {
			hasUsProfile = true
		}
		// Bare anthropic.* base IDs should be suppressed when inference profiles cover them.
		if strings.HasPrefix(m.ID, "anthropic.claude-3-5") || strings.HasPrefix(m.ID, "anthropic.claude-haiku") {
			hasBareAnthropic = true
		}
	}
	if !hasUsProfile {
		t.Error("expected us.anthropic.* inference profile IDs in discovery output")
	}
	if hasBareAnthropic {
		t.Error("bare anthropic.claude-3-5* base IDs should be suppressed by inference profile dedup")
	}
	t.Logf("discovered %d models", len(models))
}
