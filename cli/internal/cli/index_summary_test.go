package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/apresai/2ndbrain/internal/ai"
	"github.com/apresai/2ndbrain/internal/testutil"
	"github.com/apresai/2ndbrain/internal/vault"
)

// TestForceReembedJSONListsAnEmbedPhaseUnparseableNote is reviewer G3. The JSON
// envelope was built from the WALK's stats alone, so a note that only fails in
// the embed phase (its row survived the walk, it broke afterwards) never
// appeared in `index --force-reembed --json` at all, while the human run printed
// it. Two surfaces, two answers.
func TestForceReembedJSONListsAnEmbedPhaseUnparseableNote(t *testing.T) {
	v := testutil.NewTestVault(t)
	const provider = "fake-embedder-merged-summary"
	ai.DefaultRegistry.RegisterEmbedder(provider, &fakeEmbedder{name: provider, dims: 8, available: true, fill: 1})

	testutil.CreateAndIndex(t, v, "Keeper", "note", "content worth embedding")
	broken := testutil.CreateAndIndex(t, v, "Breaker", "note", "about to stop parsing")
	if err := os.WriteFile(filepath.Join(v.Root, broken.Path), []byte("---\ntitle: @nope\n---\n\nbody\n"), 0o644); err != nil {
		t.Fatalf("break note: %v", err)
	}

	cfg := ai.AIConfig{Provider: provider, EmbeddingModel: "fake-model"}
	stats, err := forceReembedDocuments(context.Background(), v, cfg)
	if err != nil {
		t.Fatalf("force-reembed: %v", err)
	}
	// The walk never ran here, so the ONLY source of this note is the embed
	// pass. Merging is what puts it in the envelope.
	merged := vault.MergeUnparseable(nil, stats.Unparseable)
	if len(merged) != 1 || merged[0].Path != broken.Path {
		t.Fatalf("merged unparseable = %+v, want the embed-phase note %s", merged, broken.Path)
	}
}

// TestMergeUnparseableDedupesByPath: a note reported by both phases is one note
// to fix, not two.
func TestMergeUnparseableDedupesByPath(t *testing.T) {
	walk := []vault.UnparseableDoc{{Path: "a.md", Err: "from the walk"}}
	embed := []vault.UnparseableDoc{{Path: "a.md", Err: "from the embed pass"}, {Path: "b.md", Err: "second"}}
	got := vault.MergeUnparseable(walk, embed)
	if len(got) != 2 {
		t.Fatalf("merged = %+v, want 2 entries", got)
	}
	if got[0].Path != "a.md" || got[0].Err != "from the walk" {
		t.Errorf("merged[0] = %+v, want the FIRST report for a.md kept", got[0])
	}
	if got[1].Path != "b.md" {
		t.Errorf("merged[1] = %+v, want b.md", got[1])
	}
	if vault.MergeUnparseable() == nil {
		t.Error("MergeUnparseable with nothing must return an empty slice, never nil: the JSON key is always present")
	}
}

// TestUnparseableSummaryHintOnlyWhenElided is reviewer G2: the summary printed
// every path AND told the reader to run -v to list them, which is a hint to run
// a command that shows nothing new.
func TestUnparseableSummaryHintOnlyWhenElided(t *testing.T) {
	prevP, prevV := flagPorcelain, flagVerbose
	t.Cleanup(func() { flagPorcelain, flagVerbose = prevP, prevV })
	flagPorcelain, flagVerbose = false, false

	short := make([]vault.UnparseableDoc, unparseableSummaryLimit)
	for i := range short {
		short[i] = vault.UnparseableDoc{Path: "note.md", Err: "bad"}
	}
	out := captureStderr(t, func() { reportUnparseable(short) })
	if strings.Contains(out, "run with -v") {
		t.Errorf("a complete list must not point at -v; got %q", out)
	}
	if !strings.Contains(out, "skipped 5 unparseable note(s)") {
		t.Errorf("output = %q, want the count", out)
	}

	long := append(short, vault.UnparseableDoc{Path: "sixth.md", Err: "bad"})
	out = captureStderr(t, func() { reportUnparseable(long) })
	if !strings.Contains(out, "... and 1 more (run with -v to list them all)") {
		t.Errorf("output = %q, want the elision notice when paths were withheld", out)
	}
	if strings.Contains(out, "sixth.md") {
		t.Errorf("output = %q, want the sixth path withheld", out)
	}

	flagVerbose = true
	out = captureStderr(t, func() { reportUnparseable(long) })
	if !strings.Contains(out, "sixth.md") || strings.Contains(out, "run with -v") {
		t.Errorf("with -v the full list prints and the hint disappears; got %q", out)
	}
}

// TestIndexJSONAlwaysCarriesTheListsAndCounts is skill finding K1 plus M3: the
// key vanished when empty (omitempty), against the project rule that JSON keys
// are always present and never null, and the embed counters were invisible.
func TestIndexJSONAlwaysCarriesTheListsAndCounts(t *testing.T) {
	_, root := newContractVault(t)
	if err := os.WriteFile(filepath.Join(root, "fine.md"),
		[]byte("---\ntitle: Fine\n---\n\nreadable body\n"), 0o644); err != nil {
		t.Fatalf("write note: %v", err)
	}

	out, err := runCLIArgs(t, root, "index", "--json")
	if err != nil {
		t.Fatalf("index --json: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(out, &raw); err != nil {
		t.Fatalf("decode: %v\n%s", err, out)
	}
	for _, key := range []string{
		"files_scanned", "docs_indexed", "chunks_created", "links_found", "errors",
		"excluded_purged", "embedded", "embed_failed", "embed_skipped", "embed_retries",
		"unparseable", "unreadable",
	} {
		v, ok := raw[key]
		if !ok {
			t.Errorf("index --json is missing %q; every key is always present", key)
			continue
		}
		if v == nil {
			t.Errorf("index --json %q is null; lists are emitted empty, never null", key)
		}
	}
	if lst, ok := raw["unparseable"].([]any); !ok || len(lst) != 0 {
		t.Errorf("unparseable = %v, want an empty array on a clean vault", raw["unparseable"])
	}
}
