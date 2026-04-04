package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/apresai/2ndbrain/internal/document"
	"github.com/apresai/2ndbrain/internal/output"
	"github.com/apresai/2ndbrain/internal/vault"
	"github.com/spf13/cobra"
)

var (
	createType  string
	createTitle string
)

var createCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a new document from a template",
	RunE:  runCreate,
}

func init() {
	createCmd.Flags().StringVar(&createType, "type", "note", "Document type (adr, runbook, note, postmortem)")
	createCmd.Flags().StringVar(&createTitle, "title", "", "Document title")
	createCmd.MarkFlagRequired("title")
	rootCmd.AddCommand(createCmd)
}

func runCreate(cmd *cobra.Command, args []string) error {
	v, err := openVault()
	if err != nil {
		return fmt.Errorf("open vault: %w", err)
	}
	defer v.Close()

	// Determine initial status from schema
	initialStatus := "draft"
	if schema, ok := v.Schemas.Types[createType]; ok && schema.Status != nil {
		initialStatus = schema.Status.Initial
	}

	// Get template body and replace placeholders
	tmplBody := vault.GetTemplate(createType)
	tmplBody = strings.ReplaceAll(tmplBody, "{{.Title}}", createTitle)
	tmplBody = strings.ReplaceAll(tmplBody, "{{.Status}}", initialStatus)

	doc := document.NewDocument(createTitle, createType, tmplBody)
	doc.SetMeta("status", initialStatus)

	path, err := doc.WriteFile(v.Root)
	if err != nil {
		return fmt.Errorf("write document: %w", err)
	}

	doc.Path = v.RelPath(path)

	// Index the new document
	if err := v.DB.UpsertDocument(doc); err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to index document: %v\n", err)
	}

	chunks := document.ChunkDocument(doc)
	if err := v.DB.UpsertChunks(chunks); err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to index chunks: %v\n", err)
	}

	if err := v.DB.UpsertTags(doc.ID, doc.Tags); err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to index tags: %v\n", err)
	}

	links := document.ExtractWikiLinks(doc.Body)
	if err := v.DB.UpsertLinks(doc.ID, links); err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to index links: %v\n", err)
	}

	format := getFormat(cmd)
	result := map[string]any{
		"id":    doc.ID,
		"path":  doc.Path,
		"title": doc.Title,
		"type":  doc.Type,
	}

	return output.Write(os.Stdout, format, result)
}
