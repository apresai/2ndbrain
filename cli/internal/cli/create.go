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
	"github.com/apresai/2ndbrain/internal/embed"
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
	createPath           string
	createAllowDuplicate bool
	createContent        string
	createOverwrite      bool
	createAppend         bool
)

var createCmd = &cobra.Command{
	Use:   "create [title]",
	Short: "Create a new document from a template",
	Long:  "Create a new document. Title can be a positional argument or --title flag. Document types come from the vault's schemas.yaml — the built-in types are adr, runbook, note, postmortem, prd, prfaq.",
	Example: `  2nb create "My New Note"                         # default type: note
  2nb create --type adr "Use JWT for Auth"         # architecture decision record
  2nb create --type runbook "Deploy Rotation"      # ops runbook
  2nb create --type prd "Search Ranking v2"        # product requirements doc
  2nb create --type note --path resources "API Keys"  # file under resources/`,
	Args: cobra.MaximumNArgs(1),
	RunE: runCreate,
}

func init() {
	createCmd.Flags().StringVar(&createType, "type", "note", "Document type (adr, runbook, note, postmortem, prd, prfaq)")
	createCmd.Flags().StringVar(&createTitle, "title", "", "Document title")
	createCmd.Flags().StringVar(&createPath, "path", "", "Vault-relative subdirectory to create the document in (created if missing)")
	createCmd.Flags().BoolVar(&createAllowDuplicate, "allow-duplicate", false, "Allow creating a document with duplicate content")
	createCmd.Flags().StringVar(&createContent, "content", "", "Initial content for the document")
	createCmd.Flags().BoolVar(&createOverwrite, "overwrite", false, "If a note with this title already exists, replace its contents instead of creating a new file")
	createCmd.Flags().BoolVar(&createAppend, "append", false, "If a note with this title already exists, append the content to it instead of creating a new file")
	createCmd.MarkFlagsMutuallyExclusive("overwrite", "append")
	_ = createCmd.RegisterFlagCompletionFunc("type", completeSchemaTypes)
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

	body := tmplBody
	if cmd.Flags().Changed("content") {
		body = createContent
	}

	// Resolve the target directory first, since it's needed to locate an existing
	// note for --append/--overwrite. --path places the document in a
	// vault-relative subdirectory (created on write by WriteFile). It's
	// user-supplied, so reject absolute paths and guard against ".." escapes
	// with the same ContainsPath check the MCP handlers use.
	writeDir := v.Root
	if createPath != "" {
		if filepath.IsAbs(createPath) {
			return fmt.Errorf("--path must be a vault-relative subdirectory, not an absolute path: %q", createPath)
		}
		writeDir = v.AbsPath(createPath)
		if !v.ContainsPath(writeDir) {
			return fmt.Errorf("--path escapes the vault: %q", createPath)
		}
	}

	// --append / --overwrite: when a note with this title already exists, edit
	// THAT note in place via the shared body-write path (which preserves the
	// note's frontmatter and identity, reindexes, and re-embeds) instead of
	// minting a second note. --append adds to the body; --overwrite replaces the
	// body. Absent the note, both fall through to a normal create.
	if createAppend || createOverwrite {
		if existingAbs, ok := existingTitleNote(writeDir, createTitle); ok {
			return editExistingNote(cmd, v, existingAbs, body, createOverwrite)
		}
	}

	doc := document.NewDocument(createTitle, createType, body)
	doc.SetMeta("status", initialStatus)
	doc.ComputeContentHash()

	// Duplicate-content guard (skipped when --allow-duplicate is set).
	if !createAllowDuplicate {
		dupPath, err := v.DB.FindByContentHash(doc.ContentHash, "")
		if err != nil {
			return fmt.Errorf("check duplicates: %w", err)
		}
		if dupPath != "" {
			return fmt.Errorf("duplicate content: matches existing document %q\nUse --allow-duplicate to create anyway", dupPath)
		}
	}

	path, err := doc.WriteFile(writeDir)
	if err != nil {
		return fmt.Errorf("write document: %w", err)
	}

	doc.Path = v.RelPath(path)

	// Index the new document. IndexSingleFile wraps document/chunks/tags/links
	// in one transaction so a failure can't leave the index half-populated.
	if err := vault.IndexSingleFile(v, path); err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to index document: %v\n", err)
		slog.Warn("failed to index document", "err", err)
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

// existingTitleNote returns the absolute path of an existing note whose filename
// matches the canonical slug for title in dir, and whether it exists. Returns
// false for titles that produce no ASCII slug (those are stored under a UUID
// name and can't be located by title) and for directories.
func existingTitleNote(dir, title string) (string, bool) {
	name := document.SlugFilename(title)
	if name == "" {
		return "", false
	}
	abs := filepath.Join(dir, name)
	if info, err := os.Stat(abs); err == nil && !info.IsDir() {
		return abs, true
	}
	return "", false
}

// editExistingNote edits an existing note's body in place via the shared
// body-write path, which preserves the note's frontmatter (id, created, tags,
// status, aliases, custom fields) and only changes the body. When overwrite is
// true the body is replaced; otherwise the content is appended. Used by
// `create --overwrite`/`--append` when the target note already exists.
func editExistingNote(cmd *cobra.Command, v *vault.Vault, absPath, content string, overwrite bool) error {
	doc, err := document.ParseFile(absPath)
	if err != nil {
		return exitWithError(ExitNotFound, fmt.Sprintf("error: %v", err))
	}
	doc.Path = v.RelPath(absPath)
	op := "append"
	if overwrite {
		doc.Body = content
		op = "overwrite"
	} else {
		doc.Body = document.AppendToBody(doc.Body, content)
	}
	if err := writeBody(v, doc, absPath); err != nil {
		return err
	}
	return reportBodyWrite(cmd, doc, op)
}

// embedNewDocument tries to embed a single document inline using its in-memory body.
// Skips (without failing the write) if no AI provider is configured or available,
// or if embedding errors. Such a skip is not lost: the document's content_hash
// no longer matches its (absent) embedding_hash, so the next `2nb index` (including
// the macOS app's startup/auto sync) backfills it via DocumentsNeedingEmbedding.
// The skips are logged at debug level so a missing embedding is traceable in
// .2ndbrain/logs/cli.log instead of vanishing silently.
func embedNewDocument(v *vault.Vault, doc *document.Document) {
	cfg := v.Config.AI
	initAIProviders(v)

	embedder, err := ai.DefaultRegistry.Embedder(cfg.Provider)
	if err != nil {
		slog.Debug("inline embed skipped: no embedder", "path", doc.Path, "provider", cfg.Provider, "err", err)
		return
	}

	ctx := context.Background()
	if !embedder.Available(ctx) {
		slog.Debug("inline embed skipped: provider unavailable; will backfill on next index", "path", doc.Path, "provider", cfg.Provider)
		return
	}

	// Per-chunk embed via the shared path (vec_chunks + mean doc vector), so a
	// freshly-created note is immediately searchable through vec0. Best-effort:
	// a failure backfills on the next index.
	if _, err := embed.Document(ctx, v.DB, embedder, doc.ID, doc, cfg.EmbeddingModel); err != nil {
		slog.Debug("inline embed skipped: embed error; will backfill on next index", "path", doc.Path, "provider", cfg.Provider, "err", err)
	}
}
