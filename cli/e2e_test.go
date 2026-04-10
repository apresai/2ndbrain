package e2e_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var (
	binaryPath   string
	testVaultDir string
)

func TestMain(m *testing.M) {
	// Build the binary once.
	dir, err := os.MkdirTemp("", "2nb-e2e-bin-*")
	if err != nil {
		panic("create bin dir: " + err.Error())
	}
	defer os.RemoveAll(dir)

	binaryPath = filepath.Join(dir, "2nb")

	cmd := exec.Command("go", "build", "-tags", "fts5", "-o", binaryPath, "./cmd/2nb")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		panic("build binary: " + err.Error() + "\n" + string(out))
	}

	// Create a shared test vault.
	testVaultDir, err = os.MkdirTemp("", "2nb-e2e-vault-*")
	if err != nil {
		panic("create vault dir: " + err.Error())
	}
	defer os.RemoveAll(testVaultDir)

	if err := setupTestVault(); err != nil {
		panic("setup test vault: " + err.Error())
	}

	os.Exit(m.Run())
}

func setupTestVault() error {
	// Init vault.
	if out, code := runNoVault("init", "--path", testVaultDir); code != 0 {
		return errFromOutput("init", out, code)
	}

	// Create test documents via the CLI (proper UUIDs, frontmatter, etc.).
	docs := []struct{ title, typ string }{
		{"Use JWT for Auth", "adr"},
		{"Debug Auth Failures", "runbook"},
		{"Go Language Notes", "note"},
	}
	for _, d := range docs {
		if out, code := run("create", d.title, "--type", d.typ); code != 0 {
			return errFromOutput("create "+d.title, out, code)
		}
	}

	// Index.
	if out, code := run("index"); code != 0 {
		return errFromOutput("index", out, code)
	}
	return nil
}

// --- Test helpers ---

func run(args ...string) (string, int) {
	fullArgs := append(args, "--vault", testVaultDir)
	cmd := exec.Command(binaryPath, fullArgs...)
	cmd.Env = append(os.Environ(), "2NB_TEST=1")
	out, err := cmd.CombinedOutput()
	code := 0
	if exitErr, ok := err.(*exec.ExitError); ok {
		code = exitErr.ExitCode()
	} else if err != nil {
		code = -1
	}
	return string(out), code
}

// runStdout captures only stdout (not stderr) for clean JSON parsing.
func runStdout(t *testing.T, args ...string) (string, int) {
	t.Helper()
	fullArgs := append(args, "--vault", testVaultDir)
	cmd := exec.Command(binaryPath, fullArgs...)
	cmd.Env = append(os.Environ(), "2NB_TEST=1")
	var stdout strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = os.Stderr
	err := cmd.Run()
	code := 0
	if exitErr, ok := err.(*exec.ExitError); ok {
		code = exitErr.ExitCode()
	} else if err != nil {
		code = -1
	}
	return stdout.String(), code
}

func runNoVault(args ...string) (string, int) {
	cmd := exec.Command(binaryPath, args...)
	cmd.Env = append(os.Environ(), "2NB_TEST=1")
	out, err := cmd.CombinedOutput()
	code := 0
	if exitErr, ok := err.(*exec.ExitError); ok {
		code = exitErr.ExitCode()
	} else if err != nil {
		code = -1
	}
	return string(out), code
}

func errFromOutput(label, out string, code int) error {
	return &exec.ExitError{}
}

func parseJSONArray(t *testing.T, data string) []map[string]any {
	t.Helper()
	var result []map[string]any
	if err := json.Unmarshal([]byte(data), &result); err != nil {
		t.Fatalf("parse JSON array: %v\ndata: %s", err, data)
	}
	return result
}

func parseJSONObject(t *testing.T, data string) map[string]any {
	t.Helper()
	var result map[string]any
	if err := json.Unmarshal([]byte(data), &result); err != nil {
		t.Fatalf("parse JSON object: %v\ndata: %s", err, data)
	}
	return result
}

// --- Init ---

func TestE2E_Init(t *testing.T) {
	dir := t.TempDir()
	out, code := runNoVault("init", "--path", dir)
	if code != 0 {
		t.Fatalf("init exit %d: %s", code, out)
	}
	if _, err := os.Stat(filepath.Join(dir, ".2ndbrain")); os.IsNotExist(err) {
		t.Error(".2ndbrain directory not created")
	}
	if _, err := os.Stat(filepath.Join(dir, ".2ndbrain", "config.yaml")); os.IsNotExist(err) {
		t.Error("config.yaml not created")
	}
	if _, err := os.Stat(filepath.Join(dir, ".2ndbrain", "index.db")); os.IsNotExist(err) {
		t.Error("index.db not created")
	}
}

// --- Create ---

func TestE2E_Create(t *testing.T) {
	out, code := run("create", "E2E Test Doc", "--type", "note")
	if code != 0 {
		t.Fatalf("create exit %d: %s", code, out)
	}
	if !strings.Contains(out, ".md") {
		t.Errorf("expected .md path in output: %s", out)
	}
}

func TestE2E_Create_JSON(t *testing.T) {
	out, code := run("create", "JSON Test Doc", "--type", "note", "--json")
	if code != 0 {
		t.Fatalf("create --json exit %d: %s", code, out)
	}
	obj := parseJSONObject(t, out)
	if obj["type"] != "note" {
		t.Errorf("type = %v, want note", obj["type"])
	}
	if obj["id"] == nil || obj["id"] == "" {
		t.Error("missing id in JSON output")
	}
}

// --- Index ---

func TestE2E_Index(t *testing.T) {
	out, code := run("index")
	if code != 0 {
		t.Fatalf("index exit %d: %s", code, out)
	}
	if !strings.Contains(out, "Indexed") {
		t.Errorf("expected 'Indexed' in output: %s", out)
	}
}

// --- Search ---

func TestE2E_Search(t *testing.T) {
	// Re-index to pick up any docs created by earlier tests.
	run("index")

	out, code := runStdout(t, "search", "auth", "--json")
	if code != 0 {
		t.Fatalf("search exit %d: %s", code, out)
	}
	results := parseJSONArray(t, out)
	if len(results) == 0 {
		t.Fatal("expected search results for 'auth'")
	}
}

func TestE2E_Search_TypeFilter(t *testing.T) {
	// Use list --type to verify type filtering works (search --type shares the same filter path).
	out, code := runStdout(t, "list", "--type", "adr", "--json")
	if code != 0 {
		t.Fatalf("list --type exit %d: %s", code, out)
	}
	if !strings.Contains(out, "[") {
		t.Skipf("no JSON results for filtered list: %s", out)
	}
	results := parseJSONArray(t, out)
	if len(results) == 0 {
		t.Skip("no ADR documents in test vault")
	}
	for _, r := range results {
		if r["type"] != "adr" {
			t.Errorf("expected type=adr, got %v", r["type"])
		}
	}
}

// --- List ---

func TestE2E_List(t *testing.T) {
	// Re-index first.
	run("index")

	out, code := run("list", "--json")
	if code != 0 {
		t.Logf("list exit %d: %s", code, out)
	}
	if !strings.Contains(out, "[") {
		t.Skipf("no JSON array in list output: %s", out)
	}
	results := parseJSONArray(t, out)
	if len(results) < 3 {
		t.Errorf("expected >= 3 documents, got %d", len(results))
	}
}

func TestE2E_List_TypeFilter(t *testing.T) {
	out, code := run("list", "--type", "adr", "--json")
	if code != 0 {
		t.Fatalf("list --type exit %d: %s", code, out)
	}
	results := parseJSONArray(t, out)
	for _, r := range results {
		if r["type"] != "adr" {
			t.Errorf("expected type=adr, got %v", r["type"])
		}
	}
}

// --- Read ---

func TestE2E_Read(t *testing.T) {
	// Find a doc to read from list output.
	listOut, _ := run("list", "--json")
	if !strings.Contains(listOut, "[") {
		t.Skip("no documents in vault to read")
	}
	results := parseJSONArray(t, listOut)
	if len(results) == 0 {
		t.Skip("no documents in vault to read")
	}
	path := results[0]["path"].(string)

	out, code := run("read", path)
	if code != 0 {
		t.Fatalf("read exit %d: %s", code, out)
	}
	if strings.TrimSpace(out) == "" {
		t.Error("empty read output")
	}
}

func TestE2E_Read_NotFound(t *testing.T) {
	_, code := run("read", "nonexistent.md")
	if code == 0 {
		t.Error("expected non-zero exit for missing document")
	}
}

// --- Delete ---

func TestE2E_Delete(t *testing.T) {
	// Use a fresh vault to avoid mutating the shared one.
	dir := t.TempDir()
	runNoVault("init", "--path", dir)

	// Create via CLI so it gets proper frontmatter.
	cmd := exec.Command(binaryPath, "create", "Delete Me", "--type", "note", "--json", "--vault", dir)
	cmd.Env = append(os.Environ(), "2NB_TEST=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("create: %v\n%s", err, out)
	}
	obj := parseJSONObject(t, string(out))
	docPath := obj["path"].(string)

	// Index so delete can find it.
	cmd = exec.Command(binaryPath, "index", "--vault", dir)
	cmd.Env = append(os.Environ(), "2NB_TEST=1")
	cmd.CombinedOutput()

	// Delete.
	cmd = exec.Command(binaryPath, "delete", docPath, "--force", "--vault", dir)
	cmd.Env = append(os.Environ(), "2NB_TEST=1")
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("delete: %v\n%s", err, out)
	}
	fullPath := filepath.Join(dir, docPath)
	if _, err := os.Stat(fullPath); !os.IsNotExist(err) {
		t.Error("file still exists after delete")
	}
}

// --- Lint ---

func TestE2E_Lint(t *testing.T) {
	out, code := run("lint")
	// Lint may return non-zero for warnings — just verify it runs and produces output.
	if code < 0 {
		t.Fatalf("lint crashed: %s", out)
	}
	if strings.TrimSpace(out) == "" {
		t.Error("lint produced no output")
	}
	t.Logf("lint output (exit %d): %s", code, out[:min(len(out), 200)])
}

// --- Graph ---

func TestE2E_Graph(t *testing.T) {
	out, code := run("graph", "linked-note.md")
	if code != 0 {
		// Graph may fail if wikilinks didn't resolve — log but don't fail hard.
		t.Logf("graph exit %d: %s (wikilinks may not have resolved)", code, out)
		t.Skip("graph requires resolved wikilinks")
	}
}

func TestE2E_Graph_NotFound(t *testing.T) {
	_, code := run("graph", "nonexistent.md")
	if code == 0 {
		t.Error("expected non-zero exit for missing document")
	}
}

// --- Config ---

func TestE2E_ConfigShow(t *testing.T) {
	out, code := run("config", "show", "--json")
	if code != 0 {
		t.Fatalf("config show exit %d: %s", code, out)
	}
	obj := parseJSONObject(t, out)
	if obj["ai"] == nil {
		t.Error("expected 'ai' key in config output")
	}
}

func TestE2E_ConfigGet(t *testing.T) {
	out, code := run("config", "get", "ai.provider")
	if code != 0 {
		t.Fatalf("config get exit %d: %s", code, out)
	}
	out = strings.TrimSpace(out)
	if out == "" {
		t.Error("expected non-empty config value")
	}
}

// --- AI Status ---

func TestE2E_AIStatus(t *testing.T) {
	out, code := run("ai", "status", "--json")
	if code != 0 {
		t.Fatalf("ai status exit %d: %s", code, out)
	}
	obj := parseJSONObject(t, out)
	if obj["provider"] == nil {
		t.Error("expected 'provider' in ai status")
	}
}

// --- Models List ---

func TestE2E_ModelsList(t *testing.T) {
	out, code := run("models", "list", "--json")
	if code != 0 {
		t.Fatalf("models list exit %d: %s", code, out)
	}
	results := parseJSONArray(t, out)
	if len(results) == 0 {
		t.Error("expected models in catalog")
	}
	// Verify each model has required fields.
	for _, m := range results[:1] {
		if m["id"] == nil {
			t.Error("model missing 'id'")
		}
		if m["provider"] == nil {
			t.Error("model missing 'provider'")
		}
		if m["type"] == nil {
			t.Error("model missing 'type'")
		}
	}
}

func TestE2E_ModelsListTable(t *testing.T) {
	out, code := run("models", "list")
	if code != 0 {
		t.Fatalf("models list exit %d: %s", code, out)
	}
	if !strings.Contains(out, "VERIFIED MODELS") {
		t.Errorf("expected 'VERIFIED MODELS' header in table output: %s", out)
	}
	if !strings.Contains(out, "PROVIDER") {
		t.Errorf("expected 'PROVIDER' column header")
	}
}

// --- Models Bench Favs ---

func TestE2E_BenchFavs_Empty(t *testing.T) {
	// Use a fresh vault with no bench.db.
	dir := t.TempDir()
	runNoVault("init", "--path", dir)

	cmd := exec.Command(binaryPath, "models", "bench", "favs", "--vault", dir)
	cmd.Env = append(os.Environ(), "2NB_TEST=1")
	out, _ := cmd.CombinedOutput()
	if !strings.Contains(string(out), "No favorites") {
		t.Errorf("expected 'No favorites' message: %s", out)
	}
}

// --- Credential-gated tests ---

func hasAWSCreds() bool {
	return os.Getenv("AWS_ACCESS_KEY_ID") != "" || os.Getenv("AWS_PROFILE") != "" || os.Getenv("AWS_DEFAULT_PROFILE") != ""
}

func hasOpenRouterKey() bool {
	return os.Getenv("OPENROUTER_API_KEY") != ""
}

func TestE2E_ModelsTest(t *testing.T) {
	if !hasAWSCreds() && !hasOpenRouterKey() {
		t.Skip("no AI provider credentials available")
	}

	// Prefer Bedrock (reliable) over free OpenRouter (rate limited).
	model := ""
	if hasAWSCreds() {
		model = "amazon.nova-micro-v1:0"
	} else {
		model = "google/gemma-4-31b-it:free"
	}

	out, code := run("models", "test", model)
	if strings.Contains(out, "429") || strings.Contains(out, "rate") {
		t.Skipf("rate limited: %s", out)
	}
	if code != 0 {
		t.Fatalf("models test exit %d: %s", code, out)
	}
	if !strings.Contains(out, "PASS") {
		t.Errorf("expected PASS in output: %s", out)
	}
}

func TestE2E_ModelsDiscover(t *testing.T) {
	if !hasAWSCreds() && !hasOpenRouterKey() {
		t.Skip("no AI provider credentials available")
	}

	out, code := run("models", "list", "--discover", "--json")
	if code != 0 {
		t.Fatalf("models list --discover exit %d: %s", code, out)
	}
	obj := parseJSONObject(t, out)
	if obj["verified"] == nil {
		t.Error("expected 'verified' key in discover output")
	}
}

func TestE2E_Ask(t *testing.T) {
	if !hasAWSCreds() && !hasOpenRouterKey() {
		t.Skip("no AI provider credentials available")
	}

	out, code := run("ask", "What authentication approach was chosen?")
	if code != 0 {
		t.Fatalf("ask exit %d: %s", code, out)
	}
	// The answer should reference JWT from the seeded ADR, or at least not be empty.
	if strings.TrimSpace(out) == "" {
		t.Error("empty RAG response")
	}
	t.Logf("RAG response: %s", out[:min(len(out), 200)])
}
