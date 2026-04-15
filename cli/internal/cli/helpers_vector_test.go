package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/apresai/2ndbrain/internal/ai"
	"github.com/apresai/2ndbrain/internal/testutil"
	"github.com/apresai/2ndbrain/internal/vault"
)

// fakeEmbedder is a local interface fake for VectorCompat unit tests.
// It is NOT a provider mock — the CLAUDE.md "no mocks" policy forbids
// stubbing real provider integrations (Bedrock/Ollama/OpenRouter), but
// does not forbid a tiny test-local fake that implements an interface
// purely for testing a compat-check function whose logic is independent
// of the actual provider.
type fakeEmbedder struct {
	name      string
	dims      int
	available bool
}

func (f *fakeEmbedder) Name() string                                             { return f.name }
func (f *fakeEmbedder) Dimensions() int                                          { return f.dims }
func (f *fakeEmbedder) Available(ctx context.Context) bool                       { return f.available }
func (f *fakeEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	vecs := make([][]float32, len(texts))
	for i := range vecs {
		vecs[i] = make([]float32, f.dims)
	}
	return vecs, nil
}
func (f *fakeEmbedder) ListModels(ctx context.Context) ([]ai.ModelInfo, error) {
	return nil, nil
}

var _ ai.EmbeddingProvider = (*fakeEmbedder)(nil)

func TestVectorCompat_NoEmbeddings(t *testing.T) {
	v := testutil.NewTestVault(t)
	// Provider is set in DefaultAIConfig, but no docs have embeddings yet.
	ready, msg := VectorCompat(context.Background(), v, &fakeEmbedder{name: "fake", dims: 768, available: true})
	if ready {
		t.Error("expected not-ready for zero-embedding vault")
	}
	if msg != "" {
		t.Errorf("expected empty message (silent fallback), got: %q", msg)
	}
}

func TestVectorCompat_NilEmbedderNoProvider(t *testing.T) {
	v := testutil.NewTestVault(t)
	seedEmbedding(t, v, "doc1", 768)
	v.Config.AI.Provider = ""

	ready, msg := VectorCompat(context.Background(), v, nil)
	if ready {
		t.Error("expected not-ready")
	}
	if !strings.Contains(msg, "no AI provider configured") {
		t.Errorf("message should mention missing provider, got: %q", msg)
	}
}

func TestVectorCompat_Unavailable(t *testing.T) {
	v := testutil.NewTestVault(t)
	seedEmbedding(t, v, "doc1", 768)
	v.Config.AI.Provider = "ollama"

	ready, msg := VectorCompat(context.Background(), v, &fakeEmbedder{name: "ollama", dims: 768, available: false})
	if ready {
		t.Error("expected not-ready when provider unavailable")
	}
	if !strings.Contains(msg, "unavailable") {
		t.Errorf("message should mention unavailable, got: %q", msg)
	}
	if !strings.Contains(msg, "ollama") {
		t.Errorf("message should name the provider, got: %q", msg)
	}
}

func TestVectorCompat_DimensionBreak(t *testing.T) {
	v := testutil.NewTestVault(t)
	seedEmbedding(t, v, "doc1", 768)
	v.Config.AI.Provider = "bedrock"

	ready, msg := VectorCompat(context.Background(), v, &fakeEmbedder{name: "bedrock", dims: 1024, available: true})
	if ready {
		t.Error("expected not-ready on dim mismatch")
	}
	if !strings.Contains(msg, "768d") || !strings.Contains(msg, "1024d") {
		t.Errorf("message should mention both dims, got: %q", msg)
	}
	if !strings.Contains(msg, "--force-reembed") {
		t.Errorf("message should suggest --force-reembed, got: %q", msg)
	}
}

func TestVectorCompat_OK(t *testing.T) {
	v := testutil.NewTestVault(t)
	seedEmbedding(t, v, "doc1", 768)
	v.Config.AI.Provider = "ollama"

	ready, msg := VectorCompat(context.Background(), v, &fakeEmbedder{name: "ollama", dims: 768, available: true})
	if !ready {
		t.Errorf("expected ready, got not-ready with msg: %q", msg)
	}
	if msg != "" {
		t.Errorf("expected empty message on OK, got: %q", msg)
	}
}

// seedEmbedding inserts a synthetic embedding of the given dim directly
// into the vault's DB so VectorCompat has something to sample. Bypasses
// the indexer/provider path — the point of these tests is to exercise
// the compat check's logic, not real embedding generation.
func seedEmbedding(t *testing.T, v *vault.Vault, docID string, dims int) {
	t.Helper()
	_, err := v.DB.Conn().Exec(
		`INSERT INTO documents (id, path, title, content_hash) VALUES (?, ?, ?, ?)`,
		docID, docID+".md", docID, "h",
	)
	if err != nil {
		t.Fatalf("insert doc: %v", err)
	}
	vec := make([]float32, dims)
	if err := v.DB.SetEmbedding(docID, vec, "synthetic", "h"); err != nil {
		t.Fatalf("set embedding: %v", err)
	}
}
