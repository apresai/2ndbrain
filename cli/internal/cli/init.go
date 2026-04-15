package cli

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/apresai/2ndbrain/internal/vault"
	"github.com/spf13/cobra"
)

var initPath string

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize a new 2ndbrain vault",
	RunE:  runInit,
}

func init() {
	initCmd.Flags().StringVar(&initPath, "path", ".", "Directory to initialize as a vault")
	initCmd.GroupID = "start"
	rootCmd.AddCommand(initCmd)
}

func runInit(cmd *cobra.Command, args []string) error {
	v, err := vault.Init(initPath)
	if err != nil {
		if errors.Is(err, vault.ErrAlreadyInit) {
			return fmt.Errorf("vault already initialized at %s", initPath)
		}
		return fmt.Errorf("init vault: %w", err)
	}
	defer v.Close()
	setupFileLogging(v)

	absPath, _ := filepath.Abs(v.Root)
	if absPath != "" {
		_ = setActiveVault(absPath)
	}

	// Write a .gitignore for the personal/local-state files under
	// .2ndbrain/ so team vaults shared via git don't produce merge
	// conflicts on config, DBs, or recovery snapshots. Only the
	// schemas.yaml (shared doc-type definitions) is committable.
	writeVaultGitignore(v.Root)

	slog.Info("vault initialized", "path", absPath)

	fmt.Fprintf(cmd.ErrOrStderr(), "Initialized 2ndbrain vault at %s\n", v.Root)
	if !flagPorcelain {
		fmt.Fprintln(cmd.ErrOrStderr(), "\nNext steps:")
		fmt.Fprintln(cmd.ErrOrStderr(), "  2nb create \"My First Note\"    Create a document")
		fmt.Fprintln(cmd.ErrOrStderr(), "  2nb ai setup                  Configure AI search")
	}
	return nil
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
