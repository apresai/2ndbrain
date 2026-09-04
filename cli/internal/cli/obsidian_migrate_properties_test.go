package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// quotedNote is the exact shape every note 2nb wrote before this release: an
// ISO instant in double quotes, which Obsidian reads as Text. It carries a
// comment and a deliberately non-alphabetical key order, because the whole-map
// re-marshal that Document.Serialize falls back to would destroy both, and the
// migration is required never to reach it.
const quotedNote = `---
title: Quoted Note
type: note
status: draft
# hand-written, and it must survive
created: "2026-04-05T07:27:34Z"
modified: "2026-04-05T07:27:34Z"
tags: [alpha, beta]
aliases:
  - QN
---

# Body

Some prose with a [[link]].
`

func migrateJSON(t *testing.T, root string, extra ...string) MigratePropertiesResult {
	t.Helper()
	argv := append([]string{"obsidian", "migrate-properties", "--json"}, extra...)
	out, err := runCLIArgs(t, root, argv...)
	if err != nil {
		t.Fatalf("migrate-properties: %v\n%s", err, out)
	}
	var res MigratePropertiesResult
	if err := json.Unmarshal(jsonPortion(out), &res); err != nil {
		t.Fatalf("migrate-properties --json is not parseable: %v\n%s", err, out)
	}
	return res
}

// The core promise: the named date lines change and NOTHING else does. Asserted
// as a full-file comparison, line by line, rather than by spot-checking, because
// the failure mode this guards against (Document.Serialize's silent fallback to
// the whole-map re-marshal) alphabetizes every key and drops every comment, and
// a spot check would not see it.
func TestContract_MigrateProperties_TouchesOnlyTheDateLines(t *testing.T) {
	_, root := newContractVault(t)
	path := filepath.Join(root, "quoted.md")
	if err := os.WriteFile(path, []byte(quotedNote), 0o644); err != nil {
		t.Fatal(err)
	}

	res := migrateJSON(t, root, "--write")
	if res.Changed != 1 {
		t.Fatalf("changed = %d, want 1 (skipped: %+v)", res.Changed, res.Skipped)
	}

	after := readNote(t, path)
	beforeLines := strings.Split(quotedNote, "\n")
	afterLines := strings.Split(after, "\n")
	if len(beforeLines) != len(afterLines) {
		t.Fatalf("line count changed from %d to %d; the whole-map re-marshal ran:\n%s", len(beforeLines), len(afterLines), after)
	}
	for i := range beforeLines {
		if beforeLines[i] == afterLines[i] {
			continue
		}
		key, _, _ := strings.Cut(afterLines[i], ":")
		if key != "created" && key != "modified" {
			t.Errorf("line %d changed and is not a migrated date:\n  before: %q\n   after: %q", i+1, beforeLines[i], afterLines[i])
		}
	}
	for _, want := range []string{
		"created: 2026-04-05T07:27:34Z\n",
		"modified: 2026-04-05T07:27:34Z\n",
		"# hand-written, and it must survive\n",
		"tags: [alpha, beta]\n",
	} {
		if !strings.Contains(after, want) {
			t.Errorf("the migrated note lost %q:\n%s", want, after)
		}
	}
	if strings.Contains(after, `created: "`) || strings.Contains(after, `modified: "`) {
		t.Errorf("a date is still quoted, so Obsidian still types it as Text:\n%s", after)
	}
}

// Idempotent in both directions: a second run finds nothing to do, and the file
// is byte-identical to what the first run left.
func TestContract_MigrateProperties_IsIdempotent(t *testing.T) {
	_, root := newContractVault(t)
	path := filepath.Join(root, "quoted.md")
	if err := os.WriteFile(path, []byte(quotedNote), 0o644); err != nil {
		t.Fatal(err)
	}

	if res := migrateJSON(t, root, "--write"); res.Changed != 1 {
		t.Fatalf("first run changed = %d, want 1", res.Changed)
	}
	afterFirst := readNote(t, path)

	res := migrateJSON(t, root, "--write")
	if res.Changed != 0 {
		t.Errorf("second run changed = %d, want 0: %+v", res.Changed, res.Notes)
	}
	if got := readNote(t, path); got != afterFirst {
		t.Errorf("the second run rewrote the file:\n%s", got)
	}
}

// A preview writes nothing at all, and still reports exactly what it would do.
func TestContract_MigrateProperties_PreviewWritesNothing(t *testing.T) {
	_, root := newContractVault(t)
	path := filepath.Join(root, "quoted.md")
	if err := os.WriteFile(path, []byte(quotedNote), 0o644); err != nil {
		t.Fatal(err)
	}

	res := migrateJSON(t, root)
	if res.Written {
		t.Error("a preview reported itself as written")
	}
	if res.Changed != 1 || len(res.Notes) != 1 {
		t.Fatalf("preview changed = %d, notes = %+v, want 1 note", res.Changed, res.Notes)
	}
	fields := res.Notes[0].Fields
	if len(fields) != 2 {
		t.Fatalf("preview fields = %+v, want created and modified", fields)
	}
	for _, f := range fields {
		if !strings.Contains(f.From, `"`) || strings.Contains(f.To, `"`) {
			t.Errorf("preview shows %s: %q -> %q, want quoted -> plain", f.Field, f.From, f.To)
		}
	}
	if got := readNote(t, path); got != quotedNote {
		t.Errorf("the preview wrote to disk:\n%s", got)
	}
}

// A value that is not a date is never guessed at: it is named, skipped, and the
// note is left exactly as it was.
func TestContract_MigrateProperties_RefusesAValueThatIsNotADate(t *testing.T) {
	_, root := newContractVault(t)
	original := "---\ntitle: Odd\ntype: note\ncreated: sometime last spring\nmodified: \"2026-04-05T07:27:34Z\"\n---\nbody\n"
	path := filepath.Join(root, "odd.md")
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	res := migrateJSON(t, root, "--write")
	found := false
	for _, s := range res.Skipped {
		if s.Path == "odd.md" && s.Field == "created" {
			found = true
			if s.Value != "sometime last spring" {
				t.Errorf("the refusal did not name the value: %+v", s)
			}
		}
	}
	if !found {
		t.Errorf("the unparseable value was not reported: %+v", res.Skipped)
	}
	after := readNote(t, path)
	if !strings.Contains(after, "created: sometime last spring\n") {
		t.Errorf("the refused value was rewritten:\n%s", after)
	}
	// The parseable sibling on the same note still migrates: a refusal is per
	// property, not per note.
	if !strings.Contains(after, "modified: 2026-04-05T07:27:34Z\n") {
		t.Errorf("the parseable sibling was not migrated:\n%s", after)
	}
}

// A property the vault's own schema declares as a date migrates too, and one
// the schema does not declare is reported and left alone. This is what makes
// the migration honor a user's schemas.yaml rather than a hardcoded key list.
func TestContract_MigrateProperties_HonorsSchemaDatesAndReportsTheRest(t *testing.T) {
	_, root := newContractVault(t)
	schemas := "types:\n  meeting:\n    name: Meeting\n    fields:\n      held:\n        type: date\n    required: []\n"
	if err := os.WriteFile(filepath.Join(root, ".2ndbrain", "schemas.yaml"), []byte(schemas), 0o644); err != nil {
		t.Fatal(err)
	}
	original := "---\ntitle: Standup\ntype: meeting\nheld: \"2026-04-05\"\nreviewed: \"2026-04-06\"\n---\nbody\n"
	if err := os.WriteFile(filepath.Join(root, "standup.md"), []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	res := migrateJSON(t, root, "--write")
	after := readNote(t, filepath.Join(root, "standup.md"))
	if !strings.Contains(after, "held: 2026-04-05T00:00:00Z\n") {
		t.Errorf("the schema-declared date field was not migrated:\n%s", after)
	}
	if !strings.Contains(after, `reviewed: "2026-04-06"`) {
		t.Errorf("an undeclared property was rewritten; the migration must leave the user's own fields alone:\n%s", after)
	}
	reported := false
	for _, f := range res.UserDateFields {
		if f.Field == "reviewed" && f.DateOnly == 1 {
			reported = true
		}
	}
	if !reported {
		t.Errorf("the user's own date-shaped property was not reported: %+v", res.UserDateFields)
	}
}

// The migration shares the polish recovery slot, exactly as repair-links,
// relink and unlink do, so one command undoes any of them.
func TestContract_MigrateProperties_IsUndoable(t *testing.T) {
	_, root := newContractVault(t)
	path := filepath.Join(root, "quoted.md")
	if err := os.WriteFile(path, []byte(quotedNote), 0o644); err != nil {
		t.Fatal(err)
	}

	if res := migrateJSON(t, root, "--write"); res.Changed != 1 {
		t.Fatalf("changed = %d, want 1", res.Changed)
	}
	if strings.Contains(readNote(t, path), `created: "`) {
		t.Fatal("the migration did not run")
	}

	if out, err := runCLIArgs(t, root, "polish", "quoted.md", "--undo"); err != nil {
		t.Fatalf("polish --undo: %v\n%s", err, out)
	}
	if got := readNote(t, path); got != quotedNote {
		t.Errorf("undo did not restore the original byte for byte:\n%s", got)
	}
}

// A template folder is not migrated: it is not indexed, its frontmatter is
// deliberately not valid YAML, and rewriting one would be a write into
// scaffolding the user never asked 2nb to touch.
func TestContract_MigrateProperties_SkipsTemplateFolders(t *testing.T) {
	_, root := newContractVault(t)
	writeCorePlugins(t, root, true)
	dir := filepath.Join(root, "templates")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	tmplPath := filepath.Join(dir, "note.md")
	tmpl := "---\ntitle: {{title}}\ncreated: \"2026-04-05T07:27:34Z\"\n---\n{{date}}\n"
	if err := os.WriteFile(tmplPath, []byte(tmpl), 0o644); err != nil {
		t.Fatal(err)
	}

	res := migrateJSON(t, root, "--write")
	for _, n := range res.Notes {
		if strings.HasPrefix(n.Path, "templates/") {
			t.Errorf("the migration rewrote a template: %+v", n)
		}
	}
	for _, s := range res.Skipped {
		if strings.HasPrefix(s.Path, "templates/") {
			t.Errorf("the template folder was walked at all: %+v", s)
		}
	}
	if got := readNote(t, tmplPath); got != tmpl {
		t.Errorf("the template was rewritten:\n%s", got)
	}
}

// A daily note is TITLED by its date. `title` must never be reported as a
// date-shaped property the user authors, because the report's own advice is to
// declare such a field `date` in schemas.yaml, and doing that for `title` would
// route it through CoerceDate and have this migration rewrite the title to
// `2026-09-04T00:00:00Z`: the exact regression 0.22.4 shipped to fix.
func TestContract_MigrateProperties_NeverSuggestsTypingATextFieldAsADate(t *testing.T) {
	_, root := newContractVault(t)
	if err := os.WriteFile(filepath.Join(root, "daily.md"), []byte(
		"---\ntitle: 2026-09-04\ntype: note\nstatus: draft\nid: 2026-09-04\ndate: 2026-09-04\n---\nstandup\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	res := migrateJSON(t, root)
	for _, f := range res.UserDateFields {
		if documentTextFields[f.Field] {
			t.Errorf("%q is a TEXT field Document mirrors; reporting it as a date-shaped property invites declaring it a date, which reintroduces the 0.22.4 title regression: %+v", f.Field, res.UserDateFields)
		}
	}
	// The user's OWN date-shaped property is still reported, so the exclusion
	// is narrow rather than a blanket silencing.
	found := false
	for _, f := range res.UserDateFields {
		if f.Field == "date" {
			found = true
		}
	}
	if !found {
		t.Errorf("the user's own `date` property stopped being reported: %+v", res.UserDateFields)
	}
	// And the title on disk is untouched either way.
	if !strings.Contains(readNote(t, filepath.Join(root, "daily.md")), "title: 2026-09-04\n") {
		t.Errorf("the date-shaped title was rewritten:\n%s", readNote(t, filepath.Join(root, "daily.md")))
	}
}

// An inline comment on a date line the migration RESPELLS is not carried onto
// the new value: the surgical writer drops a comment on a value it replaces,
// deliberately, and a respelling does not get an option that relaxes that on
// the most safety-critical function in the package. So the loss is REPORTED,
// per field, in the preview, before anything is written.
//
// It was silent before: other_lines_changed skips any line keyed by a migrated
// field, which is exactly the line the comment sat on, and both the command's
// help and the release notes claimed comments came back byte for byte.
func TestContract_MigrateProperties_ReportsACommentItCannotKeep(t *testing.T) {
	_, root := newContractVault(t)
	path := filepath.Join(root, "commented.md")
	original := "---\ntitle: Commented\n" +
		"created: \"2026-04-05T07:27:34Z\" # imported from an old vault\n" +
		"modified: \"2026-04-05T07:27:34Z\"\n" +
		"other: keep me # this one is never touched\n---\nbody\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	// The PREVIEW reports it, which is the point: the user sees the loss before
	// any byte is written.
	preview := migrateJSON(t, root)
	if len(preview.Notes) != 1 {
		t.Fatalf("preview notes = %d, want 1", len(preview.Notes))
	}
	dropped := preview.Notes[0].CommentsDropped
	if len(dropped) != 1 || dropped[0].Field != "created" {
		t.Fatalf("comments_dropped = %+v, want one entry for created", dropped)
	}
	if !strings.Contains(dropped[0].Comment, "imported from an old vault") {
		t.Errorf("the reported comment does not carry its text: %q", dropped[0].Comment)
	}

	res := migrateJSON(t, root, "--write")
	if len(res.Notes) != 1 || len(res.Notes[0].CommentsDropped) != 1 {
		t.Fatalf("the write run did not report the same loss: %+v", res.Notes)
	}
	after := readNote(t, path)
	// A comment on a property the migration did NOT touch still survives, which
	// is what makes this one exception rather than a general regression.
	if !strings.Contains(after, "other: keep me # this one is never touched") {
		t.Errorf("a comment on an untouched property was dropped:\n%s", after)
	}
	if !strings.Contains(after, "created: 2026-04-05T07:27:34Z") {
		t.Errorf("the date was not migrated:\n%s", after)
	}
	// modified carried no comment, so nothing is reported for it.
	for _, c := range res.Notes[0].CommentsDropped {
		if c.Field != "created" {
			t.Errorf("reported a dropped comment for %q, which never had one", c.Field)
		}
	}
}

// The read-back check runs over EVERY migrated field. It used to check only the
// alphabetically first one, so a note carrying two date properties had the
// second written to disk without ever being confirmed to read back as a date.
func TestContract_MigrateProperties_VerifiesEveryFieldNotJustTheFirst(t *testing.T) {
	_, root := newContractVault(t)
	schemas := "types:\n  meeting:\n    name: Meeting\n    fields:\n      zz-held:\n        type: datetime\n    required: []\n"
	if err := os.WriteFile(filepath.Join(root, ".2ndbrain", "schemas.yaml"), []byte(schemas), 0o644); err != nil {
		t.Fatal(err)
	}
	// "created" sorts first and "zz-held" last, so a check that reads fields[0]
	// alone never looks at the schema-declared one.
	original := "---\ntitle: Standup\ntype: meeting\ncreated: \"2026-04-05T07:27:34Z\"\nzz-held: \"2026-04-06T09:00:00Z\"\n---\nbody\n"
	if err := os.WriteFile(filepath.Join(root, "standup.md"), []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	res := migrateJSON(t, root, "--write")
	if res.Changed != 1 {
		t.Fatalf("changed = %d, want 1: %+v", res.Changed, res.Skipped)
	}
	after := readNote(t, filepath.Join(root, "standup.md"))
	for _, want := range []string{"created: 2026-04-05T07:27:34Z", "zz-held: 2026-04-06T09:00:00Z"} {
		if !strings.Contains(after, want) {
			t.Errorf("missing %q after migration:\n%s", want, after)
		}
	}
	if len(res.Notes) != 1 || len(res.Notes[0].Fields) != 2 {
		t.Fatalf("both date fields should be reported as migrated: %+v", res.Notes)
	}
}
