package cli

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/apresai/2ndbrain/internal/document"
)

// meta --set array-coercion contract tests. The pre-fix bug: `--set tags=a,b`
// stored a scalar string "a,b" (one literal tag), so `list --tag a` found
// nothing. The fix coerces array-typed fields (tags/aliases, or any schema
// "list"/"tags" field) to a YAML list with replace semantics.

func TestMetaSet_MultiTagCoercedToArray(t *testing.T) {
	_, root := newContractVault(t)
	writeTaggedNote(t, root, "n.md", "Note", []string{"seed"})
	if _, err := runCLIArgs(t, root, "index"); err != nil {
		t.Fatalf("index: %v", err)
	}

	if _, err := runCLIArgs(t, root, "meta", "n.md", "--set", "tags=alpha,beta"); err != nil {
		t.Fatalf("meta --set: %v", err)
	}

	// alpha and beta are SEPARATE searchable tags (pre-fix: one tag "alpha,beta").
	counts := tagCountMap(t, root)
	if counts["alpha"] != 1 || counts["beta"] != 1 {
		t.Errorf("tag counts = %+v, want alpha=1 beta=1 (multi-tag not split)", counts)
	}
	if counts["alpha,beta"] != 0 {
		t.Errorf("found a literal 'alpha,beta' tag (value was not coerced to an array): %+v", counts)
	}

	// Stored as a 2-element list (extractTags on a scalar would yield len 1), and
	// --set has replace semantics, so the seed tag is gone.
	tags := docFrontmatterTags(t, root, "n.md")
	if len(tags) != 2 || !contains(tags, "alpha") || !contains(tags, "beta") {
		t.Errorf("frontmatter tags = %v, want [alpha beta]", tags)
	}
	if contains(tags, "seed") {
		t.Errorf("--set should replace; seed tag still present: %v", tags)
	}
}

func TestMetaSet_ClearsTags(t *testing.T) {
	_, root := newContractVault(t)
	writeTaggedNote(t, root, "n.md", "Note", []string{"a", "b"})
	if _, err := runCLIArgs(t, root, "index"); err != nil {
		t.Fatalf("index: %v", err)
	}

	if _, err := runCLIArgs(t, root, "meta", "n.md", "--set", "tags="); err != nil {
		t.Fatalf("meta --set tags=: %v", err)
	}
	if tags := docFrontmatterTags(t, root, "n.md"); len(tags) != 0 {
		t.Errorf("expected cleared tags, got %v", tags)
	}
	if c := tagCountMap(t, root); c["a"] != 0 || c["b"] != 0 {
		t.Errorf("tags still indexed after clear: %+v", c)
	}
}

func TestMetaSet_ScalarFieldUnaffected(t *testing.T) {
	_, root := newContractVault(t)
	writeTaggedNote(t, root, "n.md", "Note", []string{"x"})
	if _, err := runCLIArgs(t, root, "index"); err != nil {
		t.Fatalf("index: %v", err)
	}
	if _, err := runCLIArgs(t, root, "meta", "n.md", "--set", "status=complete"); err != nil {
		t.Fatalf("meta --set status: %v", err)
	}
	out, err := runCLIArgs(t, root, "meta", "n.md", "--get", "status")
	if err != nil {
		t.Fatalf("meta --get status: %v", err)
	}
	if !strings.Contains(string(out), "complete") {
		t.Errorf("scalar status not set to complete: %s", out)
	}
}

func TestMetaSet_ListSchemaFieldCoerced(t *testing.T) {
	_, root := newContractVault(t)
	// adr declares a "deciders" field of type list.
	if _, err := runCLIArgs(t, root, "create", "--type", "adr", "My ADR"); err != nil {
		t.Fatalf("create adr: %v", err)
	}
	if _, err := runCLIArgs(t, root, "meta", "my-adr.md", "--set", "deciders=alice,bob"); err != nil {
		t.Fatalf("meta --set deciders: %v", err)
	}
	doc, err := document.ParseFile(filepath.Join(root, "my-adr.md"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	dec, ok := doc.Frontmatter["deciders"].([]any)
	if !ok || len(dec) != 2 {
		t.Fatalf("deciders not a 2-element list: %#v", doc.Frontmatter["deciders"])
	}
}
