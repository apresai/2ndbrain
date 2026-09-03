package e2e_test

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// A query that matches nothing is an ORDINARY outcome, not an error, and the
// commands that can return zero rows still owe a machine consumer a parseable
// document on stdout.
//
// The regression this pins: `search`, `list` and `stale` printed their
// human hint to stderr and returned early, BEFORE the format branch, so
// `2nb search --json` wrote zero bytes to stdout and exited 0. Piping that to
// jq is a parse error on a completely normal query. Worse, the search envelope
// is also how degradation is reported, so a vault whose semantic channel had
// silently fallen back to BM25 returned no `mode` and no `warnings` either:
// an agent could not tell "nothing matched" from "semantic search is off".
//
// Credential-free by construction: BM25 runs with no AI provider configured,
// so this executes on every PR.
func TestBattery_EmptyResultsStillEmitJSON(t *testing.T) {
	home := isolatedHome(t)
	vault := filepath.Join(home, "vault")
	if out, code := runWithHome(t, home, "vault", "create", vault); code != 0 {
		t.Fatalf("vault create: exit %d: %s", code, out)
	}
	setObsidianOpenVault(t, home, vault)
	if out, code := runWithHome(t, home, "create", "--type", "note", "--title", "Only Note",
		"--content", "unrelated body text", "--vault", vault); code != 0 {
		t.Fatalf("create: exit %d: %s", code, out)
	}
	if out, code := runWithHome(t, home, "index", "--vault", vault); code != 0 {
		t.Fatalf("index: exit %d: %s", code, out)
	}

	t.Run("search envelope is complete when nothing matches", func(t *testing.T) {
		out, code := runWithHome(t, home, "search", "zzzznomatchtoken", "--vault", vault, "--json")
		if code != 0 {
			t.Fatalf("search: exit %d: %s", code, out)
		}
		if out == "" {
			t.Fatal("search --json wrote nothing to stdout on a zero-result query; a JSON consumer cannot parse an empty stream")
		}
		var env struct {
			Mode     *string   `json:"mode"`
			Warnings *[]string `json:"warnings"`
			Results  *[]any    `json:"results"`
		}
		if err := json.Unmarshal([]byte(out), &env); err != nil {
			t.Fatalf("search --json is not valid JSON: %v\n%s", err, out)
		}
		// Pointers: a MISSING key and a present-but-empty one are different
		// failures, and only the pointer form can tell them apart.
		if env.Mode == nil {
			t.Errorf("envelope is missing `mode`: %s", out)
		}
		if env.Warnings == nil {
			t.Errorf("envelope is missing `warnings` (it must not be omitempty: degradation is reported here): %s", out)
		}
		if env.Results == nil {
			t.Errorf("envelope has `results: null`; it must be an empty LIST so `.results[]` works: %s", out)
		} else if len(*env.Results) != 0 {
			t.Errorf("expected zero results, got %d: %s", len(*env.Results), out)
		}
	})

	t.Run("list emits an empty array when no document matches", func(t *testing.T) {
		out, code := runWithHome(t, home, "list", "--type", "nosuchtypexyz", "--vault", vault, "--json")
		if code != 0 {
			t.Fatalf("list: exit %d: %s", code, out)
		}
		var rows *[]any
		if err := json.Unmarshal([]byte(out), &rows); err != nil {
			t.Fatalf("list --json is not valid JSON (was it empty?): %v\n%q", err, out)
		}
		if rows == nil {
			t.Fatalf("list --json returned null, want []: %q", out)
		}
		if len(*rows) != 0 {
			t.Errorf("expected 0 rows, got %d: %s", len(*rows), out)
		}
	})

	t.Run("stale emits an empty array rather than null", func(t *testing.T) {
		out, code := runWithHome(t, home, "stale", "--since", "99999", "--vault", vault, "--json")
		if code != 0 {
			t.Fatalf("stale: exit %d: %s", code, out)
		}
		var rows *[]any
		if err := json.Unmarshal([]byte(out), &rows); err != nil {
			t.Fatalf("stale --json is not valid JSON: %v\n%q", err, out)
		}
		if rows == nil {
			t.Fatalf("stale --json returned null, want []; `jq '.[]'` errors on null: %q", out)
		}
	})

	// The fix is scoped to JSON on purpose. csv/tsv render zero rows as an
	// empty stream, and a literal "[]" would CORRUPT a csv consumer, so those
	// must keep emitting nothing.
	t.Run("delimited machine formats still emit nothing", func(t *testing.T) {
		for _, format := range []string{"csv", "tsv"} {
			out, code := runWithHome(t, home, "search", "zzzznomatchtoken", "--vault", vault, "--format", format)
			if code != 0 {
				t.Fatalf("search --format %s: exit %d: %s", format, code, out)
			}
			if out != "" {
				t.Errorf("search --format %s on zero results wrote %q to stdout; want an empty stream", format, out)
			}
		}
	})

	// raw and md are different: a result set has no document body to emit, and
	// output.Write already refuses them when it is reached. The zero-result
	// early return meant it was NOT reached, so the same command was refused
	// with one hit and silently exited 0 with none. The refusal has to depend on
	// the command, not on the row count.
	t.Run("raw and md are refused whether or not anything matched", func(t *testing.T) {
		for _, query := range []string{"zzzznomatchtoken", "unrelated"} {
			for _, format := range []string{"raw", "md"} {
				out, code := runWithHome(t, home, "search", query, "--vault", vault, "--format", format)
				if code == 0 {
					t.Errorf("search %q --format %s exited 0; it should be refused: %q", query, format, out)
				}
				if !strings.Contains(out, "document body") {
					t.Errorf("search %q --format %s refusal should name the missing document body: %q", query, format, out)
				}
			}
		}
	})

	// The human path must stay quiet on stdout: the hint belongs on stderr, and
	// a bare table header with no rows under it reads as a broken listing.
	t.Run("human output stays off stdout", func(t *testing.T) {
		for _, args := range [][]string{
			{"search", "zzzznomatchtoken", "--vault", vault},
			{"list", "--type", "nosuchtypexyz", "--vault", vault},
		} {
			out, code := runWithHome(t, home, args...)
			if code != 0 {
				t.Fatalf("%v: exit %d: %s", args, code, out)
			}
			if out != "" {
				t.Errorf("%v wrote %q to stdout; the empty-state hint belongs on stderr", args, out)
			}
		}
	})
}
