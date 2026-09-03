package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every command that indexes reports what it skipped.
//
// The walk used to print `warning: index <path>: <err>` from its per-file error
// switch, so every caller of vault.IndexVault reported a broken note for free.
// Diverting an unparseable note into its own branch moved the human reporting
// into `2nb index`, which left the two commands that index without being the
// index command saying nothing at all.

// TestSearchAutoIndexReportsSkippedNotes: the auto-index on a first `2nb search`
// is many users' FIRST index, so a note missing from every result afterwards has
// to be named here.
func TestSearchAutoIndexReportsSkippedNotes(t *testing.T) {
	_, root := newContractVault(t)

	if err := os.WriteFile(filepath.Join(root, "fine.md"),
		[]byte("---\ntitle: Fine\n---\n\nsearchable body\n"), 0o644); err != nil {
		t.Fatalf("write fine note: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "broken.md"),
		[]byte("---\ntitle: @nope\n---\n\nbody\n"), 0o644); err != nil {
		t.Fatalf("write broken note: %v", err)
	}
	lockedPath := filepath.Join(root, "locked.md")
	if err := os.WriteFile(lockedPath, []byte("---\ntitle: Locked\n---\n\nbody\n"), 0o644); err != nil {
		t.Fatalf("write locked note: %v", err)
	}
	if err := os.Chmod(lockedPath, 0o000); err != nil {
		t.Fatalf("lock note: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(lockedPath, 0o644) })

	var searchErr error
	stderr := captureStderr(t, func() {
		_, searchErr = runCLIArgs(t, root, "search", "searchable", "--bm25-only")
	})
	if searchErr != nil {
		t.Fatalf("search must not fail because notes were skipped: %v\n%s", searchErr, stderr)
	}
	if !strings.Contains(stderr, "skipped 1 unparseable note(s)") || !strings.Contains(stderr, "broken.md") {
		t.Errorf("stderr = %q, want the unparseable note named", stderr)
	}
	if !strings.Contains(stderr, "could not read 1 note(s)") || !strings.Contains(stderr, "locked.md") {
		t.Errorf("stderr = %q, want the unreadable note named", stderr)
	}
}

// TestImportObsidianReportsSkippedNotes: an import is where a foreign vault's
// broken notes first surface, and it printed nothing about them.
func TestImportObsidianReportsSkippedNotes(t *testing.T) {
	_, root := newContractVault(t)

	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "fine.md"),
		[]byte("---\ntitle: Fine\n---\n\nbody\n"), 0o644); err != nil {
		t.Fatalf("write fine note: %v", err)
	}
	if err := os.WriteFile(filepath.Join(src, "broken.md"),
		[]byte("---\ntitle: @nope\n---\n\nbody\n"), 0o644); err != nil {
		t.Fatalf("write broken note: %v", err)
	}

	target := filepath.Join(t.TempDir(), "imported")
	var importErr error
	stderr := captureStderr(t, func() {
		_, importErr = runCLIArgs(t, root, "import-obsidian", src, "--target", target)
	})
	if importErr != nil {
		t.Fatalf("import-obsidian: %v\n%s", importErr, stderr)
	}
	if !strings.Contains(stderr, "skipped 1 unparseable note(s)") || !strings.Contains(stderr, "broken.md") {
		t.Errorf("stderr = %q, want the unparseable note named in the import summary", stderr)
	}
}
