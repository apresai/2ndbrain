package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// seedRepo creates a tiny git repo in t.TempDir() with one or more commits
// and returns its path. Skips the test if git is not installed.
func seedRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed; skipping")
	}
	dir := t.TempDir()
	runInDir := makeRunInDir(t, dir)

	runInDir("init", "-q", "-b", "main")
	runInDir("config", "commit.gpgsign", "false")

	// First commit: a single file.
	if err := os.WriteFile(filepath.Join(dir, "foo.md"), []byte("# Foo\nhello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runInDir("add", "foo.md")
	runInDir("commit", "-q", "-m", "initial commit")

	// Second commit: modify foo.md and add bar.md, so Show has multi-file data.
	if err := os.WriteFile(filepath.Join(dir, "foo.md"), []byte("# Foo\nhello world\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bar.md"), []byte("# Bar\nbaz\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runInDir("add", "foo.md", "bar.md")
	runInDir("commit", "-q", "-m", "second commit: add bar, update foo")

	return dir
}

func latestHash(t *testing.T, dir string) string {
	t.Helper()
	cmd := exec.Command("git", "-C", dir, "rev-parse", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("rev-parse HEAD: %v", err)
	}
	return strings.TrimSpace(string(out))
}

func TestShow_SingleCommit(t *testing.T) {
	dir := seedRepo(t)
	hash := latestHash(t, dir)

	detail, err := Show(dir, hash)
	if err != nil {
		t.Fatalf("Show: %v", err)
	}
	if detail.Hash != hash {
		t.Errorf("Hash mismatch: got %s want %s", detail.Hash, hash)
	}
	if !strings.Contains(detail.Subject, "second commit") {
		t.Errorf("Subject should contain 'second commit', got %q", detail.Subject)
	}
	if detail.Author != "Test" {
		t.Errorf("Author got %q", detail.Author)
	}
	if detail.Stats.FilesChanged != 2 {
		t.Errorf("FilesChanged got %d want 2", detail.Stats.FilesChanged)
	}
	if detail.Stats.Insertions < 2 {
		t.Errorf("Insertions should be >= 2, got %d", detail.Stats.Insertions)
	}
	if len(detail.Files) != 2 {
		t.Fatalf("Files len got %d want 2", len(detail.Files))
	}
	// Each file should have a non-empty diff.
	for _, f := range detail.Files {
		if f.Diff == "" {
			t.Errorf("File %s has empty diff", f.Path)
		}
	}
}

func TestShow_BadHash(t *testing.T) {
	dir := seedRepo(t)
	_, err := Show(dir, "0123456789abcdef0123456789abcdef01234567")
	if err == nil {
		t.Error("expected error for unknown hash, got nil")
	}
}

func TestShow_NonGitRepo(t *testing.T) {
	dir := t.TempDir()
	_, err := Show(dir, "HEAD")
	if err == nil {
		t.Error("expected error for non-git dir, got nil")
	}
	if !strings.Contains(err.Error(), "not a git repository") {
		t.Errorf("error should mention 'not a git repository', got %q", err.Error())
	}
}

func TestShow_BinaryFile(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	runInDir := makeRunInDir(t, dir)

	runInDir("init", "-q", "-b", "main")
	runInDir("config", "commit.gpgsign", "false")

	// A small binary-ish blob (PNG header)
	binaryData := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0x00, 0x00, 0x00, 0x0D}
	if err := os.WriteFile(filepath.Join(dir, "icon.png"), binaryData, 0o644); err != nil {
		t.Fatal(err)
	}
	runInDir("add", "icon.png")
	runInDir("commit", "-q", "-m", "add binary")

	hash := latestHash(t, dir)
	detail, err := Show(dir, hash)
	if err != nil {
		t.Fatalf("Show: %v", err)
	}
	if len(detail.Files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(detail.Files))
	}
	if !detail.Files[0].Binary {
		t.Errorf("expected binary flag to be true")
	}
	if detail.Files[0].Diff != "" {
		t.Errorf("binary file should have empty diff, got %q", detail.Files[0].Diff)
	}
}

// makeRunInDir returns a closure that runs git commands inside dir
// with deterministic author/committer env vars.
func makeRunInDir(t *testing.T, dir string) func(args ...string) {
	t.Helper()
	return func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=t@x",
			"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=t@x",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
}

// TestShow_PathologicalFilename exercises a directory name "foo b"
// (literal trailing space+b) that collides with the " b/" separator
// the old parser used to extract paths from "diff --git a/X b/X"
// headers. Order-based matching in Show() handles this correctly.
func TestShow_PathologicalFilename(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	runInDir := makeRunInDir(t, dir)

	runInDir("init", "-q", "-b", "main")
	runInDir("config", "commit.gpgsign", "false")

	sub := filepath.Join(dir, "foo b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "nested.md"), []byte("# Hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runInDir("add", ".")
	runInDir("commit", "-q", "-m", "pathological dir")

	hash := latestHash(t, dir)
	detail, err := Show(dir, hash)
	if err != nil {
		t.Fatalf("Show: %v", err)
	}
	if len(detail.Files) != 1 {
		t.Fatalf("want 1 file, got %d", len(detail.Files))
	}
	if detail.Files[0].Diff == "" {
		t.Errorf("diff should not be empty for %s", detail.Files[0].Path)
	}
}

// TestShow_NonASCIIFilename proves that a non-ASCII filename produces
// a non-empty diff. Leaves core.quotePath at git's default (true) so
// the test exercises the -c core.quotePath=false override that Show()
// now passes to git.
func TestShow_NonASCIIFilename(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	runInDir := makeRunInDir(t, dir)

	runInDir("init", "-q", "-b", "main")
	runInDir("config", "commit.gpgsign", "false")

	name := "caf\u00e9.md"
	if err := os.WriteFile(filepath.Join(dir, name), []byte("# Café\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runInDir("add", name)
	runInDir("commit", "-q", "-m", "non-ascii name")

	hash := latestHash(t, dir)
	detail, err := Show(dir, hash)
	if err != nil {
		t.Fatalf("Show: %v", err)
	}
	if len(detail.Files) != 1 {
		t.Fatalf("want 1 file, got %d", len(detail.Files))
	}
	if detail.Files[0].Path != name {
		t.Errorf("path got %q want %q", detail.Files[0].Path, name)
	}
	if detail.Files[0].Diff == "" {
		t.Errorf("diff should not be empty for %s", name)
	}
}

// TestShow_RenameFile guards order-based matching against git's
// brace-notation rename output ({old => new}). numstat emits one line
// per rename, and the patch emits one corresponding chunk, so the
// index-based zip still aligns 1:1.
func TestShow_RenameFile(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	runInDir := makeRunInDir(t, dir)

	runInDir("init", "-q", "-b", "main")
	runInDir("config", "commit.gpgsign", "false")

	if err := os.WriteFile(filepath.Join(dir, "foo.md"), []byte("# Foo\nhello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runInDir("add", "foo.md")
	runInDir("commit", "-q", "-m", "initial")

	runInDir("mv", "foo.md", "bar.md")
	runInDir("commit", "-q", "-m", "rename foo to bar")

	hash := latestHash(t, dir)
	detail, err := Show(dir, hash)
	if err != nil {
		t.Fatalf("Show: %v", err)
	}
	if len(detail.Files) != 1 {
		t.Fatalf("want 1 file, got %d", len(detail.Files))
	}
	if detail.Files[0].Diff == "" {
		t.Errorf("diff should not be empty for rename")
	}
}
