package cli

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// captureStderr runs fn with os.Stderr redirected and returns what it wrote.
// runCLIArgs captures stdout only; the empty-state hints and the move summary
// go to stderr on purpose, and that is exactly the text these tests are about.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stderr
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = pw
	done := make(chan []byte, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, pr)
		done <- buf.Bytes()
	}()
	fn()
	pw.Close()
	os.Stderr = orig
	return string(<-done)
}

func vaultDigest(t *testing.T, root string) string {
	t.Helper()
	var names []string
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(p, ".md") {
			names = append(names, p)
		}
		return nil
	})
	sort.Strings(names)
	h := sha256.New()
	for _, n := range names {
		b, _ := os.ReadFile(n)
		h.Write([]byte(n))
		h.Write(b)
	}
	return string(h.Sum(nil))
}

// B. The bare and --porcelain forms of the listing commands render JSON
// (output.Write's default branch), so they owe the same [] rather than null
// that the explicit --json form was given in 0.21.1.
func TestContract_BareAndPorcelainListingsAreEmptyArrays(t *testing.T) {
	_, root := newContractVault(t)
	// unresolved is not here: its bare form is a human message, not JSON.
	for _, cmd := range []string{"tags", "aliases", "orphans", "deadends", "stale"} {
		for _, extra := range [][]string{nil, {"--porcelain"}} {
			label := cmd
			if extra != nil {
				label += " --porcelain"
			}
			t.Run(label, func(t *testing.T) {
				out, err := runCLIArgs(t, root, append([]string{cmd}, extra...)...)
				if err != nil {
					t.Fatalf("%s: %v\n%s", label, err, out)
				}
				var rows *[]any
				if err := json.Unmarshal(out, &rows); err != nil {
					t.Fatalf("%s is not JSON: %v\n%q", label, err, out)
				}
				if rows == nil {
					t.Fatalf("%s printed null on an empty vault, want []", label)
				}
			})
		}
	}
}

// C. A nested list field is subject to the same contract.
func TestContract_LintIssuesIsEmptyArrayNotNull(t *testing.T) {
	_, root := newContractVault(t)
	out, err := runCLIArgs(t, root, "lint", "--json")
	if err != nil {
		t.Fatalf("lint: %v\n%s", err, out)
	}
	var rep struct {
		Issues *[]any `json:"issues"`
	}
	if err := json.Unmarshal(out, &rep); err != nil {
		t.Fatalf("parse: %v\n%s", err, out)
	}
	if rep.Issues == nil {
		t.Fatalf("lint --json on a clean vault has issues: null; `jq '.issues[]'` fails on it: %s", out)
	}
}

// D. A refused move must not read as a completed one, in JSON or in prose.
func TestContract_RefusedMoveIsReportedAsRefused(t *testing.T) {
	_, root := newContractVault(t)
	write := func(rel, body string) {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("a/dup.md", "---\ntitle: Dup\ntype: note\nstatus: draft\n---\nA\n")
	write("b/dup.md", "---\ntitle: Dup\ntype: note\nstatus: draft\n---\nB\n")
	write("ref.md", "---\ntitle: Ref\ntype: note\nstatus: draft\n---\nsee [[dup]]\n")
	if out, err := runCLIArgs(t, root, "index"); err != nil {
		t.Fatalf("index: %v\n%s", err, out)
	}
	before := vaultDigest(t, root)

	var out []byte
	var err error
	stderr := captureStderr(t, func() {
		out, err = runCLIArgs(t, root, "move", "a/dup.md", "a/renamed.md", "--json")
	})
	if err == nil {
		t.Fatal("an ambiguous non-force move must be refused with an error")
	}
	var res struct {
		Refused bool `json:"refused"`
		DryRun  bool `json:"dry_run"`
	}
	// The in-process harness routes cobra's error line into the same capture as
	// stdout, so decode exactly one JSON value and ignore what follows it.
	if jerr := json.NewDecoder(bytes.NewReader(out)).Decode(&res); jerr != nil {
		t.Fatalf("move --json output is not JSON: %v\n%s", jerr, out)
	}
	if !res.Refused {
		t.Errorf("JSON does not carry refused: true; a machine consumer cannot tell this from a completed move: %s", out)
	}
	if strings.Contains(stderr, "Moved:") || strings.Contains(stderr, "Rewrote") {
		t.Errorf("stderr describes a completed move in the past tense on a refusal:\n%s", stderr)
	}
	if vaultDigest(t, root) != before {
		t.Error("a refused move changed files on disk")
	}
	if _, serr := os.Stat(filepath.Join(root, "a", "dup.md")); serr != nil {
		t.Error("the source file is gone after a refused move")
	}

	// --dry-run is a preview, not a refusal.
	out, err = runCLIArgs(t, root, "move", "a/dup.md", "a/renamed.md", "--dry-run", "--json")
	if err != nil {
		t.Fatalf("dry-run: %v\n%s", err, out)
	}
	if jerr := json.NewDecoder(bytes.NewReader(out)).Decode(&res); jerr != nil || res.Refused || !res.DryRun {
		t.Errorf("dry-run JSON: want refused=false dry_run=true, got %s (err %v)", out, jerr)
	}
}

// E. A filter that matched nothing is not an empty vault.
func TestContract_ListFilterMissMessageNamesTheFilter(t *testing.T) {
	_, root := newContractVault(t)
	if err := os.WriteFile(filepath.Join(root, "one.md"), []byte("---\ntitle: One\ntype: note\nstatus: draft\n---\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := runCLIArgs(t, root, "index"); err != nil {
		t.Fatalf("index: %v\n%s", err, out)
	}
	var out []byte
	var err error
	stderr := captureStderr(t, func() {
		out, err = runCLIArgs(t, root, "list", "--tag", "nosuchtag")
	})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(bytes.TrimSpace(out)) != 0 {
		t.Errorf("stdout should be empty on a filter miss, got %q", out)
	}
	if strings.Contains(stderr, "No documents yet") {
		t.Errorf("a filter miss on a populated vault was reported as an empty vault:\n%s", stderr)
	}
	if !strings.Contains(stderr, "filter") {
		t.Errorf("the hint does not mention the filter:\n%s", stderr)
	}
}

// F. cobra prints "Error: " itself; the message must not carry its own.
func TestExitError_StripsRedundantErrorPrefix(t *testing.T) {
	if got := (&ExitError{Message: "error: boom"}).Error(); got != "boom" {
		t.Errorf("got %q, want %q (cobra adds the prefix; 51 call sites produced \"Error: error: ...\")", got, "boom")
	}
	if got := (&ExitError{Message: "boom"}).Error(); got != "boom" {
		t.Errorf("an unprefixed message changed: %q", got)
	}
}

// G. --format documents a fixed set; a typo must be refused, not rendered as JSON.
func TestContract_UnknownFormatIsRefused(t *testing.T) {
	_, root := newContractVault(t)
	_, err := runCLIArgs(t, root, "list", "--format", "bogus")
	if err == nil {
		t.Fatal("--format bogus was accepted; it used to silently render JSON")
	}
	if !strings.Contains(err.Error(), "unknown --format") || !strings.Contains(err.Error(), "json") {
		t.Errorf("refusal %q should name the flag and list valid values", err)
	}
	if out, err := runCLIArgs(t, root, "list", "--format", "json"); err != nil {
		t.Fatalf("--format json regressed: %v\n%s", err, out)
	}
}
