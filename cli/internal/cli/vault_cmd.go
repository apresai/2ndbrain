package cli

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/apresai/2ndbrain/internal/ai"
	"github.com/apresai/2ndbrain/internal/output"
	"github.com/apresai/2ndbrain/internal/vault"
	"github.com/spf13/cobra"
)

var vaultCmd = &cobra.Command{
	Use:   "vault",
	Short: "Manage vaults — status, create, switch, list",
	Long: `Manage your 2ndbrain vaults.

With no subcommand, shows a health report for the active vault (same as
` + "`" + `vault status` + "`" + `). The active vault is whichever vault you have open in
Obsidian — 2nb follows Obsidian's open vault. With a path argument (legacy
form), registers that path in recents.`,
	Example: `  2nb vault                        Health report for the active vault
  2nb vault show                   Terse one-line-per-field summary
  2nb vault create ~/my-vault      Create a new vault
  2nb vault set ~/my-vault         Register a vault in recents
  2nb vault list                   List recently used vaults`,
	Args: cobra.MaximumNArgs(1),
	RunE: runVaultDefault,
}

var vaultStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show a health report for the active vault",
	Long: `Shows a unified health report: vault info, index coverage, AI provider
reachability, portability status, and stale document count. Mirrors the
Vault Status panel in the macOS editor.`,
	RunE: runVaultStatus,
}

var vaultShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Show a terse summary of the active vault",
	Long:  "Prints the active vault path, how it was resolved, the vault name, and the document count. Useful for scripting via --json.",
	RunE:  runVaultShow,
}

var vaultCreateCmd = &cobra.Command{
	Use:   "create <path>",
	Short: "Initialize a new vault",
	Long: `Create a new 2ndbrain vault at the given path. Initializes the
.2ndbrain/ directory with schemas and index, writes a .gitignore covering
personal/local state, and records the vault in recents. To use it, open the
folder as a vault in Obsidian (2nb follows your open Obsidian vault) or pass
--vault.`,
	Example: `  2nb vault create ~/my-vault      Create ~/my-vault
  2nb vault create .               Initialize the current directory`,
	Args: cobra.ExactArgs(1),
	ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) > 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		return nil, cobra.ShellCompDirectiveFilterDirs
	},
	RunE: runVaultCreate,
}

var vaultSetCmd = &cobra.Command{
	Use:   "set <path>",
	Short: "Register an existing vault in recents",
	Long: `Records an existing vault in the recents list (for ` + "`" + `vault list` + "`" + `). 2nb's
active vault follows the vault you have open in Obsidian, so this does not
"switch" the active vault — open the vault in Obsidian, or pass --vault, to use
it. Fails if the path is not a 2ndbrain vault.`,
	Example: `  2nb vault set ~/work-notes       Register ~/work-notes in recents
  2nb vault set .                  Register the current directory`,
	Args:              cobra.ExactArgs(1),
	ValidArgsFunction: completeVaultPaths,
	RunE:              runVaultSet,
}

var vaultListCmd = &cobra.Command{
	Use:   "list",
	Short: "List recently used vaults",
	Long:  "Shows vaults that have been created or switched to recently. Stale paths are pruned. The active vault is marked with *.",
	Example: `  2nb vault list
  2nb vault list --json`,
	RunE: runVaultList,
}

var vaultCheckpointCmd = &cobra.Command{
	Use:   "checkpoint",
	Short: "Collapse and truncate the index write-ahead log (WAL)",
	Long: `Runs a SQLite WAL checkpoint (PASSIVE then TRUNCATE) on the vault index:
writes the .2ndbrain/index.db-wal contents back into index.db and truncates the
-wal file.

Use this when index.db-wal has grown large. SQLite's auto-checkpoint flushes
WAL frames but never truncates the file, so a busy vault's -wal can park at its
high-water mark (we've seen 4MB against a 372KB database). This is safe to run
while the macOS app or an MCP server is connected: if a reader is active the
truncate is skipped (reported as busy) rather than forced, so it never blocks
or corrupts. Reports the WAL size before and after.`,
	Example: `  2nb vault checkpoint
  2nb vault checkpoint --json`,
	RunE: runVaultCheckpoint,
}

func init() {
	vaultCmd.AddCommand(vaultStatusCmd)
	vaultCmd.AddCommand(vaultShowCmd)
	vaultCmd.AddCommand(vaultCreateCmd)
	vaultCmd.AddCommand(vaultSetCmd)
	vaultCmd.AddCommand(vaultListCmd)
	vaultCmd.AddCommand(vaultCheckpointCmd)
	vaultCmd.GroupID = "start"
	rootCmd.AddCommand(vaultCmd)
}

func runVaultCheckpoint(cmd *cobra.Command, _ []string) error {
	v, err := openVault()
	if err != nil {
		return err
	}
	defer v.Close()

	res, err := v.DB.Checkpoint()
	if err != nil {
		return fmt.Errorf("checkpoint: %w", err)
	}

	if format := getFormat(cmd); format != "" {
		return output.Write(os.Stdout, format, res)
	}

	fmt.Printf("WAL %s → %s", humanBytes(res.WALBytesBefore), humanBytes(res.WALBytesAfter))
	if reclaimed := res.WALBytesBefore - res.WALBytesAfter; reclaimed > 0 {
		fmt.Printf(" (reclaimed %s)", humanBytes(reclaimed))
	}
	fmt.Println()
	fmt.Printf("Checkpointed %d of %d WAL frames into a %s database.\n",
		res.PagesCheckpointed, res.PagesTotal, humanBytes(res.DBBytes))
	if res.Busy {
		fmt.Println("Note: a reader was active, so the WAL could not be fully truncated. Re-run when the app / MCP server is idle.")
	}
	return nil
}

// humanBytes formats a byte count as B/KB/MB with one decimal place.
func humanBytes(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1fMB", float64(n)/float64(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1fKB", float64(n)/float64(1<<10))
	default:
		return fmt.Sprintf("%dB", n)
	}
}

type VaultInfo struct {
	Path      string `json:"path"`
	Source    string `json:"source"`
	Name      string `json:"name"`
	Documents int    `json:"documents"`
}

type VaultListEntry struct {
	Path      string `json:"path"`
	Name      string `json:"name"`
	Documents int    `json:"documents"`
	Active    bool   `json:"active"`
}

// VaultStatus is the JSON shape returned by `2nb vault status --json`.
// Mirrors the information shown in the macOS editor's Vault Status panel
// so the same fields are usable by automation. Human-readable hints like
// the "Next:" line are rendered only in terminal output — JSON consumers
// derive their own recommendations from the machine-readable fields
// (PortabilityStatus, PortabilityAction, documents / embedded counts,
// AIProvider).
type VaultStatus struct {
	Path                string   `json:"path"`
	Name                string   `json:"name"`
	Source              string   `json:"source"`
	Documents           int      `json:"documents"`
	EmbeddedDocuments   int      `json:"embedded_documents"`
	EmbeddableDocuments int      `json:"embeddable_documents"` // docs with content (excludes empty notes); the embedded denominator
	StaleDocuments      int      `json:"stale_documents"`
	StaleSinceDays      int      `json:"stale_since_days"`
	AIProvider          string   `json:"ai_provider"`
	EmbeddingModel      string   `json:"embedding_model"`
	GenerationModel     string   `json:"generation_model"`
	EmbedAvailable      bool     `json:"embed_available"`
	GenAvailable        bool     `json:"gen_available"`
	PortabilityStatus   string   `json:"portability_status"`
	PortabilityAction   string   `json:"portability_action"`
	VaultEmbeddingDim   int      `json:"vault_embedding_dim"`
	EmbeddingModels     []string `json:"vault_embedding_models"`
}

// runVaultDefault handles `2nb vault` (no subcommand). With no args it
// behaves like `vault status`; with a positional path it behaves like
// `vault set <path>` to preserve the pre-subcommand muscle memory.
func runVaultDefault(cmd *cobra.Command, args []string) error {
	if len(args) == 1 {
		return runVaultSet(cmd, args)
	}
	return runVaultStatus(cmd, args)
}

func runVaultShow(cmd *cobra.Command, _ []string) error {
	dir, source := resolveVaultDir()

	v, err := openResolvedVault(dir, source)
	if err != nil {
		return err
	}
	defer v.Close()

	var docCount int
	v.DB.Conn().QueryRow("SELECT COUNT(*) FROM documents").Scan(&docCount)

	info := VaultInfo{
		Path:      v.Root,
		Source:    string(source),
		Name:      v.Config.Name,
		Documents: docCount,
	}

	format := getFormat(cmd)
	if format != "" {
		return output.Write(os.Stdout, format, info)
	}

	fmt.Printf("Active vault:  %s\n", info.Path)
	fmt.Printf("Source:        %s\n", info.Source)
	fmt.Printf("Name:          %s\n", info.Name)
	fmt.Printf("Documents:     %d\n", info.Documents)
	return nil
}

func runVaultStatus(cmd *cobra.Command, _ []string) error {
	dir, source := resolveVaultDir()

	v, err := openResolvedVault(dir, source)
	if err != nil {
		return err
	}
	defer v.Close()

	ctx := context.Background()
	cfg := v.Config.AI
	initAIProviders(v)

	const staleSinceDays = 90
	docCount, embeddedCount, embeddableUnembedded, errCounts := v.DB.EmbeddingCounts()
	if errCounts != nil {
		slog.Warn("embedding counts query failed", "err", errCounts)
	}
	var staleCount int
	staleCutoff := time.Now().AddDate(0, 0, -staleSinceDays).UTC().Format(time.RFC3339)
	// Exclude empty modified_at (e.g. a doc whose `modified` frontmatter was
	// removed): in SQLite '' < <cutoff> is true, so without this guard such a
	// doc would falsely count as stale here while `2nb stale` (which has the
	// same guard) excludes it, making the two diverge.
	if err := v.DB.Conn().QueryRow("SELECT COUNT(*) FROM documents WHERE modified_at < ? AND modified_at != ''", staleCutoff).Scan(&staleCount); err != nil {
		slog.Warn("vault status: stale-doc count query failed", "err", err)
	}

	vaultDim, _ := v.DB.SampleEmbeddingDim()
	vaultModels, _ := v.DB.DistinctEmbeddingModels()

	var embedder ai.EmbeddingProvider
	var generator ai.GenerationProvider
	if cfg.Provider != "" {
		embedder, _ = ai.DefaultRegistry.Embedder(cfg.Provider)
		generator, _ = ai.DefaultRegistry.Generator(cfg.Provider)
	}

	// Provider reachability probes can each block 100-500ms (Bedrock
	// STS, Ollama daemon ping). Run them concurrently so the default
	// `2nb vault` action doesn't pay the sum of both latencies.
	var embedAvail, genAvail bool
	var wg sync.WaitGroup
	if embedder != nil {
		wg.Add(1)
		go func() { defer wg.Done(); embedAvail = embedder.Available(ctx) }()
	}
	if generator != nil {
		wg.Add(1)
		go func() { defer wg.Done(); genAvail = generator.Available(ctx) }()
	}
	wg.Wait()

	portStatus, portAction := derivePortability(ctx, cfg, embedder, vaultDim, vaultModels, docCount, embeddedCount, embeddableUnembedded)

	status := VaultStatus{
		Path:                v.Root,
		Name:                v.Config.Name,
		Source:              string(source),
		Documents:           docCount,
		EmbeddedDocuments:   embeddedCount,
		EmbeddableDocuments: embeddedCount + embeddableUnembedded,
		StaleDocuments:      staleCount,
		StaleSinceDays:      staleSinceDays,
		AIProvider:          cfg.Provider,
		EmbeddingModel:      cfg.EmbeddingModel,
		GenerationModel:     cfg.GenerationModel,
		EmbedAvailable:      embedAvail,
		GenAvailable:        genAvail,
		PortabilityStatus:   portStatus,
		PortabilityAction:   portAction,
		VaultEmbeddingDim:   vaultDim,
		EmbeddingModels:     vaultModels,
	}

	format := getFormat(cmd)
	if format != "" {
		return output.Write(os.Stdout, format, status)
	}

	label, hint := nextStepHint(docCount, embeddedCount, embeddedCount+embeddableUnembedded, cfg.Provider)
	printVaultStatus(status, label, hint)
	return nil
}

// printVaultStatus renders the terminal-only status report. Section
// headings mirror the macOS Vault Status panel so vocabulary stays
// consistent across CLI and GUI.
func printVaultStatus(s VaultStatus, nextLabel, nextHint string) {
	fmt.Println("Vault")
	fmt.Printf("  Name:       %s\n", orDash(s.Name))
	fmt.Printf("  Path:       %s\n", s.Path)
	fmt.Printf("  Source:     %s\n", s.Source)
	fmt.Printf("  Documents:  %d\n", s.Documents)

	fmt.Println()
	fmt.Println("Index & Embeddings")
	// Denominator is embeddable docs (content-bearing); empty notes are excluded
	// so coverage reads cleanly. The raw file count is shown under Documents above.
	fmt.Printf("  Embedded:   %d / %d\n", s.EmbeddedDocuments, s.EmbeddableDocuments)
	if s.VaultEmbeddingDim > 0 {
		model := "(no model recorded)"
		if len(s.EmbeddingModels) == 1 {
			model = s.EmbeddingModels[0]
		} else if len(s.EmbeddingModels) > 1 {
			model = "mixed: " + strings.Join(s.EmbeddingModels, ", ")
		}
		fmt.Printf("  As-embedded: %s (%dd)\n", model, s.VaultEmbeddingDim)
	}
	portLabel := strings.ToUpper(strings.ReplaceAll(s.PortabilityStatus, "_", " "))
	fmt.Printf("  Portability: %s\n", portLabel)
	if s.PortabilityAction != "" {
		fmt.Printf("    → %s\n", s.PortabilityAction)
	}

	fmt.Println()
	fmt.Println("AI Provider")
	if s.AIProvider == "" {
		fmt.Println("  (not configured — run `2nb ai setup`)")
	} else {
		fmt.Printf("  Provider:   %s\n", s.AIProvider)
		fmt.Printf("  Embedding:  %s  [%s]\n", orDash(s.EmbeddingModel), reachDot(s.EmbedAvailable))
		fmt.Printf("  Generation: %s  [%s]\n", orDash(s.GenerationModel), reachDot(s.GenAvailable))
	}

	fmt.Println()
	fmt.Println("Stale Documents")
	switch {
	case s.Documents == 0:
		fmt.Println("  (no documents yet)")
	case s.StaleDocuments == 0:
		fmt.Printf("  None older than %d days.\n", s.StaleSinceDays)
	default:
		fmt.Printf("  %d not modified in the last %d days.  (list with: 2nb stale --since %d)\n", s.StaleDocuments, s.StaleSinceDays, s.StaleSinceDays)
	}

	if nextHint != "" {
		fmt.Println()
		fmt.Printf("%s: %s\n", nextLabel, nextHint)
	}
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

func reachDot(ok bool) string {
	if ok {
		return "reachable"
	}
	return "unavailable"
}

func runVaultCreate(cmd *cobra.Command, args []string) error {
	return createVaultAt(cmd, args[0])
}

func runVaultSet(cmd *cobra.Command, args []string) error {
	absPath, err := filepath.Abs(expandPath(args[0]))
	if err != nil {
		return fmt.Errorf("resolve path: %w", err)
	}
	absPath = canonicalVaultPath(absPath)

	v, err := vault.Open(absPath)
	if err != nil {
		return fmt.Errorf("not a vault: %w\n\nCreate one with: 2nb vault create %s", err, args[0])
	}
	v.Close()

	addRecentVault(absPath)

	if !flagPorcelain {
		fmt.Printf("Registered %s in recent vaults.\n\n", absPath)
		fmt.Printf("2nb follows the vault you have open in Obsidian. To make this the\n")
		fmt.Printf("active vault, open it in Obsidian; or run commands with --vault %s\n", absPath)
	}
	return nil
}

func runVaultList(cmd *cobra.Command, _ []string) error {
	paths := listRecentVaults()
	// The active vault is whatever resolveVaultDir picks — the vault Obsidian
	// has open (or the cwd vault) — NOT a stored pointer. FindVaultRoot walks up
	// so running from a subdirectory of a vault still marks that vault's row,
	// matching how every other command resolves via vault.Open's walk-up.
	activeDir, _ := resolveVaultDir()
	absActive, _ := filepath.Abs(activeDir)
	active := canonicalVaultPath(vault.FindVaultRoot(absActive))

	// The active vault can be missing from recents (e.g. the recents file
	// was trimmed or written by an older version). Synthesize a row for it
	// so the `*` marker always appears, as this command's help promises.
	inRecents := false
	for _, p := range paths {
		if canonicalVaultPath(p) == active {
			inRecents = true
			break
		}
	}
	if !inRecents && active != "" && vault.IsVaultRoot(active) {
		paths = append([]string{active}, paths...)
	}

	entries := make([]VaultListEntry, 0, len(paths))
	for _, p := range paths {
		e := VaultListEntry{Path: p, Active: canonicalVaultPath(p) == active}
		if v, err := vault.Open(p); err == nil {
			e.Name = v.Config.Name
			v.DB.Conn().QueryRow("SELECT COUNT(*) FROM documents").Scan(&e.Documents)
			v.Close()
		}
		entries = append(entries, e)
	}

	format := getFormat(cmd)
	if format != "" {
		return output.Write(os.Stdout, format, entries)
	}

	if len(entries) == 0 {
		fmt.Println("No vaults recorded yet.")
		if !flagPorcelain {
			fmt.Println("\nCreate one with: 2nb vault create <path>")
		}
		return nil
	}

	for _, e := range entries {
		marker := " "
		if e.Active {
			marker = "*"
		}
		name := e.Name
		if name == "" {
			name = filepath.Base(e.Path)
		}
		fmt.Printf("%s %-30s %4d docs  %s\n", marker, name, e.Documents, e.Path)
	}
	if !flagPorcelain {
		fmt.Println("\n* = active vault (the vault Obsidian has open)  •  switch by opening another vault in Obsidian")
	}
	return nil
}

// createVaultAt is the shared implementation used by `vault create` and the
// deprecated `init` alias. Initializes a vault at path, writes the vault-root
// .gitignore, records it in recents, and prints next-step hints. It does NOT
// make the vault "active" — 2nb follows the vault Obsidian has open, so the
// user opens the new folder as a vault in Obsidian (or passes --vault) to use it.
func createVaultAt(cmd *cobra.Command, path string) error {
	expanded := expandPath(path)
	v, err := vault.Init(expanded)
	if err != nil {
		if errors.Is(err, vault.ErrAlreadyInit) {
			return fmt.Errorf("vault already initialized at %s", expanded)
		}
		return fmt.Errorf("init vault: %w", err)
	}
	defer v.Close()
	setupFileLogging(v)

	absPath, _ := filepath.Abs(v.Root)
	if absPath != "" {
		addRecentVault(absPath)
	}

	writeVaultGitignore(v.Root)

	slog.Info("vault initialized", "path", absPath)

	fmt.Fprintf(cmd.ErrOrStderr(), "Initialized 2ndbrain vault at %s\n", v.Root)
	if !flagPorcelain {
		fmt.Fprintln(cmd.ErrOrStderr(), "\nTo use this vault, open it in Obsidian (Open folder as vault) — 2nb")
		fmt.Fprintf(cmd.ErrOrStderr(), "follows your open Obsidian vault — or run commands with --vault %s\n", v.Root)
		fmt.Fprintln(cmd.ErrOrStderr(), "\nNext steps:")
		fmt.Fprintf(cmd.ErrOrStderr(), "  2nb create \"My First Note\" --vault %s\n", v.Root)
		fmt.Fprintln(cmd.ErrOrStderr(), "  2nb ai setup                  Configure AI for semantic search & ask")
		fmt.Fprintln(cmd.ErrOrStderr(), "  2nb skills install --all      Teach AI agents (Claude Code, Cursor, …) about your vault")
	}
	return nil
}

