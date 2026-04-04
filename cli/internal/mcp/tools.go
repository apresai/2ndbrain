package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/apresai/2ndbrain/internal/ai"
	"github.com/apresai/2ndbrain/internal/document"
	graphpkg "github.com/apresai/2ndbrain/internal/graph"
	"github.com/apresai/2ndbrain/internal/search"
	"github.com/apresai/2ndbrain/internal/vault"
	mcplib "github.com/mark3labs/mcp-go/mcp"
)

type handlers struct {
	vault *vault.Vault
}

func (h *handlers) handleKBSearch(ctx context.Context, request mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	query, _ := request.GetArguments()["query"].(string)
	docType, _ := request.GetArguments()["type"].(string)
	status, _ := request.GetArguments()["status"].(string)
	tag, _ := request.GetArguments()["tag"].(string)
	limit := 10
	if l, ok := request.GetArguments()["limit"].(float64); ok {
		limit = int(l)
	}

	engine := search.NewEngine(h.vault.DB.Conn())
	opts := search.Options{
		Query:  query,
		Type:   docType,
		Status: status,
		Tag:    tag,
		Limit:  limit,
	}

	// Try hybrid search if provider is available
	cfg := h.vault.Config.AI
	var results []search.Result
	var mode search.SearchMode

	embedder, embErr := ai.DefaultRegistry.Embedder(cfg.Provider)
	embCount, _ := h.vault.DB.EmbeddingCount()

	if embErr == nil && embedder.Available(ctx) && embCount > 0 && query != "" {
		queryVecs, err := embedder.Embed(ctx, []string{query})
		if err == nil && len(queryVecs) > 0 {
			docIDs, embeddings, err := h.vault.DB.AllEmbeddings()
			if err == nil {
				results, mode, err = engine.HybridSearch(opts, queryVecs[0], docIDs, embeddings)
				if err != nil {
					return mcplib.NewToolResultError(fmt.Sprintf("hybrid search failed: %v", err)), nil
				}
			}
		}
	}

	// Fall back to BM25
	if results == nil {
		var err error
		results, err = engine.Search(opts)
		if err != nil {
			return mcplib.NewToolResultError(fmt.Sprintf("search failed: %v", err)), nil
		}
		mode = search.ModeKeyword
	}

	// Return plain array for backward compatibility (C2 fix)
	// Add search_mode to each result as metadata
	type resultWithMode struct {
		search.Result
		SearchMode string `json:"search_mode"`
	}
	enriched := make([]resultWithMode, len(results))
	for i, r := range results {
		enriched[i] = resultWithMode{Result: r, SearchMode: string(mode)}
	}
	data, _ := json.MarshalIndent(enriched, "", "  ")
	return mcplib.NewToolResultText(string(data)), nil
}

func (h *handlers) handleKBAsk(ctx context.Context, request mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	question, _ := request.GetArguments()["question"].(string)
	if question == "" {
		return mcplib.NewToolResultError("question is required"), nil
	}

	cfg := h.vault.Config.AI

	// Get generator
	generator, err := ai.DefaultRegistry.Generator(cfg.Provider)
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("no generation provider: %v", err)), nil
	}
	if !generator.Available(ctx) {
		return mcplib.NewToolResultError("generation provider not available"), nil
	}

	// Search for relevant context
	engine := search.NewEngine(h.vault.DB.Conn())
	opts := search.Options{Query: question, Limit: 5}

	var results []search.Result
	embedder, embErr := ai.DefaultRegistry.Embedder(cfg.Provider)
	embCount, _ := h.vault.DB.EmbeddingCount()

	if embErr == nil && embedder.Available(ctx) && embCount > 0 {
		queryVecs, err := embedder.Embed(ctx, []string{question})
		if err == nil && len(queryVecs) > 0 {
			docIDs, embeddings, _ := h.vault.DB.AllEmbeddings()
			results, _, _ = engine.HybridSearch(opts, queryVecs[0], docIDs, embeddings)
		}
	}
	if results == nil {
		results, _ = engine.Search(opts)
	}

	// Build RAG context from search results
	var chunks []ai.RAGChunk
	for _, r := range results {
		if r.Content != "" {
			chunks = append(chunks, ai.RAGChunk{Title: r.Title, Path: r.Path, Content: r.Content})
		} else if r.Path != "" {
			// Read from disk for vector-only results
			content, err := os.ReadFile(filepath.Join(h.vault.Root, r.Path))
			if err == nil {
				runes := []rune(string(content))
				if len(runes) > 2000 {
					runes = runes[:2000]
				}
				chunks = append(chunks, ai.RAGChunk{Title: r.Title, Path: r.Path, Content: string(runes)})
			}
		}
	}

	result, err := ai.RAG(ctx, generator, question, chunks)
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("RAG failed: %v", err)), nil
	}

	data, _ := json.MarshalIndent(result, "", "  ")
	return mcplib.NewToolResultText(string(data)), nil
}

func (h *handlers) handleKBRead(ctx context.Context, request mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	path, _ := request.GetArguments()["path"].(string)
	chunk, _ := request.GetArguments()["chunk"].(string)

	if path == "" {
		return mcplib.NewToolResultError("path is required"), nil
	}

	// Security: prevent path traversal
	if strings.Contains(path, "..") {
		return mcplib.NewToolResultError("path traversal not allowed"), nil
	}

	absPath := h.vault.AbsPath(path)
	if !strings.HasPrefix(absPath, h.vault.Root) {
		return mcplib.NewToolResultError("path outside vault"), nil
	}

	doc, err := document.ParseFile(absPath)
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("read failed: %v", err)), nil
	}
	doc.Path = path

	if chunk != "" {
		chunks := document.ChunkDocument(doc)
		target := strings.ToLower(chunk)
		for _, c := range chunks {
			heading := strings.ToLower(c.HeadingPath)
			cleaned := strings.TrimLeft(heading, "# ")
			if strings.Contains(cleaned, target) || strings.HasSuffix(heading, target) {
				data, _ := json.MarshalIndent(c, "", "  ")
				return mcplib.NewToolResultText(string(data)), nil
			}
		}
		return mcplib.NewToolResultError(fmt.Sprintf("chunk '%s' not found in document", chunk)), nil
	}

	data, _ := json.MarshalIndent(doc, "", "  ")
	return mcplib.NewToolResultText(string(data)), nil
}

func (h *handlers) handleKBRelated(ctx context.Context, request mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	path, _ := request.GetArguments()["path"].(string)
	depth := 2
	if d, ok := request.GetArguments()["depth"].(float64); ok {
		depth = int(d)
	}

	if path == "" {
		return mcplib.NewToolResultError("path is required"), nil
	}

	doc, err := h.vault.DB.GetDocumentByPath(path)
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("document not found: %s", path)), nil
	}

	g, err := graphpkg.Traverse(h.vault.DB.Conn(), doc.ID, depth)
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("traverse failed: %v", err)), nil
	}

	data, _ := json.MarshalIndent(g, "", "  ")
	return mcplib.NewToolResultText(string(data)), nil
}

func (h *handlers) handleKBCreate(ctx context.Context, request mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	title, _ := request.GetArguments()["title"].(string)
	docType, _ := request.GetArguments()["type"].(string)

	if title == "" || docType == "" {
		return mcplib.NewToolResultError("title and type are required"), nil
	}

	// Determine initial status from schema
	initialStatus := "draft"
	if schema, ok := h.vault.Schemas.Types[docType]; ok && schema.Status != nil {
		initialStatus = schema.Status.Initial
	}

	tmplBody := vault.GetTemplate(docType)
	tmplBody = strings.ReplaceAll(tmplBody, "{{.Title}}", title)
	tmplBody = strings.ReplaceAll(tmplBody, "{{.Status}}", initialStatus)

	doc := document.NewDocument(title, docType, tmplBody)
	doc.SetMeta("status", initialStatus)

	writePath, err := doc.WriteFile(h.vault.Root)
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("create failed: %v", err)), nil
	}

	doc.Path = h.vault.RelPath(writePath)

	// Index
	h.vault.DB.UpsertDocument(doc)
	chunks := document.ChunkDocument(doc)
	h.vault.DB.UpsertChunks(chunks)
	h.vault.DB.UpsertTags(doc.ID, doc.Tags)

	result := map[string]any{
		"id":    doc.ID,
		"path":  doc.Path,
		"title": doc.Title,
		"type":  doc.Type,
	}
	data, _ := json.MarshalIndent(result, "", "  ")
	return mcplib.NewToolResultText(string(data)), nil
}

func (h *handlers) handleKBUpdateMeta(ctx context.Context, request mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	path, _ := request.GetArguments()["path"].(string)
	fieldsRaw, _ := request.GetArguments()["fields"].(map[string]any)

	if path == "" {
		return mcplib.NewToolResultError("path is required"), nil
	}

	if strings.Contains(path, "..") {
		return mcplib.NewToolResultError("path traversal not allowed"), nil
	}

	absPath := h.vault.AbsPath(path)
	doc, err := document.ParseFile(absPath)
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("read failed: %v", err)), nil
	}
	doc.Path = path

	for k, v := range fieldsRaw {
		if err := h.vault.Schemas.ValidateField(doc.Type, k, v); err != nil {
			return mcplib.NewToolResultError(fmt.Sprintf("validation error: %v", err)), nil
		}
		if k == "status" {
			if strVal, ok := v.(string); ok && doc.Status != "" {
				if err := h.vault.Schemas.ValidateStatusTransition(doc.Type, doc.Status, strVal); err != nil {
					return mcplib.NewToolResultError(fmt.Sprintf("validation error: %v", err)), nil
				}
			}
		}
		doc.SetMeta(k, v)
	}

	content, err := doc.Serialize()
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("serialize failed: %v", err)), nil
	}

	tmp := absPath + ".tmp"
	if err := os.WriteFile(tmp, content, 0o644); err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("write failed: %v", err)), nil
	}
	if err := os.Rename(tmp, absPath); err != nil {
		os.Remove(tmp)
		return mcplib.NewToolResultError(fmt.Sprintf("rename failed: %v", err)), nil
	}

	h.vault.DB.UpsertDocument(doc)

	data, _ := json.MarshalIndent(doc.Frontmatter, "", "  ")
	return mcplib.NewToolResultText(string(data)), nil
}

func (h *handlers) handleKBStructure(ctx context.Context, request mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	path, _ := request.GetArguments()["path"].(string)

	if path == "" {
		return mcplib.NewToolResultError("path is required"), nil
	}

	if strings.Contains(path, "..") {
		return mcplib.NewToolResultError("path traversal not allowed"), nil
	}

	absPath := filepath.Join(h.vault.Root, path)
	doc, err := document.ParseFile(absPath)
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("read failed: %v", err)), nil
	}
	doc.Path = path

	chunks := document.ChunkDocument(doc)

	type headingNode struct {
		ID          string `json:"id"`
		HeadingPath string `json:"heading_path"`
		Level       int    `json:"level"`
		StartLine   int    `json:"start_line"`
		EndLine     int    `json:"end_line"`
	}

	nodes := make([]headingNode, len(chunks))
	for i, c := range chunks {
		nodes[i] = headingNode{
			ID:          c.ID,
			HeadingPath: c.HeadingPath,
			Level:       c.Level,
			StartLine:   c.StartLine,
			EndLine:     c.EndLine,
		}
	}

	result := map[string]any{
		"path":     path,
		"title":    doc.Title,
		"sections": nodes,
	}

	data, _ := json.MarshalIndent(result, "", "  ")
	return mcplib.NewToolResultText(string(data)), nil
}

func (h *handlers) handleKBDelete(ctx context.Context, request mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	path, _ := request.GetArguments()["path"].(string)

	if path == "" {
		return mcplib.NewToolResultError("path is required"), nil
	}
	if strings.Contains(path, "..") {
		return mcplib.NewToolResultError("path traversal not allowed"), nil
	}

	doc, err := h.vault.DB.GetDocumentByPath(path)
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("document not found: %s", path)), nil
	}

	absPath := h.vault.AbsPath(path)
	if err := os.Remove(absPath); err != nil && !os.IsNotExist(err) {
		return mcplib.NewToolResultError(fmt.Sprintf("delete file failed: %v", err)), nil
	}

	if err := h.vault.DB.DeleteDocument(doc.ID); err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("delete from index failed: %v", err)), nil
	}

	deleteResult := map[string]any{"deleted": true, "id": doc.ID, "path": path, "title": doc.Title}
	deleteData, _ := json.MarshalIndent(deleteResult, "", "  ")
	return mcplib.NewToolResultText(string(deleteData)), nil
}

func (h *handlers) handleKBList(ctx context.Context, request mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	docType, _ := request.GetArguments()["type"].(string)
	status, _ := request.GetArguments()["status"].(string)
	tag, _ := request.GetArguments()["tag"].(string)
	limit := 100
	if l, ok := request.GetArguments()["limit"].(float64); ok {
		limit = int(l)
	}

	query := "SELECT id, path, title, doc_type, status, modified_at FROM documents"
	var conditions []string
	var args []any

	if docType != "" {
		conditions = append(conditions, "doc_type = ?")
		args = append(args, docType)
	}
	if status != "" {
		conditions = append(conditions, "status = ?")
		args = append(args, status)
	}
	if tag != "" {
		conditions = append(conditions, "id IN (SELECT doc_id FROM tags WHERE tag = ?)")
		args = append(args, tag)
	}

	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	query += " ORDER BY modified_at DESC LIMIT ?"
	args = append(args, limit)

	rows, err := h.vault.DB.Conn().Query(query, args...)
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("query failed: %v", err)), nil
	}
	defer rows.Close()

	type listItem struct {
		ID         string `json:"id"`
		Path       string `json:"path"`
		Title      string `json:"title"`
		Type       string `json:"type"`
		Status     string `json:"status"`
		ModifiedAt string `json:"modified_at"`
	}

	var items []listItem
	for rows.Next() {
		var i listItem
		if err := rows.Scan(&i.ID, &i.Path, &i.Title, &i.Type, &i.Status, &i.ModifiedAt); err != nil {
			continue
		}
		items = append(items, i)
	}

	listData, _ := json.MarshalIndent(items, "", "  ")
	return mcplib.NewToolResultText(string(listData)), nil
}
