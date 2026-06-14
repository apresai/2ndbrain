package cli

import (
	"fmt"

	"github.com/apresai/2ndbrain/internal/document"
	"github.com/spf13/cobra"
)

var (
	prependText string
	prependFile string
)

var prependCmd = &cobra.Command{
	Use:   "prepend <path>",
	Short: "Insert content at the start of a document's body",
	Long: `Insert content at the start of a document's body, after the frontmatter.
The frontmatter is left untouched; the new text is placed before the existing
body.

Content comes from --text, --file, or stdin (in that order of precedence).
This is an explicit, opt-in body write: 2nb otherwise never rewrites note bodies.`,
	Example: `  2nb prepend notes/log.md --text "> Top banner"
  2nb prepend notes/log.md --file header.md
  echo "prepended via pipe" | 2nb prepend notes/log.md`,
	Args:              cobra.ExactArgs(1),
	ValidArgsFunction: completeDocPaths,
	RunE:              runPrepend,
}

func init() {
	prependCmd.Flags().StringVar(&prependText, "text", "", "Content to prepend (inline string)")
	prependCmd.Flags().StringVar(&prependFile, "file", "", "Read content to prepend from this file")
	prependCmd.GroupID = "docs"
	rootCmd.AddCommand(prependCmd)
}

func runPrepend(cmd *cobra.Command, args []string) error {
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

	content, err := readWriteContent(prependText, prependFile, cmd.Flags().Changed("text"))
	if err != nil {
		return err
	}

	// doc.Body already excludes the frontmatter, so prepending to it inserts
	// the content right after the closing --- of the frontmatter.
	doc.Body = document.PrependToBody(doc.Body, content)

	if err := writeBody(v, doc, absPath); err != nil {
		return err
	}

	return reportBodyWrite(cmd, doc, "prepend")
}
