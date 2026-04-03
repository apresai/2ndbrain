package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

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
	results, err := engine.Search(search.Options{
		Query:  query,
		Type:   docType,
		Status: status,
		Tag:    tag,
		Limit:  limit,
	})
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("search failed: %v", err)), nil
	}

	data, _ := json.MarshalIndent(results, "", "  ")
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
