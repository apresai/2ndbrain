package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Body-write contract tests.
//
// append / prepend / replace are the first commands that rewrite a note's body
// on disk. Each test drives the exact argv a user or the GUI would send through
// runCLIArgs, then reads the doc back through `read --json` (or off disk) to
// assert the body changed as intended and that frontmatter + comments survive.
//
// Provider is forced to "no-provider" so the inline re-embed in writeBody is
// skipped (no AWS creds in CI), matching the other contract tests. No mocks.

// noProviderContractVault is newContractVault with the AI provider pinned to a
// value with no registered embedder, so writeBody's embed step is a no-op.
func noProviderContractVault(t *testing.T) (string, func(path string) document_body) {
	t.Helper()
	v, root := newContractVault(t)
	v.Config.AI.Provider = "no-provider"
	if err := v.Config.Save(v.DotDir); err != nil {
		t.Fatalf("save config: %v", err)
	}
	readBody := func(path string) document_body {
		out, err := runCLIArgs(t, root, "read", path, "--json", "--porcelain")
		if err != nil {
			t.Fatalf("read %s: %v\n%s", path, err, out)
		}
		var doc document_body
		if err := json.Unmarshal(out, &doc); err != nil {
			t.Fatalf("read %s JSON: %v\n%s", path, err, out)
		}
		return doc
	}
	return root, readBody
}

type document_body struct {
	Path  string `json:"path"`
	Title string `json:"title"`
	Body  string `json:"body"`
}

func createContractNote(t *testing.T, root, title, body string) string {
	t.Helper()
	created, err := runCLIArgs(t, root, "create", title, "--json", "--porcelain")
	if err != nil {
		t.Fatalf("create %q: %v\n%s", title, err, created)
	}
	var doc struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(created, &doc); err != nil {
		t.Fatalf("create %q JSON: %v\n%s", title, err, created)
	}
	if body != "" {
		// Replace the templated body with a known body via `replace` so each
		// test starts from a deterministic state.
		if _, err := runCLIArgs(t, root, "replace", doc.Path, "--text", body); err != nil {
			t.Fatalf("seed body for %q: %v", title, err)
		}
	}
	return doc.Path
}

func TestContract_AppendAddsText(t *testing.T) {
	root, readBody := noProviderContractVault(t)
	path := createContractNote(t, root, "Append Note", "Original body line.")

	out, err := runCLIArgs(t, root, "append", path, "--text", "Appended sentence.", "--json", "--porcelain")
	if err != nil {
		t.Fatalf("append: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), `"operation": "append"`) {
		t.Fatalf("append result missing operation:\n%s", out)
	}

	doc := readBody(path)
	if !strings.Contains(doc.Body, "Original body line.") {
		t.Fatalf("append dropped original body:\n%s", doc.Body)
	}
	if !strings.Contains(doc.Body, "Appended sentence.") {
		t.Fatalf("appended text not present:\n%s", doc.Body)
	}
	// Append goes after the original content.
	if strings.Index(doc.Body, "Appended sentence.") < strings.Index(doc.Body, "Original body line.") {
		t.Fatalf("appended text should come after original:\n%s", doc.Body)
	}
}

func TestContract_AppendFromStdin(t *testing.T) {
	root, readBody := noProviderContractVault(t)
	path := createContractNote(t, root, "Stdin Note", "Seed.")

	// Feed content via stdin (no --text, no --file).
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	origStdin := os.Stdin
	os.Stdin = r
	if _, err := w.WriteString("From stdin pipe.\n"); err != nil {
		t.Fatalf("write stdin: %v", err)
	}
	w.Close()

	out, err := runCLIArgs(t, root, "append", path)
	os.Stdin = origStdin
	if err != nil {
		t.Fatalf("append stdin: %v\n%s", err, out)
	}

	doc := readBody(path)
	if !strings.Contains(doc.Body, "From stdin pipe.") {
		t.Fatalf("stdin content not appended:\n%s", doc.Body)
	}
}

func TestContract_PrependPutsTextBeforeBody(t *testing.T) {
	root, readBody := noProviderContractVault(t)
	path := createContractNote(t, root, "Prepend Note", "Existing body.")

	if _, err := runCLIArgs(t, root, "prepend", path, "--text", "Top line."); err != nil {
		t.Fatalf("prepend: %v", err)
	}

	doc := readBody(path)
	if !strings.Contains(doc.Body, "Top line.") || !strings.Contains(doc.Body, "Existing body.") {
		t.Fatalf("prepend lost content:\n%s", doc.Body)
	}
	if strings.Index(doc.Body, "Top line.") > strings.Index(doc.Body, "Existing body.") {
		t.Fatalf("prepended text should come before existing body:\n%s", doc.Body)
	}
}

func TestContract_ReplaceSectionSwapsOneSection(t *testing.T) {
	root, readBody := noProviderContractVault(t)
	seed := strings.Join([]string{
		"## Decision",
		"old decision",
		"",
		"## Consequences",
		"keep me",
	}, "\n")
	path := createContractNote(t, root, "Replace Section Note", seed)

	out, err := runCLIArgs(t, root, "replace", path, "--section", "Decision", "--text", "new decision", "--json", "--porcelain")
	if err != nil {
		t.Fatalf("replace --section: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), `"operation": "replace-section"`) {
		t.Fatalf("replace-section result missing operation:\n%s", out)
	}

	doc := readBody(path)
	if !strings.Contains(doc.Body, "new decision") {
		t.Fatalf("new section content missing:\n%s", doc.Body)
	}
	if strings.Contains(doc.Body, "old decision") {
		t.Fatalf("old section content not removed:\n%s", doc.Body)
	}
	// Sibling section untouched.
	if !strings.Contains(doc.Body, "## Consequences") || !strings.Contains(doc.Body, "keep me") {
		t.Fatalf("sibling section damaged:\n%s", doc.Body)
	}
}

func TestContract_ReplaceSectionMissingErrors(t *testing.T) {
	root, _ := noProviderContractVault(t)
	path := createContractNote(t, root, "No Section Note", "## Only\nbody")

	if _, err := runCLIArgs(t, root, "replace", path, "--section", "Ghost", "--text", "x"); err == nil {
		t.Fatalf("expected error replacing a missing section")
	}
}

func TestContract_ReplaceWholeBody(t *testing.T) {
	root, readBody := noProviderContractVault(t)
	path := createContractNote(t, root, "Whole Body Note", "old everything")

	if _, err := runCLIArgs(t, root, "replace", path, "--text", "brand new body"); err != nil {
		t.Fatalf("replace whole body: %v", err)
	}

	doc := readBody(path)
	if doc.Body != "" && !strings.Contains(doc.Body, "brand new body") {
		t.Fatalf("body not replaced:\n%s", doc.Body)
	}
	if strings.Contains(doc.Body, "old everything") {
		t.Fatalf("old body survived a whole-body replace:\n%s", doc.Body)
	}
}

// Regression: appending must leave the frontmatter and an Obsidian %% comment %%
// in the body intact. The comment is in the raw file but stripped from the
// indexed/search representation, so we read the file off disk directly.
func TestContract_AppendPreservesFrontmatterAndComment(t *testing.T) {
	root, _ := noProviderContractVault(t)
	path := createContractNote(t, root, "Comment Note", "visible body\n%% secret comment %%\nmore body")

	if _, err := runCLIArgs(t, root, "append", path, "--text", "tail"); err != nil {
		t.Fatalf("append: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(root, path))
	if err != nil {
		t.Fatalf("read raw file: %v", err)
	}
	text := string(raw)
	if !strings.HasPrefix(text, "---") {
		t.Fatalf("frontmatter delimiter missing:\n%s", text)
	}
	if !strings.Contains(text, "title: Comment Note") {
		t.Fatalf("frontmatter title lost:\n%s", text)
	}
	if !strings.Contains(text, "%% secret comment %%") {
		t.Fatalf("Obsidian comment lost from body:\n%s", text)
	}
	if !strings.Contains(text, "tail") {
		t.Fatalf("appended text missing:\n%s", text)
	}
}

// Appending a [[wikilink]] to a target that exists must show up in the body and,
// because writeBody reindexes + resolves links, become a resolved link.
func TestContract_AppendWikilinkResolves(t *testing.T) {
	v, root := newContractVault(t)
	v.Config.AI.Provider = "no-provider"
	if err := v.Config.Save(v.DotDir); err != nil {
		t.Fatalf("save config: %v", err)
	}

	// Create the link target first so resolution has something to point at.
	target := createContractNote(t, root, "Target", "I am the target.")
	source := createContractNote(t, root, "Source", "Source body.")

	if _, err := runCLIArgs(t, root, "append", source, "--text", "See [[Target]] for more."); err != nil {
		t.Fatalf("append wikilink: %v", err)
	}

	// Body shows the raw wikilink.
	out, err := runCLIArgs(t, root, "read", source, "--json", "--porcelain")
	if err != nil {
		t.Fatalf("read source: %v\n%s", err, out)
	}
	var doc document_body
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatalf("read JSON: %v\n%s", err, out)
	}
	if !strings.Contains(doc.Body, "[[Target]]") {
		t.Fatalf("wikilink not in body:\n%s", doc.Body)
	}

	// The link must resolve to a real doc: gate on target_id IS NOT NULL,
	// the canonical "linked to a real doc" test.
	var resolved int
	row := v.DB.Conn().QueryRow(`
		SELECT COUNT(*) FROM links
		WHERE resolved = 1 AND target_id IS NOT NULL
		  AND source_id = (SELECT id FROM documents WHERE path = ?)
	`, source)
	if err := row.Scan(&resolved); err != nil {
		t.Fatalf("count resolved links: %v", err)
	}
	if resolved < 1 {
		t.Fatalf("appended [[Target]] did not resolve to %s", target)
	}
}
