package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/apresai/2ndbrain/internal/output"
	"github.com/apresai/2ndbrain/internal/vault"
	"github.com/spf13/cobra"
)

var Version = "dev"

var (
	flagFormat    string
	flagPorcelain bool
	flagVault     string
)

const (
	ExitOK         = 0
	ExitNotFound   = 1
	ExitValidation = 2
	ExitStaleRef   = 3
)

var rootCmd = &cobra.Command{
	Use:   "2nb",
	Short: "2ndbrain — AI-native markdown knowledge base",
	Long:  "A CLI for managing markdown knowledge bases with hybrid search, MCP server, and structured metadata.",
}

func init() {
	rootCmd.PersistentFlags().StringVar(&flagFormat, "format", "", "Output format: json, csv, yaml")
	rootCmd.PersistentFlags().BoolVar(&flagPorcelain, "porcelain", false, "Machine-readable output (no color, no progress)")
	rootCmd.PersistentFlags().Bool("json", false, "Output as JSON (shorthand for --format json)")
	rootCmd.PersistentFlags().Bool("csv", false, "Output as CSV (shorthand for --format csv)")
	rootCmd.PersistentFlags().Bool("yaml", false, "Output as YAML (shorthand for --format yaml)")
	rootCmd.PersistentFlags().StringVar(&flagVault, "vault", "", "Path to vault (default: current directory or 2NB_VAULT env var)")

	rootCmd.Version = Version
}

func Execute() error {
	return rootCmd.Execute()
}

// expandPath resolves ~ to home directory and cleans the path.
func expandPath(path string) string {
	if path == "" {
		return path
	}
	if path == "~" {
		home, _ := os.UserHomeDir()
		return home
	}
	if len(path) > 1 && path[:2] == "~/" {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, path[2:])
	}
	return path
}

// openVault resolves the vault path using this priority:
// 1. --vault flag
// 2. 2NB_VAULT env var
// 3. ~/.2ndbrain-active-vault (shared with GUI)
// 4. Current directory
func openVault() (*vault.Vault, error) {
	dir := expandPath(flagVault)
	if dir == "" {
		dir = expandPath(os.Getenv("2NB_VAULT"))
	}
	if dir == "" {
		dir = getActiveVault()
	}
	if dir == "" {
		dir = "."
	}

	v, err := vault.Open(dir)
	if err != nil {
		return nil, fmt.Errorf("%w\n\nSet --vault flag, 2NB_VAULT env var, or run `2nb init <path>`", err)
	}

	// Update active vault so future commands use this vault
	if abs, err := filepath.Abs(v.Root); err == nil {
		_ = setActiveVault(abs)
	}

	return v, nil
}

func getFormat(cmd *cobra.Command) output.Format {
	if flagFormat != "" {
		return output.Format(flagFormat)
	}
	if v, _ := cmd.Flags().GetBool("json"); v {
		return output.FormatJSON
	}
	if v, _ := cmd.Flags().GetBool("csv"); v {
		return output.FormatCSV
	}
	if v, _ := cmd.Flags().GetBool("yaml"); v {
		return output.FormatYAML
	}
	return "" // default: pretty output; use --json for machine-readable
}

// ExitError is an error that carries an exit code for the CLI.
type ExitError struct {
	Code    int
	Message string
}

func (e *ExitError) Error() string {
	return e.Message
}

func exitWithError(code int, msg string) error {
	return &ExitError{Code: code, Message: msg}
}
