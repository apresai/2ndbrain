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

func (h *handlers) handleKBInfo(ctx context.Context, request mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	v := h.vault
	cfg := v.Config

	// Count documents by type
	typeCounts := make(map[string]int)
	rows, err := v.DB.Conn().Query("SELECT doc_type, COUNT(*) FROM documents GROUP BY doc_type")
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var t string
			var c int
			if rows.Scan(&t, &c) == nil {
				typeCounts[t] = c
			}
		}
	}

	var totalDocs int
	v.DB.Conn().QueryRow("SELECT COUNT(*) FROM documents").Scan(&totalDocs)
	embCount, _ := v.DB.EmbeddingCount()

	// Build schema info
	type schemaInfo struct {
		Type          string   `json:"type"`
		StatusValues  []string `json:"status_values,omitempty"`
		InitialStatus string   `json:"initial_status,omitempty"`
		Count         int      `json:"document_count"`
	}
	var schemas []schemaInfo
	for typeName, schema := range v.Schemas.Types {
		si := schemaInfo{Type: typeName, Count: typeCounts[typeName]}
		if schema.Status != nil {
			si.InitialStatus = schema.Status.Initial
			for state := range schema.Status.Transitions {
				si.StatusValues = append(si.StatusValues, state)
			}
		}
		schemas = append(schemas, si)
	}

	// AI status
	aiStatus := map[string]any{
		"provider":         cfg.AI.Provider,
		"embedding_model":  cfg.AI.EmbeddingModel,
		"generation_model": cfg.AI.GenerationModel,
		"embeddings":       embCount,
	}

	info := map[string]any{
		"vault_name":     cfg.Name,
		"total_documents": totalDocs,
		"document_types": schemas,
		"ai":             aiStatus,
		"usage_tips": []string{
			"Use kb_search to find documents by topic",
			"Use kb_ask to get AI-synthesized answers with source citations",
			"Use kb_list to browse documents by type or status",
			"Use kb_create to add new ADRs, runbooks, postmortems, or notes",
			"Use kb_read with the chunk parameter to read specific sections",
		},
	}

	data, _ := json.MarshalIndent(info, "", "  ")
	return mcplib.NewToolResultText(string(data)), nil
}

func (h *handlers) handleKBIndex(ctx context.Context, request mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	stats, err := vault.IndexVault(h.vault, func(path string) {})
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("index failed: %v", err)), nil
	}

	// Generate embeddings if a provider is available
	embedded := 0
	cfg := h.vault.Config.AI
	embedder, embErr := ai.DefaultRegistry.Embedder(cfg.Provider)
	if embErr == nil && embedder.Available(ctx) {
		model := cfg.EmbeddingModel
		docs, _ := h.vault.DB.DocumentsNeedingEmbedding(model)
		for _, doc := range docs {
			absPath := h.vault.AbsPath(doc.Path)
			content, err := os.ReadFile(absPath)
			if err != nil {
				continue
			}
			parsed, err := document.Parse(doc.Path, content)
			if err != nil {
				continue
			}
			vecs, err := embedder.Embed(ctx, []string{parsed.Body})
			if err != nil {
				continue
			}
			parsed.ComputeContentHash()
			if h.vault.DB.SetEmbedding(doc.ID, vecs[0], model, parsed.ContentHash) == nil {
				embedded++
			}
		}
	}

	result := map[string]any{
		"documents_indexed": stats.DocsIndexed,
		"chunks_created":    stats.ChunksCreated,
		"links_found":       stats.LinksFound,
		"embeddings_updated": embedded,
	}

	data, _ := json.MarshalIndent(result, "", "  ")
	return mcplib.NewToolResultText(string(data)), nil
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

	// Return array format (original MCP contract was an object wrapper; reverted to array per C2 fix)
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
	doc.ComputeContentHash()

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

	// Embed inline if provider available
	cfg := h.vault.Config.AI
	embedder, embErr := ai.DefaultRegistry.Embedder(cfg.Provider)
	if embErr == nil && embedder.Available(ctx) {
		if vecs, err := embedder.Embed(ctx, []string{doc.Body}); err == nil {
			h.vault.DB.SetEmbedding(doc.ID, vecs[0], cfg.EmbeddingModel, doc.ContentHash)
		}
	}

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

const defaultPolishSystemPrompt = `You are a copy editor. Fix spelling, grammar, and punctuation errors in the markdown below. Improve clarity where wording is awkward, but preserve the author's voice, all wikilinks like [[foo]], all code blocks (fenced and inline), and the heading structure exactly. Do not add or remove sections. Do not reformat lists. Return ONLY the corrected markdown body with no explanation, no commentary, and no surrounding code fences.`

func (h *handlers) handleKBPolish(ctx context.Context, request mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	path, _ := request.GetArguments()["path"].(string)
	if path == "" {
		return mcplib.NewToolResultError("path is required"), nil
	}
	if strings.Contains(path, "..") {
		return mcplib.NewToolResultError("path traversal not allowed"), nil
	}
	systemPrompt, _ := request.GetArguments()["system"].(string)
	if systemPrompt == "" {
		systemPrompt = defaultPolishSystemPrompt
	}

	absPath := h.vault.AbsPath(path)
	if !strings.HasPrefix(absPath, h.vault.Root) {
		return mcplib.NewToolResultError("path outside vault"), nil
	}

	parsed, err := document.ParseFile(absPath)
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("parse source: %v", err)), nil
	}

	cfg := h.vault.Config.AI
	generator, err := ai.DefaultRegistry.Generator(cfg.Provider)
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("no generation provider: %v", err)), nil
	}
	if !generator.Available(ctx) {
		return mcplib.NewToolResultError("generation provider not available"), nil
	}

	opts := ai.GenOpts{
		Temperature:  0.2,
		MaxTokens:    4096,
		SystemPrompt: systemPrompt,
	}
	polished, err := generator.Generate(ctx, parsed.Body, opts)
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("polish generation failed: %v", err)), nil
	}
	polished = strings.TrimSpace(polished)

	result := map[string]any{
		"path":     path,
		"original": parsed.Body,
		"polished": polished,
		"provider": cfg.Provider,
		"model":    cfg.GenerationModel,
	}
	data, _ := json.MarshalIndent(result, "", "  ")
	return mcplib.NewToolResultText(string(data)), nil
}

func (h *handlers) handleKBSuggestLinks(ctx context.Context, request mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	path, _ := request.GetArguments()["path"].(string)
	if path == "" {
		return mcplib.NewToolResultError("path is required"), nil
	}
	if strings.Contains(path, "..") {
		return mcplib.NewToolResultError("path traversal not allowed"), nil
	}
	limit := 10
	if v, ok := request.GetArguments()["limit"].(float64); ok && v > 0 {
		limit = int(v)
	}

	absPath := h.vault.AbsPath(path)
	if !strings.HasPrefix(absPath, h.vault.Root) {
		return mcplib.NewToolResultError("path outside vault"), nil
	}

	parsed, err := document.ParseFile(absPath)
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("parse source: %v", err)), nil
	}
	parsed.Path = path

	cfg := h.vault.Config.AI
	embedder, err := ai.DefaultRegistry.Embedder(cfg.Provider)
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("no embedding provider: %v", err)), nil
	}
	if !embedder.Available(ctx) {
		return mcplib.NewToolResultError("embedding provider not available"), nil
	}

	runes := []rune(parsed.Body)
	if len(runes) > 2000 {
		runes = runes[:2000]
	}
	queryVecs, err := embedder.Embed(ctx, []string{string(runes)})
	if err != nil || len(queryVecs) == 0 {
		return mcplib.NewToolResultError(fmt.Sprintf("embed source: %v", err)), nil
	}

	docIDs, embeddings, err := h.vault.DB.AllEmbeddings()
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("load embeddings: %v", err)), nil
	}

	scored := search.VectorSearch(queryVecs[0], docIDs, embeddings, limit*3)

	var sourceID string
	if dbDoc, err := h.vault.DB.GetDocumentByPath(path); err == nil && dbDoc != nil {
		sourceID = dbDoc.ID
	}
	linked := make(map[string]bool)
	if sourceID != "" {
		rows, err := h.vault.DB.Conn().Query(
			`SELECT target_id FROM links WHERE source_id = ? AND target_id IS NOT NULL AND target_id != ''`,
			sourceID,
		)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var targetID string
				if err := rows.Scan(&targetID); err == nil {
					linked[targetID] = true
				}
			}
		}
	}

	type suggestItem struct {
		Path    string  `json:"path"`
		Title   string  `json:"title"`
		Score   float64 `json:"score"`
		Snippet string  `json:"snippet"`
	}

	engine := search.NewEngine(h.vault.DB.Conn())
	items := make([]suggestItem, 0, limit)
	for _, s := range scored {
		if s.DocID == sourceID || linked[s.DocID] {
			continue
		}
		lookup, ok := engine.GetDocumentByID(s.DocID)
		if !ok {
			continue
		}
		content, err := os.ReadFile(filepath.Join(h.vault.Root, lookup.Path))
		snippet := ""
		if err == nil {
			if parsed, err := document.Parse(lookup.Path, content); err == nil {
				r := []rune(parsed.Body)
				if len(r) > 200 {
					r = r[:200]
				}
				snippet = string(r)
			}
		}
		items = append(items, suggestItem{
			Path:    lookup.Path,
			Title:   lookup.Title,
			Score:   s.Score,
			Snippet: snippet,
		})
		if len(items) >= limit {
			break
		}
	}

	data, _ := json.MarshalIndent(items, "", "  ")
	return mcplib.NewToolResultText(string(data)), nil
}
