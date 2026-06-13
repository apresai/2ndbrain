package cli

import (
	"fmt"

	"github.com/apresai/2ndbrain/internal/document"
	"github.com/spf13/cobra"
)

var (
	replaceSection string
	replaceText    string
	replaceFile    string
)

var replaceCmd = &cobra.Command{
	Use:   "replace <path>",
	Short: "Replace a document's body, or one section of it",
	Long: `Replace a document's body. With --section, only that heading's section
content is replaced (the heading line is preserved and sibling sections are
untouched). Without --section, the entire body is replaced.

Content comes from --text, --file, or stdin (in that order of precedence).
This is an explicit, opt-in body write: 2nb otherwise never rewrites note bodies.

Section matching is case-insensitive on the heading title and ignores leading
"#" markers, so --section "Decision" and --section "## Decision" match the same
heading. When a heading title appears more than once, the first match wins.`,
	Example: `  2nb replace notes/log.md --text "Brand new body"
  2nb replace decisions/0001.md --section "Decision" --text "We chose plan B."
  2nb replace notes/log.md --file body.md`,
	Args:              cobra.ExactArgs(1),
	ValidArgsFunction: completeDocPaths,
	RunE:              runReplace,
}

func init() {
	replaceCmd.Flags().StringVar(&replaceSection, "section", "", "Heading whose section content to replace (default: whole body)")
	replaceCmd.Flags().StringVar(&replaceText, "text", "", "Replacement content (inline string)")
	replaceCmd.Flags().StringVar(&replaceFile, "file", "", "Read replacement content from this file")
	replaceCmd.GroupID = "docs"
	rootCmd.AddCommand(replaceCmd)
}

func runReplace(cmd *cobra.Command, args []string) error {
	v, err := openVaultAndSetActive()
	if err != nil {
		return err
	}
	defer v.Close()

	absPath := v.AbsPath(expandPath(args[0]))
	doc, err := document.ParseFile(absPath)
	if err != nil {
		return exitWithError(ExitNotFound, fmt.Sprintf("error: %v", err))
	}
	doc.Path = v.RelPath(absPath)

	content, err := readWriteContent(replaceText, replaceFile, cmd.Flags().Changed("text"))
	if err != nil {
		return err
	}

	op := "replace"
	if replaceSection != "" {
		newBody, ok := document.ReplaceSection(doc.Body, replaceSection, content)
		if !ok {
			return exitWithError(ExitNotFound, fmt.Sprintf("section not found: %q (in %s)", replaceSection, doc.Path))
		}
		doc.Body = newBody
		op = "replace-section"
	} else {
		doc.Body = content
	}

	if err := writeBody(v, doc, absPath); err != nil {
		return err
	}

	return reportBodyWrite(cmd, doc, op)
}
