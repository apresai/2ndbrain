package ai

import (
	"context"
	"math"
	"os"
	"testing"
)

func TestTruncateNormalize(t *testing.T) {
	// dim < len: slice + L2-normalize. [3,4,x,x] -> [0.6,0.8].
	got := truncateNormalize([]float32{3, 4, 9, 9}, 2)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if math.Abs(float64(got[0])-0.6) > 1e-6 || math.Abs(float64(got[1])-0.8) > 1e-6 {
		t.Errorf("normalized = %v, want [0.6 0.8]", got)
	}
	var norm float64
	for _, x := range got {
		norm += float64(x) * float64(x)
	}
	if math.Abs(math.Sqrt(norm)-1) > 1e-6 {
		t.Errorf("result not unit-length: norm=%v", math.Sqrt(norm))
	}

	// dim >= len: unchanged.
	full := []float32{1, 2, 3}
	if out := truncateNormalize(full, 3); len(out) != 3 || out[0] != 1 {
		t.Errorf("dim>=len should return input unchanged, got %v", out)
	}
	if out := truncateNormalize(full, 0); len(out) != 3 {
		t.Errorf("dim<=0 should return input unchanged, got %v", out)
	}

	// all-zero prefix: no NaN, returns zeros.
	z := truncateNormalize([]float32{0, 0, 5}, 2)
	for _, x := range z {
		if math.IsNaN(float64(x)) || x != 0 {
			t.Errorf("zero prefix should stay zero (no NaN), got %v", z)
		}
	}
}

func TestInitLlamaRegisters(t *testing.T) {
	reg := NewRegistry()
	err := InitLlama(context.Background(), reg, LlamaConfig{}, AIConfig{
		EmbeddingModel:  "embeddinggemma-300m",
		GenerationModel: "gemma4-e4b",
		Dimensions:      768,
	})
	if err != nil {
		t.Fatal(err)
	}
	e, err := reg.Embedder(llamaProviderName)
	if err != nil {
		t.Fatalf("embedder not registered: %v", err)
	}
	if e.Name() != llamaProviderName || e.Dimensions() != 768 {
		t.Errorf("embedder = %q/%d, want %q/768", e.Name(), e.Dimensions(), llamaProviderName)
	}
	g, err := reg.Generator(llamaProviderName)
	if err != nil {
		t.Fatalf("generator not registered: %v", err)
	}
	if g.Name() != llamaProviderName {
		t.Errorf("generator name = %q", g.Name())
	}
	// UsageGenerator is implemented so RAG records real token counts.
	if _, ok := g.(UsageGenerator); !ok {
		t.Error("LlamaGenerator should implement UsageGenerator")
	}
}

// requireLlamaEndpoint skips unless a real llama-server is reachable. Point the
// env vars at a manually-started server:
//
//	llama-server -m embeddinggemma.gguf --embeddings --pooling mean --port 8081
//	llama-server -m gemma4-e4b.gguf --port 8080
//	2NB_TEST_LLAMA_EMBED_ENDPOINT=http://127.0.0.1:8081 \
//	2NB_TEST_LLAMA_GEN_ENDPOINT=http://127.0.0.1:8080 go test ./internal/ai -run Llama
func requireLlamaEndpoints(t *testing.T) (embed, gen string) {
	t.Helper()
	embed = os.Getenv("2NB_TEST_LLAMA_EMBED_ENDPOINT")
	gen = os.Getenv("2NB_TEST_LLAMA_GEN_ENDPOINT")
	if embed == "" || gen == "" {
		t.Skip("set 2NB_TEST_LLAMA_EMBED_ENDPOINT and 2NB_TEST_LLAMA_GEN_ENDPOINT to a running llama-server")
	}
	return embed, gen
}

func TestLlamaEmbedAndGenerateLive(t *testing.T) {
	embedEP, genEP := requireLlamaEndpoints(t)
	ctx := context.Background()

	e := NewLlamaEmbedder("embeddinggemma-300m", 768, embedEP)
	if !e.Available(ctx) {
		t.Skip("embed endpoint not answering /health resolution")
	}
	vecs, err := e.Embed(ctx, []string{"a local knowledge base", "unrelated text"})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(vecs) != 2 || len(vecs[0]) != 768 {
		t.Fatalf("got %d vecs of dim %d, want 2x768", len(vecs), len(vecs[0]))
	}

	// Matryoshka truncation via WithDimension.
	small, err := e.Embed(ctx, []string{"x"}, WithDimension(256))
	if err != nil {
		t.Fatalf("Embed(256): %v", err)
	}
	if len(small[0]) != 256 {
		t.Errorf("WithDimension(256) len = %d, want 256", len(small[0]))
	}

	g := NewLlamaGenerator("gemma4-e4b", genEP)
	text, usage, err := g.GenerateWithUsage(ctx, "Reply with the single word: ok", GenOpts{MaxTokens: 16})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if text == "" {
		t.Error("empty generation")
	}
	if usage.OutputTokens == 0 {
		t.Error("expected real token usage from llama-server")
	}
}
