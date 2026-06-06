package ai

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

const ollamaTestEndpoint = "http://localhost:11434"

func requireOllama(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, ollamaTestEndpoint+"/", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Skip("Ollama not running at localhost:11434")
	}
	defer resp.Body.Close()
	buf := make([]byte, 100)
	n, _ := resp.Body.Read(buf)
	bodyStr := string(buf[:n])
	if !strings.Contains(bodyStr, "Ollama is running") {
		t.Skip("Ollama not running at localhost:11434 (port open but not Ollama)")
	}
}

func requireOllamaModel(t *testing.T, model string) {
	t.Helper()
	requireOllama(t)
	// Check if model is available by listing tags
	emb := NewOllamaEmbedder(ollamaTestEndpoint, model, 768)
	models, err := emb.ListModels(context.Background())
	if err != nil {
		t.Skipf("cannot list Ollama models: %v", err)
	}
	for _, m := range models {
		if m.ID == model || strings.HasPrefix(m.ID, model) || strings.HasPrefix(model, strings.Split(m.ID, ":")[0]) {
			return
		}
	}
	t.Skipf("Ollama model %q not pulled — run: ollama pull %s", model, model)
}

func TestOllamaAvailable(t *testing.T) {
	requireOllama(t)

	emb := NewOllamaEmbedder(ollamaTestEndpoint, "embeddinggemma", 768)
	if !emb.Available(context.Background()) {
		t.Error("expected available when Ollama is running")
	}
	// Second call should be cached
	if !emb.Available(context.Background()) {
		t.Error("expected cached available")
	}
}

func TestOllamaEmbed(t *testing.T) {
	requireOllamaModel(t, "embeddinggemma")

	emb := NewOllamaEmbedder(ollamaTestEndpoint, "embeddinggemma", 768)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	vecs, err := emb.Embed(ctx, []string{"hello world", "semantic search test"})
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || strings.Contains(err.Error(), "deadline exceeded") {
			t.Skip("Ollama embed timed out (model load or network issue)")
		}
		t.Fatalf("Embed: %v", err)
	}
	if len(vecs) != 2 {
		t.Fatalf("vecs count = %d, want 2", len(vecs))
	}
	if len(vecs[0]) == 0 {
		t.Fatal("empty embedding vector")
	}
	t.Logf("embedding dims: %d", len(vecs[0]))
}

func TestOllamaGenerate(t *testing.T) {
	requireOllamaModel(t, "qwen2.5:0.5b")

	gen := NewOllamaGenerator(ollamaTestEndpoint, "qwen2.5:0.5b")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := gen.Generate(ctx, "What is 2+2? Reply with just the number.", GenOpts{MaxTokens: 10})
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || strings.Contains(err.Error(), "deadline exceeded") {
			t.Skip("Ollama generate timed out (model load or network issue)")
		}
		t.Fatalf("Generate: %v", err)
	}
	if result == "" {
		t.Fatal("empty response")
	}
	t.Logf("response: %s", result)
}

func TestOllamaGenerateWithSystemPrompt(t *testing.T) {
	requireOllamaModel(t, "qwen2.5:0.5b")

	gen := NewOllamaGenerator(ollamaTestEndpoint, "qwen2.5:0.5b")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := gen.Generate(ctx, "What color is the sky?", GenOpts{
		SystemPrompt: "Reply with exactly one word.",
		MaxTokens:    10,
	})
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || strings.Contains(err.Error(), "deadline exceeded") {
			t.Skip("Ollama generate timed out (model load or network issue)")
		}
		t.Fatalf("Generate: %v", err)
	}
	if result == "" {
		t.Fatal("empty response")
	}
	t.Logf("response: %s", result)
}

func TestOllamaListModels(t *testing.T) {
	requireOllama(t)

	emb := NewOllamaEmbedder(ollamaTestEndpoint, "embeddinggemma", 768)
	models, err := emb.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	t.Logf("found %d models", len(models))
	for _, m := range models {
		if !m.Local {
			t.Errorf("model %s should be local", m.ID)
		}
		if m.PriceIn != 0 || m.PriceOut != 0 {
			t.Errorf("model %s should be free", m.ID)
		}
		if m.Provider != "ollama" {
			t.Errorf("model %s has provider %q, want ollama", m.ID, m.Provider)
		}
		t.Logf("  %s (type=%s, dims=%d)", m.ID, m.Type, m.Dimensions)
	}
}

func TestOllamaRegistryNoDuplicates(t *testing.T) {
	requireOllama(t)

	reg := NewRegistry()
	emb := NewOllamaEmbedder(ollamaTestEndpoint, "embeddinggemma", 768)
	gen := NewOllamaGenerator(ollamaTestEndpoint, "qwen2.5:0.5b")
	reg.RegisterEmbedder("ollama", emb)
	reg.RegisterGenerator("ollama", gen)

	models := reg.ListModels(context.Background())

	// Count occurrences of each model ID
	counts := make(map[string]int)
	for _, m := range models {
		counts[m.ID]++
	}
	for id, count := range counts {
		if count > 1 {
			t.Errorf("model %q appeared %d times, want 1", id, count)
		}
	}
	t.Logf("registry returned %d unique models", len(models))
}

func TestOllamaGeneratorListModels(t *testing.T) {
	requireOllama(t)

	gen := NewOllamaGenerator(ollamaTestEndpoint, "qwen2.5:0.5b")
	models, err := gen.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(models) == 0 {
		t.Skip("no models installed")
	}
	// Generator's ListModels should return ALL installed models (not filtered by type)
	// so that the registry can deduplicate properly
	t.Logf("generator ListModels returned %d models", len(models))
	for _, m := range models {
		t.Logf("  %s (type=%s)", m.ID, m.Type)
	}
}

func TestOllamaGeneratorAvailable(t *testing.T) {
	requireOllama(t)

	gen := NewOllamaGenerator(ollamaTestEndpoint, "gemma3:4b")
	if !gen.Available(context.Background()) {
		t.Error("expected available when Ollama is running")
	}
}

// Pure logic tests — no API calls needed

func TestClassifyOllamaModel(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{"embeddinggemma:308m", "embedding"},
		{"nomic-embed-text:latest", "embedding"},
		{"mxbai-embed-large:latest", "embedding"},
		{"gemma3:4b", "generation"},
		{"llama3.1:8b", "generation"},
		{"qwen2:7b", "generation"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyOllamaModel(tt.name)
			if got != tt.want {
				t.Errorf("classifyOllamaModel(%q) = %q, want %q", tt.name, got, tt.want)
			}
		})
	}
}

func TestLookupEmbeddingDims(t *testing.T) {
	tests := []struct {
		name string
		want int
	}{
		{"embeddinggemma:308m", 768},
		{"nomic-embed-text:latest", 768},
		{"mxbai-embed-large:latest", 1024},
		{"unknown-model", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := lookupEmbeddingDims(tt.name)
			if got != tt.want {
				t.Errorf("lookupEmbeddingDims(%q) = %d, want %d", tt.name, got, tt.want)
			}
		})
	}
}
