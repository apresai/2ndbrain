package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Phase 7 (daily notes) contract tests. Each builds the exact argv a shell user
// or the GUI sends and asserts the CLI accepts it. The AI provider is pinned to
// "no-provider" so the create-on-demand path's embed step is a no-op.

// expectedDailyName returns today's daily-note filename stem with the default
// YYYY-MM-DD format, so the test stays correct on any calendar day.
func expectedDailyName() string {
	return time.Now().Format("2006-01-02")
}

func TestContract_DailyResolveCreatesAndPrints(t *testing.T) {
	v, root := newContractVault(t)
	v.Config.AI.Provider = "no-provider"
	if err := v.Config.Save(v.DotDir); err != nil {
		t.Fatalf("save config: %v", err)
	}

	stem := expectedDailyName()
	wantRel := stem + ".md"

	// Bare `daily` resolves + creates + prints the path (JSON form).
	out, err := runCLIArgs(t, root, "daily", "--json", "--porcelain")
	if err != nil {
		t.Fatalf("daily: %v\n%s", err, out)
	}
	var res struct {
		Path    string `json:"path"`
		Created bool   `json:"created"`
	}
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatalf("daily JSON: %v\n%s", err, out)
	}
	if res.Path != wantRel {
		t.Fatalf("daily path = %q, want %q", res.Path, wantRel)
	}
	if !res.Created {
		t.Fatalf("first daily call should report created=true, got false")
	}
	if _, err := os.Stat(filepath.Join(root, wantRel)); err != nil {
		t.Fatalf("daily note not written: %v", err)
	}

	// A second call must be idempotent: same path, created=false.
	out2, err := runCLIArgs(t, root, "daily", "--json", "--porcelain")
	if err != nil {
		t.Fatalf("daily (second): %v\n%s", err, out2)
	}
	var res2 struct {
		Path    string `json:"path"`
		Created bool   `json:"created"`
	}
	if err := json.Unmarshal(out2, &res2); err != nil {
		t.Fatalf("daily JSON (second): %v\n%s", err, out2)
	}
	if res2.Path != wantRel {
		t.Fatalf("second daily path = %q, want %q", res2.Path, wantRel)
	}
	if res2.Created {
		t.Fatalf("second daily call should report created=false")
	}
}

func TestContract_DailyAppendThenRead(t *testing.T) {
	v, root := newContractVault(t)
	v.Config.AI.Provider = "no-provider"
	if err := v.Config.Save(v.DotDir); err != nil {
		t.Fatalf("save config: %v", err)
	}

	const marker = "STANDUP_MARKER_42"

	// append creates the note on demand and writes the body.
	if out, err := runCLIArgs(t, root, "daily", "append", "--text", "- "+marker, "--json", "--porcelain"); err != nil {
		t.Fatalf("daily append: %v\n%s", err, out)
	} else if !strings.Contains(string(out), `"operation": "daily-append"`) {
		t.Fatalf("daily append output missing operation:\n%s", out)
	}

	// read must show the appended text.
	out, err := runCLIArgs(t, root, "daily", "read", "--json", "--porcelain")
	if err != nil {
		t.Fatalf("daily read: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), marker) {
		t.Fatalf("daily read output missing appended marker %q:\n%s", marker, out)
	}
}

func TestContract_DailyCustomFolderHonored(t *testing.T) {
	v, root := newContractVault(t)
	v.Config.AI.Provider = "no-provider"
	if err := v.Config.Save(v.DotDir); err != nil {
		t.Fatalf("save config: %v", err)
	}

	// Configure Obsidian's core daily-notes plugin to place notes under a
	// nested folder with a slash-separated format.
	obsDir := filepath.Join(root, ".obsidian")
	if err := os.MkdirAll(obsDir, 0o755); err != nil {
		t.Fatalf("mkdir .obsidian: %v", err)
	}
	cfg := `{"folder":"journal","format":"YYYY/MM/DD"}`
	if err := os.WriteFile(filepath.Join(obsDir, "daily-notes.json"), []byte(cfg), 0o644); err != nil {
		t.Fatalf("write daily-notes.json: %v", err)
	}

	now := time.Now()
	wantRel := filepath.Join("journal", now.Format("2006"), now.Format("01"), now.Format("02")+".md")

	out, err := runCLIArgs(t, root, "daily", "--json", "--porcelain")
	if err != nil {
		t.Fatalf("daily (custom folder): %v\n%s", err, out)
	}
	var res struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatalf("daily JSON: %v\n%s", err, out)
	}
	if res.Path != wantRel {
		t.Fatalf("daily path = %q, want %q", res.Path, wantRel)
	}
	if _, err := os.Stat(filepath.Join(root, wantRel)); err != nil {
		t.Fatalf("daily note not written at custom folder path: %v", err)
	}
}
