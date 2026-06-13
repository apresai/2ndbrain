package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Phase 2 (structure & stats) contract tests. Each builds the exact argv a
// shell user or the GUI sends and asserts the CLI accepts it (exit 0, valid
// JSON where applicable, porcelain accepted). These commands are read-only.

// seedStructureVault writes two documents into the vault (one with headings,
// tags, and aliases; one nested in a subfolder) and indexes them, so the
// structure/stats commands have real content to report over.
func seedStructureVault(t *testing.T, root string) {
	t.Helper()

	notesDoc := `---
title: Notes Doc
type: note
tags: [alpha, beta]
aliases: [nd, primary-note]
---
# Notes Doc

Intro preamble before the first subheading.

## Context

Some context text here with several words to count.

## Decision

The decision body.
`
	if err := os.WriteFile(filepath.Join(root, "notes-doc.md"), []byte(notesDoc), 0o644); err != nil {
		t.Fatal(err)
	}

	subDir := filepath.Join(root, "research")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}
	subDoc := `---
title: Sub Doc
type: note
tags: [alpha]
---
# Sub Doc

Body of the nested document.
`
	if err := os.WriteFile(filepath.Join(subDir, "sub-doc.md"), []byte(subDoc), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := runCLIArgs(t, root, "index"); err != nil {
		t.Fatalf("index: %v", err)
	}
}

func TestContract_Outline(t *testing.T) {
	_, root := newContractVault(t)
	seedStructureVault(t, root)

	out, err := runCLIArgs(t, root, "outline", "notes-doc.md", "--json")
	if err != nil {
		t.Fatalf("outline --json: %v (out=%s)", err, out)
	}

	var res struct {
		Path     string `json:"path"`
		Title    string `json:"title"`
		Sections []struct {
			HeadingPath string `json:"heading_path"`
			Level       int    `json:"level"`
		} `json:"sections"`
	}
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatalf("outline output is not valid JSON: %v\n%s", err, out)
	}
	if len(res.Sections) == 0 {
		t.Fatalf("expected sections, got none: %s", out)
	}
	text := string(out)
	for _, h := range []string{"Notes Doc", "Context", "Decision"} {
		if !strings.Contains(text, h) {
			t.Errorf("expected heading %q in outline, got: %s", h, text)
		}
	}

	// Porcelain must be accepted (exit 0).
	if _, err := runCLIArgs(t, root, "outline", "notes-doc.md", "--json", "--porcelain"); err != nil {
		t.Fatalf("outline --json --porcelain: %v", err)
	}
}

func TestContract_Wordcount(t *testing.T) {
	_, root := newContractVault(t)
	seedStructureVault(t, root)

	out, err := runCLIArgs(t, root, "wordcount", "notes-doc.md", "--json")
	if err != nil {
		t.Fatalf("wordcount --json: %v (out=%s)", err, out)
	}

	var res struct {
		Path       string `json:"path"`
		Words      int    `json:"words"`
		Characters int    `json:"characters"`
		Headings   int    `json:"headings"`
	}
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatalf("wordcount output is not valid JSON: %v\n%s", err, out)
	}
	if res.Words <= 0 {
		t.Errorf("expected positive word count, got %d", res.Words)
	}
	if res.Characters <= 0 {
		t.Errorf("expected positive character count, got %d", res.Characters)
	}
	// notes-doc.md has H1 (Notes Doc) + two H2 (Context, Decision) = 3 headings.
	if res.Headings != 3 {
		t.Errorf("expected 3 headings, got %d", res.Headings)
	}

	if _, err := runCLIArgs(t, root, "wordcount", "notes-doc.md", "--json", "--porcelain"); err != nil {
		t.Fatalf("wordcount --json --porcelain: %v", err)
	}
}

func TestContract_Folders(t *testing.T) {
	_, root := newContractVault(t)
	seedStructureVault(t, root)

	out, err := runCLIArgs(t, root, "folders", "--json")
	if err != nil {
		t.Fatalf("folders --json: %v (out=%s)", err, out)
	}

	var folders []struct {
		Folder string `json:"folder"`
		Count  int    `json:"count"`
	}
	if err := json.Unmarshal(out, &folders); err != nil {
		t.Fatalf("folders output is not valid JSON: %v\n%s", err, out)
	}
	seen := map[string]int{}
	for _, f := range folders {
		seen[f.Folder] = f.Count
	}
	if seen["(root)"] != 1 {
		t.Errorf("expected 1 doc in (root), got %d (%+v)", seen["(root)"], folders)
	}
	if seen["research"] != 1 {
		t.Errorf("expected 1 doc in research/, got %d (%+v)", seen["research"], folders)
	}

	if _, err := runCLIArgs(t, root, "folders", "--json", "--porcelain"); err != nil {
		t.Fatalf("folders --json --porcelain: %v", err)
	}
}

func TestContract_Tags(t *testing.T) {
	_, root := newContractVault(t)
	seedStructureVault(t, root)

	out, err := runCLIArgs(t, root, "tags", "--json")
	if err != nil {
		t.Fatalf("tags --json: %v (out=%s)", err, out)
	}

	var tags []struct {
		Tag   string `json:"tag"`
		Count int    `json:"count"`
	}
	if err := json.Unmarshal(out, &tags); err != nil {
		t.Fatalf("tags output is not valid JSON: %v\n%s", err, out)
	}
	counts := map[string]int{}
	for _, tc := range tags {
		counts[tc.Tag] = tc.Count
	}
	// alpha is on both docs (2); beta only on notes-doc (1).
	if counts["alpha"] != 2 {
		t.Errorf("expected alpha count 2, got %d (%+v)", counts["alpha"], tags)
	}
	if counts["beta"] != 1 {
		t.Errorf("expected beta count 1, got %d (%+v)", counts["beta"], tags)
	}

	// Bare `tags` (parent default) and the explicit `tags list` subcommand both work.
	if _, err := runCLIArgs(t, root, "tags", "--json", "--porcelain"); err != nil {
		t.Fatalf("tags --json --porcelain: %v", err)
	}
	if _, err := runCLIArgs(t, root, "tags", "list", "--json"); err != nil {
		t.Fatalf("tags list --json: %v", err)
	}
}

func TestContract_Aliases(t *testing.T) {
	_, root := newContractVault(t)
	seedStructureVault(t, root)

	out, err := runCLIArgs(t, root, "aliases", "--json")
	if err != nil {
		t.Fatalf("aliases --json: %v (out=%s)", err, out)
	}

	var aliases []struct {
		Alias string `json:"alias"`
		Path  string `json:"path"`
		Title string `json:"title"`
	}
	if err := json.Unmarshal(out, &aliases); err != nil {
		t.Fatalf("aliases output is not valid JSON: %v\n%s", err, out)
	}
	byAlias := map[string]string{}
	for _, a := range aliases {
		byAlias[a.Alias] = a.Path
	}
	for _, alias := range []string{"nd", "primary-note"} {
		if byAlias[alias] != "notes-doc.md" {
			t.Errorf("alias %q -> %q, want notes-doc.md (%+v)", alias, byAlias[alias], aliases)
		}
	}

	if _, err := runCLIArgs(t, root, "aliases", "--json", "--porcelain"); err != nil {
		t.Fatalf("aliases --json --porcelain: %v", err)
	}
}
