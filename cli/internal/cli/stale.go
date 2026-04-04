package cli

import (
	"fmt"
	"os"
	"time"

	"github.com/apresai/2ndbrain/internal/output"
	"github.com/spf13/cobra"
)

var staleSince int

var staleCmd = &cobra.Command{
	Use:   "stale",
	Short: "List documents not modified within a given number of days",
	RunE:  runStale,
}

func init() {
	staleCmd.Flags().IntVar(&staleSince, "since", 90, "Number of days to consider stale")
	rootCmd.AddCommand(staleCmd)
}

type StaleDoc struct {
	Path       string `json:"path"`
	Title      string `json:"title"`
	Type       string `json:"type"`
	Status     string `json:"status"`
	ModifiedAt string `json:"modified_at"`
	DaysStale  int    `json:"days_stale"`
}

func runStale(cmd *cobra.Command, args []string) error {
	v, err := openVault()
	if err != nil {
		return fmt.Errorf("open vault: %w", err)
	}
	defer v.Close()

	cutoff := time.Now().AddDate(0, 0, -staleSince).Format(time.RFC3339)

	rows, err := v.DB.Conn().Query(`
		SELECT path, title, doc_type, status, modified_at
		FROM documents
		WHERE modified_at < ? AND modified_at != ''
		ORDER BY modified_at ASC
	`, cutoff)
	if err != nil {
		return fmt.Errorf("query stale: %w", err)
	}
	defer rows.Close()

	var results []StaleDoc
	now := time.Now()

	for rows.Next() {
		var d StaleDoc
		if err := rows.Scan(&d.Path, &d.Title, &d.Type, &d.Status, &d.ModifiedAt); err != nil {
			continue
		}
		if t, err := time.Parse(time.RFC3339, d.ModifiedAt); err == nil {
			d.DaysStale = int(now.Sub(t).Hours() / 24)
		}
		results = append(results, d)
	}

	if len(results) == 0 {
		fmt.Fprintf(os.Stderr, "No documents stale for %d+ days.\n", staleSince)
	}

	format := getFormat(cmd)
	return output.Write(os.Stdout, format, results)
}
