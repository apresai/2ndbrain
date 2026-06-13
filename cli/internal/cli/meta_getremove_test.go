package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// exitCode extracts the CLI exit code an error carries. Handlers return
// *ExitError for the loud, scriptable failures (ExitNotFound, ExitValidation);
// any other non-nil error is treated as a generic failure (code 1).
func exitCode(t *testing.T, err error) int {
	t.Helper()
	if err == nil {
		return ExitOK
	}
	var ee *ExitError
	if errors.As(err, &ee) {
		return ee.Code
	}
	return ExitNotFound
}

// writeVaultDoc drops a markdown file at a vault-relative path so a test can
// control the exact on-disk frontmatter (comments, key order) before exercising
// meta. The doc is not indexed; meta --get reads from disk, and meta --remove
// re-indexes via UpsertDocument on its own.
func writeVaultDoc(t *testing.T, vaultRoot, rel, content string) {
	t.Helper()
	abs := filepath.Join(vaultRoot, rel)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", rel, err)
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

// TestMetaGetRemove_RoundTrip walks the full set/get/remove/get cycle a script
// or the GUI would drive: set a custom key, read it back, remove it, confirm a
// follow-up get reports ExitNotFound.
func TestMetaGetRemove_RoundTrip(t *testing.T) {
	_, root := newContractVault(t)

	rel := "round-trip.md"
	writeVaultDoc(t, root, rel, "---\nid: rt-1\ntitle: Round Trip\ntype: note\nstatus: draft\n---\nbody\n")

	// set a non-schema key.
	if _, err := runCLIArgs(t, root, "meta", rel, "--set", "foo=bar"); err != nil {
		t.Fatalf("meta --set foo=bar: %v", err)
	}

	// get returns the value (porcelain JSON so the scalar is unambiguous).
	got, err := runCLIArgs(t, root, "meta", rel, "--get", "foo", "--json", "--porcelain")
	if err != nil {
		t.Fatalf("meta --get foo: %v", err)
	}
	if !strings.Contains(string(got), "bar") {
		t.Fatalf("meta --get foo: expected value bar, got %q", got)
	}

	// remove it.
	if _, err := runCLIArgs(t, root, "meta", rel, "--remove", "foo"); err != nil {
		t.Fatalf("meta --remove foo: %v", err)
	}

	// get now reports not-found.
	_, err = runCLIArgs(t, root, "meta", rel, "--get", "foo")
	if code := exitCode(t, err); code != ExitNotFound {
		t.Fatalf("meta --get foo after remove: expected ExitNotFound (%d), got %d (err=%v)", ExitNotFound, code, err)
	}
}

// TestMetaGet_PlainAndArray covers the default (non-JSON) get rendering:
// scalars print on one line, arrays print one item per line.
func TestMetaGet_PlainAndArray(t *testing.T) {
	_, root := newContractVault(t)

	rel := "plain.md"
	writeVaultDoc(t, root, rel, "---\nid: p-1\ntitle: Plain\ntype: note\ntags:\n  - alpha\n  - beta\n---\nbody\n")

	got, err := runCLIArgs(t, root, "meta", rel, "--get", "title")
	if err != nil {
		t.Fatalf("meta --get title: %v", err)
	}
	if strings.TrimSpace(string(got)) != "Plain" {
		t.Fatalf("meta --get title: expected 'Plain', got %q", got)
	}

	got, err = runCLIArgs(t, root, "meta", rel, "--get", "tags")
	if err != nil {
		t.Fatalf("meta --get tags: %v", err)
	}
	body := string(got)
	if !strings.Contains(body, "alpha") || !strings.Contains(body, "beta") {
		t.Fatalf("meta --get tags: expected alpha and beta, got %q", got)
	}
}

// TestMetaRemove_RejectsIdentityKeys asserts the protected identity keys cannot
// be removed: doing so would orphan the doc (id), strip its label (title), or
// break schema/template selection (type). Each returns ExitValidation.
func TestMetaRemove_RejectsIdentityKeys(t *testing.T) {
	_, root := newContractVault(t)

	rel := "identity.md"
	const original = "---\nid: id-1\ntitle: Identity\ntype: note\nstatus: draft\n---\nbody\n"
	writeVaultDoc(t, root, rel, original)

	for _, key := range []string{"id", "title", "type", "path"} {
		_, err := runCLIArgs(t, root, "meta", rel, "--remove", key)
		if code := exitCode(t, err); code != ExitValidation {
			t.Fatalf("meta --remove %s: expected ExitValidation (%d), got %d (err=%v)", key, ExitValidation, code, err)
		}
	}

	// The file must be untouched after the rejected removals.
	after, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(after) != original {
		t.Fatalf("rejected removals must not modify the file.\nwant:\n%s\ngot:\n%s", original, after)
	}
}

// TestMetaRemove_RejectsSchemaRequired asserts a schema-required field cannot be
// removed. For type adr the schema requires status, so removing it is refused.
func TestMetaRemove_RejectsSchemaRequired(t *testing.T) {
	_, root := newContractVault(t)

	rel := "adr.md"
	writeVaultDoc(t, root, rel, "---\nid: adr-1\ntitle: A Decision\ntype: adr\nstatus: proposed\n---\nbody\n")

	_, err := runCLIArgs(t, root, "meta", rel, "--remove", "status")
	if code := exitCode(t, err); code != ExitValidation {
		t.Fatalf("meta --remove status (adr): expected ExitValidation (%d), got %d (err=%v)", ExitValidation, code, err)
	}
}

// TestMetaRemove_PreservesCommentsAndOrder is the golden-ish guard: removing one
// key must leave the surrounding YAML comments and the order of every untouched
// key intact, because doc.Serialize routes through UpdateDocumentFrontmatterAST.
func TestMetaRemove_PreservesCommentsAndOrder(t *testing.T) {
	_, root := newContractVault(t)

	rel := "commented.md"
	// A comment above 'keep', a custom 'drop' key after it, and a trailing
	// custom key so order is observable.
	original := strings.Join([]string{
		"---",
		"id: c-1",
		"title: Commented",
		"type: note",
		"# this comment documents the keep field",
		"keep: yes-please",
		"drop: remove-me",
		"zeta: last-key",
		"---",
		"body text",
		"",
	}, "\n")
	writeVaultDoc(t, root, rel, original)

	if _, err := runCLIArgs(t, root, "meta", rel, "--remove", "drop"); err != nil {
		t.Fatalf("meta --remove drop: %v", err)
	}

	out, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	s := string(out)

	if strings.Contains(s, "drop:") {
		t.Fatalf("removed key still present:\n%s", s)
	}
	if !strings.Contains(s, "# this comment documents the keep field") {
		t.Fatalf("comment on a sibling key was lost:\n%s", s)
	}
	if !strings.Contains(s, "keep: yes-please") {
		t.Fatalf("sibling key 'keep' was lost:\n%s", s)
	}
	if !strings.Contains(s, "zeta: last-key") {
		t.Fatalf("sibling key 'zeta' was lost:\n%s", s)
	}
	// Order: keep must still precede zeta, and the comment must still precede keep.
	keepIdx := strings.Index(s, "keep:")
	zetaIdx := strings.Index(s, "zeta:")
	commentIdx := strings.Index(s, "# this comment")
	if commentIdx < 0 || keepIdx < 0 || zetaIdx < 0 || !(commentIdx < keepIdx && keepIdx < zetaIdx) {
		t.Fatalf("key order / comment placement not preserved (comment=%d keep=%d zeta=%d):\n%s",
			commentIdx, keepIdx, zetaIdx, s)
	}
}

// TestMetaGet_RejectsCombinedWithWrite guards the documented mutual exclusion:
// --get is read-only, so combining it with --set or --remove is ExitValidation.
func TestMetaGet_RejectsCombinedWithWrite(t *testing.T) {
	_, root := newContractVault(t)

	rel := "combo.md"
	writeVaultDoc(t, root, rel, "---\nid: combo-1\ntitle: Combo\ntype: note\n---\nbody\n")

	_, err := runCLIArgs(t, root, "meta", rel, "--get", "title", "--set", "foo=bar")
	if code := exitCode(t, err); code != ExitValidation {
		t.Fatalf("meta --get --set: expected ExitValidation (%d), got %d (err=%v)", ExitValidation, code, err)
	}

	_, err = runCLIArgs(t, root, "meta", rel, "--get", "title", "--remove", "foo")
	if code := exitCode(t, err); code != ExitValidation {
		t.Fatalf("meta --get --remove: expected ExitValidation (%d), got %d (err=%v)", ExitValidation, code, err)
	}
}

// TestMetaRemove_MissingKeyNotFound: removing an absent key reports ExitNotFound
// so a script can tell "nothing to do" from "removed".
func TestMetaRemove_MissingKeyNotFound(t *testing.T) {
	_, root := newContractVault(t)

	rel := "missing.md"
	writeVaultDoc(t, root, rel, "---\nid: m-1\ntitle: Missing\ntype: note\n---\nbody\n")

	_, err := runCLIArgs(t, root, "meta", rel, "--remove", "nope")
	if code := exitCode(t, err); code != ExitNotFound {
		t.Fatalf("meta --remove nope: expected ExitNotFound (%d), got %d (err=%v)", ExitNotFound, code, err)
	}
}
