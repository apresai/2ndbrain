package vault_test

import (
	"context"
	"testing"

	"github.com/apresai/2ndbrain/internal/ai"
	"github.com/apresai/2ndbrain/internal/testutil"
	"github.com/apresai/2ndbrain/internal/vault"
)

// TestEmbedDocuments_NoWorkIsNoOp: with nothing needing embedding, the pass
// returns Attempted=0 without ever touching the embedding provider — so a nil
// embedder is safe. Locks the early-return that the credential-gated real-embed
// tests skip when no provider is configured.
func TestEmbedDocuments_NoWorkIsNoOp(t *testing.T) {
	v := testutil.NewTestVault(t)
	defer v.Close()

	stats, err := vault.EmbedDocuments(context.Background(), v, v.Config.AI, nil, vault.EmbedOpts{})
	if err != nil {
		t.Fatalf("EmbedDocuments on empty vault: %v", err)
	}
	if stats.Attempted != 0 || stats.Embedded != 0 || stats.Cancelled {
		t.Errorf("empty vault: got %+v, want Attempted=0 Embedded=0 Cancelled=false", stats)
	}
}

// TestEmbedDocuments_ContextCancelledStopsBeforeProvider: when the caller's
// context is already cancelled, the pass reports the work set via Attempted but
// embeds nothing and never calls the provider (nil embedder is safe), returning
// Cancelled=true and no error. This is the MCP abort-on-disconnect guarantee,
// now shared by both index paths.
func TestEmbedDocuments_ContextCancelledStopsBeforeProvider(t *testing.T) {
	v := testutil.NewTestVault(t)
	defer v.Close()

	// Index (but never embed) a few notes so they show up as needing embedding.
	for _, title := range []string{"Note One", "Note Two", "Note Three"} {
		testutil.CreateAndIndex(t, v, title, "note", "Some body text to chunk and embed.")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancelled before the pass starts

	stats, err := vault.EmbedDocuments(ctx, v, v.Config.AI, nil, vault.EmbedOpts{})
	if err != nil {
		t.Fatalf("EmbedDocuments with cancelled ctx returned error (want partial success): %v", err)
	}
	if stats.Attempted != 3 {
		t.Errorf("Attempted = %d, want 3 (the docs needing embedding)", stats.Attempted)
	}
	if stats.Embedded != 0 {
		t.Errorf("Embedded = %d, want 0 (cancelled before any provider call)", stats.Embedded)
	}
	if !stats.Cancelled {
		t.Error("Cancelled = false, want true (context was cancelled before the pass ran)")
	}
}

// cancelOnEmbedEmbedder is a test-local interface fake (NOT a provider HTTP mock
// — the no-mock policy targets stubbing real provider calls, not a tiny fake for
// exercising the worker pool's cancellation LOGIC, the same carve-out the
// existing concEmbedder in package cli relies on). Its Embed cancels the caller's
// context and then returns an error, simulating a provider call interrupted
// mid-flight by an MCP client disconnect.
type cancelOnEmbedEmbedder struct {
	dims   int
	cancel context.CancelFunc
}

func (e *cancelOnEmbedEmbedder) Name() string                       { return "cancel-on-embed" }
func (e *cancelOnEmbedEmbedder) Dimensions() int                    { return e.dims }
func (e *cancelOnEmbedEmbedder) Available(ctx context.Context) bool { return true }
func (e *cancelOnEmbedEmbedder) ListModels(ctx context.Context) ([]ai.ModelInfo, error) {
	return nil, nil
}
func (e *cancelOnEmbedEmbedder) Embed(ctx context.Context, texts []string, _ ...ai.EmbedOption) ([][]float32, error) {
	e.cancel()
	return nil, context.Canceled
}

// TestEmbedDocuments_CancelMidEmbedIsNotFailed: when the context is cancelled
// WHILE embed.Document is in flight (so it returns an error), the pass must
// classify the document as cancelled, NOT failed — Failed stays 0 and Cancelled
// is set. This is the core new guarantee that the pre-start cancel test can't
// reach (it never enters a worker), so it gets its own concurrency-logic fake.
func TestEmbedDocuments_CancelMidEmbedIsNotFailed(t *testing.T) {
	v := testutil.NewTestVault(t)
	defer v.Close()
	testutil.CreateAndIndex(t, v, "Solo Note", "note", "some body text to embed into a chunk")

	ctx, cancel := context.WithCancel(context.Background())
	emb := &cancelOnEmbedEmbedder{dims: 8, cancel: cancel}

	cfg := v.Config.AI
	cfg.EmbedConcurrency = 1 // single worker → deterministic ordering

	stats, err := vault.EmbedDocuments(ctx, v, cfg, emb, vault.EmbedOpts{})
	if err != nil {
		t.Fatalf("EmbedDocuments returned error (cancellation is partial success, not an error): %v", err)
	}
	if stats.Failed != 0 {
		t.Errorf("Failed = %d, want 0 (a cancel mid-embed must NOT count as a document failure)", stats.Failed)
	}
	if stats.Embedded != 0 {
		t.Errorf("Embedded = %d, want 0", stats.Embedded)
	}
	if !stats.Cancelled {
		t.Error("Cancelled = false, want true (context was cancelled during embed.Document)")
	}
}
