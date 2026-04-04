package cli

import (
	"fmt"
	"os"

	"github.com/apresai/2ndbrain/internal/output"
	"github.com/spf13/cobra"
)

var deleteForce bool

var deleteCmd = &cobra.Command{
	Use:   "delete <path>",
	Short: "Delete a document from the vault and index",
	Args:  cobra.ExactArgs(1),
	RunE:  runDelete,
}

func init() {
	deleteCmd.Flags().BoolVar(&deleteForce, "force", false, "Skip confirmation")
	rootCmd.AddCommand(deleteCmd)
}

func runDelete(cmd *cobra.Command, args []string) error {
	v, err := openVault()
	if err != nil {
		return fmt.Errorf("open vault: %w", err)
	}
	defer v.Close()

	relPath := args[0]
	absPath := v.AbsPath(relPath)

	doc, err := v.DB.GetDocumentByPath(relPath)
	if err != nil {
		return exitWithError(ExitNotFound, fmt.Sprintf("document not found in index: %s", relPath))
	}

	if !deleteForce && !flagPorcelain {
		fmt.Fprintf(os.Stderr, "Delete %q (%s)? [y/N] ", doc.Title, relPath)
		var answer string
		fmt.Scanln(&answer)
		if answer != "y" && answer != "Y" {
			fmt.Fprintln(os.Stderr, "Cancelled.")
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

	fmt.Printf("Deleted: %s\n", relPath)
	return nil
}
