package cli

import (
	"fmt"
	"os"

	"github.com/apresai/2ndbrain/internal/output"
	"github.com/spf13/cobra"
)

var backlinksCmd = &cobra.Command{
	Use:   "backlinks <path>",
	Short: "List resolved inbound links to a document",
	Long: `List every document that links to the document at <path> via a wikilink that
resolved to a real document. Use it to see what references a note before you
move, rename, or retire it.`,
	Args: cobra.ExactArgs(1),
	RunE: runBacklinks,
}

func init() {
	backlinksCmd.GroupID = "quality"
	rootCmd.AddCommand(backlinksCmd)
}

func runBacklinks(cmd *cobra.Command, args []string) error {
	v, err := openVault()
	if err != nil {
		return err
	}
	defer v.Close()

	_, relPath, err := resolveTargetArg(v, args[0])
	if err != nil {
		return err
	}
	doc, err := v.DB.GetDocumentByPath(relPath)
	if err != nil {
		return exitWithError(ExitNotFound, fmt.Sprintf("document not found: %s", relPath))
	}

	refs, err := v.DB.Backlinks(doc.ID)
	if err != nil {
		return fmt.Errorf("backlinks: %w", err)
	}

	if len(refs) == 0 {
		fmt.Fprintf(os.Stderr, "No documents link to %s.\n", relPath)
	}

	format := getFormat(cmd)
	return output.Write(os.Stdout, format, refs)
}
