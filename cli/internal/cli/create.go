package cli

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/apresai/2ndbrain/internal/ai"
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
	createType           string
	createTitle          string
	createAllowDuplicate bool
)

var createCmd = &cobra.Command{
	Use:   "create [title]",
	Short: "Create a new document from a template",
	Long:  "Create a new document. Title can be a positional argument or --title flag.\nExamples:\n  2nb create \"My New Note\"\n  2nb create --type adr \"Use JWT for Auth\"",
	Args:  cobra.MaximumNArgs(1),
	RunE:  runCreate,
}

func init() {
	createCmd.Flags().StringVar(&createType, "type", "note", "Document type (adr, runbook, note, postmortem, prd, prfaq)")
	createCmd.Flags().StringVar(&createTitle, "title", "", "Document title")
	createCmd.Flags().BoolVar(&createAllowDuplicate, "allow-duplicate", false, "Allow creating a document with duplicate content")
	createCmd.GroupID = "docs"
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
		return err
	}
	defer v.Close()
	setupFileLogging(v)
	slog.Info("create", "type", createType, "title", createTitle)

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
	doc.ComputeContentHash()

	// Check for duplicate content
	if !createAllowDuplicate {
		dupPath, err := v.DB.FindByContentHash(doc.ContentHash, "")
		if err != nil {
			return fmt.Errorf("check duplicates: %w", err)
		}
		if dupPath != "" {
			return fmt.Errorf("duplicate content: matches existing document %q\nUse --allow-duplicate to create anyway", dupPath)
		}
	}

	path, err := doc.WriteFile(v.Root)
	if err != nil {
		return fmt.Errorf("write document: %w", err)
	}

	doc.Path = v.RelPath(path)

	// Index the new document
	if err := v.DB.UpsertDocument(doc); err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to index document: %v\n", err)
		slog.Warn("failed to index document", "err", err)
	}

	chunks := document.ChunkDocument(doc)
	if err := v.DB.UpsertChunks(chunks); err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to index chunks: %v\n", err)
		slog.Warn("failed to index chunks", "err", err)
	}

	if err := v.DB.UpsertTags(doc.ID, doc.Tags); err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to index tags: %v\n", err)
		slog.Warn("failed to index tags", "err", err)
	}

	links := document.ExtractWikiLinks(doc.Body)
	if err := v.DB.UpsertLinks(doc.ID, links); err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to index links: %v\n", err)
		slog.Warn("failed to index links", "err", err)
	}

	// Embed the new document if an AI provider is available
	embedNewDocument(v, doc)

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

	slog.Info("document created", "type", doc.Type, "path", doc.Path, "title", doc.Title)
	fmt.Printf("Created %s: %s\n", doc.Type, doc.Path)
	if !flagPorcelain {
		fmt.Fprintf(os.Stderr, "  Edit: open %s\n", filepath.Join(v.Root, doc.Path))
		fmt.Fprintf(os.Stderr, "  Read: 2nb read %s\n", doc.Path)
	}
	return nil
}

// embedNewDocument tries to embed a single document inline using its in-memory body.
// Silently skips if no AI provider is configured or available.
func embedNewDocument(v *vault.Vault, doc *document.Document) {
	cfg := v.Config.AI
	initAIProviders(v)

	embedder, err := ai.DefaultRegistry.Embedder(cfg.Provider)
	if err != nil {
		return
	}

	ctx := context.Background()
	if !embedder.Available(ctx) {
		return
	}

	vecs, err := embedder.Embed(ctx, []string{doc.Body})
	if err != nil {
		return
	}

	if err := v.DB.SetEmbedding(doc.ID, vecs[0], cfg.EmbeddingModel, doc.ContentHash); err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to store embedding: %v\n", err)
		slog.Warn("failed to store embedding", "err", err)
	}
}
