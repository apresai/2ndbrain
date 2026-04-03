package cli

import (
	"fmt"
	"os"

	"github.com/apresai/2ndbrain/internal/output"
	"github.com/apresai/2ndbrain/internal/vault"
	"github.com/spf13/cobra"
)

var indexCmd = &cobra.Command{
	Use:   "index",
	Short: "Build or rebuild the vault search index",
	RunE:  runIndex,
}

func init() {
	rootCmd.AddCommand(indexCmd)
}

func runIndex(cmd *cobra.Command, args []string) error {
	v, err := vault.Open(".")
	if err != nil {
		return fmt.Errorf("open vault: %w", err)
	}
	defer v.Close()

	if !flagPorcelain {
		fmt.Fprintln(os.Stderr, "Indexing vault...")
	}

	stats, err := vault.IndexVault(v, func(path string) {
		if !flagPorcelain {
			fmt.Fprintf(os.Stderr, "  %s\n", path)
		}
	})
	if err != nil {
		return fmt.Errorf("index vault: %w", err)
	}

	format := getFormat(cmd)
	return output.Write(os.Stdout, format, stats)
}
