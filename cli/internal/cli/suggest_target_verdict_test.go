package cli

// Tests for the --verdict recommendation envelope: the confidence-first
// candidate ordering, the pure recommendation rules, and the CLI-level
// envelope shape. All offline (drift + BM25 tiers only; llm stays "off").

import (
	"encoding/json"
	"testing"
)

func TestSortSuggestionsByConfidence(t *testing.T) {
	// A later-tier high-confidence candidate must sort above earlier low ones
	// (the bug where the GUI's candidates[0] check missed non-first highs),
	// ties break by score desc, and equal keys keep their add order (stable).
	in := []SuggestLinkResult{
		{Path: "low-drift.md", Confidence: "low", Score: 1.0},
		{Path: "medium-a.md", Confidence: "medium", Score: 0.3},
		{Path: "high-bm25.md", Confidence: "high", Score: 2.1},
		{Path: "medium-b.md", Confidence: "medium", Score: 0.9},
		{Path: "unknown.md", Confidence: "", Score: 9.9},
	}
	sortSuggestionsByConfidence(in)
	want := []string{"high-bm25.md", "medium-b.md", "medium-a.md", "low-drift.md", "unknown.md"}
	for i, w := range want {
		if in[i].Path != w {
			t.Fatalf("position %d = %s, want %s (full: %+v)", i, in[i].Path, w, in)
		}
	}

	// Stability: equal (confidence, score) keys keep their add order, so the
	// drift-before-semantic-before-BM25 tie-break survives the sort.
	ties := []SuggestLinkResult{
		{Path: "first-added.md", Confidence: "medium", Score: 0.5},
		{Path: "second-added.md", Confidence: "medium", Score: 0.5},
		{Path: "third-added.md", Confidence: "medium", Score: 0.5},
	}
	sortSuggestionsByConfidence(ties)
	for i, w := range []string{"first-added.md", "second-added.md", "third-added.md"} {
		if ties[i].Path != w {
			t.Fatalf("stability violated at %d: got %s, want %s", i, ties[i].Path, w)
		}
	}
}

func TestComputeSuggestRecommendation(t *testing.T) {
	high := SuggestLinkResult{Path: "a.md", Title: "A", Confidence: "high", Reason: "same note"}
	medium := SuggestLinkResult{Path: "b.md", Title: "B", Confidence: "medium"}
	low := SuggestLinkResult{Path: "c.md", Title: "C", Confidence: "low"}

	cases := []struct {
		name       string
		results    []SuggestLinkResult
		llm        string
		wantAction string
		wantTo     string
	}{
		{"high top -> relink", []SuggestLinkResult{high, medium}, llmOutcomeSkipped, "relink", "a.md"},
		{"medium top -> relink", []SuggestLinkResult{medium, low}, llmOutcomeOff, "relink", "b.md"},
		{"llm declined overrides medium", []SuggestLinkResult{medium, low}, llmOutcomeDeclined, "unlink", ""},
		{"all low -> unlink", []SuggestLinkResult{low}, llmOutcomeOff, "unlink", ""},
		{"empty -> unlink", nil, llmOutcomeOff, "unlink", ""},
		{"llm error keeps medium relink (fail-closed)", []SuggestLinkResult{medium}, llmOutcomeError, "relink", "b.md"},
		{"promoted medium -> relink with reason", []SuggestLinkResult{{Path: "d.md", Confidence: "medium", Reason: "picked"}}, llmOutcomePromoted, "relink", "d.md"},
	}
	for _, c := range cases {
		got := computeSuggestRecommendation(c.results, c.llm)
		if got.Action != c.wantAction || got.To != c.wantTo {
			t.Errorf("%s: got %+v, want action=%s to=%s", c.name, got, c.wantAction, c.wantTo)
		}
	}
	// The recommended candidate's confidence and reason ride along for the GUI.
	got := computeSuggestRecommendation([]SuggestLinkResult{high}, llmOutcomeSkipped)
	if got.Confidence != "high" || got.Reason != "same note" || got.Title != "A" {
		t.Errorf("relink recommendation should carry confidence/title/reason: %+v", got)
	}
}

func TestSuggestTarget_VerdictEnvelope_Relink(t *testing.T) {
	root, source := seedSuggestVault(t)
	out, err := runCLIArgs(t, root, "suggest-target", "ghostty", "--source", source, "--verdict", "--json", "--porcelain")
	if err != nil {
		t.Fatalf("suggest-target --verdict: %v\n%s", err, out)
	}
	var env SuggestTargetEnvelope
	if err := json.Unmarshal(out, &env); err != nil {
		t.Fatalf("unmarshal envelope: %v\n%s", err, out)
	}
	if env.LLM != llmOutcomeOff {
		t.Errorf("llm = %q, want %q (no --llm passed)", env.LLM, llmOutcomeOff)
	}
	if env.Recommendation.Action != "relink" || env.Recommendation.To != "Ghostty Config.md" {
		t.Errorf("expected a relink recommendation to Ghostty Config.md, got %+v", env.Recommendation)
	}
	if len(env.Candidates) == 0 {
		t.Errorf("envelope should carry the candidate list")
	}
	// The first candidate is the recommended one (ordering invariant).
	if env.Candidates[0].Path != env.Recommendation.To {
		t.Errorf("candidates[0] (%s) should match the recommendation (%s)", env.Candidates[0].Path, env.Recommendation.To)
	}
}

func TestSuggestTarget_VerdictLLMSkippedOffline(t *testing.T) {
	// --llm with a high-confidence deterministic candidate short-circuits the
	// model entirely (no generator is even resolved), so this runs offline and
	// pins the "skipped" outcome: the one llm state reachable without a
	// network call. seedSuggestVault's [[ghostty]] resolves high via drift.
	root, source := seedSuggestVault(t)
	out, err := runCLIArgs(t, root, "suggest-target", "ghostty", "--source", source, "--llm", "--verdict", "--json", "--porcelain")
	if err != nil {
		t.Fatalf("suggest-target --llm --verdict: %v\n%s", err, out)
	}
	var env SuggestTargetEnvelope
	if err := json.Unmarshal(out, &env); err != nil {
		t.Fatalf("unmarshal envelope: %v\n%s", err, out)
	}
	if env.LLM != llmOutcomeSkipped {
		t.Errorf("llm = %q, want %q (high-confidence candidate short-circuits the model)", env.LLM, llmOutcomeSkipped)
	}
	if env.Recommendation.Action != "relink" || env.Recommendation.Confidence != "high" {
		t.Errorf("expected a high relink recommendation, got %+v", env.Recommendation)
	}
}

func TestSuggestTarget_VerdictEnvelope_UnlinkOnNoMatch(t *testing.T) {
	root, source := seedSuggestVault(t)
	out, err := runCLIArgs(t, root, "suggest-target", "zz-quarterly-okr-cycle-1999", "--source", source, "--verdict", "--json", "--porcelain")
	if err != nil {
		t.Fatalf("suggest-target --verdict: %v\n%s", err, out)
	}
	var env SuggestTargetEnvelope
	if err := json.Unmarshal(out, &env); err != nil {
		t.Fatalf("unmarshal envelope: %v\n%s", err, out)
	}
	if env.Recommendation.Action != "unlink" {
		t.Errorf("nothing >= medium should recommend unlink, got %+v (candidates %+v)", env.Recommendation, env.Candidates)
	}
}

func TestSuggestTarget_BareArrayUnchangedWithoutVerdict(t *testing.T) {
	root, source := seedSuggestVault(t)
	out, err := runCLIArgs(t, root, "suggest-target", "ghostty", "--source", source, "--json", "--porcelain")
	if err != nil {
		t.Fatalf("suggest-target: %v\n%s", err, out)
	}
	// Back-compat: without --verdict the output must still be a bare array.
	var results []SuggestLinkResult
	if err := json.Unmarshal(out, &results); err != nil {
		t.Fatalf("without --verdict the JSON must remain a bare array: %v\n%s", err, out)
	}
}
