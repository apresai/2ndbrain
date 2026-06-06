package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestContract_SearchEnvelopeShape pins the JSON shape of `2nb search --json`
// that the Obsidian plugin (and Swift app) decode. Runs on the BM25 path, so it
// needs no AI provider. Field names here are the contract — changing them
// silently breaks every consumer.
func TestContract_SearchEnvelopeShape(t *testing.T) {
	_, root := newContractVault(t)

	md := "---\nid: b1\ntitle: Battery Notes\ntype: note\nstatus: draft\n---\n" +
		"The quick brown fox. Distinctiveword appears here for retrieval.\n"
	if err := os.WriteFile(filepath.Join(root, "battery.md"), []byte(md), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runCLIArgs(t, root, "index"); err != nil {
		t.Fatalf("index: %v", err)
	}

	out, err := runCLIArgs(t, root, "search", "--json", "Distinctiveword")
	if err != nil {
		t.Fatalf("search: %v", err)
	}

	var env struct {
		Mode     string   `json:"mode"`
		Warnings []string `json:"warnings"`
		Results  []struct {
			DocID       string  `json:"doc_id"`
			Path        string  `json:"path"`
			Title       string  `json:"title"`
			ChunkID     string  `json:"chunk_id"`
			HeadingPath string  `json:"heading_path"`
			Content     string  `json:"content"`
			Score       float64 `json:"score"`
			DocType     string  `json:"type"`
			Status      string  `json:"status"`
		} `json:"results"`
	}
	if err := json.Unmarshal(out, &env); err != nil {
		t.Fatalf("search --json did not produce the expected envelope: %v\nraw: %s", err, out)
	}
	if env.Mode == "" {
		t.Errorf("envelope missing mode; raw: %s", out)
	}
	if len(env.Results) == 0 {
		t.Fatalf("expected at least one result; raw: %s", out)
	}
	r := env.Results[0]
	if r.DocID == "" || r.Path == "" || r.Title == "" {
		t.Errorf("result missing required fields: %+v", r)
	}
}

// TestContract_AskSourcesAreStrings pins that `2nb ask --json` emits sources as
// a string array (vault-relative paths), not objects. The Obsidian plugin
// shipped a bug assuming objects; this is the regression guard for the contract.
func TestContract_AskSourcesAreStrings(t *testing.T) {
	data, err := json.Marshal(AskResponse{
		Mode:    "hybrid",
		Answer:  "an answer",
		Sources: []string{"a.md", "notes/b.md"},
	})
	if err != nil {
		t.Fatal(err)
	}

	var asStrings struct {
		Sources []string `json:"sources"`
	}
	if err := json.Unmarshal(data, &asStrings); err != nil {
		t.Fatalf("sources must decode as []string: %v", err)
	}
	if len(asStrings.Sources) != 2 || asStrings.Sources[0] != "a.md" {
		t.Errorf("unexpected sources: %v", asStrings.Sources)
	}

	var asObjects struct {
		Sources []map[string]any `json:"sources"`
	}
	if err := json.Unmarshal(data, &asObjects); err == nil {
		t.Error("sources decoded as []object, but the CLI contract is []string")
	}
}

// TestMeta_RefusesCanvasAndBase proves the data-loss guard: `2nb meta --set`
// must refuse to write a .canvas/.base file and leave it byte-for-byte intact.
func TestMeta_RefusesCanvasAndBase(t *testing.T) {
	_, root := newContractVault(t)

	cases := []struct {
		name    string
		content string
	}{
		{"board.canvas", `{"nodes":[{"id":"n1","type":"text","text":"hi"}],"edges":[]}`},
		{"cfg.base", "root:\n  key: value\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := filepath.Join(root, tc.name)
			if err := os.WriteFile(p, []byte(tc.content), 0o644); err != nil {
				t.Fatal(err)
			}
			before, _ := os.ReadFile(p)

			if _, err := runCLIArgs(t, root, "meta", tc.name, "--set", "status=changed"); err == nil {
				t.Errorf("expected meta --set on %s to be refused", tc.name)
			}

			after, _ := os.ReadFile(p)
			if !bytes.Equal(before, after) {
				t.Errorf("%s modified despite refusal:\nbefore=%q\nafter=%q", tc.name, before, after)
			}
		})
	}
}
