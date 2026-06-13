package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/apresai/2ndbrain/internal/document"
	"github.com/apresai/2ndbrain/internal/vault"
)

// tags rename contract tests.
//
// These exercise the full argv dispatch (runCLIArgs) over a real temp vault, no
// provider needed: tags rename is a frontmatter-only filesystem + index op. We
// seed notes with frontmatter tags directly on disk, index, then assert each
// affected frontmatter is rewritten and the tags table (via TagCounts) reflects
// the rename. No mocks.

// writeTaggedNote writes a markdown note with the given frontmatter tags to the
// vault root and returns its filename.
func writeTaggedNote(t *testing.T, root, name, title string, tags []string) string {
	t.Helper()
	var sb strings.Builder
	sb.WriteString("---\n")
	sb.WriteString("title: " + title + "\n")
	sb.WriteString("type: note\n")
	sb.WriteString("tags:\n")
	for _, tag := range tags {
		sb.WriteString("  - " + tag + "\n")
	}
	sb.WriteString("---\n\n# " + title + "\n\nBody of " + title + ".\n")
	if err := os.WriteFile(filepath.Join(root, name), []byte(sb.String()), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return name
}

// tagCountMap reads the live tag counts from the indexed vault.
func tagCountMap(t *testing.T, root string) map[string]int {
	t.Helper()
	v, err := vault.Open(root)
	if err != nil {
		t.Fatalf("open vault: %v", err)
	}
	defer v.Close()
	counts, err := v.DB.TagCounts()
	if err != nil {
		t.Fatalf("TagCounts: %v", err)
	}
	m := map[string]int{}
	for _, tc := range counts {
		m[tc.Tag] = tc.Count
	}
	return m
}

// docFrontmatterTags reads a note off disk and returns its frontmatter tags.
func docFrontmatterTags(t *testing.T, root, name string) []string {
	t.Helper()
	doc, err := document.ParseFile(filepath.Join(root, name))
	if err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	return doc.Tags
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

func TestTagsRename_RewritesAllAffectedDocs(t *testing.T) {
	_, root := newContractVault(t)
	writeTaggedNote(t, root, "n1.md", "Note One", []string{"draft", "infra"})
	writeTaggedNote(t, root, "n2.md", "Note Two", []string{"draft"})
	writeTaggedNote(t, root, "n3.md", "Note Three", []string{"draft", "design"})
	if _, err := runCLIArgs(t, root, "index"); err != nil {
		t.Fatalf("index: %v", err)
	}

	// Sanity: three docs carry "draft" before rename.
	before := tagCountMap(t, root)
	if before["draft"] != 3 {
		t.Fatalf("pre-rename draft count = %d, want 3: %+v", before["draft"], before)
	}

	if _, err := runCLIArgs(t, root, "tags", "rename", "draft", "in-progress"); err != nil {
		t.Fatalf("tags rename: %v", err)
	}

	// All three frontmatters now carry "in-progress", none carry "draft".
	for _, n := range []string{"n1.md", "n2.md", "n3.md"} {
		tags := docFrontmatterTags(t, root, n)
		if !contains(tags, "in-progress") {
			t.Errorf("%s missing renamed tag in-progress: %v", n, tags)
		}
		if contains(tags, "draft") {
			t.Errorf("%s still carries old tag draft: %v", n, tags)
		}
	}
	// Untouched tags survive.
	if !contains(docFrontmatterTags(t, root, "n1.md"), "infra") {
		t.Errorf("n1.md lost its infra tag")
	}

	// Tag counts reflect the rename: draft gone, in-progress at 3.
	after := tagCountMap(t, root)
	if _, ok := after["draft"]; ok {
		t.Errorf("draft tag still present after rename: %+v", after)
	}
	if after["in-progress"] != 3 {
		t.Errorf("in-progress count = %d, want 3: %+v", after["in-progress"], after)
	}
	if after["design"] != 1 || after["infra"] != 1 {
		t.Errorf("untouched tag counts changed: %+v", after)
	}
}

func TestTagsRename_DryRunWritesNothing(t *testing.T) {
	_, root := newContractVault(t)
	writeTaggedNote(t, root, "n1.md", "Note One", []string{"draft", "infra"})
	writeTaggedNote(t, root, "n2.md", "Note Two", []string{"draft"})
	if _, err := runCLIArgs(t, root, "index"); err != nil {
		t.Fatalf("index: %v", err)
	}

	out, err := runCLIArgs(t, root, "tags", "rename", "draft", "in-progress", "--dry-run", "--json", "--porcelain")
	if err != nil {
		t.Fatalf("tags rename --dry-run: %v\n%s", err, out)
	}

	var res struct {
		DryRun  bool     `json:"dry_run"`
		Renamed []string `json:"renamed"`
	}
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatalf("unmarshal dry-run result: %v\n%s", err, out)
	}
	if !res.DryRun {
		t.Errorf("dry_run flag not echoed in result")
	}
	if len(res.Renamed) != 2 {
		t.Errorf("dry-run reported %d affected, want 2: %+v", len(res.Renamed), res.Renamed)
	}

	// Nothing on disk changed: both notes still carry "draft", none "in-progress".
	for _, n := range []string{"n1.md", "n2.md"} {
		tags := docFrontmatterTags(t, root, n)
		if !contains(tags, "draft") {
			t.Errorf("%s lost draft under --dry-run: %v", n, tags)
		}
		if contains(tags, "in-progress") {
			t.Errorf("%s gained in-progress under --dry-run (should write nothing): %v", n, tags)
		}
	}
	// Index unchanged too.
	if tagCountMap(t, root)["draft"] != 2 {
		t.Errorf("--dry-run changed the index")
	}
}

func TestTagsRename_DedupesWhenNewTagAlreadyPresent(t *testing.T) {
	_, root := newContractVault(t)
	// n1 already has BOTH old and new; rename must collapse to a single new.
	writeTaggedNote(t, root, "n1.md", "Note One", []string{"draft", "in-progress", "infra"})
	if _, err := runCLIArgs(t, root, "index"); err != nil {
		t.Fatalf("index: %v", err)
	}

	if _, err := runCLIArgs(t, root, "tags", "rename", "draft", "in-progress"); err != nil {
		t.Fatalf("tags rename: %v", err)
	}

	tags := docFrontmatterTags(t, root, "n1.md")
	// Exactly one "in-progress", "draft" gone, "infra" preserved.
	count := 0
	for _, tag := range tags {
		if tag == "in-progress" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected exactly one in-progress after dedupe, got %d: %v", count, tags)
	}
	if contains(tags, "draft") {
		t.Errorf("draft survived rename: %v", tags)
	}
	if !contains(tags, "infra") {
		t.Errorf("infra dropped during dedupe rename: %v", tags)
	}

	// Index agrees: in-progress on one doc, no draft.
	after := tagCountMap(t, root)
	if after["in-progress"] != 1 {
		t.Errorf("in-progress count = %d, want 1: %+v", after["in-progress"], after)
	}
	if _, ok := after["draft"]; ok {
		t.Errorf("draft still in index: %+v", after)
	}
}
