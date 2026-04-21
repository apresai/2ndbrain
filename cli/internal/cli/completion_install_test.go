package cli

import (
	"bytes"
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
	if !strings.Contains(s, "if [[ -o interactive ]]; then") {
		t.Error("missing interactive shell guard")
	}
	if !strings.Contains(s, "autoload -Uz compinit") {
		t.Error("missing compinit")
	}
	if strings.Contains(s, "whence compdef") {
		t.Error("block should not have whence compdef guard — compinit must always run")
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

func TestUpdateZshrc_PlacesBlockBeforeEarlyReturn(t *testing.T) {
	dir := t.TempDir()
	zshrc := filepath.Join(dir, ".zshrc")
	existing := strings.Join([]string{
		"# header",
		"export PATH=$HOME/bin:$PATH",
		"if [[ ! -o interactive ]]; then",
		"  return 2>/dev/null || exit 0",
		"fi",
		"autoload -Uz compinit",
		"compinit -i",
		"",
	}, "\n")
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
	blockIdx := strings.Index(s, zshrcBlockBegin)
	// The block should land before the start of the if-guard, not just before
	// the indented return inside it.
	guardIdx := strings.Index(s, "if [[ ! -o interactive ]]")
	if blockIdx == -1 || guardIdx == -1 {
		t.Fatalf("missing block (%d) or guard (%d)", blockIdx, guardIdx)
	}
	if blockIdx > guardIdx {
		t.Fatalf("managed block should be inserted before the if-guard (block at %d, guard at %d)", blockIdx, guardIdx)
	}
}

func TestUpdateZshrc_PlacesBlockBeforeConditionalReturn(t *testing.T) {
	dir := t.TempDir()
	zshrc := filepath.Join(dir, ".zshrc")
	existing := strings.Join([]string{
		"# zshrc",
		"export PATH=$HOME/bin:$PATH",
		`[[ "$TERM_PROGRAM" == "antigravity" ]] && return`,
		"autoload -Uz compinit",
		"compinit -i",
		"",
	}, "\n")
	if err := os.WriteFile(zshrc, []byte(existing), 0o644); err != nil {
		t.Fatalf("write zshrc: %v", err)
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
	blockIdx := strings.Index(s, zshrcBlockBegin)
	guardIdx := strings.Index(s, "&& return")
	if blockIdx == -1 || guardIdx == -1 {
		t.Fatalf("missing block (%d) or guard (%d)", blockIdx, guardIdx)
	}
	if blockIdx > guardIdx {
		t.Fatalf("managed block should be inserted before `&& return` guard (block at %d, guard at %d)", blockIdx, guardIdx)
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
		"if [[ -o interactive ]]; then",
		"fpath=(/home/user/.zsh/completions $fpath)",
		"/opt/homebrew/share/zsh/site-functions",
		"autoload -Uz compinit",
		"compinit -i",
	}
	for _, want := range checks {
		if !strings.Contains(block, want) {
			t.Errorf("block missing %q", want)
		}
	}
}

func TestCompletionDirsFromZshrc_PrioritizesHomePaths(t *testing.T) {
	home := filepath.Join(string(filepath.Separator), "Users", "chad")
	content := strings.Join([]string{
		"fpath=(/opt/homebrew/share/zsh/site-functions $fpath)",
		"fpath=($HOME/.zfunc ~/.zsh/completions $fpath)",
	}, "\n")

	got := completionDirsFromZshrc(content, home)
	if len(got) < 3 {
		t.Fatalf("expected at least 3 completion dirs, got %d: %v", len(got), got)
	}
	if got[0] != filepath.Join(home, ".zfunc") {
		t.Fatalf("expected first home dir to be %q, got %q", filepath.Join(home, ".zfunc"), got[0])
	}
	if got[1] != filepath.Join(home, ".zsh", "completions") {
		t.Fatalf("expected second home dir to be %q, got %q", filepath.Join(home, ".zsh", "completions"), got[1])
	}
	if got[2] != "/opt/homebrew/share/zsh/site-functions" {
		t.Fatalf("expected third dir to be homebrew site-functions, got %q", got[2])
	}
}

func TestFindExecutablesOnPATH(t *testing.T) {
	root := t.TempDir()
	dirA := filepath.Join(root, "a")
	dirB := filepath.Join(root, "b")
	dirC := filepath.Join(root, "c")
	for _, d := range []string{dirA, dirB, dirC} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}

	binA := filepath.Join(dirA, "2nb")
	binB := filepath.Join(dirB, "2nb")
	binC := filepath.Join(dirC, "2nb")
	if err := os.WriteFile(binA, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write %s: %v", binA, err)
	}
	if err := os.WriteFile(binB, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write %s: %v", binB, err)
	}
	// non-executable should be ignored
	if err := os.WriteFile(binC, []byte("#!/bin/sh\n"), 0o644); err != nil {
		t.Fatalf("write %s: %v", binC, err)
	}

	pathEnv := strings.Join([]string{dirA, dirB, dirC}, string(os.PathListSeparator))
	got := findExecutablesOnPATH("2nb", pathEnv)
	if len(got) != 2 {
		t.Fatalf("expected 2 executables, got %d (%v)", len(got), got)
	}
}

func TestWarnIfMultiple2nbOnPath(t *testing.T) {
	root := t.TempDir()
	dirA := filepath.Join(root, "a")
	dirB := filepath.Join(root, "b")
	for _, d := range []string{dirA, dirB} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}
	// Scripts respond to --version so the warning can show version strings.
	if err := os.WriteFile(filepath.Join(dirA, "2nb"), []byte("#!/bin/sh\necho '2nb version 1.0.0'\n"), 0o755); err != nil {
		t.Fatalf("write dirA 2nb: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dirB, "2nb"), []byte("#!/bin/sh\necho '2nb version 2.0.0'\n"), 0o755); err != nil {
		t.Fatalf("write dirB 2nb: %v", err)
	}

	oldPath := os.Getenv("PATH")
	t.Cleanup(func() {
		_ = os.Setenv("PATH", oldPath)
	})
	if err := os.Setenv("PATH", strings.Join([]string{dirA, dirB}, string(os.PathListSeparator))); err != nil {
		t.Fatalf("set PATH: %v", err)
	}

	var buf bytes.Buffer
	warnIfMultiple2nbOnPath(&buf)
	out := buf.String()
	if !strings.Contains(out, "multiple 2nb binaries found on PATH") {
		t.Fatalf("expected warning text, got: %s", out)
	}
	if !strings.Contains(out, dirA) || !strings.Contains(out, dirB) {
		t.Fatalf("expected both binary paths in warning, got: %s", out)
	}
	if !strings.Contains(out, "1.0.0") || !strings.Contains(out, "2.0.0") {
		t.Fatalf("expected both version strings in warning, got: %s", out)
	}
}

func TestGetBinaryVersion(t *testing.T) {
	dir := t.TempDir()

	// Script that responds to --version.
	okScript := filepath.Join(dir, "ok")
	if err := os.WriteFile(okScript, []byte("#!/bin/sh\necho '2nb version 1.2.3'\n"), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}
	if got := getBinaryVersion(okScript); got != "1.2.3" {
		t.Errorf("got %q, want %q", got, "1.2.3")
	}

	// Script that exits non-zero.
	failScript := filepath.Join(dir, "fail")
	if err := os.WriteFile(failScript, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatalf("write fail script: %v", err)
	}
	if got := getBinaryVersion(failScript); got != "unknown" {
		t.Errorf("got %q, want %q", got, "unknown")
	}
}

func TestUpdateZshrc_PlacesBlockBeforeExistingCompinit(t *testing.T) {
	dir := t.TempDir()
	zshrc := filepath.Join(dir, ".zshrc")
	existing := strings.Join([]string{
		"# zshrc",
		"export PATH=$HOME/bin:$PATH",
		`source "$HOME/google-cloud-sdk/completion.zsh.inc"`,
		"autoload -Uz compinit",
		"compinit -i",
		"",
	}, "\n")
	if err := os.WriteFile(zshrc, []byte(existing), 0o644); err != nil {
		t.Fatalf("write zshrc: %v", err)
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
	blockIdx := strings.Index(s, zshrcBlockBegin)
	// Block must land before the gcloud source line so our fpath is set up
	// before gcloud's compinit runs.
	gcloudIdx := strings.Index(s, "google-cloud-sdk")
	if blockIdx == -1 || gcloudIdx == -1 {
		t.Fatalf("missing block (%d) or gcloud line (%d)", blockIdx, gcloudIdx)
	}
	if blockIdx > gcloudIdx {
		t.Fatalf("managed block must be before gcloud completion source (block at %d, gcloud at %d)", blockIdx, gcloudIdx)
	}
}

func TestUpdateZshrc_PlacesBlockBeforeExistingFpath(t *testing.T) {
	dir := t.TempDir()
	zshrc := filepath.Join(dir, ".zshrc")
	existing := strings.Join([]string{
		"# zshrc",
		"export PATH=$HOME/bin:$PATH",
		"fpath=(/opt/homebrew/share/zsh/site-functions $fpath)",
		"autoload -Uz compinit",
		"compinit -i",
		"",
	}, "\n")
	if err := os.WriteFile(zshrc, []byte(existing), 0o644); err != nil {
		t.Fatalf("write zshrc: %v", err)
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
	blockIdx := strings.Index(s, zshrcBlockBegin)
	fpathIdx := strings.Index(s, "fpath=(/opt/homebrew")
	if blockIdx == -1 || fpathIdx == -1 {
		t.Fatalf("missing block (%d) or existing fpath line (%d)", blockIdx, fpathIdx)
	}
	if blockIdx > fpathIdx {
		t.Fatalf("managed block must be before existing fpath line (block at %d, fpath at %d)", blockIdx, fpathIdx)
	}
}

func TestFirstTopLevelReturnOrExitLine(t *testing.T) {
	cases := []struct {
		name  string
		lines []string
		want  int
	}{
		{
			name:  "bare return",
			lines: []string{"export X=1", "return"},
			want:  1,
		},
		{
			name:  "bare exit",
			lines: []string{"export X=1", "exit 0"},
			want:  1,
		},
		{
			name:  "conditional && return",
			lines: []string{"export X=1", `[[ "$TERM_PROGRAM" == "antigravity" ]] && return`, "compinit"},
			want:  1,
		},
		{
			name:  "conditional || return",
			lines: []string{"export X=1", "[[ $- == *i* ]] || return", "compinit"},
			want:  1,
		},
		{
			name:  "if-block guard with indented return",
			lines: []string{"# header", "export X=1", "if [[ ! -o interactive ]]; then", "  return 2>/dev/null || exit 0", "fi", "compinit"},
			want:  2,
		},
		{
			name:  "indented return inside block ignored",
			lines: []string{"if [[ cond ]]; then", "  do_something", "  return", "fi"},
			want:  -1, // do_something before return → not a simple guard
		},
		{
			name:  "no return",
			lines: []string{"export X=1", "autoload -Uz compinit", "compinit -i"},
			want:  -1,
		},
		{
			name:  "skip comment lines",
			lines: []string{"# not a return", "return"},
			want:  1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := firstTopLevelReturnOrExitLine(tc.lines)
			if got != tc.want {
				t.Errorf("got %d, want %d", got, tc.want)
			}
		})
	}
}

func TestFirstCompletionSetupLine(t *testing.T) {
	cases := []struct {
		name  string
		lines []string
		want  int
	}{
		{
			name:  "explicit compinit",
			lines: []string{"export X=1", "autoload -Uz compinit", "compinit -i"},
			want:  1,
		},
		{
			name:  "fpath assignment",
			lines: []string{"export X=1", "fpath=(/opt/homebrew/share/zsh/site-functions $fpath)"},
			want:  1,
		},
		{
			name:  "gcloud source completion",
			lines: []string{"export X=1", `source "$HOME/google-cloud-sdk/completion.zsh.inc"`, "compinit"},
			want:  1,
		},
		{
			name:  "dot-source completion",
			lines: []string{"export X=1", `. "$HOME/google-cloud-sdk/completion.zsh.inc"`, "compinit"},
			want:  1,
		},
		{
			name:  "no completion setup",
			lines: []string{"export X=1", "alias ll='ls -la'"},
			want:  -1,
		},
		{
			name:  "skip indented compinit",
			lines: []string{"if true; then", "  compinit", "fi"},
			want:  -1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := firstCompletionSetupLine(tc.lines)
			if got != tc.want {
				t.Errorf("got %d, want %d", got, tc.want)
			}
		})
	}
}
