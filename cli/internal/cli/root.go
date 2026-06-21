package cli

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

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
	// Don't dump the full usage/help text when a command fails at runtime
	// (a RunE error, e.g. "force-reembed incomplete"). Cobra otherwise prints
	// the error followed by the entire flag listing, so a caller that scrapes
	// the last stderr line (like the macOS app's index sheet) shows a stray
	// flag description instead of the real error. Errors themselves still print
	// (SilenceErrors stays false); only the usage dump is suppressed. Genuine
	// arg-parse mistakes still surface a clear "Error: unknown flag …" line.
	SilenceUsage: true,
	RunE:         runRoot,
}

func init() {
	rootCmd.PersistentFlags().StringVar(&flagFormat, "format", "", "Output format: json, csv, tsv, yaml, raw, md, text (listings also: paths, tree)")
	rootCmd.PersistentFlags().BoolVar(&flagPorcelain, "porcelain", false, "Machine-readable output (no color, no progress)")
	rootCmd.PersistentFlags().Bool("json", false, "Output as JSON (shorthand for --format json)")
	rootCmd.PersistentFlags().Bool("csv", false, "Output as CSV (shorthand for --format csv)")
	rootCmd.PersistentFlags().Bool("yaml", false, "Output as YAML (shorthand for --format yaml)")
	rootCmd.PersistentFlags().StringVar(&flagVault, "vault", "", "Path to vault (default: current directory or 2NB_VAULT env var)")
	rootCmd.PersistentFlags().BoolVarP(&flagVerbose, "verbose", "v", false, "Enable verbose logging (debug level)")
	rootCmd.PersistentFlags().BoolVar(&flagCopy, "copy", false, "Also copy the output to the clipboard (macOS pbcopy; supported commands only)")
	// Hidden: set by the obsidian-syntax shim from path=/file=. exact = strict
	// vault-relative path; fuzzy = always run the title/alias/suffix resolver;
	// "" (default) = auto (exact path if it exists, else the resolver).
	rootCmd.PersistentFlags().StringVar(&flagResolveMode, "resolve", "", "Target resolution mode: exact, fuzzy, auto")
	_ = rootCmd.PersistentFlags().MarkHidden("resolve")

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
	os.Args = preprocessArgs(os.Args)

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

	count, embedded, embeddableUnembedded, errCounts := v.DB.EmbeddingCounts()
	if errCounts != nil {
		slog.Warn("embedding counts query failed", "err", errCounts)
	}

	aiStatus := "not configured"
	if p := v.Config.AI.Provider; p != "" {
		aiStatus = p
		if m := v.Config.AI.EmbeddingModel; m != "" {
			aiStatus += " (" + m + ")"
		}
	}

	label, hint := nextStepHint(count, embedded, embedded+embeddableUnembedded, v.Config.AI.Provider)
	fmt.Fprintf(cmd.ErrOrStderr(), "Vault: %s (%d docs, AI: %s)\n", v.Root, count, aiStatus)
	fmt.Fprintf(cmd.ErrOrStderr(), "%s: %s\n\n", label, hint)
	return cmd.Help()
}

// nextStepHint returns a label ("Next" or "Try") and a one-line hint
// matched to the vault's current state, so running `2nb` in a vault
// always surfaces the single most useful next command. embeddableCount
// excludes empty notes (which can't be embedded), so a vault whose only
// gap is blank notes isn't perpetually told to run `2nb index`.
func nextStepHint(docCount, embeddedCount, embeddableCount int, provider string) (label, hint string) {
	switch {
	case docCount == 0:
		return "Next", `2nb create "My First Note"    (add your first document)`
	case provider == "":
		return "Next", "2nb ai setup                  (enable semantic search & ask)"
	case embeddedCount < embeddableCount:
		return "Next", "2nb index                     (embed your documents for semantic search)"
	default:
		return "Try", `2nb search "query"  or  2nb ask "your question"`
	}
}

// vaultSource labels which rung of the resolution ladder picked the vault.
// The string values are part of the `vault show --json` contract (its
// "source" field) — keep them stable.
type vaultSource string

const (
	sourceFlag     vaultSource = "--vault flag"
	sourceEnv      vaultSource = "2NB_VAULT environment variable"
	sourceActive   vaultSource = "~/.2ndbrain-active-vault"
	sourceObsidian vaultSource = "open Obsidian vault"
	sourceCwd      vaultSource = "current directory"
)

// resolveVaultDir is the single implementation of the vault-resolution
// ladder, shared by every command (including `vault status`/`show`):
//
//  1. --vault flag
//  2. 2NB_VAULT env var
//  3. ~/.2ndbrain-active-vault (shared with the GUI and a bare terminal)
//  4. Current directory, when it is inside a vault
//  5. The vault Obsidian currently has open (its own registry)
//
// The active pointer is validated with vault.IsVaultRoot — the stored path
// must itself be a vault root (.obsidian or .2ndbrain child). The current
// directory wins over the Obsidian registry so standing inside a vault always
// targets that vault; the upward walk for the cwd case still happens in
// openResolvedVault via vault.Open, so this never silently retargets a parent.
//
// Rung 5 is what lets a bare `2nb` from a non-vault directory (e.g. this source
// repo) resolve the same vault the macOS dashboard binds to, instead of erroring
// — the CLI now looks where the GUI looks. When nothing resolves, the final
// "." → sourceCwd produces the actionable error in openResolvedVault.
func resolveVaultDir() (string, vaultSource) {
	if dir := expandPath(flagVault); dir != "" {
		return dir, sourceFlag
	}
	if dir := expandPath(os.Getenv("2NB_VAULT")); dir != "" {
		return dir, sourceEnv
	}
	if dir := getActiveVault(); dir != "" && vault.IsVaultRoot(dir) {
		return dir, sourceActive
	}
	// The current directory wins when it (or an ancestor) is a vault — the
	// long-standing cwd behavior. Returning "." keeps the upward walk in
	// openResolvedVault's vault.Open, unchanged.
	if cwd, err := os.Getwd(); err == nil && vault.FindVaultRoot(cwd) != "" {
		return ".", sourceCwd
	}
	// Nothing local resolved: fall back to the vault Obsidian has open. Inert
	// under 2NB_TEST so the binary harness (e2e tests run with the developer's
	// real HOME) never resolves the live Obsidian vault into a test — the same
	// isolation guard setActiveVault/addRecentVault use.
	if os.Getenv("2NB_TEST") == "" {
		if dir := vault.ObsidianOpenVault(); dir != "" && vault.IsVaultRoot(dir) {
			// Rung 5 is the surprising one ("how did it find this vault?"); leave
			// a --verbose trace for support.
			slog.Debug("resolved vault from Obsidian open vault", "path", dir)
			return dir, sourceObsidian
		}
	}
	return ".", sourceCwd
}

// openResolvedVault opens the vault at dir, wrapping failure in the
// standard "no vault found" guidance with the resolution source named.
func openResolvedVault(dir string, source vaultSource) (*vault.Vault, error) {
	absDir, _ := filepath.Abs(dir)
	v, err := vault.Open(dir)
	if err != nil {
		return nil, vaultNotFoundError(absDir, source)
	}
	return v, nil
}

// vaultNotFoundError builds the actionable error shown when no vault could be
// resolved. Beyond the generic guidance it diagnoses the two states that leave
// a user stuck: a stale ~/.2ndbrain-active-vault pointer (named explicitly), and
// a recents list the caller can re-target with a paste-ready --vault flag.
func vaultNotFoundError(absDir string, source vaultSource) error {
	var b strings.Builder
	fmt.Fprintf(&b, "no vault found at %s (resolved from %s)", absDir, source)

	// A stale active pointer is the subtlest failure: the file exists and names
	// a path, but that path is no longer a vault, so resolution silently fell
	// through to the current directory. Name it so the fix is obvious.
	if source == sourceCwd {
		if raw := getActiveVault(); raw != "" && !vault.IsVaultRoot(raw) {
			fmt.Fprintf(&b, "\n\nThe active vault pointer (~/%s) points at %s,\n"+
				"which is no longer a vault (missing %s / .obsidian). Re-point it:\n"+
				"  2nb vault set /path/to/vault", activeVaultFile, raw, vault.DotDirName)
		}
	}

	// Live recents give a paste-ready fix without the user hunting for the path.
	if recents := listRecentVaults(); len(recents) > 0 {
		b.WriteString("\n\nRecent vaults — re-run with one of:")
		for _, p := range recents {
			fmt.Fprintf(&b, "\n  --vault %q", p)
		}
	}

	b.WriteString("\n\nTo fix:\n" +
		"  • Run from inside your vault directory\n" +
		"  • Use --vault /path/to/vault\n" +
		"  • Set 2NB_VAULT=/path/to/vault\n" +
		"  • Create a new vault with `2nb init /path/to/vault`")
	return errors.New(b.String())
}

// openVault resolves the vault via resolveVaultDir and opens it.
func openVault() (*vault.Vault, error) {
	dir, source := resolveVaultDir()
	return openResolvedVault(dir, source)
}

// openVaultAndSetActive opens the vault and, when the vault was resolved from
// the active pointer, the current directory, or the open Obsidian vault, records
// it as the active vault and in the recents list (so the first write command
// resolved via Obsidian's registry self-heals the pointer for later bare
// commands). An explicit --vault flag or
// 2NB_VAULT env var is a one-shot override: it must NOT repoint the shared
// ~/.2ndbrain-active-vault that the GUI, the Obsidian plugin's pinned
// calls, and a bare terminal all coordinate on.
// Use for write commands (init, create, index, delete). Read commands use openVault().
func openVaultAndSetActive() (*vault.Vault, error) {
	dir, source := resolveVaultDir()
	v, err := openResolvedVault(dir, source)
	if err != nil {
		return nil, err
	}
	if source == sourceActive || source == sourceCwd || source == sourceObsidian {
		canonical := canonicalVaultPath(v.Root)
		if err := setActiveVault(canonical); err != nil {
			// Best-effort: the command itself proceeds, but a failed pointer
			// write means the next bare `2nb` resolves a stale vault — leave
			// a trace for that investigation.
			slog.Warn("active-vault pointer write failed", "path", canonical, "error", err)
		}
		addRecentVault(canonical)
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

// ExitCode maps an error returned by Execute to a process exit code. An
// *ExitError carries its own code (ExitValidation=2, ExitNotFound=1,
// ExitStaleRef=3) so scripts can distinguish "bad input" from "not found"; any
// other non-nil error is a generic failure (1). nil → 0. main() calls this
// instead of a blanket os.Exit(1), which previously flattened every code to 1.
func ExitCode(err error) int {
	if err == nil {
		return ExitOK
	}
	var ee *ExitError
	if errors.As(err, &ee) {
		return ee.Code
	}
	return ExitNotFound // generic failure → 1
}

func preprocessArgs(args []string) []string {
	if len(args) <= 1 {
		return args
	}

	var newArgs []string
	newArgs = append(newArgs, args[0])

	var cmdName string
	var subCmdName string
	var hasColonCmd bool
	var colonParts []string

	var knownCommands = map[string]bool{
		"read": true, "create": true, "append": true, "prepend": true, "delete": true,
		"move": true, "rename": true, "daily": true, "unresolved": true, "orphans": true,
		"deadends": true, "outline": true, "aliases": true, "search": true, "task": true,
		"tasks": true, "tags": true, "tag": true, "links": true, "backlinks": true, "folders": true,
		"version": true, "help": true, "property": true, "properties": true, "link": true,
		"vault": true, "init": true, "index": true, "ai": true, "models": true, "polish": true,
		"suggest-links": true, "graph": true, "related": true, "stale": true, "wordcount": true,
		"mcp": true, "mcp-server": true, "mcp-setup": true, "plugin": true, "skills": true,
		"export-context": true, "git": true, "import-obsidian": true, "export-obsidian": true,
		"migrate": true, "completion": true, "config": true, "ask": true, "chat": true,
		// Obsidian-CLI compatibility verbs + aliases (see docs/obsidian-cli-mapping.md).
		"meta": true, "list": true, "files": true, "print": true, "frontmatter": true,
		"fm": true, "search-content": true, "list-vaults": true, "set-default-vault": true,
		"add-vault": true,
	}

	// freeTextCommands take an arbitrary free-text positional (a query / a
	// question) that must NEVER be parsed as a key=value parameter, or the user
	// silently loses any query that happens to contain "=" (a code snippet, a
	// "key=value" search, etc.). For these, only the obsidian "query=" convenience
	// plus the universal vault=/format= are honored; everything else passes
	// through verbatim as the positional.
	freeTextCommands := map[string]bool{"search": true, "ask": true, "chat": true, "search-content": true}

	isCommand := func(arg string) bool {
		if strings.Contains(arg, ":") {
			part := strings.SplitN(arg, ":", 2)[0]
			return knownCommands[part]
		}
		return knownCommands[arg]
	}

	// Find the command name by checking against known command roots
	var cmdIdx = -1
	for i := 1; i < len(args); i++ {
		arg := args[i]
		if strings.HasPrefix(arg, "-") || strings.Contains(arg, "=") {
			continue
		}
		if isCommand(arg) {
			cmdIdx = i
			break
		}
	}

	if cmdIdx != -1 {
		cmdName = args[cmdIdx]
		if strings.Contains(cmdName, ":") {
			hasColonCmd = true
			colonParts = strings.Split(cmdName, ":")
			cmdName = colonParts[0]
			if len(colonParts) > 1 {
				subCmdName = colonParts[1]
			}
		}
	}

	// Extract parameters
	var fileVal string
	var pathVal string
	var toVal string
	var contentVal string
	var nameVal string
	var valueVal string
	var queryVal string
	var formatVal string
	var doneFlag bool
	var todoFlag bool
	var toggleFlag bool
	var overwriteFlag bool
	var appendFlag bool
	var totalFlag bool
	var verboseFlag bool
	var refVal string
	var lineVal string
	var templateVal string
	var oldVal string
	var newVal string
	var tagVal string
	var resolveModeVal string // "exact" (path=) or "fuzzy" (file=); "" = auto

	var processed []string

	for i := 1; i < len(args); i++ {
		arg := args[i]
		if i == cmdIdx {
			continue // Handle this separately
		}

		if strings.HasPrefix(arg, "-") {
			// `add-vault --set-default` is a no-op: `vault create` already makes
			// the new vault active, so drop the flag rather than pass an unknown
			// flag to cobra.
			if arg == "--set-default" && cmdName == "add-vault" {
				continue
			}
			processed = append(processed, arg)
			continue
		}

		if strings.Contains(arg, "=") {
			parts := strings.SplitN(arg, "=", 2)
			k, v := parts[0], parts[1]
			// For free-text commands (search/ask/chat) only the query/vault/format
			// conveniences are parameters; anything else is part of the query and
			// must pass through verbatim (e.g. searching for "a=b"). For all
			// commands, an UNRECOGNIZED key is never silently dropped: it falls
			// through to `processed` as a literal positional, so a value that
			// merely contains "=" (a config value, a query) is preserved.
			isFreeText := freeTextCommands[cmdName]
			switch {
			case k == "vault":
				processed = append(processed, "--vault", v)
			case k == "format":
				formatVal = v
			case k == "query":
				queryVal = v
			case isFreeText:
				// Not a recognized param for a free-text command: it is query text.
				processed = append(processed, arg)
			case k == "file":
				// file= is the fuzzy resolver form (title/alias/shortest-unique suffix).
				fileVal = v
				resolveModeVal = resolveFuzzy
			case k == "path":
				// path= is the strict exact vault-relative form.
				pathVal = v
				resolveModeVal = resolveExact
			case k == "to":
				toVal = v
			case k == "content":
				contentVal = v
			case k == "name":
				nameVal = v
			case k == "value":
				valueVal = v
			case k == "ref":
				refVal = v
			case k == "line":
				lineVal = v
			case k == "template" && cmdName == "create":
				templateVal = v
			case k == "old" && cmdName == "tags":
				oldVal = v
			case k == "new" && cmdName == "tags":
				newVal = v
			case k == "tag" && cmdName == "tag":
				tagVal = v
			default:
				// Unknown key=value for a structured command: preserve it verbatim
				// rather than dropping it, so e.g. `config set ai.x a=b` survives.
				processed = append(processed, arg)
			}
			continue
		}

		// Bare flag-words (done/todo/toggle/overwrite/verbose) are only the
		// obsidian-style spelling of a flag for the commands that actually take
		// them. For a free-text command (search/ask/chat) every bare token is
		// part of the query and must pass through verbatim, or `2nb search
		// verbose` / `2nb search done` would silently lose the query word. Each
		// flag-word is also scoped to its owning command so an unrelated command
		// can't have a positional eaten.
		if freeTextCommands[cmdName] {
			processed = append(processed, arg)
			continue
		}
		switch {
		case arg == "done" && (cmdName == "task" || cmdName == "tasks"):
			doneFlag = true
		case arg == "todo" && (cmdName == "task" || cmdName == "tasks"):
			todoFlag = true
		case arg == "toggle" && cmdName == "task":
			toggleFlag = true
		case arg == "overwrite" && cmdName == "create":
			overwriteFlag = true
		case arg == "append" && cmdName == "create":
			appendFlag = true
		case arg == "total" && (cmdName == "list" || cmdName == "files" ||
			cmdName == "tasks" || cmdName == "unresolved"):
			totalFlag = true
		case arg == "verbose":
			// --verbose is a universal flag, so the bare obsidian spelling maps
			// for any structured (non-free-text) command.
			verboseFlag = true
		default:
			processed = append(processed, arg)
		}
	}

	// Command translation
	var finalCmd []string
	if cmdIdx != -1 {
		switch {
		case hasColonCmd:
			switch cmdName {
			case "daily":
				finalCmd = append(finalCmd, "daily")
				switch subCmdName {
				case "read", "append", "prepend", "path":
					finalCmd = append(finalCmd, subCmdName)
				}
			case "tags":
				finalCmd = append(finalCmd, "tags")
				if subCmdName == "rename" {
					finalCmd = append(finalCmd, "rename")
				}
			case "tag":
				finalCmd = append(finalCmd, "tag")
				if subCmdName == "add" || subCmdName == "remove" {
					finalCmd = append(finalCmd, subCmdName)
				}
			case "property":
				finalCmd = append(finalCmd, "meta")
			case "link":
				switch subCmdName {
				case "unresolved":
					finalCmd = append(finalCmd, "unresolved")
				case "orphans":
					finalCmd = append(finalCmd, "orphans")
				case "deadends":
					finalCmd = append(finalCmd, "deadends")
				}
			case "search":
				if subCmdName == "context" {
					finalCmd = append(finalCmd, "search")
				}
			default:
				finalCmd = append(finalCmd, colonParts...)
			}
		default:
			switch cmdName {
			case "version":
				finalCmd = append(finalCmd, "--version")
			case "unresolved":
				finalCmd = append(finalCmd, "unresolved")
			case "property":
				finalCmd = append(finalCmd, "meta")
			// Obsidian-CLI command aliases normalized to their canonical 2nb
			// command so the parameter handling below applies uniformly. The
			// cobra Aliases on read/meta/list cover direct (non-shim) use + help.
			case "print":
				finalCmd = append(finalCmd, "read")
			case "frontmatter", "fm", "properties":
				finalCmd = append(finalCmd, "meta")
			case "files":
				finalCmd = append(finalCmd, "list")
			case "search-content":
				finalCmd = append(finalCmd, "search")
			// Vault registry verbs map onto the `vault` subcommands.
			case "list-vaults":
				finalCmd = append(finalCmd, "vault", "list")
			case "set-default-vault":
				finalCmd = append(finalCmd, "vault", "set")
			case "add-vault":
				finalCmd = append(finalCmd, "vault", "create")
			default:
				finalCmd = append(finalCmd, cmdName)
			}
		}
	}

	newArgs = append(newArgs, finalCmd...)
	newArgs = append(newArgs, processed...)

	if formatVal != "" {
		newArgs = append(newArgs, "--format", formatVal)
	}
	if verboseFlag {
		newArgs = append(newArgs, "--verbose")
	}
	// path=/file= select the target-resolution mode (exact vs fuzzy); a bare
	// positional leaves it at auto.
	if resolveModeVal != "" {
		newArgs = append(newArgs, "--resolve", resolveModeVal)
	}
	if totalFlag {
		newArgs = append(newArgs, "--total")
	}
	// search-content is keyword/content search: force BM25-only.
	if cmdName == "search-content" {
		newArgs = append(newArgs, "--bm25-only")
	}

	targetPath := pathVal
	if targetPath == "" {
		targetPath = fileVal
	}

	primaryCmd := ""
	secondaryCmd := ""
	if len(finalCmd) > 0 {
		primaryCmd = finalCmd[0]
	}
	if len(finalCmd) > 1 {
		secondaryCmd = finalCmd[1]
	}

	switch primaryCmd {
	case "read":
		if targetPath != "" {
			newArgs = append(newArgs, targetPath)
		}
	case "create":
		if targetPath != "" {
			newArgs = append(newArgs, targetPath)
		}
		if contentVal != "" {
			newArgs = append(newArgs, "--content", contentVal)
		}
		if templateVal != "" {
			newArgs = append(newArgs, "--type", templateVal)
		}
		if overwriteFlag {
			newArgs = append(newArgs, "--overwrite")
		}
		if appendFlag {
			newArgs = append(newArgs, "--append")
		}
	case "append":
		if targetPath != "" {
			newArgs = append(newArgs, targetPath)
		}
		if contentVal != "" {
			newArgs = append(newArgs, "--text", contentVal)
		}
	case "prepend":
		if targetPath != "" {
			newArgs = append(newArgs, targetPath)
		}
		if contentVal != "" {
			newArgs = append(newArgs, "--text", contentVal)
		}
	case "delete":
		if targetPath != "" {
			newArgs = append(newArgs, targetPath)
		}
	case "move":
		if targetPath != "" {
			newArgs = append(newArgs, targetPath)
		}
		if toVal != "" {
			newArgs = append(newArgs, toVal)
		}
	case "rename":
		if targetPath != "" {
			newArgs = append(newArgs, targetPath)
		}
		if nameVal != "" {
			newArgs = append(newArgs, nameVal)
		}
	case "daily":
		switch secondaryCmd {
		case "append", "prepend":
			if contentVal != "" {
				newArgs = append(newArgs, "--text", contentVal)
			}
		}
	case "meta":
		if targetPath != "" {
			newArgs = append(newArgs, targetPath)
		}
		if hasColonCmd && cmdName == "property" {
			switch subCmdName {
			case "read":
				if nameVal != "" {
					newArgs = append(newArgs, "--get", nameVal)
				}
			case "set":
				if nameVal != "" {
					newArgs = append(newArgs, "--set", fmt.Sprintf("%s=%s", nameVal, valueVal))
				}
			case "remove":
				if nameVal != "" {
					newArgs = append(newArgs, "--remove", nameVal)
				}
			}
		}
	case "search":
		if queryVal != "" {
			newArgs = append(newArgs, queryVal)
		}
	case "task":
		resolvedPath := targetPath
		resolvedLine := lineVal
		if refVal != "" {
			parts := strings.SplitN(refVal, ":", 2)
			resolvedPath = parts[0]
			if len(parts) > 1 {
				resolvedLine = parts[1]
			}
		}
		if resolvedPath != "" {
			newArgs = append(newArgs, resolvedPath)
		}
		if resolvedLine != "" {
			newArgs = append(newArgs, resolvedLine)
		}
		if doneFlag {
			newArgs = append(newArgs, "--done")
		}
		if todoFlag {
			newArgs = append(newArgs, "--todo")
		}
		if toggleFlag {
			newArgs = append(newArgs, "--toggle")
		}
	case "tags":
		// tags:rename old=… new=… -> tags rename <old> <new>
		if secondaryCmd == "rename" {
			if oldVal != "" {
				newArgs = append(newArgs, oldVal)
			}
			if newVal != "" {
				newArgs = append(newArgs, newVal)
			}
		}
	case "tag":
		// tag:add/tag:remove file=… tag=a,b -> tag add|remove <note> a,b
		// (the command comma-splits the tag argument).
		if targetPath != "" {
			newArgs = append(newArgs, targetPath)
		}
		if tagVal != "" {
			newArgs = append(newArgs, tagVal)
		}
	case "vault":
		// add-vault/set-default-vault path=… (or a bare positional) -> the vault
		// path argument for `vault create`/`vault set`.
		if targetPath != "" {
			newArgs = append(newArgs, targetPath)
		}
	}

	return newArgs
}
