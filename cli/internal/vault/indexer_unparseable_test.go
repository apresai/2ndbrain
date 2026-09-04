package vault

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/apresai/2ndbrain/internal/search"
)

// unparseableFrontmatter is frontmatter that still fails to parse after the
// empty-frontmatter-block fix: "@" cannot start a YAML token. Using a shape the
// parser genuinely rejects keeps this test about the INDEXER's handling of a
// bad note rather than about the parser.
const unparseableFrontmatter = "---\ntitle: @nope\n---\n\n# Broken\n\nunreadable\n"

func writeNote(t *testing.T, root, rel, content string) string {
	t.Helper()
	abs := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
	return abs
}

// TestIndexVaultDropsRowOfNoteThatStoppedParsing locks the rule that an index
// row never outlives the note's readability: once a note stops parsing, its
// stale chunks must stop answering searches instead of serving the last good
// version forever.
func TestIndexVaultDropsRowOfNoteThatStoppedParsing(t *testing.T) {
	root := t.TempDir()
	v, err := Init(root)
	if err != nil {
		t.Fatalf("init vault: %v", err)
	}
	defer v.Close()

	writeNote(t, root, "breaks.md", "---\ntitle: Breaks\n---\n\n# Breaks\n\nzarquon distinctive content\n")
	writeNote(t, root, "keeps.md", "---\ntitle: Keeps\n---\n\n# Keeps\n\nunrelated prose\n")

	if _, err := IndexVault(v, nil); err != nil {
		t.Fatalf("first index: %v", err)
	}

	eng := search.NewEngine(v.DB.Conn())
	hits, err := eng.Search(search.Options{Query: "zarquon"})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("before breaking the note, search for its content returned %d hits, want 1", len(hits))
	}

	// The note is edited into a shape that will not parse.
	writeNote(t, root, "breaks.md", unparseableFrontmatter)

	stats, err := IndexVault(v, nil)
	if err != nil {
		t.Fatalf("second index: %v", err)
	}
	if len(stats.Unparseable) != 1 {
		t.Fatalf("stats.Unparseable = %+v, want exactly the one broken note", stats.Unparseable)
	}
	if stats.Unparseable[0].Path != "breaks.md" {
		t.Errorf("unparseable path = %q, want breaks.md", stats.Unparseable[0].Path)
	}
	if stats.Unparseable[0].Err == "" {
		t.Error("unparseable entry carries no parser message")
	}

	var rows int
	if err := v.DB.Conn().QueryRow(`SELECT COUNT(*) FROM documents WHERE path = ?`, "breaks.md").Scan(&rows); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if rows != 0 {
		t.Errorf("index row for the unparseable note survived (%d rows); its stale chunks keep answering searches", rows)
	}

	hits, err = eng.Search(search.Options{Query: "zarquon"})
	if err != nil {
		t.Fatalf("search after break: %v", err)
	}
	if len(hits) != 0 {
		t.Errorf("search still returns the unparseable note (%d hits); the index is serving a version that is no longer on disk", len(hits))
	}

	// The healthy note is untouched.
	hits, err = eng.Search(search.Options{Query: "unrelated"})
	if err != nil {
		t.Fatalf("search healthy: %v", err)
	}
	if len(hits) != 1 {
		t.Errorf("healthy note hits = %d, want 1 (one bad note must not take the vault with it)", len(hits))
	}
}

// TestIndexVaultKeepsDatabaseFailuresOutOfUnparseable guards the classification:
// only a note the PARSER rejected is reported as unparseable, so the list stays
// a list of notes to fix.
func TestIndexVaultKeepsDatabaseFailuresOutOfUnparseable(t *testing.T) {
	root := t.TempDir()
	v, err := Init(root)
	if err != nil {
		t.Fatalf("init vault: %v", err)
	}
	defer v.Close()

	writeNote(t, root, "fine.md", "---\ntitle: Fine\n---\n\nbody\n")
	stats, err := IndexVault(v, nil)
	if err != nil {
		t.Fatalf("index: %v", err)
	}
	if len(stats.Unparseable) != 0 {
		t.Errorf("stats.Unparseable = %+v, want empty for a clean vault", stats.Unparseable)
	}
	if stats.Errors != 0 {
		t.Errorf("stats.Errors = %d, want 0", stats.Errors)
	}
}

// TestIndexSingleFileStillFailsOnAnUnparseableNote keeps `index --doc` loud: an
// editor saving one broken note must hear about it, unlike a whole-vault pass
// where one bad note is reported and stepped over.
func TestIndexSingleFileStillFailsOnAnUnparseableNote(t *testing.T) {
	root := t.TempDir()
	v, err := Init(root)
	if err != nil {
		t.Fatalf("init vault: %v", err)
	}
	defer v.Close()

	abs := writeNote(t, root, "broken.md", unparseableFrontmatter)
	err = IndexSingleFile(v, abs)
	if err == nil {
		t.Fatal("IndexSingleFile on an unparseable note returned nil; a single-file index must report the parse error")
	}
	if !strings.Contains(err.Error(), "unparseable note") {
		t.Errorf("error = %v, want it to name the note as unparseable", err)
	}
}
