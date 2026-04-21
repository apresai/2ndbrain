package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUpdateZshrc_FreshInsert(t *testing.T) {
	dir := t.TempDir()
	zshrc := filepath.Join(dir, ".zshrc")
	completionDir := filepath.Join(dir, ".zsh", "completions")

	added, err := updateZshrc(zshrc, completionDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !added {
		t.Fatal("expected added=true for fresh .zshrc")
	}

	content, err := os.ReadFile(zshrc)
	if err != nil {
		t.Fatalf("read zshrc: %v", err)
	}
	s := string(content)
	if !strings.Contains(s, zshrcBlockBegin) {
		t.Error("missing BEGIN marker")
	}
	if !strings.Contains(s, zshrcBlockEnd) {
		t.Error("missing END marker")
	}
	if !strings.Contains(s, completionDir) {
		t.Errorf("expected completionDir %q in block", completionDir)
	}
	if !strings.Contains(s, "compinit") {
		t.Error("missing compinit")
	}
}

func TestUpdateZshrc_Idempotent(t *testing.T) {
	dir := t.TempDir()
	zshrc := filepath.Join(dir, ".zshrc")
	completionDir := filepath.Join(dir, ".zsh", "completions")

	// First install
	if _, err := updateZshrc(zshrc, completionDir); err != nil {
		t.Fatalf("first run: %v", err)
	}

	// Second install
	added, err := updateZshrc(zshrc, completionDir)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if added {
		t.Fatal("expected added=false on second run")
	}

	content, err := os.ReadFile(zshrc)
	if err != nil {
		t.Fatalf("read zshrc: %v", err)
	}
	count := strings.Count(string(content), zshrcBlockBegin)
	if count != 1 {
		t.Errorf("expected 1 managed block, got %d", count)
	}
}

func TestUpdateZshrc_CustomDir(t *testing.T) {
	dir := t.TempDir()
	zshrc := filepath.Join(dir, ".zshrc")
	customDir := filepath.Join(dir, "mycompletions")

	added, err := updateZshrc(zshrc, customDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !added {
		t.Fatal("expected added=true")
	}

	content, err := os.ReadFile(zshrc)
	if err != nil {
		t.Fatalf("read zshrc: %v", err)
	}
	if !strings.Contains(string(content), customDir) {
		t.Errorf("expected custom dir %q in managed block", customDir)
	}
}

func TestUpdateZshrc_PreservesExistingContent(t *testing.T) {
	dir := t.TempDir()
	zshrc := filepath.Join(dir, ".zshrc")
	existing := "export PATH=$HOME/bin:$PATH\nalias ll='ls -la'\n"
	if err := os.WriteFile(zshrc, []byte(existing), 0o644); err != nil {
		t.Fatalf("write existing zshrc: %v", err)
	}

	completionDir := filepath.Join(dir, ".zsh", "completions")
	added, err := updateZshrc(zshrc, completionDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !added {
		t.Fatal("expected added=true")
	}

	content, err := os.ReadFile(zshrc)
	if err != nil {
		t.Fatalf("read zshrc: %v", err)
	}
	s := string(content)
	if !strings.Contains(s, "export PATH=$HOME/bin:$PATH") {
		t.Error("existing PATH line was removed")
	}
	if !strings.Contains(s, "alias ll='ls -la'") {
		t.Error("existing alias was removed")
	}
	if !strings.Contains(s, zshrcBlockBegin) {
		t.Error("missing managed block")
	}
	// Existing content should appear before the managed block
	existingIdx := strings.Index(s, "export PATH")
	blockIdx := strings.Index(s, zshrcBlockBegin)
	if existingIdx > blockIdx {
		t.Error("existing content should come before the managed block")
	}
}

func TestBuildZshrcBlock_Contents(t *testing.T) {
	block := buildZshrcBlock("/home/user/.zsh/completions")

	checks := []string{
		zshrcBlockBegin,
		zshrcBlockEnd,
		"fpath=(/home/user/.zsh/completions $fpath)",
		"/opt/homebrew/share/zsh/site-functions",
		"whence compdef",
		"autoload -Uz compinit",
		"compinit -i",
	}
	for _, want := range checks {
		if !strings.Contains(block, want) {
			t.Errorf("block missing %q", want)
		}
	}
}
