package cli

import (
	"context"
	"testing"

	"github.com/apresai/2ndbrain/internal/ai"
	"github.com/apresai/2ndbrain/internal/document"
	"github.com/apresai/2ndbrain/internal/testutil"
)

// TestEmbedDocumentsSkipsEmptyDocuments verifies that a blank note (empty
// IndexableBody, e.g. a freshly-created "Untitled.md") is counted as skipped,
// not failed. Amazon Nova-2 rejects empty input with a 400
// ValidationException (minLength: 1); before this fix every such note failed
// embedding on every index, which both pinned the embedded count below 100%
// and wedged the macOS app's rebuild-progress UI. A vault with empty notes
// must still produce a clean (exit 0) index.
//
// Per the No-Mock policy this exercises the real Bedrock embedder and skips
// when AWS credentials aren't configured.
func TestEmbedDocumentsSkipsEmptyDocuments(t *testing.T) {
	ctx := context.Background()
	const embedModel = "amazon.nova-2-multimodal-embeddings-v1:0"
	embedder, err := ai.NewBedrockEmbedder(ctx, ai.BedrockConfig{Profile: "default", Region: "us-east-1"}, embedModel, 1024)
	if err != nil {
		t.Skipf("AWS credentials not configured: %v", err)
	}
	if !embedder.Available(ctx) {
		t.Skip("Bedrock not reachable")
	}

	v := testutil.NewTestVault(t)

	// An empty-body note: frontmatter only, no embeddable text. Written to
	// disk (the embed loop re-parses from disk) and registered in the
	// documents table so DocumentsNeedingEmbedding returns it.
	empty := document.NewDocument("Untitled", "note", "")
	emptyPath, err := empty.WriteFile(v.Root)
	if err != nil {
		t.Fatalf("write empty doc: %v", err)
	}
	empty.Path = v.RelPath(emptyPath)
	if err := v.DB.UpsertDocument(empty); err != nil {
		t.Fatalf("upsert empty doc: %v", err)
	}

	// A real note with embeddable content.
	testutil.CreateAndIndex(t, v, "Real Note", "note", "This note has genuinely embeddable content.")

	cfg := ai.AIConfig{Provider: "bedrock", EmbeddingModel: embedModel}
	stats, err := embedDocumentsWithProvider(ctx, v, cfg, embedder)
	if err != nil {
		t.Fatalf("embedDocumentsWithProvider returned error (empty notes must not fail the run): %v", err)
	}
	if stats.Skipped != 1 {
		t.Errorf("expected 1 skipped (the empty note), got %d", stats.Skipped)
	}
	if stats.Failed != 0 {
		t.Errorf("expected 0 failed, got %d (empty notes must be skipped, not failed)", stats.Failed)
	}
	if stats.Embedded != 1 {
		t.Errorf("expected 1 embedded (the non-empty note), got %d", stats.Embedded)
	}
}

// TestForceReembedCompletesWithEmptyDocuments guards the subtle completeness
// math in forceReembedDocuments: an empty (skipped) note must not count as a
// shortfall, so `--force-reembed` on a vault with blank notes returns cleanly
// (no "force-reembed incomplete" error, exit 0) rather than rolling back.
//
// Real Bedrock per the No-Mock policy; skips without AWS credentials.
func TestForceReembedCompletesWithEmptyDocuments(t *testing.T) {
	ctx := context.Background()
	const embedModel = "amazon.nova-2-multimodal-embeddings-v1:0"
	embedder, err := ai.NewBedrockEmbedder(ctx, ai.BedrockConfig{Profile: "default", Region: "us-east-1"}, embedModel, 1024)
	if err != nil {
		t.Skipf("AWS credentials not configured: %v", err)
	}
	if !embedder.Available(ctx) {
		t.Skip("Bedrock not reachable")
	}
	// forceReembedDocuments resolves the embedder from DefaultRegistry.
	ai.DefaultRegistry.RegisterEmbedder("bedrock", embedder)

	v := testutil.NewTestVault(t)

	empty := document.NewDocument("Untitled", "note", "")
	emptyPath, err := empty.WriteFile(v.Root)
	if err != nil {
		t.Fatalf("write empty doc: %v", err)
	}
	empty.Path = v.RelPath(emptyPath)
	if err := v.DB.UpsertDocument(empty); err != nil {
		t.Fatalf("upsert empty doc: %v", err)
	}
	real := testutil.CreateAndIndex(t, v, "Real Note", "note", "This note has genuinely embeddable content.")

	cfg := ai.AIConfig{Provider: "bedrock", EmbeddingModel: embedModel}
	if _, err := forceReembedDocuments(ctx, v, cfg); err != nil {
		t.Fatalf("force-reembed must succeed when the only shortfall is a skipped empty note: %v", err)
	}

	// The non-empty note is embedded; the empty one is left unembedded (no
	// vector was ever sent to the provider).
	if vec, err := v.DB.GetEmbedding(real.ID); err != nil || len(vec) == 0 {
		t.Errorf("expected the non-empty note to be embedded after force-reembed (err=%v, len=%d)", err, len(vec))
	}
}
