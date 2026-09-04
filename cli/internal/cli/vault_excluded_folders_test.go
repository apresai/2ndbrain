package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestVaultStatusNamesTheExcludedTemplateFolder: "why is my template missing
// from search?" must be answerable from `2nb vault status`, not from the source.
func TestVaultStatusNamesTheExcludedTemplateFolder(t *testing.T) {
	_, root := newContractVault(t)

	obsDir := filepath.Join(root, ".obsidian")
	if err := os.MkdirAll(obsDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(obsDir, "templates.json"), []byte(`{"folder":"templates"}`), 0o644); err != nil {
		t.Fatalf("write templates.json: %v", err)
	}

	out, err := runCLIArgs(t, root, "vault", "status", "--json")
	if err != nil {
		t.Fatalf("vault status --json: %v", err)
	}
	var got struct {
		ExcludedFolders []string `json:"excluded_folders"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("decode: %v\n%s", err, out)
	}
	if len(got.ExcludedFolders) != 1 || got.ExcludedFolders[0] != "templates" {
		t.Fatalf("excluded_folders = %v, want [templates]", got.ExcludedFolders)
	}

	human, err := runCLIArgs(t, root, "vault", "status")
	if err != nil {
		t.Fatalf("vault status: %v", err)
	}
	if !strings.Contains(string(human), "Not indexed: templates") {
		t.Errorf("vault status did not name the excluded folder:\n%s", human)
	}
}

// TestVaultStatusOmitsExcludedFoldersWhenThereAreNone: the field is omitempty so
// the Swift decoder never meets a null, and the human output gains no line.
func TestVaultStatusOmitsExcludedFoldersWhenThereAreNone(t *testing.T) {
	_, root := newContractVault(t)

	out, err := runCLIArgs(t, root, "vault", "status", "--json")
	if err != nil {
		t.Fatalf("vault status --json: %v", err)
	}
	if strings.Contains(string(out), "excluded_folders") {
		t.Errorf("excluded_folders is present with no template folder configured:\n%s", out)
	}
	human, err := runCLIArgs(t, root, "vault", "status")
	if err != nil {
		t.Fatalf("vault status: %v", err)
	}
	if strings.Contains(string(human), "Not indexed:") {
		t.Errorf("vault status printed an exclusion line with nothing excluded:\n%s", human)
	}
}
