package cli

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/apresai/2ndbrain/internal/output"
	"github.com/apresai/2ndbrain/internal/skills"
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
		// needsEmbedder marks a command that must reach a provider to produce
		// rows at all. Its raw/md refusal is still unconditional (that is the
		// point: the refusal must not depend on whether a provider answers),
		// but the positive json/csv/yaml cases gate on the capability probe, so
		// a credential-free runner SKIPS them instead of failing. Gating on the
		// outcome rather than on an environment variable is the project rule.
		needsEmbedder bool
	}{
		{"mcp status", []string{"mcp", "status"}, "", false},
		{"mcp configured", []string{"mcp", "configured"}, "client", false},
		{"git status", []string{"git", "status"}, "", false},
		{"git activity", []string{"git", "activity"}, "subject", false},
		{"git show", []string{"git", "show", headSHA}, "", false},
		{"suggest-links", []string{"suggest-links", "doc.md"}, "", true},
		{"suggest-target", []string{"suggest-target", "missing-note"}, "", true},
		{"repair-links", []string{"repair-links", "doc.md"}, "", false},
		{"relink", []string{"relink", "doc.md", "--from", "nope", "--to", "doc.md"}, "", false},
		{"unlink", []string{"unlink", "doc.md", "--target", "nope"}, "", false},
		{"index --doc", []string{"index", "--doc", "doc.md"}, "", false},
		{"config bedrock", []string{"config", "bedrock"}, "", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// raw and md FIRST, and unconditionally. These commands have no
			// document body to emit, so the refusal must hold on any machine,
			// including one with no provider credentials at all.
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

			// The positive cases run the command for real, so a command that
			// needs a provider to produce rows skips when none answers.
			t.Run("structured formats render", func(t *testing.T) {
				if tc.needsEmbedder {
					requireEmbeddings(t, root)
				}
				out, err := runCLIArgs(t, root, append(append([]string{}, tc.argv...), "--format", "json")...)
				if err != nil {
					t.Fatalf("--format json: %v\n%s", err, out)
				}
				var any any
				if err := json.Unmarshal(jsonPortion(out), &any); err != nil {
					t.Errorf("--format json is not parseable: %v\n%s", err, out)
				}

				// csv and yaml must produce STRUCTURED output, not the human
				// prose they used to fall through to.
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
			})
		})
	}
}

// `--format text` promises plain text and delivered Go's %v rendering of the
// payload: `list --format text` printed `{cb9316a1-... resources/x.md Title
// note draft 2026-...}`, `folders` printed `{(root) 1}`, `models list` printed
// one such line per model, and `config show` printed a HEAP ADDRESS for its
// nested config pointer. A row renders as named pairs now, and a single struct
// as named lines.
//
// The discriminator is that a line must not START with "{" (that is the struct
// dump); a "{" INSIDE a value is the compact JSON of a composite cell, which is
// what `config show`'s nested config legitimately renders as.
func TestContract_TextFormatRendersReadableLines(t *testing.T) {
	root, _ := newFormatCoverageVault(t)

	cases := []struct {
		name   string
		argv   []string
		want   []string
		absent []string
	}{
		{"list", []string{"list"}, []string{"path=doc.md", "title=Doc", "type=note"}, []string{"0x", "{"}},
		{"folders", []string{"folders"}, []string{"folder=(root)", "count=1"}, []string{"0x", "{"}},
		{"config show", []string{"config", "show"}, []string{"vault_root: ", "vault_name: "}, []string{"0x"}},
		{"models list", []string{"models", "list"}, []string{"id=", "provider="}, []string{"0x", "<nil>"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := runCLIArgs(t, root, append(append([]string{}, tc.argv...), "--format", "text")...)
			if err != nil {
				t.Fatalf("--format text: %v\n%s", err, out)
			}
			text := string(out)
			if strings.TrimSpace(text) == "" {
				t.Fatalf("--format text produced nothing")
			}
			for _, line := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
				if strings.HasPrefix(line, "{") {
					t.Errorf("line is a Go struct dump, not text: %q", line)
				}
			}
			for _, want := range tc.want {
				if !strings.Contains(text, want) {
					t.Errorf("missing %q in:\n%s", want, text)
				}
			}
			for _, unwanted := range tc.absent {
				if strings.Contains(text, unwanted) {
					t.Errorf("output still carries %q:\n%s", unwanted, text)
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
// other format, so csv/yaml exited 0 with an empty stdout and the caller could
// not tell a successful revert from an ignored format. emitUndoResult is the
// whole of that command's output, so it is exercised directly: reaching it
// through the CLI needs a real polish snapshot, which needs a paid model call.
//
// raw and md are deliberately NOT here. runPolish refuses them up front on both
// polish paths, so asserting them against emitUndoResult would pin a path no
// invocation reaches; TestContract_PolishRefusesBodylessFormatsUpFront covers
// the pair where it actually happens.
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

// git diff is the one report command whose output IS a body, so it inverts the
// rule: raw/md/text emit the diff, json wraps it in a record, and the row-set
// formats have nothing to render and are refused by name. Before this they were
// silently handed the diff text, which is exactly the "silently ignored" shape
// the rest of this file exists to stop.
func TestContract_GitDiffIsBodyShaped(t *testing.T) {
	root, _ := newFormatCoverageVault(t)
	if err := os.WriteFile(filepath.Join(root, "doc.md"),
		[]byte("---\ntitle: Doc\ntype: note\nstatus: draft\n---\nintro text CHANGED\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, format := range []string{"raw", "md", "text"} {
		out, err := runCLIArgs(t, root, "git", "diff", "doc.md", "--format", format)
		if err != nil {
			t.Errorf("git diff --format %s: %v\n%s", format, err, out)
			continue
		}
		if !strings.Contains(string(out), "CHANGED") {
			t.Errorf("git diff --format %s did not emit the diff:\n%s", format, out)
		}
	}

	out, err := runCLIArgs(t, root, "git", "diff", "doc.md", "--format", "json")
	if err != nil {
		t.Fatalf("git diff --format json: %v\n%s", err, out)
	}
	var rec struct {
		Path string `json:"path"`
		Diff string `json:"diff"`
	}
	if err := json.Unmarshal(jsonPortion(out), &rec); err != nil {
		t.Fatalf("git diff --format json is not parseable: %v\n%s", err, out)
	}
	if rec.Path != "doc.md" || !strings.Contains(rec.Diff, "CHANGED") {
		t.Errorf("git diff --json record = %+v, want doc.md with the diff", rec)
	}

	for _, format := range []string{"csv", "tsv", "yaml"} {
		out, err := runCLIArgs(t, root, "git", "diff", "doc.md", "--format", format)
		if err == nil {
			t.Errorf("git diff --format %s was accepted; a diff is not a row set:\n%s", format, out)
			continue
		}
		if !strings.Contains(err.Error(), "row set") {
			t.Errorf("git diff --format %s refusal should say a diff is not a row set, got: %v", format, err)
		}
	}
}

// export-context is the second body-shaped report command, and it honored no
// format at all: runExport never called getFormat, so `--json`, `--csv` and
// `--yaml` each printed the markdown bundle and exited 0. `2nb export-context
// --json | jq .` was a parse error on a command reporting success. It takes the
// git diff shape now: raw/md/text emit the bundle, json wraps it in a record,
// and the row-set formats are refused by name.
func TestContract_ExportContextIsBodyShaped(t *testing.T) {
	root, _ := newFormatCoverageVault(t)

	for _, format := range []string{"raw", "md", "text"} {
		out, err := runCLIArgs(t, root, "export-context", "--format", format)
		if err != nil {
			t.Errorf("export-context --format %s: %v\n%s", format, err, out)
			continue
		}
		if !strings.Contains(string(out), "# Knowledge Base Context") {
			t.Errorf("export-context --format %s did not emit the bundle:\n%s", format, out)
		}
	}

	out, err := runCLIArgs(t, root, "export-context", "--format", "json")
	if err != nil {
		t.Fatalf("export-context --format json: %v\n%s", err, out)
	}
	var rec struct {
		Bundle string `json:"bundle"`
		Docs   int    `json:"docs"`
		Chars  int    `json:"chars"`
	}
	if err := json.Unmarshal(jsonPortion(out), &rec); err != nil {
		t.Fatalf("export-context --format json is not parseable: %v\n%s", err, out)
	}
	if !strings.Contains(rec.Bundle, "# Knowledge Base Context") {
		t.Errorf("json record does not carry the bundle: %+v", rec)
	}
	if rec.Docs != 1 || rec.Chars != len(rec.Bundle) {
		t.Errorf("json record counts = docs %d chars %d, want 1 and %d", rec.Docs, rec.Chars, len(rec.Bundle))
	}

	for _, format := range []string{"csv", "tsv", "yaml"} {
		out, err := runCLIArgs(t, root, "export-context", "--format", format)
		if err == nil {
			t.Errorf("export-context --format %s was accepted; a bundle is not a row set:\n%s", format, out)
			continue
		}
		if !strings.Contains(err.Error(), "row set") {
			t.Errorf("export-context --format %s refusal should say a bundle is not a row set, got: %v", format, err)
		}
	}

	// The refusal must not depend on there being any documents to bundle: it is
	// a property of the command, and it runs before the vault is even opened.
	out, err = runCLIArgs(t, root, "export-context", "--types", "nosuchtypexyz", "--format", "csv")
	if err == nil {
		t.Errorf("export-context --format csv with no matching docs was accepted:\n%s", out)
	}
	// A zero-document bundle is still a JSON record, not an empty stream.
	out, err = runCLIArgs(t, root, "export-context", "--types", "nosuchtypexyz", "--json")
	if err != nil {
		t.Fatalf("export-context --json with no matching docs: %v\n%s", err, out)
	}
	if err := json.Unmarshal(jsonPortion(out), &rec); err != nil {
		t.Fatalf("empty export-context --json is not parseable: %v\n%s", err, out)
	}
	if rec.Docs != 0 || rec.Bundle != "" {
		t.Errorf("empty bundle record = %+v, want zero docs and an empty bundle", rec)
	}
}

// The bundle's own header states how many documents it contains, and it stated
// the number the QUERY matched. A note whose file cannot be parsed is skipped
// with a warning, so a vault with one unreadable note announced one more
// document than the bundle held, and the `--json` record then carried that
// header inside `bundle` while `docs` reported the real number: two counts of
// the same thing, disagreeing, in one record.
//
// The fixture indexes three good notes and then corrupts one on disk, which is
// how this happens for real: the index row survives, the file no longer parses.
func TestContract_ExportContextCountsIncludedNotes(t *testing.T) {
	_, root := newContractVault(t)
	for _, name := range []string{"good-one.md", "good-two.md", "broken.md"} {
		if err := os.WriteFile(filepath.Join(root, name),
			[]byte("---\ntitle: "+name+"\ntype: note\nstatus: draft\n---\nbody of "+name+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if out, err := runCLIArgs(t, root, "index"); err != nil {
		t.Fatalf("index: %v\n%s", err, out)
	}
	// Unterminated quoted scalar: valid enough to have been indexed a moment
	// ago, invalid YAML now.
	if err := os.WriteFile(filepath.Join(root, "broken.md"),
		[]byte("---\ntitle: \"unterminated\ntype: note\n---\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := runCLIArgs(t, root, "export-context", "--format", "json")
	if err != nil {
		t.Fatalf("export-context --json: %v\n%s", err, out)
	}
	var rec struct {
		Bundle string `json:"bundle"`
		Docs   int    `json:"docs"`
		Chars  int    `json:"chars"`
	}
	if err := json.Unmarshal(jsonPortion(out), &rec); err != nil {
		t.Fatalf("not parseable: %v\n%s", err, out)
	}
	if rec.Docs != 2 {
		t.Errorf("docs = %d, want 2 (three matched, one could not be parsed): %s", rec.Docs, rec.Bundle)
	}
	if !strings.Contains(rec.Bundle, "2 documents included") {
		t.Errorf("the bundle header disagrees with the docs count:\n%s", rec.Bundle)
	}
	if strings.Contains(rec.Bundle, "3 documents included") {
		t.Errorf("the header still counts the notes the query matched:\n%s", rec.Bundle)
	}
	if strings.Contains(rec.Bundle, "broken.md") {
		t.Errorf("the unparseable note was counted into the bundle:\n%s", rec.Bundle)
	}
	if rec.Chars != len(rec.Bundle) {
		t.Errorf("chars = %d, want %d", rec.Chars, len(rec.Bundle))
	}

	// The body form carries the same header, since both render one bundle.
	body, err := runCLIArgs(t, root, "export-context", "--format", "raw")
	if err != nil {
		t.Fatalf("export-context --format raw: %v\n%s", err, body)
	}
	if !strings.Contains(string(body), "2 documents included") {
		t.Errorf("the body form's header disagrees:\n%s", body)
	}
}

// The up-front guard in runPolish is the only raw/md gate on either polish
// path. It has to fire before any provider work, so this must hold on a machine
// with no credentials at all: polish is a paid generation call, and --undo
// opens the write path, neither of which should be reached to learn that the
// format cannot render the result.
func TestContract_PolishRefusesBodylessFormatsUpFront(t *testing.T) {
	root, _ := newFormatCoverageVault(t)

	for _, argv := range [][]string{
		{"polish", "doc.md"},
		{"polish", "doc.md", "--undo"},
	} {
		for _, format := range []string{"raw", "md"} {
			out, err := runCLIArgs(t, root, append(append([]string{}, argv...), "--format", format)...)
			if err == nil {
				t.Errorf("%v --format %s was accepted; a polish result has no document body:\n%s", argv, format, out)
				continue
			}
			if !strings.Contains(err.Error(), "document body") {
				t.Errorf("%v --format %s refusal should name the missing document body, got: %v", argv, format, err)
			}
			// The refusal must be the FORMAT, not a provider or a snapshot: those
			// are the errors this guard exists to run ahead of.
			for _, leaked := range []string{"provider", "credential", "snapshot"} {
				if strings.Contains(strings.ToLower(err.Error()), leaked) {
					t.Errorf("%v --format %s reached %s work before refusing: %v", argv, format, leaked, err)
				}
			}
		}
	}
}

// The two reports a human is likeliest to point `--format text` at both embed
// an anonymous struct: DoctorReport embeds SuiteStatus, SkillDoctorReport
// embeds skills.Verification (which itself embeds InstallStatus). json FLATTENS
// an untagged anonymous field, the formatter did not, so `2nb skills doctor
// --format text` printed one `Verification: {"slug":...}` JSON blob keyed by a
// Go type name that appears nowhere in the json view, and `2nb doctor --format
// text` printed a `SuiteStatus:` blob the same way. The promise those formats
// carry is that text, json and csv agree about what a field is called.
//
// Rendered through output.Write, which is the path both commands take, so no
// model is called: `2nb doctor` probes the active models for real.
func TestContract_EmbeddedReportsFlattenLikeJSON(t *testing.T) {
	reports := []struct {
		name    string
		value   any
		absent  []string
		present []string
	}{
		{
			name: "doctor",
			value: DoctorReport{
				SuiteStatus: SuiteStatus{Latest: "0.22.3", Checked: true, Detail: "d", InSync: true},
				OK:          true,
			},
			absent:  []string{"SuiteStatus", "ProductState"},
			present: []string{"latest", "checked", "in_sync", "ok"},
		},
		{
			name: "skills doctor",
			value: SkillDoctorReport{
				Verification: skills.Verification{
					InstallStatus: skills.InstallStatus{Slug: "claude-code", Name: "Claude Code"},
					Installed:     true,
				},
				OK: true,
			},
			absent:  []string{"Verification", "InstallStatus"},
			present: []string{"slug", "name", "installed", "ok"},
		},
	}

	for _, rep := range reports {
		t.Run(rep.name, func(t *testing.T) {
			var textBuf bytes.Buffer
			if err := output.Write(&textBuf, output.FormatText, rep.value); err != nil {
				t.Fatalf("render text: %v", err)
			}
			text := textBuf.String()
			for _, bad := range rep.absent {
				if strings.Contains(text, bad+":") {
					t.Errorf("--format text still nests a blob under the Go type name %q:\n%s", bad, text)
				}
			}
			for _, want := range rep.present {
				if !strings.Contains(text, want+": ") {
					t.Errorf("--format text is missing the promoted field %q:\n%s", want, text)
				}
			}

			// Every name text printed is a name json actually emits.
			raw, err := json.Marshal(rep.value)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			var keys map[string]any
			if err := json.Unmarshal(raw, &keys); err != nil {
				t.Fatalf("unmarshal %s: %v", raw, err)
			}
			for _, line := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
				name, _, ok := strings.Cut(line, ": ")
				if !ok {
					continue
				}
				if _, isJSONKey := keys[name]; !isJSONKey {
					t.Errorf("--format text names a field %q that the json view does not have (keys: %s)", name, raw)
				}
			}
		})
	}
}

// The same defect end to end, through the command a user actually runs.
// `skills doctor` calls no model, so this is credential-free; it exits non-zero
// when the skill is not installed for the probed agent, which says nothing
// about the rendering and is deliberately ignored.
func TestContract_SkillsDoctorTextNamesItsFields(t *testing.T) {
	_, root := newContractVault(t)
	out, _ := runCLIArgs(t, root, "skills", "doctor", "--format", "text")
	text := string(out)
	if strings.TrimSpace(text) == "" {
		t.Fatal("skills doctor --format text produced nothing")
	}
	for _, bad := range []string{"Verification:", "InstallStatus:"} {
		if strings.Contains(text, bad) {
			t.Errorf("skills doctor --format text still prints a %s blob:\n%s", bad, text)
		}
	}
	if !strings.Contains(text, "slug: ") {
		t.Errorf("skills doctor --format text lost its promoted fields:\n%s", text)
	}
}
