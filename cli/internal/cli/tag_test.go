package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// `tag add` / `tag remove` contract tests. Full argv dispatch (runCLIArgs) over a
// real temp vault, no provider needed. Reuses writeTaggedNote / docFrontmatterTags
// / tagCountMap / contains / newContractVault / runCLIArgs from tags_rename_test.go.

func TestTagAdd_MakesTagsSearchable(t *testing.T) {
	_, root := newContractVault(t)
	writeTaggedNote(t, root, "n.md", "Note", []string{"seed"})
	if _, err := runCLIArgs(t, root, "index"); err != nil {
		t.Fatalf("index: %v", err)
	}

	if _, err := runCLIArgs(t, root, "tag", "add", "n.md", "alpha", "beta"); err != nil {
		t.Fatalf("tag add: %v", err)
	}

	// Searchable with no manual reindex (tag add reindexes).
	out, err := runCLIArgs(t, root, "list", "--tag", "alpha", "--json")
	if err != nil {
		t.Fatalf("list --tag: %v", err)
	}
	if !strings.Contains(string(out), "Note") {
		t.Errorf("list --tag alpha did not find the note:\n%s", out)
	}

	// Frontmatter carries seed + both new tags; tags table reflects them.
	tags := docFrontmatterTags(t, root, "n.md")
	for _, want := range []string{"seed", "alpha", "beta"} {
		if !contains(tags, want) {
			t.Errorf("expected tag %q in %v", want, tags)
		}
	}
	counts := tagCountMap(t, root)
	if counts["alpha"] != 1 || counts["beta"] != 1 {
		t.Errorf("tag counts after add = %+v, want alpha=1 beta=1", counts)
	}
}

func TestTagAdd_DedupesAndPreservesOrder(t *testing.T) {
	_, root := newContractVault(t)
	writeTaggedNote(t, root, "n.md", "Note", []string{"a", "b"})
	if _, err := runCLIArgs(t, root, "index"); err != nil {
		t.Fatalf("index: %v", err)
	}

	// Add b (already present) + c: result must be [a, b, c] (no dup, order kept).
	if _, err := runCLIArgs(t, root, "tag", "add", "n.md", "b", "c"); err != nil {
		t.Fatalf("tag add: %v", err)
	}
	tags := docFrontmatterTags(t, root, "n.md")
	want := []string{"a", "b", "c"}
	if len(tags) != len(want) {
		t.Fatalf("tags = %v, want %v", tags, want)
	}
	for i := range want {
		if tags[i] != want[i] {
			t.Errorf("tags = %v, want %v", tags, want)
			break
		}
	}
}

func TestTagAdd_CommaSplit(t *testing.T) {
	_, root := newContractVault(t)
	writeTaggedNote(t, root, "n.md", "Note", []string{"seed"})
	if _, err := runCLIArgs(t, root, "index"); err != nil {
		t.Fatalf("index: %v", err)
	}
	// A single comma-separated argument is split into multiple tags.
	if _, err := runCLIArgs(t, root, "tag", "add", "n.md", "x,y"); err != nil {
		t.Fatalf("tag add: %v", err)
	}
	tags := docFrontmatterTags(t, root, "n.md")
	if !contains(tags, "x") || !contains(tags, "y") || contains(tags, "x,y") {
		t.Errorf("comma-separated arg not split into x,y: %v", tags)
	}
}

func TestTagRemove_DropsOnlyNamed(t *testing.T) {
	_, root := newContractVault(t)
	writeTaggedNote(t, root, "n.md", "Note", []string{"a", "b", "c"})
	if _, err := runCLIArgs(t, root, "index"); err != nil {
		t.Fatalf("index: %v", err)
	}

	if _, err := runCLIArgs(t, root, "tag", "remove", "n.md", "b"); err != nil {
		t.Fatalf("tag remove: %v", err)
	}
	tags := docFrontmatterTags(t, root, "n.md")
	if contains(tags, "b") {
		t.Errorf("b should be removed: %v", tags)
	}
	if !contains(tags, "a") || !contains(tags, "c") {
		t.Errorf("a and c should remain: %v", tags)
	}
	// No longer findable by the removed tag, still by the kept ones.
	if out, _ := runCLIArgs(t, root, "list", "--tag", "b", "--json"); strings.Contains(string(out), "Note") {
		t.Errorf("removed tag b still finds the note:\n%s", out)
	}
}

func TestTagRemove_AbsentIsNoOp(t *testing.T) {
	_, root := newContractVault(t)
	writeTaggedNote(t, root, "n.md", "Note", []string{"a", "b"})
	if _, err := runCLIArgs(t, root, "index"); err != nil {
		t.Fatalf("index: %v", err)
	}
	// Removing a tag the note doesn't have is a no-op and still exits 0.
	if _, err := runCLIArgs(t, root, "tag", "remove", "n.md", "nope"); err != nil {
		t.Fatalf("tag remove (absent): %v", err)
	}
	tags := docFrontmatterTags(t, root, "n.md")
	if len(tags) != 2 || !contains(tags, "a") || !contains(tags, "b") {
		t.Errorf("no-op remove changed tags: %v", tags)
	}
}

func TestTagAdd_ResolvesByTitle(t *testing.T) {
	_, root := newContractVault(t)
	writeTaggedNote(t, root, "alpha-note.md", "Alpha Note", []string{"seed"})
	if _, err := runCLIArgs(t, root, "index"); err != nil {
		t.Fatalf("index: %v", err)
	}
	// A bare positional that is not an on-disk path resolves via the auto resolver
	// (title -> alpha-note.md).
	if _, err := runCLIArgs(t, root, "tag", "add", "Alpha Note", "bytitle"); err != nil {
		t.Fatalf("tag add by title: %v", err)
	}
	if tags := docFrontmatterTags(t, root, "alpha-note.md"); !contains(tags, "bytitle") {
		t.Errorf("tag not added via title resolution: %v", tags)
	}
}

func TestRemoveTagsList_DedupesKeptTags(t *testing.T) {
	// A note that already carried duplicate tags must come out deduped after a
	// remove (symmetric with mergeTagsList), not just have the named tag dropped.
	got := removeTagsList([]string{"a", "b", "a", "c", "b"}, []string{"c"})
	want := []string{"a", "b"}
	if !sameStringSlice(got, want) {
		t.Errorf("removeTagsList dedupe = %v, want %v", got, want)
	}
}

func TestRemoveTagsList_PreservesOrderAndDropsNamed(t *testing.T) {
	got := removeTagsList([]string{"x", "y", "z"}, []string{"y"})
	want := []string{"x", "z"}
	if !sameStringSlice(got, want) {
		t.Errorf("removeTagsList = %v, want %v", got, want)
	}
}

func TestTag_ReadOnlyRejected(t *testing.T) {
	_, root := newContractVault(t)
	if err := os.WriteFile(filepath.Join(root, "board.canvas"), []byte(`{"nodes":[],"edges":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runCLIArgs(t, root, "index"); err != nil {
		t.Fatalf("index: %v", err)
	}
	_, err := runCLIArgs(t, root, "tag", "add", "board.canvas", "x")
	if ExitCode(err) != ExitValidation {
		t.Fatalf("tag add on a .canvas: want exit %d, got %d (err=%v)", ExitValidation, ExitCode(err), err)
	}
}
