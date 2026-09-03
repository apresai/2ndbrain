package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/apresai/2ndbrain/internal/document"
	"github.com/apresai/2ndbrain/internal/output"
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
	exportCmd.GroupID = "integr"
	exportCmd.Flags().StringVar(&exportTypes, "types", "", "Comma-separated document types to include (e.g., adr,runbook)")
	exportCmd.Flags().StringVar(&exportStatus, "status", "", "Filter by status")
	exportCmd.Flags().IntVar(&exportLimit, "limit", 50, "Maximum documents to include")
	rootCmd.AddCommand(exportCmd)
}

// exportBundle is the `--json` record for a context bundle: the bundle itself
// plus the two counts a caller would otherwise have to derive by parsing it.
type exportBundle struct {
	Bundle string `json:"bundle"`
	Docs   int    `json:"docs"`
	Chars  int    `json:"chars"`
}

func runExport(cmd *cobra.Command, args []string) error {
	// export-context's output IS a document body (a markdown bundle), so it has
	// the same shape as `git diff`: raw/md/text emit it, json wraps it in a
	// record, and the row-set formats have nothing to render. runExport never
	// called getFormat at all, so `--json`, `--csv` and `--yaml` each printed
	// the markdown bundle and exited 0: piping `export-context --json` to jq
	// was a parse error on a command that reported success. Refused up front,
	// before the vault open, so the answer never depends on how many documents
	// matched. refuseNonJSONStream is deliberately not used: it would refuse
	// raw/md/text too, and those are exactly the formats that work here.
	format := getFormat(cmd)
	switch format {
	case output.FormatCSV, output.FormatTSV, output.FormatYAML:
		return exitWithError(ExitValidation, fmt.Sprintf(
			"error: a context bundle is a document body, not a row set; --format %s has nothing to render (use --json for a record, or raw/md/text for the bundle itself)", format))
	}

	v, err := openVault()
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
		if format == output.FormatJSON {
			// A machine consumer still owes stdout a parseable record: "no
			// documents matched" is an ordinary outcome, not an error.
			return writeOut(cmd, format, exportBundle{})
		}
		fmt.Fprintln(os.Stderr, "No documents match the filters.")
		return nil
	}

	// Generate CLAUDE.md-compatible output. Built into a buffer rather than
	// printed as we go, so the json record can carry the same bytes the body
	// formats emit.
	var b strings.Builder
	included := 0
	fmt.Fprintln(&b, "# Knowledge Base Context")
	fmt.Fprintln(&b)
	fmt.Fprintf(&b, "Generated from 2ndbrain vault. %d documents included.\n\n", len(docs))

	for _, d := range docs {
		absPath := v.AbsPath(d.path)
		doc, err := document.ParseFile(absPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: skip %s: %v\n", d.path, err)
			continue
		}
		included++

		fmt.Fprintf(&b, "## %s\n\n", d.title)
		fmt.Fprintf(&b, "**Type**: %s | **Status**: %s | **Path**: `%s`\n\n", d.docType, d.status, d.path)
		fmt.Fprintln(&b, strings.TrimSpace(doc.Body))
		fmt.Fprintln(&b)
		fmt.Fprintln(&b, "---")
		fmt.Fprintln(&b)
	}

	bundle := b.String()
	if format == output.FormatJSON {
		return writeOut(cmd, format, exportBundle{Bundle: bundle, Docs: included, Chars: len(bundle)})
	}
	// Empty, raw, md and text all emit the bundle itself; --copy goes through
	// the same writer so it copies the bundle rather than nothing.
	return writeOut(cmd, output.FormatRaw, bundle)
}
