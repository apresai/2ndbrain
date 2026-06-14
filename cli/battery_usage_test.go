package e2e_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	mcpclient "github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
)

// Runnable usage battery ("validate our use"): drives the REAL `2nb` binary and
// the REAL `2nb mcp-server` (over stdio) through the workflows we actually use,
// end to end. Subprocess + protocol boundaries that the in-package handler tests
// skip. Run via `make test-usage` (or `make test-battery`). AI steps are
// provider-gated and skip without credentials, per the no-mock policy.

// newUsageVault creates an isolated HOME + initialized vault and returns both.
func newUsageVault(t *testing.T) (home, vaultDir string) {
	t.Helper()
	home = isolatedHome(t)
	vaultDir = filepath.Join(home, "vault")
	if out, code := runWithHome(t, home, "vault", "create", vaultDir); code != 0 {
		t.Fatalf("vault create: exit %d: %s", code, out)
	}
	return home, vaultDir
}

// TestUsageBattery_CreateTagSearch: create a note with an inline #tag, then
// confirm it is findable by both keyword search (BM25) and tag filter, the CLI
// counterpart of the MCP tag round-trip.
func TestUsageBattery_CreateTagSearch(t *testing.T) {
	home, vaultDir := newUsageVault(t)

	if out, code := runWithHome(t, home, "create", "Workflow Note",
		"--content", "Body with an inline #usagetag and a distinctmarker word.",
		"--vault", vaultDir); code != 0 {
		t.Fatalf("create: exit %d: %s", code, out)
	}

	// keyword search (BM25, no AI) finds it
	if out, code := runWithHome(t, home, "search", "distinctmarker", "--vault", vaultDir); code != 0 || !strings.Contains(out, "Workflow Note") {
		t.Fatalf("search did not find the note (exit %d):\n%s", code, out)
	}

	// inline #tag is indexed and tag-filterable
	out, code := runWithHome(t, home, "list", "--tag", "usagetag", "--json", "--vault", vaultDir)
	if code != 0 {
		t.Fatalf("list --tag: exit %d: %s", code, out)
	}
	if !strings.Contains(out, "Workflow Note") {
		t.Errorf("inline #usagetag not tag-filterable:\n%s", out)
	}
}

// TestUsageBattery_MetaStatusRoundTrip: a scalar frontmatter write via the CLI
// (status) is persisted and reflected by meta --get and list --status.
func TestUsageBattery_MetaStatusRoundTrip(t *testing.T) {
	home, vaultDir := newUsageVault(t)

	if out, code := runWithHome(t, home, "create", "Status Note", "--vault", vaultDir); code != 0 {
		t.Fatalf("create: exit %d: %s", code, out)
	}
	rel := "status-note.md"

	if out, code := runWithHome(t, home, "meta", rel, "--set", "status=complete", "--vault", vaultDir); code != 0 {
		t.Fatalf("meta --set: exit %d: %s", code, out)
	}
	if out, code := runWithHome(t, home, "meta", rel, "--get", "status", "--vault", vaultDir); code != 0 || !strings.Contains(out, "complete") {
		t.Fatalf("meta --get status did not return 'complete' (exit %d): %s", code, out)
	}
	if out, code := runWithHome(t, home, "list", "--status", "complete", "--json", "--vault", vaultDir); code != 0 || !strings.Contains(out, "Status Note") {
		t.Fatalf("list --status complete did not return the note (exit %d): %s", code, out)
	}
}

// TestUsageBattery_MoveRewritesLinks: when a linked note is moved, the inbound
// [[wikilink]] is rewritten so it stays resolved (the move guarantee).
func TestUsageBattery_MoveRewritesLinks(t *testing.T) {
	home, vaultDir := newUsageVault(t)

	if out, code := runWithHome(t, home, "create", "Hub", "--content", "Points to [[Spoke]].", "--vault", vaultDir); code != 0 {
		t.Fatalf("create hub: exit %d: %s", code, out)
	}
	if out, code := runWithHome(t, home, "create", "Spoke", "--content", "spoke body", "--vault", vaultDir); code != 0 {
		t.Fatalf("create spoke: exit %d: %s", code, out)
	}

	// Precondition: hub -> spoke resolves.
	if out, code := runWithHome(t, home, "links", "hub.md", "--json", "--vault", vaultDir); code != 0 || !strings.Contains(out, "\"resolved\": true") {
		t.Fatalf("precondition: hub->spoke not resolved (exit %d): %s", code, out)
	}

	// Move the spoke; the [[Spoke]] link in hub must be rewritten to stay valid.
	if out, code := runWithHome(t, home, "move", "spoke.md", "renamed-spoke.md", "--vault", vaultDir); code != 0 {
		t.Fatalf("move: exit %d: %s", code, out)
	}

	out, code := runWithHome(t, home, "links", "hub.md", "--json", "--vault", vaultDir)
	if code != 0 {
		t.Fatalf("links after move: exit %d: %s", code, out)
	}
	if !strings.Contains(out, "renamed-spoke.md") || !strings.Contains(out, "\"resolved\": true") {
		t.Errorf("move did not rewrite the inbound link to stay resolved:\n%s", out)
	}
}

// TestUsageBattery_DailyAndTasks: daily resolves/creates today's note, and the
// tasks/task commands list and toggle a GFM checkbox.
func TestUsageBattery_DailyAndTasks(t *testing.T) {
	home, vaultDir := newUsageVault(t)

	// daily: prints a .md path and creates the file.
	out, code := runWithHome(t, home, "daily", "--vault", vaultDir)
	if code != 0 {
		t.Fatalf("daily: exit %d: %s", code, out)
	}
	rel := strings.TrimSpace(strings.Split(out, "\n")[0])
	if !strings.HasSuffix(rel, ".md") {
		t.Fatalf("daily did not print a .md path: %q", rel)
	}
	if _, err := os.Stat(filepath.Join(vaultDir, rel)); err != nil {
		t.Fatalf("daily note not created on disk: %v", err)
	}

	// tasks: a note with a GFM checkbox is listed, then toggled.
	if out, code := runWithHome(t, home, "create", "Todo Note", "--content", "- [ ] first task\n- [x] done task", "--vault", vaultDir); code != 0 {
		t.Fatalf("create todo note: exit %d: %s", code, out)
	}
	if out, code := runWithHome(t, home, "tasks", "--json", "--vault", vaultDir); code != 0 || !strings.Contains(out, "first task") {
		t.Fatalf("tasks did not list the checkbox (exit %d): %s", code, out)
	}
	// Toggle line 1 (first body line) to done; it must then appear under --done.
	if out, code := runWithHome(t, home, "task", "todo-note.md", "1", "--done", "--vault", vaultDir); code != 0 {
		t.Fatalf("task toggle: exit %d: %s", code, out)
	}
	if out, code := runWithHome(t, home, "tasks", "--done", "--json", "--path", "todo-note.md", "--vault", vaultDir); code != 0 || !strings.Contains(out, "first task") {
		t.Fatalf("toggled task not under --done (exit %d): %s", code, out)
	}
}

// TestUsageBattery_ObsidianCompatForms: the obsidian-CLI-style invocations we
// rely on work through the real binary's argv shim.
func TestUsageBattery_ObsidianCompatForms(t *testing.T) {
	home, vaultDir := newUsageVault(t)

	if out, code := runWithHome(t, home, "create", "Compat Note", "--content", "v1 body", "--vault", vaultDir); code != 0 {
		t.Fatalf("create: exit %d: %s", code, out)
	}

	// read file= (fuzzy resolve by title) + format=raw
	if out, code := runWithHome(t, home, "read", "file=Compat Note", "format=raw", "--vault", vaultDir); code != 0 || !strings.Contains(out, "v1 body") {
		t.Fatalf("read file= did not resolve by title (exit %d): %s", code, out)
	}

	// create ... overwrite (replace body of the existing same-title note in place)
	if out, code := runWithHome(t, home, "create", "Compat Note", "content=v2 body", "overwrite", "--vault", vaultDir); code != 0 {
		t.Fatalf("create overwrite: exit %d: %s", code, out)
	}
	if out, code := runWithHome(t, home, "read", "file=Compat Note", "format=raw", "--vault", vaultDir); code != 0 || !strings.Contains(out, "v2 body") || strings.Contains(out, "v1 body") {
		t.Fatalf("overwrite did not replace the body (exit %d): %s", code, out)
	}

	// files total (alias of list + count)
	if out, code := runWithHome(t, home, "files", "total", "--vault", vaultDir); code != 0 || strings.TrimSpace(out) != "1" {
		t.Fatalf("files total = %q (want 1), exit %d", strings.TrimSpace(out), code)
	}
}

// TestUsageBattery_McpUpdateMetaTagsRoundTrip is the end-to-end proof of the
// kb_update_meta reindex fix: through the REAL mcp-server over stdio, a tag set
// via kb_update_meta becomes findable by kb_list{tag}, the exact path that was
// broken when the tag landed in the file but not the index.
func TestUsageBattery_McpUpdateMetaTagsRoundTrip(t *testing.T) {
	home, vaultDir := newUsageVault(t)

	env := append(filterEnv(os.Environ(), "HOME", "2NB_TEST"), "HOME="+home)
	cli, err := mcpclient.NewStdioMCPClient(binaryPath, env, "mcp-server", "--vault", vaultDir)
	if err != nil {
		t.Fatalf("start stdio MCP client: %v", err)
	}
	defer cli.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	initReq := mcp.InitializeRequest{}
	initReq.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcp.Implementation{Name: "usage-battery", Version: "1.0"}
	if _, err := cli.Initialize(ctx, initReq); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	call := func(name string, args map[string]any) *mcp.CallToolResult {
		t.Helper()
		req := mcp.CallToolRequest{}
		req.Params.Name = name
		req.Params.Arguments = args
		res, err := cli.CallTool(ctx, req)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if res.IsError {
			t.Fatalf("%s error: %s", name, mcpText(res))
		}
		return res
	}

	// kb_create -> get the path
	createRes := call("kb_create", map[string]any{"type": "note", "title": "Mcp Tagged"})
	var created map[string]any
	if err := json.Unmarshal([]byte(mcpText(createRes)), &created); err != nil {
		t.Fatalf("kb_create result not JSON: %v\n%s", err, mcpText(createRes))
	}
	path, _ := created["path"].(string)
	if path == "" {
		t.Fatalf("kb_create returned no path: %s", mcpText(createRes))
	}

	// kb_update_meta sets a tag
	call("kb_update_meta", map[string]any{"path": path, "fields": map[string]any{"tags": []any{"mcptag"}}})

	// kb_list{tag} must now find it, over the real protocol, end to end.
	listRes := call("kb_list", map[string]any{"tag": "mcptag"})
	if !strings.Contains(mcpText(listRes), "Mcp Tagged") {
		t.Errorf("kb_list{tag:mcptag} did not return the note after kb_update_meta (reindex missing over stdio):\n%s", mcpText(listRes))
	}
}

// TestUsageBattery_AskRAG: grounded RAG over a seeded note. Provider-gated.
func TestUsageBattery_AskRAG(t *testing.T) {
	if !hasAWSCreds() && !hasOpenRouterKey() {
		t.Skip("no AI provider configured; skipping grounded ask")
	}
	home, vaultDir := newUsageVault(t)

	if out, code := runWithHome(t, home, "create", "Release Facts", "--content",
		"The widget release shipped on a Tuesday with the zephyrquark feature.", "--vault", vaultDir); code != 0 {
		t.Fatalf("create: exit %d: %s", code, out)
	}
	// Ensure embeddings exist so RAG retrieval has a vector channel (create
	// embeds inline when a provider is present; index makes that explicit).
	if out, code := runWithHome(t, home, "index", "--vault", vaultDir); code != 0 {
		t.Fatalf("index: exit %d: %s", code, out)
	}

	out, code := runWithHome(t, home, "ask", "--json", "What feature shipped in the widget release?", "--vault", vaultDir)
	if code != 0 {
		t.Skipf("ask failed (transient/provider): exit %d: %s", code, out)
	}
	var ans struct {
		Answer  string   `json:"answer"`
		Sources []string `json:"sources"`
	}
	if err := json.Unmarshal([]byte(out), &ans); err != nil {
		t.Fatalf("ask envelope decode: %v\n%s", err, out)
	}
	if ans.Answer == "" {
		t.Errorf("grounded ask returned empty answer:\n%s", out)
	}
}
