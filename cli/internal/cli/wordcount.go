package cli

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/apresai/2ndbrain/internal/document"
	"github.com/apresai/2ndbrain/internal/output"
	"github.com/spf13/cobra"
)

var wordcountCmd = &cobra.Command{
	Use:     "wordcount <path>",
	Aliases: []string{"wc"},
	Short:   "Count words, characters, and headings in a document",
	Long: `Report word, character, and heading counts for a document. Counts run over
the indexable body (Obsidian comments stripped, frontmatter excluded), so the
numbers match what search and embeddings see rather than the raw file bytes.`,
	Example: `  2nb wordcount my-note.md
  2nb wordcount research/notes.md --json`,
	Args:              cobra.ExactArgs(1),
	ValidArgsFunction: completeDocPaths,
	RunE:              runWordcount,
}

func init() {
	wordcountCmd.GroupID = "quality"
	rootCmd.AddCommand(wordcountCmd)
}

// WordCountResult is the serializable payload for `2nb wordcount`. Counts are
// computed over the indexable body (comments stripped), so they are consistent
// with indexing rather than the raw file.
type WordCountResult struct {
	Path       string `json:"path"`
	Title      string `json:"title"`
	Words      int    `json:"words"`
	Characters int    `json:"characters"`
	Headings   int    `json:"headings"`
}

func runWordcount(cmd *cobra.Command, args []string) error {
	v, err := openVault()
	if err != nil {
		return err
	}
	defer v.Close()

	path, _, err := resolveTargetArg(v, args[0])
	if err != nil {
		return err
	}
	doc, err := document.ParseFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return exitWithError(ExitNotFound, fmt.Sprintf("file not found: %s\n\nRun `2nb list` to see available documents", args[0]))
		}
		return exitWithError(ExitNotFound, fmt.Sprintf("cannot read %s: %v", args[0], err))
	}
	doc.Path = v.RelPath(path)

	body := doc.IndexableBody()
	result := WordCountResult{
		Path:       doc.Path,
		Title:      doc.Title,
		Words:      len(strings.Fields(body)),
		Characters: utf8.RuneCountInString(body),
		Headings:   countHeadings(doc),
	}

	format := getFormat(cmd)
	return output.Write(os.Stdout, format, result)
}

// countHeadings returns the number of heading-bounded sections that actually
// open with a heading. A document's leading preamble (text before the first
// heading) chunks to "(preamble)" and is not a heading, so it is excluded.
func countHeadings(doc *document.Document) int {
	n := 0
	for _, node := range document.BuildOutline(doc) {
		if node.Level > 0 {
			n++
		}
	}
	return n
}
