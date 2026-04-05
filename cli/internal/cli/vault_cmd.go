package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/apresai/2ndbrain/internal/output"
	"github.com/apresai/2ndbrain/internal/vault"
	"github.com/spf13/cobra"
)

var vaultCmd = &cobra.Command{
	Use:   "vault [path]",
	Short: "Show or set the active vault",
	Long:  "With no arguments, shows which vault is active and how it was resolved.\nWith a path argument, sets the active vault.",
	Args:  cobra.MaximumNArgs(1),
	RunE:  runVault,
}

func init() {
	rootCmd.AddCommand(vaultCmd)
}

type VaultInfo struct {
	Path      string `json:"path"`
	Source    string `json:"source"`
	Name      string `json:"name"`
	Documents int    `json:"documents"`
}

func runVault(cmd *cobra.Command, args []string) error {
	// Set mode: 2nb vault <path>
	if len(args) == 1 {
		return setVault(cmd, args[0])
	}

	// Show mode: 2nb vault
	return showVault(cmd)
}

func setVault(cmd *cobra.Command, path string) error {
	absPath, err := filepath.Abs(expandPath(path))
	if err != nil {
		return fmt.Errorf("resolve path: %w", err)
	}

	// Validate it's a real vault
	v, err := vault.Open(absPath)
	if err != nil {
		return fmt.Errorf("not a vault: %w", err)
	}
	v.Close()

	if err := setActiveVault(absPath); err != nil {
		return fmt.Errorf("set active vault: %w", err)
	}

	if !flagPorcelain {
		fmt.Printf("Active vault set to %s\n", absPath)
	}
	return nil
}

func showVault(cmd *cobra.Command) error {
	// Determine resolution source
	dir, source := resolveVaultSource()

	v, err := vault.Open(dir)
	if err != nil {
		return fmt.Errorf("no active vault: %w\n\nSet with: 2nb vault <path>", err)
	}
	defer v.Close()

	var docCount int
	v.DB.Conn().QueryRow("SELECT COUNT(*) FROM documents").Scan(&docCount)

	info := VaultInfo{
		Path:      v.Root,
		Source:    source,
		Name:      v.Config.Name,
		Documents: docCount,
	}

	format := getFormat(cmd)
	if format != "" {
		return output.Write(os.Stdout, format, info)
	}

	fmt.Printf("Active vault:  %s\n", info.Path)
	fmt.Printf("Source:         %s\n", info.Source)
	fmt.Printf("Name:           %s\n", info.Name)
	fmt.Printf("Documents:      %d\n", info.Documents)
	return nil
}

func resolveVaultSource() (string, string) {
	if flagVault != "" {
		return expandPath(flagVault), "--vault flag"
	}
	if env := os.Getenv("2NB_VAULT"); env != "" {
		return expandPath(env), "2NB_VAULT environment variable"
	}
	if active := getActiveVault(); active != "" {
		return active, "~/.2ndbrain-active-vault"
	}
	return ".", "current directory"
}
