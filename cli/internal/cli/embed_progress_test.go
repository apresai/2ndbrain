package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/apresai/2ndbrain/internal/ai"
	"github.com/apresai/2ndbrain/internal/testutil"
)

// TestSingleDocEmbedPassPrintsNoProgress is the contract that keeps the
// slow-call notice readable. Both the per-document progress printer and the
// notice write to stderr, and the notice writes WITHOUT a newline and then
// erases its line, so running them together interleaves them on one line and
// the erase wipes whatever the printer left. `index --doc` therefore runs the
// pass silent: for one document its progress lines say nothing the notice does
// not.
func TestSingleDocEmbedPassPrintsNoProgress(t *testing.T) {
	v := testutil.NewTestVault(t)
	const provider = "fake-embedder-progress"
	ai.DefaultRegistry.RegisterEmbedder(provider, &fakeEmbedder{name: provider, dims: 8, available: true, fill: 1})
	testutil.CreateAndIndex(t, v, "Progress Note", "note", "content worth embedding")

	cfg := ai.AIConfig{Provider: provider, EmbeddingModel: "fake-model"}
	prev := flagPorcelain
	t.Cleanup(func() { flagPorcelain = prev })
	flagPorcelain = false

	quiet := captureStderr(t, func() {
		if _, err := embedDocuments(context.Background(), v, cfg, withoutEmbedProgress); err != nil {
			t.Errorf("embed: %v", err)
		}
	})
	if quiet != "" {
		t.Errorf("the single-doc pass wrote %q to stderr; it must stay silent so the slow-call notice owns the line", quiet)
	}
}

// TestWholeVaultEmbedPassStillPrintsProgress: the other half. A whole-vault run
// has real per-document progress worth watching, and no notice competing for
// the line, so it must keep printing.
func TestWholeVaultEmbedPassStillPrintsProgress(t *testing.T) {
	v := testutil.NewTestVault(t)
	const provider = "fake-embedder-progress-loud"
	ai.DefaultRegistry.RegisterEmbedder(provider, &fakeEmbedder{name: provider, dims: 8, available: true, fill: 1})
	testutil.CreateAndIndex(t, v, "Loud Note", "note", "content worth embedding")

	cfg := ai.AIConfig{Provider: provider, EmbeddingModel: "fake-model"}
	prev := flagPorcelain
	t.Cleanup(func() { flagPorcelain = prev })
	flagPorcelain = false

	loud := captureStderr(t, func() {
		if _, err := embedDocuments(context.Background(), v, cfg, withEmbedProgress); err != nil {
			t.Errorf("embed: %v", err)
		}
	})
	if !strings.Contains(loud, "embedding 1 documents") || !strings.Contains(loud, "embedded 1/1") {
		t.Errorf("the whole-vault pass lost its progress output; stderr = %q", loud)
	}
}

// TestPorcelainSilencesTheEmbedPass: unchanged behavior, restated here because
// embedProgressOpts is now the single place that decides it.
func TestPorcelainSilencesTheEmbedPass(t *testing.T) {
	prev := flagPorcelain
	t.Cleanup(func() { flagPorcelain = prev })

	flagPorcelain = true
	if opts := embedProgressOpts(withEmbedProgress); opts.OnStart != nil || opts.OnEvent != nil {
		t.Error("--porcelain must silence the pass even when progress is requested")
	}
	flagPorcelain = false
	if opts := embedProgressOpts(withoutEmbedProgress); opts.OnStart != nil || opts.OnEvent != nil {
		t.Error("withoutEmbedProgress must produce no callbacks")
	}
	if opts := embedProgressOpts(withEmbedProgress); opts.OnStart == nil || opts.OnEvent == nil {
		t.Error("withEmbedProgress on a TTY-bound run must produce callbacks")
	}
}
