package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

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
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolve home directory: %w", err)
	}
	zshrcPath := filepath.Join(home, ".zshrc")
	dir, err := resolveCompletionInstallDir(completionInstallDir, zshrcPath, home)
	if err != nil {
		return err
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

	added, err := updateZshrc(zshrcPath, dir)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "Warning: could not update %s: %v\n", zshrcPath, err)
		fmt.Fprintln(cmd.ErrOrStderr(), "Add this to your ~/.zshrc manually:")
		fmt.Fprintf(cmd.ErrOrStderr(), "  fpath=(%s $fpath)\n", dir)
		fmt.Fprintln(cmd.ErrOrStderr(), "  autoload -Uz compinit && compinit -i")
	} else if added {
		fmt.Fprintf(cmd.ErrOrStderr(), "Updated %s with completion init block.\n", zshrcPath)
		fmt.Fprintln(cmd.ErrOrStderr(), "Restart your shell or run: source ~/.zshrc")
	} else {
		fmt.Fprintf(cmd.ErrOrStderr(), "Shell config %s already has completion block — no changes made.\n", zshrcPath)
	}
	warnIfMultiple2nbOnPath(cmd.ErrOrStderr())

	return nil
}

const zshrcBlockBegin = "# BEGIN 2nb completion managed block"
const zshrcBlockEnd = "# END 2nb completion managed block"

func updateZshrc(zshrcPath, completionDir string) (added bool, err error) {
	existing, err := os.ReadFile(zshrcPath)
	if err != nil && !os.IsNotExist(err) {
		return false, fmt.Errorf("read %s: %w", zshrcPath, err)
	}

	existingStr := string(existing)

	block := buildZshrcBlock(completionDir)
	withoutManaged := stripManagedZshrcBlock(existingStr)
	updated := injectManagedZshrcBlock(withoutManaged, block)
	if strings.TrimRight(existingStr, "\n") == strings.TrimRight(updated, "\n") {
		return false, nil
	}
	if err = os.WriteFile(zshrcPath, []byte(updated), 0o644); err != nil {
		return false, fmt.Errorf("write %s: %w", zshrcPath, err)
	}
	return true, nil
}

func buildZshrcBlock(completionDir string) string {
	var b strings.Builder
	b.WriteString(zshrcBlockBegin + "\n")
	b.WriteString("if [[ -o interactive ]]; then\n")
	fmt.Fprintf(&b, "  fpath=(%s $fpath)\n", completionDir)
	b.WriteString("  [[ -d /opt/homebrew/share/zsh/site-functions ]] && fpath=(/opt/homebrew/share/zsh/site-functions $fpath)\n")
	b.WriteString("  autoload -Uz compinit\n")
	b.WriteString("  compinit -i\n")
	b.WriteString("fi\n")
	b.WriteString(zshrcBlockEnd)
	return b.String()
}

func stripManagedZshrcBlock(content string) string {
	if strings.TrimRight(content, "\n") == "" {
		return ""
	}
	lines := strings.Split(strings.TrimRight(content, "\n"), "\n")
	out := make([]string, 0, len(lines))
	inBlock := false
	for _, line := range lines {
		t := strings.TrimSpace(line)
		if t == zshrcBlockBegin {
			inBlock = true
			continue
		}
		if inBlock {
			if t == zshrcBlockEnd {
				inBlock = false
			}
			continue
		}
		out = append(out, line)
	}
	return strings.TrimRight(strings.Join(out, "\n"), "\n")
}

func injectManagedZshrcBlock(content, block string) string {
	if strings.TrimRight(content, "\n") == "" {
		return block + "\n"
	}
	lines := strings.Split(strings.TrimRight(content, "\n"), "\n")
	insertAt := firstTopLevelReturnOrExitLine(lines)
	if insertAt < 0 {
		insertAt = firstCompletionSetupLine(lines)
	}
	if insertAt < 0 {
		return strings.TrimRight(content, "\n") + "\n\n" + block + "\n"
	}
	before := strings.TrimRight(strings.Join(lines[:insertAt], "\n"), "\n")
	after := strings.TrimLeft(strings.Join(lines[insertAt:], "\n"), "\n")
	parts := make([]string, 0, 3)
	if before != "" {
		parts = append(parts, before)
	}
	parts = append(parts, block)
	if after != "" {
		parts = append(parts, after)
	}
	return strings.Join(parts, "\n\n") + "\n"
}

func firstTopLevelReturnOrExitLine(lines []string) int {
	for i, line := range lines {
		// Indented lines are inside a block — never top-level guards.
		if len(line) > 0 && (line[0] == ' ' || line[0] == '\t') {
			continue
		}
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		// Bare return/exit at top level.
		if hasWordPrefix(t, "return") || hasWordPrefix(t, "exit") {
			return i
		}
		// One-liner guards: `[[ cond ]] && return`, `cond || exit`, etc.
		if hasInlineReturnOrExit(t) {
			return i
		}
		// if-block guard whose only body is return/exit.
		if (strings.HasPrefix(t, "if ") || t == "if") && isSimpleReturnBlock(lines, i) {
			return i
		}
	}
	return -1
}

// firstCompletionSetupLine returns the index of the first unindented line that
// performs completion setup: an explicit compinit call, an fpath assignment, or
// a source of a file whose path contains "completion" or "compinit". Used as a
// fallback insertion point when no early-return guard is found, so our fpath
// update lands before any existing compinit — avoiding the case where compinit
// runs before our directory is in fpath.
func firstCompletionSetupLine(lines []string) int {
	for i, line := range lines {
		if len(line) > 0 && (line[0] == ' ' || line[0] == '\t') {
			continue
		}
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		if hasWordPrefix(t, "compinit") || strings.HasPrefix(t, "fpath=") ||
			(hasWordPrefix(t, "autoload") && strings.Contains(t, "compinit")) {
			return i
		}
		// `source .../completion.zsh.inc` or `. .../compinit` etc.
		if (strings.HasPrefix(t, "source ") || (len(t) >= 2 && t[0] == '.' && t[1] == ' ')) &&
			(strings.Contains(t, "completion") || strings.Contains(t, "compinit")) {
			return i
		}
	}
	return -1
}

// hasInlineReturnOrExit detects one-liner guards like `[[ cond ]] && return`.
func hasInlineReturnOrExit(line string) bool {
	for _, tok := range []string{"&& return", "|| return", "; return", "&& exit", "|| exit", "; exit"} {
		idx := strings.Index(line, tok)
		if idx < 0 {
			continue
		}
		rest := strings.TrimSpace(line[idx+len(tok):])
		// Accept: end of line, comment, semicolon, or return code digit.
		if rest == "" || rest[0] == '#' || rest[0] == ';' || (rest[0] >= '0' && rest[0] <= '9') {
			return true
		}
	}
	return false
}

// isSimpleReturnBlock reports whether the if-block whose header is at ifIdx
// has only return/exit as its then-branch body (i.e., it's a guard, not logic).
func isSimpleReturnBlock(lines []string, ifIdx int) bool {
	for j := ifIdx + 1; j < len(lines); j++ {
		t := strings.TrimSpace(lines[j])
		if t == "" || strings.HasPrefix(t, "#") || t == "then" {
			continue
		}
		if t == "fi" {
			return false // closed without a return — not a guard
		}
		if t == "else" || t == "elif" || strings.HasPrefix(t, "elif ") {
			return false // multi-branch if — not a simple guard
		}
		if hasWordPrefix(t, "return") || hasWordPrefix(t, "exit") || hasInlineReturnOrExit(t) {
			return true
		}
		return false // other commands in the block
	}
	return false
}

func hasWordPrefix(s, word string) bool {
	if !strings.HasPrefix(s, word) {
		return false
	}
	if len(s) == len(word) {
		return true
	}
	next := s[len(word)]
	return next == ' ' || next == '\t'
}

func resolveCompletionInstallDir(explicitDir, zshrcPath, home string) (string, error) {
	if explicitDir != "" {
		dir := expandPath(explicitDir)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", fmt.Errorf("create %s: %w", dir, err)
		}
		return dir, nil
	}
	candidates := completionDirCandidates(zshrcPath, home)
	var errs []string
	for _, dir := range candidates {
		if err := os.MkdirAll(dir, 0o755); err == nil {
			return dir, nil
		} else {
			errs = append(errs, fmt.Sprintf("%s: %v", dir, err))
		}
	}
	return "", fmt.Errorf("create completion directory: %s", strings.Join(errs, "; "))
}

func completionDirCandidates(zshrcPath, home string) []string {
	seen := map[string]struct{}{}
	var out []string

	if data, err := os.ReadFile(zshrcPath); err == nil {
		for _, dir := range completionDirsFromZshrc(string(data), home) {
			if _, ok := seen[dir]; ok {
				continue
			}
			seen[dir] = struct{}{}
			out = append(out, dir)
		}
	}

	defaults := []string{
		filepath.Join(home, ".zsh", "completions"),
		filepath.Join(home, ".zfunc"),
		"/opt/homebrew/share/zsh/site-functions",
		"/usr/local/share/zsh/site-functions",
	}
	for _, dir := range defaults {
		dir = filepath.Clean(dir)
		if _, ok := seen[dir]; ok {
			continue
		}
		seen[dir] = struct{}{}
		out = append(out, dir)
	}
	return out
}

var completionDirTokenRE = regexp.MustCompile(`(?:~|\$HOME|/)[^ \t\n\r"')()]*?(?:completions?|site-functions|zfunc)[^ \t\n\r"')()]*`)

func completionDirsFromZshrc(content, home string) []string {
	matches := completionDirTokenRE.FindAllString(content, -1)
	seen := map[string]struct{}{}
	var homeDirs []string
	var otherDirs []string

	for _, token := range matches {
		dir := normalizeCompletionDirToken(token, home)
		if dir == "" {
			continue
		}
		if _, ok := seen[dir]; ok {
			continue
		}
		seen[dir] = struct{}{}
		if dir == home || strings.HasPrefix(dir, home+string(os.PathSeparator)) {
			homeDirs = append(homeDirs, dir)
		} else {
			otherDirs = append(otherDirs, dir)
		}
	}

	return append(homeDirs, otherDirs...)
}

func normalizeCompletionDirToken(token, home string) string {
	token = strings.TrimSpace(token)
	switch {
	case token == "~":
		token = home
	case strings.HasPrefix(token, "~/"):
		token = filepath.Join(home, token[2:])
	case token == "$HOME":
		token = home
	case strings.HasPrefix(token, "$HOME/"):
		token = filepath.Join(home, token[len("$HOME/"):])
	}
	token = filepath.Clean(token)
	if token == "" || !filepath.IsAbs(token) {
		return ""
	}
	return token
}

func warnIfMultiple2nbOnPath(w io.Writer) {
	paths := findExecutablesOnPATH("2nb", os.Getenv("PATH"))
	if len(paths) <= 1 {
		return
	}

	active, _ := exec.LookPath("2nb")
	if active != "" {
		if abs, err := filepath.Abs(active); err == nil {
			active = abs
		}
		if real, err := filepath.EvalSymlinks(active); err == nil {
			active = real
		}
	}

	// Fetch versions in parallel; each probe is bounded by getBinaryVersion's own
	// deadline (and a one-shot timeout retry).
	var mu sync.Mutex
	versions := make(map[string]string, len(paths))
	var wg sync.WaitGroup
	for _, p := range paths {
		wg.Add(1)
		go func(p string) {
			defer wg.Done()
			v := getBinaryVersion(p)
			mu.Lock()
			versions[p] = v
			mu.Unlock()
		}(p)
	}
	wg.Wait()

	fmt.Fprintln(w, "Warning: multiple 2nb binaries found on PATH:")
	hasHomebrew, hasManual := false, false
	for _, p := range paths {
		marker := " -"
		if active != "" && sameExecutablePath(active, p) {
			marker = " *"
		}
		label := ""
		if looksLikeHomebrewPath(p) {
			label = "  [Homebrew]"
			hasHomebrew = true
		} else {
			hasManual = true
		}
		fmt.Fprintf(w, "  %s %s  v%s%s\n", marker, p, versions[p], label)
	}
	fmt.Fprintln(w, "  (* = active binary used when you run `2nb`)")
	if hasHomebrew && hasManual {
		fmt.Fprintln(w, "Tip: you have both a Homebrew-managed install and a manual install.")
		fmt.Fprintln(w, "  Keep Homebrew: remove the manual copy  →  rm $(which 2nb)  (run for each non-Homebrew path above)")
		fmt.Fprintln(w, "  Keep manual:   unlink Homebrew         →  brew unlink apresai/tap/twonb")
	} else {
		fmt.Fprintln(w, "Remove duplicates or reorder PATH so the version you want comes first.")
	}
}

// getBinaryVersion runs `path --version` and returns the last space-delimited
// token from the output (Cobra format: "2nb version 0.2.4"). Returns "unknown"
// if the probe fails or the output is empty.
//
// The probe shells out to a freshly-built binary; under a loaded `-race` test
// battery the first exec can be starved past a tight deadline, which is why the
// old 1s budget made the completion tests load-flaky. It now retries once with a
// longer deadline ONLY on a timeout: a clean non-zero exit (or empty output)
// won't change on a retry, so the failure path stays fast (one exec).
func getBinaryVersion(path string) string {
	for _, timeout := range []time.Duration{3 * time.Second, 6 * time.Second} {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		out, err := exec.CommandContext(ctx, path, "--version").Output()
		timedOut := ctx.Err() == context.DeadlineExceeded
		cancel()
		if err == nil && len(out) > 0 {
			if parts := strings.Fields(string(out)); len(parts) > 0 {
				return parts[len(parts)-1]
			}
		}
		if !timedOut {
			break
		}
	}
	return "unknown"
}

func looksLikeHomebrewPath(p string) bool {
	return strings.Contains(p, "/homebrew/") ||
		strings.Contains(p, "/Homebrew/") ||
		strings.Contains(p, "/Cellar/")
}

func findExecutablesOnPATH(name, pathEnv string) []string {
	seenCanonical := map[string]struct{}{}
	var out []string

	for _, dir := range filepath.SplitList(pathEnv) {
		if dir == "" {
			continue
		}
		candidate := filepath.Join(dir, name)
		info, err := os.Stat(candidate)
		if err != nil || info.IsDir() {
			continue
		}
		if info.Mode().Perm()&0o111 == 0 {
			continue
		}
		abs := candidate
		if v, err := filepath.Abs(candidate); err == nil {
			abs = v
		}
		canonical := abs
		if real, err := filepath.EvalSymlinks(abs); err == nil {
			canonical = real
		}
		if _, ok := seenCanonical[canonical]; ok {
			continue
		}
		seenCanonical[canonical] = struct{}{}
		out = append(out, abs)
	}
	return out
}

func sameExecutablePath(a, b string) bool {
	if a == b {
		return true
	}
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	return errA == nil && errB == nil && ra == rb
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

// resolveVaultRootForCompletion resolves a vault root for shell completion
// without opening the DB: --vault flag, 2NB_VAULT env, then cwd. It deliberately
// does NOT read Obsidian's registry (unlike openVault) — completion fires on
// every Tab press and must stay fast and non-blocking, so it skips the registry
// file read and falls back to cwd; worst case completions use DefaultSchemas().
func resolveVaultRootForCompletion() string {
	candidates := []string{
		expandPath(flagVault),
		expandPath(os.Getenv("2NB_VAULT")),
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
	return []string{"embed", "generate", "retrieval", "search", "rag"}, cobra.ShellCompDirectiveNoFileComp
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
