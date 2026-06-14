package cli

import (
	"fmt"
	"os"

	"github.com/apresai/2ndbrain/internal/output"
	"github.com/spf13/cobra"
)

var linksCmd = &cobra.Command{
	Use:   "links <path>",
	Short: "List outbound links from a document, including broken ones",
	Long: `List every wikilink the document at <path> emits, including unresolved ones.
Each entry carries a resolved flag, so this doubles as a per-file broken-link
view: rows with "resolved": false point at a target that no document in the
vault matches.`,
	Args: cobra.ExactArgs(1),
	RunE: runLinks,
}

func init() {
	linksCmd.GroupID = "quality"
	rootCmd.AddCommand(linksCmd)
}

func runLinks(cmd *cobra.Command, args []string) error {
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

	refs, err := v.DB.OutboundLinks(doc.ID)
	if err != nil {
		return fmt.Errorf("links: %w", err)
	}

	if len(refs) == 0 {
		fmt.Fprintf(os.Stderr, "%s has no outbound links.\n", relPath)
	}

	format := getFormat(cmd)
	return output.Write(os.Stdout, format, refs)
}
