package cli

import (
	"os"

	"github.com/apresai/2ndbrain/internal/output"
	"github.com/spf13/cobra"
)

var aliasesCmd = &cobra.Command{
	Use:   "aliases",
	Short: "List frontmatter aliases mapped to their documents",
	Long: `List every frontmatter alias in the vault joined to the document that
declares it (alias to path/title), ordered by alias. Aliases are the alternate
names Obsidian resolves wikilinks against.`,
	Example: `  2nb aliases
  2nb aliases --json`,
	RunE: runAliases,
}

func init() {
	aliasesCmd.GroupID = "quality"
	rootCmd.AddCommand(aliasesCmd)
}

func runAliases(cmd *cobra.Command, args []string) error {
	v, err := openVault()
	if err != nil {
		return err
	}
	defer v.Close()

	aliases, err := v.DB.AllAliases()
	if err != nil {
		return err
	}

	format := getFormat(cmd)
	return output.Write(os.Stdout, format, aliases)
}
