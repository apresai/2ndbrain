package cli

import (
	"fmt"
	"os"

	"github.com/apresai/2ndbrain/internal/output"
	"github.com/spf13/cobra"
)

var unresolvedCmd = &cobra.Command{
	Use:   "unresolved",
	Short: "List unresolved links in the vault",
	Long:  `List all unresolved wikilinks (references to notes that do not yet exist) across the entire vault.`,
	Args:  cobra.NoArgs,
	RunE:  runUnresolved,
}

func init() {
	unresolvedCmd.GroupID = "quality"
	rootCmd.AddCommand(unresolvedCmd)
}

// UnresolvedLink is one broken wikilink: a document (SourcePath) that links to
// a raw target (TargetRaw, the text inside [[...]]) that resolves to no note in
// the vault. It is the JSON shape emitted by `2nb unresolved`.
type UnresolvedLink struct {
	SourcePath string `json:"source_path"`
	TargetRaw  string `json:"target_raw"`
}

func runUnresolved(cmd *cobra.Command, args []string) error {
	v, err := openVault()
	if err != nil {
		return err
	}
	defer v.Close()

	rows, err := v.DB.Conn().Query(`
		SELECT d.path, l.target_raw
		FROM links l
		JOIN documents d ON d.id = l.source_id
		WHERE l.target_id IS NULL
		ORDER BY d.path, l.target_raw
	`)
	if err != nil {
		return fmt.Errorf("query unresolved links: %w", err)
	}
	defer rows.Close()

	var list []UnresolvedLink
	for rows.Next() {
		var u UnresolvedLink
		if err := rows.Scan(&u.SourcePath, &u.TargetRaw); err != nil {
			return fmt.Errorf("scan unresolved link: %w", err)
		}
		list = append(list, u)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	format := getFormat(cmd)
	if format != "" {
		return output.Write(os.Stdout, format, list)
	}

	if len(list) == 0 {
		if !flagPorcelain {
			fmt.Println("No unresolved links found in the vault.")
		}
		return nil
	}

	for _, u := range list {
		fmt.Printf("%s\t%s\n", u.SourcePath, u.TargetRaw)
	}
	return nil
}
