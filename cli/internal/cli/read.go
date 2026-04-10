package cli

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/apresai/2ndbrain/internal/document"
	"github.com/apresai/2ndbrain/internal/output"
	"github.com/spf13/cobra"
)

var readChunk string

var readCmd = &cobra.Command{
	Use:   "read <path>",
	Short: "Read a document or specific chunk",
	Args:  cobra.ExactArgs(1),
	RunE:  runRead,
}

func init() {
	readCmd.Flags().StringVar(&readChunk, "chunk", "", "Heading path to read a specific section")
	readCmd.GroupID = "docs"
	rootCmd.AddCommand(readCmd)
}

func runRead(cmd *cobra.Command, args []string) error {
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

	if readChunk != "" {
		return readSpecificChunk(cmd, doc)
	}

	format := getFormat(cmd)
	return output.Write(os.Stdout, format, doc)
}

func readSpecificChunk(cmd *cobra.Command, doc *document.Document) error {
	chunks := document.ChunkDocument(doc)
	target := strings.ToLower(readChunk)

	for _, c := range chunks {
		heading := strings.ToLower(c.HeadingPath)
		// Match by exact heading path or by the last heading component
		if heading == target || strings.HasSuffix(heading, target) || containsHeading(heading, target) {
			format := getFormat(cmd)
			return output.Write(os.Stdout, format, c)
		}
	}

	return exitWithError(ExitNotFound, fmt.Sprintf("chunk not found: %s", readChunk))
}

func containsHeading(headingPath, target string) bool {
	parts := strings.Split(headingPath, " > ")
	for _, p := range parts {
		// Strip # prefix for comparison
		cleaned := strings.TrimLeft(p, "# ")
		if strings.ToLower(cleaned) == target {
			return true
		}
	}
	return false
}
