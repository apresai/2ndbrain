package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// Phase 1 link/health command contracts. A small real vault is written to disk
// and indexed through the CLI (so links resolve), then each command is invoked
// the way the GUI / a shell user would: with --json --porcelain. The fixture:
//
//	a.md -> [[b]] (resolved) and -> [[nope]] (broken)
//	b.md -> (no outbound links; one inbound from a)
//	c.md -> (no inbound, no outbound)
func writeLinkFixture(t *testing.T, root string) {
	t.Helper()
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("a.md", "---\ntitle: A\ntype: note\nstatus: draft\n---\nSee [[b]] and also [[nope]].\n")
	write("b.md", "---\ntitle: B\ntype: note\nstatus: draft\n---\nPlain target, no links.\n")
	write("c.md", "---\ntitle: C\ntype: note\nstatus: draft\n---\nIsolated note, no links.\n")

	if _, err := runCLIArgs(t, root, "index"); err != nil {
		t.Fatalf("index: %v", err)
	}
}

func TestContract_Backlinks(t *testing.T) {
	_, root := newContractVault(t)
	writeLinkFixture(t, root)

	out, err := runCLIArgs(t, root, "backlinks", "b.md", "--json", "--porcelain")
	if err != nil {
		t.Fatalf("backlinks: %v\n%s", err, out)
	}
	var refs []struct {
		Path      string `json:"path"`
		Title     string `json:"title"`
		TargetRaw string `json:"target_raw"`
		Resolved  bool   `json:"resolved"`
	}
	if err := json.Unmarshal(out, &refs); err != nil {
		t.Fatalf("backlinks JSON: %v\n%s", err, out)
	}
	if len(refs) != 1 {
		t.Fatalf("backlinks(b.md): got %d, want 1: %+v", len(refs), refs)
	}
	if refs[0].Path != "a.md" {
		t.Errorf("backlink source: got %q, want a.md", refs[0].Path)
	}
	if !refs[0].Resolved {
		t.Errorf("backlink should be resolved: %+v", refs[0])
	}

	// A document nothing links to reports an empty inbound set (exit 0, null/[]).
	out, err = runCLIArgs(t, root, "backlinks", "c.md", "--json", "--porcelain")
	if err != nil {
		t.Fatalf("backlinks c.md: %v\n%s", err, out)
	}
	var none []json.RawMessage
	if err := json.Unmarshal(out, &none); err != nil {
		t.Fatalf("backlinks c.md JSON: %v\n%s", err, out)
	}
	if len(none) != 0 {
		t.Errorf("backlinks(c.md): got %d, want 0: %s", len(none), out)
	}
}

func TestContract_Links(t *testing.T) {
	_, root := newContractVault(t)
	writeLinkFixture(t, root)

	out, err := runCLIArgs(t, root, "links", "a.md", "--json", "--porcelain")
	if err != nil {
		t.Fatalf("links: %v\n%s", err, out)
	}
	var refs []struct {
		Path      string `json:"path"`
		Title     string `json:"title"`
		TargetRaw string `json:"target_raw"`
		Resolved  bool   `json:"resolved"`
	}
	if err := json.Unmarshal(out, &refs); err != nil {
		t.Fatalf("links JSON: %v\n%s", err, out)
	}
	if len(refs) != 2 {
		t.Fatalf("links(a.md): got %d, want 2 (one resolved, one broken): %+v", len(refs), refs)
	}
	byRaw := map[string]bool{} // target_raw -> resolved
	for _, r := range refs {
		byRaw[r.TargetRaw] = r.Resolved
	}
	if resolved, ok := byRaw["b"]; !ok || !resolved {
		t.Errorf("link to [[b]] should be present and resolved: %+v", refs)
	}
	if broken, ok := byRaw["nope"]; !ok || broken {
		t.Errorf("link to [[nope]] should be present and unresolved: %+v", refs)
	}
}

func TestContract_Orphans(t *testing.T) {
	_, root := newContractVault(t)
	writeLinkFixture(t, root)

	out, err := runCLIArgs(t, root, "orphans", "--json", "--porcelain")
	if err != nil {
		t.Fatalf("orphans: %v\n%s", err, out)
	}
	var refs []struct {
		Path  string `json:"path"`
		Title string `json:"title"`
	}
	if err := json.Unmarshal(out, &refs); err != nil {
		t.Fatalf("orphans JSON: %v\n%s", err, out)
	}
	paths := map[string]bool{}
	for _, r := range refs {
		paths[r.Path] = true
	}
	// b.md has an inbound link from a.md → not an orphan.
	if paths["b.md"] {
		t.Errorf("b.md has an inbound link and must not be an orphan: %+v", refs)
	}
	// a.md and c.md have no inbound link → orphans.
	if !paths["a.md"] {
		t.Errorf("a.md has no inbound link and must be an orphan: %+v", refs)
	}
	if !paths["c.md"] {
		t.Errorf("c.md has no inbound link and must be an orphan: %+v", refs)
	}
}

func TestContract_Deadends(t *testing.T) {
	_, root := newContractVault(t)
	writeLinkFixture(t, root)

	out, err := runCLIArgs(t, root, "deadends", "--json", "--porcelain")
	if err != nil {
		t.Fatalf("deadends: %v\n%s", err, out)
	}
	var refs []struct {
		Path  string `json:"path"`
		Title string `json:"title"`
	}
	if err := json.Unmarshal(out, &refs); err != nil {
		t.Fatalf("deadends JSON: %v\n%s", err, out)
	}
	paths := map[string]bool{}
	for _, r := range refs {
		paths[r.Path] = true
	}
	// a.md has a resolved outbound link to b.md → not a deadend.
	if paths["a.md"] {
		t.Errorf("a.md has a resolved outbound link and must not be a deadend: %+v", refs)
	}
	// b.md and c.md have no outbound links → deadends.
	if !paths["b.md"] {
		t.Errorf("b.md has no outbound link and must be a deadend: %+v", refs)
	}
	if !paths["c.md"] {
		t.Errorf("c.md has no outbound link and must be a deadend: %+v", refs)
	}
}

// TestContract_Unresolved covers the vault-wide broken-link command. The
// fixture's a.md links [[b]] (resolves) and [[nope]] (broken), so unresolved
// must report exactly the a.md -> nope row and nothing for the resolved link.
func TestContract_Unresolved(t *testing.T) {
	_, root := newContractVault(t)
	writeLinkFixture(t, root)

	out, err := runCLIArgs(t, root, "unresolved", "--json", "--porcelain")
	if err != nil {
		t.Fatalf("unresolved: %v\n%s", err, out)
	}
	var refs []struct {
		SourcePath string `json:"source_path"`
		TargetRaw  string `json:"target_raw"`
	}
	if err := json.Unmarshal(out, &refs); err != nil {
		t.Fatalf("unresolved JSON: %v\n%s", err, out)
	}
	if len(refs) != 1 {
		t.Fatalf("unresolved: got %d rows, want exactly 1 (a.md -> nope): %+v", len(refs), refs)
	}
	if refs[0].SourcePath != "a.md" {
		t.Errorf("unresolved source: got %q, want a.md", refs[0].SourcePath)
	}
	if refs[0].TargetRaw != "nope" {
		t.Errorf("unresolved target: got %q, want nope", refs[0].TargetRaw)
	}
}

// TestContract_BacklinksMissingDoc confirms an unknown path exits non-zero
// rather than emitting an empty success.
func TestContract_BacklinksMissingDoc(t *testing.T) {
	_, root := newContractVault(t)
	writeLinkFixture(t, root)

	if _, err := runCLIArgs(t, root, "backlinks", "does-not-exist.md", "--json", "--porcelain"); err == nil {
		t.Error("expected error for a non-existent document path")
	}
}
