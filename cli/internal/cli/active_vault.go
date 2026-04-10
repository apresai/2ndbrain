package cli

import (
	"os"
	"path/filepath"
	"strings"
)

const activeVaultFile = ".2ndbrain-active-vault"

// activeVaultPath returns the path to the active vault config file.
// Stored in ~/.2ndbrain-active-vault (readable by both CLI and GUI).
func activeVaultPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, activeVaultFile)
}

// getActiveVault reads the active vault path from the config file.
func getActiveVault() string {
	path := activeVaultPath()
	if path == "" {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// setActiveVault writes the active vault path to the config file.
// Skips writing when running under go test to avoid polluting the user's config.
func setActiveVault(vaultPath string) error {
	if os.Getenv("2NB_TEST") != "" {
		return nil
	}
	path := activeVaultPath()
	if path == "" {
		return nil
	}
	return os.WriteFile(path, []byte(vaultPath+"\n"), 0o644)
}
