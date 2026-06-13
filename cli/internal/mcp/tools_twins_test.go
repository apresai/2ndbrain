package mcp

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/apresai/2ndbrain/internal/document"
	"github.com/apresai/2ndbrain/internal/store"
	"github.com/apresai/2ndbrain/internal/testutil"
)

// TestHandleKBBacklinks seeds a resolved inbound link and asserts kb_backlinks
// returns the source document as a backlink of the target.
func TestHandleKBBacklinks(t *testing.T) {
	h, v := makeHandlers(t)
	ctx := context.Background()

	target := testutil.CreateAndIndex(t, v, "Target Doc", "note", "target body")
	source := testutil.CreateAndIndex(t, v, "Source Doc", "note", "See [[Target Doc]].")
	if _, err := v.DB.Conn().Exec(
		`INSERT INTO links (source_id, target_id, target_raw, resolved) VALUES (?, ?, ?, 1)`,
		source.ID, target.ID, "Target Doc",
	); err != nil {
		t.Fatalf("seed resolved link: %v", err)
	}

	res, err := h.handleKBBacklinks(ctx, makeRequest(map[string]any{"path": target.Path}))
	if err != nil {
		t.Fatalf("handleKBBacklinks returned error: %v", err)
	}
	text := resultText(t, res)
	if res.IsError {
		t.Fatalf("result is an error: %s", text)
	}

	var refs []store.LinkRef
	if err := json.Unmarshal([]byte(text), &refs); err != nil {
		t.Fatalf("response is not a LinkRef array: %v\n%s", err, text)
	}
	if len(refs) != 1 {
		t.Fatalf("expected 1 backlink, got %d:\n%s", len(refs), text)
	}
	if refs[0].Path != source.Path {
		t.Errorf("backlink source path = %q, want %q", refs[0].Path, source.Path)
	}
	if !refs[0].Resolved {
		t.Error("backlink should be marked resolved")
	}
}

// TestHandleKBBacklinks_NotFound verifies a path with no indexed document
// returns an error result.
func TestHandleKBBacklinks_NotFound(t *testing.T) {
	h, _ := makeHandlers(t)
	res, err := h.handleKBBacklinks(context.Background(), makeRequest(map[string]any{"path": "nope.md"}))
	if err != nil {
		t.Fatalf("unexpected hard error: %v", err)
	}
	if !res.IsError {
		t.Error("expected error result for missing document")
	}
}

// TestHandleKBBacklinks_PathTraversal verifies traversal is rejected.
func TestHandleKBBacklinks_PathTraversal(t *testing.T) {
	h, _ := makeHandlers(t)
	res, err := h.handleKBBacklinks(context.Background(), makeRequest(map[string]any{"path": "../../etc/passwd"}))
	if err != nil {
		t.Fatalf("unexpected hard error: %v", err)
	}
	if !res.IsError {
		t.Error("expected error result for path traversal")
	}
}

// TestHandleKBLinks seeds one resolved and one broken outbound link and asserts
// kb_links returns both with the correct resolved flags.
func TestHandleKBLinks(t *testing.T) {
	h, v := makeHandlers(t)
	ctx := context.Background()

	target := testutil.CreateAndIndex(t, v, "Real Target", "note", "real body")
	source := testutil.CreateAndIndex(t, v, "Linker", "note", "Links here.")
	if _, err := v.DB.Conn().Exec(
		`INSERT INTO links (source_id, target_id, target_raw, resolved) VALUES (?, ?, ?, 1)`,
		source.ID, target.ID, "Real Target",
	); err != nil {
		t.Fatalf("seed resolved link: %v", err)
	}
	if _, err := v.DB.Conn().Exec(
		`INSERT INTO links (source_id, target_id, target_raw, resolved) VALUES (?, NULL, ?, 0)`,
		source.ID, "Ghost Target",
	); err != nil {
		t.Fatalf("seed broken link: %v", err)
	}

	res, err := h.handleKBLinks(ctx, makeRequest(map[string]any{"path": source.Path}))
	if err != nil {
		t.Fatalf("handleKBLinks returned error: %v", err)
	}
	text := resultText(t, res)
	if res.IsError {
		t.Fatalf("result is an error: %s", text)
	}

	var refs []store.LinkRef
	if err := json.Unmarshal([]byte(text), &refs); err != nil {
		t.Fatalf("response is not a LinkRef array: %v\n%s", err, text)
	}
	if len(refs) != 2 {
		t.Fatalf("expected 2 outbound links, got %d:\n%s", len(refs), text)
	}

	var sawResolved, sawBroken bool
	for _, r := range refs {
		switch r.TargetRaw {
		case "Real Target":
			if !r.Resolved || r.Path != target.Path {
				t.Errorf("resolved link wrong: %+v", r)
			}
			sawResolved = true
		case "Ghost Target":
			if r.Resolved {
				t.Errorf("broken link should be unresolved: %+v", r)
			}
			sawBroken = true
		}
	}
	if !sawResolved || !sawBroken {
		t.Errorf("missing expected links: resolved=%v broken=%v", sawResolved, sawBroken)
	}
}

// TestHandleKBTags asserts kb_tags returns vault-wide tag counts.
func TestHandleKBTags(t *testing.T) {
	h, v := makeHandlers(t)
	ctx := context.Background()

	// Two docs share "shared"; one carries "solo".
	a := testutil.CreateAndIndex(t, v, "Doc A", "note", "body a")
	b := testutil.CreateAndIndex(t, v, "Doc B", "note", "body b")
	if err := v.DB.UpsertTags(a.ID, []string{"shared", "solo"}); err != nil {
		t.Fatalf("upsert tags a: %v", err)
	}
	if err := v.DB.UpsertTags(b.ID, []string{"shared"}); err != nil {
		t.Fatalf("upsert tags b: %v", err)
	}

	res, err := h.handleKBTags(ctx, makeRequest(nil))
	if err != nil {
		t.Fatalf("handleKBTags returned error: %v", err)
	}
	text := resultText(t, res)
	if res.IsError {
		t.Fatalf("result is an error: %s", text)
	}

	var counts []store.TagCount
	if err := json.Unmarshal([]byte(text), &counts); err != nil {
		t.Fatalf("response is not a TagCount array: %v\n%s", err, text)
	}
	got := map[string]int{}
	for _, c := range counts {
		got[c.Tag] = c.Count
	}
	if got["shared"] != 2 {
		t.Errorf("tag 'shared' count = %d, want 2", got["shared"])
	}
	if got["solo"] != 1 {
		t.Errorf("tag 'solo' count = %d, want 1", got["solo"])
	}
}

// TestHandleKBTasks seeds a doc with open and done checkboxes and asserts the
// filters behave (all, todo-only, done-only).
func TestHandleKBTasks(t *testing.T) {
	h, v := makeHandlers(t)
	ctx := context.Background()

	body := "# Todos\n\n- [ ] open one\n- [x] done one\n- [ ] open two\n"
	testutil.CreateAndIndex(t, v, "Task List", "note", body)

	// All tasks.
	all := decodeTaskRows(t, h, ctx, map[string]any{})
	if len(all) != 3 {
		t.Fatalf("expected 3 tasks, got %d: %+v", len(all), all)
	}

	// Only open.
	todo := decodeTaskRows(t, h, ctx, map[string]any{"todo": true})
	if len(todo) != 2 {
		t.Fatalf("expected 2 open tasks, got %d: %+v", len(todo), todo)
	}
	for _, r := range todo {
		if r.Done {
			t.Errorf("todo filter returned a done task: %+v", r)
		}
	}

	// Only done.
	done := decodeTaskRows(t, h, ctx, map[string]any{"done": true})
	if len(done) != 1 {
		t.Fatalf("expected 1 done task, got %d: %+v", len(done), done)
	}
	if !done[0].Done || done[0].Text != "done one" {
		t.Errorf("done task wrong: %+v", done[0])
	}
}

// TestHandleKBTasks_MutualExclusion verifies done+todo together errors.
func TestHandleKBTasks_MutualExclusion(t *testing.T) {
	h, _ := makeHandlers(t)
	res, err := h.handleKBTasks(context.Background(), makeRequest(map[string]any{"done": true, "todo": true}))
	if err != nil {
		t.Fatalf("unexpected hard error: %v", err)
	}
	if !res.IsError {
		t.Error("expected error result when both done and todo are set")
	}
}

func decodeTaskRows(t *testing.T, h *handlers, ctx context.Context, args map[string]any) []kbTaskRow {
	t.Helper()
	res, err := h.handleKBTasks(ctx, makeRequest(args))
	if err != nil {
		t.Fatalf("handleKBTasks returned error: %v", err)
	}
	text := resultText(t, res)
	if res.IsError {
		t.Fatalf("result is an error: %s", text)
	}
	var rows []kbTaskRow
	if err := json.Unmarshal([]byte(text), &rows); err != nil {
		t.Fatalf("response is not a task array: %v\n%s", err, text)
	}
	return rows
}

// TestHandleKBAppend asserts the body changed on disk, frontmatter is
// preserved, and the index sees the new content.
func TestHandleKBAppend(t *testing.T) {
	h, v := makeHandlers(t)
	ctx := context.Background()

	doc := testutil.CreateAndIndex(t, v, "Appendable", "note", "# Appendable\n\nOriginal body.\n")
	absPath := v.AbsPath(doc.Path)

	res, err := h.handleKBAppend(ctx, makeRequest(map[string]any{
		"path": doc.Path,
		"text": "Appended paragraph here.",
	}))
	if err != nil {
		t.Fatalf("handleKBAppend returned error: %v", err)
	}
	if res.IsError {
		t.Fatalf("result is an error: %s", resultText(t, res))
	}

	// Re-read the file from disk and confirm body + frontmatter.
	reparsed, err := document.ParseFile(absPath)
	if err != nil {
		t.Fatalf("re-parse after append: %v", err)
	}
	if !strings.Contains(reparsed.Body, "Original body.") {
		t.Error("original body was lost after append")
	}
	if !strings.Contains(reparsed.Body, "Appended paragraph here.") {
		t.Error("appended text not found on disk")
	}
	if reparsed.ID != doc.ID || reparsed.Title != "Appendable" || reparsed.Type != "note" {
		t.Errorf("frontmatter not preserved: id=%q title=%q type=%q", reparsed.ID, reparsed.Title, reparsed.Type)
	}
}

// TestHandleKBAppend_RequiresText verifies an empty text arg errors.
func TestHandleKBAppend_RequiresText(t *testing.T) {
	h, v := makeHandlers(t)
	doc := testutil.CreateAndIndex(t, v, "NoText", "note", "body")
	res, err := h.handleKBAppend(context.Background(), makeRequest(map[string]any{"path": doc.Path}))
	if err != nil {
		t.Fatalf("unexpected hard error: %v", err)
	}
	if !res.IsError {
		t.Error("expected error result when text is missing")
	}
}

// TestHandleKBAppend_RejectsCanvas verifies a read-only .canvas file is
// rejected and not overwritten.
func TestHandleKBAppend_RejectsCanvas(t *testing.T) {
	h, v := makeHandlers(t)
	ctx := context.Background()

	canvasJSON := `{"nodes":[{"id":"a","type":"text","text":"hi","x":0,"y":0,"width":100,"height":100}],"edges":[]}`
	canvasPath := v.AbsPath("board.canvas")
	if err := os.WriteFile(canvasPath, []byte(canvasJSON), 0o644); err != nil {
		t.Fatalf("write canvas: %v", err)
	}

	res, err := h.handleKBAppend(ctx, makeRequest(map[string]any{
		"path": "board.canvas",
		"text": "should be rejected",
	}))
	if err != nil {
		t.Fatalf("unexpected hard error: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected error result for read-only .canvas append")
	}
	if !strings.Contains(resultText(t, res), "read-only") {
		t.Errorf("expected read-only message, got: %s", resultText(t, res))
	}

	// The original JSON must be untouched.
	onDisk, err := os.ReadFile(canvasPath)
	if err != nil {
		t.Fatalf("re-read canvas: %v", err)
	}
	if string(onDisk) != canvasJSON {
		t.Errorf("canvas file was modified: %s", string(onDisk))
	}
}

// TestHandleKBReplaceSection asserts only the named section is replaced, the
// heading and siblings survive, and frontmatter is preserved.
func TestHandleKBReplaceSection(t *testing.T) {
	h, v := makeHandlers(t)
	ctx := context.Background()

	body := "# Doc\n\n## Context\n\nold context\n\n## Decision\n\nold decision\n"
	doc := testutil.CreateAndIndex(t, v, "Sectioned", "note", body)
	absPath := v.AbsPath(doc.Path)

	res, err := h.handleKBReplaceSection(ctx, makeRequest(map[string]any{
		"path":    doc.Path,
		"section": "Decision",
		"text":    "we chose plan B",
	}))
	if err != nil {
		t.Fatalf("handleKBReplaceSection returned error: %v", err)
	}
	if res.IsError {
		t.Fatalf("result is an error: %s", resultText(t, res))
	}

	reparsed, err := document.ParseFile(absPath)
	if err != nil {
		t.Fatalf("re-parse after replace: %v", err)
	}
	if !strings.Contains(reparsed.Body, "we chose plan B") {
		t.Error("replacement content not written")
	}
	if strings.Contains(reparsed.Body, "old decision") {
		t.Error("old decision content should be gone")
	}
	if !strings.Contains(reparsed.Body, "old context") {
		t.Error("sibling Context section should be untouched")
	}
	if !strings.Contains(reparsed.Body, "## Decision") {
		t.Error("Decision heading line should be preserved")
	}
	if reparsed.ID != doc.ID || reparsed.Title != "Sectioned" {
		t.Errorf("frontmatter not preserved: id=%q title=%q", reparsed.ID, reparsed.Title)
	}
}

// TestHandleKBReplaceSection_NotFound verifies a missing heading errors and the
// file is left unchanged.
func TestHandleKBReplaceSection_NotFound(t *testing.T) {
	h, v := makeHandlers(t)
	ctx := context.Background()

	body := "# Doc\n\n## Context\n\nstays\n"
	doc := testutil.CreateAndIndex(t, v, "NoSection", "note", body)
	absPath := v.AbsPath(doc.Path)
	before, _ := os.ReadFile(absPath)

	res, err := h.handleKBReplaceSection(ctx, makeRequest(map[string]any{
		"path":    doc.Path,
		"section": "Nonexistent",
		"text":    "x",
	}))
	if err != nil {
		t.Fatalf("unexpected hard error: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected error result for missing section")
	}

	after, _ := os.ReadFile(absPath)
	if string(before) != string(after) {
		t.Error("file should be unchanged when section not found")
	}
}

// TestHandleKBReplaceSection_RejectsBase verifies a read-only .base file is
// rejected by the body-write guard. The base body synthesizes one "# <key>"
// heading per flattened YAML key, so targeting an existing flattened key ("name")
// ensures ReplaceSection finds the section and we exercise the read-only guard
// inside the write path rather than a section-not-found short-circuit.
func TestHandleKBReplaceSection_RejectsBase(t *testing.T) {
	h, v := makeHandlers(t)
	ctx := context.Background()

	baseYAML := "name: data-board\n"
	basePath := v.AbsPath("data.base")
	if err := os.WriteFile(basePath, []byte(baseYAML), 0o644); err != nil {
		t.Fatalf("write base: %v", err)
	}

	// Sanity: the synthetic base body must contain a "# name" heading so the
	// replace targets a real section and reaches the read-only guard.
	bdoc, err := document.ParseFile(basePath)
	if err != nil {
		t.Fatalf("parse base: %v", err)
	}
	if !strings.Contains(bdoc.Body, "# name") {
		t.Fatalf("expected synthetic '# name' heading in base body, got:\n%s", bdoc.Body)
	}

	res, err := h.handleKBReplaceSection(ctx, makeRequest(map[string]any{
		"path":    "data.base",
		"section": "name",
		"text":    "x",
	}))
	if err != nil {
		t.Fatalf("unexpected hard error: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected error result for read-only .base replace-section")
	}
	if !strings.Contains(resultText(t, res), "read-only") {
		t.Errorf("expected read-only message, got: %s", resultText(t, res))
	}

	onDisk, _ := os.ReadFile(basePath)
	if string(onDisk) != baseYAML {
		t.Errorf("base file was modified: %s", string(onDisk))
	}
}
