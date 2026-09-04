package cli

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Obsidian infers a property's TYPE from how the value is written. A quoted ISO
// value is Text: no date picker, no date sorting, no date-based query. yaml.v3
// double-quotes any Go string that would re-resolve to a non-string tag, so
// every `created`/`modified` 2nb has ever written was quoted, and every note 2nb
// authored showed both dates as plain text in the Properties panel.
//
// These assert on the BYTES ON DISK, because the quoting is the whole property
// type and nothing above the file can see it, then reparse to prove the note
// still reads as the same instant.

var plainFrontmatterDate = regexp.MustCompile(`(?m)^(created|modified): [^"'\n]+$`)

func assertPlainDates(t *testing.T, note, what string) {
	t.Helper()
	found := plainFrontmatterDate.FindAllString(note, -1)
	if len(found) != 2 {
		t.Errorf("%s: want unquoted created and modified, found %v in:\n%s", what, found, note)
	}
	for _, key := range []string{"created", "modified"} {
		if strings.Contains(note, key+`: "`) {
			t.Errorf("%s: %s is quoted, so Obsidian types it as Text:\n%s", what, key, note)
		}
	}
}

func TestContract_CreateWritesDatesObsidianTypesAsDates(t *testing.T) {
	_, root := newContractVault(t)

	if out, err := runCLIArgs(t, root, "create", "--type", "note", "--title", "Typed Dates", "--content", "body"); err != nil {
		t.Fatalf("create: %v\n%s", err, out)
	}
	note := readNote(t, filepath.Join(root, "typed-dates.md"))
	assertPlainDates(t, note, "2nb create")

	// Reparsed through the index: the note must still carry a comparable
	// instant, and `stale` must be able to parse it.
	if out, err := runCLIArgs(t, root, "index"); err != nil {
		t.Fatalf("index: %v\n%s", err, out)
	}
	out, err := runCLIArgs(t, root, "meta", "typed-dates.md", "--get", "created")
	if err != nil {
		t.Fatalf("meta --get: %v\n%s", err, out)
	}
	got := strings.TrimSpace(string(out))
	if !regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$`).MatchString(got) {
		t.Errorf("meta --get created = %q, want a second-precision RFC3339 instant", got)
	}
	if !strings.Contains(note, "created: "+got+"\n") {
		t.Errorf("the file and the reader disagree: file has no `created: %s`, in:\n%s", got, note)
	}
}

// `meta --set` passes the raw CLI string, so once a date node is plain, writing
// a string over it requotes the line and the note is back to Obsidian's Text
// type, one `meta --set` at a time. The coercion closes that.
func TestContract_MetaSetKeepsADateUnquoted(t *testing.T) {
	_, root := newContractVault(t)
	if out, err := runCLIArgs(t, root, "create", "--type", "note", "--title", "Set Dates", "--content", "body"); err != nil {
		t.Fatalf("create: %v\n%s", err, out)
	}

	for _, value := range []string{
		"2026-09-04T12:34:56Z", // the form 2nb itself prints
		"2026-09-04T12:34:56",  // the form Obsidian's datetime editor writes
		"2026-09-04",           // a bare date
	} {
		if out, err := runCLIArgs(t, root, "meta", "set-dates.md", "--set", "modified="+value); err != nil {
			t.Fatalf("meta --set modified=%s: %v\n%s", value, err, out)
		}
		note := readNote(t, filepath.Join(root, "set-dates.md"))
		assertPlainDates(t, note, "meta --set modified="+value)

		out, err := runCLIArgs(t, root, "meta", "set-dates.md", "--get", "modified")
		if err != nil {
			t.Fatalf("meta --get: %v\n%s", err, out)
		}
		if got := strings.TrimSpace(string(out)); got != "2026-09-04T12:34:56Z" && got != "2026-09-04T00:00:00Z" {
			t.Errorf("meta --set modified=%s then --get = %q, want the same instant back", value, got)
		}
	}
}

// A value that is not a date must still be storable: coercion is additive, and
// a vault carrying some other convention must not start failing writes.
func TestContract_MetaSetNonDateInADateFieldStillWrites(t *testing.T) {
	_, root := newContractVault(t)
	if out, err := runCLIArgs(t, root, "create", "--type", "note", "--title", "Odd Date", "--content", "body"); err != nil {
		t.Fatalf("create: %v\n%s", err, out)
	}
	if out, err := runCLIArgs(t, root, "meta", "odd-date.md", "--set", "modified=sometime soon"); err != nil {
		t.Fatalf("meta --set: %v\n%s", err, out)
	}
	note := readNote(t, filepath.Join(root, "odd-date.md"))
	if !strings.Contains(note, "modified: sometime soon\n") {
		t.Errorf("a non-date value was not stored verbatim:\n%s", note)
	}
	out, err := runCLIArgs(t, root, "meta", "odd-date.md", "--get", "modified")
	if err != nil {
		t.Fatalf("meta --get: %v\n%s", err, out)
	}
	if got := strings.TrimSpace(string(out)); got != "sometime soon" {
		t.Errorf("meta --get modified = %q, want the text back verbatim", got)
	}
}

// import-obsidian stamps its own created/modified when the source note carries
// none. An imported note must land with the same property types a created one
// gets, or the vault ends up half typed.
func TestContract_ImportObsidianWritesDatesObsidianTypesAsDates(t *testing.T) {
	_, root := newContractVault(t)
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "plain.md"), []byte("# Plain\n\nNo frontmatter at all.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if out, err := runCLIArgs(t, root, "import-obsidian", src); err != nil {
		t.Fatalf("import-obsidian: %v\n%s", err, out)
	}
	assertPlainDates(t, readNote(t, filepath.Join(root, "plain.md")), "import-obsidian")
}
