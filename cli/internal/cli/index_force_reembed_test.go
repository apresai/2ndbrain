package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/apresai/2ndbrain/internal/ai"
	"github.com/apresai/2ndbrain/internal/testutil"
)

func TestForceReembedPreflightsProviderBeforeInvalidating(t *testing.T) {
	v := testutil.NewTestVault(t)
	doc := testutil.CreateAndIndex(t, v, "Force Reembed Safety", "note", "semantic content")

	if err := v.DB.SetEmbedding(doc.ID, []float32{0.1, 0.2, 0.3}, "existing-model", "existing-hash"); err != nil {
		t.Fatalf("seed embedding: %v", err)
	}

	cfg := ai.AIConfig{
		Provider:       "missing-provider",
		EmbeddingModel: "new-model",
	}
	err := forceReembedDocuments(context.Background(), v, cfg)
	if err == nil {
		t.Fatal("forceReembedDocuments with missing provider should fail")
	}
	if !strings.Contains(err.Error(), "preflight") {
		t.Fatalf("expected preflight error, got %v", err)
	}

	got, err := v.DB.GetEmbedding(doc.ID)
	if err != nil {
		t.Fatalf("embedding should still be present after preflight failure: %v", err)
	}
	if len(got) != 3 || got[0] != 0.1 || got[1] != 0.2 || got[2] != 0.3 {
		t.Fatalf("embedding was mutated after preflight failure: %+v", got)
	}

	var model, hash string
	if err := v.DB.Conn().QueryRow(`SELECT embedding_model, embedding_hash FROM documents WHERE id = ?`, doc.ID).Scan(&model, &hash); err != nil {
		t.Fatalf("query embedding metadata: %v", err)
	}
	if model != "existing-model" || hash != "existing-hash" {
		t.Fatalf("embedding metadata mutated after preflight failure: model=%q hash=%q", model, hash)
	}
}
