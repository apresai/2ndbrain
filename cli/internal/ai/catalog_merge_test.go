package ai

import (
	"context"
	"testing"
)

func TestBuildModelList_VerifiedOnly(t *testing.T) {
	ctx := context.Background()
	result, err := BuildModelList(ctx, MergedListOptions{
		Config: DefaultAIConfig(),
	})
	if err != nil {
		t.Fatalf("BuildModelList: %v", err)
	}
	if len(result.Verified) == 0 {
		t.Fatal("expected verified models from catalog")
	}
	if len(result.Unverified) != 0 {
		t.Errorf("expected no unverified models without --discover, got %d", len(result.Unverified))
	}
}

func TestBuildModelList_ActiveMarking(t *testing.T) {
	ctx := context.Background()
	cfg := DefaultAIConfig() // bedrock, haiku, nova embed
	result, err := BuildModelList(ctx, MergedListOptions{Config: cfg})
	if err != nil {
		t.Fatalf("BuildModelList: %v", err)
	}

	var activeCount int
	for _, m := range result.Verified {
		if m.Active {
			activeCount++
			if m.Provider != cfg.Provider {
				t.Errorf("active model %s has provider %s, expected %s", m.ID, m.Provider, cfg.Provider)
			}
			if m.ID != cfg.EmbeddingModel && m.ID != cfg.GenerationModel {
				t.Errorf("active model %s doesn't match config embedding=%s or generation=%s",
					m.ID, cfg.EmbeddingModel, cfg.GenerationModel)
			}
		}
	}
	if activeCount != 2 {
		t.Errorf("expected 2 active models (embed + gen), got %d", activeCount)
	}
}

func TestBuildModelList_Sorting(t *testing.T) {
	ctx := context.Background()
	result, err := BuildModelList(ctx, MergedListOptions{
		Config: DefaultAIConfig(),
	})
	if err != nil {
		t.Fatalf("BuildModelList: %v", err)
	}

	for i := 1; i < len(result.Verified); i++ {
		a, b := result.Verified[i-1], result.Verified[i]
		if a.Provider > b.Provider {
			t.Errorf("sort violation: %s/%s before %s/%s (provider order)",
				a.Provider, a.ID, b.Provider, b.ID)
		}
		if a.Provider == b.Provider {
			if a.Type == "generation" && b.Type == "embedding" {
				t.Errorf("sort violation: %s/%s (generation) before %s/%s (embedding)",
					a.Provider, a.ID, b.Provider, b.ID)
			}
			if a.Provider == b.Provider && a.Type == b.Type && a.ID > b.ID {
				t.Errorf("sort violation: %s/%s before %s/%s (id order)",
					a.Provider, a.ID, b.Provider, b.ID)
			}
		}
	}
}

func TestIsActiveModel(t *testing.T) {
	cfg := DefaultAIConfig()

	tests := []struct {
		name   string
		model  ModelInfo
		expect bool
	}{
		{
			name:   "matching embed",
			model:  ModelInfo{Provider: "bedrock", Type: "embedding", ID: cfg.EmbeddingModel},
			expect: true,
		},
		{
			name:   "matching gen",
			model:  ModelInfo{Provider: "bedrock", Type: "generation", ID: cfg.GenerationModel},
			expect: true,
		},
		{
			name:   "wrong provider",
			model:  ModelInfo{Provider: "ollama", Type: "generation", ID: cfg.GenerationModel},
			expect: false,
		},
		{
			name:   "wrong model",
			model:  ModelInfo{Provider: "bedrock", Type: "generation", ID: "some-other-model"},
			expect: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isActiveModel(tt.model, cfg)
			if got != tt.expect {
				t.Errorf("isActiveModel(%s/%s) = %v, want %v", tt.model.Provider, tt.model.ID, got, tt.expect)
			}
		})
	}
}

func TestSortModels(t *testing.T) {
	models := []ModelInfo{
		{Provider: "ollama", Type: "generation", ID: "gemma3:4b"},
		{Provider: "bedrock", Type: "generation", ID: "z-model"},
		{Provider: "bedrock", Type: "embedding", ID: "a-embed"},
		{Provider: "bedrock", Type: "generation", ID: "a-model"},
		{Provider: "ollama", Type: "embedding", ID: "nomic"},
	}
	sortModels(models)

	expected := []string{
		"bedrock/embedding/a-embed",
		"bedrock/generation/a-model",
		"bedrock/generation/z-model",
		"ollama/embedding/nomic",
		"ollama/generation/gemma3:4b",
	}
	for i, m := range models {
		got := m.Provider + "/" + m.Type + "/" + m.ID
		if got != expected[i] {
			t.Errorf("position %d: got %s, want %s", i, got, expected[i])
		}
	}
}
