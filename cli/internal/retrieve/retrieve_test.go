package retrieve

import (
	"context"
	"testing"

	"github.com/apresai/2ndbrain/internal/search"
	"github.com/apresai/2ndbrain/internal/testutil"
)

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
