package cli

import (
	"fmt"
	"os"

	"github.com/apresai/2ndbrain/internal/graph"
	"github.com/apresai/2ndbrain/internal/output"
	"github.com/spf13/cobra"
)

var relatedDepth int

var relatedCmd = &cobra.Command{
	Use:   "related <path>",
	Short: "Find documents related via link graph traversal",
	Args:  cobra.ExactArgs(1),
	RunE:  runRelated,
}

func init() {
	relatedCmd.Flags().IntVar(&relatedDepth, "depth", 2, "Maximum traversal depth")
	rootCmd.AddCommand(relatedCmd)
}

func runRelated(cmd *cobra.Command, args []string) error {
	v, err := openVault()
	if err != nil {
		return fmt.Errorf("open vault: %w", err)
	}
	defer v.Close()

	// Resolve path to doc ID
	relPath := expandPath(args[0])
	doc, err := v.DB.GetDocumentByPath(relPath)
	if err != nil {
		return exitWithError(ExitNotFound, fmt.Sprintf("document not found: %s", relPath))
	}

	g, err := graph.Traverse(v.DB.Conn(), doc.ID, relatedDepth)
	if err != nil {
		return fmt.Errorf("traverse: %w", err)
	}

	format := getFormat(cmd)
	return output.Write(os.Stdout, format, g)
}
