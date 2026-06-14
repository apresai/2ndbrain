package cli

import (
	"fmt"
	"os"

	"github.com/apresai/2ndbrain/internal/document"
	"github.com/apresai/2ndbrain/internal/output"
	"github.com/spf13/cobra"
)

var (
	appendText string
	appendFile string
)

var appendCmd = &cobra.Command{
	Use:   "append <path>",
	Short: "Append content to the end of a document's body",
	Long: `Append content to the end of a document's body. The frontmatter is left
untouched; the new text is added after the existing body.

Content comes from --text, --file, or stdin (in that order of precedence).
This is an explicit, opt-in body write: 2nb otherwise never rewrites note bodies.`,
	Example: `  2nb append notes/log.md --text "- New entry"
  2nb append notes/log.md --file snippet.md
  echo "appended via pipe" | 2nb append notes/log.md`,
	Args:              cobra.ExactArgs(1),
	ValidArgsFunction: completeDocPaths,
	RunE:              runAppend,
}

func init() {
	appendCmd.Flags().StringVar(&appendText, "text", "", "Content to append (inline string)")
	appendCmd.Flags().StringVar(&appendFile, "file", "", "Read content to append from this file")
	appendCmd.GroupID = "docs"
	rootCmd.AddCommand(appendCmd)
}

func runAppend(cmd *cobra.Command, args []string) error {
	v, err := openVaultAndSetActive()
	if err != nil {
		return err
	}
	defer v.Close()

	absPath, _, err := resolveTargetArg(v, args[0])
	if err != nil {
		return err
	}
	doc, err := document.ParseFile(absPath)
	if err != nil {
		return exitWithError(ExitNotFound, fmt.Sprintf("error: %v", err))
	}
	doc.Path = v.RelPath(absPath)

	content, err := readWriteContent(appendText, appendFile, cmd.Flags().Changed("text"))
	if err != nil {
		return err
	}

	doc.Body = document.AppendToBody(doc.Body, content)

	if err := writeBody(v, doc, absPath); err != nil {
		return err
	}

	return reportBodyWrite(cmd, doc, "append")
}

// reportBodyWrite emits the standard result for a body-write command: a JSON
// object when a machine format is requested, otherwise a one-line confirmation
// on stdout.
func reportBodyWrite(cmd *cobra.Command, doc *document.Document, op string) error {
	format := getFormat(cmd)
	if format != "" {
		return output.Write(os.Stdout, format, map[string]any{
			"path":      doc.Path,
			"title":     doc.Title,
			"type":      doc.Type,
			"operation": op,
		})
	}
	fmt.Printf("%s: %s\n", op, doc.Path)
	return nil
}
