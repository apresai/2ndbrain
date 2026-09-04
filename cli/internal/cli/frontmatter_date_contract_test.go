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
// filters `modified_at != ''`) omitted the note entirely. Hand-edited and
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
