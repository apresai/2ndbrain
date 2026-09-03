package cli

import (
	"encoding/csv"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The serious half of the zero-result bug: the search envelope is ALSO how a
// vault reports that its semantic channel has degraded to keyword-only, so a
// degraded vault whose query happened to match nothing used to return no
// `mode` and no `warnings` either. An agent could not tell "nothing matched"
// from "semantic search is off".
//
// Credential-free by construction. VectorCompat silently falls back when a
// vault holds ZERO embeddings, so a synthetic vector is seeded to get past that
// (the same trick TestSearchJSONCarriesUnroutedCause uses), and the provider is
// set to a name that will never register an embedder. No provider is called.
func TestContract_DegradedSearchWithZeroResultsStillWarns(t *testing.T) {
	v, root := newContractVault(t)
	v.Config.AI.Provider = "no-provider"
	if err := v.Config.Save(v.DotDir); err != nil {
		t.Fatalf("save config: %v", err)
	}

	md := "---\nid: d1\ntitle: Only Note\ntype: note\nstatus: draft\n---\nDistinctiveword body.\n"
	if err := os.WriteFile(filepath.Join(root, "only.md"), []byte(md), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := runCLIArgs(t, root, "index"); err != nil {
		t.Fatalf("index: %v\n%s", err, out)
	}
	var docID string
	if err := v.DB.Conn().QueryRow(`SELECT id FROM documents LIMIT 1`).Scan(&docID); err != nil {
		t.Fatalf("no indexed document: %v", err)
	}
	if err := v.DB.SetEmbedding(docID, make([]float32, 1024), "synthetic", "h"); err != nil {
		t.Fatal(err)
	}

	// A token that matches nothing. This is the combination that used to
	// produce zero bytes on stdout.
	out, err := runCLIArgs(t, root, "search", "--json", "zzzznomatchtoken")
	if err != nil {
		t.Fatalf("search: %v\n%s", err, out)
	}
	if len(out) == 0 {
		t.Fatal("degraded search with zero results wrote nothing to stdout; the degradation is invisible to a JSON consumer")
	}
	var env struct {
		Mode     *string   `json:"mode"`
		Warnings *[]string `json:"warnings"`
		Results  *[]any    `json:"results"`
	}
	if err := json.Unmarshal(out, &env); err != nil {
		t.Fatalf("parse envelope: %v\n%s", err, out)
	}
	if env.Mode == nil || *env.Mode != "keyword" {
		t.Errorf("mode = %v, want \"keyword\" (the vault degraded): %s", env.Mode, out)
	}
	if env.Results == nil {
		t.Errorf("results is null, want []: %s", out)
	} else if len(*env.Results) != 0 {
		t.Errorf("expected zero results, got %d: %s", len(*env.Results), out)
	}
	if env.Warnings == nil || len(*env.Warnings) == 0 {
		t.Fatalf("a degraded vault reported no warnings on a zero-result query; this is the case an agent cannot otherwise detect: %s", out)
	}
	// The stable prefix agents are told to match on (docs/agent-teaching.md).
	if joined := strings.Join(*env.Warnings, "\n"); !strings.Contains(joined, "semantic search disabled: ") {
		t.Errorf("warning lost its stable %q prefix: %v", "semantic search disabled: ", *env.Warnings)
	}
}

// Every --json listing returns an empty LIST, never `null`, because `jq '.[]'`
// rejects null and an empty vault is an ordinary state rather than an error.
func TestContract_EmptyListingsAreEmptyArraysNotNull(t *testing.T) {
	_, root := newContractVault(t)

	for _, tc := range []struct {
		name string
		argv []string
	}{
		{"tags", []string{"tags", "--json", "--porcelain"}},
		{"aliases", []string{"aliases", "--json", "--porcelain"}},
		{"orphans", []string{"orphans", "--json", "--porcelain"}},
		{"deadends", []string{"deadends", "--json", "--porcelain"}},
		{"unresolved", []string{"unresolved", "--json", "--porcelain"}},
		{"stale", []string{"stale", "--json", "--porcelain"}},
		{"list", []string{"list", "--json", "--porcelain"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := runCLIArgs(t, root, tc.argv...)
			if err != nil {
				t.Fatalf("%s: %v\n%s", tc.name, err, out)
			}
			if len(out) == 0 {
				t.Fatalf("%s --json wrote nothing; a JSON consumer cannot parse an empty stream", tc.name)
			}
			var rows *[]any
			if err := json.Unmarshal(out, &rows); err != nil {
				t.Fatalf("%s --json is not valid JSON: %v\n%q", tc.name, err, out)
			}
			if rows == nil {
				t.Fatalf("%s --json returned null, want []", tc.name)
			}
			if len(*rows) != 0 {
				t.Errorf("%s: expected 0 rows on a fresh vault, got %d: %s", tc.name, len(*rows), out)
			}
		})
	}
}

// The same listings under the OTHER two machine formats, which the 0.21.1 fix
// left behind because it normalized the nil slice at the json call sites only.
//
// yaml went through json.Marshal on its way to the encoder, so a nil slice
// arrived as `null` and printed `null`: `2nb orphans --yaml` disagreed with its
// own json view AND with `2nb tasks --yaml`, whose slice merely happens to be
// built non-nil at its construction site. csv and tsv were worse: a zero-row
// slice missed the struct-slice branch entirely and fell through to the
// JSON-record fallback, so the stream's one and only cell was the literal
// `null` or `[]`, a JSON value inside a csv document.
func TestContract_EmptyListingsUnderYAMLAndCSV(t *testing.T) {
	_, root := newContractVault(t)

	commands := [][]string{
		{"tags"}, {"aliases"}, {"orphans"}, {"deadends"}, {"unresolved"},
		{"stale", "--since", "99999"}, {"list"},
	}

	for _, argv := range commands {
		name := argv[0]

		t.Run(name+" --yaml", func(t *testing.T) {
			out, err := runCLIArgs(t, root, append(append([]string{}, argv...), "--yaml", "--porcelain")...)
			if err != nil {
				t.Fatalf("%s --yaml: %v\n%s", name, err, out)
			}
			got := strings.TrimSpace(string(out))
			if got != "[]" {
				t.Errorf("%s --yaml on an empty vault = %q, want %q", name, got, "[]")
			}
		})

		t.Run(name+" --format csv", func(t *testing.T) {
			out, err := runCLIArgs(t, root, append(append([]string{}, argv...), "--format", "csv", "--porcelain")...)
			if err != nil {
				t.Fatalf("%s --format csv: %v\n%s", name, err, out)
			}
			text := strings.TrimSpace(string(out))
			for _, bad := range []string{"null", "[]"} {
				if text == bad {
					t.Fatalf("%s --format csv wrote the JSON record %q into a delimited stream", name, bad)
				}
			}
			recs, rerr := csv.NewReader(strings.NewReader(string(out))).ReadAll()
			if rerr != nil {
				t.Fatalf("%s --format csv is not a csv document (%v):\n%s", name, rerr, out)
			}
			// A header naming the columns, and no data rows.
			if len(recs) != 1 {
				t.Fatalf("%s --format csv on an empty vault = %d records, want the header alone: %q", name, len(recs), out)
			}
			if len(recs[0]) == 0 || recs[0][0] == "" {
				t.Errorf("%s --format csv header is empty: %q", name, out)
			}
		})
	}
}
