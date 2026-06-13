package cli

import (
	"os"

	"github.com/apresai/2ndbrain/internal/output"
	"github.com/spf13/cobra"
)

var foldersCmd = &cobra.Command{
	Use:   "folders",
	Short: "List folders in the vault with document counts",
	Long: `List every directory that holds documents, with how many documents sit
directly in it. Documents at the vault root are bucketed under "(root)".`,
	Example: `  2nb folders
  2nb folders --json`,
	RunE: runFolders,
}

func init() {
	foldersCmd.GroupID = "quality"
	rootCmd.AddCommand(foldersCmd)
}

func runFolders(cmd *cobra.Command, args []string) error {
	v, err := openVault()
	if err != nil {
		return err
	}
	defer v.Close()

	folders, err := v.DB.FolderCounts()
	if err != nil {
		return err
	}

	format := getFormat(cmd)
	return output.Write(os.Stdout, format, folders)
}
