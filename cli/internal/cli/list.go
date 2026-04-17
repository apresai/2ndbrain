package cli

import (
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/apresai/2ndbrain/internal/output"
	"github.com/spf13/cobra"
)

var (
	listType   string
	listStatus string
	listTag    string
	listLimit  int
	listSort   string
)

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List all documents in the vault with optional filters",
	Example: `  2nb list
  2nb list --type adr --status accepted
  2nb list --tag auth --sort modified --limit 10
  2nb list --json                                   # machine-readable`,
	RunE: runList,
}

func init() {
	listCmd.Flags().StringVar(&listType, "type", "", "Filter by document type")
	listCmd.Flags().StringVar(&listStatus, "status", "", "Filter by status")
	listCmd.Flags().StringVar(&listTag, "tag", "", "Filter by tag")
	listCmd.Flags().IntVar(&listLimit, "limit", 100, "Maximum results")
	listCmd.Flags().StringVar(&listSort, "sort", "modified", "Sort by: modified, created, title, path")
	_ = listCmd.RegisterFlagCompletionFunc("type", completeSchemaTypes)
	_ = listCmd.RegisterFlagCompletionFunc("status", completeSchemaStatuses)
	_ = listCmd.RegisterFlagCompletionFunc("sort", completeSortFields)
	listCmd.GroupID = "docs"
	rootCmd.AddCommand(listCmd)
}

type ListItem struct {
	ID         string `json:"id"`
	Path       string `json:"path"`
	Title      string `json:"title"`
	Type       string `json:"type"`
	Status     string `json:"status"`
	ModifiedAt string `json:"modified_at"`
}

func runList(cmd *cobra.Command, args []string) error {
	v, err := openVault()
	if err != nil {
		return err
	}
	defer v.Close()

	query := "SELECT id, path, title, doc_type, status, modified_at FROM documents"
	var conditions []string
	var qArgs []any

	if listType != "" {
		conditions = append(conditions, "doc_type = ?")
		qArgs = append(qArgs, listType)
	}
	if listStatus != "" {
		conditions = append(conditions, "status = ?")
		qArgs = append(qArgs, listStatus)
	}
	if listTag != "" {
		conditions = append(conditions, "id IN (SELECT doc_id FROM tags WHERE tag = ?)")
		qArgs = append(qArgs, listTag)
	}

	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}

	switch listSort {
	case "created":
		query += " ORDER BY created_at DESC"
	case "title":
		query += " ORDER BY title ASC"
	case "path":
		query += " ORDER BY path ASC"
	default:
		query += " ORDER BY modified_at DESC"
	}

	query += " LIMIT ?"
	qArgs = append(qArgs, listLimit)

	rows, err := v.DB.Conn().Query(query, qArgs...)
	if err != nil {
		return fmt.Errorf("query: %w", err)
	}
	defer rows.Close()

	var items []ListItem
	for rows.Next() {
		var item ListItem
		if err := rows.Scan(&item.ID, &item.Path, &item.Title, &item.Type, &item.Status, &item.ModifiedAt); err != nil {
			continue
		}
		items = append(items, item)
	}

	if len(items) == 0 {
		if !flagPorcelain {
			fmt.Fprintln(os.Stderr, "No documents yet. Create one with: 2nb create \"My Note\"")
		} else {
			fmt.Fprintln(os.Stderr, "No documents found.")
		}
		return nil
	}

	format := getFormat(cmd)
	if format != "" {
		return output.Write(os.Stdout, format, items)
	}

	// Pretty table output by default
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "TITLE\tTYPE\tSTATUS\tPATH")
	for _, item := range items {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", item.Title, item.Type, item.Status, item.Path)
	}
	if err := w.Flush(); err != nil {
		return err
	}

	if !flagPorcelain && v.Config.AI.Provider == "" {
		fmt.Fprintln(os.Stderr, "\nTip: run `2nb ai setup` to enable semantic search and Q&A")
	}
	return nil
}
