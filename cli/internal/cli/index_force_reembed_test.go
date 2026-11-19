package cli

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/apresai/2ndbrain/internal/ai"
	"github.com/apresai/2ndbrain/internal/testutil"
)

type failingDocEmbedder struct {
	dims int
}

func (f *failingDocEmbedder) Name() string                       { return "failing-doc-embedder" }
func (f *failingDocEmbedder) Dimensions() int                    { return f.dims }
func (f *failingDocEmbedder) Available(ctx context.Context) bool { return true }
func (f *failingDocEmbedder) ListModels(ctx context.Context) ([]ai.ModelInfo, error) {
	return nil, nil
}
func (f *failingDocEmbedder) Embed(ctx context.Context, texts []string, _ ...ai.EmbedOption) ([][]float32, error) {
	return nil, fmt.Errorf("gemini embed: Error 400, Message: * batch limit exceeded\n, Status: INVALID_ARGUMENT")
}

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
	_, err := forceReembedDocuments(context.Background(), v, cfg)
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

// TestForceReembedRestoresPreviousEmbeddingsAndSurfacesFirstError verifies that
// when a note fails during force-reembed with a multi-line API error, previous
// embeddings are restored and the returned error is single-line naming the note
// and root cause (so CLI/GUI failure panes display actionable errors).
func TestForceReembedRestoresPreviousEmbeddingsAndSurfacesFirstError(t *testing.T) {
	v := testutil.NewTestVault(t)
	doc := testutil.CreateAndIndex(t, v, "Oversized Audit", "note", "semantic content to preserve")

	if err := v.DB.SetEmbedding(doc.ID, []float32{0.2, 0.4, 0.6}, "existing-model", "existing-hash"); err != nil {
		t.Fatalf("seed embedding: %v", err)
	}

	const provider = "failing-doc-embedder-test"
	prev, prevErr := ai.DefaultRegistry.Embedder(provider)
	ai.DefaultRegistry.RegisterEmbedder(provider, &failingDocEmbedder{dims: 3})
	t.Cleanup(func() {
		if prevErr == nil && prev != nil {
			ai.DefaultRegistry.RegisterEmbedder(provider, prev)
		}
	})

	cfg := ai.AIConfig{Provider: provider, EmbeddingModel: "test-model", Dimensions: 3}
	stats, err := forceReembedDocuments(context.Background(), v, cfg)
	if err == nil {
		t.Fatal("forceReembedDocuments expected failure when embedder fails")
	}
	if stats.Failed != 1 {
		t.Errorf("stats.Failed = %d, want 1", stats.Failed)
	}
	msg := err.Error()
	if strings.Contains(msg, "\n") {
		t.Errorf("force-reembed error contains newline; GUI/stderr single-line summary would truncate: %q", msg)
	}
	if !strings.Contains(msg, doc.Path) {
		t.Errorf("force-reembed error missing failed doc path %q: %q", doc.Path, msg)
	}
	if !strings.Contains(msg, "batch limit exceeded") {
		t.Errorf("force-reembed error missing root cause 'batch limit exceeded': %q", msg)
	}

	got, gerr := v.DB.GetEmbedding(doc.ID)
	if gerr != nil || len(got) != 3 || got[0] != 0.2 {
		t.Fatalf("previous embedding was not restored on failure: got=%v err=%v", got, gerr)
	}
}
