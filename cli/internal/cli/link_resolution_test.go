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
