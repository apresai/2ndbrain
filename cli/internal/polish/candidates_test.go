package polish

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/apresai/2ndbrain/internal/document"
	"github.com/apresai/2ndbrain/internal/testutil"
	"github.com/apresai/2ndbrain/internal/vault"
)

// note builds a minimal markdown body with the given title + prose.
func note(title, body string) string {
	return "---\ntitle: " + title + "\ntype: note\nstatus: draft\n---\n\n# " + title + "\n\n" + body + "\n"
}

func TestGatherCandidates_SubstringAndExclusions(t *testing.T) {
	v := testutil.NewTestVault(t)

	testutil.CreateAndIndex(t, v, "Auth Flow", "note", note("Auth Flow", "How auth works."))
	testutil.CreateAndIndex(t, v, "JWT Tokens", "note", note("JWT Tokens", "Token details."))

	srcBody := "We rely on the Auth Flow daily.\n\n```\nUse JWT Tokens here in code\n```\n"
	src := testutil.CreateAndIndex(t, v, "Source Doc", "note", note("Source Doc", srcBody))

	// No embeddings loaded → semantic step is skipped; substring path drives.
	cands, _, err := GatherCandidates(context.Background(), v, nil, CandidateInput{
		Source:     src,
		SourcePath: src.Path,
		Max:        10,
	})
	if err != nil {
		t.Fatalf("GatherCandidates: %v", err)
	}

	titles := map[string]bool{}
	for _, c := range cands {
		titles[c.Title] = true
		if c.Source != "substring" {
			t.Errorf("expected substring source, got %q for %q", c.Source, c.Title)
		}
	}
	if !titles["Auth Flow"] {
		t.Errorf("Auth Flow mentioned in prose should be a candidate; got %v", titles)
	}
	if titles["JWT Tokens"] {
		t.Errorf("JWT Tokens appears only inside code and must NOT be a candidate")
	}
	if titles["Source Doc"] {
		t.Errorf("the source note must never suggest itself")
	}
}

func TestGatherCandidates_DropsAmbiguousTitle(t *testing.T) {
	v := testutil.NewTestVault(t)
	write := func(rel, body string) {
		t.Helper()
		abs := filepath.Join(v.Root, rel)
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(abs, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	// Two notes share the title "Shared Topic" in different folders → a bare
	// [[Shared Topic]] would be ambiguous, so it must not be offered as a target.
	write("a/dup.md", note("Shared Topic", "First copy."))
	write("b/dup.md", note("Shared Topic", "Second copy."))
	write("src.md", note("Source", "This design builds on the Shared Topic heavily."))
	if _, err := vault.IndexVault(v, func(string) {}); err != nil {
		t.Fatalf("index: %v", err)
	}

	src, err := document.ParseFile(filepath.Join(v.Root, "src.md"))
	if err != nil {
		t.Fatalf("parse src: %v", err)
	}
	cands, _, err := GatherCandidates(context.Background(), v, nil, CandidateInput{
		Source:     src,
		SourcePath: "src.md",
		Max:        10,
	})
	if err != nil {
		t.Fatalf("GatherCandidates: %v", err)
	}
	for _, c := range cands {
		if NormalizeLinkKey(c.Title) == "shared topic" {
			t.Errorf("ambiguous title must be dropped, but got candidate %+v", c)
		}
	}
}

func TestGatherCandidates_CapRespected(t *testing.T) {
	v := testutil.NewTestVault(t)
	testutil.CreateAndIndex(t, v, "Alpha Topic", "note", note("Alpha Topic", "x"))
	testutil.CreateAndIndex(t, v, "Beta Topic", "note", note("Beta Topic", "y"))
	testutil.CreateAndIndex(t, v, "Gamma Topic", "note", note("Gamma Topic", "z"))

	src := testutil.CreateAndIndex(t, v, "Hub", "note",
		note("Hub", "Covers Alpha Topic, Beta Topic, and Gamma Topic together."))

	cands, _, err := GatherCandidates(context.Background(), v, nil, CandidateInput{
		Source:     src,
		SourcePath: src.Path,
		Max:        2,
	})
	if err != nil {
		t.Fatalf("GatherCandidates: %v", err)
	}
	if len(cands) != 2 {
		t.Fatalf("Max=2 must cap candidates, got %d: %+v", len(cands), cands)
	}
}
