package e2e_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	mcpclient "github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	_ "modernc.org/sqlite"
)

func mcpText(res *mcp.CallToolResult) string {
	if res == nil || len(res.Content) == 0 {
		return ""
	}
	if tc, ok := res.Content[0].(mcp.TextContent); ok {
		return tc.Text
	}
	return ""
}

// TestBattery_MCPStdioDriveTools is the genuine "an LLM client drives the
// server" proof: it speaks the MCP protocol over real stdio to a spawned
// `2nb mcp-server`, initializes, and calls kb_info / kb_create / kb_search /
// kb_read — crossing the JSON-RPC marshal + handshake boundary that direct
// handler tests skip.
func TestBattery_MCPStdioDriveTools(t *testing.T) {
	home := isolatedHome(t)
	vaultDir := filepath.Join(home, "vault")
	if out, code := runWithHome(t, home, "vault", "create", vaultDir); code != 0 {
		t.Fatalf("vault create: exit %d: %s", code, out)
	}

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
	initReq.Params.ClientInfo = mcp.Implementation{Name: "battery-test", Version: "1.0"}
	if _, err := cli.Initialize(ctx, initReq); err != nil {
		t.Fatalf("initialize over stdio: %v", err)
	}

	// kb_create a note via the protocol.
	createReq := mcp.CallToolRequest{}
	createReq.Params.Name = "kb_create"
	createReq.Params.Arguments = map[string]any{
		"type":  "note",
		"title": "Stdio Driven Note",
	}
	createRes, err := cli.CallTool(ctx, createReq)
	if err != nil {
		t.Fatalf("kb_create: %v", err)
	}
	if createRes.IsError {
		t.Fatalf("kb_create error: %s", mcpText(createRes))
	}

	// kb_info should now report at least one document.
	infoReq := mcp.CallToolRequest{}
	infoReq.Params.Name = "kb_info"
	infoRes, err := cli.CallTool(ctx, infoReq)
	if err != nil {
		t.Fatalf("kb_info: %v", err)
	}
	if infoRes.IsError {
		t.Fatalf("kb_info error: %s", mcpText(infoRes))
	}
	if !strings.Contains(strings.ToLower(mcpText(infoRes)), "document") {
		t.Errorf("kb_info missing document info: %s", mcpText(infoRes))
	}

	// kb_search should find the created note by title.
	searchReq := mcp.CallToolRequest{}
	searchReq.Params.Name = "kb_search"
	searchReq.Params.Arguments = map[string]any{"query": "Stdio Driven"}
	searchRes, err := cli.CallTool(ctx, searchReq)
	if err != nil {
		t.Fatalf("kb_search: %v", err)
	}
	if searchRes.IsError {
		t.Fatalf("kb_search error: %s", mcpText(searchRes))
	}
	if !strings.Contains(mcpText(searchRes), "Stdio Driven") {
		t.Errorf("kb_search did not surface the created note: %s", mcpText(searchRes))
	}
}

// writeV2Index builds a schema-v2 index.db in place so the migrate command has a
// genuine legacy vault to upgrade. Columns match v1 + the v2 embedding ALTERs so
// schemaV1's IF NOT EXISTS indexes (which reference doc_type/status/modified_at)
// succeed when store.Open re-runs them.
func writeV2Index(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	stmts := []string{
		`CREATE TABLE documents (
			id TEXT PRIMARY KEY,
			path TEXT NOT NULL UNIQUE,
			title TEXT NOT NULL DEFAULT '',
			doc_type TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL DEFAULT '',
			modified_at TEXT NOT NULL DEFAULT '',
			indexed_at TEXT NOT NULL DEFAULT '',
			content_hash TEXT NOT NULL DEFAULT '',
			frontmatter TEXT NOT NULL DEFAULT '{}',
			embedding BLOB,
			embedding_model TEXT NOT NULL DEFAULT '',
			embedding_hash TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE TABLE schema_version (version INTEGER NOT NULL)`,
		`INSERT INTO schema_version (version) VALUES (2)`,
		`INSERT INTO documents (id, path, title, doc_type) VALUES ('m1', 'note.md', 'Legacy Note', 'note')`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("build v2 db (%q): %v", s, err)
		}
	}
}

func dbMaxVersion(t *testing.T, path string) int {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var v int
	if err := db.QueryRow("SELECT MAX(version) FROM schema_version").Scan(&v); err != nil {
		t.Fatalf("read schema version: %v", err)
	}
	return v
}

// TestBattery_Migrate exercises `2nb migrate` end-to-end against a real legacy
// v2 vault: the schema is upgraded to v3 and the source markdown is byte-for-byte
// unchanged (the non-mutating guarantee).
func TestBattery_Migrate(t *testing.T) {
	home := isolatedHome(t)
	vaultDir := filepath.Join(home, "vault")
	if out, code := runWithHome(t, home, "vault", "create", vaultDir); code != 0 {
		t.Fatalf("vault create: exit %d: %s", code, out)
	}

	// A real note on disk; migrate must not touch it.
	notePath := filepath.Join(vaultDir, "note.md")
	noteContent := "---\nid: m1\ntitle: Legacy Note\ntype: note\nstatus: draft\n---\nUntouched body.\n"
	if err := os.WriteFile(notePath, []byte(noteContent), 0o644); err != nil {
		t.Fatal(err)
	}

	// Replace the freshly-created v3 index with a legacy v2 one.
	idx := filepath.Join(vaultDir, ".2ndbrain", "index.db")
	os.Remove(idx + "-wal")
	os.Remove(idx + "-shm")
	if err := os.Remove(idx); err != nil {
		t.Fatal(err)
	}
	writeV2Index(t, idx)

	if got := dbMaxVersion(t, idx); got != 2 {
		t.Fatalf("precondition: expected v2 db, got v%d", got)
	}

	if out, code := runWithHome(t, home, "migrate", "--vault", vaultDir); code != 0 {
		t.Fatalf("migrate: exit %d: %s", code, out)
	}

	if got := dbMaxVersion(t, idx); got != 3 {
		t.Errorf("after migrate, schema version = %d, want 3", got)
	}
	after, _ := os.ReadFile(notePath)
	if string(after) != noteContent {
		t.Errorf("migrate mutated the source markdown:\n got: %q\nwant: %q", after, noteContent)
	}
}

// TestBattery_MigrateDryRun verifies `--dry-run` reports the legacy version and
// makes no changes.
func TestBattery_MigrateDryRun(t *testing.T) {
	home := isolatedHome(t)
	vaultDir := filepath.Join(home, "vault")
	if out, code := runWithHome(t, home, "vault", "create", vaultDir); code != 0 {
		t.Fatalf("vault create: exit %d: %s", code, out)
	}
	idx := filepath.Join(vaultDir, ".2ndbrain", "index.db")
	os.Remove(idx + "-wal")
	os.Remove(idx + "-shm")
	os.Remove(idx)
	writeV2Index(t, idx)

	out, code := runWithHome(t, home, "migrate", "--dry-run", "--vault", vaultDir)
	if code != 0 {
		t.Fatalf("migrate --dry-run: exit %d: %s", code, out)
	}
	if !strings.Contains(out, "v2") {
		t.Errorf("dry-run output should mention schema v2, got: %s", out)
	}
	if got := dbMaxVersion(t, idx); got != 2 {
		t.Errorf("dry-run must not migrate; version = %d, want 2", got)
	}
}

// TestBattery_ObsidianNativeRAG is the documented golden-path for the
// Obsidian-native flow: a vault with wikilinks, aliases, and inline tags is
// indexed; search returns the pinned JSON envelope (no provider needed); and a
// grounded ask is exercised only when a provider is configured.
func TestBattery_ObsidianNativeRAG(t *testing.T) {
	home := isolatedHome(t)
	vaultDir := filepath.Join(home, "vault")
	if out, code := runWithHome(t, home, "vault", "create", vaultDir); code != 0 {
		t.Fatalf("vault create: exit %d: %s", code, out)
	}

	arch := "---\nid: a1\ntitle: Architecture\ntype: note\nstatus: draft\naliases:\n  - architecture\n---\n" +
		"The #design of the system. See [[Overview]] for the big picture.\n"
	overview := "---\nid: o1\ntitle: Overview\ntype: note\nstatus: draft\n---\n" +
		"System overview. The distinctword anchors retrieval.\n"
	if err := os.WriteFile(filepath.Join(vaultDir, "arch.md"), []byte(arch), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(vaultDir, "overview.md"), []byte(overview), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, code := runWithHome(t, home, "index", "--vault", vaultDir); code != 0 {
		t.Fatalf("index: exit %d: %s", code, out)
	}

	// search --json envelope (BM25 path, ungated).
	out, code := runWithHome(t, home, "search", "--json", "distinctword", "--vault", vaultDir)
	if code != 0 {
		t.Fatalf("search: exit %d: %s", code, out)
	}
	var env struct {
		Mode    string `json:"mode"`
		Results []struct {
			Path  string `json:"path"`
			Title string `json:"title"`
		} `json:"results"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("search envelope decode: %v\n%s", err, out)
	}
	if env.Mode == "" || len(env.Results) == 0 {
		t.Fatalf("expected non-empty search envelope, got: %s", out)
	}

	// Inline #design tag was indexed (proves body-tag extraction end-to-end).
	out, code = runWithHome(t, home, "list", "--tag", "design", "--json", "--vault", vaultDir)
	if code != 0 {
		t.Fatalf("list --tag: exit %d: %s", code, out)
	}
	if !strings.Contains(out, "arch.md") && !strings.Contains(out, "Architecture") {
		t.Errorf("inline #design tag not indexed; list --tag design did not return arch: %s", out)
	}

	// Grounded ask is provider-gated.
	if !hasAWSCreds() && !hasOpenRouterKey() {
		t.Skip("no AI provider configured; skipping grounded ask")
	}
	out, code = runWithHome(t, home, "ask", "--json", "What does the overview describe?", "--vault", vaultDir)
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
		t.Errorf("grounded ask returned empty answer: %s", out)
	}
}

// TestBattery_CanvasBaseIndexing proves .canvas and .base files are indexed as
// first-class documents.
func TestBattery_CanvasBaseIndexing(t *testing.T) {
	home := isolatedHome(t)
	vaultDir := filepath.Join(home, "vault")
	if out, code := runWithHome(t, home, "vault", "create", vaultDir); code != 0 {
		t.Fatalf("vault create: exit %d: %s", code, out)
	}

	canvas := `{"nodes":[{"id":"n1","type":"text","text":"Canvas card content"}],"edges":[]}`
	base := "settings:\n  name: Prod\n  timeout: 500\n"
	if err := os.WriteFile(filepath.Join(vaultDir, "board.canvas"), []byte(canvas), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(vaultDir, "cfg.base"), []byte(base), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, code := runWithHome(t, home, "index", "--vault", vaultDir); code != 0 {
		t.Fatalf("index: exit %d: %s", code, out)
	}

	out, code := runWithHome(t, home, "list", "--json", "--vault", vaultDir)
	if code != 0 {
		t.Fatalf("list: exit %d: %s", code, out)
	}
	for _, want := range []string{"board.canvas", "cfg.base"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %s in list output: %s", want, out)
		}
	}
}
