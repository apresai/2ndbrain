package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/apresai/2ndbrain/internal/vault"
)

// Phase 6: link-aware move / rename contract tests.
//
// These exercise the full cobra dispatch (via runCLIArgs) for the only command
// that mutates OTHER notes' bodies. The fixtures build a small vault where A and
// B both link to C, then assert that moving/renaming C rewrites A and B, moves
// the file, purges the stale index row, and re-resolves backlinks.

// writeNote writes a markdown note with a UUID-bearing frontmatter at a
// vault-relative path (creating parent dirs) and returns its absolute path. It
// does NOT index; tests index via the CLI `index` command or assert on a fresh
// move.
func writeNote(t *testing.T, root, relPath, title, body string) string {
	t.Helper()
	abs := filepath.Join(root, relPath)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", relPath, err)
	}
	// Derive a unique id from the full vault-relative path so two notes that
	// share a basename (e.g. one/dup.md and two/dup.md) get distinct ids; the
	// index keys on id (ON CONFLICT id), so a collision would silently drop one.
	idSlug := strings.NewReplacer("/", "-", " ", "-", ".", "-").Replace(strings.TrimSuffix(relPath, ".md"))
	id := "id-" + idSlug
	content := "---\nid: " + id + "\ntitle: " + title + "\ntype: note\nstatus: draft\ntags: []\n---\n\n" + body + "\n"
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", relPath, err)
	}
	return abs
}

// readBody returns the on-disk body (everything after the frontmatter) of a
// vault-relative note.
func readBody(t *testing.T, root, relPath string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, relPath))
	if err != nil {
		t.Fatalf("read %s: %v", relPath, err)
	}
	s := string(b)
	// Strip the leading frontmatter block (--- ... ---).
	if strings.HasPrefix(s, "---\n") {
		if idx := strings.Index(s[4:], "\n---\n"); idx >= 0 {
			return s[4+idx+5:]
		}
	}
	return s
}

func TestContract_Move_RewritesReferencingNotes(t *testing.T) {
	_, root := newContractVault(t)

	// A and B both link to C. Renaming C's basename (C -> renamed) forces both
	// referencing notes to be rewritten, since a bare [[C]] no longer resolves
	// after the rename. (A folder-only move that keeps the basename would need
	// zero rewrites, because bare/basename links still resolve by basename.)
	writeNote(t, root, "C.md", "C note", "I am C.")
	writeNote(t, root, "A.md", "A note", "A links to [[C]] here.")
	writeNote(t, root, "B.md", "B note", "B also links to [[C#section|see C]].")

	if _, err := runCLIArgs(t, root, "index"); err != nil {
		t.Fatalf("index: %v", err)
	}

	// Move (rename) C.md -> archive/renamed.md: both the folder and basename change.
	out, err := runCLIArgs(t, root, "move", "C.md", "archive/renamed.md", "--json")
	if err != nil {
		t.Fatalf("move: %v (out=%s)", err, out)
	}

	var res moveResult
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatalf("decode move result: %v (out=%s)", err, out)
	}
	if res.Moved.From != "C.md" || res.Moved.To != "archive/renamed.md" {
		t.Errorf("moved = %+v, want C.md -> archive/renamed.md", res.Moved)
	}
	if len(res.Rewritten) != 2 {
		t.Errorf("rewritten = %d notes, want 2: %+v", len(res.Rewritten), res.Rewritten)
	}
	if len(res.SkippedAmbiguous) != 0 {
		t.Errorf("skipped_ambiguous = %+v, want none", res.SkippedAmbiguous)
	}
	if len(res.Failed) != 0 {
		t.Errorf("failed = %+v, want none", res.Failed)
	}

	// File moved.
	if _, err := os.Stat(filepath.Join(root, "archive", "renamed.md")); err != nil {
		t.Errorf("destination archive/renamed.md missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "C.md")); !os.IsNotExist(err) {
		t.Errorf("source C.md should be gone, stat err = %v", err)
	}

	// A used a bare [[C]] and the basename changed, so it becomes a bare [[renamed]].
	aBody := readBody(t, root, "A.md")
	if !strings.Contains(aBody, "[[renamed]]") {
		t.Errorf("A body should link by the new bare name [[renamed]]: %q", aBody)
	}
	// B's heading + alias suffix must survive the target rewrite.
	bBody := readBody(t, root, "B.md")
	if !strings.Contains(bBody, "[[renamed#section|see C]]") {
		t.Errorf("B body should rewrite target but preserve heading+alias: %q", bBody)
	}

	// Old index row gone, backlinks resolve to the moved doc.
	v, err := vault.Open(root)
	if err != nil {
		t.Fatalf("open vault: %v", err)
	}
	defer v.Close()

	if _, err := v.DB.GetDocumentByPath("C.md"); err == nil {
		t.Error("stale index row for C.md should be purged")
	}
	moved, err := v.DB.GetDocumentByPath("archive/renamed.md")
	if err != nil {
		t.Fatalf("moved doc not indexed at new path: %v", err)
	}
	backlinks, err := v.DB.Backlinks(moved.ID)
	if err != nil {
		t.Fatalf("backlinks: %v", err)
	}
	gotSources := map[string]bool{}
	for _, b := range backlinks {
		gotSources[b.Path] = true
	}
	if !gotSources["A.md"] || !gotSources["B.md"] {
		t.Errorf("backlinks after move = %v, want A.md and B.md", gotSources)
	}
}

// TestContract_Move_RewritesOwnSelfLinks guards the regression where a moved
// note's OWN self-links (its body links to itself) were left pointing at the
// old name on disk, because the moved doc is excluded from the referencing-note
// set and IndexSingleFile only updates the index, not the body. After the fix,
// step (e2) rewrites the moved file's body at its new path.
func TestContract_Move_RewritesOwnSelfLinks(t *testing.T) {
	_, root := newContractVault(t)

	// hub links to itself by bare name AND path form, plus a heading anchor.
	writeNote(t, root, "notes/hub.md", "Hub",
		"See [[hub]] and [[notes/hub]] and [[hub#intro]] for context.")

	if _, err := runCLIArgs(t, root, "index"); err != nil {
		t.Fatalf("index: %v", err)
	}

	if _, err := runCLIArgs(t, root, "move", "notes/hub.md", "notes/center.md", "--json"); err != nil {
		t.Fatalf("move: %v", err)
	}

	body := readBody(t, root, "notes/center.md")
	// No self-link should still point at the old name.
	if strings.Contains(body, "[[hub") || strings.Contains(body, "[[notes/hub") {
		t.Errorf("moved note still has self-links to the old name: %q", body)
	}
	// The bare, path, and heading forms should all be rewritten to the new name.
	if !strings.Contains(body, "[[center]]") {
		t.Errorf("bare self-link not rewritten to [[center]]: %q", body)
	}
	if !strings.Contains(body, "[[notes/center]]") {
		t.Errorf("path self-link not rewritten to [[notes/center]]: %q", body)
	}
	if !strings.Contains(body, "[[center#intro]]") {
		t.Errorf("heading self-link not rewritten to [[center#intro]]: %q", body)
	}

	// And lint should report zero broken wikilinks for the moved note.
	out, err := runCLIArgs(t, root, "lint", "notes/center.md", "--json", "--porcelain")
	if err != nil {
		t.Fatalf("lint: %v\n%s", err, out)
	}
	if strings.Contains(string(out), `"level": "error"`) {
		t.Errorf("moved note has broken-link lint errors after self-link rewrite:\n%s", out)
	}
}

func TestContract_Move_RenameWrapper(t *testing.T) {
	_, root := newContractVault(t)

	writeNote(t, root, "notes/draft.md", "Draft", "the draft body.")
	writeNote(t, root, "index.md", "Index", "Point to [[notes/draft]] and also [[draft]].")
	if _, err := runCLIArgs(t, root, "index"); err != nil {
		t.Fatalf("index: %v", err)
	}

	out, err := runCLIArgs(t, root, "rename", "notes/draft.md", "final", "--json")
	if err != nil {
		t.Fatalf("rename: %v (out=%s)", err, out)
	}
	var res moveResult
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatalf("decode rename result: %v (out=%s)", err, out)
	}
	if res.Moved.To != "notes/final.md" {
		t.Errorf("rename target = %q, want notes/final.md (.md appended, same dir)", res.Moved.To)
	}
	if _, err := os.Stat(filepath.Join(root, "notes", "final.md")); err != nil {
		t.Errorf("renamed file missing: %v", err)
	}

	body := readBody(t, root, "index.md")
	if !strings.Contains(body, "[[notes/final]]") {
		t.Errorf("path-form link should become [[notes/final]]: %q", body)
	}
	if !strings.Contains(body, "[[final]]") {
		t.Errorf("bare-form link should become [[final]]: %q", body)
	}
}

func TestContract_Move_DryRunWritesNothing(t *testing.T) {
	_, root := newContractVault(t)

	writeNote(t, root, "C.md", "C", "C body")
	writeNote(t, root, "A.md", "A", "links [[C]].")
	if _, err := runCLIArgs(t, root, "index"); err != nil {
		t.Fatalf("index: %v", err)
	}

	beforeA := readBody(t, root, "A.md")

	// Rename (basename change) so a bare [[C]] is actually planned for rewrite.
	out, err := runCLIArgs(t, root, "move", "C.md", "moved/renamed.md", "--dry-run", "--json")
	if err != nil {
		t.Fatalf("dry-run move: %v (out=%s)", err, out)
	}
	var res moveResult
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatalf("decode: %v (out=%s)", err, out)
	}
	if !res.DryRun {
		t.Error("dry_run flag should be true in result")
	}
	if len(res.Rewritten) != 1 || res.Rewritten[0].Path != "A.md" {
		t.Errorf("dry-run should plan to rewrite A.md: %+v", res.Rewritten)
	}

	// Nothing on disk changed: source still present, dest absent, A untouched.
	if _, err := os.Stat(filepath.Join(root, "C.md")); err != nil {
		t.Errorf("dry-run must not move the file; C.md missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "moved", "renamed.md")); !os.IsNotExist(err) {
		t.Errorf("dry-run must not create the destination; stat err = %v", err)
	}
	if got := readBody(t, root, "A.md"); got != beforeA {
		t.Errorf("dry-run must not rewrite A.md:\n got %q\nwant %q", got, beforeA)
	}
}

// TestContract_Move_RewritesMarkdownLinks guards that move/rename rewrites
// markdown-style [label](old.md) links (not just [[wikilinks]]) so a rename no
// longer silently breaks them, and that the dry-run preview counts the md-link
// rewrite. The links use the path form ([..](notes/C.md)) so the rewrite is
// confidently correct: a path-form md-link becomes the new vault-relative path
// keeping its ".md" extension, the [label] text, and any #anchor suffix.
func TestContract_Move_RewritesMarkdownLinks(t *testing.T) {
	_, root := newContractVault(t)

	writeNote(t, root, "notes/C.md", "C note", "I am C.")
	// A references C via a path-form markdown link with a heading anchor;
	// B references C via a plain path-form markdown link. Neither uses [[..]].
	writeNote(t, root, "A.md", "A note", "A links to [see C](notes/C.md#intro) here.")
	writeNote(t, root, "B.md", "B note", "B also links to [the C doc](notes/C.md).")

	if _, err := runCLIArgs(t, root, "index"); err != nil {
		t.Fatalf("index: %v", err)
	}

	// Dry-run first: it must plan to rewrite both referencing notes' md-links
	// without touching disk.
	beforeA := readBody(t, root, "A.md")
	beforeB := readBody(t, root, "B.md")
	out, err := runCLIArgs(t, root, "move", "notes/C.md", "notes/renamed.md", "--dry-run", "--json")
	if err != nil {
		t.Fatalf("dry-run move: %v (out=%s)", err, out)
	}
	var dry moveResult
	if err := json.Unmarshal(out, &dry); err != nil {
		t.Fatalf("decode dry-run: %v (out=%s)", err, out)
	}
	if !dry.DryRun {
		t.Error("dry_run flag should be true")
	}
	if len(dry.Rewritten) != 2 {
		t.Errorf("dry-run should plan 2 md-link rewrites, got %+v", dry.Rewritten)
	}
	if got := readBody(t, root, "A.md"); got != beforeA {
		t.Errorf("dry-run must not modify A.md")
	}
	if got := readBody(t, root, "B.md"); got != beforeB {
		t.Errorf("dry-run must not modify B.md")
	}

	// Real move: rewrite both md-links, preserving label text and the #intro anchor.
	out, err = runCLIArgs(t, root, "move", "notes/C.md", "notes/renamed.md", "--json")
	if err != nil {
		t.Fatalf("move: %v (out=%s)", err, out)
	}
	var res moveResult
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatalf("decode move result: %v (out=%s)", err, out)
	}
	if len(res.Rewritten) != 2 {
		t.Errorf("rewritten = %d notes, want 2: %+v", len(res.Rewritten), res.Rewritten)
	}

	aBody := readBody(t, root, "A.md")
	if !strings.Contains(aBody, "[see C](notes/renamed.md#intro)") {
		t.Errorf("A md-link should be rewritten with label + #intro preserved: %q", aBody)
	}
	bBody := readBody(t, root, "B.md")
	if !strings.Contains(bBody, "[the C doc](notes/renamed.md)") {
		t.Errorf("B md-link should be rewritten with label preserved: %q", bBody)
	}
}

func TestContract_Move_MissingSourceErrors(t *testing.T) {
	_, root := newContractVault(t)
	if _, err := runCLIArgs(t, root, "index"); err != nil {
		t.Fatalf("index: %v", err)
	}
	out, err := runCLIArgs(t, root, "move", "nope.md", "dst.md")
	if err == nil {
		t.Fatalf("expected error moving a missing source, got nil (out=%s)", out)
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error should mention 'not found': %v", err)
	}
}

func TestContract_Move_DestExistsWithoutForceRefused(t *testing.T) {
	_, root := newContractVault(t)
	writeNote(t, root, "src.md", "Src", "source")
	writeNote(t, root, "dst.md", "Dst", "destination")
	if _, err := runCLIArgs(t, root, "index"); err != nil {
		t.Fatalf("index: %v", err)
	}

	out, err := runCLIArgs(t, root, "move", "src.md", "dst.md")
	if err == nil {
		t.Fatalf("expected refusal when dst exists without --force (out=%s)", out)
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("error should mention destination exists: %v", err)
	}
	// Both files must still exist (no move happened).
	if _, serr := os.Stat(filepath.Join(root, "src.md")); serr != nil {
		t.Errorf("src.md should be untouched: %v", serr)
	}
}

// --force overwrites an existing destination, clobbering its file AND purging
// its stale index row so the moved doc indexes cleanly at the now-unique path
// (documents.path is UNIQUE).
func TestContract_Move_ForceOverwritesDestination(t *testing.T) {
	_, root := newContractVault(t)
	writeNote(t, root, "src.md", "Src", "the source body keeps this.")
	writeNote(t, root, "dst.md", "Dst", "the destination body gets clobbered.")
	if _, err := runCLIArgs(t, root, "index"); err != nil {
		t.Fatalf("index: %v", err)
	}

	out, err := runCLIArgs(t, root, "move", "src.md", "dst.md", "--force", "--json")
	if err != nil {
		t.Fatalf("force move: %v (out=%s)", err, out)
	}
	var res moveResult
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatalf("decode: %v (out=%s)", err, out)
	}
	if len(res.Failed) != 0 {
		t.Errorf("force move should not report failures: %+v", res.Failed)
	}

	// Source gone, destination holds the source's content.
	if _, serr := os.Stat(filepath.Join(root, "src.md")); !os.IsNotExist(serr) {
		t.Errorf("src.md should be gone after move, stat err = %v", serr)
	}
	body := readBody(t, root, "dst.md")
	if !strings.Contains(body, "the source body keeps this") {
		t.Errorf("dst.md should hold the moved source content: %q", body)
	}

	// Index has exactly one row at dst.md (the UNIQUE path constraint held).
	v, verr := vault.Open(root)
	if verr != nil {
		t.Fatalf("open vault: %v", verr)
	}
	defer v.Close()
	var n int
	if qerr := v.DB.Conn().QueryRow("SELECT COUNT(*) FROM documents WHERE path = ?", "dst.md").Scan(&n); qerr != nil {
		t.Fatalf("count dst rows: %v", qerr)
	}
	if n != 1 {
		t.Errorf("expected exactly 1 index row at dst.md, got %d", n)
	}
	if _, gerr := v.DB.GetDocumentByPath("src.md"); gerr == nil {
		t.Error("stale src.md index row should be purged")
	}
}

func TestContract_Move_AbsoluteDestRejected(t *testing.T) {
	_, root := newContractVault(t)
	writeNote(t, root, "src.md", "Src", "source")
	if _, err := runCLIArgs(t, root, "index"); err != nil {
		t.Fatalf("index: %v", err)
	}
	out, err := runCLIArgs(t, root, "move", "src.md", "/tmp/escape.md")
	if err == nil {
		t.Fatalf("expected rejection of an absolute destination (out=%s)", out)
	}
	if !strings.Contains(err.Error(), "absolute") {
		t.Errorf("error should mention absolute path: %v", err)
	}
}

// Ambiguity: two docs named "dup" in different folders. A bare [[dup]] link can't
// be attributed to either, so --dry-run reports it and a non-force move refuses.
func TestContract_Move_AmbiguousBareLink(t *testing.T) {
	_, root := newContractVault(t)
	writeNote(t, root, "one/dup.md", "Dup One", "first dup")
	writeNote(t, root, "two/dup.md", "Dup Two", "second dup")
	writeNote(t, root, "ref.md", "Ref", "ambiguous [[dup]] and explicit [[one/dup]].")
	if _, err := runCLIArgs(t, root, "index"); err != nil {
		t.Fatalf("index: %v", err)
	}

	// Dry-run should surface the ambiguous bare link AND plan the path-form rewrite.
	out, err := runCLIArgs(t, root, "move", "one/dup.md", "one/renamed.md", "--dry-run", "--json")
	if err != nil {
		t.Fatalf("dry-run move: %v (out=%s)", err, out)
	}
	var res moveResult
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatalf("decode: %v (out=%s)", err, out)
	}
	if len(res.SkippedAmbiguous) == 0 {
		t.Errorf("dry-run should report the ambiguous bare [[dup]] link: %+v", res)
	}
	// The path-qualified [[one/dup]] is unambiguous and should still be planned.
	if len(res.Rewritten) != 1 || res.Rewritten[0].Path != "ref.md" {
		t.Errorf("path-form link in ref.md should be planned for rewrite: %+v", res.Rewritten)
	}

	refBefore := readBody(t, root, "ref.md")

	// Non-force real move must be refused due to the ambiguity.
	out, err = runCLIArgs(t, root, "move", "one/dup.md", "one/renamed.md")
	if err == nil {
		t.Fatalf("non-force move should be refused on ambiguity (out=%s)", out)
	}
	if !strings.Contains(err.Error(), "ambiguous") {
		t.Errorf("refusal should mention ambiguity: %v", err)
	}
	// The file must NOT have moved.
	if _, serr := os.Stat(filepath.Join(root, "one", "dup.md")); serr != nil {
		t.Errorf("one/dup.md should be untouched after refused move: %v", serr)
	}
	// And no referencing note may have been partially rewritten: the refusal
	// happens in the plan phase, before any write, so ref.md must be byte-for-byte
	// what it was. (Writing the unambiguous path link then refusing the move would
	// leave [[one/dup]] pointing at a path that does not exist.)
	if got := readBody(t, root, "ref.md"); got != refBefore {
		t.Errorf("refused move must not write any rewrite:\n got %q\nwant %q", got, refBefore)
	}

	// --force proceeds: path-form link rewritten, bare [[dup]] left untouched.
	out, err = runCLIArgs(t, root, "move", "one/dup.md", "one/renamed.md", "--force", "--json")
	if err != nil {
		t.Fatalf("force move: %v (out=%s)", err, out)
	}
	body := readBody(t, root, "ref.md")
	if !strings.Contains(body, "[[one/renamed]]") {
		t.Errorf("path-form link should be rewritten under --force: %q", body)
	}
	if !strings.Contains(body, "[[dup]]") {
		t.Errorf("ambiguous bare [[dup]] should be left untouched under --force: %q", body)
	}
}
