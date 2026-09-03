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
	// Restore under defer: a t.Fatal inside fn unwinds through here, and
	// without this the swapped os.Stderr and the reader goroutine would leak
	// into every later test in the package.
	restored := false
	restore := func() {
		if restored {
			return
		}
		restored = true
		pw.Close()
		os.Stderr = orig
	}
	defer restore()
	fn()
	restore()
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
	for in, want := range map[string]string{
		"error: boom": "boom",        // the 52 self-prefixing call sites
		"Error: boom": "boom",        // a message that quoted an upstream error verbatim
		"boom":        "boom",        // unprefixed, unchanged
		"errors: two": "errors: two", // not the prefix; must survive
	} {
		if got := (&ExitError{Message: in}).Error(); got != want {
			t.Errorf("ExitError(%q).Error() = %q, want %q", in, got, want)
		}
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
	// The gate must reject only the unknown, never a documented value. `table`
	// is deliberately absent: nothing renders it, so accepting it would let it
	// fall through to JSON, the exact failure the gate exists to stop.
	for _, f := range []string{"json", "csv", "tsv", "yaml", "text", "paths", "tree"} {
		if out, err := runCLIArgs(t, root, "list", "--format", f); err != nil {
			t.Errorf("--format %s was refused by the format gate: %v\n%s", f, err, out)
		}
	}
	if _, err := runCLIArgs(t, root, "list", "--format", "table"); err == nil {
		t.Error("--format table was accepted, but nothing renders a table; it would fall through to JSON")
	}
}

// A section IS a body. `read --chunk <heading> --format raw` printed a Go
// struct dump on 0.21.1 and was refused by the first version of the no-body
// guard; both were wrong for the same reason.
func TestContract_ReadChunkRawEmitsTheSectionBody(t *testing.T) {
	_, root := newContractVault(t)
	body := "---\ntitle: Doc\ntype: note\nstatus: draft\n---\nintro\n\n## Setup\n\nsetup body here\n"
	if err := os.WriteFile(filepath.Join(root, "doc.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, format := range []string{"raw", "md"} {
		out, err := runCLIArgs(t, root, "read", "doc.md", "--chunk", "Setup", "--format", format)
		if err != nil {
			t.Fatalf("read --chunk --format %s: %v\n%s", format, err, out)
		}
		s := string(out)
		if !strings.Contains(s, "setup body here") {
			t.Errorf("--format %s did not emit the section body:\n%s", format, s)
		}
		if strings.Contains(s, "{") && strings.Contains(s, "doc_id") {
			t.Errorf("--format %s emitted a struct rendering, not the body:\n%s", format, s)
		}
	}
}

// A failed rename is neither a move nor a refusal, and the summary must say
// which notes it left pointing at nothing. The plan rewrites referencing notes
// BEFORE the rename, so a rename that then fails is exactly the case where an
// honest count matters. Forced by making the destination directory unwritable.
func TestContract_FailedRenameIsReportedAsFailed(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions; the forced failure cannot be produced")
	}
	_, root := newContractVault(t)
	write := func(rel, body string) {
		if err := os.WriteFile(filepath.Join(root, rel), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("lone.md", "---\ntitle: Lone\ntype: note\nstatus: draft\n---\nnobody links here\n")
	write("target.md", "---\ntitle: Target\ntype: note\nstatus: draft\n---\nlinked\n")
	write("ref.md", "---\ntitle: Ref\ntype: note\nstatus: draft\n---\nsee [[target]]\n")
	if out, err := runCLIArgs(t, root, "index"); err != nil {
		t.Fatalf("index: %v\n%s", err, out)
	}
	blocked := filepath.Join(root, "blocked")
	if err := os.MkdirAll(blocked, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(blocked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(blocked, 0o755) })

	// The JSON form emits only the document; the human summary is the default
	// form. Run each once.
	run := func(src string) (out []byte, jsonErr error, stderr string, humanErr error) {
		out, jsonErr = runCLIArgs(t, root, "move", src, "blocked/"+src, "--json")
		stderr = captureStderr(t, func() {
			_, humanErr = runCLIArgs(t, root, "move", src, "blocked/"+src)
		})
		return
	}

	t.Run("no referencing notes: says nothing else changed", func(t *testing.T) {
		out, jsonErr, stderr, humanErr := run("lone.md")
		if jsonErr == nil || humanErr == nil {
			t.Fatal("a rename into an unwritable directory must fail")
		}
		var res struct {
			MoveFailed bool `json:"move_failed"`
		}
		if jerr := json.NewDecoder(bytes.NewReader(out)).Decode(&res); jerr != nil || !res.MoveFailed {
			t.Errorf("want move_failed: true in JSON, got %s (err %v)", out, jerr)
		}
		if strings.Contains(stderr, "Moved:") {
			t.Errorf("stderr claims a completed move:\n%s", stderr)
		}
		if !strings.Contains(stderr, "Move FAILED") || !strings.Contains(stderr, "no referencing note was changed") {
			t.Errorf("stderr should report the failure and that nothing else changed:\n%s", stderr)
		}
		if _, serr := os.Stat(filepath.Join(root, "lone.md")); serr != nil {
			t.Error("source file is gone after a failed rename")
		}
	})

	t.Run("with a referencing note: counts the rewrite that landed", func(t *testing.T) {
		// One invocation only. A first attempt rewrites ref.md to the destination
		// BEFORE the rename fails, so a second attempt would find nothing left to
		// rewrite and truthfully report "no referencing note was changed". That
		// is the dangling-link hazard itself, not a test artifact to paper over.
		// The destination changes the BASENAME. A bare [[target]] resolves by
		// basename, so a move that keeps it needs no rewrite at all; only a
		// rename forces the referencing note to be rewritten before the file
		// move, which is the ordering under test.
		var humanErr error
		stderr := captureStderr(t, func() {
			_, humanErr = runCLIArgs(t, root, "move", "target.md", "blocked/renamed.md")
		})
		if humanErr == nil {
			t.Fatal("a rename into an unwritable directory must fail")
		}
		if !strings.Contains(stderr, "1 referencing note(s) were already rewritten") {
			t.Errorf("stderr should count the one rewrite that landed:\n%s", stderr)
		}
		if strings.Contains(stderr, "no referencing note was changed") {
			t.Errorf("stderr denies a rewrite that did happen:\n%s", stderr)
		}
	})
}

// H. `--yaml` is the json view in YAML syntax, so it uses the SAME field names.
// yaml.v3 lowercases a bare Go field name and honors no json `omitempty`, and
// every output struct here carries json tags only, so --yaml used to invent a
// third set of key names that matched neither the documented json shape nor
// anything a consumer could look up: `modifiedat`, `sourcepath`, `targetraw`.
func TestContract_YAMLUsesTheJSONFieldNames(t *testing.T) {
	_, root := newContractVault(t)
	body := "---\ntitle: Doc\ntype: note\nstatus: draft\n---\nintro\n\nA broken [[no-such-note]] link.\n"
	if err := os.WriteFile(filepath.Join(root, "doc.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runCLIArgs(t, root, "index"); err != nil {
		t.Fatalf("index: %v", err)
	}

	cases := []struct {
		argv     []string
		wantKeys []string
		wantNot  []string
	}{
		// `list` rows carry modified_at / created_at.
		{[]string{"list", "--format", "yaml"}, []string{"modified_at:"}, []string{"modifiedat", "createdat"}},
		// `unresolved` rows carry source_path / target_raw.
		{[]string{"unresolved", "--format", "yaml"}, []string{"source_path:", "target_raw:"}, []string{"sourcepath", "targetraw"}},
	}
	for _, tc := range cases {
		t.Run(strings.Join(tc.argv, " "), func(t *testing.T) {
			out, err := runCLIArgs(t, root, tc.argv...)
			if err != nil {
				t.Fatalf("%v: %v\n%s", tc.argv, err, out)
			}
			s := string(out)
			for _, want := range tc.wantKeys {
				if !strings.Contains(s, want) {
					t.Errorf("missing json field name %q in:\n%s", want, s)
				}
			}
			for _, unwanted := range tc.wantNot {
				if strings.Contains(s, unwanted) {
					t.Errorf("yaml emitted the Go field name %q instead of the json one:\n%s", unwanted, s)
				}
			}
		})
	}
}
