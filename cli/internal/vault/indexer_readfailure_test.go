package vault

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/apresai/2ndbrain/internal/document"
	"github.com/apresai/2ndbrain/internal/search"
)

// makeUnreadable removes every permission bit from path and probes the result.
// A mode change is not enough on its own: running as root, or on a filesystem
// that ignores mode bits, leaves the file readable, and the test would then be
// asserting nothing. Probing the actual capability is the project's rule.
func makeUnreadable(t *testing.T, path string) {
	t.Helper()
	if err := os.Chmod(path, 0o000); err != nil {
		t.Skipf("cannot change mode on this filesystem: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o644) })
	if _, err := os.ReadFile(path); err == nil {
		t.Skip("the file is still readable after chmod 0o000 (running as root, or a filesystem that ignores mode bits)")
	}
}

// TestIndexKeepsTheRowOfANoteItCannotRead is Bugbot B1. indexFile wrapped EVERY
// ParseFile failure as ErrUnparseable, and ParseFile returns os.ReadFile errors
// through the same path, so a permission bit or a file locked mid-save made the
// whole-vault walk DELETE the note's row, its chunks and its embeddings. A read
// failure says nothing about a note's contents and must cost it nothing.
func TestIndexKeepsTheRowOfANoteItCannotRead(t *testing.T) {
	root := t.TempDir()
	v, err := Init(root)
	if err != nil {
		t.Fatalf("init vault: %v", err)
	}
	defer v.Close()

	abs := writeNote(t, root, "locked.md", "---\ntitle: Locked\n---\n\nzarquon distinctive content\n")
	writeNote(t, root, "other.md", "---\ntitle: Other\n---\n\nunrelated prose\n")
	if _, err := IndexVault(v, nil); err != nil {
		t.Fatalf("first index: %v", err)
	}

	var docID string
	if err := v.DB.Conn().QueryRow(`SELECT id FROM documents WHERE path = ?`, "locked.md").Scan(&docID); err != nil {
		t.Fatalf("the note was not indexed to begin with: %v", err)
	}
	if err := v.DB.SetEmbedding(docID, []float32{0.1, 0.2, 0.3}, "test-model", "test-hash"); err != nil {
		t.Fatalf("seed embedding: %v", err)
	}

	makeUnreadable(t, abs)

	stats, err := IndexVault(v, nil)
	if err != nil {
		t.Fatalf("second index: %v", err)
	}
	if len(stats.Unparseable) != 0 {
		t.Errorf("stats.Unparseable = %+v, want none: a file that cannot be READ is not unparseable", stats.Unparseable)
	}
	// Errorf, not Fatalf: the assertions below are the ones about DATA LOSS, and
	// they must still run (and be visible in the proof) when the classification
	// is wrong.
	if len(stats.Unreadable) != 1 || stats.Unreadable[0].Path != "locked.md" {
		t.Errorf("stats.Unreadable = %+v, want exactly locked.md", stats.Unreadable)
	}
	if stats.Errors != 1 {
		t.Errorf("stats.Errors = %d, want 1: the run did fail to index it", stats.Errors)
	}

	var rows int
	if err := v.DB.Conn().QueryRow(`SELECT COUNT(*) FROM documents WHERE path = ?`, "locked.md").Scan(&rows); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if rows != 1 {
		t.Errorf("the index row was deleted (%d rows) because the file could not be read", rows)
	}
	var chunks int
	if err := v.DB.Conn().QueryRow(`SELECT COUNT(*) FROM chunks WHERE doc_id = ?`, docID).Scan(&chunks); err != nil {
		t.Fatalf("count chunks: %v", err)
	}
	if chunks == 0 {
		t.Error("the note's chunks were deleted because the file could not be read")
	}
	vec, err := v.DB.GetEmbedding(docID)
	if err != nil || len(vec) != 3 {
		t.Errorf("the note's embedding was destroyed (err=%v, len=%d); re-embedding it costs real money", err, len(vec))
	}

	// It still answers searches, which is the whole point: the index holds the
	// last thing known about the note, and nothing new was learned.
	hits, err := search.NewEngine(v.DB.Conn()).Search(search.Options{Query: "zarquon"})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(hits) != 1 {
		t.Errorf("search hits = %d, want 1: an unreadable note keeps answering from what was indexed", len(hits))
	}
}

// TestIndexStillDropsTheRowOfANoteItCannotParse: the other side of the
// classification. A note whose CONTENT is broken is still dropped, and does not
// leak into the unreadable list.
func TestIndexStillDropsTheRowOfANoteItCannotParse(t *testing.T) {
	root := t.TempDir()
	v, err := Init(root)
	if err != nil {
		t.Fatalf("init vault: %v", err)
	}
	defer v.Close()

	writeNote(t, root, "breaks.md", "---\ntitle: Breaks\n---\n\nbody\n")
	if _, err := IndexVault(v, nil); err != nil {
		t.Fatalf("first index: %v", err)
	}
	writeNote(t, root, "breaks.md", unparseableFrontmatter)

	stats, err := IndexVault(v, nil)
	if err != nil {
		t.Fatalf("second index: %v", err)
	}
	if len(stats.Unparseable) != 1 {
		t.Errorf("stats.Unparseable = %+v, want the broken note", stats.Unparseable)
	}
	if len(stats.Unreadable) != 0 {
		t.Errorf("stats.Unreadable = %+v, want none: the file read fine, its content did not parse", stats.Unreadable)
	}
	var rows int
	_ = v.DB.Conn().QueryRow(`SELECT COUNT(*) FROM documents WHERE path = ?`, "breaks.md").Scan(&rows)
	if rows != 0 {
		t.Errorf("the unparseable note kept its row (%d)", rows)
	}
}

// TestParseFileClassifiesAReadFailure pins the sentinel at its source, for every
// extension ParseFile handles: all three read the file before dispatching, so
// all three must report a read failure as one.
func TestParseFileClassifiesAReadFailure(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"missing.md", "missing.canvas", "missing.base"} {
		_, err := document.ParseFile(filepath.Join(dir, name))
		if err == nil {
			t.Fatalf("%s: expected an error for a missing file", name)
		}
		if !errors.Is(err, document.ErrRead) {
			t.Errorf("%s: err = %v, want it to classify as document.ErrRead", name, err)
		}
	}
	// A note that reads fine but will not parse is NOT a read failure.
	bad := writeNote(t, dir, "bad.md", unparseableFrontmatter)
	_, err := document.ParseFile(bad)
	if err == nil {
		t.Fatal("expected a parse error")
	}
	if errors.Is(err, document.ErrRead) {
		t.Errorf("err = %v, want a parse failure, not a read failure", err)
	}
}

// TestIndexPurgesRowsUnderAFolderExcludedAfterIndexing is Bugbot B2 / reviewer
// G1. The walk SkipDirs an excluded folder, and purgeStale only removed rows
// whose FILE was gone, so notes indexed before the folder was marked as
// templates kept their rows, chunks and vectors forever: they answered searches
// and vault status counted them.
func TestIndexPurgesRowsUnderAFolderExcludedAfterIndexing(t *testing.T) {
	root := t.TempDir()
	v, err := Init(root)
	if err != nil {
		t.Fatalf("init vault: %v", err)
	}
	defer v.Close()

	writeNote(t, root, "templates/note.md", "---\ntitle: Template\n---\n\nzarquon distinctive content\n")
	writeNote(t, root, "keeps.md", "---\ntitle: Keeps\n---\n\nunrelated prose\n")
	if _, err := IndexVault(v, nil); err != nil {
		t.Fatalf("first index: %v", err)
	}

	var docID string
	if err := v.DB.Conn().QueryRow(`SELECT id FROM documents WHERE path = ?`, "templates/note.md").Scan(&docID); err != nil {
		t.Fatalf("the template was not indexed to begin with: %v", err)
	}
	if err := v.DB.SetEmbedding(docID, []float32{0.1, 0.2, 0.3}, "test-model", "test-hash"); err != nil {
		t.Fatalf("seed embedding: %v", err)
	}

	// Only NOW is the folder declared a template folder.
	writeObsidianJSON(t, root, []string{".obsidian", "templates.json"}, `{"folder":"templates"}`)

	stats, err := IndexVault(v, nil)
	if err != nil {
		t.Fatalf("second index: %v", err)
	}
	if stats.ExcludedPurged != 1 {
		t.Errorf("stats.ExcludedPurged = %d, want 1", stats.ExcludedPurged)
	}

	var rows int
	_ = v.DB.Conn().QueryRow(`SELECT COUNT(*) FROM documents WHERE path = ?`, "templates/note.md").Scan(&rows)
	if rows != 0 {
		t.Errorf("the excluded note kept its row (%d); it still answers searches and vault status still counts it", rows)
	}
	var chunks int
	_ = v.DB.Conn().QueryRow(`SELECT COUNT(*) FROM chunks WHERE doc_id = ?`, docID).Scan(&chunks)
	if chunks != 0 {
		t.Errorf("the excluded note kept %d chunks", chunks)
	}
	var vecs int
	_ = v.DB.Conn().QueryRow(`SELECT COUNT(*) FROM vec_chunks WHERE doc_id = ?`, docID).Scan(&vecs)
	if vecs != 0 {
		t.Errorf("the excluded note kept %d chunk vectors", vecs)
	}

	hits, err := search.NewEngine(v.DB.Conn()).Search(search.Options{Query: "zarquon"})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(hits) != 0 {
		t.Errorf("search still returns the excluded note (%d hits)", len(hits))
	}
	// The note outside the folder is untouched.
	hits, err = search.NewEngine(v.DB.Conn()).Search(search.Options{Query: "unrelated"})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(hits) != 1 {
		t.Errorf("the note outside the excluded folder was purged too (%d hits, want 1)", len(hits))
	}
}

// TestDropUnparseableRowLogsItsLookupFailure is reviewer G9: the SELECT failure
// was swallowed while the adjacent delete failure logged, so a row that outlived
// its note did so in silence. A genuinely absent row is not a failure and stays
// quiet.
func TestDropUnparseableRowLogsItsLookupFailure(t *testing.T) {
	root := t.TempDir()
	v, err := Init(root)
	if err != nil {
		t.Fatalf("init vault: %v", err)
	}

	h := &vaultCaptureHandler{}
	prev := slog.Default()
	slog.SetDefault(slog.New(h))
	t.Cleanup(func() { slog.SetDefault(prev) })

	// A note that was never indexed: no row, no failure, no log.
	dropUnparseableRow(v, "never-indexed.md")
	if h.has("look up index row for unparseable note failed") {
		t.Error("a note that was never indexed must not log a lookup failure")
	}

	// A closed DB makes the lookup fail for real.
	if err := v.DB.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}
	dropUnparseableRow(v, "some-note.md")
	if !h.has("look up index row for unparseable note failed") {
		t.Error("a failed lookup was swallowed; a row can outlive its note with nobody told")
	}
}

type vaultCaptureHandler struct {
	mu   sync.Mutex
	msgs []string
}

func (h *vaultCaptureHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h *vaultCaptureHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.msgs = append(h.msgs, r.Message)
	return nil
}
func (h *vaultCaptureHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *vaultCaptureHandler) WithGroup(string) slog.Handler      { return h }
func (h *vaultCaptureHandler) has(msg string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, m := range h.msgs {
		if m == msg {
			return true
		}
	}
	return false
}
