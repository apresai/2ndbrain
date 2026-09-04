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
)

// TestForceReembedCompletesWhenANoteCannotBeParsed is the regression this cycle
// exists for. A vault of 314 notes, one of which had frontmatter the parser
// rejected, failed `index --force-reembed` three times in a row: each run
// re-embedded 313 documents over 70 to 90 seconds, counted the unreadable note
// as a FAILED embedding, declared the run incomplete, and restored the previous
// embeddings. The parse failure is not an embedding failure, so the run must
// finish and keep what it embedded.
//
// The embedder is the package's test-local interface fake, not a provider mock:
// the note never reaches a provider, so this test is about the CLI's completion
// arithmetic and costs nothing.
func TestForceReembedCompletesWhenANoteCannotBeParsed(t *testing.T) {
	v := testutil.NewTestVault(t)

	const provider = "fake-embedder-unparseable"
	// A dedicated provider name: DefaultRegistry is global and registering under
	// a real provider's name would leak into every other test in the package.
	ai.DefaultRegistry.RegisterEmbedder(provider, &fakeEmbedder{name: provider, dims: 8, available: true, fill: 1})

	good := testutil.CreateAndIndex(t, v, "Keeper", "note", "This note has genuinely embeddable content.")
	broken := testutil.CreateAndIndex(t, v, "Breaker", "note", "This one is about to stop parsing.")

	// Break the note on disk while its index row stays: exactly the state the
	// embed pass hits when a note changed after it was indexed.
	brokenAbs := filepath.Join(v.Root, broken.Path)
	if err := os.WriteFile(brokenAbs, []byte("---\ntitle: @nope\n---\n\nbody\n"), 0o644); err != nil {
		t.Fatalf("break note: %v", err)
	}

	cfg := ai.AIConfig{Provider: provider, EmbeddingModel: "fake-model"}
	stats, err := forceReembedDocuments(context.Background(), v, cfg)
	if err != nil {
		t.Fatalf("force-reembed must complete when the only shortfall is an unreadable note: %v", err)
	}
	if len(stats.Unparseable) != 1 {
		t.Fatalf("stats.Unparseable = %+v, want exactly the broken note", stats.Unparseable)
	}
	if stats.Unparseable[0].Path != broken.Path {
		t.Errorf("unparseable path = %q, want %q", stats.Unparseable[0].Path, broken.Path)
	}
	if stats.Failed != 0 {
		t.Errorf("stats.Failed = %d, want 0 (an unreadable note is not a failed embedding call)", stats.Failed)
	}
	if stats.Embedded != 1 {
		t.Errorf("stats.Embedded = %d, want 1 (the readable note)", stats.Embedded)
	}

	// The readable note's embeddings SURVIVED, i.e. nothing was rolled back.
	vec, err := v.DB.GetEmbedding(good.ID)
	if err != nil || len(vec) == 0 {
		t.Fatalf("the readable note lost its embedding (err=%v, len=%d): the run rolled back", err, len(vec))
	}
	var vecChunks int
	if err := v.DB.Conn().QueryRow(
		`SELECT COUNT(*) FROM vec_chunks WHERE chunk_id IN (SELECT id FROM chunks WHERE doc_id = ?)`, good.ID,
	).Scan(&vecChunks); err != nil {
		t.Fatalf("count vec_chunks: %v", err)
	}
	if vecChunks == 0 {
		t.Error("the readable note has no chunk vectors after force-reembed; the vector leg would be empty for it")
	}
}

// TestIndexReportsUnparseableNotesWithoutBreakingTheStdoutContract covers the
// two report surfaces at once: `index --json` names the notes that could not be
// read, and the human run still prints the "Indexed N files, N chunks, N links"
// line the macOS app parses.
func TestIndexReportsUnparseableNotesWithoutBreakingTheStdoutContract(t *testing.T) {
	_, root := newContractVault(t)

	if err := os.WriteFile(filepath.Join(root, "fine.md"),
		[]byte("---\ntitle: Fine\n---\n\nreadable body\n"), 0o644); err != nil {
		t.Fatalf("write fine note: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "broken.md"),
		[]byte("---\ntitle: @nope\n---\n\nbody\n"), 0o644); err != nil {
		t.Fatalf("write broken note: %v", err)
	}

	out, err := runCLIArgs(t, root, "index", "--json")
	if err != nil {
		t.Fatalf("index --json must not fail because one note cannot be parsed: %v", err)
	}
	var got struct {
		DocsIndexed int `json:"docs_indexed"`
		Unparseable []struct {
			Path string `json:"path"`
			Err  string `json:"error"`
		} `json:"unparseable"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("decode index --json (%s): %v", out, err)
	}
	if len(got.Unparseable) != 1 || got.Unparseable[0].Path != "broken.md" {
		t.Fatalf("index --json unparseable = %+v, want exactly broken.md", got.Unparseable)
	}
	if got.Unparseable[0].Err == "" {
		t.Error("unparseable entry carries no parser message, so nothing tells the user what to fix")
	}
	if got.DocsIndexed != 1 {
		t.Errorf("docs_indexed = %d, want 1 (the readable note)", got.DocsIndexed)
	}

	human, err := runCLIArgs(t, root, "index")
	if err != nil {
		t.Fatalf("index: %v", err)
	}
	if !strings.Contains(string(human), "Indexed 1 files, ") {
		t.Errorf("stdout = %q, want the unchanged \"Indexed N files, N chunks, N links\" contract line", human)
	}
}
