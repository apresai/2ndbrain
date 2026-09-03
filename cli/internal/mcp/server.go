package mcp

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/apresai/2ndbrain/internal/ai"
	"github.com/apresai/2ndbrain/internal/metrics"
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

// Per-tool timeouts. Cheap metadata/graph calls stay tight; the budgets that
// wrap a provider call derive from the transport worst cases in internal/ai
// so the innermost bound always fires first and a timeout names the real
// subsystem (TestToolBudgetsNested pins the nesting). These bound hangs; a
// slow-but-working provider is never failed by them.
const (
	// mcpEmbedBudget bounds the single query-embed call inside the retrieval
	// pipeline. Shared with BOTH WithEmbedTimeout call sites in tools.go so
	// the tSearch derivation below cannot drift from what the pipeline
	// actually enforces.
	mcpEmbedBudget = 60 * time.Second

	tCheap  = 10 * time.Second
	tCreate = 30 * time.Second
	// tSearch strictly CONTAINS the embed budget it wraps, leaving room for
	// the BM25 + KNN work after the embed returns. It used to EQUAL the embed
	// budget (60s == 60s), so both bounds fired together and a stuck embed
	// was misattributed to the search tool.
	tSearch = mcpEmbedBudget + 30*time.Second
	// tGenerate wraps kb_ask / kb_polish: one retrieval embed (mcpEmbedBudget)
	// plus a generation call whose worst case is the mantle plane's full
	// retry budget. The old flat 120s sat far inside that budget and killed
	// working cold-start reasoning models mid-answer.
	// The trailing slack covers the pre-generation work (Available probe,
	// retrieval, context assembly) so the tool bound can never fire while
	// the inner transport bound would still legitimately succeed: strict
	// containment, never equality, same rule as tSearch.
	tGenerate = ai.MantleWorstCase + mcpEmbedBudget + 30*time.Second
	tIndex    = 300 * time.Second
)

// DoctorExercisedBudget is the wall-clock budget one `2nb mcp doctor` run
// needs: the per-tool timeouts of every engine tool the self-test exercises
// sequentially (kb_info, kb_list, kb_search), plus slack for the AI-readiness
// probe and the config/WAL reads around them. Deriving it HERE, next to the
// budgets it contains, keeps the doctor's outer deadline nested outside the
// engine budgets it wraps: the old flat 15s cap made the 60s search budget
// dead code and blamed the index whenever the clock expired first.
func DoctorExercisedBudget() time.Duration {
	const doctorChecksSlack = 10 * time.Second
	return tCheap + tCheap + tSearch + doctorChecksSlack
}

func mcpToolRegistrations(h *handlers) []toolRegistration {
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
func newMCPServer(v *vault.Vault, version string, opts ...serverOption) (*server.MCPServer, *StatusWriter, *metrics.DB) {
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

	// Observatory recorder: one metrics.db handle reused across tool calls (the
	// long-lived server can't afford a per-call open/close). Best-effort — a nil
	// recorder just means MCP-driven ops go unrecorded; it never blocks startup.
	var metricsDB *metrics.DB
	if mdb, err := metrics.Open(filepath.Join(v.DotDir, "metrics.db")); err == nil {
		metricsDB = mdb
	} else {
		slog.Warn("mcp metrics recorder unavailable", "err", err)
	}

	addTool := func(tool mcplib.Tool, handler server.ToolHandlerFunc) {
		// Innermost wrap: record op latency for the perf-relevant tools, tagged
		// source=mcp, so the observatory sees agent-driven search/ask/index too.
		if op := mcpMetricOp(tool.Name); op != "" && metricsDB != nil {
			handler = wrapMCPMetric(metricsDB, op, version, handler)
		}
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

	return s, statusWriter, metricsDB
}

// mcpMetricOp maps an MCP tool name to the observatory operation it should be
// recorded as, or "" for tools that aren't performance-interesting (read-only
// metadata, graph, git). The write tools that reindex map to index_doc.
func mcpMetricOp(tool string) string {
	switch tool {
	case "kb_search":
		return metrics.OpSearch
	case "kb_ask":
		return metrics.OpAsk
	case "kb_index":
		return metrics.OpIndex
	case "kb_append", "kb_replace_section", "kb_create", "kb_update_meta":
		return metrics.OpIndexDoc
	default:
		return ""
	}
}

// mcpMetricDetail carries the token/count detail a metered handler knows but the
// generic wrapper can't see (it would otherwise have to parse the serialized
// tool result). wrapMCPMetric seeds an empty one into the context; the handler
// fills what it has via recordMCPDetail, and the wrapper folds it into the row.
type mcpMetricDetail struct {
	InputTokens  int
	OutputTokens int
	ResultCount  int
	DocsIndexed  int
	Embedded     int
	TotalChars   int
	Mode         string
	// EmbedRetries is filled by the wrapper from the call's own retry counter,
	// not by a handler: no handler can see how many times the provider was
	// retried underneath it.
	EmbedRetries int
}

type mcpMetricDetailKey struct{}

// recordMCPDetail mutates the in-flight metric detail for this tool call when the
// context carries one (it does for the metered tools). It's a no-op otherwise,
// so a handler may call it unconditionally without caring whether it's metered.
func recordMCPDetail(ctx context.Context, f func(*mcpMetricDetail)) {
	if d, ok := ctx.Value(mcpMetricDetailKey{}).(*mcpMetricDetail); ok && d != nil {
		f(d)
	}
}

// wrapMCPMetric records one operation row (best-effort) around a tool handler.
// It seeds an mcpMetricDetail into the context so the handler can attach real
// token usage + counts (recordMCPDetail) — matching the CLI path, so agent-driven
// ask/search/index rows carry tokens and result/doc counts rather than zeros.
func wrapMCPMetric(mdb *metrics.DB, op, version string, fn server.ToolHandlerFunc) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
		start := time.Now()
		detail := &mcpMetricDetail{}
		ctx = context.WithValue(ctx, mcpMetricDetailKey{}, detail)
		// A FRESH counter per call, never shared: two concurrent tool calls
		// would otherwise each report the other's retries. Without this every
		// MCP row recorded embed_retries 0 while the CLI rows recorded the real
		// number, so the observatory's retry history had a hole exactly the
		// shape of agent-driven work.
		retries := &ai.RetryCounter{}
		ctx = ai.WithRetryCounter(ctx, retries)
		result, err := fn(ctx, req)
		detail.EmbedRetries = retries.Count()
		errMsg := ""
		if err != nil {
			errMsg = err.Error()
		}
		_ = mdb.Record(metrics.Operation{
			Operation:    op,
			Source:       "mcp",
			DurationMs:   time.Since(start).Milliseconds(),
			OK:           err == nil,
			Error:        errMsg,
			CLIVersion:   version,
			InputTokens:  detail.InputTokens,
			OutputTokens: detail.OutputTokens,
			ResultCount:  detail.ResultCount,
			DocsIndexed:  detail.DocsIndexed,
			Embedded:     detail.Embedded,
			TotalChars:   detail.TotalChars,
			Mode:         detail.Mode,
			EmbedRetries: detail.EmbedRetries,
		})
		return result, err
	}
}

// Start runs the stdio MCP server until its client disconnects, its parent (the
// client) process dies, a signal arrives, or — when idleTimeout > 0 — the server
// has been idle that long.
//
// The server exits instantly when the client closes the stdio pipe (ServeStdio
// returns on stdin EOF) and promptly when the client process dies WITHOUT
// closing the pipe (the parent-death watchdog), so a crashed or closed session
// never leaves an orphan holding the index open. It does NOT self-exit while a
// client is connected: idleTimeout is an opt-in activity cap, disabled by
// default (idleTimeout <= 0).
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
	var metricsDB *metrics.DB
	s, statusWriter, metricsDB = newMCPServer(v, version, sOpts...)

	if statusWriter != nil {
		sigs := make(chan os.Signal, 1)
		signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
		go func() {
			<-sigs
			statusWriter.Remove()
			os.Exit(0)
		}()
	}

	// Parent-death watchdog: an stdio server's parent is its MCP client, so when
	// the client process dies without cleanly closing the pipe (crash, SIGKILL,
	// leaked child) the parent PID changes and we exit promptly — reaping the
	// orphan that would otherwise pin the index/WAL. It never fires while the
	// client is connected, so it does not interrupt a live-but-quiet session.
	// Always on; replaces the activity-idle timer's orphan-cleanup role.
	parent := newParentWatchdog(defaultParentPollInterval, func(startPPID, nowPPID int) {
		if statusWriter != nil {
			statusWriter.Remove()
		}
		slog.Info("mcp server exiting; client/parent process gone",
			"start_ppid", startPPID, "now_ppid", nowPPID)
		os.Exit(0)
	})
	go parent.run()

	// Launch the idle watchdog after statusWriter is assigned so its onExpire
	// closure observes the writer. No-op when idleTimeout <= 0 (the default).
	if watchdog != nil {
		go watchdog.run()
	}

	// Wrap stdout so tools/list omits empty annotations/required that mcp-go
	// always emits. Claude Code still sees the same 22 tools either way.
	stdio := server.NewStdioServer(s)
	err := stdio.Listen(context.Background(), os.Stdin, newToolsListSanitizer(os.Stdout))
	if statusWriter != nil {
		statusWriter.Remove()
	}
	// Close on the clean-disconnect path. The os.Exit watchdog paths (parent
	// death, idle, signal) skip this, but metrics.db is WAL-mode so an unclosed
	// handle on process exit is crash-safe and reclaimed on the next open.
	if metricsDB != nil {
		metricsDB.Close()
	}
	return err
}

func kbInfoTool() mcplib.Tool {
	return kbTool("kb_info",
		"Vault overview: name, root, types/schemas, counts, AI readiness. Call first.",
		map[string]any{},
		nil)
}

func kbSearchTool() mcplib.Tool {
	return kbTool("kb_search",
		"Hybrid BM25 plus vector search. Required: query. Rank by vector_score (cosine), not score (RRF). Optional type, status, tag, limit.",
		map[string]any{
			"query":  map[string]any{"type": "string", "description": "Natural language search query. Works with keywords ('stripe webhook') and questions ('how does auth work?')."},
			"type":   map[string]any{"type": "string", "description": "Filter by document type: adr, runbook, postmortem, note"},
			"status": map[string]any{"type": "string", "description": "Filter by status: draft, active, accepted, proposed, complete, etc."},
			"tag":    map[string]any{"type": "string", "description": "Filter by tag"},
			"limit":  map[string]any{"type": "integer", "description": "Maximum results (default 10)"},
		},
		[]string{"query"})
}

func kbReadTool() mcplib.Tool {
	return kbTool("kb_read",
		"Read a vault-relative path, or one heading via chunk. Required: path.",
		map[string]any{
			"path":  map[string]any{"type": "string", "description": "Vault-relative path to the document (e.g. use-jwt-for-auth.md)"},
			"chunk": map[string]any{"type": "string", "description": "Optional heading name to read only that section (e.g. 'Decision', 'Context')"},
		},
		[]string{"path"})
}

func kbRelatedTool() mcplib.Tool {
	return kbTool("kb_related",
		"Wikilink graph from path, depth N (default 2). Required: path. For semantic \"should link\", use kb_suggest_links.",
		map[string]any{
			"path":  map[string]any{"type": "string", "description": "Vault-relative path to the document"},
			"depth": map[string]any{"type": "integer", "description": "Maximum traversal depth (default 2)"},
		},
		[]string{"path"})
}

func kbCreateTool() mcplib.Tool {
	return kbTool("kb_create",
		"Create from template (UUID plus frontmatter). Required: title, type (adr|runbook|prd|prfaq|postmortem|note). Optional path subdirectory. Search first.",
		map[string]any{
			"title": map[string]any{"type": "string", "description": "Document title"},
			"type":  map[string]any{"type": "string", "description": "Document type: adr, runbook, prd, prfaq, postmortem, note"},
			"path":  map[string]any{"type": "string", "description": "Optional vault-relative subdirectory to create the document in (e.g. \"resources\"). Created if missing. Defaults to the vault root."},
		},
		[]string{"title", "type"})
}

func kbUpdateMetaTool() mcplib.Tool {
	return kbTool("kb_update_meta",
		"Schema-validated frontmatter only. Required: path, fields. Status transitions are enforced.",
		map[string]any{
			"path":   map[string]any{"type": "string", "description": "Vault-relative path to the document"},
			"fields": map[string]any{"type": "object", "description": "Key-value pairs of frontmatter fields to update"},
		},
		[]string{"path", "fields"})
}

func kbStructureTool() mcplib.Tool {
	return kbTool("kb_structure",
		"Heading tree for path. Required: path. Then kb_read with chunk.",
		map[string]any{
			"path": map[string]any{"type": "string", "description": "Vault-relative path to the document"},
		},
		[]string{"path"})
}

func kbDeleteTool() mcplib.Tool {
	return kbTool("kb_delete",
		"Delete file plus index. Required: path. Irreversible except via git.",
		map[string]any{
			"path": map[string]any{"type": "string", "description": "Vault-relative path to the document to delete"},
		},
		[]string{"path"})
}

func kbListTool() mcplib.Tool {
	return kbTool("kb_list",
		"Enumerate docs by type/status/tag without content. No query. Follow with kb_read.",
		map[string]any{
			"type":   map[string]any{"type": "string", "description": "Filter by document type (e.g. adr, runbook)"},
			"status": map[string]any{"type": "string", "description": "Filter by status"},
			"tag":    map[string]any{"type": "string", "description": "Filter by tag"},
			"limit":  map[string]any{"type": "integer", "description": "Maximum results (default 100)"},
		},
		nil)
}

func kbIndexTool() mcplib.Tool {
	return kbTool("kb_index",
		"Full reindex plus re-embed. Only after bulk external edits or model switch.",
		map[string]any{},
		nil)
}

func kbGitActivityTool() mcplib.Tool {
	return kbTool("kb_git_activity",
		"Recent vault commits. Optional since_days (default 7). No-op if not a git repo.",
		map[string]any{
			"since_days": map[string]any{"type": "integer", "description": "Days to look back (default 7)"},
		},
		nil)
}

func kbGitDiffTool() mcplib.Tool {
	return kbTool("kb_git_diff",
		"Unified diff of path vs HEAD. Required: path.",
		map[string]any{
			"path": map[string]any{"type": "string", "description": "Vault-relative path to the file"},
		},
		[]string{"path"})
}

func kbGitStatusTool() mcplib.Tool {
	return kbTool("kb_git_status",
		"Porcelain map of dirty/untracked vault files.",
		map[string]any{},
		nil)
}

func kbPolishTool() mcplib.Tool {
	return kbTool("kb_polish",
		"Copy-edit preview; does not write. Required: path. Optional links, system.",
		map[string]any{
			"path":   map[string]any{"type": "string", "description": "Vault-relative path to the document to polish"},
			"system": map[string]any{"type": "string", "description": "Optional system prompt override (default: copy-editor)"},
			"links":  map[string]any{"type": "boolean", "description": "Also propose grounded [[wikilinks]] to existing notes (never invents targets); default false"},
		},
		[]string{"path"})
}

func kbSuggestLinksTool() mcplib.Tool {
	return kbTool("kb_suggest_links",
		"Semantic wikilink candidates for path, excluding already-linked. Required: path.",
		map[string]any{
			"path":  map[string]any{"type": "string", "description": "Vault-relative path to the source document"},
			"limit": map[string]any{"type": "integer", "description": "Maximum number of suggestions (default 10)"},
		},
		[]string{"path"})
}

func kbBacklinksTool() mcplib.Tool {
	return kbTool("kb_backlinks",
		"Resolved inbound links to path. Required: path. Check before delete/rename.",
		map[string]any{
			"path": map[string]any{"type": "string", "description": "Vault-relative path to the document"},
		},
		[]string{"path"})
}

func kbLinksTool() mcplib.Tool {
	return kbTool("kb_links",
		"Outbound links from path, including broken (resolved=false). Required: path.",
		map[string]any{
			"path": map[string]any{"type": "string", "description": "Vault-relative path to the document"},
		},
		[]string{"path"})
}

func kbTagsTool() mcplib.Tool {
	return kbTool("kb_tags",
		"Vault tag vocabulary with counts.",
		map[string]any{},
		nil)
}

func kbTasksTool() mcplib.Tool {
	return kbTool("kb_tasks",
		"GFM checkboxes (- [ ] / - [x]) vault-wide or under path. Optional done, todo, path.",
		map[string]any{
			"path": map[string]any{"type": "string", "description": "Optional vault-relative file or directory to limit the listing to. Omit to scan the whole vault."},
			"done": map[string]any{"type": "boolean", "description": "Only completed tasks"},
			"todo": map[string]any{"type": "boolean", "description": "Only open tasks (do not combine with done)"},
		},
		nil)
}

func kbAppendTool() mcplib.Tool {
	return kbTool("kb_append",
		"Append text to body end, then reindex. Required: path, text. Frontmatter untouched.",
		map[string]any{
			"path": map[string]any{"type": "string", "description": "Vault-relative path to the document"},
			"text": map[string]any{"type": "string", "description": "Text to append to the end of the body"},
		},
		[]string{"path", "text"})
}

func kbReplaceSectionTool() mcplib.Tool {
	return kbTool("kb_replace_section",
		"Replace one heading body. Required: path, section, text. Call kb_structure first for heading names.",
		map[string]any{
			"path":    map[string]any{"type": "string", "description": "Vault-relative path to the document"},
			"section": map[string]any{"type": "string", "description": "Heading whose section content to replace (e.g. 'Decision' or '## Decision')"},
			"text":    map[string]any{"type": "string", "description": "Replacement content for that section"},
		},
		[]string{"path", "section", "text"})
}

func kbAskTool() mcplib.Tool {
	return kbTool("kb_ask",
		"RAG answer plus source paths. Required: question. Verify cites with kb_read. If no hits, drop to kb_search.",
		map[string]any{
			"question": map[string]any{"type": "string", "description": "The question to answer based on knowledge base content"},
		},
		[]string{"question"})
}
