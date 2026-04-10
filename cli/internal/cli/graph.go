package cli

import (
	"fmt"
	"os"

	"github.com/apresai/2ndbrain/internal/graph"
	"github.com/apresai/2ndbrain/internal/output"
	"github.com/spf13/cobra"
)

var graphCmd = &cobra.Command{
	Use:   "graph <path>",
	Short: "Output the link graph for a document as a JSON adjacency list",
	Args:  cobra.ExactArgs(1),
	RunE:  runGraph,
}

func init() {
	graphCmd.GroupID = "quality"
	rootCmd.AddCommand(graphCmd)
}

func runGraph(cmd *cobra.Command, args []string) error {
	v, err := openVault()
	if err != nil {
		return err
	}
	defer v.Close()

	relPath := expandPath(args[0])
	doc, err := v.DB.GetDocumentByPath(relPath)
	if err != nil {
		return exitWithError(ExitNotFound, fmt.Sprintf("document not found: %s", relPath))
	}

	adj, err := graph.AdjacencyList(v.DB.Conn(), doc.ID)
	if err != nil {
		return fmt.Errorf("adjacency list: %w", err)
	}

	format := getFormat(cmd)
	return output.Write(os.Stdout, format, adj)
}
