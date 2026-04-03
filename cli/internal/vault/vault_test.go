package vault

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInit_CreatesStructure(t *testing.T) {
	dir := t.TempDir()
	v, err := Init(dir)
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	defer v.Close()

	// Check directory structure
	for _, sub := range []string{"", "models", "recovery", "logs"} {
		path := filepath.Join(dir, DotDirName, sub)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			t.Errorf("directory %s should exist", sub)
		}
	}

	// Check config and schemas
	for _, file := range []string{"config.yaml", "schemas.yaml", "index.db"} {
		path := filepath.Join(dir, DotDirName, file)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			t.Errorf("file %s should exist", file)
		}
	}
}

func TestInit_AlreadyInitialized(t *testing.T) {
	dir := t.TempDir()
	v, err := Init(dir)
	if err != nil {
		t.Fatalf("first init: %v", err)
	}
	v.Close()

	_, err = Init(dir)
	if err != ErrAlreadyInit {
		t.Errorf("second init should return ErrAlreadyInit, got %v", err)
	}
}

func TestOpen_FindsVaultRoot(t *testing.T) {
	dir := t.TempDir()
	v, err := Init(dir)
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	v.Close()

	// Create a subdirectory
	child := filepath.Join(dir, "subdir", "deep")
	os.MkdirAll(child, 0o755)

	v2, err := Open(child)
	if err != nil {
		t.Fatalf("open from child: %v", err)
	}
	defer v2.Close()

	if v2.Root != dir {
		t.Errorf("root = %q, want %q", v2.Root, dir)
	}
}

func TestOpen_NotAVault(t *testing.T) {
	_, err := Open(t.TempDir())
	if err != ErrNotAVault {
		t.Errorf("expected ErrNotAVault, got %v", err)
	}
}

func TestIsIgnored(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{".git", true},
		{".env", true},
		{".env.local", true},
		{"credentials.json", true},
		{"my-secret-file.md", true},
		{"README.md", false},
		{"notes/doc.md", false},
		{".hidden", true},
	}
	for _, tc := range tests {
		got := IsIgnored(tc.path)
		if got != tc.want {
			t.Errorf("IsIgnored(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}

func TestRelPath_AbsPath_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	v, err := Init(dir)
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	defer v.Close()

	absPath := filepath.Join(dir, "notes", "test.md")
	relPath := v.RelPath(absPath)
	if relPath != filepath.Join("notes", "test.md") {
		t.Errorf("relPath = %q", relPath)
	}

	back := v.AbsPath(relPath)
	if back != absPath {
		t.Errorf("absPath = %q, want %q", back, absPath)
	}
}
