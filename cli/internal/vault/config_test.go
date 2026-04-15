package vault

import (
	"os"
	"path/filepath"
	"testing"
)

// TestLoadConfig_Recovers verifies that LoadConfig self-heals a vault
// whose config.yaml is missing or corrupt — the "just works" portability
// requirement. The DB and markdown files remain authoritative; config
// is a user-preference file and losing it should never brick the vault.
func TestLoadConfig_Recovers(t *testing.T) {
	t.Run("missing file", func(t *testing.T) {
		dir := t.TempDir()
		dotDir := filepath.Join(dir, DotDirName)
		if err := os.MkdirAll(dotDir, 0o755); err != nil {
			t.Fatal(err)
		}
		// Don't create config.yaml.
		cfg, err := LoadConfig(dotDir)
		if err != nil {
			t.Fatalf("missing file should recover, got error: %v", err)
		}
		if cfg.Recovered != "config_missing" {
			t.Errorf("expected Recovered=config_missing, got %q", cfg.Recovered)
		}
		// Defaults should be applied.
		if cfg.AI.Provider == "" {
			t.Error("expected default AI provider to be populated")
		}
		// File should have been written.
		if _, err := os.Stat(filepath.Join(dotDir, "config.yaml")); err != nil {
			t.Errorf("expected config.yaml to be regenerated: %v", err)
		}
	})

	t.Run("corrupt yaml", func(t *testing.T) {
		dir := t.TempDir()
		dotDir := filepath.Join(dir, DotDirName)
		if err := os.MkdirAll(dotDir, 0o755); err != nil {
			t.Fatal(err)
		}
		// Write garbage that YAML can't parse.
		if err := os.WriteFile(filepath.Join(dotDir, "config.yaml"), []byte("not: [valid yaml"), 0o644); err != nil {
			t.Fatal(err)
		}
		cfg, err := LoadConfig(dotDir)
		if err != nil {
			t.Fatalf("corrupt file should recover, got error: %v", err)
		}
		if cfg.Recovered != "config_corrupt_backup" {
			t.Errorf("expected Recovered=config_corrupt_backup, got %q", cfg.Recovered)
		}
		// Original should have been backed up to .bak.
		if _, err := os.Stat(filepath.Join(dotDir, "config.yaml.bak")); err != nil {
			t.Errorf("expected config.yaml.bak to exist: %v", err)
		}
		// New config.yaml should exist and be valid.
		if _, err := os.Stat(filepath.Join(dotDir, "config.yaml")); err != nil {
			t.Errorf("expected config.yaml to be regenerated: %v", err)
		}
	})

	t.Run("valid file", func(t *testing.T) {
		// Sanity check: a well-formed config should not be marked recovered.
		dir := t.TempDir()
		dotDir := filepath.Join(dir, DotDirName)
		if err := os.MkdirAll(dotDir, 0o755); err != nil {
			t.Fatal(err)
		}
		cfg := DefaultConfig("test")
		if err := cfg.Save(dotDir); err != nil {
			t.Fatal(err)
		}
		loaded, err := LoadConfig(dotDir)
		if err != nil {
			t.Fatal(err)
		}
		if loaded.Recovered != "" {
			t.Errorf("valid config should not be marked recovered, got %q", loaded.Recovered)
		}
	})
}
