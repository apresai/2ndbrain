package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/apresai/2ndbrain/internal/instructions"
)

// TestInstructionsCmd_Roundtrip drives install -> configured --all -> uninstall
// against an isolated HOME (newContractVault sets HOME to a temp dir), so the
// managed block lands in a throwaway ~/.claude/CLAUDE.md.
func TestInstructionsCmd_Roundtrip(t *testing.T) {
	_, root := newContractVault(t)
	home, _ := os.UserHomeDir()
	memPath := filepath.Join(home, ".claude", "CLAUDE.md")

	// install
	out, err := runCLIArgs(t, root, "instructions", "install", "--client", "claude-code", "--json")
	if err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}
	var res instructions.Result
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatalf("install JSON: %v\n%s", err, out)
	}
	if !res.Changed || !res.Installed {
		t.Fatalf("install result: %+v", res)
	}
	if _, statErr := os.Stat(memPath); statErr != nil {
		t.Fatalf("memory file not written: %v", statErr)
	}

	// configured --all reports claude-code + claude-desktop (shared file,
	// installed+current) and codex (its own file, NOT installed by the
	// claude-code install: per-client isolation).
	out, err = runCLIArgs(t, root, "instructions", "configured", "--all", "--json")
	if err != nil {
		t.Fatalf("configured: %v\n%s", err, out)
	}
	var statuses []instructions.Status
	if err := json.Unmarshal(out, &statuses); err != nil {
		t.Fatalf("configured JSON: %v\n%s", err, out)
	}
	if len(statuses) != 3 {
		t.Fatalf("want 3 clients (claude-code, claude-desktop, codex), got %d: %+v", len(statuses), statuses)
	}
	for _, st := range statuses {
		switch st.Client {
		case "codex":
			if !strings.HasSuffix(st.Path, filepath.Join(".codex", "AGENTS.md")) {
				t.Errorf("codex file path = %q, want ~/.codex/AGENTS.md", st.Path)
			}
			if st.Installed {
				t.Errorf("claude-code install must not mark codex installed: %+v", st)
			}
		default:
			if !st.Installed || !st.UpToDate || st.Modified {
				t.Errorf("%s status: %+v", st.Client, st)
			}
		}
	}

	// uninstall removes the block
	out, err = runCLIArgs(t, root, "instructions", "uninstall", "--client", "claude-code", "--json")
	if err != nil {
		t.Fatalf("uninstall: %v\n%s", err, out)
	}
	var un instructions.Result
	if err := json.Unmarshal(out, &un); err != nil {
		t.Fatalf("uninstall JSON: %v\n%s", err, out)
	}
	if !un.Changed {
		t.Errorf("uninstall should report Changed")
	}
	data, _ := os.ReadFile(memPath)
	if strings.Contains(string(data), "BEGIN 2nb managed instructions") {
		t.Errorf("block should be gone after uninstall:\n%s", data)
	}
}

// TestInstructionsCmd_UnsupportedClient verifies a client with no known memory
// file errors with a helpful message (ExitValidation), not a silent success.
func TestInstructionsCmd_UnsupportedClient(t *testing.T) {
	_, root := newContractVault(t)
	_, err := runCLIArgs(t, root, "instructions", "install", "--client", "warp", "--json")
	if code := exitCode(t, err); code != ExitValidation {
		t.Fatalf("unsupported client: want ExitValidation(%d), got %d (err=%v)", ExitValidation, code, err)
	}
}

// TestInstructionsCmd_CodexPreservesUserContent verifies the codex install
// lands in ~/.codex/AGENTS.md, preserves existing user content, and stays
// isolated from the claude-code file.
func TestInstructionsCmd_CodexPreservesUserContent(t *testing.T) {
	_, root := newContractVault(t)
	home, _ := os.UserHomeDir()
	codexPath := filepath.Join(home, ".codex", "AGENTS.md")
	claudePath := filepath.Join(home, ".claude", "CLAUDE.md")

	// Pre-existing user content in the codex memory file.
	if err := os.MkdirAll(filepath.Dir(codexPath), 0o755); err != nil {
		t.Fatal(err)
	}
	userContent := "# My global Codex rules\n\nAlways run tests.\n"
	if err := os.WriteFile(codexPath, []byte(userContent), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := runCLIArgs(t, root, "instructions", "install", "--client", "codex", "--json")
	if err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}
	data, err := os.ReadFile(codexPath)
	if err != nil {
		t.Fatalf("read codex memory file: %v", err)
	}
	body := string(data)
	if !strings.Contains(body, "My global Codex rules") {
		t.Errorf("user content was not preserved:\n%s", body)
	}
	if !strings.Contains(body, "BEGIN 2nb managed instructions") {
		t.Errorf("managed block missing:\n%s", body)
	}
	// Per-client isolation: the codex install must not create the claude file.
	if _, statErr := os.Stat(claudePath); !os.IsNotExist(statErr) {
		t.Errorf("codex install must not touch ~/.claude/CLAUDE.md")
	}
}

// TestSetup_WritesInstructions verifies `2nb setup` installs the global
// instructions block and reports it in the per-client result.
func TestSetup_WritesInstructions(t *testing.T) {
	_, root := newContractVault(t)
	out, err := runCLIArgs(t, root, "setup", "--client", "claude-code", "--json")
	if err != nil {
		t.Fatalf("setup: %v\n%s", err, out)
	}
	var results []SetupClientResult
	if err := json.Unmarshal(out, &results); err != nil {
		t.Fatalf("setup JSON: %v\n%s", err, out)
	}
	if len(results) != 1 {
		t.Fatalf("want 1 client result, got %d", len(results))
	}
	r := results[0]
	if r.InstructionsPath == "" {
		t.Errorf("setup should report an instructions_file_path: %+v", r)
	}
	if !r.InstructionsWritten {
		t.Errorf("first setup should write the instructions block: %+v", r)
	}
	// the file exists and carries the block
	data, err := os.ReadFile(r.InstructionsPath)
	if err != nil || !strings.Contains(string(data), "2ndbrain") {
		t.Errorf("instructions block not written to %s: err=%v", r.InstructionsPath, err)
	}
}
