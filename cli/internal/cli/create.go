package cli

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/apresai/2ndbrain/internal/document"
	"github.com/apresai/2ndbrain/internal/output"
	"github.com/apresai/2ndbrain/internal/vault"
	"github.com/spf13/cobra"
)

var safeFilenameRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9 _\-\.,'&:()#+]*$`)

func validateTitle(title string) error {
	if title == "-" {
		return fmt.Errorf("title cannot be a single dash")
	}
	if strings.HasPrefix(title, "-") {
		return fmt.Errorf("title cannot start with a dash")
	}
	if !safeFilenameRe.MatchString(title) {
		return fmt.Errorf("title contains invalid characters — use letters, numbers, spaces, and basic punctuation only")
	}
	return nil
}

var (
	createType  string
	createTitle string
)

var createCmd = &cobra.Command{
	Use:   "create [title]",
	Short: "Create a new document from a template",
	Long:  "Create a new document. Title can be a positional argument or --title flag.\nExamples:\n  2nb create \"My New Note\"\n  2nb create --type adr \"Use JWT for Auth\"",
	Args:  cobra.MaximumNArgs(1),
	RunE:  runCreate,
}

func init() {
	createCmd.Flags().StringVar(&createType, "type", "note", "Document type (adr, runbook, note, postmortem)")
	createCmd.Flags().StringVar(&createTitle, "title", "", "Document title")
	rootCmd.AddCommand(createCmd)
}

func runCreate(cmd *cobra.Command, args []string) error {
	// Accept title as positional arg or --title flag
	if len(args) > 0 && createTitle == "" {
		createTitle = args[0]
	}
	if createTitle == "" {
		return fmt.Errorf("title is required: 2nb create \"My Note\" or 2nb create --title \"My Note\"")
	}

	// Validate title for safe filenames
	if err := validateTitle(createTitle); err != nil {
		return err
	}

	v, err := openVaultAndSetActive()
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

	if format != "" {
		return output.Write(os.Stdout, format, result)
	}

	fmt.Printf("Created %s: %s\n", doc.Type, doc.Path)
	return nil
}
