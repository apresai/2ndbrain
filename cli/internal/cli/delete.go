package cli

import (
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/apresai/2ndbrain/internal/output"
	"github.com/spf13/cobra"
)

var deleteForce bool

var deleteCmd = &cobra.Command{
	Use:   "delete <path>",
	Short: "Delete a document from the vault and index",
	Long: `Delete a document from disk and the index (irreversible; the index's FK
cascades drop its chunks, tags, and links).

Interactively, delete asks for confirmation on stderr. For non-interactive or
agent use, pass --force to skip the prompt (--porcelain also skips it). Without
--force, an unanswered prompt times out after 60s (or errors immediately on a
closed stdin) and reports the note was NOT removed, rather than hanging.`,
	Example: `  2nb delete note.md            # prompts: Delete "Title" (note.md)? [y/N]
  2nb delete note.md --force    # no prompt (scripts, agents, CI)`,
	Args:              cobra.ExactArgs(1),
	ValidArgsFunction: completeDocPaths,
	RunE:              runDelete,
}

func init() {
	deleteCmd.Flags().BoolVar(&deleteForce, "force", false, "Skip the confirmation prompt (required for non-interactive/agent use)")
	deleteCmd.GroupID = "docs"
	rootCmd.AddCommand(deleteCmd)
}

// deleteConfirmTimeout bounds how long the interactive delete prompt waits for a
// Y/n answer. A non-terminal stdin (an agent driving 2nb over a pty) never sends
// a line, so a bare fmt.Scanln would block forever; instead we time out and
// loudly report the note was not removed, pointing at --force.
const deleteConfirmTimeout = 60 * time.Second

// confirmDelete prompts on stderr and returns whether the user approved the
// delete. It returns (false, nil) for a deliberate "no" (after printing
// "Cancelled."), and a loud ExitValidation error when no answer arrives — either
// an immediate EOF (e.g. </dev/null) or a 60s timeout on an unanswered prompt —
// so a non-interactive caller gets an actionable failure instead of a silent
// no-op or a hang.
func confirmDelete(title, relPath string) (bool, error) {
	fmt.Fprintf(os.Stderr, "Delete %q (%s)? [y/N] ", title, relPath)

	type reply struct {
		answer string
		n      int
	}
	// Buffered so the reader goroutine never blocks on send if we already timed out.
	ch := make(chan reply, 1)
	go func() {
		var answer string
		// Scanln returns n=0 (with an error) on EOF/no input, n>0 when a token is read.
		n, _ := fmt.Scanln(&answer)
		ch <- reply{answer: answer, n: n}
	}()

	notRemoved := func() error {
		return exitWithError(ExitValidation, fmt.Sprintf(
			"%q was NOT deleted (no confirmation received). Re-run with --force to skip the prompt:\n  2nb delete %s --force",
			relPath, relPath))
	}

	select {
	case r := <-ch:
		if r.n == 0 {
			// No input at all (closed/empty stdin): loud, so an agent piping
			// </dev/null doesn't misread a silent cancel as success.
			return false, notRemoved()
		}
		if r.answer != "y" && r.answer != "Y" {
			fmt.Fprintln(os.Stderr, "Cancelled.")
			return false, nil
		}
		return true, nil
	case <-time.After(deleteConfirmTimeout):
		fmt.Fprintln(os.Stderr) // finish the prompt line before the report
		return false, notRemoved()
	}
}

func runDelete(cmd *cobra.Command, args []string) error {
	v, err := openVaultAndSetActive()
	if err != nil {
		return err
	}
	defer v.Close()
	setupFileLogging(v)

	// Destructive command: a BARE positional stays strict-exact (the pre-compat
	// behavior) so a stale/typo'd path errors instead of fuzzy-resolving to a
	// different note that --force would then delete without a prompt. An explicit
	// file=/path= still honors the shim mode (fuzzy delete-by-title opt-in).
	delMode := flagResolveMode
	if delMode == "" {
		delMode = resolveExact
	}
	_, relPath, err := resolveTargetArgMode(v, args[0], delMode)
	if err != nil {
		return err
	}
	absPath := v.AbsPath(relPath)

	doc, err := v.DB.GetDocumentByPath(relPath)
	if err != nil {
		return exitWithError(ExitNotFound, fmt.Sprintf("document not found: %s\n\nRun `2nb list` to see available documents", relPath))
	}

	if !deleteForce && !flagPorcelain {
		confirmed, err := confirmDelete(doc.Title, relPath)
		if err != nil {
			return err
		}
		if !confirmed {
			return nil
		}
	}

	// Delete from disk
	if err := os.Remove(absPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("delete file: %w", err)
	}

	// Delete from index (FK cascades handle chunks, tags, links)
	if err := v.DB.DeleteDocument(doc.ID); err != nil {
		return fmt.Errorf("delete from index: %w", err)
	}

	format := getFormat(cmd)
	if format != "" {
		result := map[string]any{
			"deleted": true,
			"id":      doc.ID,
			"path":    relPath,
			"title":   doc.Title,
		}
		return output.Write(os.Stdout, format, result)
	}

	slog.Info("document deleted", "path", relPath)
	fmt.Printf("Deleted: %s\n", relPath)
	return nil
}
