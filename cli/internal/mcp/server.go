package mcp

import (
	"github.com/apresai/2ndbrain/internal/vault"
	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func Start(v *vault.Vault, version string) error {
	s := server.NewMCPServer(
		"2ndbrain",
		version,
		server.WithToolCapabilities(true),
	)

	h := &handlers{vault: v}

	s.AddTool(kbSearchTool(), h.handleKBSearch)
	s.AddTool(kbReadTool(), h.handleKBRead)
	s.AddTool(kbRelatedTool(), h.handleKBRelated)
	s.AddTool(kbCreateTool(), h.handleKBCreate)
	s.AddTool(kbUpdateMetaTool(), h.handleKBUpdateMeta)
	s.AddTool(kbStructureTool(), h.handleKBStructure)

	return server.ServeStdio(s)
}

func kbSearchTool() mcplib.Tool {
	return mcplib.Tool{
		Name:        "kb_search",
		Description: "Search the knowledge base with hybrid BM25 + semantic search. Returns ranked results with chunk content, metadata, and relevance scores.",
		InputSchema: mcplib.ToolInputSchema{
			Type: "object",
			Properties: map[string]any{
				"query":  map[string]any{"type": "string", "description": "Search query text"},
				"type":   map[string]any{"type": "string", "description": "Filter by document type (e.g. adr, runbook, postmortem, note)"},
				"status": map[string]any{"type": "string", "description": "Filter by document status (e.g. accepted, draft, active)"},
				"tag":    map[string]any{"type": "string", "description": "Filter by tag"},
				"limit":  map[string]any{"type": "integer", "description": "Maximum results to return (default 10)"},
			},
			Required: []string{"query"},
		},
	}
}

func kbReadTool() mcplib.Tool {
	return mcplib.Tool{
		Name:        "kb_read",
		Description: "Read a document from the knowledge base. Returns the full document content with frontmatter, or a specific section identified by heading path.",
		InputSchema: mcplib.ToolInputSchema{
			Type: "object",
			Properties: map[string]any{
				"path":  map[string]any{"type": "string", "description": "Vault-relative path to the document (e.g. use-jwt-for-auth.md)"},
				"chunk": map[string]any{"type": "string", "description": "Optional heading name to read only that section (e.g. 'Decision', 'Context')"},
			},
			Required: []string{"path"},
		},
	}
}

func kbRelatedTool() mcplib.Tool {
	return mcplib.Tool{
		Name:        "kb_related",
		Description: "Find documents related to a given document via wikilink graph traversal. Returns connected documents up to the specified depth.",
		InputSchema: mcplib.ToolInputSchema{
			Type: "object",
			Properties: map[string]any{
				"path":  map[string]any{"type": "string", "description": "Vault-relative path to the document"},
				"depth": map[string]any{"type": "integer", "description": "Maximum traversal depth (default 2)"},
			},
			Required: []string{"path"},
		},
	}
}

func kbCreateTool() mcplib.Tool {
	return mcplib.Tool{
		Name:        "kb_create",
		Description: "Create a new document in the knowledge base from a template. Auto-generates a UUID and populates frontmatter based on the document type schema.",
		InputSchema: mcplib.ToolInputSchema{
			Type: "object",
			Properties: map[string]any{
				"title": map[string]any{"type": "string", "description": "Document title"},
				"type":  map[string]any{"type": "string", "description": "Document type: adr, runbook, postmortem, note"},
			},
			Required: []string{"title", "type"},
		},
	}
}

func kbUpdateMetaTool() mcplib.Tool {
	return mcplib.Tool{
		Name:        "kb_update_meta",
		Description: "Update frontmatter fields on a document without modifying the body content. Validates against the document type schema.",
		InputSchema: mcplib.ToolInputSchema{
			Type: "object",
			Properties: map[string]any{
				"path":   map[string]any{"type": "string", "description": "Vault-relative path to the document"},
				"fields": map[string]any{"type": "object", "description": "Key-value pairs of frontmatter fields to update"},
			},
			Required: []string{"path", "fields"},
		},
	}
}

func kbStructureTool() mcplib.Tool {
	return mcplib.Tool{
		Name:        "kb_structure",
		Description: "Get the heading structure of a document as a JSON tree with chunk IDs. Useful for understanding document organization before reading specific sections.",
		InputSchema: mcplib.ToolInputSchema{
			Type: "object",
			Properties: map[string]any{
				"path": map[string]any{"type": "string", "description": "Vault-relative path to the document"},
			},
			Required: []string{"path"},
		},
	}
}
