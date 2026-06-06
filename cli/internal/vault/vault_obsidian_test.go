package vault

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestOpen_AutoCreatesSidecarForObsidianVault covers the core Obsidian-native
// entry path: a directory that has only .obsidian/ (a real Obsidian vault, no
// 2ndbrain sidecar yet) opens cleanly and gets its .2ndbrain/ sidecar created.
func TestOpen_AutoCreatesSidecarForObsidianVault(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".obsidian"), 0o755); err != nil {
		t.Fatal(err)
	}

	v, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { v.Close() })

	for _, p := range []string{
		".2ndbrain/config.yaml",
		".2ndbrain/schemas.yaml",
		".2ndbrain/index.db",
	} {
		if _, err := os.Stat(filepath.Join(dir, p)); err != nil {
			t.Errorf("expected %s to be created: %v", p, err)
		}
	}

	gi, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil || !strings.Contains(string(gi), ".2ndbrain/") {
		t.Errorf(".gitignore should list .2ndbrain/: err=%v content=%q", err, gi)
	}
}

func TestFindVaultRoot_DetectsObsidianDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".obsidian"), 0o755); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(dir, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := FindVaultRoot(sub); got != dir {
		t.Errorf("FindVaultRoot(%q) = %q, want %q", sub, got, dir)
	}
}

func TestEnsureGitignore(t *testing.T) {
	t.Run("creates when missing", func(t *testing.T) {
		dir := t.TempDir()
		ensureGitignore(dir)
		b, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
		if err != nil || !strings.Contains(string(b), ".2ndbrain/") {
			t.Errorf("expected created gitignore with entry: err=%v %q", err, b)
		}
	})

	t.Run("appends to existing, preserving prior content", func(t *testing.T) {
		dir := t.TempDir()
		gi := filepath.Join(dir, ".gitignore")
		if err := os.WriteFile(gi, []byte("node_modules/\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		ensureGitignore(dir)
		b, _ := os.ReadFile(gi)
		if !strings.Contains(string(b), "node_modules/") || !strings.Contains(string(b), ".2ndbrain/") {
			t.Errorf("expected appended entry preserving existing: %q", b)
		}
	})

	t.Run("idempotent when already present", func(t *testing.T) {
		dir := t.TempDir()
		gi := filepath.Join(dir, ".gitignore")
		if err := os.WriteFile(gi, []byte(".2ndbrain/\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		ensureGitignore(dir)
		b, _ := os.ReadFile(gi)
		if strings.Count(string(b), ".2ndbrain/") != 1 {
			t.Errorf("expected no duplicate entry, got %q", b)
		}
	})
}
