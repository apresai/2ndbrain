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

var metaSet []string

var metaCmd = &cobra.Command{
	Use:   "meta <path>",
	Short: "View or update document frontmatter",
	Args:  cobra.ExactArgs(1),
	RunE:  runMeta,
}

func init() {
	metaCmd.Flags().StringArrayVar(&metaSet, "set", nil, "Set a frontmatter field (key=value)")
	rootCmd.AddCommand(metaCmd)
}

func runMeta(cmd *cobra.Command, args []string) error {
	v, err := vault.Open(".")
	if err != nil {
		return fmt.Errorf("open vault: %w", err)
	}
	defer v.Close()

	path := v.AbsPath(args[0])
	doc, err := document.ParseFile(path)
	if err != nil {
		return exitWithError(ExitNotFound, fmt.Sprintf("error: %v", err))
	}

	doc.Path = v.RelPath(path)

	// If --set flags provided, update fields
	if len(metaSet) > 0 {
		return updateMeta(cmd, v, doc, path)
	}

	// Otherwise, display frontmatter
	format := getFormat(cmd)
	return output.Write(os.Stdout, format, doc.Frontmatter)
}

func updateMeta(cmd *cobra.Command, v *vault.Vault, doc *document.Document, absPath string) error {
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

	// Write back
	content, err := doc.Serialize()
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
