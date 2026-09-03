package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/apresai/2ndbrain/internal/ai"
	"github.com/apresai/2ndbrain/internal/testutil"
	"github.com/apresai/2ndbrain/internal/vault"
)

// lockNote makes a note unopenable for the rest of the test and restores its
// mode immediately, via t.Cleanup. The restore is not optional: t.TempDir()
// cannot remove a directory holding a 0o000 file, and that failure surfaces as
// the whole package's cleanup failing rather than as this test.
func lockNote(t *testing.T, abs string) {
	t.Helper()
	if err := os.Chmod(abs, 0o000); err != nil {
		t.Fatalf("lock note %s: %v", abs, err)
	}
	t.Cleanup(func() { _ = os.Chmod(abs, 0o644) })
}

// TestForceReembedKeepsPaidEmbeddingsWhenANoteCannotBeRead is the money
// assertion of this round. A note that cannot be OPENED counted as a failed
// embedding call, which made the run incomplete, which restored the whole
// vault's previous embeddings. One permission bit on the last note of a
// 300-note rebuild therefore threw away 299 embeddings the user had just paid
// for. The unreadable note is named instead, and everything that did embed
// stays embedded.
//
// The embedder is the package's test-local interface fake (fake_embedder_test.go),
// the same one the sibling unparseable test uses: the note never reaches a
// provider, so this is about the CLI's completion arithmetic and costs nothing.
func TestForceReembedKeepsPaidEmbeddingsWhenANoteCannotBeRead(t *testing.T) {
	v := testutil.NewTestVault(t)

	const provider = "fake-embedder-unreadable"
	ai.DefaultRegistry.RegisterEmbedder(provider, &fakeEmbedder{name: provider, dims: 8, available: true, fill: 1})

	first := testutil.CreateAndIndex(t, v, "Keeper One", "note", "The first note with genuinely embeddable content.")
	second := testutil.CreateAndIndex(t, v, "Keeper Two", "note", "The second note with genuinely embeddable content.")
	locked := testutil.CreateAndIndex(t, v, "Locked", "note", "This one stops being readable.")

	lockNote(t, filepath.Join(v.Root, locked.Path))

	cfg := ai.AIConfig{Provider: provider, EmbeddingModel: "fake-model"}
	stats, err := forceReembedDocuments(context.Background(), v, cfg)
	if err == nil {
		t.Fatal("force-reembed must not report success while a note it could not open is still unembedded")
	}
	if !errors.Is(err, errReembedUnreadable) {
		t.Fatalf("err = %v, want errReembedUnreadable so the caller knows not to roll back", err)
	}
	if len(stats.Unreadable) != 1 || stats.Unreadable[0].Path != locked.Path {
		t.Fatalf("stats.Unreadable = %+v, want exactly %s", stats.Unreadable, locked.Path)
	}
	if stats.Unreadable[0].Err == "" {
		t.Error("the unreadable entry carries no reason, so nothing tells the user why the note would not open")
	}
	if stats.Failed != 0 {
		t.Errorf("stats.Failed = %d, want 0: no embedding call was made for a note that would not open", stats.Failed)
	}
	if stats.Embedded != 2 {
		t.Errorf("stats.Embedded = %d, want 2 (both readable notes)", stats.Embedded)
	}

	// The whole point: the embeddings that were paid for survived.
	for _, keeper := range []struct {
		title string
		id    string
	}{{"Keeper One", first.ID}, {"Keeper Two", second.ID}} {
		vec, gerr := v.DB.GetEmbedding(keeper.id)
		if gerr != nil || len(vec) == 0 {
			t.Fatalf("%s lost its embedding (err=%v, len=%d): the run rolled back over a note it could not open",
				keeper.title, gerr, len(vec))
		}
		var vecChunks int
		if qerr := v.DB.Conn().QueryRow(
			`SELECT COUNT(*) FROM vec_chunks WHERE chunk_id IN (SELECT id FROM chunks WHERE doc_id = ?)`, keeper.id,
		).Scan(&vecChunks); qerr != nil {
			t.Fatalf("count vec_chunks for %s: %v", keeper.title, qerr)
		}
		if vecChunks == 0 {
			t.Errorf("%s has no chunk vectors after force-reembed; the vector leg would be empty for it", keeper.title)
		}
	}
}

// TestIndexForceReembedReportsUnreadableNotesOnBothSurfaces covers the reporting
// half of the same fix: the run still says what it could not do, on stdout as
// JSON and on stderr as prose, and still exits non-zero. It also pins the
// dedupe: the walk and the embed pass BOTH meet the locked note, and one note
// to fix is one entry.
func TestIndexForceReembedReportsUnreadableNotesOnBothSurfaces(t *testing.T) {
	v, root := newContractVault(t)

	// Index with no embedding provider first, so the notes reach the force-reembed
	// with no stored vectors. That is what makes the rollback visible: restoring a
	// snapshot of nothing wipes exactly the embeddings the run just produced.
	v.Config.AI.Provider = "no-provider"
	if err := v.Config.Save(v.DotDir); err != nil {
		t.Fatalf("save config: %v", err)
	}
	for _, name := range []string{"keeper-one", "keeper-two", "locked"} {
		body := "---\ntitle: " + name + "\n---\n\nbody text for " + name + "\n"
		if err := os.WriteFile(filepath.Join(root, name+".md"), []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	if out, err := runCLIArgs(t, root, "index"); err != nil {
		t.Fatalf("initial index: %v\n%s", err, out)
	}

	const provider = "fake-embedder-unreadable-cli"
	ai.DefaultRegistry.RegisterEmbedder(provider, &fakeEmbedder{name: provider, dims: 8, available: true, fill: 1})
	v.Config.AI.Provider = provider
	v.Config.AI.EmbeddingModel = "fake-model"
	v.Config.AI.Dimensions = 8
	if err := v.Config.Save(v.DotDir); err != nil {
		t.Fatalf("save config: %v", err)
	}

	lockNote(t, filepath.Join(root, "locked.md"))

	var out []byte
	var runErr error
	stderr := captureStderr(t, func() {
		out, runErr = runCLIArgs(t, root, "index", "--force-reembed", "--json")
	})
	if runErr == nil {
		t.Fatalf("index --force-reembed must exit non-zero while a note could not be read; stderr=%q", stderr)
	}
	var got struct {
		Embedded    int `json:"embedded"`
		EmbedFailed int `json:"embed_failed"`
		Unreadable  []struct {
			Path string `json:"path"`
			Err  string `json:"error"`
		} `json:"unreadable"`
	}
	// A streaming decoder, because the test harness routes cobra's own error
	// line into the same capture buffer as stdout; only the first JSON value is
	// the command's output.
	if err := json.NewDecoder(bytes.NewReader(out)).Decode(&got); err != nil {
		t.Fatalf("index --force-reembed --json must still print its envelope when a note could not be read; decode (%s): %v", out, err)
	}
	if len(got.Unreadable) != 1 || got.Unreadable[0].Path != "locked.md" {
		t.Fatalf("unreadable = %+v, want exactly locked.md once (the walk and the embed pass both report it)", got.Unreadable)
	}
	if got.EmbedFailed != 0 {
		t.Errorf("embed_failed = %d, want 0: nothing was sent for the note that would not open", got.EmbedFailed)
	}
	if got.Embedded != 2 {
		t.Errorf("embedded = %d, want 2 (both readable notes)", got.Embedded)
	}

	// The two readable notes kept the embeddings this run produced.
	rv, err := vault.Open(root)
	if err != nil {
		t.Fatalf("reopen vault: %v", err)
	}
	defer rv.Close()
	count, cerr := rv.DB.EmbeddingCount()
	if cerr != nil {
		t.Fatalf("embedding count: %v", cerr)
	}
	if count != 2 {
		t.Fatalf("embedding count = %d, want 2: the run discarded what it paid for", count)
	}

	// The human summary names the note too, and that run also exits non-zero.
	var humanErr error
	humanStderr := captureStderr(t, func() {
		_, humanErr = runCLIArgs(t, root, "index", "--force-reembed")
	})
	if humanErr == nil {
		t.Error("the human form must exit non-zero as well")
	}
	if !strings.Contains(humanStderr, "could not read 1 note(s)") || !strings.Contains(humanStderr, "locked.md") {
		t.Errorf("stderr = %q, want the unreadable note named in the summary", humanStderr)
	}
}
