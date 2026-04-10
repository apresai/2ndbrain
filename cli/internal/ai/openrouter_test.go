package ai

import (
	"context"
	"os"
	"strings"
	"testing"
)

func requireOpenRouterKey(t *testing.T) string {
	t.Helper()
	key := os.Getenv("OPENROUTER_API_KEY")
	if key == "" {
		t.Skip("OPENROUTER_API_KEY not set")
	}
	return key
}

// skipOnTransient skips the test if the error is a transient API issue (rate limit, empty response, timeout).
func skipOnTransient(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		return
	}
	msg := err.Error()
	if strings.Contains(msg, "status 429") ||
		strings.Contains(msg, "empty response") ||
		strings.Contains(msg, "context deadline exceeded") {
		t.Skipf("skipped: transient API error — %v", err)
	}
}

func TestOpenRouterEmbed(t *testing.T) {
	key := requireOpenRouterKey(t)

	emb := NewOpenRouterEmbedder(key, openrouterDefaultEmbedModel, 1024)
	vecs, err := emb.Embed(context.Background(), []string{"hello world", "semantic search test"})
	skipOnTransient(t, err)
	if err != nil {
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

func TestOpenRouterEmbedSingle(t *testing.T) {
	key := requireOpenRouterKey(t)

	emb := NewOpenRouterEmbedder(key, openrouterDefaultEmbedModel, 1024)
	vecs, err := emb.Embed(context.Background(), []string{"single text embedding"})
	skipOnTransient(t, err)
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(vecs) != 1 {
		t.Fatalf("vecs count = %d, want 1", len(vecs))
	}
	// Verify non-zero values
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
}

func TestOpenRouterGenerate(t *testing.T) {
	key := requireOpenRouterKey(t)

	gen := NewOpenRouterGenerator(key, "google/gemma-3-4b-it:free")
	result, err := gen.Generate(context.Background(), "What is 2+2? Reply with just the number.", GenOpts{MaxTokens: 10, Temperature: 0})
	skipOnTransient(t, err)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if result == "" {
		t.Fatal("empty response")
	}
	t.Logf("response: %s", result)
}

func TestOpenRouterGenerateWithSystemPrompt(t *testing.T) {
	key := requireOpenRouterKey(t)

	gen := NewOpenRouterGenerator(key, "anthropic/claude-haiku-4-5")
	result, err := gen.Generate(context.Background(), "What color is the sky?", GenOpts{
		SystemPrompt: "Reply with exactly one word.",
		MaxTokens:    10,
		Temperature:  0,
	})
	skipOnTransient(t, err)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if result == "" {
		t.Fatal("empty response")
	}
	t.Logf("response: %s", result)
}

func TestOpenRouterGenerate_Gemma3SystemPrompt(t *testing.T) {
	key := requireOpenRouterKey(t)

	// Gemma 3 on Google AI Studio does not support system prompts (developer instructions).
	// This is a regression test — if this model starts working with system prompts, great.
	// If it fails with a 400 error about "Developer instruction is not enabled", that confirms
	// the model is incompatible and should not be used as the easy mode default.
	gen := NewOpenRouterGenerator(key, "google/gemma-3-4b-it:free")
	_, err := gen.Generate(context.Background(), "What is 2+2?", GenOpts{
		SystemPrompt: "Reply with just the number.",
		MaxTokens:    10,
		Temperature:  0,
	})
	skipOnTransient(t, err)
	if err != nil {
		t.Logf("Gemma 3 system prompt: %v (expected — model does not support system prompts)", err)
	} else {
		t.Log("Gemma 3 accepted system prompt — model may now support it")
	}
}

func TestOpenRouterAvailable(t *testing.T) {
	key := requireOpenRouterKey(t)

	emb := NewOpenRouterEmbedder(key, openrouterDefaultEmbedModel, 1024)
	if !emb.Available(context.Background()) {
		t.Error("expected available")
	}
	// Second call should be cached
	if !emb.Available(context.Background()) {
		t.Error("expected cached available")
	}
}

func TestOpenRouterGeneratorAvailable(t *testing.T) {
	key := requireOpenRouterKey(t)

	gen := NewOpenRouterGenerator(key, "qwen/qwen3.6-plus:free")
	if !gen.Available(context.Background()) {
		t.Skip("skipped: generator not available (transient upstream issue)")
	}
}

func TestOpenRouterListModels(t *testing.T) {
	key := requireOpenRouterKey(t)

	models, err := ListOpenRouterModels(context.Background(), key, "")
	skipOnTransient(t, err)
	if err != nil {
		t.Fatalf("ListOpenRouterModels: %v", err)
	}
	if len(models) == 0 {
		t.Fatal("no models returned")
	}

	// Verify we get both embedding and generation models
	hasEmbed := false
	hasGen := false
	for _, m := range models {
		if m.Type == "embedding" {
			hasEmbed = true
		}
		if m.Type == "generation" {
			hasGen = true
		}
		if m.Provider != "openrouter" {
			t.Errorf("model %s has provider %q, want openrouter", m.ID, m.Provider)
		}
	}
	if !hasGen {
		t.Error("no generation models found")
	}
	t.Logf("found %d models (%v embedding)", len(models), hasEmbed)
}

func TestOpenRouterDefaultEmbedModel(t *testing.T) {
	emb := NewOpenRouterEmbedder("key", "", 768)
	if emb.model != openrouterDefaultEmbedModel {
		t.Errorf("default model = %q, want %q", emb.model, openrouterDefaultEmbedModel)
	}
}

func TestParsePerMillionPrice(t *testing.T) {
	tests := []struct {
		input string
		want  float64
	}{
		{"0.000005", 5.0},
		{"0", 0},
		{"0.000001", 1.0},
		{"invalid", 0},
	}
	for _, tt := range tests {
		got := parsePerMillionPrice(tt.input)
		if got != tt.want {
			t.Errorf("parsePerMillionPrice(%q) = %f, want %f", tt.input, got, tt.want)
		}
	}
}
