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
	metaSet    []string
	metaGet    string
	metaRemove []string
)

var metaCmd = &cobra.Command{
	Use:   "meta <path>",
	Short: "View, read, set, or remove document frontmatter",
	Long: `View a document's frontmatter, or operate on a single field.

With no flags, meta prints the full frontmatter. --get reads one field;
--set writes one (schema-validated); --remove deletes one in place, preserving
comments and key order. --get is read-only and cannot be combined with --set or
--remove, and --set and --remove are not combined in one invocation.

Identity keys (id, path, title, type) and any schema-required field cannot be
removed.`,
	Example: `  2nb meta note.md                        # print full frontmatter
  2nb meta note.md --get status           # read one field (exit 1 if absent)
  2nb meta note.md --set status=complete  # write one field
  2nb meta note.md --remove draft         # delete one field in place`,
	Args:              cobra.ExactArgs(1),
	ValidArgsFunction: completeDocPaths,
	RunE:              runMeta,
}

func init() {
	metaCmd.Flags().StringArrayVar(&metaSet, "set", nil, "Set a frontmatter field (key=value)")
	metaCmd.Flags().StringVar(&metaGet, "get", "", "Read a single frontmatter field by key (read-only)")
	metaCmd.Flags().StringArrayVar(&metaRemove, "remove", nil, "Remove a frontmatter field by key (repeatable)")
	_ = metaCmd.RegisterFlagCompletionFunc("set", completeMetaSetKeys)
	_ = metaCmd.RegisterFlagCompletionFunc("get", completeMetaSetKeys)
	_ = metaCmd.RegisterFlagCompletionFunc("remove", completeMetaSetKeys)
	metaCmd.GroupID = "docs"
	rootCmd.AddCommand(metaCmd)
}

func runMeta(cmd *cobra.Command, args []string) error {
	v, err := openVaultAndSetActive()
	if err != nil {
		return err
	}
	defer v.Close()

	path := v.AbsPath(expandPath(args[0]))
	doc, err := document.ParseFile(path)
	if err != nil {
		return exitWithError(ExitNotFound, fmt.Sprintf("error: %v", err))
	}

	doc.Path = v.RelPath(path)

	// --get is read-only and takes precedence: it never opens the write path,
	// so combining it with --set/--remove would be ambiguous. Reject the combo.
	if metaGet != "" {
		if len(metaSet) > 0 || len(metaRemove) > 0 {
			return exitWithError(ExitValidation, "error: --get cannot be combined with --set or --remove")
		}
		return getMeta(cmd, doc)
	}

	// --set and --remove each rewrite the whole file via one Serialize() pass;
	// running both in one invocation would mean two writes with overlapping
	// intent. Keep one write path per invocation.
	if len(metaSet) > 0 && len(metaRemove) > 0 {
		return exitWithError(ExitValidation, "error: --set cannot be combined with --remove (run them as separate invocations)")
	}

	if len(metaRemove) > 0 {
		return removeMeta(cmd, v, doc, path)
	}

	// If --set flags provided, update fields
	if len(metaSet) > 0 {
		return updateMeta(cmd, v, doc, path)
	}

	// Otherwise, display frontmatter
	format := getFormat(cmd)
	return output.Write(os.Stdout, format, doc.Frontmatter)
}

// getMeta prints a single frontmatter value. With a machine-readable --format
// it emits the raw scalar/array via output.Write; otherwise it prints a plain
// representation. A missing key exits ExitNotFound so scripts can branch on it.
func getMeta(cmd *cobra.Command, doc *document.Document) error {
	val, ok := doc.Frontmatter[metaGet]
	if !ok {
		return exitWithError(ExitNotFound, fmt.Sprintf("error: frontmatter key %q not found", metaGet))
	}

	if format := getFormat(cmd); format != "" {
		return output.Write(os.Stdout, format, val)
	}

	// Pretty (default) output: print the value plainly. Arrays print one item
	// per line so the common `tags` case reads naturally in a terminal.
	switch t := val.(type) {
	case []any:
		for _, item := range t {
			fmt.Println(item)
		}
	default:
		fmt.Println(val)
	}
	return nil
}

func updateMeta(cmd *cobra.Command, v *vault.Vault, doc *document.Document, absPath string) error {
	// .canvas/.base files are parsed into a read-only synthetic view. Writing
	// one back would overwrite the original JSON/YAML with markdown.
	if document.IsReadOnlyType(doc.Type) {
		return exitWithError(ExitValidation, fmt.Sprintf("error: cannot edit metadata of a read-only %s file (%s); .canvas/.base files are indexed read-only", doc.Type, doc.Path))
	}

	for _, kv := range metaSet {
		parts := strings.SplitN(kv, "=", 2)
		if len(parts) != 2 {
			return exitWithError(ExitValidation, fmt.Sprintf("invalid --set format: %q (expected key=value)", kv))
		}
		key, value := parts[0], parts[1]

		// Validate against schema
		if err := v.Schemas.ValidateField(doc.Type, key, value); err != nil {
			return exitWithError(ExitValidation, fmt.Sprintf("validation error: %v", err))
		}

		// Validate status transitions
		if key == "status" && doc.Status != "" {
			if err := v.Schemas.ValidateStatusTransition(doc.Type, doc.Status, value); err != nil {
				return exitWithError(ExitValidation, fmt.Sprintf("validation error: %v", err))
			}
		}

		doc.SetMeta(key, value)
	}

	// Write back. Serialize reads the on-disk file (by doc.Path) to surgically
	// preserve YAML comments and key order; point it at the absolute path so
	// this works from any cwd, then restore the vault-relative path for indexing.
	rel := doc.Path
	doc.Path = absPath
	content, err := doc.Serialize()
	doc.Path = rel
	if err != nil {
		return fmt.Errorf("serialize document: %w", err)
	}

	tmp := absPath + ".tmp"
	if err := os.WriteFile(tmp, content, 0o644); err != nil {
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := os.Rename(tmp, absPath); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("rename: %w", err)
	}

	// Re-index
	if err := v.DB.UpsertDocument(doc); err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to update index: %v\n", err)
	}

	format := getFormat(cmd)
	return output.Write(os.Stdout, format, doc.Frontmatter)
}

// metaProtectedKeys are frontmatter keys that anchor a document's identity and
// schema and so can never be removed: id and title are needed to resolve and
// label a doc, type drives schema/template selection, and path mirrors the
// on-disk location. Schema-required fields are rejected separately via v.Schemas.
var metaProtectedKeys = map[string]bool{
	"id":    true,
	"path":  true,
	"title": true,
	"type":  true,
}

// removeMeta deletes one or more frontmatter keys and rewrites the file in
// place, reusing the exact atomic temp+rename write path as updateMeta. The
// surgical AST rewrite (document.UpdateDocumentFrontmatterAST, via
// doc.Serialize) drops the removed keys while preserving comments and the order
// of every untouched key. Identity keys (id/path/title/type) and any
// schema-Required field are refused.
func removeMeta(cmd *cobra.Command, v *vault.Vault, doc *document.Document, absPath string) error {
	// .canvas/.base files are parsed into a read-only synthetic view. Writing
	// one back would overwrite the original JSON/YAML with markdown.
	if document.IsReadOnlyType(doc.Type) {
		return exitWithError(ExitValidation, fmt.Sprintf("error: cannot edit metadata of a read-only %s file (%s); .canvas/.base files are indexed read-only", doc.Type, doc.Path))
	}

	// Schema-required fields for this doc type must stay present.
	required := map[string]bool{}
	if schema, ok := v.Schemas.Types[doc.Type]; ok {
		for _, r := range schema.Required {
			required[r] = true
		}
	}

	for _, key := range metaRemove {
		if key == "" {
			return exitWithError(ExitValidation, "error: --remove requires a non-empty key")
		}
		if metaProtectedKeys[key] {
			return exitWithError(ExitValidation, fmt.Sprintf("error: cannot remove identity key %q", key))
		}
		if required[key] {
			return exitWithError(ExitValidation, fmt.Sprintf("error: cannot remove %q: required by the %q schema", key, doc.Type))
		}
		if _, ok := doc.Frontmatter[key]; !ok {
			return exitWithError(ExitNotFound, fmt.Sprintf("error: frontmatter key %q not found", key))
		}

		delete(doc.Frontmatter, key)

		// Mirror SetMeta's struct-field sync in reverse: clearing a key that
		// shadows a struct field must also clear that field so the re-index
		// (UpsertDocument reads the struct, not Frontmatter) stays consistent.
		switch key {
		case "title":
			doc.Title = ""
		case "type":
			doc.Type = ""
		case "status":
			doc.Status = ""
		case "tags":
			doc.Tags = nil
		}
	}

	// Write back. Serialize reads the on-disk file (by doc.Path) to surgically
	// preserve YAML comments and key order; point it at the absolute path so
	// this works from any cwd, then restore the vault-relative path for indexing.
	rel := doc.Path
	doc.Path = absPath
	content, err := doc.Serialize()
	doc.Path = rel
	if err != nil {
		return fmt.Errorf("serialize document: %w", err)
	}

	tmp := absPath + ".tmp"
	if err := os.WriteFile(tmp, content, 0o644); err != nil {
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := os.Rename(tmp, absPath); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("rename: %w", err)
	}

	// Re-index
	if err := v.DB.UpsertDocument(doc); err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to update index: %v\n", err)
	}

	format := getFormat(cmd)
	return output.Write(os.Stdout, format, doc.Frontmatter)
}
