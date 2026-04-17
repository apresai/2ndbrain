package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/apresai/2ndbrain/internal/ai"
	"github.com/apresai/2ndbrain/internal/skills"
	"github.com/apresai/2ndbrain/internal/vault"
	"github.com/spf13/cobra"
)

var completionCmd = &cobra.Command{
	Use:   "completion",
	Short: "Generate or install shell completion scripts",
	Long: `Generate the shell completion script for 2nb, or install it in one step.

Subcommand/flag completion works out of the box. Most commands also
offer dynamic value completion — vault paths, document paths, schema
types, agent names, model IDs, and AI providers — so ` + "`" + `2nb read <TAB>` + "`" + `
suggests actual markdown files in your vault.

The fastest path is:
  2nb completion install    # writes the script + prints rc setup

If you'd rather manage it yourself, ` + "`" + `2nb completion zsh > <path>` + "`" + ` prints
the script to stdout.`,
	Example: `  2nb completion install             # recommended: one-shot install
  2nb completion zsh                 # print zsh completion to stdout
  2nb completion bash > /tmp/2nb.sh  # print bash completion to a file`,
}

var completionZshCmd = &cobra.Command{
	Use:   "zsh",
	Short: "Generate zsh completion script",
	Long:  "Prints a zsh completion script to stdout. To enable it, save it somewhere in your $fpath (e.g. ~/.zsh/completions/_2nb) and ensure `autoload -Uz compinit && compinit` runs.",
	RunE: func(cmd *cobra.Command, args []string) error {
		return rootCmd.GenZshCompletion(cmd.OutOrStdout())
	},
}

var completionBashCmd = &cobra.Command{
	Use:   "bash",
	Short: "Generate bash completion script",
	RunE: func(cmd *cobra.Command, args []string) error {
		return rootCmd.GenBashCompletionV2(cmd.OutOrStdout(), true)
	},
}

var completionFishCmd = &cobra.Command{
	Use:   "fish",
	Short: "Generate fish completion script",
	RunE: func(cmd *cobra.Command, args []string) error {
		return rootCmd.GenFishCompletion(cmd.OutOrStdout(), true)
	},
}

var completionPowershellCmd = &cobra.Command{
	Use:   "powershell",
	Short: "Generate powershell completion script",
	RunE: func(cmd *cobra.Command, args []string) error {
		return rootCmd.GenPowerShellCompletionWithDesc(cmd.OutOrStdout())
	},
}

var completionInstallDir string

var completionInstallCmd = &cobra.Command{
	Use:   "install",
	Short: "Install zsh completion to ~/.zsh/completions/_2nb",
	Long: `Writes the zsh completion script into ~/.zsh/completions/_2nb (or a
directory you pass with --dir) and prints the .zshrc snippet needed to
pick it up. Idempotent — re-running just overwrites the script.`,
	Example: `  2nb completion install
  2nb completion install --dir ~/.config/zsh/completions`,
	RunE: runCompletionInstall,
}

func init() {
	completionInstallCmd.Flags().StringVar(&completionInstallDir, "dir", "", "Target directory (default: ~/.zsh/completions)")
	completionCmd.AddCommand(completionZshCmd)
	completionCmd.AddCommand(completionBashCmd)
	completionCmd.AddCommand(completionFishCmd)
	completionCmd.AddCommand(completionPowershellCmd)
	completionCmd.AddCommand(completionInstallCmd)
	completionCmd.GroupID = "config"
	rootCmd.AddCommand(completionCmd)
}

func runCompletionInstall(cmd *cobra.Command, args []string) error {
	dir := completionInstallDir
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("resolve home directory: %w", err)
		}
		dir = filepath.Join(home, ".zsh", "completions")
	} else {
		dir = expandPath(dir)
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}

	target := filepath.Join(dir, "_2nb")
	f, err := os.Create(target)
	if err != nil {
		return fmt.Errorf("write %s: %w", target, err)
	}
	defer f.Close()
	if err := rootCmd.GenZshCompletion(f); err != nil {
		return fmt.Errorf("generate zsh completion: %w", err)
	}

	fmt.Fprintf(cmd.ErrOrStderr(), "Installed zsh completion to %s\n", target)
	if !flagPorcelain {
		fmt.Fprintln(cmd.ErrOrStderr())
		fmt.Fprintln(cmd.ErrOrStderr(), "If tab-completion doesn't work in a new shell, add this to ~/.zshrc:")
		fmt.Fprintf(cmd.ErrOrStderr(), "  fpath=(%s $fpath)\n", dir)
		fmt.Fprintln(cmd.ErrOrStderr(), "  autoload -Uz compinit && compinit")
	}
	return nil
}

// -----------------------------------------------------------------------------
// Dynamic completion helpers.
//
// Completion functions fire on every tab press. Constraints:
//   - Must stay fast (<100ms) — avoid DB opens unless actually needed.
//   - Must never write to stdout; any emit there becomes a completion
//     candidate. Errors must be swallowed.
//   - On any failure, fall back to ShellCompDirectiveDefault so the
//     shell still does filesystem completion where relevant.
// -----------------------------------------------------------------------------

// loadSchemasForCompletion reads the active vault's schemas without
// doing a full vault.Open (which touches SQLite, runs migrations, and
// can emit stderr on config self-heal). Completion hits this on every
// --type / --status / --set tab press, so the raw YAML read is the
// right tradeoff. Falls back to DefaultSchemas() if no vault is
// reachable.
func loadSchemasForCompletion() *vault.SchemaSet {
	root := resolveVaultRootForCompletion()
	if root == "" {
		return vault.DefaultSchemas()
	}
	schemas, err := vault.LoadSchemas(filepath.Join(root, vault.DotDirName))
	if err != nil || schemas == nil {
		return vault.DefaultSchemas()
	}
	return schemas
}

// resolveVaultRootForCompletion resolves the active vault root without
// opening the DB. Same priority order as openVault (--vault flag,
// 2NB_VAULT env, active-vault file, cwd), but stops at the directory
// check — no config/schema parse, no SQLite open.
func resolveVaultRootForCompletion() string {
	candidates := []string{
		expandPath(flagVault),
		expandPath(os.Getenv("2NB_VAULT")),
		getActiveVault(),
	}
	if cwd, err := os.Getwd(); err == nil {
		candidates = append(candidates, cwd)
	}
	for _, c := range candidates {
		if c == "" {
			continue
		}
		if root := vault.FindVaultRoot(c); root != "" {
			return root
		}
	}
	return ""
}

// completeDocPaths suggests relative .md paths from the active vault's
// index, newest-modified first.
func completeDocPaths(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	v, err := openVault()
	if err != nil {
		return nil, cobra.ShellCompDirectiveDefault
	}
	defer v.Close()

	rows, err := v.DB.Conn().Query(`SELECT path FROM documents ORDER BY modified_at DESC LIMIT 500`)
	if err != nil {
		return nil, cobra.ShellCompDirectiveDefault
	}
	defer rows.Close()

	var paths []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err == nil && p != "" {
			paths = append(paths, p)
		}
	}
	return paths, cobra.ShellCompDirectiveNoFileComp
}

// completeVaultPaths suggests recent vault roots alongside directory
// completion, so users can tab-complete any path — not just recents.
func completeVaultPaths(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	return listRecentVaults(), cobra.ShellCompDirectiveFilterDirs
}

func completeSchemaTypes(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	schemas := loadSchemasForCompletion()
	types := make([]string, 0, len(schemas.Types))
	for t := range schemas.Types {
		types = append(types, t)
	}
	sort.Strings(types)
	return types, cobra.ShellCompDirectiveNoFileComp
}

// completeSchemaStatuses returns status enum values. When --type is set
// it narrows to that type; otherwise the union across all types.
func completeSchemaStatuses(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	schemas := loadSchemasForCompletion()

	selectedType, _ := cmd.Flags().GetString("type")
	seen := map[string]struct{}{}
	for t, s := range schemas.Types {
		if selectedType != "" && t != selectedType {
			continue
		}
		if f, ok := s.Fields["status"]; ok {
			for _, v := range f.Enum {
				seen[v] = struct{}{}
			}
		}
	}
	out := make([]string, 0, len(seen))
	for s := range seen {
		out = append(out, s)
	}
	sort.Strings(out)
	return out, cobra.ShellCompDirectiveNoFileComp
}

func completeAgentSlugs(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	out := make([]string, 0, len(skills.Agents))
	for _, a := range skills.Agents {
		out = append(out, a.Slug)
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}

// completeModelIDs merges the built-in catalog with the user catalog,
// filtered by --provider when set. Avoids BuildModelList because its
// CheckStatus/Discover paths can make network calls.
func completeModelIDs(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	provider, _ := cmd.Flags().GetString("provider")
	vaultRoot := resolveVaultRootForCompletion()

	seen := map[string]struct{}{}
	add := func(models []ai.ModelInfo) {
		for _, m := range models {
			if provider != "" && m.Provider != provider {
				continue
			}
			seen[m.ID] = struct{}{}
		}
	}
	add(ai.BuiltinCatalog())
	add(ai.LoadUserCatalog(vaultRoot))

	out := make([]string, 0, len(seen))
	for id := range seen {
		out = append(out, id)
	}
	sort.Strings(out)
	return out, cobra.ShellCompDirectiveNoFileComp
}

func completeProviders(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	out := make([]string, len(ai.KnownProviders))
	copy(out, ai.KnownProviders)
	return out, cobra.ShellCompDirectiveNoFileComp
}

func completeCatalogScopes(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	return []string{"global", "vault"}, cobra.ShellCompDirectiveNoFileComp
}

func completeModelTypes(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	return []string{"embedding", "generation"}, cobra.ShellCompDirectiveNoFileComp
}

func completeConfigKeys(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	out := make([]string, len(settableConfigKeys))
	copy(out, settableConfigKeys)
	return out, cobra.ShellCompDirectiveNoFileComp
}

func completeSortFields(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	return []string{"modified", "created", "title", "path"}, cobra.ShellCompDirectiveNoFileComp
}

func completeBenchProbes(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	return []string{"embed", "generate", "search", "rag"}, cobra.ShellCompDirectiveNoFileComp
}

// completeMetaSetKeys completes the key= half of `--set key=value`
// tokens by showing schema field names. We can't complete the value
// half: Cobra's ValidArgsFunction runs once per whole token, so we
// can't re-enter when the user has typed `key=` and wants value
// suggestions — field names alone already save most keystrokes.
func completeMetaSetKeys(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if strings.Contains(toComplete, "=") {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	schemas := loadSchemasForCompletion()
	seen := map[string]struct{}{
		"title": {}, "status": {}, "tags": {}, "type": {},
	}
	for _, s := range schemas.Types {
		for field := range s.Fields {
			seen[field] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k+"=")
	}
	sort.Strings(out)
	return out, cobra.ShellCompDirectiveNoFileComp | cobra.ShellCompDirectiveNoSpace
}
