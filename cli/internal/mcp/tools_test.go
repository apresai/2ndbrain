package mcp

import (
	"context"
	"encoding/json"
	"os"
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
