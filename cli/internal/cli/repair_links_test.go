package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// repair-links is deterministic and AI-free, so these tests run with NO skip
// (unlike polish_write_test.go which needs Bedrock).

// seedRepairVault creates two target notes ("Auth Flow", "JWT Tokens") plus a
// source note that links to a case-drifted target, an exact one, and a
// nonexistent one, then indexes. Returns the vault root and source filename.
func seedRepairVault(t *testing.T) (root, source string) {
	t.Helper()
	_, root = newContractVault(t)
	writeNote(t, root, "Auth Flow.md", "Auth Flow", "# Auth Flow\n\nHow auth works.")
	writeNote(t, root, "JWT Tokens.md", "JWT Tokens", "# JWT Tokens\n\nToken details.")
	source = "source-doc.md"
	writeNote(t, root, source, "Source Doc",
		"See [[auth flow]] and [[JWT Tokens]].\n\nAlso [[Nonexistent Topic]].")
	if _, err := runCLIArgs(t, root, "index"); err != nil {
		t.Fatalf("index: %v", err)
	}
	return root, source
}

func TestRepairLinks_WriteAndUndoRoundTrip(t *testing.T) {
	root, source := seedRepairVault(t)
	srcPath := filepath.Join(root, source)
	originalBytes, _ := os.ReadFile(srcPath)

	out, err := runCLIArgs(t, root, "repair-links", source, "--write", "--json", "--porcelain")
	if err != nil {
		t.Fatalf("repair-links --write: %v\n%s", err, out)
	}
	var res PolishResult
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, out)
	}
	if res.Provider != "repair-links" {
		t.Errorf("provider = %q, want repair-links", res.Provider)
	}
	if len(res.LinksRepaired) != 1 || res.LinksRepaired[0].Raw != "auth flow" || res.LinksRepaired[0].NewTarget != "Auth Flow" {
		t.Errorf("LinksRepaired = %+v, want one auth flow -> Auth Flow", res.LinksRepaired)
	}
	foundSkip := false
	for _, s := range res.LinksSkipped {
		if s.Raw == "Nonexistent Topic" && s.Reason == "no_match" {
			foundSkip = true
		}
	}
	if !foundSkip {
		t.Errorf("LinksSkipped should report Nonexistent Topic as no_match: %+v", res.LinksSkipped)
	}

	// On disk: [[auth flow]] became [[Auth Flow]]; the resolving and nonexistent
	// links are untouched; frontmatter intact.
	text := readFileString(t, srcPath)
	if strings.Contains(text, "[[auth flow]]") {
		t.Errorf("broken [[auth flow]] should be repaired:\n%s", text)
	}
	if !strings.Contains(text, "[[Auth Flow]]") {
		t.Errorf("expected [[Auth Flow]] after repair:\n%s", text)
	}
	if !strings.Contains(text, "[[JWT Tokens]]") || !strings.Contains(text, "[[Nonexistent Topic]]") {
		t.Errorf("non-target links should be untouched:\n%s", text)
	}
	if !strings.Contains(text, "title: Source Doc") {
		t.Errorf("frontmatter lost:\n%s", text)
	}

	// Undo via the shared polish snapshot slot restores the original byte-for-byte.
	undoOut, err := runCLIArgs(t, root, "polish", source, "--undo", "--json")
	if err != nil {
		t.Fatalf("polish --undo: %v\n%s", err, undoOut)
	}
	var undo PolishUndoResult
	if err := json.Unmarshal(undoOut, &undo); err != nil {
		t.Fatalf("unmarshal undo: %v\n%s", err, undoOut)
	}
	if !undo.Reverted {
		t.Errorf("undo should report reverted=true: %+v", undo)
	}
	if got := readFileString(t, srcPath); got != string(originalBytes) {
		t.Errorf("undo did not restore original:\nwant %q\ngot  %q", string(originalBytes), got)
	}
}

func TestRepairLinks_PreviewLeavesFileUntouched(t *testing.T) {
	root, source := seedRepairVault(t)
	srcPath := filepath.Join(root, source)
	before := readFileString(t, srcPath)

	out, err := runCLIArgs(t, root, "repair-links", source, "--json", "--porcelain")
	if err != nil {
		t.Fatalf("repair-links preview: %v\n%s", err, out)
	}
	var res PolishResult
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, out)
	}
	if len(res.LinksRepaired) != 1 {
		t.Errorf("preview should still report the repairable link: %+v", res.LinksRepaired)
	}
	if got := readFileString(t, srcPath); got != before {
		t.Errorf("preview must not write the file")
	}
}

func TestRepairLinks_TargetScoping(t *testing.T) {
	_, root := newContractVault(t)
	writeNote(t, root, "Auth Flow.md", "Auth Flow", "# Auth Flow")
	writeNote(t, root, "JWT Tokens.md", "JWT Tokens", "# JWT Tokens")
	source := "src.md"
	writeNote(t, root, source, "Src", "See [[auth flow]] and [[jwt tokens]].")
	if _, err := runCLIArgs(t, root, "index"); err != nil {
		t.Fatalf("index: %v", err)
	}

	out, err := runCLIArgs(t, root, "repair-links", source, "--write", "--target", "auth flow", "--json", "--porcelain")
	if err != nil {
		t.Fatalf("repair-links --target: %v\n%s", err, out)
	}
	var res PolishResult
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, out)
	}
	if len(res.LinksRepaired) != 1 || res.LinksRepaired[0].Raw != "auth flow" {
		t.Errorf("only the targeted link should be repaired: %+v", res.LinksRepaired)
	}
	text := readFileString(t, filepath.Join(root, source))
	if !strings.Contains(text, "[[Auth Flow]]") {
		t.Errorf("targeted link should be repaired:\n%s", text)
	}
	if !strings.Contains(text, "[[jwt tokens]]") {
		t.Errorf("untargeted broken link must be left alone:\n%s", text)
	}
}

// A --target value is taken verbatim, never comma-split: a target containing a
// comma must not be torn into two names (which would skip the real link and
// could repair unrelated ones). Guards the StringArray (not StringSlice) flag.
func TestRepairLinks_TargetNotCommaSplit(t *testing.T) {
	_, root := newContractVault(t)
	writeNote(t, root, "Alpha.md", "Alpha", "# Alpha")
	writeNote(t, root, "Beta.md", "Beta", "# Beta")
	source := "src.md"
	writeNote(t, root, source, "Src", "See [[alpha]] and [[beta]].")
	if _, err := runCLIArgs(t, root, "index"); err != nil {
		t.Fatalf("index: %v", err)
	}

	// "alpha,beta" is one literal target that matches no note. If the flag
	// comma-split it, it would (wrongly) repair both [[alpha]] and [[beta]].
	out, err := runCLIArgs(t, root, "repair-links", source, "--write", "--target", "alpha,beta", "--json", "--porcelain")
	if err != nil {
		t.Fatalf("repair-links: %v\n%s", err, out)
	}
	var res PolishResult
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, out)
	}
	if len(res.LinksRepaired) != 0 {
		t.Errorf("a comma-containing target must not be split and repair real links: %+v", res.LinksRepaired)
	}
	text := readFileString(t, filepath.Join(root, source))
	if !strings.Contains(text, "[[alpha]]") || !strings.Contains(text, "[[beta]]") {
		t.Errorf("neither link should have been repaired:\n%s", text)
	}
}

func TestRepairLinks_RejectsReadOnlyType(t *testing.T) {
	_, root := newContractVault(t)
	canvas := "board.canvas"
	if err := os.WriteFile(filepath.Join(root, canvas), []byte(`{"nodes":[],"edges":[]}`), 0o644); err != nil {
		t.Fatalf("write canvas: %v", err)
	}
	if _, err := runCLIArgs(t, root, "index"); err != nil {
		t.Fatalf("index: %v", err)
	}
	before := readFileString(t, filepath.Join(root, canvas))

	_, err := runCLIArgs(t, root, "repair-links", canvas, "--write", "--json", "--porcelain")
	if err == nil {
		t.Fatal("repair-links on a .canvas should fail")
	}
	if got := readFileString(t, filepath.Join(root, canvas)); got != before {
		t.Errorf("rejected repair must not touch the file")
	}
}

func TestRepairLinks_NoBrokenLinksIsNoOp(t *testing.T) {
	_, root := newContractVault(t)
	source := "clean.md"
	writeNote(t, root, source, "Clean", "No links here at all.")
	if _, err := runCLIArgs(t, root, "index"); err != nil {
		t.Fatalf("index: %v", err)
	}
	before := readFileString(t, filepath.Join(root, source))

	out, err := runCLIArgs(t, root, "repair-links", source, "--write", "--json", "--porcelain")
	if err != nil {
		t.Fatalf("repair-links: %v\n%s", err, out)
	}
	var res PolishResult
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, out)
	}
	if len(res.LinksRepaired) != 0 {
		t.Errorf("no repairs expected: %+v", res.LinksRepaired)
	}
	if got := readFileString(t, filepath.Join(root, source)); got != before {
		t.Errorf("no-op repair must not rewrite the file")
	}
}

func readFileString(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}
