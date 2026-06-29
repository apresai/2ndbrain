package cli

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/apresai/2ndbrain/internal/ai"
	"github.com/apresai/2ndbrain/internal/testutil"
)

// concEmbedder is a test-local interface fake (not a provider mock — the no-mock
// policy targets stubbing real provider HTTP, not a tiny fake for testing the
// concurrency LOGIC of the embed worker pool). It returns finite non-zero
// vectors so embed.Document actually stores them, and sleeps briefly so workers
// genuinely overlap.
type concEmbedder struct {
	dims  int
	calls atomic.Int64
}

func (e *concEmbedder) Name() string                       { return "conc" }
func (e *concEmbedder) Dimensions() int                    { return e.dims }
func (e *concEmbedder) Available(ctx context.Context) bool { return true }
func (e *concEmbedder) ListModels(ctx context.Context) ([]ai.ModelInfo, error) {
	return nil, nil
}
func (e *concEmbedder) Embed(ctx context.Context, texts []string, _ ...ai.EmbedOption) ([][]float32, error) {
	e.calls.Add(1)
	time.Sleep(2 * time.Millisecond) // let pool workers actually interleave
	vecs := make([][]float32, len(texts))
	for i := range vecs {
		v := make([]float32, e.dims)
		for j := range v {
			v[j] = 0.1 // finite, non-zero → stored, not skipped
		}
		vecs[i] = v
	}
	return vecs, nil
}

// TestEmbedDocumentsWithProvider_ConcurrentAggregation runs the real worker pool
// at concurrency 8 over 20 docs and asserts the per-worker result slots aggregate
// correctly. Run under -race, this is the race guard for the shared results
// slice + atomic progress counter.
func TestEmbedDocumentsWithProvider_ConcurrentAggregation(t *testing.T) {
	flagPorcelain = true // suppress progress/cost stderr (and its catalog load)
	t.Cleanup(func() { flagPorcelain = false })

	v := testutil.NewTestVault(t)
	const n = 20
	for i := 0; i < n; i++ {
		testutil.CreateAndIndex(t, v, fmt.Sprintf("Doc %d", i), "note",
			fmt.Sprintf("content body number %d with several distinct words here", i))
	}

	cfg := v.Config.AI
	cfg.Provider = "fake"
	cfg.EmbeddingModel = "fake-model"
	cfg.EmbedConcurrency = 8 // force concurrency well above 1
	emb := &concEmbedder{dims: 8}

	stats, err := embedDocumentsWithProvider(context.Background(), v, cfg, emb)
	if err != nil {
		t.Fatalf("embedDocumentsWithProvider: %v", err)
	}
	if stats.Attempted != n {
		t.Errorf("Attempted = %d, want %d", stats.Attempted, n)
	}
	if stats.Embedded != n {
		t.Errorf("Embedded = %d, want %d (all docs embedded under concurrency)", stats.Embedded, n)
	}
	if stats.Failed != 0 || stats.Skipped != 0 {
		t.Errorf("Failed=%d Skipped=%d, want 0/0", stats.Failed, stats.Skipped)
	}
	if stats.TotalChars == 0 {
		t.Errorf("TotalChars = 0, want > 0 (chars summed from each worker)")
	}
	if got := emb.calls.Load(); got != int64(n) {
		t.Errorf("embedder calls = %d, want %d (one Embed per doc)", got, n)
	}
	// Every doc actually has a stored embedding.
	if c, err := v.DB.EmbeddingCount(); err != nil || c != n {
		t.Errorf("EmbeddingCount = %d (err %v), want %d", c, err, n)
	}
}
