package cli

import (
	"encoding/csv"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// A dozen report commands tested `getFormat(cmd) == output.FormatJSON` and fell
// through to their human printer for every other value. So --csv, --tsv and
// --yaml were silently IGNORED (prose where a machine consumer asked for a
// table), and --format raw/md printed prose too, where the whole point of those
// formats is that a value with no document body is refused rather than rendered
// as something else. This table is the contract for all of them at once.

// newFormatCoverageVault builds a contract vault with a note, an index, and a
// real git repository, so `git activity` / `git show` / `git status` have
// something to report.
func newFormatCoverageVault(t *testing.T) (root, headSHA string) {
	t.Helper()
	_, root = newContractVault(t)
	notePath := filepath.Join(root, "doc.md")
	if err := os.WriteFile(notePath,
		[]byte("---\ntitle: Doc\ntype: note\nstatus: draft\n---\nintro text\n\n## Setup\n\nsetup body\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := runCLIArgs(t, root, "index"); err != nil {
		t.Fatalf("index: %v\n%s", err, out)
	}

	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Skipf("git is unavailable or refused %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q")
	git("add", "doc.md")
	git("commit", "-q", "-m", "add doc")
	return root, git("rev-parse", "HEAD")
}

func TestContract_ReportCommandsHonorEveryFormat(t *testing.T) {
	root, headSHA := newFormatCoverageVault(t)

	cases := []struct {
		name string
		argv []string
		// csvHeader, when set, is a column name the csv rendering MUST carry.
		// Only the commands whose payload is a slice of structs get a header
		// row (output.writeDelimited renders any other shape as one
		// JSON-encoded record), and for those it turns the "csv produced
		// something" guard into a real check that the STRUCTURED rendering ran
		// rather than the human printer.
		csvHeader string
	}{
		{"mcp status", []string{"mcp", "status"}, ""},
		{"mcp configured", []string{"mcp", "configured"}, "client"},
		{"git status", []string{"git", "status"}, ""},
		{"git activity", []string{"git", "activity"}, "subject"},
		{"git show", []string{"git", "show", headSHA}, ""},
		{"suggest-links", []string{"suggest-links", "doc.md"}, ""},
		{"suggest-target", []string{"suggest-target", "missing-note"}, ""},
		{"repair-links", []string{"repair-links", "doc.md"}, ""},
		{"relink", []string{"relink", "doc.md", "--from", "nope", "--to", "doc.md"}, ""},
		{"unlink", []string{"unlink", "doc.md", "--target", "nope"}, ""},
		{"index --doc", []string{"index", "--doc", "doc.md"}, ""},
		{"config bedrock", []string{"config", "bedrock"}, ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// json still parses.
			out, err := runCLIArgs(t, root, append(append([]string{}, tc.argv...), "--format", "json")...)
			if err != nil {
				t.Fatalf("--format json: %v\n%s", err, out)
			}
			var any any
			if err := json.Unmarshal(jsonPortion(out), &any); err != nil {
				t.Errorf("--format json is not parseable: %v\n%s", err, out)
			}

			// csv and yaml must produce STRUCTURED output, not the human prose
			// they used to fall through to.
			for _, format := range []string{"csv", "yaml"} {
				out, err := runCLIArgs(t, root, append(append([]string{}, tc.argv...), "--format", format)...)
				if err != nil {
					t.Errorf("--format %s: %v\n%s", format, err, out)
					continue
				}
				if strings.TrimSpace(string(out)) == "" {
					t.Errorf("--format %s produced nothing", format)
				}
				if format == "csv" && tc.csvHeader != "" {
					recs, rerr := csv.NewReader(strings.NewReader(string(out))).ReadAll()
					if rerr != nil || len(recs) == 0 {
						t.Errorf("--format csv is not a csv document (%v):\n%s", rerr, out)
						continue
					}
					if !slices.Contains(recs[0], tc.csvHeader) {
						t.Errorf("--format csv header %v does not carry %q; the human printer ran instead of the writer:\n%s",
							recs[0], tc.csvHeader, out)
					}
				}
			}

			// raw and md have no document body to emit here, so they must be
			// refused rather than print prose and exit 0.
			for _, format := range []string{"raw", "md"} {
				out, err := runCLIArgs(t, root, append(append([]string{}, tc.argv...), "--format", format)...)
				if err == nil {
					t.Errorf("--format %s was accepted; it should be refused:\n%s", format, out)
					continue
				}
				if !strings.Contains(err.Error(), "document body") {
					t.Errorf("--format %s refusal should say a document body is expected, got: %v", format, err)
				}
			}
		})
	}
}

// jsonPortion trims anything the command wrote to stderr ahead of its JSON
// document; runCLIArgs captures stdout and stderr into one buffer.
func jsonPortion(out []byte) []byte {
	s := string(out)
	for i, r := range s {
		if r == '{' || r == '[' {
			return []byte(s[i:])
		}
	}
	return out
}

// Bucket B: a command whose machine output is a STREAM of JSON events cannot go
// through output.Write at all, so it must refuse any other explicit format by
// name rather than ignore it and print its human lines.
func TestContract_JSONEventStreamsRefuseOtherFormats(t *testing.T) {
	root, _ := newFormatCoverageVault(t)

	for _, argv := range [][]string{
		{"models", "bench", "--probe", "retrieval"},
		{"ai", "engine", "rm", "some-model"},
	} {
		for _, format := range []string{"raw", "md", "csv", "yaml", "tsv"} {
			out, err := runCLIArgs(t, root, append(append([]string{}, argv...), "--format", format)...)
			if err == nil {
				t.Errorf("%v --format %s was accepted; it should be refused:\n%s", argv, format, out)
				continue
			}
			if !strings.Contains(err.Error(), "stream of JSON events") {
				t.Errorf("%v --format %s refusal should name the event stream, got: %v", argv, format, err)
			}
		}
	}
}

// Bucket C: `polish --undo` honored --json only and printed NOTHING for every
// other format, so csv/yaml/raw/md exited 0 with an empty stdout and the caller
// could not tell a successful revert from an ignored format. emitUndoResult is
// the whole of that command's output, so it is exercised directly: reaching it
// through the CLI needs a real polish snapshot, which needs a paid model call.
func TestContract_PolishUndoReportsEveryFormat(t *testing.T) {
	setFormat := func(t *testing.T, f string) {
		t.Helper()
		old := flagFormat
		flagFormat = f
		t.Cleanup(func() { flagFormat = old })
	}

	t.Run("json emits the verdict", func(t *testing.T) {
		setFormat(t, "json")
		out := captureStdout(t, func() {
			if err := emitUndoResult(polishCmd, "doc.md", true); err != nil {
				t.Fatalf("emitUndoResult: %v", err)
			}
		})
		var got PolishUndoResult
		if err := json.Unmarshal([]byte(out), &got); err != nil {
			t.Fatalf("not JSON: %v\n%s", err, out)
		}
		if got.Path != "doc.md" || !got.Reverted {
			t.Errorf("verdict = %+v, want doc.md reverted", got)
		}
	})

	for _, format := range []string{"csv", "yaml", "tsv"} {
		t.Run(format+" emits something", func(t *testing.T) {
			setFormat(t, format)
			out := captureStdout(t, func() {
				if err := emitUndoResult(polishCmd, "doc.md", true); err != nil {
					t.Fatalf("emitUndoResult: %v", err)
				}
			})
			if strings.TrimSpace(out) == "" {
				t.Errorf("--format %s produced nothing", format)
			}
			if !strings.Contains(out, "doc.md") {
				t.Errorf("--format %s did not name the note:\n%s", format, out)
			}
		})
	}

	for _, format := range []string{"raw", "md"} {
		t.Run(format+" is refused", func(t *testing.T) {
			setFormat(t, format)
			err := emitUndoResult(polishCmd, "doc.md", true)
			if err == nil {
				t.Fatalf("--format %s was accepted; an undo verdict has no document body", format)
			}
			if !strings.Contains(err.Error(), "document body") {
				t.Errorf("refusal should say a document body is expected, got: %v", err)
			}
		})
	}

	t.Run("the default format still says what happened", func(t *testing.T) {
		setFormat(t, "")
		stderr := captureStderr(t, func() {
			if err := emitUndoResult(polishCmd, "doc.md", false); err != nil {
				t.Fatalf("emitUndoResult: %v", err)
			}
		})
		if !strings.Contains(stderr, "doc.md") || !strings.Contains(stderr, "nothing to undo") {
			t.Errorf("human line lost: %q", stderr)
		}
	})
}

// captureStdout runs fn with os.Stdout redirected and returns what it wrote.
// writeOut writes to os.Stdout directly, not through cobra's writer.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stdout
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = pw
	done := make(chan string, 1)
	go func() {
		var b strings.Builder
		_, _ = io.Copy(&b, pr)
		done <- b.String()
	}()
	restored := false
	restore := func() {
		if restored {
			return
		}
		restored = true
		pw.Close()
		os.Stdout = orig
	}
	defer restore()
	fn()
	restore()
	return <-done
}

// Bucket D: search and list return early on zero rows, so raw/md printed
// nothing and exited 0 there while the same command with one row exited
// non-zero. The refusal must depend on the command, not the row count.
func TestContract_RowSetCommandsRefuseRawOnZeroRows(t *testing.T) {
	root, _ := newFormatCoverageVault(t)

	cases := []struct {
		name string
		argv []string
	}{
		{"search with hits", []string{"search", "intro"}},
		{"search with none", []string{"search", "zzz-nothing-matches-this"}},
		{"list with rows", []string{"list"}},
		{"list with none", []string{"list", "--type", "zzz-no-such-type"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, format := range []string{"raw", "md"} {
				out, err := runCLIArgs(t, root, append(append([]string{}, tc.argv...), "--format", format)...)
				if err == nil {
					t.Errorf("--format %s was accepted:\n%s", format, out)
					continue
				}
				if !strings.Contains(err.Error(), "document body") {
					t.Errorf("--format %s refusal should say a document body is expected, got: %v", format, err)
				}
			}
			// csv keeps emitting the empty stream for zero rows; it must not
			// start failing.
			if out, err := runCLIArgs(t, root, append(append([]string{}, tc.argv...), "--format", "csv")...); err != nil {
				t.Errorf("--format csv: %v\n%s", err, out)
			}
		})
	}
}
