package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/apresai/2ndbrain/internal/vault"
	mcplib "github.com/mark3labs/mcp-go/mcp"
)

// MCP write->query index round-trip tests ("validate our use").
//
// These lock the invariant that was missing when kb_update_meta shipped without
// a reindex: after ANY MCP write tool, the change must be reflected in the index
// that the query tools read (tags table, chunks/FTS, documents row). They use the
// shared makeHandlers/makeRequest/resultText harness (tools_test.go) and need no
// AI provider (tag/list/BM25 assertions run on the index alone).

// callOK asserts a handler returned a non-error result and returns it.
func callOK(t *testing.T, res *mcplib.CallToolResult, err error) *mcplib.CallToolResult {
	t.Helper()
	if err != nil {
		t.Fatalf("handler hard error: %v", err)
	}
	if res == nil || res.IsError {
		t.Fatalf("handler returned error result: %s", resultText(t, res))
	}
	return res
}

// createNoteViaMCP creates a note through kb_create and returns its
// vault-relative path and document id.
func createNoteViaMCP(t *testing.T, h *handlers, ctx context.Context, title string) (path, id string) {
	t.Helper()
	res, err := h.handleKBCreate(ctx, makeRequest(map[string]any{"title": title, "type": "note"}))
	callOK(t, res, err)
	var r map[string]any
	if err := json.Unmarshal([]byte(resultText(t, res)), &r); err != nil {
		t.Fatalf("kb_create result not JSON: %v\n%s", err, resultText(t, res))
	}
	path, _ = r["path"].(string)
	id, _ = r["id"].(string)
	if path == "" || id == "" {
		t.Fatalf("kb_create result missing path/id: %s", resultText(t, res))
	}
	return path, id
}

// tagRowCount returns how many rows the tags table holds for (docID, tag).
func tagRowCount(t *testing.T, v *vault.Vault, docID, tag string) int {
	t.Helper()
	var n int
	if err := v.DB.Conn().QueryRow(
		"SELECT COUNT(*) FROM tags WHERE doc_id = ? AND tag = ?", docID, tag,
	).Scan(&n); err != nil {
		t.Fatalf("query tags: %v", err)
	}
	return n
}

// contentHash returns the documents.content_hash for a doc id.
func contentHash(t *testing.T, v *vault.Vault, docID string) string {
	t.Helper()
	var h string
	if err := v.DB.Conn().QueryRow(
		"SELECT content_hash FROM documents WHERE id = ?", docID,
	).Scan(&h); err != nil {
		t.Fatalf("query content_hash: %v", err)
	}
	return h
}

func mcpListText(t *testing.T, h *handlers, ctx context.Context, args map[string]any) string {
	t.Helper()
	res, err := h.handleKBList(ctx, makeRequest(args))
	callOK(t, res, err)
	return resultText(t, res)
}

func mcpSearchText(t *testing.T, h *handlers, ctx context.Context, query string) string {
	t.Helper()
	res, err := h.handleKBSearch(ctx, makeRequest(map[string]any{"query": query}))
	callOK(t, res, err)
	return resultText(t, res)
}

// TestUsageMCP_UpdateMetaTagsIndexed is the regression for the kb_update_meta
// reindex fix: a tag set via kb_update_meta must land in the tags table and be
// findable via kb_list{tag}. Before the fix, kb_update_meta wrote only the
// documents row, so this failed.
func TestUsageMCP_UpdateMetaTagsIndexed(t *testing.T) {
	h, v := makeHandlers(t)
	ctx := context.Background()

	path, id := createNoteViaMCP(t, h, ctx, "Tagged Note")

	res, err := h.handleKBUpdateMeta(ctx, makeRequest(map[string]any{
		"path":   path,
		"fields": map[string]any{"tags": []any{"release", "obsidian-cli"}},
	}))
	callOK(t, res, err)

	// The tags table must reflect the update (the bug: it didn't).
	for _, tag := range []string{"release", "obsidian-cli"} {
		if n := tagRowCount(t, v, id, tag); n != 1 {
			t.Errorf("tags table has %d rows for %q (want 1): kb_update_meta did not reindex tags", n, tag)
		}
	}

	// And the doc must be findable via kb_list{tag}.
	if got := mcpListText(t, h, ctx, map[string]any{"tag": "release"}); !strings.Contains(got, "Tagged Note") {
		t.Errorf("kb_list{tag:release} did not return the tagged note:\n%s", got)
	}
}

// TestUsageMCP_UpdateMetaStatusIndexed verifies a status change is reflected in
// kb_list{status} filtering.
func TestUsageMCP_UpdateMetaStatusIndexed(t *testing.T) {
	h, _ := makeHandlers(t)
	ctx := context.Background()

	// ADR initial status is "proposed".
	res, err := h.handleKBCreate(ctx, makeRequest(map[string]any{"title": "Adopt Postgres", "type": "adr"}))
	callOK(t, res, err)
	var r map[string]any
	_ = json.Unmarshal([]byte(resultText(t, res)), &r)
	path, _ := r["path"].(string)

	ures, uerr := h.handleKBUpdateMeta(ctx, makeRequest(map[string]any{
		"path":   path,
		"fields": map[string]any{"status": "accepted"},
	}))
	callOK(t, ures, uerr)

	if got := mcpListText(t, h, ctx, map[string]any{"status": "accepted"}); !strings.Contains(got, "Adopt Postgres") {
		t.Errorf("kb_list{status:accepted} missing the doc after status change:\n%s", got)
	}
	if got := mcpListText(t, h, ctx, map[string]any{"status": "proposed"}); strings.Contains(got, "Adopt Postgres") {
		t.Errorf("doc still appears under the old status:\n%s", got)
	}
}

// TestUsageMCP_WriteToolsIndexRoundTrip drives every MCP write tool and asserts
// the index agrees after each write: create appears in list, appended/replaced
// body text is BM25-findable, a tag is in the tags table, and a deleted doc is
// gone from both list and search.
func TestUsageMCP_WriteToolsIndexRoundTrip(t *testing.T) {
	h, v := makeHandlers(t)
	ctx := context.Background()

	path, id := createNoteViaMCP(t, h, ctx, "RoundTrip Doc")

	// create -> in kb_list
	if got := mcpListText(t, h, ctx, nil); !strings.Contains(got, "RoundTrip Doc") {
		t.Errorf("created doc not in kb_list:\n%s", got)
	}

	// append -> appended text is BM25-findable (chunks reindexed)
	ares, aerr := h.handleKBAppend(ctx, makeRequest(map[string]any{"path": path, "text": "zqxwvu unique marker"}))
	callOK(t, ares, aerr)
	if got := mcpSearchText(t, h, ctx, "zqxwvu"); !strings.Contains(got, "RoundTrip Doc") {
		t.Errorf("appended text not searchable (append did not reindex chunks):\n%s", got)
	}

	// replace_section -> add a heading, replace it, new text is findable
	r2, e2 := h.handleKBAppend(ctx, makeRequest(map[string]any{"path": path, "text": "## Marker\n\nold content here"}))
	callOK(t, r2, e2)
	rs, rserr := h.handleKBReplaceSection(ctx, makeRequest(map[string]any{"path": path, "section": "Marker", "text": "replacedtoken qwerlytics"}))
	callOK(t, rs, rserr)
	if got := mcpSearchText(t, h, ctx, "qwerlytics"); !strings.Contains(got, "RoundTrip Doc") {
		t.Errorf("replaced-section text not searchable:\n%s", got)
	}

	// update_meta tags -> tag in the tags table
	ms, mserr := h.handleKBUpdateMeta(ctx, makeRequest(map[string]any{"path": path, "fields": map[string]any{"tags": []any{"rtt"}}}))
	callOK(t, ms, mserr)
	if n := tagRowCount(t, v, id, "rtt"); n != 1 {
		t.Errorf("update_meta tag not in tags table (got %d rows)", n)
	}

	// delete -> gone from list and search
	ds, dserr := h.handleKBDelete(ctx, makeRequest(map[string]any{"path": path}))
	callOK(t, ds, dserr)
	if got := mcpListText(t, h, ctx, nil); strings.Contains(got, "RoundTrip Doc") {
		t.Errorf("deleted doc still in kb_list:\n%s", got)
	}
	if got := mcpSearchText(t, h, ctx, "zqxwvu"); strings.Contains(got, "RoundTrip Doc") {
		t.Errorf("deleted doc still in kb_search:\n%s", got)
	}
}

// TestUsageMCP_UpdateMetaPreservesBody confirms a metadata-only update keeps the
// body intact and does not change the content hash (so it can't trigger a
// spurious re-embed, since re-embedding is content-hash gated).
func TestUsageMCP_UpdateMetaPreservesBody(t *testing.T) {
	h, v := makeHandlers(t)
	ctx := context.Background()

	path, id := createNoteViaMCP(t, h, ctx, "Preserve Doc")

	ares, aerr := h.handleKBAppend(ctx, makeRequest(map[string]any{"path": path, "text": "BODY-MARKER-xyz"}))
	callOK(t, ares, aerr)
	hashBefore := contentHash(t, v, id)

	ures, uerr := h.handleKBUpdateMeta(ctx, makeRequest(map[string]any{"path": path, "fields": map[string]any{"tags": []any{"keep"}}}))
	callOK(t, ures, uerr)

	rres, rerr := h.handleKBRead(ctx, makeRequest(map[string]any{"path": path}))
	callOK(t, rres, rerr)
	if !strings.Contains(resultText(t, rres), "BODY-MARKER-xyz") {
		t.Errorf("body lost after metadata-only update_meta:\n%s", resultText(t, rres))
	}
	if got := contentHash(t, v, id); got != hashBefore {
		t.Errorf("content hash changed on metadata-only update_meta (%q -> %q): would trigger a spurious re-embed", hashBefore, got)
	}
}
