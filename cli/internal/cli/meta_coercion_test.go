package cli

import (
	"os"
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

// statusAsListSchema declares a doc type whose `status` field is (pathologically)
// typed as a list, while still carrying a status state machine. Without the
// `key != "status"` guard in updateMeta, the IsListField branch would coerce
// status into an array and `continue`, skipping transition validation entirely.
const statusAsListSchema = `types:
  widget:
    name: Widget
    description: status declared as a list, but still a state machine
    fields:
      status:
        type: list
    required:
      - title
      - status
    status:
      initial: proposed
      transitions:
        proposed:
          - accepted
        accepted: []
        archived: []
`

func writeWidgetNote(t *testing.T, root, name, status string) {
	t.Helper()
	body := "---\ntitle: " + name + "\ntype: widget\nstatus: " + status + "\n---\n\n# " + name + "\n\nBody.\n"
	if err := os.WriteFile(filepath.Join(root, name+".md"), []byte(body), 0o644); err != nil {
		t.Fatalf("write widget note: %v", err)
	}
}

func TestMetaSet_StatusListSchemaStillValidatesTransition(t *testing.T) {
	_, root := newContractVault(t)
	// Overwrite the default schemas with one that types `status` as a list.
	if err := os.WriteFile(filepath.Join(root, ".2ndbrain", "schemas.yaml"), []byte(statusAsListSchema), 0o644); err != nil {
		t.Fatalf("write schemas: %v", err)
	}
	writeWidgetNote(t, root, "w", "proposed")
	if _, err := runCLIArgs(t, root, "index"); err != nil {
		t.Fatalf("index: %v", err)
	}

	// proposed -> archived is NOT an allowed transition (proposed allows only
	// accepted). The guard must route status through transition validation even
	// though the field is declared `type: list`, so this is rejected.
	_, err := runCLIArgs(t, root, "meta", "w.md", "--set", "status=archived")
	if ExitCode(err) != ExitValidation {
		t.Fatalf("invalid status transition not rejected: want exit %d, got %d (err=%v)", ExitValidation, ExitCode(err), err)
	}

	// The note's status must be unchanged (still scalar "proposed"), not coerced
	// to a list or advanced to archived.
	doc, perr := document.ParseFile(filepath.Join(root, "w.md"))
	if perr != nil {
		t.Fatalf("parse: %v", perr)
	}
	if got, _ := doc.Frontmatter["status"].(string); got != "proposed" {
		t.Errorf("status changed despite rejected transition: %#v", doc.Frontmatter["status"])
	}
}

func TestMetaSet_StatusListSchemaAllowsValidTransition(t *testing.T) {
	_, root := newContractVault(t)
	if err := os.WriteFile(filepath.Join(root, ".2ndbrain", "schemas.yaml"), []byte(statusAsListSchema), 0o644); err != nil {
		t.Fatalf("write schemas: %v", err)
	}
	writeWidgetNote(t, root, "w", "proposed")
	if _, err := runCLIArgs(t, root, "index"); err != nil {
		t.Fatalf("index: %v", err)
	}

	// proposed -> accepted IS allowed: the guard must not block a legitimate
	// status set, and status stays a scalar (not coerced to a list).
	if _, err := runCLIArgs(t, root, "meta", "w.md", "--set", "status=accepted"); err != nil {
		t.Fatalf("valid status transition rejected: %v", err)
	}
	doc, err := document.ParseFile(filepath.Join(root, "w.md"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got, ok := doc.Frontmatter["status"].(string); !ok || got != "accepted" {
		t.Errorf("status not set to scalar 'accepted': %#v", doc.Frontmatter["status"])
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
