package cli

import (
	"errors"
	"fmt"
	"os"

	"github.com/apresai/2ndbrain/internal/document"
	"github.com/apresai/2ndbrain/internal/output"
	"github.com/spf13/cobra"
)

var outlineCmd = &cobra.Command{
	Use:   "outline <path>",
	Short: "Show the heading outline of a document",
	Long: `Print a document's heading tree: one row per heading-bounded section with
its heading path, level, and line span. Uses the same chunking as indexing
and read, so the outline matches what search and the MCP kb_structure tool see.`,
	Example: `  2nb outline my-note.md
  2nb outline decisions/0001-use-jwt.md --json`,
	Args:              cobra.ExactArgs(1),
	ValidArgsFunction: completeDocPaths,
	RunE:              runOutline,
}

func init() {
	outlineCmd.GroupID = "quality"
	rootCmd.AddCommand(outlineCmd)
}

// OutlineResult is the serializable payload for `2nb outline`. It carries the
// document identity plus the heading nodes from document.BuildOutline.
type OutlineResult struct {
	Path     string                 `json:"path"`
	Title    string                 `json:"title"`
	Sections []document.OutlineNode `json:"sections"`
}

func runOutline(cmd *cobra.Command, args []string) error {
	v, err := openVault()
	if err != nil {
		return err
	}
	defer v.Close()

	path := v.AbsPath(expandPath(args[0]))
	doc, err := document.ParseFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return exitWithError(ExitNotFound, fmt.Sprintf("file not found: %s\n\nRun `2nb list` to see available documents", args[0]))
		}
		return exitWithError(ExitNotFound, fmt.Sprintf("cannot read %s: %v", args[0], err))
	}
	doc.Path = v.RelPath(path)

	result := OutlineResult{
		Path:     doc.Path,
		Title:    doc.Title,
		Sections: document.BuildOutline(doc),
	}

	format := getFormat(cmd)
	return output.Write(os.Stdout, format, result)
}
