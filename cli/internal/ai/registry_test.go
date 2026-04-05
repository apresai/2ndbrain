package ai

import (
	"context"
	"os"
	"testing"
)

func TestRegistryRegisterAndRetrieve(t *testing.T) {
	key := os.Getenv("OPENROUTER_API_KEY")
	if key == "" {
		t.Skip("OPENROUTER_API_KEY not set")
	}

	r := NewRegistry()

	emb := NewOpenRouterEmbedder(key, openrouterDefaultEmbedModel, 1024)
	gen := NewOpenRouterGenerator(key, "google/gemma-3-4b-it:free")

	r.RegisterEmbedder("openrouter", emb)
	r.RegisterGenerator("openrouter", gen)

	got, err := r.Embedder("openrouter")
	if err != nil {
		t.Fatalf("Embedder: %v", err)
	}
	if got.Name() != "openrouter" {
		t.Errorf("got name %q, want openrouter", got.Name())
	}

	gotGen, err := r.Generator("openrouter")
	if err != nil {
		t.Fatalf("Generator: %v", err)
	}
	if gotGen.Name() != "openrouter" {
		t.Errorf("got name %q, want openrouter", gotGen.Name())
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
	key := os.Getenv("OPENROUTER_API_KEY")
	if key == "" {
		t.Skip("OPENROUTER_API_KEY not set")
	}

	r := NewRegistry()
	r.RegisterEmbedder("openrouter", NewOpenRouterEmbedder(key, openrouterDefaultEmbedModel, 1024))
	r.RegisterGenerator("openrouter", NewOpenRouterGenerator(key, "google/gemma-3-4b-it:free"))

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
	key := os.Getenv("OPENROUTER_API_KEY")
	if key == "" {
		t.Skip("OPENROUTER_API_KEY not set")
	}

	r := NewRegistry()
	r.RegisterEmbedder("openrouter", NewOpenRouterEmbedder(key, openrouterDefaultEmbedModel, 1024))
	r.RegisterEmbedder("ollama", NewOllamaEmbedder("http://localhost:11434", "embeddinggemma", 768))

	names := r.EmbedderNames()
	if len(names) != 2 {
		t.Errorf("got %d names, want 2", len(names))
	}
}
