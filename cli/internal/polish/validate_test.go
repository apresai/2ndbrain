package polish

import (
	"reflect"
	"testing"
)

func allowedSet(keys ...string) map[string]bool {
	m := make(map[string]bool, len(keys))
	for _, k := range keys {
		m[NormalizeLinkKey(k)] = true
	}
	return m
}

func TestStripInventedLinks(t *testing.T) {
	allowed := allowedSet("Auth Flow", "JWT Tokens")

	tests := []struct {
		name      string
		body      string
		want      string
		wantStrip []string
	}{
		{
			name:      "keeps allowed link",
			body:      "See [[Auth Flow]] for details.",
			want:      "See [[Auth Flow]] for details.",
			wantStrip: nil,
		},
		{
			name:      "strips invented link to display text",
			body:      "We use [[Nonexistent Note]] here.",
			want:      "We use Nonexistent Note here.",
			wantStrip: []string{"Nonexistent Note"},
		},
		{
			name:      "strips invented aliased link to its alias",
			body:      "Read [[Missing|the missing doc]] now.",
			want:      "Read the missing doc now.",
			wantStrip: []string{"Missing"},
		},
		{
			name:      "keeps allowed, strips invented in same body",
			body:      "[[JWT Tokens]] and [[Ghost]].",
			want:      "[[JWT Tokens]] and Ghost.",
			wantStrip: []string{"Ghost"},
		},
		{
			name:      "ignores wikilink-looking text inside inline code",
			body:      "Type `[[Ghost]]` literally; link [[Auth Flow]].",
			want:      "Type `[[Ghost]]` literally; link [[Auth Flow]].",
			wantStrip: nil,
		},
		{
			name:      "strips invented embed including bang",
			body:      "Before ![[Ghost]] after.",
			want:      "Before Ghost after.",
			wantStrip: []string{"Ghost"},
		},
		{
			name:      "keeps allowed link with heading anchor",
			body:      "See [[Auth Flow#Setup]].",
			want:      "See [[Auth Flow#Setup]].",
			wantStrip: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, stripped := StripInventedLinks(tc.body, allowed)
			if got != tc.want {
				t.Errorf("body:\n got: %q\nwant: %q", got, tc.want)
			}
			if !reflect.DeepEqual(stripped, tc.wantStrip) {
				t.Errorf("removed: got %v want %v", stripped, tc.wantStrip)
			}
		})
	}
}

func TestNewLinks(t *testing.T) {
	orig := "Existing [[Kept Note]] here."
	polished := "Existing [[Kept Note]] here, plus [[Fresh Note]] and [[Another]]."
	added := NewLinks(orig, polished)
	if len(added) != 2 {
		t.Fatalf("want 2 new links, got %d: %+v", len(added), added)
	}
	targets := map[string]bool{}
	for _, l := range added {
		targets[l.Target] = true
	}
	if !targets["Fresh Note"] || !targets["Another"] {
		t.Fatalf("unexpected new link targets: %+v", targets)
	}
}

func TestExistingLinksPreserved(t *testing.T) {
	orig := "Link to [[Alpha]] and [[Beta]]."
	if !ExistingLinksPreserved(orig, "Now [[Alpha]] then [[Beta]] reworded.") {
		t.Errorf("should be preserved when both targets remain")
	}
	if ExistingLinksPreserved(orig, "Only [[Alpha]] remains.") {
		t.Errorf("should fail when [[Beta]] dropped")
	}
}

func TestCodeSpansEqual(t *testing.T) {
	orig := "Text\n```go\nfmt.Println(\"teh\")\n```\nand `inline teh`."
	sameCode := "Different prose\n```go\nfmt.Println(\"teh\")\n```\nand `inline teh` reworded."
	if !CodeSpansEqual(orig, sameCode) {
		t.Errorf("code identical, prose changed → should be equal")
	}
	changedFence := "Text\n```go\nfmt.Println(\"the\")\n```\n"
	if CodeSpansEqual(orig, changedFence) {
		t.Errorf("fenced code edited → should be unequal")
	}
	changedInline := "Text\n```go\nfmt.Println(\"teh\")\n```\nand `inline the`."
	if CodeSpansEqual(orig, changedInline) {
		t.Errorf("inline code edited → should be unequal")
	}
}

func TestHeadingStructureEqual(t *testing.T) {
	orig := "# Title\n\nintro\n\n## Section A\n\nbody\n\n### Sub\n"
	reworded := "# Title\n\nrewritten intro\n\n## Section A\n\nbetter body\n\n### Sub\n"
	if !HeadingStructureEqual(orig, reworded) {
		t.Errorf("same headings, reworded body → should be equal")
	}
	renamed := "# Title\n\n## Section B\n\n### Sub\n"
	if HeadingStructureEqual(orig, renamed) {
		t.Errorf("heading renamed → should be unequal")
	}
	// A '#' inside a fence must not count as a heading.
	withCodeHash := "# Title\n\n```\n## not a heading\n```\n\n## Section A\n\n### Sub\n"
	if !HeadingStructureEqual(orig, withCodeHash) {
		t.Errorf("hash inside fence must be ignored")
	}
}

func TestAllowedLinkSet(t *testing.T) {
	cands := []LinkCandidate{
		{Title: "Auth Flow", Aliases: []string{"auth", "login flow"}},
		{Title: "JWT Tokens"},
	}
	original := "Existing link to [[Token Store]] and [[Auth Flow#Setup]] here."
	set := AllowedLinkSet(cands, original)

	// Candidate titles + aliases + every existing link target must be allowed.
	for _, key := range []string{"Auth Flow", "auth", "login flow", "JWT Tokens", "Token Store"} {
		if !set[NormalizeLinkKey(key)] {
			t.Errorf("AllowedLinkSet missing %q", key)
		}
	}
	if set[NormalizeLinkKey("Nonexistent")] {
		t.Errorf("AllowedLinkSet must not contain a target that is neither a candidate nor an existing link")
	}
}

func TestNormalizeLinkKey(t *testing.T) {
	cases := map[string]string{
		"Auth Flow":       "auth flow",
		"  Auth Flow.md ": "auth flow",
		"./notes/Auth":    "notes/auth",
		`notes\Auth`:      "notes/auth",
	}
	for in, want := range cases {
		if got := NormalizeLinkKey(in); got != want {
			t.Errorf("NormalizeLinkKey(%q) = %q, want %q", in, got, want)
		}
	}
}
