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

	s.AddTool(kbInfoTool(), h.handleKBInfo)
	s.AddTool(kbSearchTool(), h.handleKBSearch)
	s.AddTool(kbAskTool(), h.handleKBAsk)
	s.AddTool(kbReadTool(), h.handleKBRead)
	s.AddTool(kbListTool(), h.handleKBList)
	s.AddTool(kbCreateTool(), h.handleKBCreate)
	s.AddTool(kbUpdateMetaTool(), h.handleKBUpdateMeta)
	s.AddTool(kbRelatedTool(), h.handleKBRelated)
	s.AddTool(kbStructureTool(), h.handleKBStructure)
	s.AddTool(kbDeleteTool(), h.handleKBDelete)
	s.AddTool(kbIndexTool(), h.handleKBIndex)

	return server.ServeStdio(s)
}

func kbInfoTool() mcplib.Tool {
	return mcplib.Tool{
		Name: "kb_info",
		Description: `Get an overview of the knowledge base: vault name, document types with schemas, document counts by type, and AI provider status. Call this FIRST when starting work with the knowledge base to understand what's available.

Example prompts that should trigger this tool:
- "What's in my knowledge base?"
- "What document types do I have?"
- "Show me the vault overview"`,
		InputSchema: mcplib.ToolInputSchema{
			Type:       "object",
			Properties: map[string]any{},
		},
	}
}

func kbSearchTool() mcplib.Tool {
	return mcplib.Tool{
		Name: "kb_search",
		Description: `Search the knowledge base using hybrid BM25 keyword + semantic vector search. Returns ranked results with content snippets, metadata, and relevance scores. Use natural language queries for best semantic results.

Example prompts that should trigger this tool:
- "Search for authentication patterns"
- "Find notes about Stripe integration"
- "What do we know about database migrations?"`,
		InputSchema: mcplib.ToolInputSchema{
			Type: "object",
			Properties: map[string]any{
				"query":  map[string]any{"type": "string", "description": "Natural language search query. Works with keywords ('stripe webhook') and questions ('how does auth work?')."},
				"type":   map[string]any{"type": "string", "description": "Filter by document type: adr, runbook, postmortem, note"},
				"status": map[string]any{"type": "string", "description": "Filter by status: draft, active, accepted, proposed, complete, etc."},
				"tag":    map[string]any{"type": "string", "description": "Filter by tag"},
				"limit":  map[string]any{"type": "integer", "description": "Maximum results (default 10)"},
			},
			Required: []string{"query"},
		},
	}
}

func kbReadTool() mcplib.Tool {
	return mcplib.Tool{
		Name: "kb_read",
		Description: `Read a document's full content with frontmatter metadata, or a specific section by heading name. Use kb_list first to discover paths, then kb_read to get content. Use the chunk parameter to read just one section of a long document.

Example prompts that should trigger this tool:
- "Read the JWT authentication ADR"
- "Show me the Decision section of use-jwt-for-auth.md"
- "What does the debug auth runbook say?"`,
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
		Name: "kb_related",
		Description: `Find documents connected to a given document via [[wikilink]] graph traversal. Returns linked documents up to the specified depth. Useful for understanding context around a topic.

Example prompts that should trigger this tool:
- "What's related to the auth ADR?"
- "Show connected documents for stripe.md"`,
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
		Name: "kb_create",
		Description: `Create a new document from a template. Auto-generates UUID, populates frontmatter with schema defaults, and indexes it for search. Types: adr (architecture decision), runbook (operational procedure), prd (product requirements), prfaq (press release / FAQ), postmortem (incident analysis), note (general knowledge).

Example prompts that should trigger this tool:
- "Create an ADR for switching to PostgreSQL"
- "Write a runbook for deploying the API"
- "Create a PRD for the mobile app redesign"
- "Write a PR/FAQ for the new AI feature"
- "Add a note about the new caching strategy"`,
		InputSchema: mcplib.ToolInputSchema{
			Type: "object",
			Properties: map[string]any{
				"title": map[string]any{"type": "string", "description": "Document title"},
				"type":  map[string]any{"type": "string", "description": "Document type: adr, runbook, prd, prfaq, postmortem, note"},
			},
			Required: []string{"title", "type"},
		},
	}
}

func kbUpdateMetaTool() mcplib.Tool {
	return mcplib.Tool{
		Name: "kb_update_meta",
		Description: `Update frontmatter metadata on a document without changing the body. Validates values against the document type schema (e.g., ADR status must follow: proposed → accepted → deprecated/superseded).

Example prompts that should trigger this tool:
- "Mark the JWT ADR as accepted"
- "Add the 'security' tag to the auth runbook"
- "Update the status of the postmortem to reviewed"`,
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
		Name: "kb_structure",
		Description: `Get the heading outline of a document as a JSON tree. Useful for understanding a document's organization before reading specific sections with kb_read's chunk parameter.

Example prompts that should trigger this tool:
- "Show me the outline of the auth runbook"
- "What sections does this ADR have?"`,
		InputSchema: mcplib.ToolInputSchema{
			Type: "object",
			Properties: map[string]any{
				"path": map[string]any{"type": "string", "description": "Vault-relative path to the document"},
			},
			Required: []string{"path"},
		},
	}
}

func kbDeleteTool() mcplib.Tool {
	return mcplib.Tool{
		Name: "kb_delete",
		Description: `Permanently delete a document from the vault. Removes the file from disk and all index entries (chunks, tags, links, embeddings). This cannot be undone.

Example prompts that should trigger this tool:
- "Delete the old caching note"
- "Remove the draft postmortem"`,
		InputSchema: mcplib.ToolInputSchema{
			Type: "object",
			Properties: map[string]any{
				"path": map[string]any{"type": "string", "description": "Vault-relative path to the document to delete"},
			},
			Required: []string{"path"},
		},
	}
}

func kbListTool() mcplib.Tool {
	return mcplib.Tool{
		Name: "kb_list",
		Description: `List documents in the knowledge base with optional filters. Returns titles, paths, types, and statuses without content. Use this to discover what documents exist before reading them.

Example prompts that should trigger this tool:
- "List all my ADRs"
- "Show draft runbooks"
- "What documents are tagged with 'security'?"`,
		InputSchema: mcplib.ToolInputSchema{
			Type: "object",
			Properties: map[string]any{
				"type":   map[string]any{"type": "string", "description": "Filter by document type (e.g. adr, runbook)"},
				"status": map[string]any{"type": "string", "description": "Filter by status"},
				"tag":    map[string]any{"type": "string", "description": "Filter by tag"},
				"limit":  map[string]any{"type": "integer", "description": "Maximum results (default 100)"},
			},
		},
	}
}

func kbIndexTool() mcplib.Tool {
	return mcplib.Tool{
		Name: "kb_index",
		Description: `Rebuild the search index and generate AI embeddings for all documents. Run this after bulk changes to ensure search results are up to date. Individual document creates are auto-indexed, but this is needed after external edits or imports.

Example prompts that should trigger this tool:
- "Reindex the knowledge base"
- "Update the search index"
- "Rebuild embeddings"`,
		InputSchema: mcplib.ToolInputSchema{
			Type:       "object",
			Properties: map[string]any{},
		},
	}
}

func kbAskTool() mcplib.Tool {
	return mcplib.Tool{
		Name: "kb_ask",
		Description: `Ask a natural language question and get an answer synthesized from the knowledge base using RAG (retrieval-augmented generation). Searches for relevant documents, then generates an answer with source citations. Requires an AI provider to be configured.

Example prompts that should trigger this tool:
- "What authentication approach did we choose and why?"
- "Summarize our Stripe integration setup"
- "What are the steps to debug auth failures?"`,
		InputSchema: mcplib.ToolInputSchema{
			Type: "object",
			Properties: map[string]any{
				"question": map[string]any{"type": "string", "description": "The question to answer based on knowledge base content"},
			},
			Required: []string{"question"},
		},
	}
}
