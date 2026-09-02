package cli

import (
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
// (the same trick TestContract_UnroutedSlotWarnings uses), and the provider is
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
