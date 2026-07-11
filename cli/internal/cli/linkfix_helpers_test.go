package cli

// Ungated unit tests for the pure link-fix helpers: the re-rank message
// builders in suggest_target.go and the corpus-corruption helpers in
// linkfix_eval_test.go. These run in every `make test` — unlike the
// credential-gated TestLinkFixEval, which is the only other caller of the
// corruption helpers and would otherwise let a regression hide until the next
// paid eval run.

import (
	"strings"
	"testing"

	"github.com/apresai/2ndbrain/internal/polish"
)

func TestBuildRerankCatalog_WithScores(t *testing.T) {
	cands := []SuggestLinkResult{
		{Path: "a/b.md", Title: "Note B", Score: 0.5, Confidence: "medium"},
		{Path: "c.md", Title: "", Score: 1.0, Confidence: "low"},
	}
	got := buildRerankCatalog(cands, true)
	if !strings.Contains(got, `1. path=a/b.md title="Note B" conf=medium score=0.500`) {
		t.Fatalf("scored line 1 wrong:\n%s", got)
	}
	// Empty title falls back to the path.
	if !strings.Contains(got, `2. path=c.md title="c.md" conf=low score=1.000`) {
		t.Fatalf("scored line 2 (title fallback) wrong:\n%s", got)
	}
}

func TestBuildRerankCatalog_WithoutScores(t *testing.T) {
	cands := []SuggestLinkResult{{Path: "a/b.md", Title: "Note B", Score: 0.5, Confidence: "medium"}}
	got := buildRerankCatalog(cands, false)
	if !strings.Contains(got, `1. path=a/b.md title="Note B"`) {
		t.Fatalf("unscored line wrong:\n%s", got)
	}
	if strings.Contains(got, "conf=") || strings.Contains(got, "score=") {
		t.Fatalf("unscored catalog must omit conf/score:\n%s", got)
	}
}

func TestBuildRerankUser(t *testing.T) {
	catalog := "1. path=a.md title=\"A\"\n"
	withCtx := buildRerankUser("my-target", "some surrounding prose", catalog)
	for _, want := range []string{`Broken wikilink target: "my-target"`, "Surrounding note context:\nsome surrounding prose", "Shortlist (grounded existing notes):\n" + catalog} {
		if !strings.Contains(withCtx, want) {
			t.Fatalf("missing %q in:\n%s", want, withCtx)
		}
	}
	noCtx := buildRerankUser("my-target", "", catalog)
	if strings.Contains(noCtx, "Surrounding note context") {
		t.Fatalf("empty snippet must omit the context section:\n%s", noCtx)
	}
}

func TestParseLLMPicks_ConfidenceField(t *testing.T) {
	picks, err := parseLLMPicks(`[{"path":"a.md","reason":"same note","confidence":"high"}]`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(picks) != 1 || picks[0].Confidence != "high" {
		t.Fatalf("confidence not parsed: %+v", picks)
	}
}

func TestParseLLMPicks_NullIsDecline(t *testing.T) {
	// A literal JSON null parses to zero picks with no error — the decline
	// path, not a failure.
	picks, err := parseLLMPicks("null")
	if err != nil || len(picks) != 0 {
		t.Fatalf("null should be an empty decline, got %v, %v", picks, err)
	}
}

func TestDriftCorrupt_PreservesNormalizedForm(t *testing.T) {
	cases := []struct{ in, want string }{
		{"aws-bedrock-latest", "Aws Bedrock Latest"}, // kebab -> Title Case spaces
		{"Some Note Title", "some-note-title"},       // spaced -> kebab lower
		{"snake_case_name", "Snake Case Name"},
	}
	for _, c := range cases {
		got := driftCorrupt(c.in)
		if got != c.want {
			t.Errorf("driftCorrupt(%q) = %q, want %q", c.in, got, c.want)
		}
		// The whole point of the drift class: the normalized form is unchanged,
		// so the deterministic repair index still maps it to the same note.
		if polish.NormalizeName(got) != polish.NormalizeName(c.in) {
			t.Errorf("driftCorrupt(%q) = %q changed the normalized form", c.in, got)
		}
	}
}

func TestTypoCorrupt(t *testing.T) {
	in := "notarytool-sigbus-workaround"
	got := typoCorrupt(in)
	if got == "" || strings.EqualFold(got, in) {
		t.Fatalf("typoCorrupt(%q) = %q, want a changed non-empty form", in, got)
	}
	if len(got) != len(in)-1 {
		t.Fatalf("typoCorrupt(%q) = %q, want exactly one dropped character", in, got)
	}
	// No word long enough to corrupt -> empty (case skipped by the corpus builder).
	if got := typoCorrupt("a-bb-cc"); got != "" {
		t.Fatalf("typoCorrupt on short words should return empty, got %q", got)
	}
}

func TestReorderCorrupt(t *testing.T) {
	if got := reorderCorrupt("aws-bedrock-models"); got != "bedrock-models-aws" {
		t.Fatalf("reorderCorrupt = %q, want bedrock-models-aws", got)
	}
	if got := reorderCorrupt("single"); got != "" {
		t.Fatalf("reorderCorrupt on one word should return empty, got %q", got)
	}
}

func TestWorddropCorrupt(t *testing.T) {
	if got := worddropCorrupt("aws-bedrock-latest-models"); got != "aws-bedrock-latest" {
		t.Fatalf("worddropCorrupt = %q, want aws-bedrock-latest", got)
	}
	if got := worddropCorrupt("two-words"); got != "" {
		t.Fatalf("worddropCorrupt below 3 words should return empty, got %q", got)
	}
}

func TestStridePairs_DeterministicAndSized(t *testing.T) {
	pairs := make([]resolvedPair, 20)
	for i := range pairs {
		pairs[i] = resolvedPair{source: string(rune('a' + i)), truth: string(rune('A' + i))}
	}
	a := stridePairs(pairs, 5, 42)
	b := stridePairs(pairs, 5, 42)
	if len(a) != 5 {
		t.Fatalf("stridePairs returned %d items, want 5", len(a))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("stridePairs is not deterministic at %d: %v vs %v", i, a[i], b[i])
		}
	}
	if got := stridePairs(pairs[:3], 5, 42); len(got) != 3 {
		t.Fatalf("stridePairs with n>len should return all, got %d", len(got))
	}
}

func TestRankOf(t *testing.T) {
	results := []SuggestLinkResult{{Path: "a.md"}, {Path: "b.md"}, {Path: "c.md"}, {Path: "d.md"}}
	if r := rankOf(results, "b.md", 3); r != 2 {
		t.Fatalf("rankOf b.md = %d, want 2", r)
	}
	if r := rankOf(results, "d.md", 3); r != 0 {
		t.Fatalf("rankOf beyond k should be 0, got %d", r)
	}
	if r := rankOf(results, "zz.md", 4); r != 0 {
		t.Fatalf("rankOf missing should be 0, got %d", r)
	}
}
