package cli

import (
	"os"
	"path/filepath"
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
	// Write a temp active vault path
	tmpDir := t.TempDir()
	origPath := activeVaultPath()

	// Test get when file doesn't exist
	got := getActiveVault()
	// Just verify it doesn't panic — the result depends on whether the file exists

	// Test set and get round-trip
	testPath := filepath.Join(tmpDir, "test-vault")
	if err := os.MkdirAll(testPath, 0o755); err != nil {
		t.Fatal(err)
	}

	// Save original, set test, verify, restore
	original := got
	if err := setActiveVault(testPath); err != nil {
		t.Fatalf("setActiveVault: %v", err)
	}

	got = getActiveVault()
	if got != testPath {
		t.Errorf("getActiveVault() = %q, want %q", got, testPath)
	}

	// Restore
	if original != "" {
		setActiveVault(original)
	} else {
		os.Remove(activeVaultPath())
	}
	_ = origPath
}
