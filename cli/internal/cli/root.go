package cli

import (
	"github.com/apresai/2ndbrain/internal/output"
	"github.com/spf13/cobra"
)

var Version = "dev"

var (
	flagFormat    string
	flagPorcelain bool
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

	rootCmd.Version = Version
}

func Execute() error {
	return rootCmd.Execute()
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
	return output.FormatJSON // default to JSON for AI consumption
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
