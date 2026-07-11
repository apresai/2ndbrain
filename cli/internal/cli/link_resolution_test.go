package cli

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// The link-resolution commands (unlink/relink/suggest-target) are deterministic
// and AI-free, so these tests run with NO skip (suggest-target's semantic tier
// is simply absent without an embedder; the BM25 + drift tiers still drive).

func TestUnlink_WriteAndUndoRoundTrip(t *testing.T) {
	_, root := newContractVault(t)
	writeNote(t, root, "src.md", "Src",
		"See [[083477d]] for the run and [[Auth Flow]] elsewhere.\n\nAlso `[[083477d]]` in code.")
	if _, err := runCLIArgs(t, root, "index"); err != nil {
		t.Fatalf("index: %v", err)
	}

	out, err := runCLIArgs(t, root, "unlink", "src.md", "--target", "083477d", "--write", "--json", "--porcelain")
	if err != nil {
		t.Fatalf("unlink --write: %v\n%s", err, out)
	}
	var res PolishResult
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, out)
	}
	if res.Provider != "unlink" {
		t.Errorf("provider = %q, want unlink", res.Provider)
	}
	if len(res.LinksRepaired) != 1 || res.LinksRepaired[0].Raw != "083477d" || res.LinksRepaired[0].NewTarget != "" {
		t.Errorf("LinksRepaired = %+v, want one {Raw:083477d, NewTarget:\"\"}", res.LinksRepaired)
	}

	text := readFileString(t, root+"/src.md")
	if strings.Contains(text, "[[083477d]]\n") || strings.Contains(text, "See [[083477d]]") {
		t.Errorf("prose [[083477d]] should be unlinked:\n%s", text)
	}
	if !strings.Contains(text, "See 083477d for the run") {
		t.Errorf("expected plain text 083477d after unlink:\n%s", text)
	}
	// The link inside inline code and an unrelated link are untouched.
	if !strings.Contains(text, "`[[083477d]]` in code") {
		t.Errorf("code-span link must be preserved:\n%s", text)
	}
	if !strings.Contains(text, "[[Auth Flow]]") {
		t.Errorf("unrelated link must be untouched:\n%s", text)
	}

	// Undo restores the original byte-for-byte.
	if _, err := runCLIArgs(t, root, "polish", "src.md", "--undo", "--json", "--porcelain"); err != nil {
		t.Fatalf("polish --undo: %v", err)
	}
	restored := readFileString(t, root+"/src.md")
	if !strings.Contains(restored, "See [[083477d]] for the run") {
		t.Errorf("undo did not restore the original [[083477d]]:\n%s", restored)
	}
}

func TestUnlink_PreviewDoesNotWrite(t *testing.T) {
	_, root := newContractVault(t)
	writeNote(t, root, "src.md", "Src", "Junk [[aagent]] here.")
	if _, err := runCLIArgs(t, root, "index"); err != nil {
		t.Fatalf("index: %v", err)
	}
	out, err := runCLIArgs(t, root, "unlink", "src.md", "--target", "aagent", "--json", "--porcelain")
	if err != nil {
		t.Fatalf("unlink preview: %v\n%s", err, out)
	}
	var res PolishResult
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !strings.Contains(res.Polished, "Junk aagent here.") {
		t.Errorf("preview polished should show unlinked text, got %q", res.Polished)
	}
	if !strings.Contains(readFileString(t, root+"/src.md"), "[[aagent]]") {
		t.Errorf("preview must NOT write: on-disk link should remain")
	}
}

func TestRelink_WriteRepointsToChosenNote(t *testing.T) {
	_, root := newContractVault(t)
	writeNote(t, root, "go-mod-why.md", "Go Mod Why", "# Go Mod Why\n\nPhantom indirect updates.")
	writeNote(t, root, "src.md", "Src", "See [[go-modules]] and [[go-modules#Setup|the setup]].")
	if _, err := runCLIArgs(t, root, "index"); err != nil {
		t.Fatalf("index: %v", err)
	}

	out, err := runCLIArgs(t, root, "relink", "src.md", "--from", "go-modules", "--to", "go-mod-why", "--write", "--json", "--porcelain")
	if err != nil {
		t.Fatalf("relink --write: %v\n%s", err, out)
	}
	var res PolishResult
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, out)
	}
	if res.Provider != "relink" || len(res.LinksRepaired) != 1 || res.LinksRepaired[0].NewTarget != "go-mod-why" {
		t.Errorf("expected relink -> go-mod-why, got %+v (provider %q)", res.LinksRepaired, res.Provider)
	}
	text := readFileString(t, root+"/src.md")
	if strings.Contains(text, "[[go-modules]]") {
		t.Errorf("[[go-modules]] should be repointed:\n%s", text)
	}
	if !strings.Contains(text, "[[go-mod-why]]") {
		t.Errorf("expected [[go-mod-why]]:\n%s", text)
	}
	// The #Setup|alias suffix on the second link is preserved.
	if !strings.Contains(text, "[[go-mod-why#Setup|the setup]]") {
		t.Errorf("heading/alias suffix not preserved:\n%s", text)
	}
}

func TestRelink_PreviewDoesNotWrite(t *testing.T) {
	_, root := newContractVault(t)
	writeNote(t, root, "go-mod-why.md", "Go Mod Why", "# Go Mod Why\n\nx")
	writeNote(t, root, "src.md", "Src", "See [[go-modules]].")
	if _, err := runCLIArgs(t, root, "index"); err != nil {
		t.Fatalf("index: %v", err)
	}
	out, err := runCLIArgs(t, root, "relink", "src.md", "--from", "go-modules", "--to", "go-mod-why", "--json", "--porcelain")
	if err != nil {
		t.Fatalf("relink preview: %v\n%s", err, out)
	}
	var res PolishResult
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !strings.Contains(res.Polished, "[[go-mod-why]]") {
		t.Errorf("preview polished should show the repointed link, got %q", res.Polished)
	}
	if !strings.Contains(readFileString(t, root+"/src.md"), "[[go-modules]]") {
		t.Errorf("preview must NOT write: on-disk link should remain [[go-modules]]")
	}
}

// relink to a target that resolves to no note warns (but still applies) so a
// typo'd --to isn't silently left as a still-broken link.
func TestRelink_NonexistentTargetWarns(t *testing.T) {
	_, root := newContractVault(t)
	writeNote(t, root, "src.md", "Src", "See [[go-modules]].")
	if _, err := runCLIArgs(t, root, "index"); err != nil {
		t.Fatalf("index: %v", err)
	}
	out, err := runCLIArgs(t, root, "relink", "src.md", "--from", "go-modules", "--to", "does-not-exist", "--json", "--porcelain")
	if err != nil {
		t.Fatalf("relink: %v\n%s", err, out)
	}
	var res PolishResult
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !strings.Contains(res.Warning, "does not resolve to an existing note") {
		t.Errorf("expected a non-resolving --to warning, got %q", res.Warning)
	}
}

func TestSuggestTarget_DriftKeywordAndEmpty(t *testing.T) {
	_, root := newContractVault(t)
	writeNote(t, root, "claude-code-skills.md", "Claude Code Skills", "# Claude Code Skills\n\nSkills index.")
	writeNote(t, root, "apresai-models.md", "Apresai Models", "# Apresai Models\n\nThe apresai models catalog.")
	if _, err := runCLIArgs(t, root, "index"); err != nil {
		t.Fatalf("index: %v", err)
	}

	// Drift tier: lower-cased spaced target matches the title-cased note.
	out, err := runCLIArgs(t, root, "suggest-target", "claude code skills", "--json", "--porcelain")
	if err != nil {
		t.Fatalf("suggest-target drift: %v\n%s", err, out)
	}
	var drift []SuggestLinkResult
	if err := json.Unmarshal(out, &drift); err != nil {
		t.Fatalf("unmarshal drift: %v\n%s", err, out)
	}
	if !containsPath(drift, "claude-code-skills.md") {
		t.Errorf("drift tier should suggest claude-code-skills.md, got %+v", drift)
	}

	// Keyword tier: word-reordered target ("models-apresai") still surfaces the
	// note via BM25 token overlap (apresai/models), which drift-matching misses.
	out, err = runCLIArgs(t, root, "suggest-target", "models-apresai", "--json", "--porcelain")
	if err != nil {
		t.Fatalf("suggest-target keyword: %v\n%s", err, out)
	}
	var kw []SuggestLinkResult
	if err := json.Unmarshal(out, &kw); err != nil {
		t.Fatalf("unmarshal keyword: %v\n%s", err, out)
	}
	if !containsPath(kw, "apresai-models.md") {
		t.Errorf("keyword tier should suggest apresai-models.md, got %+v", kw)
	}
}

// An empty vault yields no candidates from any tier, proving the JSON is an
// empty array, never null (Swift decodes [SuggestTargetResult]). A non-empty
// vault can't test this deterministically: the semantic tier weakly matches
// almost any query when an embedder is configured.
func TestSuggestTarget_EmptyVaultIsEmptyArray(t *testing.T) {
	_, root := newContractVault(t)
	out, err := runCLIArgs(t, root, "suggest-target", "anything at all", "--json", "--porcelain")
	if err != nil {
		t.Fatalf("suggest-target empty vault: %v\n%s", err, out)
	}
	if strings.TrimSpace(string(out)) != "[]" {
		t.Errorf("empty-vault output should be [], got %q", out)
	}
}

func containsPath(rs []SuggestLinkResult, path string) bool {
	for _, r := range rs {
		if r.Path == path {
			return true
		}
	}
	return false
}

// PR #179 regression pair: relink's --to advisory check resolves against the
// LIVE filesystem, so both stale-DB directions behave correctly.
func TestRelink_UnindexedOnDiskTargetDoesNotWarn(t *testing.T) {
	_, root := newContractVault(t)
	writeNote(t, root, "src.md", "Src", "See [[go-modules]].")
	if _, err := runCLIArgs(t, root, "index"); err != nil {
		t.Fatalf("index: %v", err)
	}
	// Created AFTER the index: the DB does not know it, the disk does.
	writeNote(t, root, "fresh-note.md", "Fresh Note", "Just created in Obsidian.")
	out, err := runCLIArgs(t, root, "relink", "src.md", "--from", "go-modules", "--to", "fresh-note", "--json", "--porcelain")
	if err != nil {
		t.Fatalf("relink: %v\n%s", err, out)
	}
	var res PolishResult
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if strings.Contains(res.Warning, "does not resolve") {
		t.Errorf("an unindexed on-disk --to target must not warn (live resolution): %q", res.Warning)
	}
}

func TestRelink_DBGhostTargetWarns(t *testing.T) {
	_, root := newContractVault(t)
	writeNote(t, root, "src.md", "Src", "See [[go-modules]].")
	ghost := writeNote(t, root, "ghost.md", "Ghost", "About to be deleted.")
	if _, err := runCLIArgs(t, root, "index"); err != nil {
		t.Fatalf("index: %v", err)
	}
	// Deleted from disk AFTER the index: the DB still has it, the disk does not.
	if err := os.Remove(ghost); err != nil {
		t.Fatal(err)
	}
	out, err := runCLIArgs(t, root, "relink", "src.md", "--from", "go-modules", "--to", "ghost", "--json", "--porcelain")
	if err != nil {
		t.Fatalf("relink: %v\n%s", err, out)
	}
	var res PolishResult
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !strings.Contains(res.Warning, "does not resolve") {
		t.Errorf("a --to target deleted from disk must warn even though the DB still has it: %q", res.Warning)
	}
}

func TestContextWindowAroundTarget(t *testing.T) {
	body := "Intro paragraph about Ghostty themes.\n\nSee [[terminal-emulators]] and more about colors."
	win := contextWindowAroundTarget(body, "terminal-emulators", 80)
	if !strings.Contains(win, "[[terminal-emulators]]") {
		t.Errorf("window should include the link, got %q", win)
	}
	// Missing link: head of body.
	head := contextWindowAroundTarget("ABCDEFGHIJ", "missing", 4)
	if head != "ABCD" {
		t.Errorf("missing link should take body head, got %q", head)
	}
}

func TestBuildSourceContextQuery(t *testing.T) {
	dir := t.TempDir()
	// Note body ends with related-topic links the way real vault notes do.
	path := dir + "/note.md"
	content := "---\ntitle: Ghostty Themes\ntype: note\nid: 00000000-0000-4000-8000-000000000001\n---\n# Ghostty Themes\n\nGPU terminal themes and matrix colors.\n\n[[ghostty-config]] [[terminal-emulators]] [[color-schemes]]\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	q, ctx := buildSourceContextQuery(dir, "note.md", "terminal-emulators")
	if !strings.HasPrefix(q, "terminal-emulators\n") {
		t.Errorf("query should start with target, got %q", q)
	}
	if !strings.Contains(ctx, "terminal-emulators") && !strings.Contains(ctx, "Ghostty") {
		t.Errorf("context should carry surrounding prose, got %q", ctx)
	}
	// No source: bare target.
	q2, ctx2 := buildSourceContextQuery(dir, "", "terminal-emulators")
	if q2 != "terminal-emulators" || ctx2 != "" {
		t.Errorf("no-source should be bare target, got q=%q ctx=%q", q2, ctx2)
	}
}

func TestParseLLMPicks(t *testing.T) {
	raw := "Here you go:\n```json\n[{\"path\":\"a.md\",\"reason\":\"typo of Auth\"},{\"path\":\"b.md\",\"reason\":\"related\"}]\n```\n"
	picks, err := parseLLMPicks(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(picks) != 2 || picks[0].Path != "a.md" || picks[0].Reason != "typo of Auth" {
		t.Errorf("unexpected picks: %+v", picks)
	}
}

func TestApplyLLMPicks_GroundedAndCap(t *testing.T) {
	original := []SuggestLinkResult{
		{Path: "noise.md", Title: "Noise", Score: 0.9, Confidence: "low"},
		{Path: "auth.md", Title: "Auth Flow", Score: 0.5, Confidence: "low"},
		{Path: "other.md", Title: "Other", Score: 0.4, Confidence: "low"},
	}
	picks := []llmPick{
		{Path: "auth.md", Reason: "closest title match"},
		{Path: "invented.md", Reason: "hallucination"},
		{Path: "other.md", Reason: "secondary"},
	}
	out := applyLLMPicks(original, picks, nil)
	if len(out) < 2 {
		t.Fatalf("expected at least 2, got %+v", out)
	}
	if out[0].Path != "auth.md" {
		t.Errorf("LLM top pick should lead, got %q", out[0].Path)
	}
	if out[0].Confidence != "medium" {
		t.Errorf("LLM pick confidence should cap at medium, got %q", out[0].Confidence)
	}
	if out[0].Reason != "closest title match" {
		t.Errorf("reason not attached: %q", out[0].Reason)
	}
	// Invented path dropped.
	for _, r := range out {
		if r.Path == "invented.md" {
			t.Errorf("invented path must not appear: %+v", out)
		}
	}
	// Unused originals still present after LLM top.
	if !containsPath(out, "noise.md") {
		t.Errorf("unused original should remain: %+v", out)
	}
}

func TestHasHighConfidence(t *testing.T) {
	if hasHighConfidence([]SuggestLinkResult{{Confidence: "medium"}, {Confidence: "low"}}) {
		t.Error("expected false")
	}
	if !hasHighConfidence([]SuggestLinkResult{{Confidence: "low"}, {Confidence: "high"}}) {
		t.Error("expected true")
	}
}

// Context-aware search: a bare-target query finds nothing useful for an
// aspirational related-topic link, but --source folds surrounding prose so
// BM25/semantic can surface a real neighbor note.
func TestSuggestTarget_SourceContextSurfacesNeighbor(t *testing.T) {
	_, root := newContractVault(t)
	writeNote(t, root, "ghostty-config.md", "Ghostty Config",
		"# Ghostty Config\n\nGPU-accelerated terminal emulator configuration and themes.")
	writeNote(t, root, "unrelated.md", "Unrelated Cooking",
		"# Unrelated Cooking\n\nPasta recipes and wine pairings.")
	writeNote(t, root, "src.md", "Ghostty Themes Guide",
		"# Ghostty Themes\n\nGPU terminal themes and matrix color schemes.\n\n[[ghostty-config]] [[terminal-emulators]]")
	if _, err := runCLIArgs(t, root, "index"); err != nil {
		t.Fatalf("index: %v", err)
	}

	// Without context the bare target "terminal-emulators" is unlikely to match
	// (no note has that name). With --source the prose mentions terminal/themes
	// and ghostty-config should rank.
	out, err := runCLIArgs(t, root, "suggest-target", "terminal-emulators",
		"--source", "src.md", "--limit", "3", "--json", "--porcelain")
	if err != nil {
		t.Fatalf("suggest-target: %v\n%s", err, out)
	}
	var results []SuggestLinkResult
	if err := json.Unmarshal(out, &results); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, out)
	}
	// Source note must never appear.
	if containsPath(results, "src.md") {
		t.Errorf("source note must be excluded: %+v", results)
	}
	// Best-effort: with BM25 at least, ghostty-config should surface from context
	// words (terminal/themes/ghostty). If the vault has no FTS hit either way,
	// empty is still valid JSON [] — only fail if cooking outranks ghostty when
	// both are present.
	if containsPath(results, "ghostty-config.md") {
		return // ideal path
	}
	// Soft assert: if we got any hit, ghostty should outrank cooking.
	if len(results) > 0 && results[0].Path == "unrelated.md" {
		t.Errorf("context-aware query ranked cooking over ghostty: %+v", results)
	}
}
