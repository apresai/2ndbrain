package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/apresai/2ndbrain/internal/document"
	"github.com/apresai/2ndbrain/internal/vault"
	"github.com/spf13/cobra"
)

var (
	exportTypes  string
	exportStatus string
	exportLimit  int
)

var exportCmd = &cobra.Command{
	Use:   "export-context",
	Short: "Generate a CLAUDE.md-compatible context bundle",
	RunE:  runExport,
}

func init() {
	exportCmd.Flags().StringVar(&exportTypes, "types", "", "Comma-separated document types to include (e.g., adr,runbook)")
	exportCmd.Flags().StringVar(&exportStatus, "status", "", "Filter by status")
	exportCmd.Flags().IntVar(&exportLimit, "limit", 50, "Maximum documents to include")
	rootCmd.AddCommand(exportCmd)
}

func runExport(cmd *cobra.Command, args []string) error {
	v, err := vault.Open(".")
	if err != nil {
		return fmt.Errorf("open vault: %w", err)
	}
	defer v.Close()

	query := "SELECT id, path, title, doc_type, status FROM documents WHERE 1=1"
	var qArgs []any

	if exportTypes != "" {
		types := strings.Split(exportTypes, ",")
		placeholders := make([]string, len(types))
		for i, t := range types {
			placeholders[i] = "?"
			qArgs = append(qArgs, strings.TrimSpace(t))
		}
		query += " AND doc_type IN (" + strings.Join(placeholders, ",") + ")"
	}

	if exportStatus != "" {
		query += " AND status = ?"
		qArgs = append(qArgs, exportStatus)
	}

	query += " ORDER BY modified_at DESC LIMIT ?"
	qArgs = append(qArgs, exportLimit)

	rows, err := v.DB.Conn().Query(query, qArgs...)
	if err != nil {
		return fmt.Errorf("query docs: %w", err)
	}
	defer rows.Close()

	type docEntry struct {
		id, path, title, docType, status string
	}
	var docs []docEntry

	for rows.Next() {
		var d docEntry
		if err := rows.Scan(&d.id, &d.path, &d.title, &d.docType, &d.status); err != nil {
			continue
		}
		docs = append(docs, d)
	}

	if len(docs) == 0 {
		fmt.Fprintln(os.Stderr, "No documents match the filters.")
		return nil
	}

	// Generate CLAUDE.md-compatible output
	fmt.Println("# Knowledge Base Context")
	fmt.Println()
	fmt.Printf("Generated from 2ndbrain vault. %d documents included.\n\n", len(docs))

	for _, d := range docs {
		absPath := v.AbsPath(d.path)
		doc, err := document.ParseFile(absPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: skip %s: %v\n", d.path, err)
			continue
		}

		fmt.Printf("## %s\n\n", d.title)
		fmt.Printf("**Type**: %s | **Status**: %s | **Path**: `%s`\n\n", d.docType, d.status, d.path)
		fmt.Println(strings.TrimSpace(doc.Body))
		fmt.Println()
		fmt.Println("---")
		fmt.Println()
	}

	return nil
}
