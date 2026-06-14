package polish

import (
	"testing"

	"github.com/apresai/2ndbrain/internal/testutil"
)

func TestSnapshotPath_DeterministicAndUnique(t *testing.T) {
	v := testutil.NewTestVault(t)

	a1 := SnapshotPath(v, "notes/foo.md")
	a2 := SnapshotPath(v, "notes/foo.md")
	if a1 != a2 {
		t.Errorf("SnapshotPath not deterministic: %q vs %q", a1, a2)
	}
	// Same basename, different folder → different snapshot file.
	b := SnapshotPath(v, "archive/foo.md")
	if a1 == b {
		t.Errorf("same-basename notes in different folders collided: %q", a1)
	}
}

func TestSnapshotRoundTrip(t *testing.T) {
	v := testutil.NewTestVault(t)

	snap := PolishSnapshot{
		Path:          "note.md",
		OriginalFull:  "---\ntitle: Note\n---\n\noriginal body\n",
		PolishedBody:  "polished body",
		Provider:      "bedrock",
		Model:         "haiku",
		Links:         true,
		Timestamp:     "2026-06-14T00:00:00Z",
		PostWriteHash: HashContent([]byte("written")),
	}
	if err := WriteSnapshot(v, snap); err != nil {
		t.Fatalf("WriteSnapshot: %v", err)
	}

	got, err := LoadSnapshot(v, "note.md")
	if err != nil {
		t.Fatalf("LoadSnapshot: %v", err)
	}
	if got == nil {
		t.Fatal("LoadSnapshot returned nil for an existing snapshot")
	}
	if got.Version != SnapshotVersion {
		t.Errorf("version not stamped: got %d", got.Version)
	}
	if got.OriginalFull != snap.OriginalFull || got.PostWriteHash != snap.PostWriteHash || got.Links != true {
		t.Errorf("round-trip mismatch: %+v", got)
	}

	if err := DeleteSnapshot(v, "note.md"); err != nil {
		t.Fatalf("DeleteSnapshot: %v", err)
	}
	got, err = LoadSnapshot(v, "note.md")
	if err != nil {
		t.Fatalf("LoadSnapshot after delete: %v", err)
	}
	if got != nil {
		t.Errorf("snapshot should be gone after delete, got %+v", got)
	}
	// Deleting a missing snapshot is a no-op, not an error.
	if err := DeleteSnapshot(v, "note.md"); err != nil {
		t.Errorf("DeleteSnapshot of missing file should be nil, got %v", err)
	}
}

func TestLoadSnapshot_Missing(t *testing.T) {
	v := testutil.NewTestVault(t)
	got, err := LoadSnapshot(v, "never-polished.md")
	if err != nil {
		t.Fatalf("missing snapshot must not error: %v", err)
	}
	if got != nil {
		t.Fatalf("missing snapshot must return nil, got %+v", got)
	}
}

func TestClassifyUndo(t *testing.T) {
	original := "---\ntitle: X\n---\n\noriginal\n"
	written := "---\ntitle: X\n---\n\npolished\n"
	snap := &PolishSnapshot{
		OriginalFull:  original,
		PostWriteHash: HashContent([]byte(written)),
	}

	if d := ClassifyUndo([]byte(written), snap); d != UndoProceed {
		t.Errorf("clean (current==written) → want UndoProceed, got %v", d)
	}
	if d := ClassifyUndo([]byte(original), snap); d != UndoNoop {
		t.Errorf("already reverted (current==original) → want UndoNoop, got %v", d)
	}
	if d := ClassifyUndo([]byte("edited since polish"), snap); d != UndoConflict {
		t.Errorf("edited → want UndoConflict, got %v", d)
	}
}
