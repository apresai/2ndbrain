package mcp

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/apresai/2ndbrain/internal/vault"
	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// withTimeout wraps a tool handler so a slow upstream (Bedrock, OpenRouter)
// can't hang the MCP client indefinitely. Budgets are per-tool: read-only
// graph/metadata calls get a tight bound; generation/index calls get room
// for real work. The MCP library doesn't expose a per-tool deadline knob,
// so we enforce it at registration time.
func withTimeout(d time.Duration, inner server.ToolHandlerFunc) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
		ctx, cancel := context.WithTimeout(ctx, d)
		defer cancel()
		return inner(ctx, req)
	}
}

type toolRegistration struct {
	tool    mcplib.Tool
	timeout time.Duration
	handler server.ToolHandlerFunc
}

func mcpToolRegistrations(h *handlers) []toolRegistration {
	// Per-tool timeouts. Cheap metadata/graph calls: 10s. Search + suggest:
	// 60s (includes one embed call). Generation-bound tools: 120s. Create:
	// 30s (may embed the new doc). Full reindex: 300s.
	const (
		tCheap    = 10 * time.Second
		tCreate   = 30 * time.Second
		tSearch   = 60 * time.Second
		tGenerate = 120 * time.Second
		tIndex    = 300 * time.Second
	)
	return []toolRegistration{
		{kbInfoTool(), tCheap, h.handleKBInfo},
		{kbSearchTool(), tSearch, h.handleKBSearch},
		{kbAskTool(), tGenerate, h.handleKBAsk},
		{kbReadTool(), tCheap, h.handleKBRead},
		{kbListTool(), tCheap, h.handleKBList},
		{kbCreateTool(), tCreate, h.handleKBCreate},
		{kbUpdateMetaTool(), tCheap, h.handleKBUpdateMeta},
		{kbRelatedTool(), tCheap, h.handleKBRelated},
		{kbStructureTool(), tCheap, h.handleKBStructure},
		{kbDeleteTool(), tCheap, h.handleKBDelete},
		{kbIndexTool(), tIndex, h.handleKBIndex},
		{kbSuggestLinksTool(), tSearch, h.handleKBSuggestLinks},
		{kbPolishTool(), tGenerate, h.handleKBPolish},
		{kbGitActivityTool(), tCheap, h.handleKBGitActivity},
		{kbGitDiffTool(), tCheap, h.handleKBGitDiff},
		{kbGitStatusTool(), tCheap, h.handleKBGitStatus},
		{kbBacklinksTool(), tCheap, h.handleKBBacklinks},
		{kbLinksTool(), tCheap, h.handleKBLinks},
		{kbTagsTool(), tCheap, h.handleKBTags},
		{kbTasksTool(), tCheap, h.handleKBTasks},
		{kbAppendTool(), tCreate, h.handleKBAppend},
		{kbReplaceSectionTool(), tCreate, h.handleKBReplaceSection},
	}
}

// serverConfig holds optional wrappers applied during server construction.
type serverConfig struct {
	idle *idleWatchdog
}

type serverOption func(*serverConfig)

// withIdleWatchdog registers an idle watchdog whose wrap() is applied to every
// tool handler so the server tracks activity and can self-exit when idle.
func withIdleWatchdog(w *idleWatchdog) serverOption {
	return func(c *serverConfig) { c.idle = w }
}

// newMCPServer builds a fully-configured MCP server for the vault: the
// instructions string, tool-capability flag, and all kb_* tools registered
// through the same status-writer + per-tool-timeout wrappers the live server
// uses. It is the single source of truth for server construction so the stdio
// server (Start), a future in-process self-test (mcp doctor), and tests all
// exercise the identical registration. The returned StatusWriter may be nil (its setup
// is best-effort and must not block the server) and is owned by the caller.
func newMCPServer(v *vault.Vault, version string, opts ...serverOption) (*server.MCPServer, *StatusWriter) {
	cfg := &serverConfig{}
	for _, o := range opts {
		o(cfg)
	}

	s := server.NewMCPServer(
		"2ndbrain",
		version,
		server.WithToolCapabilities(true),
		server.WithInstructions(ServerInstructions),
	)

	h := &handlers{vault: v}

	// Status writer records per-invocation telemetry to .2ndbrain/mcp/<pid>.json
	// so the editor can display live MCP server state. Failure here shouldn't
	// prevent the server from starting.
	var statusWriter *StatusWriter
	if sw, err := NewStatusWriter(v); err == nil {
		statusWriter = sw
	} else {
		slog.Warn("mcp status writer unavailable", "err", err)
	}

	addTool := func(tool mcplib.Tool, handler server.ToolHandlerFunc) {
		if statusWriter != nil {
			handler = statusWriter.Wrap(tool.Name, handler)
		}
		// The idle wrap goes OUTERMOST so inFlight is decremented only after the
		// status flush (inside statusWriter.Wrap) has completed.
		if cfg.idle != nil {
			handler = cfg.idle.wrap(handler)
		}
		s.AddTool(tool, handler)
	}

	for _, reg := range mcpToolRegistrations(h) {
		addTool(reg.tool, withTimeout(reg.timeout, reg.handler))
	}

	return s, statusWriter
}

// Start runs the stdio MCP server until its client disconnects, a signal
// arrives, or — when idleTimeout > 0 — the server has been idle that long (it
// then exits cleanly so a closed session doesn't leave an orphan). idleTimeout
// <= 0 disables the idle self-exit.
func Start(v *vault.Vault, version string, idleTimeout time.Duration) error {
	var statusWriter *StatusWriter

	var sOpts []serverOption
	var watchdog *idleWatchdog
	if idleTimeout > 0 {
		watchdog = newIdleWatchdog(idleTimeout, func() {
			if statusWriter != nil {
				statusWriter.Remove()
			}
			slog.Info("mcp server exiting after idle timeout", "timeout", idleTimeout.String())
			os.Exit(0)
		})
		sOpts = append(sOpts, withIdleWatchdog(watchdog))
	}

	var s *server.MCPServer
	s, statusWriter = newMCPServer(v, version, sOpts...)

	if statusWriter != nil {
		sigs := make(chan os.Signal, 1)
		signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
		go func() {
			<-sigs
			statusWriter.Remove()
			os.Exit(0)
		}()
	}

	// Launch the idle watchdog after statusWriter is assigned so its onExpire
	// closure observes the writer. No-op when idleTimeout <= 0.
	if watchdog != nil {
		go watchdog.run()
	}

	err := server.ServeStdio(s)
	if statusWriter != nil {
		statusWriter.Remove()
	}
	return err
}

func kbInfoTool() mcplib.Tool {
	return mcplib.Tool{
		Name: "kb_info",
		Description: `Get an overview of the knowledge base: vault name, vault root path, document types with schemas, document counts by type, and AI provider status. Call this FIRST when starting work with the knowledge base to confirm which vault you're connected to and what's available.

After kb_info, typical next moves:
- kb_list (filter by type/tag to discover what exists before creating)
- kb_search (natural-language query for specific content)
- kb_ask (synthesize an answer across multiple docs)

Example prompts that should trigger this tool:
- "What's in my knowledge base?"
- "What document types do I have?"
- "Show me the vault overview"
- "Which vault am I working with?"`,
		InputSchema: mcplib.ToolInputSchema{
			Type:       "object",
			Properties: map[string]any{},
		},
	}
}

func kbSearchTool() mcplib.Tool {
	return mcplib.Tool{
		Name: "kb_search",
		Description: `Search the knowledge base using hybrid BM25 keyword + semantic vector search. Returns ranked results with content snippets, metadata, and two relevance numbers per result: "score" is the Reciprocal Rank Fusion score (good for ordering, opaque as a relevance signal), and "vector_score" is the raw cosine similarity (use this to judge how relevant a hit actually is). Hits below the configured similarity threshold (default 0.20) are filtered out entirely.

Interpreting vector_score:
- >= 0.6 — strong semantic match
- 0.35 – 0.6 — related
- 0.20 – 0.35 — weakly related
- missing — this was a BM25-only match (still valid, just no vector signal)

After kb_search, typical next moves:
- kb_read to fetch the full content for a promising result
- kb_structure if the doc is long and you want to pick a specific chunk
- kb_related to explore what links out from a result

If kb_search returns nothing, broaden the query or drop filters. If kb_ask said "no relevant documents", drop to kb_search with a less specific query — kb_ask is stricter.

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
		Description: `Read a document's full content with frontmatter metadata, or a specific section by heading name. Paths are always vault-relative (e.g. "use-jwt-for-auth.md" or "subdir/foo.md") — absolute paths will fail.

Typical flow:
- Before kb_read, call kb_list or kb_search to discover the path.
- For long documents, call kb_structure first to see the heading tree, then pass one of those headings as the "chunk" argument to avoid pulling the whole body.
- If kb_read fails with "path outside vault", you probably have an absolute path or a stale path — re-run kb_list to get the canonical vault-relative path.

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
		Description: `Find documents connected to a given document via [[wikilink]] graph traversal. Returns linked documents up to the specified depth. This is the explicit-connection view — for semantic similarity (docs that aren't linked but discuss related things), use kb_suggest_links instead.

kb_related (this tool) vs kb_suggest_links:
- kb_related — "what does this doc actually link to?" — uses the [[wikilink]] graph
- kb_suggest_links — "what SHOULD this doc link to based on content similarity?" — uses vector embeddings

Example prompts that should trigger this tool:
- "What's related to the auth ADR?"
- "Show connected documents for stripe.md"
- "Follow the links out from this postmortem"`,
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

IMPORTANT before calling:
1. Run kb_search (or kb_list with a relevant tag/type filter) to check for duplicates. Vaults accumulate duplicates fast when agents skip this step.
2. Confirm the vault root via kb_info if you're not sure which vault you're writing to — kb_create writes to the vault configured for this MCP server, not to the filesystem location of any prompts.

Pass "path" to file the new document in a vault-relative subdirectory (e.g. "resources"); the directory is created if missing. Omit it to write to the vault root.

After kb_create, typical next moves:
- kb_read the new path to get the template body.
- Use a file-edit tool (or kb_update_meta for frontmatter-only changes) to fill in the body with [[wikilinks]] to related docs found in step 1.
- No need to call kb_index — creation auto-indexes.

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
				"path":  map[string]any{"type": "string", "description": "Optional vault-relative subdirectory to create the document in (e.g. \"resources\"). Created if missing. Defaults to the vault root."},
			},
			Required: []string{"title", "type"},
		},
	}
}

func kbUpdateMetaTool() mcplib.Tool {
	return mcplib.Tool{
		Name: "kb_update_meta",
		Description: `Update frontmatter metadata on a document without changing the body. Validates values against the document type schema and enforces status state machines:
- adr: proposed → accepted → deprecated/superseded
- runbook: draft → active → archived
- postmortem: draft → reviewed → published
- prd: draft → review → approved → shipped → archived
- prfaq: draft → review → final

Jumping over a state (e.g., proposed → superseded directly) will be rejected. Walk the transitions.

This tool preserves the existing frontmatter verbatim (comments, key order, and the "modified" value) and updates only the fields you set — it does NOT bump "modified" automatically. If you want to record an edit time, set "modified" explicitly. To change the body, use a regular file-edit tool.

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
		Description: `Get the heading outline of a document as a JSON tree. Useful for long documents where you don't want to read the whole body — call kb_structure first to pick a heading, then kb_read with that heading as the "chunk" argument.

Typical flow:
1. kb_search or kb_list → find a path
2. kb_structure → see the heading tree
3. kb_read with chunk="<heading>" → fetch just the section you need

For short documents (< 500 lines), it's usually faster to skip kb_structure and just kb_read the whole thing.

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
		Description: `Permanently delete a document from the vault. Removes the file from disk AND all index entries (chunks, tags, links, embeddings). This cannot be undone by 2nb — if the vault is git-backed, the file is still recoverable via git, otherwise it's gone.

Before calling:
1. Confirm the exact path with kb_list or kb_search. Similar titles can have different UUIDs.
2. Check for inbound wikilinks with kb_related — deleting a heavily-linked doc leaves dangling references that 2nb lint will flag.
3. Prefer kb_update_meta to change status (e.g., adr → deprecated) if the content is still worth preserving for history.

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
		Description: `List documents in the knowledge base with optional filters. Returns titles, paths, types, and statuses WITHOUT content (so it's cheap and cache-friendly).

kb_list vs kb_search:
- kb_list — "what exists?" — enumerate by metadata filters. No query needed.
- kb_search — "what matches this idea?" — requires a query, ranks by relevance.

Use kb_list when you know the filter dimensions (type, tag, status) and want an exhaustive list. Use kb_search when you have a topic in mind and want the best matches. Results from kb_list are always feed-forward: follow up with kb_read to get content for any path you care about.

Example prompts that should trigger this tool:
- "List all my ADRs"
- "Show draft runbooks"
- "What documents are tagged with 'security'?"
- "What notes do I have about caching?" (alternate: use kb_search if you want ranked semantic matches)`,
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
		Description: `Rebuild the search index and generate AI embeddings for all documents. This is usually unnecessary — kb_create, kb_update_meta, and the macOS editor all auto-index their own changes, and embeddings only regenerate for docs whose content hash changed.

Call kb_index only when:
- You've bulk-imported or externally edited a lot of files (e.g., rsync'd content into the vault)
- You're debugging stale search results and want to force a full rebuild
- You switched embedding models and need to re-embed everything

For a single document that you edited externally, you can skip kb_index and just wait — the next save from the editor will trigger an incremental re-embed. Or run "2nb index --doc <path>" on the CLI if you need it now.

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

func kbGitActivityTool() mcplib.Tool {
	return mcplib.Tool{
		Name: "kb_git_activity",
		Description: `Show recent git commits that touched files in the vault. Only works when the vault is a git repository. Returns hash, author, date, subject, and changed files for each commit.

Example prompts that should trigger this tool:
- "What have I changed in the last week?"
- "Show recent vault activity"`,
		InputSchema: mcplib.ToolInputSchema{
			Type: "object",
			Properties: map[string]any{
				"since_days": map[string]any{"type": "integer", "description": "Days to look back (default 7)"},
			},
		},
	}
}

func kbGitDiffTool() mcplib.Tool {
	return mcplib.Tool{
		Name: "kb_git_diff",
		Description: `Return the unified diff of a vault file against HEAD. Only works when the vault is a git repository. Returns an empty diff if the file is untracked or unchanged.

Example prompts that should trigger this tool:
- "Show changes to the JWT ADR"
- "What did I change in stripe.md?"`,
		InputSchema: mcplib.ToolInputSchema{
			Type: "object",
			Properties: map[string]any{
				"path": map[string]any{"type": "string", "description": "Vault-relative path to the file"},
			},
			Required: []string{"path"},
		},
	}
}

func kbGitStatusTool() mcplib.Tool {
	return mcplib.Tool{
		Name: "kb_git_status",
		Description: `List uncommitted and untracked files in the vault as a map of path → git porcelain status code (M=modified, A=added, D=deleted, ??=untracked). Only works when the vault is a git repository.

Example prompts that should trigger this tool:
- "What's dirty in the vault?"
- "Show files I haven't committed yet"`,
		InputSchema: mcplib.ToolInputSchema{
			Type:       "object",
			Properties: map[string]any{},
		},
	}
}

func kbPolishTool() mcplib.Tool {
	return mcplib.Tool{
		Name: "kb_polish",
		Description: `Run an AI copy-editor pass over a document and return both the original and polished body. The caller is expected to present a diff for user review. Fixes spelling, grammar, and awkward phrasing while preserving voice, wikilinks, and structure. With links:true it also adds grounded [[wikilinks]] to existing notes (never invents a target). Does NOT write the result back to disk.

Example prompts that should trigger this tool:
- "Polish the JWT auth ADR for spelling and grammar"
- "Clean up the writing in stripe-integration.md and link related notes"`,
		InputSchema: mcplib.ToolInputSchema{
			Type: "object",
			Properties: map[string]any{
				"path":   map[string]any{"type": "string", "description": "Vault-relative path to the document to polish"},
				"system": map[string]any{"type": "string", "description": "Optional system prompt override (default: copy-editor)"},
				"links":  map[string]any{"type": "boolean", "description": "Also propose grounded [[wikilinks]] to existing notes (never invents targets); default false"},
			},
			Required: []string{"path"},
		},
	}
}

func kbSuggestLinksTool() mcplib.Tool {
	return mcplib.Tool{
		Name: "kb_suggest_links",
		Description: `Suggest semantically related documents that would make good [[wikilink]] targets from the given document. Uses vector search to find similar content, excluding documents already linked. Returns ranked candidates with title, path, score, and snippet.

Example prompts that should trigger this tool:
- "What should I link to from the JWT ADR?"
- "Find wikilink candidates for my caching notes"`,
		InputSchema: mcplib.ToolInputSchema{
			Type: "object",
			Properties: map[string]any{
				"path":  map[string]any{"type": "string", "description": "Vault-relative path to the source document"},
				"limit": map[string]any{"type": "integer", "description": "Maximum number of suggestions (default 10)"},
			},
			Required: []string{"path"},
		},
	}
}

func kbBacklinksTool() mcplib.Tool {
	return mcplib.Tool{
		Name: "kb_backlinks",
		Description: `List the resolved inbound links to a document: every other document that links to it via a [[wikilink]] that resolved to a real indexed doc. Returns the source path and title plus the link's heading, alias, and raw form. Paths are always vault-relative (e.g. "use-jwt-for-auth.md"); absolute paths will fail.

kb_backlinks vs kb_links vs kb_related:
- kb_backlinks (this tool): "what links INTO this doc?" (resolved inbound links only)
- kb_links: "what does this doc link OUT to?" (outbound links, including broken ones)
- kb_related: "what is this doc connected to?" (multi-hop [[wikilink]] graph traversal to depth N)

Use kb_backlinks before deleting or renaming a document to see what would dangle.

Example prompts that should trigger this tool:
- "What links to the JWT auth ADR?"
- "Show backlinks for stripe.md"
- "Which notes reference this postmortem?"`,
		InputSchema: mcplib.ToolInputSchema{
			Type: "object",
			Properties: map[string]any{
				"path": map[string]any{"type": "string", "description": "Vault-relative path to the document"},
			},
			Required: []string{"path"},
		},
	}
}

func kbLinksTool() mcplib.Tool {
	return mcplib.Tool{
		Name: "kb_links",
		Description: `List the outbound links from a document, INCLUDING unresolved (broken) ones. Each link carries a "resolved" boolean, so this doubles as a per-file broken-link view: a link with resolved=false points at a [[name]] that has no matching document in the vault. For resolved links the path and title name the target document; for unresolved links they are empty. Paths are always vault-relative; absolute paths will fail.

kb_links vs kb_backlinks vs kb_suggest_links:
- kb_links (this tool): "what does this doc link OUT to, and which links are broken?"
- kb_backlinks: "what links INTO this doc?" (resolved inbound links)
- kb_suggest_links: "what SHOULD this doc link to?" (semantic candidates it isn't linked to yet)

Example prompts that should trigger this tool:
- "What does the auth ADR link to?"
- "Show outbound links from stripe.md"
- "Are there any broken links in this note?"`,
		InputSchema: mcplib.ToolInputSchema{
			Type: "object",
			Properties: map[string]any{
				"path": map[string]any{"type": "string", "description": "Vault-relative path to the document"},
			},
			Required: []string{"path"},
		},
	}
}

func kbTagsTool() mcplib.Tool {
	return mcplib.Tool{
		Name: "kb_tags",
		Description: `List every tag used in the vault with the number of documents that carry it, ordered by descending count. Use this to discover the vault's tag vocabulary before filtering kb_list / kb_search by tag, or before adding a tag with kb_update_meta so you reuse an existing tag instead of inventing a near-duplicate.

After kb_tags, typical next moves:
- kb_list (tag="<tag>") to enumerate the documents under a tag
- kb_search (tag="<tag>") to rank matches within a tag

Example prompts that should trigger this tool:
- "What tags do I use?"
- "List all tags with counts"
- "Which tags are most common in my vault?"`,
		InputSchema: mcplib.ToolInputSchema{
			Type:       "object",
			Properties: map[string]any{},
		},
	}
}

func kbTasksTool() mcplib.Tool {
	return mcplib.Tool{
		Name: "kb_tasks",
		Description: `List GFM checkbox tasks ("- [ ]" open, "- [x]" done) found across the vault, or within one file or directory. Each task carries its document path, the 1-based body line, the done state, and the task text. Checkboxes inside fenced code blocks are ignored. v1 recognizes GFM open/done only; custom statuses such as [>] or [-] (Obsidian Tasks plugin extensions) are not treated as tasks.

Filters:
- done=true: only completed tasks
- todo=true: only open tasks (don't set both)
- path: limit to a vault-relative file (e.g. "projects/launch.md") or a directory subtree (e.g. "projects")

Example prompts that should trigger this tool:
- "What are my open todos?"
- "List the checkboxes in projects/launch.md"
- "Show completed tasks in my vault"`,
		InputSchema: mcplib.ToolInputSchema{
			Type: "object",
			Properties: map[string]any{
				"path": map[string]any{"type": "string", "description": "Optional vault-relative file or directory to limit the listing to. Omit to scan the whole vault."},
				"done": map[string]any{"type": "boolean", "description": "Only completed tasks"},
				"todo": map[string]any{"type": "boolean", "description": "Only open tasks (do not combine with done)"},
			},
		},
	}
}

func kbAppendTool() mcplib.Tool {
	return mcplib.Tool{
		Name: "kb_append",
		Description: `Append text to the END of a document's body. The frontmatter is left untouched; the new text is added after the existing body (separated by a newline), then the document is reindexed and re-embedded so a newly appended [[wikilink]] or #tag is reflected immediately. This is an explicit body write (2nb otherwise never rewrites note bodies). Paths are always vault-relative; absolute paths will fail. Read-only synthetic .canvas/.base files are rejected.

kb_append vs kb_replace_section vs kb_update_meta:
- kb_append (this tool): add text at the end of the body
- kb_replace_section: replace the content under one heading, leaving the rest intact
- kb_update_meta: change frontmatter fields only, never the body

Example prompts that should trigger this tool:
- "Append a note to my daily log"
- "Add a new bullet to the runbook's checklist"
- "Tack this paragraph onto the end of stripe.md"`,
		InputSchema: mcplib.ToolInputSchema{
			Type: "object",
			Properties: map[string]any{
				"path": map[string]any{"type": "string", "description": "Vault-relative path to the document"},
				"text": map[string]any{"type": "string", "description": "Text to append to the end of the body"},
			},
			Required: []string{"path", "text"},
		},
	}
}

func kbReplaceSectionTool() mcplib.Tool {
	return mcplib.Tool{
		Name: "kb_replace_section",
		Description: `Replace the content under a single heading, leaving the heading line and every sibling section untouched. Section matching is case-insensitive on the heading title and ignores leading "#" markers, so section="Decision" and section="## Decision" match the same heading; when a heading title appears more than once, the first match wins. Returns a tool error if the heading is not found. After the write the document is reindexed and re-embedded. This is an explicit body write (2nb otherwise never rewrites note bodies). Paths are always vault-relative; absolute paths will fail. Read-only synthetic .canvas/.base files are rejected.

Tip: call kb_structure first to see the exact heading names before replacing.

kb_replace_section vs kb_append vs kb_update_meta:
- kb_replace_section (this tool): overwrite the body under one heading
- kb_append: add text at the very end of the body
- kb_update_meta: change frontmatter fields only, never the body

Example prompts that should trigger this tool:
- "Rewrite the Decision section of the JWT ADR"
- "Replace the Steps section in the deploy runbook"`,
		InputSchema: mcplib.ToolInputSchema{
			Type: "object",
			Properties: map[string]any{
				"path":    map[string]any{"type": "string", "description": "Vault-relative path to the document"},
				"section": map[string]any{"type": "string", "description": "Heading whose section content to replace (e.g. 'Decision' or '## Decision')"},
				"text":    map[string]any{"type": "string", "description": "Replacement content for that section"},
			},
			Required: []string{"path", "section", "text"},
		},
	}
}

func kbAskTool() mcplib.Tool {
	return mcplib.Tool{
		Name: "kb_ask",
		Description: `Ask a natural-language question and get an answer synthesized from the knowledge base using RAG (retrieval-augmented generation). Internally: runs hybrid search, takes the top 5 chunks above the similarity threshold, feeds them to the configured generation provider with the question, and returns the answer + source paths.

Requires both an embedding provider and a generation provider to be configured (check with kb_info). Returns an error if either is missing.

After kb_ask, typical next moves:
- kb_read each path in the "sources" field to verify the answer — RAG can hallucinate details from retrieved chunks.
- If kb_ask says "no relevant documents found", the similarity threshold filtered everything out. Fall back to kb_search with the same query (it doesn't gate on threshold the same way) to see if there's anything borderline worth reading manually.

kb_ask vs kb_search:
- kb_ask — "I want a synthesized answer that cites vault content" — LLM does the work
- kb_search — "I want the raw matching documents ranked by relevance" — you do the reading

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
