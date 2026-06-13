package cli

import (
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

var renameCmd = &cobra.Command{
	Use:   "rename <src> <newname>",
	Short: "Rename a note in place, rewriting every [[wikilink]] that points at it",
	Long: `Rename a note to <newname> within its current folder, rewriting every
[[wikilink]] across the vault that points at it. This is a thin wrapper over
` + "`2nb move`" + `: the destination is the source's directory joined with
<newname> (the ".md" extension is added if you omit it). All of move's behavior
applies, including --dry-run and the ambiguity guard.`,
	Example: `  2nb rename notes/draft.md final.md
  2nb rename notes/draft.md final          # .md is appended
  2nb rename old.md new.md --dry-run`,
	Args:              cobra.ExactArgs(2),
	ValidArgsFunction: completeDocPaths,
	RunE:              runRename,
}

func init() {
	// Reuse move's flag variables so `rename` honors --dry-run / --force
	// identically; binding them here keeps the wrapper a true delegate.
	renameCmd.Flags().BoolVar(&moveDryRun, "dry-run", false, "Preview the rename and rewrites without modifying anything")
	renameCmd.Flags().BoolVar(&moveForce, "force", false, "Overwrite an existing destination and proceed despite ambiguous links")
	renameCmd.GroupID = "docs"
	rootCmd.AddCommand(renameCmd)
}

func runRename(cmd *cobra.Command, args []string) error {
	src := expandPath(args[0])
	newName := args[1]

	// newName is a bare filename, not a path; reject directory components so
	// `rename` stays "rename in place" (use `move` to relocate).
	if strings.ContainsAny(newName, "/\\") {
		return exitWithError(ExitValidation, "error: <newname> must be a filename, not a path; use `2nb move` to relocate a note")
	}
	if !strings.HasSuffix(strings.ToLower(newName), ".md") {
		newName += ".md"
	}

	dst := filepath.Join(filepath.Dir(src), newName)
	return moveImpl(cmd, src, dst)
}
