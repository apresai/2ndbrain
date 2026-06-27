package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/apresai/2ndbrain/internal/vault"
)

// `2nb setup --json` returns an array of per-client results; --client claude-code
// installs the skill (~/.claude/skills) and the MCP server (~/.claude.json).
func TestContract_Setup_ClaudeCode(t *testing.T) {
	_, root := newContractVault(t)

	out, err := runCLIArgs(t, root, "setup", "--client", "claude-code", "--json", "--porcelain")
	if err != nil {
		t.Fatalf("setup: %v\n%s", err, out)
	}
	var results []struct {
		Client        string `json:"client"`
		SkillPath     string `json:"skill_path"`
		MCPConfigPath string `json:"mcp_config_path"`
		Configured    bool   `json:"configured"`
	}
	if err := json.Unmarshal(out, &results); err != nil {
		t.Fatalf("setup --json is not an array: %v\n%s", err, out)
	}
	if len(results) != 1 || results[0].Client != "claude-code" {
		t.Fatalf("want one claude-code result, got %+v", results)
	}
	if !results[0].Configured {
		t.Errorf("claude-code should be configured: %+v", results[0])
	}

	home := os.Getenv("HOME")
	if _, err := os.Stat(filepath.Join(home, ".claude", "skills", "2nb", "SKILL.md")); err != nil {
		t.Errorf("claude-code skill not installed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude.json")); err != nil {
		t.Errorf(".claude.json not written: %v", err)
	}
}

// `setup` at project scope from the source tree must also skip a mirror slug's
// skill (here warp) while still configuring its MCP server. Single mirror client
// keeps codex out of the test (no real `codex` exec).
func TestContract_Setup_SkipsRepoMirrorSkill(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")

	root := t.TempDir()
	v, err := vault.Init(root)
	if err != nil {
		t.Fatalf("init vault: %v", err)
	}
	t.Cleanup(func() { v.Close() })
	marker := filepath.Join(root, "cli", "internal", "skills", "content", "2ndbrain-skill.md")
	if err := os.MkdirAll(filepath.Dir(marker), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(marker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := runCLIArgs(t, root, "setup", "--client", "warp", "--scope", "project")
	if err != nil {
		t.Fatalf("setup: %v\n%s", err, out)
	}
	// Skill mirror skipped...
	if _, err := os.Stat(filepath.Join(root, ".warp", "skills", "2nb", "SKILL.md")); err == nil {
		t.Error("warp skill mirror must be skipped at project scope in the source tree")
	}
	// ...but the MCP server (not a mirror) is still configured.
	if _, err := os.Stat(filepath.Join(root, ".warp", ".mcp.json")); err != nil {
		t.Errorf("warp MCP project config should still be written: %v", err)
	}
}

// A project-scope `skills install --all` run from the 2ndbrain source tree must
// SKIP the committed mirror slugs (.agents/.warp/.claude) so it can't stamp them,
// while still installing a non-mirror agent.
func TestContract_SkillsInstallAll_SkipsRepoMirrors(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")

	root := t.TempDir()
	v, err := vault.Init(root)
	if err != nil {
		t.Fatalf("init vault: %v", err)
	}
	t.Cleanup(func() { v.Close() })

	// Make the vault root look like the 2ndbrain source tree (the sentinel
	// IsSourceRepoRoot detects).
	marker := filepath.Join(root, "cli", "internal", "skills", "content", "2ndbrain-skill.md")
	if err := os.MkdirAll(filepath.Dir(marker), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(marker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Clean up the local cobra bool flags this test sets (runCLIArgs resets
	// package vars, not skillsInstallCmd's own flags).
	t.Cleanup(func() {
		_ = skillsInstallCmd.Flags().Set("all", "false")
		_ = skillsInstallCmd.Flags().Set("force", "false")
		_ = skillsInstallCmd.Flags().Set("user", "false")
	})

	out, err := runCLIArgs(t, root, "skills", "install", "--all")
	if err != nil {
		t.Fatalf("skills install --all: %v\n%s", err, out)
	}

	for _, mirror := range []string{".agents", ".warp", ".claude"} {
		if _, err := os.Stat(filepath.Join(root, mirror, "skills", "2nb", "SKILL.md")); err == nil {
			t.Errorf("%s mirror must be skipped in the source tree (run `make sync-skills`)", mirror)
		}
	}
	// A non-mirror agent (cursor) is still installed.
	if _, err := os.Stat(filepath.Join(root, ".cursor", "skills", "2nb", "SKILL.md")); err != nil {
		t.Errorf("non-mirror agent should still install at project scope: %v", err)
	}
}
