package cli

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

var initPath string

// initCmd is the deprecated alias for `vault create`. Kept so existing
// scripts, README examples, and muscle memory still work. Cobra hides
// commands with a non-empty `Deprecated` field from the default help
// listing and prints the deprecation message before RunE.
var initCmd = &cobra.Command{
	Use:        "init [path]",
	Short:      "Initialize a new 2ndbrain vault (deprecated: use `vault create`)",
	Deprecated: "use `2nb vault create <path>` instead",
	Example:    `  2nb init ~/my-vault`,
	Args:       cobra.MaximumNArgs(1),
	RunE:       runInit,
}

func init() {
	initCmd.Flags().StringVar(&initPath, "path", "", "Directory to initialize as a vault (legacy; prefer the positional argument)")
	initCmd.GroupID = "start"
	rootCmd.AddCommand(initCmd)
}

func runInit(cmd *cobra.Command, args []string) error {
	path := ""
	switch {
	case len(args) == 1:
		path = args[0]
	case initPath != "":
		path = initPath
	default:
		path = "."
	}
	return createVaultAt(cmd, path)
}

// vaultGitignoreMarker identifies the 2nb-owned section of .gitignore
// so we can idempotently append without duplicating on subsequent
// `2nb init` or future init-like commands.
const vaultGitignoreMarker = "# 2ndbrain local state"

var vaultGitignoreEntries = []string{
	vaultGitignoreMarker,
	".2ndbrain/config.yaml",
	".2ndbrain/index.db",
	".2ndbrain/index.db-wal",
	".2ndbrain/index.db-shm",
	".2ndbrain/bench.db",
	".2ndbrain/logs/",
	".2ndbrain/recovery/",
	".2ndbrain/mcp/",
	".2ndbrain/*.bak",
}

// writeVaultGitignore ensures the vault-root .gitignore excludes the
// personal/local-state files under .2ndbrain/. Idempotent — if the
// marker line is already present, we assume the block exists and do
// nothing. schemas.yaml is intentionally NOT in the ignore list: it
// holds shared doc-type definitions that teams edit together.
func writeVaultGitignore(root string) {
	path := filepath.Join(root, ".gitignore")
	existing, err := os.ReadFile(path)
	if err == nil && strings.Contains(string(existing), vaultGitignoreMarker) {
		return
	}

	var buf strings.Builder
	if err == nil && len(existing) > 0 {
		buf.Write(existing)
		if !strings.HasSuffix(string(existing), "\n") {
			buf.WriteString("\n")
		}
		buf.WriteString("\n")
	}
	for _, line := range vaultGitignoreEntries {
		buf.WriteString(line)
		buf.WriteString("\n")
	}

	// Best-effort: if the write fails (e.g., permission issue), don't
	// fail the init — the vault is still usable, the user just won't
	// have a gitignore.
	_ = os.WriteFile(path, []byte(buf.String()), 0o644)
}
