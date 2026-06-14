package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Obsidian-CLI compatibility contract tests.
//
// These exercise the FULL pipeline a terminal user hits: the obsidian-syntax
// argv shim (preprocessArgs) feeding cobra dispatch (runCLIArgs). runObsidian
// translates the obsidian-style argv exactly as Execute() does, then runs it.

// runObsidian runs an obsidian-style invocation end-to-end: it applies the argv
// shim (preprocessArgs) and dispatches the result through cobra, pinned to the
// test vault. argv excludes the "2nb" prefix and the --vault flag.
func runObsidian(t *testing.T, vaultRoot string, argv ...string) ([]byte, error) {
	t.Helper()
	full := append([]string{"2nb"}, argv...)
	translated := preprocessArgs(full)
	return runCLIArgs(t, vaultRoot, translated[1:]...)
}

// writeCompatNote writes a note with frontmatter directly into the vault (bypassing
// create), so tests can control id/title/aliases/tags precisely. Caller indexes.
func writeCompatNote(t *testing.T, vaultRoot, relPath, id, title string, opts ...func(*[]string)) {
	t.Helper()
	abs := filepath.Join(vaultRoot, relPath)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	lines := []string{"---", "id: " + id, "title: " + title, "type: note", "status: draft"}
	for _, o := range opts {
		o(&lines)
	}
	lines = append(lines, "---", "", "Body of "+title+".")
	if err := os.WriteFile(abs, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatalf("write %s: %v", relPath, err)
	}
}

func withAliases(aliases ...string) func(*[]string) {
	return func(lines *[]string) {
		*lines = append(*lines, "aliases: ["+strings.Join(aliases, ", ")+"]")
	}
}

func withTags(tags ...string) func(*[]string) {
	return func(lines *[]string) {
		*lines = append(*lines, "tags: ["+strings.Join(tags, ", ")+"]")
	}
}

// seedCompatVault writes a small fixed set of notes and indexes them.
func seedCompatVault(t *testing.T, vaultRoot string) {
	t.Helper()
	writeCompatNote(t, vaultRoot, "projects/alpha.md", "11111111-1111-1111-1111-111111111111", "Alpha Note", withAliases("AKA-Alpha"))
	writeCompatNote(t, vaultRoot, "areas/beta.md", "22222222-2222-2222-2222-222222222222", "Beta Note", withTags("research"))
	writeCompatNote(t, vaultRoot, "projects/dup.md", "33333333-3333-3333-3333-333333333333", "Dup One")
	writeCompatNote(t, vaultRoot, "areas/dup.md", "44444444-4444-4444-4444-444444444444", "Dup Two")
	if _, err := runCLIArgs(t, vaultRoot, "index"); err != nil {
		t.Fatalf("index: %v", err)
	}
}

func TestObsidianCompat_ResolveByTitleAndAlias(t *testing.T) {
	_, root := newContractVault(t)
	seedCompatVault(t, root)

	// file= resolves by title.
	out, err := runObsidian(t, root, "read", "file=Alpha Note", "format=raw")
	if err != nil {
		t.Fatalf("read by title: %v", err)
	}
	if !strings.Contains(string(out), "Body of Alpha Note.") {
		t.Errorf("read by title missing body, got:\n%s", out)
	}

	// file= resolves by alias.
	out, err = runObsidian(t, root, "read", "file=AKA-Alpha", "format=raw")
	if err != nil {
		t.Fatalf("read by alias: %v", err)
	}
	if !strings.Contains(string(out), "Body of Alpha Note.") {
		t.Errorf("read by alias missing body, got:\n%s", out)
	}

	// file= resolves by basename suffix.
	out, err = runObsidian(t, root, "read", "file=beta", "format=raw")
	if err != nil {
		t.Fatalf("read by basename: %v", err)
	}
	if !strings.Contains(string(out), "Body of Beta Note.") {
		t.Errorf("read by basename missing body, got:\n%s", out)
	}
}

func TestObsidianCompat_BarePositionalFallback(t *testing.T) {
	_, root := newContractVault(t)
	seedCompatVault(t, root)

	// A bare positional that is not an on-disk path falls back to the resolver.
	out, err := runObsidian(t, root, "read", "Alpha Note", "format=raw")
	if err != nil {
		t.Fatalf("bare read fallback: %v", err)
	}
	if !strings.Contains(string(out), "Body of Alpha Note.") {
		t.Errorf("bare fallback missing body, got:\n%s", out)
	}
}

func TestObsidianCompat_AmbiguousResolutionFailsLoudly(t *testing.T) {
	_, root := newContractVault(t)
	seedCompatVault(t, root)

	out, err := runObsidian(t, root, "read", "file=dup")
	if ExitCode(err) != ExitValidation {
		t.Fatalf("ambiguous read: want exit %d, got %d (err=%v)", ExitValidation, ExitCode(err), err)
	}
	msg := err.Error()
	if !strings.Contains(msg, "areas/dup.md") || !strings.Contains(msg, "projects/dup.md") {
		t.Errorf("ambiguous error should list both candidates, got: %q (out=%s)", msg, out)
	}
}

func TestObsidianCompat_AmbiguousWriteRefused(t *testing.T) {
	_, root := newContractVault(t)
	seedCompatVault(t, root)

	// A write target (append) that is ambiguous must be refused with no mutation.
	before, _ := os.ReadFile(filepath.Join(root, "projects/dup.md"))
	_, err := runObsidian(t, root, "append", "file=dup", "content=SHOULD NOT WRITE")
	if ExitCode(err) != ExitValidation {
		t.Fatalf("ambiguous append: want exit %d, got %d (err=%v)", ExitValidation, ExitCode(err), err)
	}
	after, _ := os.ReadFile(filepath.Join(root, "projects/dup.md"))
	if string(before) != string(after) {
		t.Errorf("ambiguous append mutated a file")
	}
	if strings.Contains(string(after), "SHOULD NOT WRITE") {
		t.Errorf("ambiguous append wrote content")
	}
}

func TestObsidianCompat_PathStrictExact(t *testing.T) {
	_, root := newContractVault(t)
	seedCompatVault(t, root)

	// path= is strict-exact: a title is NOT a path, so it must not resolve.
	_, err := runObsidian(t, root, "read", "path=Alpha Note")
	if ExitCode(err) != ExitNotFound {
		t.Fatalf("path= strict: want exit %d, got %d (err=%v)", ExitNotFound, ExitCode(err), err)
	}
}

func TestObsidianCompat_ListingModes(t *testing.T) {
	_, root := newContractVault(t)
	seedCompatVault(t, root)

	// files total -> count.
	out, err := runObsidian(t, root, "files", "total")
	if err != nil {
		t.Fatalf("files total: %v", err)
	}
	if strings.TrimSpace(string(out)) != "4" {
		t.Errorf("files total = %q, want 4", strings.TrimSpace(string(out)))
	}

	// files format=paths -> one path per line.
	out, err = runObsidian(t, root, "files", "format=paths")
	if err != nil {
		t.Fatalf("files paths: %v", err)
	}
	for _, want := range []string{"projects/alpha.md", "areas/beta.md", "projects/dup.md"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("files paths missing %q, got:\n%s", want, out)
		}
	}

	// files format=tree -> indented hierarchy with directory headers.
	out, err = runObsidian(t, root, "files", "format=tree")
	if err != nil {
		t.Fatalf("files tree: %v", err)
	}
	tree := string(out)
	if !strings.Contains(tree, "projects\n") || !strings.Contains(tree, "  alpha.md") {
		t.Errorf("files tree missing expected structure, got:\n%s", tree)
	}
}

func TestObsidianCompat_Aliases(t *testing.T) {
	_, root := newContractVault(t)
	seedCompatVault(t, root)

	// print -> read
	if out, err := runObsidian(t, root, "print", "file=Alpha Note", "format=raw"); err != nil || !strings.Contains(string(out), "Body of Alpha Note.") {
		t.Errorf("print alias failed: err=%v out=%s", err, out)
	}
	// fm -> meta (frontmatter view, JSON)
	out, err := runObsidian(t, root, "fm", "file=Alpha Note", "format=json")
	if err != nil {
		t.Fatalf("fm alias: %v", err)
	}
	if !strings.Contains(string(out), "Alpha Note") {
		t.Errorf("fm alias missing title in frontmatter, got:\n%s", out)
	}
}

func TestObsidianCompat_CreateOverwriteAppend(t *testing.T) {
	_, root := newContractVault(t)

	// create
	if _, err := runObsidian(t, root, "create", "Fresh Note", "content=v1"); err != nil {
		t.Fatalf("create: %v", err)
	}
	// overwrite -> replaces content, keeps a single file
	if _, err := runObsidian(t, root, "create", "Fresh Note", "content=v2", "overwrite"); err != nil {
		t.Fatalf("create overwrite: %v", err)
	}
	out, err := runObsidian(t, root, "read", "file=Fresh Note", "format=raw")
	if err != nil {
		t.Fatalf("read after overwrite: %v", err)
	}
	if !strings.Contains(string(out), "v2") || strings.Contains(string(out), "v1") {
		t.Errorf("overwrite did not replace content, got:\n%s", out)
	}
	// Exactly one file exists for this title (no fresh-note-1.md).
	if _, statErr := os.Stat(filepath.Join(root, "fresh-note-1.md")); statErr == nil {
		t.Errorf("overwrite created a collision-deduped file fresh-note-1.md")
	}

	// append -> adds to the existing body
	if _, err := runObsidian(t, root, "create", "Fresh Note", "content=appended-line", "append"); err != nil {
		t.Fatalf("create append: %v", err)
	}
	out, err = runObsidian(t, root, "read", "file=Fresh Note", "format=raw")
	if err != nil {
		t.Fatalf("read after append: %v", err)
	}
	if !strings.Contains(string(out), "v2") || !strings.Contains(string(out), "appended-line") {
		t.Errorf("append did not preserve+extend body, got:\n%s", out)
	}

	// Index integrity: exactly one document.
	cnt, err := runObsidian(t, root, "files", "total")
	if err != nil {
		t.Fatalf("files total: %v", err)
	}
	if strings.TrimSpace(string(cnt)) != "1" {
		t.Errorf("index has %q docs after overwrite+append, want 1", strings.TrimSpace(string(cnt)))
	}
}

func TestObsidianCompat_OverwritePreservesFrontmatter(t *testing.T) {
	_, root := newContractVault(t)
	// An existing note with custom frontmatter (tags + a non-default status).
	writeCompatNote(t, root, "keeper.md", "66666666-6666-6666-6666-666666666666", "Keeper", withTags("important"))
	// Bump its status off the default so we can detect a reset.
	if _, err := runCLIArgs(t, root, "index"); err != nil {
		t.Fatalf("index: %v", err)
	}
	if _, err := runCLIArgs(t, root, "meta", "keeper.md", "--set", "status=complete"); err != nil {
		t.Fatalf("set status: %v", err)
	}

	// Overwrite the body of the same-title note.
	if _, err := runObsidian(t, root, "create", "Keeper", "content=replaced body", "overwrite"); err != nil {
		t.Fatalf("overwrite: %v", err)
	}

	// Body replaced...
	out, err := runObsidian(t, root, "read", "file=Keeper", "format=raw")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(out), "replaced body") {
		t.Errorf("overwrite did not replace body, got:\n%s", out)
	}
	// ...but frontmatter preserved (tags + status not reset to template defaults).
	fm, err := runObsidian(t, root, "meta", "file=Keeper", "format=json")
	if err != nil {
		t.Fatalf("meta: %v", err)
	}
	if !strings.Contains(string(fm), "important") {
		t.Errorf("overwrite dropped the existing tag, frontmatter:\n%s", fm)
	}
	if !strings.Contains(string(fm), "complete") {
		t.Errorf("overwrite reset status to a template default, frontmatter:\n%s", fm)
	}
}

func TestObsidianCompat_DeleteBareIsExactOnly(t *testing.T) {
	_, root := newContractVault(t)
	seedCompatVault(t, root)

	// A BARE positional that is not an exact on-disk path must NOT fuzzy-resolve
	// to a different note and delete it (the destructive-op safety guard). "beta"
	// is the basename of areas/beta.md, but as a bare delete target it must error.
	_, err := runObsidian(t, root, "delete", "beta", "--force")
	if ExitCode(err) != ExitNotFound {
		t.Fatalf("bare delete of a non-path: want exit %d, got %d (err=%v)", ExitNotFound, ExitCode(err), err)
	}
	if _, statErr := os.Stat(filepath.Join(root, "areas/beta.md")); statErr != nil {
		t.Errorf("bare delete fuzzy-resolved and deleted areas/beta.md (data loss)")
	}

	// An explicit file= still opts into fuzzy delete-by-basename.
	if _, err := runObsidian(t, root, "delete", "file=beta", "--force"); err != nil {
		t.Fatalf("file= delete: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(root, "areas/beta.md")); statErr == nil {
		t.Errorf("file= delete did not remove areas/beta.md")
	}
}

func TestObsidianCompat_SearchContentKeyword(t *testing.T) {
	_, root := newContractVault(t)
	seedCompatVault(t, root)

	// search-content forces BM25-only, so it needs no AI provider.
	out, err := runObsidian(t, root, "search-content", "Alpha")
	if err != nil {
		t.Fatalf("search-content: %v", err)
	}
	if !strings.Contains(string(out), "Alpha") {
		t.Errorf("search-content found no match, got:\n%s", out)
	}
}

func TestObsidianCompat_DailyPath(t *testing.T) {
	_, root := newContractVault(t)
	// daily path resolves + creates today's note and prints a path.
	out, err := runObsidian(t, root, "daily:path")
	if err != nil {
		t.Fatalf("daily:path: %v", err)
	}
	if !strings.HasSuffix(strings.TrimSpace(string(out)), ".md") {
		t.Errorf("daily path should print a .md path, got: %q", strings.TrimSpace(string(out)))
	}
}

func TestObsidianCompat_UnresolvedTotal(t *testing.T) {
	_, root := newContractVault(t)
	// One note with a broken wikilink.
	writeCompatNote(t, root, "n.md", "55555555-5555-5555-5555-555555555555", "Linker")
	// Append a broken link into the body and reindex.
	if err := os.WriteFile(filepath.Join(root, "n.md"),
		[]byte("---\nid: 55555555-5555-5555-5555-555555555555\ntitle: Linker\ntype: note\nstatus: draft\n---\n\nSee [[Nonexistent Target]].\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runCLIArgs(t, root, "index"); err != nil {
		t.Fatalf("index: %v", err)
	}
	out, err := runObsidian(t, root, "unresolved", "total")
	if err != nil {
		t.Fatalf("unresolved total: %v", err)
	}
	if strings.TrimSpace(string(out)) != "1" {
		t.Errorf("unresolved total = %q, want 1", strings.TrimSpace(string(out)))
	}
}
