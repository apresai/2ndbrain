package ai

import (
	"context"
	"fmt"
	"math"
	"slices"
	"testing"
)

// TestLiveGeminiEmbed_Over100Chunks verifies against the live Gemini API/Vertex AI
// endpoint that passing >100 chunks in one GeminiProvider.Embed call (mirroring
// large multi-section notes that triggered BatchEmbedContentsRequest 400 errors)
// sub-batches transparently and returns all 125 768d unit-norm vectors in order.
func TestLiveGeminiEmbed_Over100Chunks(t *testing.T) {
	ctx := context.Background()
	cfg := GeminiConfig{}
	if !CheckGeminiCredentials(ctx, cfg) {
		t.Skip("Gemini credentials not configured")
	}

	emb, err := NewGeminiEmbedder(ctx, cfg, DefaultGeminiEmbeddingModel, DefaultGeminiDimensions)
	if err != nil {
		t.Skipf("gemini embedder init failed: %v", err)
	}

	if _, err := emb.Embed(ctx, []string{"capability probe"}, WithPurpose(PurposeQuery)); err != nil {
		t.Skipf("gemini embedding capability probe failed: %v", err)
	}

	const totalChunks = 125
	chunks := make([]string, totalChunks)
	for i := range chunks {
		chunks[i] = fmt.Sprintf(
			"## Section %d\nField observation #%d documenting subterranean hydrothermal vent tubeworm mineral accretion.",
			i+1, i+1,
		)
	}

	vecs, err := emb.Embed(ctx, chunks, WithPurpose(PurposeIndex), WithTitle("Large Multi-Section Note"))
	if err != nil {
		t.Fatalf("live Gemini Embed(%d chunks) failed: %v", totalChunks, err)
	}
	if len(vecs) != totalChunks {
		t.Fatalf("len(vecs) = %d, want %d", len(vecs), totalChunks)
	}

	for i, v := range vecs {
		if len(v) != DefaultGeminiDimensions {
			t.Fatalf("vec[%d] len = %d, want %d", i, len(v), DefaultGeminiDimensions)
		}
		var sqSum float64
		for _, x := range v {
			if math.IsNaN(float64(x)) || math.IsInf(float64(x), 0) {
				t.Fatalf("vec[%d] contains non-finite component %v", i, x)
			}
			sqSum += float64(x) * float64(x)
		}
		norm := math.Sqrt(sqSum)
		if math.Abs(norm-1.0) > 1e-4 {
			t.Fatalf("vec[%d] L2 norm = %f, want ~1.0", i, norm)
		}
	}

	if slices.Equal(vecs[0], vecs[totalChunks-1]) {
		t.Error("expected distinct vectors for Section 1 and Section 125")
	}
}
