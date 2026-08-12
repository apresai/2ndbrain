package ai

import (
	"os"
	"path/filepath"
)

// globalConfigDir is the per-user 2nb config directory: $XDG_CONFIG_HOME/2nb
// when set, otherwise ~/.config/2nb. Empty when the home directory cannot be
// resolved. Shared by the user catalog, vendor policy, and machine-local
// Bedrock credentials so those files cannot drift onto different roots.
func globalConfigDir() string {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "2nb")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "2nb")
}
