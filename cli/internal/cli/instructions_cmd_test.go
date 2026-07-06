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

	// configured --all reports both claude-code and claude-desktop, installed+current
	out, err = runCLIArgs(t, root, "instructions", "configured", "--all", "--json")
	if err != nil {
		t.Fatalf("configured: %v\n%s", err, out)
	}
	var statuses []instructions.Status
	if err := json.Unmarshal(out, &statuses); err != nil {
		t.Fatalf("configured JSON: %v\n%s", err, out)
	}
	if len(statuses) != 2 {
		t.Fatalf("want 2 clients (claude-code, claude-desktop), got %d: %+v", len(statuses), statuses)
	}
	for _, st := range statuses {
		if !st.Installed || !st.UpToDate || st.Modified {
			t.Errorf("%s status: %+v", st.Client, st)
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
