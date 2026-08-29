package ai

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
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

// stubEmbedder is a registry map occupant. It is not a provider mock: no
// method here pretends to call Bedrock or return embeddings.
type stubEmbedder struct{}

func (stubEmbedder) Name() string                   { return "bedrock" }
func (stubEmbedder) Dimensions() int                { return 0 }
func (stubEmbedder) Available(context.Context) bool { return false }
func (stubEmbedder) Embed(context.Context, []string, ...EmbedOption) ([][]float32, error) {
	return nil, fmt.Errorf("stub")
}
func (stubEmbedder) ListModels(context.Context) ([]ModelInfo, error) { return nil, nil }

func TestRegistryUnavailableCauseWrapsAndClears(t *testing.T) {
	r := NewRegistry()
	cause := &UnroutedSlotError{
		Slot:  "embedding",
		Model: "fake.dual-embed",
		Candidates: []ModelInfo{
			{ID: "fake.dual-embed", Provider: "bedrock", Plane: PlaneClassic, Region: "us-east-1"},
			{ID: "fake.dual-embed", Provider: "bedrock", Plane: PlaneClassic, Region: "us-west-2"},
		},
	}
	r.NoteUnavailable("bedrock", cause)

	_, err := r.Embedder("bedrock")
	if err == nil {
		t.Fatal("expected embedder lookup to fail")
	}
	if !strings.Contains(err.Error(), "2nb config set") {
		t.Errorf("embedder error lost the pick command: %v", err)
	}
	_, err = r.Generator("bedrock")
	if err == nil {
		t.Fatal("expected generator lookup to fail")
	}
	if !strings.Contains(err.Error(), "2nb config set") {
		t.Errorf("generator error lost the pick command: %v", err)
	}

	r.RegisterEmbedder("bedrock", stubEmbedder{})
	if _, err := r.Embedder("bedrock"); err != nil {
		t.Fatalf("register should have cleared the note: %v", err)
	}
	if _, err := r.Generator("bedrock"); err == nil {
		t.Fatal("generator is still unregistered")
	} else if strings.Contains(err.Error(), "2nb config set") {
		t.Errorf("stale unrouted cause survived RegisterEmbedder: %v", err)
	}
}

func TestInitBedrockNotesUnroutedSlot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	root := t.TempDir()
	modelID := "fake.dual-embed"
	for _, region := range []string{"us-east-1", "us-west-2"} {
		if err := SaveUserCatalogEntry(ScopeVault, root, ModelInfo{
			ID: modelID, Provider: "bedrock", Type: "embedding",
			Tier: TierUserVerified, Plane: PlaneClassic, Region: region,
		}); err != nil {
			t.Fatal(err)
		}
	}
	cfg := DefaultAIConfig()
	cfg.Provider = "bedrock"
	cfg.EmbeddingModel = modelID
	cfg.EmbeddingPlane = ""
	cfg.EmbeddingRegion = ""

	reg := NewRegistry()
	err := InitBedrock(context.Background(), reg, cfg.Bedrock, cfg, root)
	if err == nil {
		t.Fatal("expected unrouted slot")
	}
	var unrouted *UnroutedSlotError
	if !errors.As(err, &unrouted) {
		t.Fatalf("InitBedrock error = %v, want UnroutedSlotError", err)
	}

	_, lookup := reg.Embedder("bedrock")
	if lookup == nil {
		t.Fatal("expected embedder lookup to fail")
	}
	if !strings.Contains(lookup.Error(), "2nb config set") {
		t.Errorf("registry dropped the pick command: %v", lookup)
	}
	if !strings.Contains(lookup.Error(), "not registered") {
		t.Errorf("expected the not-registered wrapper, got: %v", lookup)
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
