package mcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/apresai/2ndbrain/internal/testutil"
	"github.com/apresai/2ndbrain/internal/vault"
	mcplib "github.com/mark3labs/mcp-go/mcp"
)

// makeHandlers creates a handlers instance backed by a fresh isolated vault.
func makeHandlers(t *testing.T) (*handlers, *vault.Vault) {
	t.Helper()
	v := testutil.NewTestVault(t)
	return &handlers{vault: v}, v
}

// resultText extracts the text from the first Content element of a CallToolResult.
func resultText(t *testing.T, res *mcplib.CallToolResult) string {
	t.Helper()
	if res == nil {
		t.Fatal("result is nil")
	}
	if len(res.Content) == 0 {
		t.Fatal("result has no content")
	}
	tc, ok := res.Content[0].(mcplib.TextContent)
	if !ok {
		t.Fatalf("first content is not TextContent, got %T", res.Content[0])
	}
	return tc.Text
}

// makeRequest builds a CallToolRequest with the given arguments map.
func makeRequest(args map[string]any) mcplib.CallToolRequest {
	return mcplib.CallToolRequest{
		Params: mcplib.CallToolParams{
			Arguments: args,
		},
	}
}

// TestHandleKBInfo verifies that kb_info returns a JSON object containing the
// correct total document count after seeding the vault with 3 documents.
func TestHandleKBInfo(t *testing.T) {
	h, v := makeHandlers(t)
	ctx := context.Background()

	testutil.CreateAndIndex(t, v, "Alpha Note", "note", "body of alpha")
	testutil.CreateAndIndex(t, v, "Beta Note", "note", "body of beta")
	testutil.CreateAndIndex(t, v, "Gamma Note", "note", "body of gamma")

	res, err := h.handleKBInfo(ctx, makeRequest(nil))
	if err != nil {
		t.Fatalf("handleKBInfo returned error: %v", err)
	}

	text := resultText(t, res)
	if res.IsError {
		t.Fatalf("result is an error: %s", text)
	}

	var info map[string]any
	if err := json.Unmarshal([]byte(text), &info); err != nil {
		t.Fatalf("response is not valid JSON: %v\n%s", err, text)
	}

	total, ok := info["total_documents"].(float64)
	if !ok {
		t.Fatalf("total_documents missing or wrong type in response: %s", text)
	}
	if int(total) != 3 {
		t.Errorf("expected total_documents=3, got %v", total)
	}
}

// TestHandleKBCreate verifies that kb_create writes a file to disk and returns
// valid JSON containing the expected id, path, and title fields.
func TestHandleKBCreate(t *testing.T) {
	h, _ := makeHandlers(t)
	ctx := context.Background()

	res, err := h.handleKBCreate(ctx, makeRequest(map[string]any{
		"title": "My New Note",
		"type":  "note",
	}))
	if err != nil {
		t.Fatalf("handleKBCreate returned error: %v", err)
	}

	text := resultText(t, res)
	if res.IsError {
		t.Fatalf("result is an error: %s", text)
	}

	var result map[string]any
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		t.Fatalf("response is not valid JSON: %v\n%s", err, text)
	}

	for _, field := range []string{"id", "path", "title"} {
		if result[field] == nil || result[field] == "" {
			t.Errorf("expected field %q to be present and non-empty in result", field)
		}
	}

	if result["title"] != "My New Note" {
		t.Errorf("expected title 'My New Note', got %v", result["title"])
	}

	// Confirm file exists on disk.
	relPath, _ := result["path"].(string)
	if relPath == "" {
		t.Fatal("path field is empty")
	}
	absPath := h.vault.AbsPath(relPath)
	if _, statErr := os.Stat(absPath); os.IsNotExist(statErr) {
		t.Errorf("created file does not exist on disk: %s", absPath)
	}
}

// TestHandleKBCreate_IndexesDocument asserts the handler invoked the real
// indexer (not just wrote a file). Before the IndexSingleFile delegation,
// kb_create did its own partial upsert and skipped link resolution; the
// symptom was that kb_related on a newly-created doc saw a zero graph.
// Verifying chunks land for the created doc confirms the indexing path
// ran end-to-end.
func TestHandleKBCreate_IndexesDocument(t *testing.T) {
	h, _ := makeHandlers(t)
	ctx := context.Background()

	res, err := h.handleKBCreate(ctx, makeRequest(map[string]any{
		"title": "Indexed Note",
		"type":  "note",
	}))
	if err != nil || res.IsError {
		t.Fatalf("handleKBCreate failed: err=%v text=%s", err, resultText(t, res))
	}
	var result map[string]any
	json.Unmarshal([]byte(resultText(t, res)), &result)
	docID, _ := result["id"].(string)
	if docID == "" {
		t.Fatal("result missing id")
	}

	var count int
	err = h.vault.DB.Conn().QueryRow(
		"SELECT COUNT(*) FROM chunks WHERE doc_id = ?", docID,
	).Scan(&count)
	if err != nil {
		t.Fatalf("query chunks: %v", err)
	}
	if count == 0 {
		t.Error("chunks table is empty for newly-created doc — indexing path didn't run")
	}
}

// TestHandleKBCreate_MissingArgs verifies that kb_create returns an error
// result when required arguments are absent.
func TestHandleKBCreate_MissingArgs(t *testing.T) {
	h, _ := makeHandlers(t)
	ctx := context.Background()

	res, err := h.handleKBCreate(ctx, makeRequest(map[string]any{"title": "Only Title"}))
	if err != nil {
		t.Fatalf("unexpected hard error: %v", err)
	}
	if !res.IsError {
		t.Error("expected an error result when type is missing")
	}
}

// TestHandleKBCreate_WithPath verifies that the optional "path" argument
// files the new document under a subdirectory (created on write) and that the
// returned path is vault-relative to that subdirectory.
func TestHandleKBCreate_WithPath(t *testing.T) {
	h, _ := makeHandlers(t)
	ctx := context.Background()

	res, err := h.handleKBCreate(ctx, makeRequest(map[string]any{
		"title": "Subdir Note",
		"type":  "note",
		"path":  "resources",
	}))
	if err != nil {
		t.Fatalf("handleKBCreate returned error: %v", err)
	}
	text := resultText(t, res)
	if res.IsError {
		t.Fatalf("result is an error: %s", text)
	}

	var result map[string]any
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		t.Fatalf("response is not valid JSON: %v\n%s", err, text)
	}
	relPath, _ := result["path"].(string)
	if relPath != filepath.Join("resources", "subdir-note.md") {
		t.Fatalf("expected path resources/subdir-note.md, got %q", relPath)
	}
	if _, statErr := os.Stat(h.vault.AbsPath(relPath)); statErr != nil {
		t.Errorf("file not written under resources/: %v", statErr)
	}
}

// TestHandleKBCreate_PathTraversal verifies that a "path" argument escaping
// the vault is rejected and writes nothing.
func TestHandleKBCreate_PathTraversal(t *testing.T) {
	h, _ := makeHandlers(t)
	ctx := context.Background()

	for _, bad := range []string{"../escape", "/tmp/abs"} {
		res, err := h.handleKBCreate(ctx, makeRequest(map[string]any{
			"title": "Escape Note",
			"type":  "note",
			"path":  bad,
		}))
		if err != nil {
			t.Fatalf("unexpected hard error for %q: %v", bad, err)
		}
		if !res.IsError {
			t.Errorf("expected an error result for path %q", bad)
		}
	}
}

// TestHandleKBList verifies that all 3 seeded documents appear in an
// unfiltered list response.
func TestHandleKBList(t *testing.T) {
	h, v := makeHandlers(t)
	ctx := context.Background()

	testutil.CreateAndIndex(t, v, "Doc One", "note", "")
	testutil.CreateAndIndex(t, v, "Doc Two", "note", "")
	testutil.CreateAndIndex(t, v, "Doc Three", "note", "")

	res, err := h.handleKBList(ctx, makeRequest(nil))
	if err != nil {
		t.Fatalf("handleKBList returned error: %v", err)
	}

	text := resultText(t, res)
	if res.IsError {
		t.Fatalf("result is an error: %s", text)
	}

	for _, title := range []string{"Doc One", "Doc Two", "Doc Three"} {
		if !strings.Contains(text, title) {
			t.Errorf("expected response to contain %q", title)
		}
	}
}

// TestHandleKBList_TypeFilter verifies that filtering by type returns only
// documents of that type and excludes others.
func TestHandleKBList_TypeFilter(t *testing.T) {
	h, v := makeHandlers(t)
	ctx := context.Background()

	testutil.CreateAndIndex(t, v, "My ADR", "adr", "")
	testutil.CreateAndIndex(t, v, "My Note", "note", "")

	res, err := h.handleKBList(ctx, makeRequest(map[string]any{"type": "adr"}))
	if err != nil {
		t.Fatalf("handleKBList returned error: %v", err)
	}

	text := resultText(t, res)
	if res.IsError {
		t.Fatalf("result is an error: %s", text)
	}

	if !strings.Contains(text, "My ADR") {
		t.Error("expected ADR document in filtered results")
	}
	if strings.Contains(text, "My Note") {
		t.Error("note document should not appear in adr-filtered results")
	}
}

// TestHandleKBSearch verifies that a BM25 keyword search finds a document
// whose body contains the query term.
func TestHandleKBSearch(t *testing.T) {
	h, v := makeHandlers(t)
	ctx := context.Background()

	testutil.CreateAndIndex(t, v, "Auth Design", "note",
		"# Auth Design\n\nThis document covers authentication JWT token approach.")

	res, err := h.handleKBSearch(ctx, makeRequest(map[string]any{
		"query": "authentication",
	}))
	if err != nil {
		t.Fatalf("handleKBSearch returned error: %v", err)
	}

	text := resultText(t, res)
	if res.IsError {
		t.Fatalf("result is an error: %s", text)
	}

	if !strings.Contains(text, "Auth Design") {
		t.Errorf("expected search results to contain 'Auth Design', got: %s", text)
	}
}

// TestHandleKBSearch_NoResults verifies that searching for a term that matches
// nothing returns an empty JSON array rather than an error.
func TestHandleKBSearch_NoResults(t *testing.T) {
	h, _ := makeHandlers(t)
	ctx := context.Background()

	res, err := h.handleKBSearch(ctx, makeRequest(map[string]any{
		"query": "xyzzy_nonexistent_term_12345",
	}))
	if err != nil {
		t.Fatalf("handleKBSearch returned error: %v", err)
	}

	text := resultText(t, res)
	if res.IsError {
		t.Fatalf("result is an error: %s", text)
	}

	// Should be a valid JSON array (empty or not).
	var results []any
	if err := json.Unmarshal([]byte(text), &results); err != nil {
		t.Fatalf("expected a JSON array, got: %v\n%s", err, text)
	}
}

// TestHandleKBRead verifies that reading a document by path returns its title
// and body content in the JSON response.
func TestHandleKBRead(t *testing.T) {
	h, v := makeHandlers(t)
	ctx := context.Background()

	doc := testutil.CreateAndIndex(t, v, "Readable Doc", "note",
		"# Readable Doc\n\nSome body content here.")

	res, err := h.handleKBRead(ctx, makeRequest(map[string]any{
		"path": doc.Path,
	}))
	if err != nil {
		t.Fatalf("handleKBRead returned error: %v", err)
	}

	text := resultText(t, res)
	if res.IsError {
		t.Fatalf("result is an error: %s", text)
	}

	if !strings.Contains(text, "Readable Doc") {
		t.Errorf("expected title 'Readable Doc' in response")
	}
	if !strings.Contains(text, "Some body content here") {
		t.Errorf("expected body text in response")
	}
}

// TestHandleKBRead_PathTraversal verifies that path traversal attempts are
// rejected with an error result.
func TestHandleKBRead_PathTraversal(t *testing.T) {
	h, _ := makeHandlers(t)
	ctx := context.Background()

	res, err := h.handleKBRead(ctx, makeRequest(map[string]any{
		"path": "../../etc/passwd",
	}))
	if err != nil {
		t.Fatalf("unexpected hard error: %v", err)
	}
	if !res.IsError {
		t.Error("expected an error result for path traversal attempt")
	}
}

// TestHandleKBDelete verifies that deleting a document removes the file from
// disk and returns a success result with the deleted flag.
func TestHandleKBDelete(t *testing.T) {
	h, v := makeHandlers(t)
	ctx := context.Background()

	doc := testutil.CreateAndIndex(t, v, "Doomed Doc", "note", "")
	absPath := h.vault.AbsPath(doc.Path)

	// Confirm the file exists before deletion.
	if _, err := os.Stat(absPath); os.IsNotExist(err) {
		t.Fatalf("file should exist before delete: %s", absPath)
	}

	res, err := h.handleKBDelete(ctx, makeRequest(map[string]any{
		"path": doc.Path,
	}))
	if err != nil {
		t.Fatalf("handleKBDelete returned error: %v", err)
	}

	text := resultText(t, res)
	if res.IsError {
		t.Fatalf("result is an error: %s", text)
	}

	var result map[string]any
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
	if result["deleted"] != true {
		t.Errorf("expected deleted=true in result, got %v", result["deleted"])
	}

	// Confirm file is gone from disk.
	if _, err := os.Stat(absPath); !os.IsNotExist(err) {
		t.Errorf("file should have been removed from disk: %s", absPath)
	}
}

// TestHandleKBDelete_NotFound verifies that attempting to delete a path that
// is not in the index returns an error result.
func TestHandleKBDelete_NotFound(t *testing.T) {
	h, _ := makeHandlers(t)
	ctx := context.Background()

	res, err := h.handleKBDelete(ctx, makeRequest(map[string]any{
		"path": "nonexistent.md",
	}))
	if err != nil {
		t.Fatalf("unexpected hard error: %v", err)
	}
	if !res.IsError {
		t.Error("expected an error result when document not found")
	}
}

// TestHandleKBUpdateMeta verifies that updating the status of an ADR from
// "proposed" to "accepted" succeeds and persists the change.
func TestHandleKBUpdateMeta(t *testing.T) {
	h, v := makeHandlers(t)
	ctx := context.Background()

	// ADR initial status is "proposed".
	doc := testutil.CreateAndIndex(t, v, "Switch to Postgres", "adr", "")

	res, err := h.handleKBUpdateMeta(ctx, makeRequest(map[string]any{
		"path": doc.Path,
		"fields": map[string]any{
			"status": "accepted",
		},
	}))
	if err != nil {
		t.Fatalf("handleKBUpdateMeta returned error: %v", err)
	}

	text := resultText(t, res)
	if res.IsError {
		t.Fatalf("result is an error: %s", text)
	}

	if !strings.Contains(text, "accepted") {
		t.Errorf("expected 'accepted' in returned frontmatter JSON, got: %s", text)
	}
}

// TestHandleKBUpdateMeta_InvalidTransition verifies that an invalid status
// transition is rejected with an error result.
func TestHandleKBUpdateMeta_InvalidTransition(t *testing.T) {
	h, v := makeHandlers(t)
	ctx := context.Background()

	// ADR starts at "proposed"; jumping straight to "superseded" is invalid.
	doc := testutil.CreateAndIndex(t, v, "Bad Transition ADR", "adr", "")

	res, err := h.handleKBUpdateMeta(ctx, makeRequest(map[string]any{
		"path": doc.Path,
		"fields": map[string]any{
			"status": "superseded",
		},
	}))
	if err != nil {
		t.Fatalf("unexpected hard error: %v", err)
	}
	if !res.IsError {
		t.Error("expected an error result for invalid status transition")
	}
}

// TestHandleKBStructure verifies that kb_structure returns the document's
// heading sections in the response.
func TestHandleKBStructure(t *testing.T) {
	h, v := makeHandlers(t)
	ctx := context.Background()

	body := "# My Doc\n\n## Context\n\nSome context.\n\n## Decision\n\nThe decision.\n"
	doc := testutil.CreateAndIndex(t, v, "My Doc", "note", body)

	res, err := h.handleKBStructure(ctx, makeRequest(map[string]any{
		"path": doc.Path,
	}))
	if err != nil {
		t.Fatalf("handleKBStructure returned error: %v", err)
	}

	text := resultText(t, res)
	if res.IsError {
		t.Fatalf("result is an error: %s", text)
	}

	var result map[string]any
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		t.Fatalf("response is not valid JSON: %v\n%s", err, text)
	}

	sections, ok := result["sections"].([]any)
	if !ok {
		t.Fatalf("expected 'sections' array in response, got: %s", text)
	}
	if len(sections) == 0 {
		t.Error("expected at least one section in structure output")
	}

	// Both H2 headings should appear somewhere in the serialized output.
	if !strings.Contains(text, "Context") {
		t.Error("expected 'Context' heading in structure")
	}
	if !strings.Contains(text, "Decision") {
		t.Error("expected 'Decision' heading in structure")
	}
}

// TestHandleKBIndex verifies that running kb_index on a vault that already has
// 3 written documents reports a positive indexed count.
func TestHandleKBIndex(t *testing.T) {
	h, v := makeHandlers(t)
	ctx := context.Background()

	// Write documents to disk without indexing them so the indexer has work.
	for _, title := range []string{"Index Me One", "Index Me Two", "Index Me Three"} {
		testutil.CreateAndIndex(t, v, title, "note", "")
	}

	res, err := h.handleKBIndex(ctx, makeRequest(nil))
	if err != nil {
		t.Fatalf("handleKBIndex returned error: %v", err)
	}

	text := resultText(t, res)
	if res.IsError {
		t.Fatalf("result is an error: %s", text)
	}

	var result map[string]any
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		t.Fatalf("response is not valid JSON: %v\n%s", err, text)
	}

	docsIndexed, ok := result["documents_indexed"].(float64)
	if !ok {
		t.Fatalf("expected 'documents_indexed' numeric field in response: %s", text)
	}
	if int(docsIndexed) < 3 {
		t.Errorf("expected at least 3 documents indexed, got %v", docsIndexed)
	}
}

func TestMCPToolRegistrationsIncludesAllTools(t *testing.T) {
	h, _ := makeHandlers(t)
	regs := mcpToolRegistrations(h)
	if len(regs) != 16 {
		t.Fatalf("registered tools = %d, want 16", len(regs))
	}
	names := make(map[string]bool, len(regs))
	for _, reg := range regs {
		names[reg.tool.Name] = true
		if reg.timeout <= 0 {
			t.Fatalf("tool %s has non-positive timeout %s", reg.tool.Name, reg.timeout)
		}
	}
	for _, name := range []string{
		"kb_info", "kb_search", "kb_ask", "kb_read", "kb_list", "kb_create",
		"kb_update_meta", "kb_related", "kb_structure", "kb_delete", "kb_index",
		"kb_suggest_links", "kb_polish", "kb_git_activity", "kb_git_diff", "kb_git_status",
	} {
		if !names[name] {
			t.Fatalf("missing MCP tool registration %q", name)
		}
	}
}

func TestHandleKBRelated(t *testing.T) {
	h, v := makeHandlers(t)
	ctx := context.Background()
	target := testutil.CreateAndIndex(t, v, "Target Doc", "note", "target body")
	source := testutil.CreateAndIndex(t, v, "Source Doc", "note", "See [[Target Doc]] for more.")
	if _, err := v.DB.Conn().Exec(
		`INSERT INTO links (source_id, target_id, target_raw, resolved) VALUES (?, ?, ?, 1)`,
		source.ID, target.ID, "Target Doc",
	); err != nil {
		t.Fatalf("seed resolved link: %v", err)
	}

	res, err := h.handleKBRelated(ctx, makeRequest(map[string]any{
		"path":  source.Path,
		"depth": float64(1),
	}))
	if err != nil {
		t.Fatalf("handleKBRelated returned error: %v", err)
	}
	text := resultText(t, res)
	if res.IsError {
		t.Fatalf("result is an error: %s", text)
	}
	if !strings.Contains(text, "Target Doc") {
		t.Fatalf("related result missing target doc:\n%s", text)
	}
}

func TestHandleKBAskRequiresQuestion(t *testing.T) {
	h, _ := makeHandlers(t)
	res, err := h.handleKBAsk(context.Background(), makeRequest(nil))
	if err != nil {
		t.Fatalf("handleKBAsk returned error: %v", err)
	}
	if !res.IsError || !strings.Contains(resultText(t, res), "question is required") {
		t.Fatalf("expected question-required error, got %+v", res)
	}
}

func TestHandleKBPolishRequiresPath(t *testing.T) {
	h, _ := makeHandlers(t)
	res, err := h.handleKBPolish(context.Background(), makeRequest(nil))
	if err != nil {
		t.Fatalf("handleKBPolish returned error: %v", err)
	}
	if !res.IsError || !strings.Contains(resultText(t, res), "path is required") {
		t.Fatalf("expected path-required error, got %+v", res)
	}
}

func TestHandleKBSuggestLinksRequiresPath(t *testing.T) {
	h, _ := makeHandlers(t)
	res, err := h.handleKBSuggestLinks(context.Background(), makeRequest(nil))
	if err != nil {
		t.Fatalf("handleKBSuggestLinks returned error: %v", err)
	}
	if !res.IsError || !strings.Contains(resultText(t, res), "path is required") {
		t.Fatalf("expected path-required error, got %+v", res)
	}
}

func TestHandleKBGitHandlersNonRepo(t *testing.T) {
	h, _ := makeHandlers(t)
	ctx := context.Background()
	for name, call := range map[string]func() (*mcplib.CallToolResult, error){
		"activity": func() (*mcplib.CallToolResult, error) {
			return h.handleKBGitActivity(ctx, makeRequest(nil))
		},
		"diff": func() (*mcplib.CallToolResult, error) {
			return h.handleKBGitDiff(ctx, makeRequest(map[string]any{"path": "note.md"}))
		},
		"status": func() (*mcplib.CallToolResult, error) {
			return h.handleKBGitStatus(ctx, makeRequest(nil))
		},
	} {
		t.Run(name, func(t *testing.T) {
			res, err := call()
			if err != nil {
				t.Fatalf("git handler returned error: %v", err)
			}
			text := resultText(t, res)
			if res.IsError || !strings.Contains(text, `"git_repo": false`) {
				t.Fatalf("expected non-repo JSON response, got error=%v text=%s", res.IsError, text)
			}
		})
	}
}

func TestHandlers_ThresholdCachesOnce(t *testing.T) {
	// handlers.threshold() uses sync.Once to memoize the resolved
	// AIConfig.SimilarityThreshold for the MCP session. Subsequent vault
	// config mutations should NOT change the returned value — that's the
	// whole point of the cache.
	h, v := makeHandlers(t)
	v.Config.AI.SimilarityThreshold = 0.42
	first := h.threshold()
	if first != 0.42 {
		t.Fatalf("first call = %v, want 0.42", first)
	}

	v.Config.AI.SimilarityThreshold = 0.99
	second := h.threshold()
	if second != first {
		t.Errorf("cache bypassed: first=%v second=%v (sync.Once should keep them equal)", first, second)
	}
}

func TestHandlers_EmbeddingCacheRoundtrip(t *testing.T) {
	h, v := makeHandlers(t)

	// Fresh cache: loads from DB. No embeddings yet, should return empty slices.
	ids, vecs, err := h.getCachedEmbeddings()
	if err != nil {
		t.Fatalf("getCachedEmbeddings empty: %v", err)
	}
	if len(ids) != 0 || len(vecs) != 0 {
		t.Errorf("empty vault: got %d ids, %d vecs", len(ids), len(vecs))
	}

	// Add a doc with an embedding directly at the DB layer.
	doc := testutil.CreateAndIndex(t, v, "Cached Doc", "note", "body")
	if err := v.DB.SetEmbedding(doc.ID, []float32{1, 0, 0}, "test-model", "hash-1"); err != nil {
		t.Fatalf("SetEmbedding: %v", err)
	}

	// Without invalidation, the cache still returns the old (empty) result.
	ids, _, _ = h.getCachedEmbeddings()
	if len(ids) != 0 {
		t.Errorf("before invalidate: got %d ids, want 0 (cache should be stale)", len(ids))
	}

	// After invalidate, next call reloads from DB.
	h.invalidateEmbeddings()
	ids, vecs, err = h.getCachedEmbeddings()
	if err != nil {
		t.Fatalf("getCachedEmbeddings post-invalidate: %v", err)
	}
	if len(ids) != 1 || len(vecs) != 1 {
		t.Fatalf("post-invalidate: got %d ids %d vecs, want 1 each", len(ids), len(vecs))
	}
	if ids[0] != doc.ID {
		t.Errorf("id = %q, want %q", ids[0], doc.ID)
	}
}
