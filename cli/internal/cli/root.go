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
	flagFormat       string
	flagPorcelain    bool
	flagVault        string
	flagVerbose      bool
	flagUnconfigured bool
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
	rootCmd.PersistentFlags().StringVar(&flagVault, "vault", "", "Path to vault (default: the vault you have open in Obsidian, or 2NB_VAULT)")
	rootCmd.PersistentFlags().BoolVarP(&flagVerbose, "verbose", "v", false, "Enable verbose logging (debug level)")
	rootCmd.PersistentFlags().BoolVar(&flagUnconfigured, "unconfigured", false, "Permit a write to a vault Obsidian doesn't know (warns: the note won't appear in Obsidian or your 2nb index)")
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
	sourceObsidian vaultSource = "open Obsidian vault"
	sourceCwd      vaultSource = "current directory"
)

// resolveVaultDir is the single implementation of the vault-resolution
// ladder, shared by every command (including `vault status`/`show`):
//
//  1. --vault flag
//  2. 2NB_VAULT env var
//  3. The vault Obsidian currently has open (its own registry)
//  4. Current directory, when it is inside a vault
//
// Obsidian's registry is the AUTHORITATIVE source of "the active vault": 2nb
// follows the vault you have open in Obsidian, and the GUI binds to the same
// registry, so the dashboard, the plugin, and a bare terminal `2nb` all agree.
// There is deliberately no 2nb-managed active-vault pointer file — that pointer
// could drift (a stray write, a polluted recents list) and silently override
// Obsidian's truth, which is exactly the failure this design removes.
//
// The Obsidian rung is validated with vault.IsVaultRoot (the stored path must be
// a real vault root, .obsidian or .2ndbrain child) and is inert under 2NB_TEST,
// so the binary test harness (e2e tests run with the developer's real HOME)
// never binds the live Obsidian vault into a test. The cwd rung returns "." so
// openResolvedVault's vault.Open performs the upward walk; when nothing resolves
// that final "." → sourceCwd produces the actionable error.
func resolveVaultDir() (string, vaultSource) {
	if dir := expandPath(flagVault); dir != "" {
		return dir, sourceFlag
	}
	if dir := expandPath(os.Getenv("2NB_VAULT")); dir != "" {
		return dir, sourceEnv
	}
	// The vault Obsidian has open is the authoritative active vault. Inert under
	// 2NB_TEST so a binary test under the developer's real HOME can't bind the
	// live vault (the same isolation guard addRecentVault uses).
	if os.Getenv("2NB_TEST") == "" {
		if dir := vault.ObsidianOpenVault(); dir != "" && vault.IsVaultRoot(dir) {
			slog.Debug("resolved vault from Obsidian open vault", "path", dir)
			return dir, sourceObsidian
		}
	}
	// Otherwise fall back to the current directory: openResolvedVault's vault.Open
	// walks up from "." to find a vault root, or returns the actionable error.
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
// resolved. 2nb follows the vault Obsidian has open, so the first fix is to open
// one there; the live recents list gives a paste-ready --vault for everything else.
func vaultNotFoundError(absDir string, source vaultSource) error {
	var b strings.Builder
	fmt.Fprintf(&b, "no vault found at %s (resolved from %s)", absDir, source)

	// Live recents give a paste-ready fix without the user hunting for the path.
	if recents := listRecentVaults(); len(recents) > 0 {
		b.WriteString("\n\nRecent vaults — re-run with one of:")
		for _, p := range recents {
			fmt.Fprintf(&b, "\n  --vault %q", p)
		}
	}

	b.WriteString("\n\nTo fix:\n" +
		"  • Open a vault in Obsidian (2nb follows Obsidian's open vault)\n" +
		"  • Run from inside your vault directory\n" +
		"  • Use --vault /path/to/vault\n" +
		"  • Set 2NB_VAULT=/path/to/vault\n" +
		"  • Create a new vault with `2nb vault create /path/to/vault`")
	return errors.New(b.String())
}

// openVault resolves the vault via resolveVaultDir and opens it.
func openVault() (*vault.Vault, error) {
	dir, source := resolveVaultDir()
	return openResolvedVault(dir, source)
}

// openVaultAndSetActive is the WRITE opener and enforces the firm vault rule:
// a write goes to the vault you have open in Obsidian unless you deliberately
// override it, and 2nb never silently guesses a vault by walking up from the
// current directory (the failure mode that splits notes across vaults). Read
// commands use openVault(), which stays permissive.
//
// Resolution, in order:
//  1. --vault / 2NB_VAULT — an explicit, one-shot override (the GUI and plugin
//     pin --vault on every call). Honored; a target that isn't your configured
//     Obsidian vault gets a notice, and an unconfigured target is refused unless
//     --unconfigured (or 2NB_UNCONFIGURED) acknowledges it.
//  2. The vault Obsidian has open (or, if closed, its most-recent) — the default,
//     no flags. Inert under 2NB_TEST so the binary harness can't bind the live
//     vault. The cwd is never consulted here.
//  3. Only when there is no override and no Obsidian vault at all: the current
//     directory, but ONLY if you are standing in the vault root. A cwd that would
//     resolve by walking up to a parent vault is refused, not silently used.
//
// An explicit --vault / 2NB_VAULT is a one-shot override and is NOT recorded in
// recents; the Obsidian and cwd defaults are.
func openVaultAndSetActive() (*vault.Vault, error) {
	if dir := expandPath(flagVault); dir != "" {
		return openWriteTarget(dir, sourceFlag)
	}
	if dir := expandPath(os.Getenv("2NB_VAULT")); dir != "" {
		return openWriteTarget(dir, sourceEnv)
	}

	// Default: the vault Obsidian has open is authoritative for writes. Never the
	// cwd. Inert under 2NB_TEST (the same isolation guard resolveVaultDir uses).
	if os.Getenv("2NB_TEST") == "" {
		if active, wasOpen := vault.ObsidianActiveVault(); active != "" && vault.IsVaultRoot(active) {
			v, err := openResolvedWriteVault(active, sourceObsidian)
			if err != nil {
				return nil, err
			}
			if wasOpen {
				noteWrite("writing to %s (open Obsidian vault)", v.Root)
			} else {
				// Suppressible on a non-TTY consumer, so also log it: "why did my
				// write go to this vault?" must be answerable from cli.log.
				slog.Info("Obsidian not open; writing to most-recent vault", "path", v.Root)
				noteWrite("Obsidian isn't open — writing to your most-recent vault %s (pass --vault to be sure)", v.Root)
			}
			addRecentVault(canonicalVaultPath(v.Root))
			return v, nil
		} else if active != "" {
			slog.Debug("Obsidian registry vault is not a valid vault root; falling back to cwd", "path", active)
		}
	}

	// No override and no Obsidian vault. Use the cwd only when we are standing IN
	// a vault root; never silently walk up to a parent vault.
	cwd, _ := filepath.Abs(".")
	root := vault.FindVaultRoot(cwd)
	if root == "" {
		return nil, vaultNotFoundError(cwd, sourceCwd)
	}
	if canonicalVaultPath(root) != canonicalVaultPath(cwd) {
		slog.Warn("refusing a walked-up write (cwd is not a vault root)", "cwd", cwd, "found_vault", root)
		return nil, walkUpRefusedError(cwd, root)
	}
	v, err := openResolvedWriteVault(root, sourceCwd)
	if err != nil {
		return nil, err
	}
	noteWrite("writing to %s (current directory)", v.Root)
	addRecentVault(canonicalVaultPath(v.Root))
	return v, nil
}

// openResolvedWriteVault opens a vault root that FindVaultRoot/IsVaultRoot has
// already vetted for a write, surfacing a genuine open failure (corrupt index,
// permissions, a sidecar-create error) wrapped with %w instead of masking it as
// the misleading "no vault found" guidance. It also logs the resolved target so
// a write's destination is recoverable from cli.log even when the TTY notice is
// suppressed (a piped/MCP consumer).
func openResolvedWriteVault(root string, source vaultSource) (*vault.Vault, error) {
	v, err := vault.Open(root)
	if err != nil {
		return nil, fmt.Errorf("open vault %s (resolved from %s): %w", root, source, err)
	}
	slog.Debug("write vault resolved", "path", v.Root, "source", string(source))
	return v, nil
}

// openWriteTarget honors an explicit --vault / 2NB_VAULT target while keeping the
// firm rule's transparency: a target that matches your configured Obsidian vault
// (or when no Obsidian vault is configured at all) is silent; another real
// Obsidian vault gets a one-line notice; an unconfigured target (a stray 2nb-only
// vault Obsidian doesn't know) is refused unless --unconfigured acknowledges that
// the note won't appear in Obsidian or your main 2nb index.
func openWriteTarget(explicitDir string, source vaultSource) (*vault.Vault, error) {
	absDir, _ := filepath.Abs(explicitDir)
	root := vault.FindVaultRoot(absDir)
	if root == "" {
		// Nothing to write to (not a vault, no ancestor vault). Don't mint one
		// here — point at `vault create` via the standard guidance.
		return nil, vaultNotFoundError(absDir, source)
	}
	canon := canonicalVaultPath(root)
	configured := obsidianConfiguredCanonical() // "" under 2NB_TEST or no registry

	switch {
	case configured == "" || canon == configured:
		// The configured Obsidian vault, or no Obsidian vault configured: honor.
		return openAndAnnounce(root, source, "")
	case isObsidianVault(root) || obsidianKnownCanonical()[canon]:
		// A different but real / Obsidian-known vault: proceed with a notice.
		return openAndAnnounce(root, source,
			fmt.Sprintf("%s is not your current Obsidian vault (%s)", root, configured))
	default:
		// Unconfigured: a stray 2nb-only vault Obsidian doesn't know.
		if !unconfiguredAllowed() {
			slog.Warn("refusing a write to an unconfigured vault", "target", root, "configured_obsidian_vault", configured)
			return nil, unconfiguredVaultError(root, configured)
		}
		// Acknowledged via --unconfigured / 2NB_UNCONFIGURED. The note won't appear
		// in Obsidian or the main 2nb index — log it (the TTY warning is suppressed
		// on the MCP/captured path this can be reached from).
		slog.Warn("writing to an unconfigured vault (acknowledged) — note will not appear in Obsidian or the 2nb index", "target", root)
		return openAndAnnounce(root, source,
			fmt.Sprintf("WARNING: %s is an unconfigured vault — this note won't appear in Obsidian or your main 2nb index", root))
	}
}

// openAndAnnounce opens an explicit write target (one-shot — not recorded in
// recents) and prints an optional notice plus the resolved write target.
func openAndAnnounce(root string, source vaultSource, notice string) (*vault.Vault, error) {
	v, err := openResolvedWriteVault(root, source)
	if err != nil {
		return nil, err
	}
	if notice != "" {
		noteWrite("%s", notice)
	}
	noteWrite("writing to %s (%s)", v.Root, source)
	return v, nil
}

// noteWrite prints an informational write-target line to stderr for an
// interactive human. It is suppressed under --porcelain and whenever stderr is
// not a terminal (piped/captured) — so a machine consumer that merges stderr
// into stdout (the e2e harness, the macOS app, the plugin, a Warp tool capture)
// never sees it corrupt a --json payload. The firm rule's correctness and its
// hard refusals (returned errors) do not depend on this line.
func noteWrite(format string, args ...any) {
	if flagPorcelain || !stderrIsTTY() {
		return
	}
	fmt.Fprintf(os.Stderr, "2nb: "+format+"\n", args...)
}

// stderrIsTTY reports whether stderr is an interactive terminal (a character
// device), using only the standard library.
func stderrIsTTY() bool {
	fi, err := os.Stderr.Stat()
	return err == nil && (fi.Mode()&os.ModeCharDevice) != 0
}

// unconfiguredAllowed reports whether a write to an unconfigured vault has been
// acknowledged, via the --unconfigured flag (CLI) or 2NB_UNCONFIGURED (the MCP
// server has no flags).
func unconfiguredAllowed() bool {
	return flagUnconfigured || os.Getenv("2NB_UNCONFIGURED") != ""
}

// obsidianConfiguredCanonical returns the canonical path of the vault Obsidian
// has open (or most-recent), or "" under 2NB_TEST or when no registry resolves.
func obsidianConfiguredCanonical() string {
	if os.Getenv("2NB_TEST") != "" {
		return ""
	}
	active, _ := vault.ObsidianActiveVault()
	if active == "" || !vault.IsVaultRoot(active) {
		return ""
	}
	return canonicalVaultPath(active)
}

// obsidianKnownCanonical returns the set of canonical paths of every vault
// Obsidian knows (open + recents). Empty under 2NB_TEST or with no registry.
func obsidianKnownCanonical() map[string]bool {
	out := map[string]bool{}
	if os.Getenv("2NB_TEST") != "" {
		return out
	}
	for _, p := range vault.ObsidianKnownVaults() {
		if c := canonicalVaultPath(p); c != "" {
			out[c] = true
		}
	}
	return out
}

// isObsidianVault reports whether root has an .obsidian/ child — i.e. it is a
// real Obsidian vault (notes written there will show up in Obsidian), as opposed
// to a 2nb-only directory.
func isObsidianVault(root string) bool {
	_, err := os.Stat(filepath.Join(root, ".obsidian"))
	return err == nil
}

// walkUpRefusedError is returned when a write would resolve a vault only by
// walking up from a non-vault current directory — the split trap.
func walkUpRefusedError(cwd, foundRoot string) error {
	var b strings.Builder
	fmt.Fprintf(&b, "refusing to write: %s is not a vault", cwd)
	fmt.Fprintf(&b, "\n\n2nb found a vault by walking up the directory tree (%s), but writing there\n"+
		"could split your notes across vaults. By default 2nb writes to the vault you\n"+
		"have open in Obsidian — nothing was written.", foundRoot)
	b.WriteString("\n\nTo fix:\n" +
		"  • Open your vault in Obsidian (2nb follows Obsidian's open vault)\n" +
		fmt.Sprintf("  • Or target it explicitly: --vault %q\n", foundRoot) +
		"  • Or cd into the vault root")
	return errors.New(b.String())
}

// unconfiguredVaultError is returned when an explicit --vault / 2NB_VAULT points
// at a vault Obsidian doesn't know, without --unconfigured to acknowledge it.
func unconfiguredVaultError(root, configured string) error {
	var b strings.Builder
	fmt.Fprintf(&b, "refusing to write to %s — it is not your configured Obsidian vault", root)
	if configured != "" {
		fmt.Fprintf(&b, " (%s)", configured)
	}
	b.WriteString(".\nA note written there won't appear in Obsidian and won't be in your main 2nb index.")
	b.WriteString("\n\nIf that's really what you want, re-run with --unconfigured.")
	return errors.New(b.String())
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

// exactArgsHint enforces exactly n positional args (like cobra.ExactArgs) but on
// a count mismatch returns an ExitValidation error whose message is produced by
// hint — a copy-pasteable pointer at the correct form, instead of cobra's terse
// "accepts N arg(s), received M". SilenceUsage suppresses the flag dump, so the
// hint is the whole message the user sees. Reuse for any command that wants a
// self-correcting error (the repo had no such helper before this).
func exactArgsHint(n int, hint func(args []string) string) cobra.PositionalArgs {
	return func(_ *cobra.Command, args []string) error {
		if len(args) == n {
			return nil
		}
		return exitWithError(ExitValidation, hint(args))
	}
}

// knownCommands is the set of command roots (plus obsidian-CLI compatibility
// verbs/aliases) that preprocessArgs recognizes when locating the command token
// in an argv that may be preceded by global flags. See docs/obsidian-cli-mapping.md.
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

// isKnownCommand reports whether arg is a known command root, tolerating the
// obsidian colon form (e.g. "daily:read" -> "daily").
func isKnownCommand(arg string) bool {
	if strings.Contains(arg, ":") {
		return knownCommands[strings.SplitN(arg, ":", 2)[0]]
	}
	return knownCommands[arg]
}

// commandIndex returns the index of the command token in args (argv with the
// program name at index 0), skipping leading global flags and any non-command
// barewords (e.g. a --vault <path> value). Returns -1 if none is found. Shared by
// preprocessArgs and rewriteStaleMetaArgs so both locate the command identically.
func commandIndex(args []string) int {
	for i := 1; i < len(args); i++ {
		arg := args[i]
		if strings.HasPrefix(arg, "-") || strings.Contains(arg, "=") {
			continue
		}
		if isKnownCommand(arg) {
			return i
		}
	}
	return -1
}

// rewriteStaleMetaArgs recognizes the obsolete positional `meta` subcommand form
// (which predates the current flag form) and rewrites it to the flag form,
// preserving any leading global flags (e.g. --vault <path>, which agents/MCP
// always pass) and any trailing arguments (e.g. --json):
//
//	meta set <path> <key> <value> [tail...] -> meta <path> --set <key>=<value> [tail...]
//	meta get <path> <key>         [tail...] -> meta <path> --get <key>         [tail...]
//	meta remove <path> <key>      [tail...] -> meta <path> --remove <key>      [tail...]
//
// It fires only at the exact operand count for each verb, so `meta set` alone
// (viewing a note literally named "set") is left untouched and shorter malformed
// forms fall through to metaArgsHint. The bare <path> resolves via the default
// auto mode (exact-on-disk then fuzzy), which is correct for a title-ish argument.
func rewriteStaleMetaArgs(args []string) ([]string, bool) {
	i := commandIndex(args)
	if i == -1 || args[i] != "meta" {
		return nil, false
	}
	rest := args[i+1:] // tokens after `meta`
	if len(rest) == 0 {
		return nil, false
	}

	var flag, val string
	var consumed int // operand tokens consumed after the verb
	switch rest[0] {
	case "set":
		if len(rest) < 4 { // verb + <path> <key> <value>
			return nil, false
		}
		flag, val, consumed = "--set", rest[2]+"="+rest[3], 3
	case "get":
		if len(rest) < 3 { // verb + <path> <key>
			return nil, false
		}
		flag, val, consumed = "--get", rest[2], 2
	case "remove":
		if len(rest) < 3 {
			return nil, false
		}
		flag, val, consumed = "--remove", rest[2], 2
	default:
		return nil, false
	}

	out := make([]string, 0, len(args))
	out = append(out, args[:i+1]...) // program, leading flags, and "meta"
	out = append(out, rest[1], flag, val)
	out = append(out, rest[1+consumed:]...) // preserve any trailing flags
	return out, true
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

	// Back-compat recovery: the pre-flag positional form `meta set/get/remove
	// <path> ...` predates the current flag form. Rewrite the unambiguous shapes
	// to the flag form so a stale agent/config invocation still works. Anything
	// that doesn't match falls through to metaCmd's Args validator, which prints
	// a flag-form hint (see metaArgsHint in meta.go). See docs/obsidian-cli-mapping.md.
	if rewritten, ok := rewriteStaleMetaArgs(args); ok {
		return rewritten
	}

	var newArgs []string
	newArgs = append(newArgs, args[0])

	var cmdName string
	var subCmdName string
	var hasColonCmd bool
	var colonParts []string

	// freeTextCommands take an arbitrary free-text positional (a query / a
	// question) that must NEVER be parsed as a key=value parameter, or the user
	// silently loses any query that happens to contain "=" (a code snippet, a
	// "key=value" search, etc.). For these, only the obsidian "query=" convenience
	// plus the universal vault=/format= are honored; everything else passes
	// through verbatim as the positional.
	freeTextCommands := map[string]bool{"search": true, "ask": true, "chat": true, "search-content": true}

	// Find the command token (skips leading global flags and non-command barewords).
	cmdIdx := commandIndex(args)

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
