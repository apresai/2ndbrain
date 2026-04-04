package ai

import (
	"context"
	"testing"
)

// mockEmbedder implements EmbeddingProvider for testing.
type mockEmbedder struct {
	name string
	dims int
}

func (m *mockEmbedder) Name() string { return m.name }
func (m *mockEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	result := make([][]float32, len(texts))
	for i := range texts {
		result[i] = make([]float32, m.dims)
	}
	return result, nil
}
func (m *mockEmbedder) Dimensions() int                                { return m.dims }
func (m *mockEmbedder) Available(_ context.Context) bool               { return true }
func (m *mockEmbedder) ListModels(_ context.Context) ([]ModelInfo, error) {
	return []ModelInfo{{
		ID:       m.name,
		Name:     m.name,
		Provider: "mock",
		Type:     "embedding",
		Dimensions: m.dims,
		Local:    true,
	}}, nil
}

// mockGenerator implements GenerationProvider for testing.
type mockGenerator struct {
	name string
}

func (m *mockGenerator) Name() string { return m.name }
func (m *mockGenerator) Generate(_ context.Context, prompt string, _ GenOpts) (string, error) {
	return "mock response to: " + prompt, nil
}
func (m *mockGenerator) Available(_ context.Context) bool               { return true }
func (m *mockGenerator) ListModels(_ context.Context) ([]ModelInfo, error) {
	return []ModelInfo{{
		ID:       m.name,
		Name:     m.name,
		Provider: "mock",
		Type:     "generation",
		Local:    true,
	}}, nil
}

func TestRegistryRegisterAndRetrieve(t *testing.T) {
	r := NewRegistry()

	emb := &mockEmbedder{name: "test-embed", dims: 768}
	gen := &mockGenerator{name: "test-gen"}

	r.RegisterEmbedder("test", emb)
	r.RegisterGenerator("test", gen)

	got, err := r.Embedder("test")
	if err != nil {
		t.Fatalf("Embedder: %v", err)
	}
	if got.Name() != "test-embed" {
		t.Errorf("got name %q, want %q", got.Name(), "test-embed")
	}

	gotGen, err := r.Generator("test")
	if err != nil {
		t.Fatalf("Generator: %v", err)
	}
	if gotGen.Name() != "test-gen" {
		t.Errorf("got name %q, want %q", gotGen.Name(), "test-gen")
	}
}

func TestRegistryNotFound(t *testing.T) {
	r := NewRegistry()

	_, err := r.Embedder("nonexistent")
	if err == nil {
		t.Error("expected error for missing embedder")
	}

	_, err = r.Generator("nonexistent")
	if err == nil {
		t.Error("expected error for missing generator")
	}
}

func TestRegistryListModels(t *testing.T) {
	r := NewRegistry()
	r.RegisterEmbedder("test", &mockEmbedder{name: "embed-1", dims: 768})
	r.RegisterGenerator("test", &mockGenerator{name: "gen-1"})

	models := r.ListModels(context.Background())
	if len(models) != 2 {
		t.Fatalf("got %d models, want 2", len(models))
	}

	var hasEmbed, hasGen bool
	for _, m := range models {
		if m.Type == "embedding" {
			hasEmbed = true
		}
		if m.Type == "generation" {
			hasGen = true
		}
	}
	if !hasEmbed || !hasGen {
		t.Errorf("missing model types: embed=%v gen=%v", hasEmbed, hasGen)
	}
}

func TestRegistryNames(t *testing.T) {
	r := NewRegistry()
	r.RegisterEmbedder("a", &mockEmbedder{name: "a", dims: 384})
	r.RegisterEmbedder("b", &mockEmbedder{name: "b", dims: 768})

	names := r.EmbedderNames()
	if len(names) != 2 {
		t.Errorf("got %d names, want 2", len(names))
	}
}

func TestMockEmbedder(t *testing.T) {
	emb := &mockEmbedder{name: "test", dims: 768}
	vecs, err := emb.Embed(context.Background(), []string{"hello", "world"})
	if err != nil {
		t.Fatal(err)
	}
	if len(vecs) != 2 {
		t.Fatalf("got %d vectors, want 2", len(vecs))
	}
	if len(vecs[0]) != 768 {
		t.Errorf("got %d dims, want 768", len(vecs[0]))
	}
}

func TestMockGenerator(t *testing.T) {
	gen := &mockGenerator{name: "test"}
	resp, err := gen.Generate(context.Background(), "hello", DefaultGenOpts())
	if err != nil {
		t.Fatal(err)
	}
	if resp != "mock response to: hello" {
		t.Errorf("unexpected response: %s", resp)
	}
}
