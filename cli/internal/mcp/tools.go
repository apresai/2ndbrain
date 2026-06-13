package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/apresai/2ndbrain/internal/ai"
	"github.com/apresai/2ndbrain/internal/document"
	gitpkg "github.com/apresai/2ndbrain/internal/git"
	graphpkg "github.com/apresai/2ndbrain/internal/graph"
	"github.com/apresai/2ndbrain/internal/search"
	"github.com/apresai/2ndbrain/internal/store"
	"github.com/apresai/2ndbrain/internal/vault"
	mcplib "github.com/mark3labs/mcp-go/mcp"
)

type handlers struct {
	vault *vault.Vault

	// threshOnce caches the resolved similarity threshold for this MCP
	// session. ResolveSimilarityThresholdFull re-reads two YAML files from
	// disk (global + per-vault user catalogs), which would otherwise happen
	// on every kb_search / kb_ask / kb_suggest_links invocation. MCP
	// sessions are long-lived, so cache once at first use.
	threshOnce sync.Once
	thresh     float64

	// Embedding cache: avoids re-reading ALL embeddings from SQLite on
	// every search/ask call. Invalidated when kb_index runs.
	embedMu     sync.RWMutex
	cachedIDs   []string
	cachedVecs  [][]float32
	embedLoaded bool
}

func (h *handlers) threshold() float64 {
	h.threshOnce.Do(func() {
		h.thresh, _ = h.vault.Config.AI.ResolveSimilarityThresholdFull(h.vault.Root)
	})
	return h.thresh
}

// getCachedEmbeddings returns the cached embedding vectors, loading them from
// the database on first call. Call invalidateEmbeddings() after index updates.
func (h *handlers) getCachedEmbeddings() ([]string, [][]float32, error) {
	h.embedMu.RLock()
	if h.embedLoaded {
		ids, vecs := h.cachedIDs, h.cachedVecs
		h.embedMu.RUnlock()
		return ids, vecs, nil
	}
	h.embedMu.RUnlock()

	h.embedMu.Lock()
	defer h.embedMu.Unlock()
	// Double-check after acquiring write lock
	if h.embedLoaded {
		return h.cachedIDs, h.cachedVecs, nil
	}
	ids, vecs, err := h.vault.DB.AllEmbeddings()
	if err != nil {
		return nil, nil, err
	}
	h.cachedIDs = ids
	h.cachedVecs = vecs
	h.embedLoaded = true
	return ids, vecs, nil
}

func (h *handlers) invalidateEmbeddings() {
	h.embedMu.Lock()
	defer h.embedMu.Unlock()
	h.cachedIDs = nil
	h.cachedVecs = nil
	h.embedLoaded = false
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
		"vault_name":      cfg.Name,
		"total_documents": totalDocs,
		"document_types":  schemas,
		"ai":              aiStatus,
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

	// Invalidate embedding cache so the next search picks up new vectors.
	h.invalidateEmbeddings()

	// Generate embeddings if a provider is available
	embedded := 0
	embeddingCancelled := false
	cfg := h.vault.Config.AI
	embedder, embErr := ai.DefaultRegistry.Embedder(cfg.Provider)
	if embErr == nil && embedder.Available(ctx) {
		model := cfg.EmbeddingModel
		docs, _ := h.vault.DB.DocumentsNeedingEmbedding(model)
		throttle := ai.ThrottleDelay(ai.ProviderRPSDefault(cfg.Provider))
	embedLoop:
		for i, doc := range docs {
			if i > 0 && throttle > 0 {
				select {
				case <-ctx.Done():
					// Caller (MCP client) timed out — stop embedding and
					// return what we got so far rather than block further.
					// Reassigning `i` doesn't exit a range loop; use a labeled
					// break so the doc list stops being processed immediately.
					embeddingCancelled = true
					slog.Warn("kb_index embedding aborted by ctx",
						"embedded", embedded,
						"remaining", len(docs)-i,
						"err", ctx.Err())
					break embedLoop
				case <-time.After(throttle):
				}
			}
			absPath := h.vault.AbsPath(doc.Path)
			content, err := os.ReadFile(absPath)
			if err != nil {
				continue
			}
			parsed, err := document.Parse(doc.Path, content)
			if err != nil {
				continue
			}
			vecs, err := embedder.Embed(ctx, []string{parsed.IndexableBody()})
			if err != nil {
				continue
			}
			parsed.ComputeContentHash()
			if h.vault.DB.SetEmbedding(doc.ID, vecs[0], model, parsed.ContentHash) == nil {
				embedded++
			}
		}
		// Invalidate again after embedding to include newly generated vectors.
		h.invalidateEmbeddings()
	}

	result := map[string]any{
		"documents_indexed":   stats.DocsIndexed,
		"chunks_created":      stats.ChunksCreated,
		"links_found":         stats.LinksFound,
		"embeddings_updated":  embedded,
		"embedding_cancelled": embeddingCancelled,
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
	cfg := h.vault.Config.AI
	opts := search.Options{
		Query:          query,
		Type:           docType,
		Status:         status,
		Tag:            tag,
		Limit:          limit,
		MinVectorScore: h.threshold(),
	}

	// Try hybrid search if provider is available
	var results []search.Result
	var mode search.SearchMode

	embedder, embErr := ai.DefaultRegistry.Embedder(cfg.Provider)
	embCount, _ := h.vault.DB.EmbeddingCount()

	if embErr == nil && embedder.Available(ctx) && embCount > 0 && query != "" {
		// Apply timeout to embedding call to prevent indefinite hangs
		embedCtx, embedCancel := context.WithTimeout(ctx, 60*time.Second)
		defer embedCancel()
		queryVecs, err := embedder.Embed(embedCtx, []string{query})
		if err == nil && len(queryVecs) > 0 {
			docIDs, embeddings, err := h.getCachedEmbeddings()
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
	opts := search.Options{
		Query:          question,
		Limit:          5,
		MinVectorScore: h.threshold(),
	}

	var results []search.Result
	var warnings []string
	embedder, embErr := ai.DefaultRegistry.Embedder(cfg.Provider)
	embCount, countErr := h.vault.DB.EmbeddingCount()
	if countErr != nil {
		slog.Warn("mcp kb_ask embedding count failed", "err", countErr)
		warnings = append(warnings, fmt.Sprintf("embedding count failed: %v", countErr))
	}

	if embErr == nil && countErr == nil && embedder.Available(ctx) && embCount > 0 {
		// Apply timeout to embedding call
		embedCtx, embedCancel := context.WithTimeout(ctx, 60*time.Second)
		defer embedCancel()
		queryVecs, err := embedder.Embed(embedCtx, []string{question})
		if err == nil && len(queryVecs) > 0 {
			docIDs, embeddings, err := h.getCachedEmbeddings()
			if err != nil {
				slog.Warn("mcp kb_ask embedding load failed", "err", err)
				warnings = append(warnings, fmt.Sprintf("semantic retrieval disabled: failed to load embeddings (%v)", err))
			} else if results, _, err = engine.HybridSearch(opts, queryVecs[0], docIDs, embeddings); err != nil {
				slog.Warn("mcp kb_ask hybrid search failed", "err", err)
				warnings = append(warnings, fmt.Sprintf("semantic retrieval disabled: hybrid search failed (%v)", err))
				results = nil
			}
		} else if err != nil {
			slog.Warn("mcp kb_ask query embedding failed", "err", err)
			warnings = append(warnings, fmt.Sprintf("semantic retrieval disabled: embedder returned error (%v)", err))
		}
	}
	if results == nil {
		results, err = engine.Search(opts)
		if err != nil {
			return mcplib.NewToolResultError(fmt.Sprintf("search failed: %v", err)), nil
		}
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
			} else {
				slog.Warn("mcp kb_ask context read failed", "path", r.Path, "err", err)
				warnings = append(warnings, fmt.Sprintf("failed to read context source %s: %v", r.Path, err))
			}
		}
	}
	if len(chunks) == 0 {
		return mcplib.NewToolResultError("failed to build RAG context from search results"), nil
	}

	result, err := ai.RAG(ctx, generator, question, chunks)
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("RAG failed: %v", err)), nil
	}

	type askResponse struct {
		Warnings []string `json:"warnings,omitempty"`
		Answer   string   `json:"answer"`
		Sources  []string `json:"sources"`
	}
	data, _ := json.MarshalIndent(askResponse{
		Warnings: warnings,
		Answer:   result.Answer,
		Sources:  result.Sources,
	}, "", "  ")
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
	if !h.vault.ContainsPath(absPath) {
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
	subdir, _ := request.GetArguments()["path"].(string)

	if title == "" || docType == "" {
		return mcplib.NewToolResultError("title and type are required"), nil
	}

	// Optional subdirectory placement. This is untrusted agent input, so guard
	// it unconditionally: reject ".." and absolute paths, then confirm the
	// resolved dir stays inside the vault.
	writeDir := h.vault.Root
	if subdir != "" {
		if strings.Contains(subdir, "..") || filepath.IsAbs(subdir) {
			return mcplib.NewToolResultError("path traversal not allowed"), nil
		}
		writeDir = h.vault.AbsPath(subdir)
		if !h.vault.ContainsPath(writeDir) {
			return mcplib.NewToolResultError("path outside vault"), nil
		}
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

	writePath, err := doc.WriteFile(writeDir)
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("create failed: %v", err)), nil
	}

	// Delegate to the shared indexer — keeps chunks/tags/links + link
	// resolution + transactional semantics identical to `2nb index` and
	// editor-triggered saves, so kb_create doesn't drift from the canonical
	// indexing path.
	if err := vault.IndexSingleFile(h.vault, writePath); err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("index new doc: %v", err)), nil
	}
	doc.Path = h.vault.RelPath(writePath)

	// Embed inline if provider available
	cfg := h.vault.Config.AI
	embedder, embErr := ai.DefaultRegistry.Embedder(cfg.Provider)
	if embErr == nil && embedder.Available(ctx) {
		if vecs, err := embedder.Embed(ctx, []string{doc.IndexableBody()}); err == nil {
			h.vault.DB.SetEmbedding(doc.ID, vecs[0], cfg.EmbeddingModel, doc.ContentHash)
		}
		h.invalidateEmbeddings()
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
	if !h.vault.ContainsPath(absPath) {
		return mcplib.NewToolResultError("path outside vault"), nil
	}
	doc, err := document.ParseFile(absPath)
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("read failed: %v", err)), nil
	}
	doc.Path = path

	// .canvas/.base files are parsed into a read-only synthetic view; writing
	// one back would overwrite the original JSON/YAML with markdown.
	if document.IsReadOnlyType(doc.Type) {
		return mcplib.NewToolResultError(fmt.Sprintf("cannot edit metadata of a read-only %s file (%s); .canvas/.base files are indexed read-only", doc.Type, path)), nil
	}

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

	// Serialize reads the on-disk file (by doc.Path) to preserve YAML comments
	// and key order; point it at the absolute path so it doesn't depend on the
	// server's cwd, then restore the vault-relative path for indexing.
	doc.Path = absPath
	content, err := doc.Serialize()
	doc.Path = path
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

	if err := h.vault.DB.UpsertDocument(doc); err != nil {
		slog.Warn("kb_update_meta: failed to update index", "path", path, "err", err)
	}

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

	absPath := h.vault.AbsPath(path)
	if !h.vault.ContainsPath(absPath) {
		return mcplib.NewToolResultError("path outside vault"), nil
	}
	doc, err := document.ParseFile(absPath)
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("read failed: %v", err)), nil
	}
	doc.Path = path

	// Shared with the CLI `outline` command so the two never drift.
	nodes := document.BuildOutline(doc)

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
	if !h.vault.ContainsPath(absPath) {
		return mcplib.NewToolResultError("path outside vault"), nil
	}
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

func (h *handlers) handleKBGitActivity(ctx context.Context, request mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	days := 7
	if v, ok := request.GetArguments()["since_days"].(float64); ok && v > 0 {
		days = int(v)
	}
	if !gitpkg.IsRepo(h.vault.Root) {
		return mcplib.NewToolResultText(`{"git_repo": false, "message": "vault is not a git repository"}`), nil
	}
	changes, err := gitpkg.Activity(h.vault.Root, time.Duration(days)*24*time.Hour)
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("git log: %v", err)), nil
	}
	data, _ := json.MarshalIndent(changes, "", "  ")
	return mcplib.NewToolResultText(string(data)), nil
}

func (h *handlers) handleKBGitDiff(ctx context.Context, request mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	path, _ := request.GetArguments()["path"].(string)
	if path == "" {
		return mcplib.NewToolResultError("path is required"), nil
	}
	if strings.Contains(path, "..") {
		return mcplib.NewToolResultError("path traversal not allowed"), nil
	}
	if !gitpkg.IsRepo(h.vault.Root) {
		return mcplib.NewToolResultText(`{"git_repo": false, "message": "vault is not a git repository"}`), nil
	}
	diff, err := gitpkg.DiffFile(h.vault.Root, path)
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("git diff: %v", err)), nil
	}
	result := map[string]any{"path": path, "diff": diff}
	data, _ := json.MarshalIndent(result, "", "  ")
	return mcplib.NewToolResultText(string(data)), nil
}

func (h *handlers) handleKBGitStatus(ctx context.Context, request mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	if !gitpkg.IsRepo(h.vault.Root) {
		return mcplib.NewToolResultText(`{"git_repo": false, "message": "vault is not a git repository"}`), nil
	}
	statuses, err := gitpkg.StatusFiles(h.vault.Root)
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("git status: %v", err)), nil
	}
	data, _ := json.MarshalIndent(statuses, "", "  ")
	return mcplib.NewToolResultText(string(data)), nil
}

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
	if !h.vault.ContainsPath(absPath) {
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
		Temperature:  ai.Ptr(0.2),
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

// resolvePathArg validates an untrusted vault-relative path argument and
// returns its absolute on-disk form. It mirrors the guard used by handleKBRead:
// reject ".." traversal, resolve against the vault root, and confirm the
// resolved path stays inside the vault. The second return value is an error
// CallToolResult ready to return; it is nil when the path is acceptable.
func (h *handlers) resolvePathArg(path string) (string, *mcplib.CallToolResult) {
	if path == "" {
		return "", mcplib.NewToolResultError("path is required")
	}
	if strings.Contains(path, "..") {
		return "", mcplib.NewToolResultError("path traversal not allowed")
	}
	absPath := h.vault.AbsPath(path)
	if !h.vault.ContainsPath(absPath) {
		return "", mcplib.NewToolResultError("path outside vault")
	}
	return absPath, nil
}

// writeBodyAndReindex persists an edited document body to disk and refreshes the
// index, mirroring the CLI's writeBody (internal/cli/bodywrite.go) step for
// step. That helper lives in package cli and can't be imported here without an
// import cycle, so the steps are replicated:
//
//  1. Refuse read-only synthetic types (.canvas/.base) before touching disk
//     (Serialize would emit synthesized markdown over the original JSON/YAML).
//  2. Serialize the in-memory doc (Serialize persists doc.Body) with doc.Path
//     pointed at the absolute path so it works from any cwd, then restore the
//     vault-relative path for indexing.
//  3. Atomic temp+rename write.
//  4. Reindex the single file (chunks/tags/links + ResolveLinks) so newly
//     appended #tags / [[links]] are picked up.
//  5. Recompute the content hash and re-embed inline if a provider is
//     available, then invalidate the embedding cache so the next search sees
//     the new vector. Embedding is best-effort and silently skipped otherwise.
//
// absPath must be the absolute on-disk path; doc.Path is left vault-relative on
// return. The returned CallToolResult is non-nil only on a hard failure (it is
// ready to return as the tool result); it is nil on success.
func (h *handlers) writeBodyAndReindex(ctx context.Context, doc *document.Document, absPath string) *mcplib.CallToolResult {
	if document.IsReadOnlyType(doc.Type) {
		return mcplib.NewToolResultError(fmt.Sprintf("cannot edit the body of a read-only %s file (%s); .canvas/.base files are indexed read-only", doc.Type, doc.Path))
	}

	rel := doc.Path
	doc.Path = absPath
	content, err := doc.Serialize()
	doc.Path = rel
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("serialize document: %v", err))
	}

	tmp := absPath + ".tmp"
	if err := os.WriteFile(tmp, content, 0o644); err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("write failed: %v", err))
	}
	if err := os.Rename(tmp, absPath); err != nil {
		os.Remove(tmp)
		return mcplib.NewToolResultError(fmt.Sprintf("rename failed: %v", err))
	}

	if err := vault.IndexSingleFile(h.vault, absPath); err != nil {
		slog.Warn("mcp body write: failed to index document", "path", rel, "err", err)
	}

	// The body changed, so recompute the content hash before re-embedding
	// (SetEmbedding stores the hash alongside the vector). Embed inline if a
	// provider is available, then invalidate the cache.
	doc.ComputeContentHash()
	cfg := h.vault.Config.AI
	if embedder, embErr := ai.DefaultRegistry.Embedder(cfg.Provider); embErr == nil && embedder.Available(ctx) {
		if vecs, err := embedder.Embed(ctx, []string{doc.IndexableBody()}); err == nil && len(vecs) > 0 {
			h.vault.DB.SetEmbedding(doc.ID, vecs[0], cfg.EmbeddingModel, doc.ContentHash)
		}
		h.invalidateEmbeddings()
	}

	return nil
}

func (h *handlers) handleKBBacklinks(ctx context.Context, request mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	path, _ := request.GetArguments()["path"].(string)
	if _, errRes := h.resolvePathArg(path); errRes != nil {
		return errRes, nil
	}

	doc, err := h.vault.DB.GetDocumentByPath(path)
	if err != nil || doc == nil {
		return mcplib.NewToolResultError(fmt.Sprintf("document not found: %s", path)), nil
	}

	refs, err := h.vault.DB.Backlinks(doc.ID)
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("backlinks failed: %v", err)), nil
	}
	if refs == nil {
		refs = []store.LinkRef{}
	}

	data, _ := json.MarshalIndent(refs, "", "  ")
	return mcplib.NewToolResultText(string(data)), nil
}

func (h *handlers) handleKBLinks(ctx context.Context, request mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	path, _ := request.GetArguments()["path"].(string)
	if _, errRes := h.resolvePathArg(path); errRes != nil {
		return errRes, nil
	}

	doc, err := h.vault.DB.GetDocumentByPath(path)
	if err != nil || doc == nil {
		return mcplib.NewToolResultError(fmt.Sprintf("document not found: %s", path)), nil
	}

	refs, err := h.vault.DB.OutboundLinks(doc.ID)
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("links failed: %v", err)), nil
	}
	if refs == nil {
		refs = []store.LinkRef{}
	}

	data, _ := json.MarshalIndent(refs, "", "  ")
	return mcplib.NewToolResultText(string(data)), nil
}

func (h *handlers) handleKBTags(ctx context.Context, request mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	counts, err := h.vault.DB.TagCounts()
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("tag counts failed: %v", err)), nil
	}
	if counts == nil {
		counts = []store.TagCount{}
	}
	data, _ := json.MarshalIndent(counts, "", "  ")
	return mcplib.NewToolResultText(string(data)), nil
}

// kbTaskRow pairs a document.Task with the source document's path so a flat
// list across the vault stays addressable. It mirrors cli.TaskRow.
type kbTaskRow struct {
	Path string `json:"path"`
	Line int    `json:"line"`
	Done bool   `json:"done"`
	Text string `json:"text"`
}

func (h *handlers) handleKBTasks(ctx context.Context, request mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	scopeArg, _ := request.GetArguments()["path"].(string)
	onlyDone, _ := request.GetArguments()["done"].(bool)
	onlyTodo, _ := request.GetArguments()["todo"].(bool)
	if onlyDone && onlyTodo {
		return mcplib.NewToolResultError("done and todo are mutually exclusive"), nil
	}

	// An optional path scope is untrusted input, so guard it like any other
	// path argument before turning it into a vault-relative prefix.
	scope := ""
	if scopeArg != "" {
		if _, errRes := h.resolvePathArg(scopeArg); errRes != nil {
			return errRes, nil
		}
		scope = filepath.ToSlash(h.vault.RelPath(h.vault.AbsPath(scopeArg)))
	}

	paths, err := h.vault.DB.AllDocumentPaths()
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("list documents: %v", err)), nil
	}

	rows := make([]kbTaskRow, 0)
	for _, p := range paths {
		if !pathInScope(p, scope) {
			continue
		}
		doc, perr := document.ParseFile(h.vault.AbsPath(p))
		if perr != nil {
			continue
		}
		// Read-only synthetic views (.canvas/.base) don't carry editable GFM
		// checkboxes; skip them so a task row never points at a non-writable line.
		if document.IsReadOnlyType(doc.Type) {
			continue
		}
		for _, tk := range document.ExtractTasks(doc.Body) {
			if onlyDone && !tk.Done {
				continue
			}
			if onlyTodo && tk.Done {
				continue
			}
			rows = append(rows, kbTaskRow{Path: p, Line: tk.Line, Done: tk.Done, Text: tk.Text})
		}
	}

	data, _ := json.MarshalIndent(rows, "", "  ")
	return mcplib.NewToolResultText(string(data)), nil
}

// pathInScope reports whether a vault-relative document path falls under the
// given scope. An empty scope matches everything; a scope equal to the path
// matches that single file; otherwise the scope is a directory prefix. Mirrors
// cli.pathInScope so kb_tasks and `2nb tasks` scope identically.
func pathInScope(p, scope string) bool {
	if scope == "" {
		return true
	}
	p = filepath.ToSlash(p)
	if p == scope {
		return true
	}
	prefix := strings.TrimSuffix(scope, "/") + "/"
	return strings.HasPrefix(p, prefix)
}

func (h *handlers) handleKBAppend(ctx context.Context, request mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	path, _ := request.GetArguments()["path"].(string)
	text, _ := request.GetArguments()["text"].(string)

	absPath, errRes := h.resolvePathArg(path)
	if errRes != nil {
		return errRes, nil
	}
	if text == "" {
		return mcplib.NewToolResultError("text is required"), nil
	}

	doc, err := document.ParseFile(absPath)
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("read failed: %v", err)), nil
	}
	doc.Path = path

	doc.Body = doc.Body + "\n" + text

	if errRes := h.writeBodyAndReindex(ctx, doc, absPath); errRes != nil {
		return errRes, nil
	}

	result := map[string]any{"path": doc.Path, "title": doc.Title, "type": doc.Type, "operation": "append"}
	data, _ := json.MarshalIndent(result, "", "  ")
	return mcplib.NewToolResultText(string(data)), nil
}

func (h *handlers) handleKBReplaceSection(ctx context.Context, request mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	path, _ := request.GetArguments()["path"].(string)
	section, _ := request.GetArguments()["section"].(string)
	text, _ := request.GetArguments()["text"].(string)

	absPath, errRes := h.resolvePathArg(path)
	if errRes != nil {
		return errRes, nil
	}
	if section == "" {
		return mcplib.NewToolResultError("section is required"), nil
	}

	doc, err := document.ParseFile(absPath)
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("read failed: %v", err)), nil
	}
	doc.Path = path

	newBody, ok := document.ReplaceSection(doc.Body, section, text)
	if !ok {
		return mcplib.NewToolResultError(fmt.Sprintf("section not found: %q (in %s)", section, path)), nil
	}
	doc.Body = newBody

	if errRes := h.writeBodyAndReindex(ctx, doc, absPath); errRes != nil {
		return errRes, nil
	}

	result := map[string]any{"path": doc.Path, "title": doc.Title, "type": doc.Type, "operation": "replace-section", "section": section}
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
	if !h.vault.ContainsPath(absPath) {
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

	docIDs, embeddings, err := h.getCachedEmbeddings()
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("load embeddings: %v", err)), nil
	}

	scored := search.VectorSearchThreshold(queryVecs[0], docIDs, embeddings, limit*3, h.threshold())

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
