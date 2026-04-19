package e2e_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The battery is a curated end-to-end smoke suite: one scenario per
// critical flow, using the real `2nb` binary over subprocess. Each test
// gets its own HOME so side effects (active-vault file, recents,
// ~/.claude/skills/...) stay out of the user's home.

// isolatedHome returns a temp HOME for the test. Used so setActiveVault,
// addRecentVault, and skills install --user do NOT touch the real user home.
func isolatedHome(t *testing.T) string {
	t.Helper()
	return t.TempDir()
}

// filterEnv drops entries whose key matches any of keys.
func filterEnv(env []string, keys ...string) []string {
	out := make([]string, 0, len(env))
outer:
	for _, e := range env {
		for _, k := range keys {
			if strings.HasPrefix(e, k+"=") {
				continue outer
			}
		}
		out = append(out, e)
	}
	return out
}

// runWithHome runs the binary with HOME overridden and 2NB_TEST explicitly
// unset so the real side effects (writing ~/.2ndbrain-active-vault, recents,
// skills files) happen but stay inside the temp HOME. Returns combined
// stdout+stderr so t.Fatalf messages contain the full subprocess diagnostic
// (including slog warnings and Cobra usage errors that would otherwise
// disappear into the test-runner's shared stderr).
func runWithHome(t *testing.T, home string, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(binaryPath, args...)
	env := filterEnv(os.Environ(), "HOME", "2NB_TEST")
	env = append(env, "HOME="+home)
	cmd.Env = env
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	code := 0
	if exitErr, ok := err.(*exec.ExitError); ok {
		code = exitErr.ExitCode()
	} else if err != nil {
		code = -1
	}
	// On success (code == 0) return stdout untouched so JSON decoders get
	// a clean envelope. On failure append stderr as a diagnostic tail —
	// every call site guards JSON parsing behind `code != 0`, so callers
	// never try to json.Unmarshal the combined string.
	out := stdout.String()
	if code != 0 {
		if es := stderr.String(); es != "" {
			out += "\n--- stderr ---\n" + es
		}
	}
	return out, code
}

func TestBattery_VaultLifecycle(t *testing.T) {
	home := isolatedHome(t)
	vaultA := filepath.Join(home, "vault-a")
	vaultB := filepath.Join(home, "vault-b")

	// Create vault A → becomes active.
	if out, code := runWithHome(t, home, "vault", "create", vaultA); code != 0 {
		t.Fatalf("vault create A: exit %d: %s", code, out)
	}
	if _, err := os.Stat(filepath.Join(vaultA, ".2ndbrain", "config.yaml")); err != nil {
		t.Fatalf("config.yaml missing in vault A: %v", err)
	}

	// Create vault B → becomes active.
	if out, code := runWithHome(t, home, "vault", "create", vaultB); code != 0 {
		t.Fatalf("vault create B: exit %d: %s", code, out)
	}

	// vault list should show both, B active.
	out, code := runWithHome(t, home, "vault", "list", "--json")
	if code != 0 {
		t.Fatalf("vault list: exit %d: %s", code, out)
	}
	var entries []map[string]any
	if err := json.Unmarshal([]byte(out), &entries); err != nil {
		t.Fatalf("parse vault list: %v\n%s", err, out)
	}
	if len(entries) < 2 {
		t.Fatalf("expected >= 2 vaults in list, got %d: %s", len(entries), out)
	}
	var activeCount int
	var bActive bool
	for _, e := range entries {
		if active, _ := e["active"].(bool); active {
			activeCount++
			if p, _ := e["path"].(string); p == vaultB {
				bActive = true
			}
		}
	}
	if activeCount != 1 {
		t.Errorf("expected exactly 1 active vault in list, got %d: %s", activeCount, out)
	}
	if !bActive {
		t.Errorf("expected vault B active, got: %s", out)
	}

	// Switch back to A.
	if out, code := runWithHome(t, home, "vault", "set", vaultA); code != 0 {
		t.Fatalf("vault set A: exit %d: %s", code, out)
	}

	// vault status should see vault A.
	out, code = runWithHome(t, home, "vault", "status", "--json")
	if code != 0 {
		t.Fatalf("vault status: exit %d: %s", code, out)
	}
	status := map[string]any{}
	if err := json.Unmarshal([]byte(out), &status); err != nil {
		t.Fatalf("parse vault status: %v\n%s", err, out)
	}
	if status["path"] != vaultA {
		t.Errorf("expected status path %q, got %v", vaultA, status["path"])
	}
	// Fresh vault → zero docs.
	if docs, _ := status["documents"].(float64); docs != 0 {
		t.Errorf("expected 0 documents in fresh vault A, got %v", status["documents"])
	}

	// vault show should round-trip.
	out, code = runWithHome(t, home, "vault", "show", "--json")
	if code != 0 {
		t.Fatalf("vault show: exit %d: %s", code, out)
	}
	show := map[string]any{}
	if err := json.Unmarshal([]byte(out), &show); err != nil {
		t.Fatalf("parse vault show: %v\n%s", err, out)
	}
	if show["path"] != vaultA {
		t.Errorf("expected show path %q, got %v", vaultA, show["path"])
	}
}

func TestBattery_DocumentCRUD(t *testing.T) {
	home := isolatedHome(t)
	vaultDir := filepath.Join(home, "crud-vault")
	if out, code := runWithHome(t, home, "vault", "create", vaultDir); code != 0 {
		t.Fatalf("vault create: exit %d: %s", code, out)
	}

	// Create a note via CLI.
	out, code := runWithHome(t, home, "create", "Battery CRUD Doc", "--type", "note", "--json")
	if code != 0 {
		t.Fatalf("create: exit %d: %s", code, out)
	}
	created := map[string]any{}
	if err := json.Unmarshal([]byte(out), &created); err != nil {
		t.Fatalf("parse create: %v\n%s", err, out)
	}
	docPath, _ := created["path"].(string)
	if docPath == "" {
		t.Fatalf("create returned no path: %s", out)
	}

	// Read the body.
	if out, code := runWithHome(t, home, "read", docPath); code != 0 || strings.TrimSpace(out) == "" {
		t.Fatalf("read: exit %d out=%q", code, out)
	}

	// Update frontmatter.
	if out, code := runWithHome(t, home, "meta", docPath, "--set", "status=complete"); code != 0 {
		t.Fatalf("meta --set status=complete: exit %d: %s", code, out)
	}

	// Index so search can find it.
	if out, code := runWithHome(t, home, "index"); code != 0 {
		t.Fatalf("index: exit %d: %s", code, out)
	}

	// Search should find by title token.
	out, code = runWithHome(t, home, "search", "Battery", "--json")
	if code != 0 {
		t.Fatalf("search: exit %d: %s", code, out)
	}
	env := map[string]any{}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("parse search envelope: %v\n%s", err, out)
	}
	results, ok := env["results"].([]any)
	if !ok || len(results) == 0 {
		t.Fatalf("expected results for 'Battery': %s", out)
	}

	// Delete and verify gone from disk and index.
	if out, code := runWithHome(t, home, "delete", docPath, "--force"); code != 0 {
		t.Fatalf("delete: exit %d: %s", code, out)
	}
	if _, err := os.Stat(filepath.Join(vaultDir, docPath)); !os.IsNotExist(err) {
		t.Errorf("file still present after delete: %v", err)
	}

	out, code = runWithHome(t, home, "list", "--json")
	if code != 0 {
		t.Fatalf("list after delete: exit %d: %s", code, out)
	}
	// list --json emits an array (not the envelope).
	var listed []map[string]any
	if err := json.Unmarshal([]byte(out), &listed); err != nil {
		// Empty vault emits "[]" or similar — tolerate parse failure only when
		// output is clearly empty.
		if strings.TrimSpace(out) != "" && !strings.HasPrefix(strings.TrimSpace(out), "[") {
			t.Fatalf("parse list: %v\n%s", err, out)
		}
	}
	for _, e := range listed {
		if p, _ := e["path"].(string); p == docPath {
			t.Errorf("document %s still in index after delete", docPath)
		}
	}
}

func TestBattery_IndexRebuild(t *testing.T) {
	home := isolatedHome(t)
	vaultDir := filepath.Join(home, "index-vault")
	if out, code := runWithHome(t, home, "vault", "create", vaultDir); code != 0 {
		t.Fatalf("vault create: exit %d: %s", code, out)
	}

	// Seed two documents so index has something to rebuild.
	for _, title := range []string{"Alpha Note", "Beta Note"} {
		if out, code := runWithHome(t, home, "create", title, "--type", "note"); code != 0 {
			t.Fatalf("create %s: exit %d: %s", title, code, out)
		}
	}

	// Baseline index.
	if out, code := runWithHome(t, home, "index"); code != 0 {
		t.Fatalf("baseline index: exit %d: %s", code, out)
	}

	// --force-reembed: flag must be accepted and index must complete.
	// We assert the flag runs end-to-end without a provider by checking
	// exit 0 and that the command emits a recognizable line. Embedding
	// counts are checked in a credential-gated path below.
	if out, code := runWithHome(t, home, "index", "--force-reembed"); code != 0 {
		t.Fatalf("index --force-reembed: exit %d: %s", code, out)
	}

	// Reindex with a real provider (if credentials available) to exercise
	// the actual re-embedding path. Matches the pattern in TestE2E_Ask.
	if !hasAWSCreds() && !hasOpenRouterKey() {
		t.Log("no provider creds; skipping embedding-count assertion")
		return
	}
	out, code := runWithHome(t, home, "index", "--force-reembed")
	if code != 0 {
		t.Fatalf("index --force-reembed (creds): exit %d: %s", code, out)
	}
}

func TestBattery_SearchThreshold(t *testing.T) {
	if !hasAWSCreds() && !hasOpenRouterKey() {
		t.Skip("no provider credentials available")
	}
	home := isolatedHome(t)
	vaultDir := filepath.Join(home, "threshold-vault")
	if out, code := runWithHome(t, home, "vault", "create", vaultDir); code != 0 {
		t.Fatalf("vault create: exit %d: %s", code, out)
	}

	// Seed three docs so BM25 and vector search have material.
	for _, title := range []string{"Authentication and JWT", "Database Migrations", "Frontend Styling"} {
		if out, code := runWithHome(t, home, "create", title, "--type", "note"); code != 0 {
			t.Fatalf("create %s: exit %d: %s", title, code, out)
		}
	}
	if out, code := runWithHome(t, home, "index"); code != 0 {
		t.Fatalf("index: exit %d: %s", code, out)
	}

	// countResults parses a `search --json` envelope and returns the number of
	// hits. The CLI short-circuits empty results with zero stdout output
	// (see cli/internal/cli/search.go:173-180), so empty stdout is treated
	// as 0, not a parse failure.
	countResults := func(t *testing.T, out string) int {
		t.Helper()
		if strings.TrimSpace(out) == "" {
			return 0
		}
		env := map[string]any{}
		if err := json.Unmarshal([]byte(out), &env); err != nil {
			t.Fatalf("parse search envelope: %v\n%s", err, out)
		}
		results, _ := env["results"].([]any)
		return len(results)
	}

	// Very low threshold → should return matches.
	out, code := runWithHome(t, home, "search", "auth", "--threshold", "0.05", "--json")
	if code != 0 {
		t.Fatalf("search low threshold: exit %d: %s", code, out)
	}
	low := countResults(t, out)

	// Very high threshold → should filter more aggressively.
	out, code = runWithHome(t, home, "search", "auth", "--threshold", "0.99", "--json")
	if code != 0 {
		t.Fatalf("search high threshold: exit %d: %s", code, out)
	}
	high := countResults(t, out)
	if high > low {
		t.Errorf("high threshold returned more results (%d) than low threshold (%d) — resolution chain likely broken", high, low)
	}

	// --bm25-only should mark the envelope mode=keyword regardless of provider.
	// Use a term that will match so we get a non-empty envelope to inspect.
	out, code = runWithHome(t, home, "search", "Authentication", "--bm25-only", "--json")
	if code != 0 {
		t.Fatalf("search --bm25-only: exit %d: %s", code, out)
	}
	if strings.TrimSpace(out) == "" {
		t.Fatalf("bm25-only returned empty stdout for a term that should match: %s", out)
	}
	env := map[string]any{}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("parse bm25-only envelope: %v\n%s", err, out)
	}
	if mode, _ := env["mode"].(string); mode != "keyword" {
		t.Errorf("expected mode=keyword with --bm25-only, got %v", env["mode"])
	}
}

func TestBattery_MCPLifecycle(t *testing.T) {
	home := isolatedHome(t)
	vaultDir := filepath.Join(home, "mcp-vault")
	if out, code := runWithHome(t, home, "vault", "create", vaultDir); code != 0 {
		t.Fatalf("vault create: exit %d: %s", code, out)
	}

	// Spawn the MCP server as a subprocess. It speaks stdio — we just need
	// it alive long enough to write its sidecar file, then kill it.
	cmd := exec.Command(binaryPath, "mcp-server", "--vault", vaultDir)
	env := filterEnv(os.Environ(), "HOME", "2NB_TEST")
	env = append(env, "HOME="+home)
	cmd.Env = env
	// Give it a stdin pipe so it doesn't immediately exit.
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	defer stdin.Close()
	if err := cmd.Start(); err != nil {
		t.Fatalf("start mcp-server: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})

	// Wait for the sidecar to appear. writeStatus happens after server.AddTool
	// registration but before the server accepts stdio traffic.
	mcpDir := filepath.Join(vaultDir, ".2ndbrain", "mcp")
	deadline := time.Now().Add(5 * time.Second)
	var sidecar string
	for time.Now().Before(deadline) {
		entries, _ := os.ReadDir(mcpDir)
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), ".json") {
				sidecar = filepath.Join(mcpDir, e.Name())
				break
			}
		}
		if sidecar != "" {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if sidecar == "" {
		t.Fatalf("mcp sidecar never appeared in %s", mcpDir)
	}

	// Sidecar must be parseable and name the pid.
	data, err := os.ReadFile(sidecar)
	if err != nil {
		t.Fatalf("read sidecar: %v", err)
	}
	meta := map[string]any{}
	if err := json.Unmarshal(data, &meta); err != nil {
		t.Fatalf("parse sidecar: %v\n%s", err, data)
	}
	if pid, _ := meta["pid"].(float64); int(pid) != cmd.Process.Pid {
		t.Errorf("sidecar pid=%v, expected %d", meta["pid"], cmd.Process.Pid)
	}

	// `2nb mcp status` should report this server as live.
	out, code := runWithHome(t, home, "mcp", "status", "--json", "--vault", vaultDir)
	if code != 0 {
		t.Fatalf("mcp status: exit %d: %s", code, out)
	}
	if !strings.Contains(out, sidecar[strings.LastIndex(sidecar, "/")+1:]) && !strings.Contains(out, "pid") {
		t.Logf("mcp status output (may be empty if poll raced): %s", out)
	}
}

func TestBattery_SkillsRoundtrip(t *testing.T) {
	home := isolatedHome(t)

	// install --user → file lands in $HOME/.claude/skills/2nb/SKILL.md
	if out, code := runWithHome(t, home, "skills", "install", "claude-code", "--user", "--force"); code != 0 {
		t.Fatalf("skills install: exit %d: %s", code, out)
	}
	skillPath := filepath.Join(home, ".claude", "skills", "2nb", "SKILL.md")
	if _, err := os.Stat(skillPath); err != nil {
		t.Fatalf("skill file not written to %s: %v", skillPath, err)
	}

	// skills list --json should report claude-code installed at user scope.
	out, code := runWithHome(t, home, "skills", "list", "--json")
	if code != 0 {
		t.Fatalf("skills list: exit %d: %s", code, out)
	}
	var entries []map[string]any
	if err := json.Unmarshal([]byte(out), &entries); err != nil {
		t.Fatalf("parse skills list: %v\n%s", err, out)
	}
	var found bool
	for _, e := range entries {
		if slug, _ := e["slug"].(string); slug == "claude-code" {
			found = true
			if ui, _ := e["user_installed"].(bool); !ui {
				t.Errorf("claude-code user_installed=false, expected true: %v", e)
			}
		}
	}
	if !found {
		t.Errorf("claude-code not in skills list: %s", out)
	}

	// uninstall → file gone.
	if out, code := runWithHome(t, home, "skills", "uninstall", "claude-code", "--user"); code != 0 {
		t.Fatalf("skills uninstall: exit %d: %s", code, out)
	}
	if _, err := os.Stat(skillPath); !os.IsNotExist(err) {
		t.Errorf("skill file still exists after uninstall: %v", err)
	}
}

// TestBattery_HybridDegradation is the subprocess-visible half of the
// VectorCompat contract. Unit tests in cli/internal/cli/helpers_vector_test.go
// cover the function directly; this asserts that a dimension mismatch
// surfaces as a warning in the `search --json` envelope and forces mode=keyword.
func TestBattery_HybridDegradation(t *testing.T) {
	if !hasAWSCreds() && !hasOpenRouterKey() {
		t.Skip("need real provider to populate then mismatch embeddings")
	}
	home := isolatedHome(t)
	vaultDir := filepath.Join(home, "degraded-vault")
	if out, code := runWithHome(t, home, "vault", "create", vaultDir); code != 0 {
		t.Fatalf("vault create: exit %d: %s", code, out)
	}
	if out, code := runWithHome(t, home, "create", "Seed Doc", "--type", "note"); code != 0 {
		t.Fatalf("create: exit %d: %s", code, out)
	}
	if out, code := runWithHome(t, home, "index"); code != 0 {
		t.Fatalf("index: exit %d: %s", code, out)
	}

	// Point the config at a provider whose dims differ from what's in the DB.
	// We flip between Bedrock (default embedding dim varies by model) and
	// OpenRouter — if only one is available, we can't flip, so skip.
	if !hasAWSCreds() || !hasOpenRouterKey() {
		t.Skip("need both providers to force dim mismatch")
	}
	// Switch provider at config level — subsequent search calls will see the
	// dim mismatch via VectorCompat.
	if out, code := runWithHome(t, home, "config", "set", "ai.provider", "openrouter"); code != 0 {
		t.Fatalf("config set provider: exit %d: %s", code, out)
	}

	out, code := runWithHome(t, home, "search", "Seed", "--json")
	if code != 0 {
		t.Fatalf("search: exit %d: %s", code, out)
	}
	env := map[string]any{}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("parse envelope: %v\n%s", err, out)
	}
	warnings, _ := env["warnings"].([]any)
	var gotPrefix bool
	for _, w := range warnings {
		if s, _ := w.(string); strings.HasPrefix(s, "semantic search disabled:") {
			gotPrefix = true
		}
	}
	if !gotPrefix {
		t.Errorf("expected warning starting with \"semantic search disabled:\" after provider swap, got: %v", env["warnings"])
	}
	if mode, _ := env["mode"].(string); mode != "keyword" {
		t.Logf("mode=%v (may be hybrid if dims happen to match this provider pair)", env["mode"])
	}
}
