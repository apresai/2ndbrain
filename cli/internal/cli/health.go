package cli

import (
	"fmt"
	"os"

	"github.com/apresai/2ndbrain/internal/output"
	"github.com/spf13/cobra"
)

var orphansCmd = &cobra.Command{
	Use:   "orphans",
	Short: "List documents with no inbound links",
	Long: `List every document that nothing in the vault links to (no resolved inbound
link). Orphans are notes a reader can only reach by searching, not by
following a link, so they are candidates for wiring into the graph.`,
	RunE: runOrphans,
}

var deadendsCmd = &cobra.Command{
	Use:   "deadends",
	Short: "List documents with no outbound links",
	Long: `List every document that links to nothing real in the vault (no resolved
outbound link). A note whose only links are broken still counts as a deadend,
since none of those links lead anywhere indexed.`,
	RunE: runDeadends,
}

func init() {
	orphansCmd.GroupID = "quality"
	deadendsCmd.GroupID = "quality"
	rootCmd.AddCommand(orphansCmd)
	rootCmd.AddCommand(deadendsCmd)
}

func runOrphans(cmd *cobra.Command, args []string) error {
	v, err := openVault()
	if err != nil {
		return err
	}
	defer v.Close()

	refs, err := v.DB.Orphans()
	if err != nil {
		return fmt.Errorf("orphans: %w", err)
	}

	if len(refs) == 0 {
		fmt.Fprintln(os.Stderr, "No orphaned documents: every document has at least one inbound link.")
	}

	format := getFormat(cmd)
	return output.Write(os.Stdout, format, refs)
}

func runDeadends(cmd *cobra.Command, args []string) error {
	v, err := openVault()
	if err != nil {
		return err
	}
	defer v.Close()

	refs, err := v.DB.Deadends()
	if err != nil {
		return fmt.Errorf("deadends: %w", err)
	}

	if len(refs) == 0 {
		fmt.Fprintln(os.Stderr, "No deadend documents: every document links to at least one other document.")
	}

	format := getFormat(cmd)
	return output.Write(os.Stdout, format, refs)
}
