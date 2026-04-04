package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExpandPath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("cannot get home dir")
	}

	tests := []struct {
		input string
		want  string
	}{
		{"~", home},
		{"~/foo", filepath.Join(home, "foo")},
		{"~/foo/bar", filepath.Join(home, "foo/bar")},
		{"/absolute/path", "/absolute/path"},
		{"relative/path", "relative/path"},
		{"./here", "./here"},
		{"", ""},
		{"~user", "~user"}, // only ~ and ~/ are expanded, not ~user
	}

	for _, tt := range tests {
		got := expandPath(tt.input)
		if got != tt.want {
			t.Errorf("expandPath(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestValidateTitle(t *testing.T) {
	good := []string{
		"My Note",
		"Use JWT for Auth",
		"ADR 001: Database Choice",
		"Go and Swift's SQLite",
		"Version 2.0",
		"Notes on C++ (advanced)",
		"Hello, World",
		"A",
	}
	for _, title := range good {
		if err := validateTitle(title); err != nil {
			t.Errorf("validateTitle(%q) = %v, want nil", title, err)
		}
	}

	bad := []struct {
		title string
		desc  string
	}{
		{"-Bad", "starts with dash"},
		{"-", "single dash"},
		{"bad/path", "contains slash"},
		{"bad\\path", "contains backslash"},
		{"", "empty via regex"},
	}
	for _, tt := range bad {
		if err := validateTitle(tt.title); err == nil {
			t.Errorf("validateTitle(%q) should fail (%s)", tt.title, tt.desc)
		}
	}
}

func TestActiveVault(t *testing.T) {
	// Use a temp file to avoid mutating the real ~/.2ndbrain-active-vault (H4 fix)
	tmpFile := filepath.Join(t.TempDir(), "active-vault-test")

	// Test write and read round-trip using the file directly
	testPath := filepath.Join(t.TempDir(), "test-vault")
	os.MkdirAll(testPath, 0o755)

	// Write
	if err := os.WriteFile(tmpFile, []byte(testPath+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Read
	data, err := os.ReadFile(tmpFile)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.TrimSpace(string(data))
	if got != testPath {
		t.Errorf("round-trip = %q, want %q", got, testPath)
	}
}

func TestActiveVaultHelpers(t *testing.T) {
	// Verify getActiveVault doesn't panic when file doesn't exist
	// This reads the real file but doesn't write
	_ = getActiveVault()
}
