package cli

import (
	"fmt"
	"io"
	"log/slog"
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
	flagVerbose   bool
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
	Long: `2ndbrain is a CLI and MCP server for markdown knowledge bases with
hybrid (BM25 + semantic) search, structured metadata, and a link graph.

Quick start:
  1. Create a vault:     2nb vault create ~/my-vault
  2. Add a note:         2nb create "My First Note"
  3. Build the index:    2nb index
  4. Configure AI:       2nb ai setup
  5. Search & ask:       2nb search "query" / 2nb ask "question"
  6. Expose to agents:   2nb skills install --all  ·  2nb mcp-setup`,
	Example: `  2nb vault create ~/notes          # create a new vault
  2nb create "Project Kickoff"      # add a note
  2nb search "authentication"       # keyword + semantic search
  2nb ask "what did we decide?"     # RAG Q&A
  2nb mcp-server                    # start MCP for Claude Code, Cursor, etc.`,
	RunE: runRoot,
}

func init() {
	rootCmd.PersistentFlags().StringVar(&flagFormat, "format", "", "Output format: json, csv, yaml")
	rootCmd.PersistentFlags().BoolVar(&flagPorcelain, "porcelain", false, "Machine-readable output (no color, no progress)")
	rootCmd.PersistentFlags().Bool("json", false, "Output as JSON (shorthand for --format json)")
	rootCmd.PersistentFlags().Bool("csv", false, "Output as CSV (shorthand for --format csv)")
	rootCmd.PersistentFlags().Bool("yaml", false, "Output as YAML (shorthand for --format yaml)")
	rootCmd.PersistentFlags().StringVar(&flagVault, "vault", "", "Path to vault (default: current directory or 2NB_VAULT env var)")
	rootCmd.PersistentFlags().BoolVarP(&flagVerbose, "verbose", "v", false, "Enable verbose logging (debug level)")

	rootCmd.Version = Version

	// Our custom `completion` command (in completion.go) owns the
	// completion UX — disable Cobra's auto-generated one so the
	// `install` subcommand and the shell-specific emitters live under
	// a single tree.
	rootCmd.CompletionOptions.DisableDefaultCmd = true

	// Command groups — registered before any AddCommand calls.
	rootCmd.AddGroup(
		&cobra.Group{ID: "start", Title: "Getting Started:"},
		&cobra.Group{ID: "docs", Title: "Documents:"},
		&cobra.Group{ID: "ai", Title: "Search & AI:"},
		&cobra.Group{ID: "quality", Title: "Quality:"},
		&cobra.Group{ID: "integr", Title: "Integration:"},
		&cobra.Group{ID: "io", Title: "Import / Export:"},
		&cobra.Group{ID: "config", Title: "Configuration:"},
	)
	rootCmd.SetHelpCommandGroupID("start")
	rootCmd.SetCompletionCommandGroupID("config")
}

func Execute() error {
	// Set up slog before any command runs
	rootCmd.PersistentPreRun = func(cmd *cobra.Command, args []string) {
		setupLogging()
	}
	return rootCmd.Execute()
}

// setupLogging configures slog for the CLI. Debug level with --verbose,
// info level otherwise. Logs go to stderr when verbose, otherwise discarded
// from the terminal (file logging is set up separately after vault opens).
func setupLogging() {
	level := slog.LevelInfo
	if flagVerbose {
		level = slog.LevelDebug
	}

	var w io.Writer = io.Discard
	if flagVerbose {
		w = os.Stderr
	}

	handler := slog.NewTextHandler(w, &slog.HandlerOptions{Level: level})
	slog.SetDefault(slog.New(handler))
}

// setupFileLogging adds a log file handler after the vault is opened.
// Writes structured logs to .2ndbrain/logs/cli.log.
func setupFileLogging(v *vault.Vault) {
	logsDir := filepath.Join(v.Root, vault.DotDirName, "logs")
	os.MkdirAll(logsDir, 0o755)
	logFile := filepath.Join(logsDir, "cli.log")
	f, err := os.OpenFile(logFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}

	level := slog.LevelInfo
	if flagVerbose {
		level = slog.LevelDebug
	}

	// Multi-writer: file always, stderr only if verbose
	var writers []io.Writer
	writers = append(writers, f)
	if flagVerbose {
		writers = append(writers, os.Stderr)
	}
	w := io.MultiWriter(writers...)

	handler := slog.NewTextHandler(w, &slog.HandlerOptions{Level: level})
	slog.SetDefault(slog.New(handler))
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

func runRoot(cmd *cobra.Command, args []string) error {
	if flagPorcelain {
		return cmd.Help()
	}

	v, err := openVault()
	if err != nil {
		// No vault reachable — show welcome message.
		fmt.Fprintln(cmd.ErrOrStderr(), "2ndbrain — AI-native markdown knowledge base.")
		fmt.Fprintln(cmd.ErrOrStderr())
		fmt.Fprintln(cmd.ErrOrStderr(), "Get started:")
		fmt.Fprintln(cmd.ErrOrStderr(), "  1. Create a vault:   2nb vault create ~/my-vault")
		fmt.Fprintln(cmd.ErrOrStderr(), "  2. Add a note:       2nb create \"My First Note\"")
		fmt.Fprintln(cmd.ErrOrStderr(), "  3. Configure AI:     2nb ai setup")
		fmt.Fprintln(cmd.ErrOrStderr(), "  4. Search & ask:     2nb search \"query\"  /  2nb ask \"question\"")
		fmt.Fprintln(cmd.ErrOrStderr(), "  5. Wire up agents:   2nb skills install --all  /  2nb mcp-setup")
		fmt.Fprintln(cmd.ErrOrStderr())
		fmt.Fprintln(cmd.ErrOrStderr(), "Run `2nb --help` for the full command list or `2nb vault list` for recent vaults.")
		return nil
	}
	defer v.Close()

	count, embedded, _ := v.DB.EmbeddingCounts()

	aiStatus := "not configured"
	if p := v.Config.AI.Provider; p != "" {
		aiStatus = p
		if m := v.Config.AI.EmbeddingModel; m != "" {
			aiStatus += " (" + m + ")"
		}
	}

	label, hint := nextStepHint(count, embedded, v.Config.AI.Provider)
	fmt.Fprintf(cmd.ErrOrStderr(), "Vault: %s (%d docs, AI: %s)\n", v.Root, count, aiStatus)
	fmt.Fprintf(cmd.ErrOrStderr(), "%s: %s\n\n", label, hint)
	return cmd.Help()
}

// nextStepHint returns a label ("Next" or "Try") and a one-line hint
// matched to the vault's current state, so running `2nb` in a vault
// always surfaces the single most useful next command.
func nextStepHint(docCount, embeddedCount int, provider string) (label, hint string) {
	switch {
	case docCount == 0:
		return "Next", `2nb create "My First Note"    (add your first document)`
	case provider == "":
		return "Next", "2nb ai setup                  (enable semantic search & ask)"
	case embeddedCount < docCount:
		return "Next", "2nb index                     (embed your documents for semantic search)"
	default:
		return "Try", `2nb search "query"  or  2nb ask "your question"`
	}
}

// openVault resolves the vault path using this priority:
// 1. --vault flag
// 2. 2NB_VAULT env var
// 3. ~/.2ndbrain-active-vault (shared with GUI)
// 4. Current directory
func openVault() (*vault.Vault, error) {
	dir := expandPath(flagVault)
	source := "--vault flag"
	if dir == "" {
		dir = expandPath(os.Getenv("2NB_VAULT"))
		source = "2NB_VAULT env var"
	}
	if dir == "" {
		dir = getActiveVault()
		source = "active vault (~/.2ndbrain-active-vault)"
		// Validate the active vault path still exists on disk.
		if dir != "" {
			if _, err := os.Stat(filepath.Join(dir, vault.DotDirName)); err != nil {
				dir = ""
			}
		}
	}
	if dir == "" {
		dir = "."
		source = "current directory"
	}

	absDir, _ := filepath.Abs(dir)
	v, err := vault.Open(dir)
	if err != nil {
		return nil, fmt.Errorf("no vault found at %s (resolved from %s)\n\nTo fix:\n  • Run from inside your vault directory\n  • Use --vault /path/to/vault\n  • Set 2NB_VAULT=/path/to/vault\n  • Create a new vault with `2nb init /path/to/vault`", absDir, source)
	}

	return v, nil
}

// openVaultAndSetActive opens the vault and updates the active vault file.
// Use for write commands (init, create, index, delete). Read commands use openVault().
func openVaultAndSetActive() (*vault.Vault, error) {
	v, err := openVault()
	if err != nil {
		return nil, err
	}
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
