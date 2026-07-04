package retrieve

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/apresai/2ndbrain/internal/ai"
	"github.com/apresai/2ndbrain/internal/search"
	"github.com/apresai/2ndbrain/internal/testutil"
)

// errEmbedder is Available with matching dims (so VectorCompat passes) but fails
// on Embed, to exercise the hybrid embed-error degradation branch.
type errEmbedder struct{ dims int }

func (e *errEmbedder) Name() string                       { return "err" }
func (e *errEmbedder) Dimensions() int                    { return e.dims }
func (e *errEmbedder) Available(ctx context.Context) bool { return true }
func (e *errEmbedder) Embed(ctx context.Context, texts []string, _ ...ai.EmbedOption) ([][]float32, error) {
	return nil, errors.New("provider boom")
}
func (e *errEmbedder) ListModels(ctx context.Context) ([]ai.ModelInfo, error) { return nil, nil }

var _ ai.EmbeddingProvider = (*errEmbedder)(nil)

// TestRetrieve_BM25Fallback: with no registered embedder and no embeddings,
// VectorCompat fails and Retrieve degrades to BM25, still returning matches.
func TestRetrieve_BM25Fallback(t *testing.T) {
	v := testutil.NewTestVault(t)
	testutil.CreateAndIndex(t, v, "Auth Guide", "note", "how authentication works with tokens")

	res, err := New(v).Retrieve(context.Background(), Options{Query: "authentication", Limit: 10})
	if err != nil {
		t.Fatalf("retrieve: %v", err)
	}
	if res.Mode != search.ModeKeyword {
		t.Errorf("mode = %q, want keyword (no usable embeddings)", res.Mode)
	}
	if len(res.Results) == 0 {
		t.Error("expected BM25 results for a matching query")
	}
}

// TestRetrieve_CorpusLoadedOncePerRetriever guards ask's "load the corpus once,
// retrieve twice" optimization: a single Retriever caches the corpus across
// passes, so the injected loader is invoked exactly once.
func TestRetrieve_CorpusLoadedOncePerRetriever(t *testing.T) {
	v := testutil.NewTestVault(t)
	testutil.CreateAndIndex(t, v, "Auth Guide", "note", "how authentication works with tokens")
	seedEmbedding(t, v, "seed-doc", 768) // gives SampleEmbeddingDim=768 so VectorCompat passes
	v.Config.AI.Provider = "ollama"

	calls := 0
	r := &Retriever{
		v:        v,
		engine:   search.NewEngine(v.DB.Conn()),
		embedder: &fakeEmbedder{name: "ollama", dims: 768, available: true},
		loadCorpus: func() ([]string, [][]float32, error) {
			calls++
			return v.DB.AllEmbeddings()
		},
	}
	if _, err := r.Retrieve(context.Background(), Options{Query: "authentication", Limit: 5}); err != nil {
		t.Fatalf("retrieve 1: %v", err)
	}
	if _, err := r.Retrieve(context.Background(), Options{Query: "tokens", Limit: 5}); err != nil {
		t.Fatalf("retrieve 2: %v", err)
	}
	if calls != 1 {
		t.Errorf("corpus loader called %d times, want 1 (cached across passes)", calls)
	}
}

// TestRetrieve_BM25OnlySkipsVector: BM25Only never loads the corpus or embeds.
func TestRetrieve_BM25OnlySkipsVector(t *testing.T) {
	v := testutil.NewTestVault(t)
	testutil.CreateAndIndex(t, v, "Auth Guide", "note", "how authentication works with tokens")
	seedEmbedding(t, v, "seed-doc", 768)
	v.Config.AI.Provider = "ollama"

	loaded := false
	r := &Retriever{
		v:        v,
		engine:   search.NewEngine(v.DB.Conn()),
		embedder: &fakeEmbedder{name: "ollama", dims: 768, available: true},
		loadCorpus: func() ([]string, [][]float32, error) {
			loaded = true
			return v.DB.AllEmbeddings()
		},
	}
	res, err := r.Retrieve(context.Background(), Options{Query: "authentication", Limit: 5, BM25Only: true})
	if err != nil {
		t.Fatalf("retrieve: %v", err)
	}
	if res.Mode != search.ModeKeyword {
		t.Errorf("mode = %q, want keyword under BM25Only", res.Mode)
	}
	if loaded {
		t.Error("BM25Only must not load the embedding corpus")
	}
}

// TestRetrieve_HybridSuccessReturnsHybridMode: a passing VectorCompat gate + a
// registered embedder runs the vector channel, so the mode is hybrid (the
// success path the corpus-once test never asserts).
func TestRetrieve_HybridSuccessReturnsHybridMode(t *testing.T) {
	v := testutil.NewTestVault(t)
	testutil.CreateAndIndex(t, v, "Auth Guide", "note", "how authentication works with tokens")
	seedEmbedding(t, v, "seed-doc", 768)
	v.Config.AI.Provider = "ollama"

	r := &Retriever{
		v:          v,
		engine:     search.NewEngine(v.DB.Conn()),
		embedder:   &fakeEmbedder{name: "ollama", dims: 768, available: true},
		loadCorpus: v.DB.AllEmbeddings,
	}
	res, err := r.Retrieve(context.Background(), Options{Query: "authentication", Limit: 5})
	if err != nil {
		t.Fatalf("retrieve: %v", err)
	}
	if res.Mode != search.ModeHybrid {
		t.Errorf("mode = %q, want hybrid (vector channel ran)", res.Mode)
	}
}

// TestRetrieve_InitWarningReturnedOnce: the VectorCompat degradation warning is
// surfaced on the first pass only, so ask's rewrite+fallback double retrieval
// never duplicates it.
func TestRetrieve_InitWarningReturnedOnce(t *testing.T) {
	v := testutil.NewTestVault(t)
	testutil.CreateAndIndex(t, v, "Auth Guide", "note", "how authentication works with tokens")
	seedEmbedding(t, v, "seed-doc", 768) // vault embedded at 768d
	v.Config.AI.Provider = "bedrock"

	// Embedder produces 1024d, so VectorCompat reports a dim break (a warning).
	r := &Retriever{
		v:          v,
		engine:     search.NewEngine(v.DB.Conn()),
		embedder:   &fakeEmbedder{name: "bedrock", dims: 1024, available: true},
		loadCorpus: v.DB.AllEmbeddings,
	}
	res1, err := r.Retrieve(context.Background(), Options{Query: "authentication", Limit: 5})
	if err != nil {
		t.Fatalf("retrieve 1: %v", err)
	}
	if len(res1.Warnings) == 0 {
		t.Fatal("first pass should surface the VectorCompat dim-break warning")
	}
	res2, err := r.Retrieve(context.Background(), Options{Query: "tokens", Limit: 5})
	if err != nil {
		t.Fatalf("retrieve 2: %v", err)
	}
	if len(res2.Warnings) != 0 {
		t.Errorf("second pass must not repeat the compat warning, got: %v", res2.Warnings)
	}
	if res1.Mode != search.ModeKeyword || res2.Mode != search.ModeKeyword {
		t.Errorf("modes = %q,%q, want keyword,keyword (vector channel off)", res1.Mode, res2.Mode)
	}
}

// TestRetrieve_EmbedErrorDegradesToBM25: a passing compat gate but a failing
// Embed call warns and falls back to BM25 (keyword mode) rather than erroring.
func TestRetrieve_EmbedErrorDegradesToBM25(t *testing.T) {
	v := testutil.NewTestVault(t)
	testutil.CreateAndIndex(t, v, "Auth Guide", "note", "how authentication works with tokens")
	seedEmbedding(t, v, "seed-doc", 768)
	v.Config.AI.Provider = "ollama"

	r := &Retriever{
		v:          v,
		engine:     search.NewEngine(v.DB.Conn()),
		embedder:   &errEmbedder{dims: 768}, // VectorCompat passes; Embed fails
		loadCorpus: v.DB.AllEmbeddings,
	}
	res, err := r.Retrieve(context.Background(), Options{Query: "authentication", Limit: 5})
	if err != nil {
		t.Fatalf("retrieve: %v", err)
	}
	if res.Mode != search.ModeKeyword {
		t.Errorf("mode = %q, want keyword (embed failed, fell back to BM25)", res.Mode)
	}
	found := false
	for _, w := range res.Warnings {
		if strings.Contains(w, "embedder returned error") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected an embed-error warning, got: %v", res.Warnings)
	}
	if len(res.Results) == 0 {
		t.Error("BM25 fallback should still return the matching doc")
	}
}
