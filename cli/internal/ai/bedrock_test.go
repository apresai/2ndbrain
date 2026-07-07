package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	bedrocktypes "github.com/aws/aws-sdk-go-v2/service/bedrock/types"
	"github.com/aws/smithy-go"
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
		{"us.twelvelabs.marengo-embed-2-7-v1:0", fmtTwelveLabs27},
		{"us.twelvelabs.marengo-embed-3-0-v1:0", fmtTwelveLabs30},
	}
	for _, tt := range tests {
		got := detectEmbedFormat(tt.modelID)
		if got != tt.want {
			t.Errorf("detectEmbedFormat(%q) = %d, want %d", tt.modelID, got, tt.want)
		}
	}
}

func TestBedrockModelSupported(t *testing.T) {
	tests := []struct {
		name      string
		modelID   string
		modelType string
		wantOK    bool
		wantText  string
	}{
		{"nova micro generation", "amazon.nova-micro-v1:0", "generation", true, ""},
		{"claude generation", "us.anthropic.claude-3-5-haiku-20241022-v1:0", "generation", true, ""},
		{"marengo 27 embedding", "us.twelvelabs.marengo-embed-2-7-v1:0", "embedding", true, ""},
		{"marengo 30 embedding", "us.twelvelabs.marengo-embed-3-0-v1:0", "embedding", true, ""},
		{"nova canvas rejected", "amazon.nova-canvas-v1:0", "generation", false, "image-generation"},
		{"nova reel rejected", "amazon.nova-reel-v1:0", "generation", false, "video-generation"},
		{"rerank rejected as generation", "cohere.rerank-v3-5:0", "generation", false, "reranker"},
		{"rerank accepted as rerank", "cohere.rerank-v3-5:0", "rerank", true, ""},
		{"pegasus rejected", "us.twelvelabs.pegasus-1-2-v1:0", "generation", false, "video-understanding"},
		{"titan image embed rejected", "amazon.titan-embed-image-v1:0", "embedding", false, "image input"},
		{"palmyra vision rejected", "writer.palmyra-vision-7b", "generation", false, "Palmyra Vision"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotOK, gotReason := bedrockModelSupported(tt.modelID, tt.modelType)
			if gotOK != tt.wantOK {
				t.Fatalf("bedrockModelSupported(%q, %q) ok = %v, want %v (reason %q)", tt.modelID, tt.modelType, gotOK, tt.wantOK, gotReason)
			}
			if tt.wantText != "" && !strings.Contains(gotReason, tt.wantText) {
				t.Fatalf("bedrockModelSupported(%q, %q) reason = %q, want substring %q", tt.modelID, tt.modelType, gotReason, tt.wantText)
			}
		})
	}
}

func TestParseTwelveLabsEmbedding(t *testing.T) {
	tests := []struct {
		name string
		body string
		want []float32
	}{
		{
			name: "top-level embedding",
			body: `{"embedding":[0.1,0.2]}`,
			want: []float32{0.1, 0.2},
		},
		{
			name: "object data embedding",
			body: `{"data":{"embedding":[0.3,0.4]}}`,
			want: []float32{0.3, 0.4},
		},
		{
			name: "array data embedding",
			body: `{"data":[{"embedding":[0.5,0.6]}]}`,
			want: []float32{0.5, 0.6},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseTwelveLabsEmbedding([]byte(tt.body))
			if err != nil {
				t.Fatalf("parseTwelveLabsEmbedding() error = %v", err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("len(got) = %d, want %d", len(got), len(tt.want))
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("got[%d] = %v, want %v", i, got[i], tt.want[i])
				}
			}
		})
	}
}

// TestNovaEmbedResponseTruncation guards the decode of the truncatedCharLength
// field that embedNova uses as its over-long-input tripwire: a JSON-tag or
// shape regression would silently disable the truncation warning. Pure decode
// test, no live call — mirrors TestParseTwelveLabsEmbedding.
func TestNovaEmbedResponseTruncation(t *testing.T) {
	tests := []struct {
		name          string
		body          string
		wantEmbedding []float32
		wantTruncated *int
	}{
		{
			name:          "normal response omits truncatedCharLength",
			body:          `{"embeddings":[{"embeddingType":"TEXT","embedding":[0.1,0.2]}]}`,
			wantEmbedding: []float32{0.1, 0.2},
			wantTruncated: nil,
		},
		{
			name:          "truncated response carries truncatedCharLength",
			body:          `{"embeddings":[{"embeddingType":"TEXT","embedding":[0.3,0.4],"truncatedCharLength":12645}]}`,
			wantEmbedding: []float32{0.3, 0.4},
			wantTruncated: Ptr(12645),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var resp novaEmbedResponse
			if err := json.Unmarshal([]byte(tt.body), &resp); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if len(resp.Embeddings) != 1 {
				t.Fatalf("got %d embeddings, want 1", len(resp.Embeddings))
			}
			got := resp.Embeddings[0]
			if len(got.Embedding) != len(tt.wantEmbedding) {
				t.Fatalf("embedding len = %d, want %d", len(got.Embedding), len(tt.wantEmbedding))
			}
			switch {
			case tt.wantTruncated == nil && got.TruncatedCharLength != nil:
				t.Errorf("TruncatedCharLength = %d, want nil (no truncation)", *got.TruncatedCharLength)
			case tt.wantTruncated != nil && got.TruncatedCharLength == nil:
				t.Errorf("TruncatedCharLength = nil, want %d", *tt.wantTruncated)
			case tt.wantTruncated != nil && *got.TruncatedCharLength != *tt.wantTruncated:
				t.Errorf("TruncatedCharLength = %d, want %d", *got.TruncatedCharLength, *tt.wantTruncated)
			}
		})
	}
}

func TestBedrockEmbedTwelveLabsMarengo27(t *testing.T) {
	testBedrockMarengoEmbed(t, "us.twelvelabs.marengo-embed-2-7-v1:0")
}

func TestBedrockEmbedTwelveLabsMarengo30(t *testing.T) {
	testBedrockMarengoEmbed(t, "us.twelvelabs.marengo-embed-3-0-v1:0")
}

func testBedrockMarengoEmbed(t *testing.T, modelID string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cfg := BedrockConfig{Profile: "default", Region: "us-east-1"}
	if !CheckBedrockCredentials(ctx, cfg) {
		t.Skip("AWS credentials not configured for Bedrock")
	}

	embedder, err := NewBedrockEmbedder(ctx, cfg, modelID, 0)
	if err != nil {
		t.Skipf("Bedrock embedder unavailable: %v", err)
	}

	vecs, err := embedder.Embed(ctx, []string{"Bedrock Marengo text embedding probe"})
	if err != nil {
		msg := err.Error()
		switch {
		case strings.Contains(msg, "AccessDenied"),
			strings.Contains(msg, "not authorized"),
			strings.Contains(msg, "ModelNotReady"),
			strings.Contains(msg, "validationexception"),
			strings.Contains(msg, "ValidationException"),
			strings.Contains(msg, "ResourceNotFoundException"):
			t.Skipf("Marengo model not available in this account/region: %v", err)
		default:
			t.Fatalf("Embed(%s): %v", modelID, err)
		}
	}
	if len(vecs) != 1 {
		t.Fatalf("got %d vectors, want 1", len(vecs))
	}
	if len(vecs[0]) == 0 {
		t.Fatalf("empty embedding vector for %s", modelID)
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

func TestBedrockTextInputHelpers(t *testing.T) {
	summaryText := bedrocktypes.FoundationModelSummary{
		InputModalities: []bedrocktypes.ModelModality{bedrocktypes.ModelModalityText},
	}
	if !bedrockSummaryHasTextInput(summaryText) {
		t.Fatal("expected text input summary to be accepted")
	}

	summaryImage := bedrocktypes.FoundationModelSummary{
		InputModalities: []bedrocktypes.ModelModality{bedrocktypes.ModelModalityImage},
	}
	if bedrockSummaryHasTextInput(summaryImage) {
		t.Fatal("expected non-text summary to be rejected")
	}

	detailsText := &bedrocktypes.FoundationModelDetails{
		InputModalities:  []bedrocktypes.ModelModality{bedrocktypes.ModelModalityText},
		OutputModalities: []bedrocktypes.ModelModality{bedrocktypes.ModelModalityEmbedding},
	}
	if !bedrockDetailsHasTextInput(detailsText) {
		t.Fatal("expected text input details to be accepted")
	}
	if got := bedrockModelTypeFromDetails(detailsText); got != "embedding" {
		t.Fatalf("bedrockModelTypeFromDetails = %q, want embedding", got)
	}

	detailsImage := &bedrocktypes.FoundationModelDetails{
		InputModalities: []bedrocktypes.ModelModality{bedrocktypes.ModelModalityImage},
	}
	if bedrockDetailsHasTextInput(detailsImage) {
		t.Fatal("expected non-text details to be rejected")
	}
}

// --- isBedrockModelLifecycleBlocked pure-logic tests ---

type fakeAPIError struct {
	code    string
	message string
	fault   smithy.ErrorFault
}

func (e *fakeAPIError) Error() string                 { return fmt.Sprintf("%s: %s", e.code, e.message) }
func (e *fakeAPIError) ErrorCode() string             { return e.code }
func (e *fakeAPIError) ErrorMessage() string          { return e.message }
func (e *fakeAPIError) ErrorFault() smithy.ErrorFault { return e.fault }

func TestIsBedrockModelLifecycleBlocked(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "end of its life message",
			err:  &fakeAPIError{code: "ResourceNotFoundException", message: "Model is at end of its life"},
			want: true,
		},
		{
			name: "marked as legacy message",
			err:  &fakeAPIError{code: "ResourceNotFoundException", message: "Model is marked as legacy"},
			want: true,
		},
		{
			name: "RNF without lifecycle message",
			err:  &fakeAPIError{code: "ResourceNotFoundException", message: "Model not found"},
			want: false,
		},
		{
			name: "non-RNF error",
			err:  &fakeAPIError{code: "ValidationException", message: "end of its life"},
			want: false,
		},
		{
			name: "non-api error",
			err:  errors.New("network timeout"),
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := isBedrockModelLifecycleBlocked(tc.err)
			if got != tc.want {
				t.Errorf("isBedrockModelLifecycleBlocked = %v, want %v", got, tc.want)
			}
		})
	}
}

// --- bedrockModelSupported static allowlist tests ---

func TestBedrockModelSupportedStaticAllowlist(t *testing.T) {
	cases := []struct {
		modelID   string
		modelType string
		wantOK    bool
	}{
		// generation — supported
		{"anthropic.claude-3-5-sonnet-20241022-v2:0", "generation", true},
		{"amazon.nova-micro-v1:0", "generation", true},
		{"meta.llama3-8b-instruct-v1:0", "generation", true},
		{"mistral.mistral-7b-instruct-v0:2", "generation", true},
		// generation — blocked
		{"amazon.nova-canvas-v1:0", "generation", false},
		{"amazon.nova-reel-v1:0", "generation", false},
		{"stability.stable-image-core-v1:0", "generation", false},
		{"cohere.rerank-english-v3:0", "generation", false},
		{"twelvelabs.pegasus-1:0", "generation", false},
		// embedding — supported
		{"amazon.titan-embed-text-v2:0", "embedding", true},
		{"cohere.embed-english-v3:0", "embedding", true},
		{"amazon.titan-embed-g1-text-02", "embedding", true},
		// embedding — blocked
		{"amazon.titan-embed-image-v1", "embedding", false},
		{"some.unknown-model", "embedding", false},
	}
	for _, tc := range cases {
		t.Run(tc.modelID+"/"+tc.modelType, func(t *testing.T) {
			ok, reason := bedrockModelSupported(tc.modelID, tc.modelType)
			if ok != tc.wantOK {
				t.Errorf("bedrockModelSupported(%q, %q) = %v (reason: %q), want %v",
					tc.modelID, tc.modelType, ok, reason, tc.wantOK)
			}
		})
	}
}

// TestBuiltinBedrockAnthropicModelsStillListed is the catalog-freshness guard:
// every builtin Bedrock Anthropic ID must still appear in the live
// ListFoundationModels/ListInferenceProfiles output. Cred-gated; run it (and
// the pricing-coverage test in live_pricing_test.go) before each release so
// the curated catalog can't silently go stale against AWS again.
func TestBuiltinBedrockAnthropicModelsStillListed(t *testing.T) {
	requireBedrock(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cfg := BedrockConfig{Profile: "default", Region: "us-east-1"}
	listed, err := ListBedrockVendorModels(ctx, cfg)
	if err != nil {
		t.Skipf("ListBedrockVendorModels: %v", err)
	}
	live := map[string]bool{}
	for _, m := range listed {
		live[m.ID] = true
		live[inferenceProfileBaseID(m.ID)] = true
	}
	for _, m := range BuiltinCatalog() {
		if m.Provider != "bedrock" || !strings.Contains(m.ID, "anthropic") {
			continue
		}
		if !live[m.ID] && !live[inferenceProfileBaseID(m.ID)] {
			t.Errorf("builtin catalog entry %s no longer appears in the live Bedrock listing — refresh the catalog", m.ID)
		}
	}
}
