package cli

import (
	"os"

	"github.com/apresai/2ndbrain/internal/output"
	"github.com/spf13/cobra"
)

// tagsCmd is a parent command so future tag operations (e.g. `tags rename`)
// can attach as subcommands. Invoked bare, it lists every tag with its count.
var tagsCmd = &cobra.Command{
	Use:   "tags",
	Short: "List all tags in the vault with document counts",
	Long: `List every frontmatter tag in the vault with how many documents carry it,
ordered by descending count. Tags are a parent command: subcommands attach for
future tag operations.`,
	Example: `  2nb tags
  2nb tags --json`,
	// Default action when invoked without a subcommand: list tags.
	RunE: runTagsList,
}

var tagsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all tags in the vault with document counts",
	RunE:  runTagsList,
}

func init() {
	tagsCmd.GroupID = "docs"
	tagsCmd.AddCommand(tagsListCmd)
	rootCmd.AddCommand(tagsCmd)
}

func runTagsList(cmd *cobra.Command, args []string) error {
	v, err := openVault()
	if err != nil {
		return err
	}
	defer v.Close()

	tags, err := v.DB.TagCounts()
	if err != nil {
		return err
	}

	format := getFormat(cmd)
	return output.Write(os.Stdout, format, tags)
}
