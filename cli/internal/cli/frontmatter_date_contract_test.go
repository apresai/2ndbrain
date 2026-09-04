package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/apresai/2ndbrain/internal/vault"
)

// The controlled pair: two notes carrying the SAME instant, one with the date
// quoted (what `2nb create` and `2nb meta` write) and one with it unquoted
// (what Obsidian's own Date property writes). They must be indistinguishable to
// the index and to every command that reads a date.
//
// Before this they were not. yaml.v3 resolves an unquoted ISO-8601 scalar to
// time.Time, the frontmatter reader asserted `.(string)`, and the assertion
// failed without a word: documents.modified_at stayed EMPTY, so `stale` (which
// filters `modified_at != ”`) omitted the note entirely. Hand-edited and
// imported notes were invisible; 2nb-authored ones were fine.
const (
	fmDateQuoted   = "---\nid: q\ntitle: Quoted\ntype: note\nstatus: draft\ncreated: \"2020-01-01T00:00:00Z\"\nmodified: \"2020-01-01T00:00:00Z\"\n---\nquoted body\n"
	fmDateUnquoted = "---\nid: u\ntitle: Unquoted\ntype: note\nstatus: draft\ncreated: 2020-01-01T00:00:00Z\nmodified: 2020-01-01T00:00:00Z\n---\nunquoted body\n"
)

// newDatePairVault writes the pair and indexes it.
func newDatePairVault(t *testing.T) string {
	t.Helper()
	_, root := newContractVault(t)
	for name, body := range map[string]string{
		"quoted.md":   fmDateQuoted,
		"unquoted.md": fmDateUnquoted,
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if out, err := runCLIArgs(t, root, "index"); err != nil {
		t.Fatalf("index: %v\n%s", err, out)
	}
	return root
}

func TestContract_UnquotedFrontmatterDateReachesTheIndex(t *testing.T) {
	root := newDatePairVault(t)

	v, err := vault.Open(root)
	if err != nil {
		t.Fatalf("open vault: %v", err)
	}
	defer v.Close()

	dates := map[string][2]string{}
	rows, err := v.DB.Conn().Query(`SELECT path, created_at, modified_at FROM documents ORDER BY path`)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var path, created, modified string
		if err := rows.Scan(&path, &created, &modified); err != nil {
			t.Fatal(err)
		}
		dates[path] = [2]string{created, modified}
	}

	for _, path := range []string{"quoted.md", "unquoted.md"} {
		got, ok := dates[path]
		if !ok {
			t.Fatalf("%s is not in the index at all", path)
		}
		if got[0] != "2020-01-01T00:00:00Z" {
			t.Errorf("%s created_at = %q, want 2020-01-01T00:00:00Z", path, got[0])
		}
		if got[1] != "2020-01-01T00:00:00Z" {
			t.Errorf("%s modified_at = %q, want 2020-01-01T00:00:00Z", path, got[1])
		}
	}
	if dates["quoted.md"] != dates["unquoted.md"] {
		t.Errorf("the two notes carry the same instant but indexed differently: quoted=%v unquoted=%v",
			dates["quoted.md"], dates["unquoted.md"])
	}
}

func TestContract_StaleFindsAnUnquotedDate(t *testing.T) {
	root := newDatePairVault(t)

	out, err := runCLIArgs(t, root, "stale", "--since", "30", "--json")
	if err != nil {
		t.Fatalf("stale: %v\n%s", err, out)
	}
	var got []StaleDoc
	if err := json.Unmarshal(jsonPortion(out), &got); err != nil {
		t.Fatalf("stale --json is not parseable: %v\n%s", err, out)
	}
	paths := map[string]bool{}
	for _, d := range got {
		paths[d.Path] = true
	}
	for _, want := range []string{"quoted.md", "unquoted.md"} {
		if !paths[want] {
			t.Errorf("stale --since 30 did not return %s; it returned %v", want, paths)
		}
	}
}

// `meta --get modified` must read the same for both forms. The default (pretty)
// branch printed Go's own "2020-01-01 00:00:00 +0000 UTC" for the unquoted one,
// so the plain text a script captures depended on how the date was written.
func TestContract_MetaGetDateRoundTrip(t *testing.T) {
	root := newDatePairVault(t)

	for _, path := range []string{"quoted.md", "unquoted.md"} {
		t.Run(path, func(t *testing.T) {
			out, err := runCLIArgs(t, root, "meta", path, "--get", "modified")
			if err != nil {
				t.Fatalf("meta --get: %v\n%s", err, out)
			}
			if got := strings.TrimSpace(string(out)); got != "2020-01-01T00:00:00Z" {
				t.Errorf("meta --get modified = %q, want 2020-01-01T00:00:00Z", got)
			}

			out, err = runCLIArgs(t, root, "meta", path, "--get", "modified", "--json")
			if err != nil {
				t.Fatalf("meta --get --json: %v\n%s", err, out)
			}
			var v string
			if err := json.Unmarshal([]byte(strings.TrimSpace(string(out))), &v); err != nil {
				t.Fatalf("meta --get --json is not a JSON string: %v\n%s", err, out)
			}
			if v != "2020-01-01T00:00:00Z" {
				t.Errorf("meta --get modified --json = %q, want 2020-01-01T00:00:00Z", v)
			}
		})
	}
}

// GUARD (passes at base, must keep passing): reading a date must never REWRITE
// it. Serialize re-marshals every key of Frontmatter, so normalizing the parsed
// map instead of the struct fields would make an unrelated `meta --set` requote
// a date line the user never touched. The whole `modified:` line stays byte for
// byte what it was.
func TestContract_MetaSetLeavesAnUnquotedDateAlone(t *testing.T) {
	root := newDatePairVault(t)
	path := filepath.Join(root, "unquoted.md")

	if out, err := runCLIArgs(t, root, "meta", "unquoted.md", "--set", "status=complete"); err != nil {
		t.Fatalf("meta --set: %v\n%s", err, out)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(after), "modified: 2020-01-01T00:00:00Z\n") {
		t.Errorf("meta --set rewrote the untouched date line:\n%s", after)
	}
	if strings.Contains(string(after), `modified: "2020-01-01T00:00:00Z"`) {
		t.Errorf("meta --set requoted the untouched date line:\n%s", after)
	}
}

// A daily note is the case that makes the two readings concrete: it is TITLED
// by its date and DATED by the same date. The title must survive as the text
// the file shows, and the date must normalize, in one note, end to end through
// the index.
//
// Formatting the resolved value for both was the first attempt at this fix and
// it corrupted the title: `2026-09-04` and `2026-09-04T00:00:00Z` resolve to
// the same time.Time, so `list`, `search` and `meta --get` all showed the
// timestamp for a note whose file says the date.
func TestContract_ADailyNoteKeepsItsTitle(t *testing.T) {
	_, root := newContractVault(t)
	if err := os.WriteFile(filepath.Join(root, "daily.md"), []byte(
		"---\ntitle: 2026-09-04\ntype: note\nstatus: draft\ntags: [2026-09-04, 42]\nmodified: 2026-09-04\n---\nstandup\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := runCLIArgs(t, root, "index"); err != nil {
		t.Fatalf("index: %v\n%s", err, out)
	}

	out, err := runCLIArgs(t, root, "list", "--json")
	if err != nil {
		t.Fatalf("list: %v\n%s", err, out)
	}
	var rows []struct {
		Path       string `json:"path"`
		Title      string `json:"title"`
		ModifiedAt string `json:"modified_at"`
	}
	if err := json.Unmarshal(jsonPortion(out), &rows); err != nil {
		t.Fatalf("list --json: %v\n%s", err, out)
	}
	if len(rows) != 1 {
		t.Fatalf("want one row, got %d", len(rows))
	}
	if rows[0].Title != "2026-09-04" {
		t.Errorf("indexed title = %q, want the text the note carries, 2026-09-04", rows[0].Title)
	}
	if rows[0].ModifiedAt != "2026-09-04T00:00:00Z" {
		t.Errorf("indexed modified_at = %q, want the normalized 2026-09-04T00:00:00Z", rows[0].ModifiedAt)
	}

	// The tag keeps its text too, so `list --tag 2026-09-04` can find it.
	out, err = runCLIArgs(t, root, "tags", "--json")
	if err != nil {
		t.Fatalf("tags: %v\n%s", err, out)
	}
	var tags []struct {
		Tag string `json:"tag"`
	}
	if err := json.Unmarshal(jsonPortion(out), &tags); err != nil {
		t.Fatalf("tags --json: %v\n%s", err, out)
	}
	got := map[string]bool{}
	for _, tg := range tags {
		got[tg.Tag] = true
	}
	for _, want := range []string{"2026-09-04", "42"} {
		if !got[want] {
			t.Errorf("tag %q is missing; tags = %v", want, got)
		}
	}

	// meta --get answers what the FIELD says, in every format.
	for _, tc := range []struct {
		argv []string
		want string
	}{
		{[]string{"meta", "daily.md", "--get", "title"}, "2026-09-04"},
		{[]string{"meta", "daily.md", "--get", "title", "--json"}, `"2026-09-04"`},
		{[]string{"meta", "daily.md", "--get", "title", "--format", "csv"}, "2026-09-04"},
	} {
		out, err := runCLIArgs(t, root, tc.argv...)
		if err != nil {
			t.Fatalf("%v: %v\n%s", tc.argv, err, out)
		}
		if got := strings.TrimSpace(string(out)); got != tc.want {
			t.Errorf("%v = %q, want %q", tc.argv, got, tc.want)
		}
	}
}

// A single scalar payload is ONE PLAIN cell under csv/tsv. The JSON-record
// fallback quoted it and the csv writer escaped those quotes, so
// `config get ai.provider --format csv` emitted """bedrock""": three layers to
// unwrap for one word. Wiring config get to honor --format is what first made
// this reachable, so it is covered here rather than in the enumeration test,
// which asserts one thing per command and only under --format json.
func TestContract_ScalarPayloadIsOnePlainCell(t *testing.T) {
	_, root := newContractVault(t)
	if err := os.WriteFile(filepath.Join(root, "n.md"), []byte(
		"---\ntitle: Note\ntype: note\nstatus: draft\n---\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		argv []string
		want string
	}{
		{[]string{"config", "get", "ai.provider"}, "bedrock"},
		{[]string{"meta", "n.md", "--get", "title"}, "Note"},
	} {
		for _, format := range []string{"csv", "tsv"} {
			argv := append(append([]string{}, tc.argv...), "--format", format)
			out, err := runCLIArgs(t, root, argv...)
			if err != nil {
				t.Fatalf("%v: %v\n%s", argv, err, out)
			}
			if got := strings.TrimSpace(string(out)); got != tc.want {
				t.Errorf("%v = %q, want the plain cell %q", argv, got, tc.want)
			}
		}
	}
}

// End to end on disk: a frontmatter-only command must not touch the body, and
// must not touch a property nobody edited.
//
// Two bugs met here. The reader ended the frontmatter at the FIRST "\n---"
// anywhere in the note with no check that it was at end of file, so a body
// containing a horizontal rule was truncated on read; and Serialize rewrites
// the whole file from that body, so `meta --set` then wrote the truncated
// version to disk. Separately the writer re-marshaled EVERY key from the parsed
// map, so an unrelated edit rewrote untouched properties. Both verified against
// the released 0.22.3.
func TestContract_MetaSetTouchesOnlyWhatItWasAsked(t *testing.T) {
	_, root := newContractVault(t)
	path := filepath.Join(root, "n.md")
	src := "---\n" +
		"title: 2026-09-04\n" +
		"type: note\n" +
		"status: draft\n" +
		"modified: 2020-01-01\n" +
		"id: 007\n" +
		"num: 3.50\n" +
		"tags: [2026-09-04, 42, real]\n" +
		"note: v # keep me\n" +
		"---\n" +
		"real body that must survive\n" +
		"\n" +
		"---\n" +
		"\n" +
		"and more under a horizontal rule\n"
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	if out, err := runCLIArgs(t, root, "meta", "n.md", "--set", "status=complete"); err != nil {
		t.Fatalf("meta --set: %v\n%s", err, out)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := string(after)

	for _, line := range []string{
		"title: 2026-09-04\n",
		"modified: 2020-01-01\n",
		"id: 007\n",
		"num: 3.50\n",
		"tags: [2026-09-04, 42, real]\n",
		"note: v # keep me\n",
	} {
		if !strings.Contains(got, line) {
			t.Errorf("the untouched property %q was rewritten:\n%s", strings.TrimSuffix(line, "\n"), got)
		}
	}
	if !strings.Contains(got, "status: complete") {
		t.Errorf("the property that WAS set is missing:\n%s", got)
	}
	for _, want := range []string{"real body that must survive", "and more under a horizontal rule"} {
		if !strings.Contains(got, want) {
			t.Errorf("the body lost %q:\n%s", want, got)
		}
	}
}

// The tag commands used to assert item.(string) on the parsed map, so every
// unquoted date, integer and boolean tag was DROPPED from the list they then
// wrote back: one `tag add` deleted the others from the file.
func TestContract_TagAddKeepsEveryExistingTag(t *testing.T) {
	_, root := newContractVault(t)
	path := filepath.Join(root, "n.md")
	if err := os.WriteFile(path, []byte(
		"---\ntitle: T\ntype: note\nstatus: draft\ntags: [2026-09-04, 42, true, real]\n---\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if out, err := runCLIArgs(t, root, "tag", "add", "n.md", "extra"); err != nil {
		t.Fatalf("tag add: %v\n%s", err, out)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"2026-09-04", "42", "true", "real", "extra"} {
		if !strings.Contains(string(after), want) {
			t.Errorf("tag %q was dropped from the file by an unrelated tag add:\n%s", want, after)
		}
	}
}
