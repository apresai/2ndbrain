package cli

import (
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"

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
	slog.Info("vault initialized", "path", absPath)

	fmt.Fprintf(cmd.ErrOrStderr(), "Initialized 2ndbrain vault at %s\n", v.Root)
	if !flagPorcelain {
		fmt.Fprintln(cmd.ErrOrStderr(), "\nNext steps:")
		fmt.Fprintln(cmd.ErrOrStderr(), "  2nb create \"My First Note\"    Create a document")
		fmt.Fprintln(cmd.ErrOrStderr(), "  2nb ai setup                  Configure AI search")
	}
	return nil
}
