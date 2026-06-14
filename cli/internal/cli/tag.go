package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/apresai/2ndbrain/internal/document"
	"github.com/apresai/2ndbrain/internal/output"
	"github.com/spf13/cobra"
)

// splitCSV splits a comma-separated value into trimmed, non-empty parts. Used to
// coerce a CLI list-field value (`tags=a, b ,c`) into array elements; an empty or
// all-blank input yields an empty slice (which clears the field on `meta --set`).
// Shared by `meta --set` array coercion and the `tag add`/`tag remove` commands.
func splitCSV(value string) []string {
	raw := strings.Split(value, ",")
	out := make([]string, 0, len(raw))
	for _, p := range raw {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// `tag` is the singular, per-note counterpart to the plural `tags` (which lists
// and renames vault-wide). It mirrors the task/tasks split: `tag add`/`tag remove`
// mutate one note's frontmatter `tags` array, schema-validated and reindexed via
// the shared body/tags write path so the change is immediately searchable by
// `2nb list --tag` (no manual reindex). Frontmatter tags only (v1), matching
// `tags rename`; inline `#tags` in the body are not rewritten.

var tagCmd = &cobra.Command{
	Use:   "tag",
	Short: "Add or remove a frontmatter tag on a note",
	Long: `Add or remove tags on a single note's frontmatter, reindexing so the change
is immediately reflected in 2nb list --tag.

This is the per-note counterpart to the vault-wide tags command (tags list /
tags rename). Tags can be passed as separate arguments or comma-separated.`,
	Example: `  2nb tag add notes/auth.md security oauth
  2nb tag add notes/auth.md security,oauth        # comma-separated
  2nb tag remove notes/auth.md oauth`,
}

var tagAddCmd = &cobra.Command{
	Use:               "add <note> <tag>...",
	Short:             "Add one or more tags to a note's frontmatter",
	Args:              cobra.MinimumNArgs(2),
	ValidArgsFunction: completeDocPaths,
	RunE:              func(cmd *cobra.Command, args []string) error { return runTagMutate(cmd, args, true) },
}

var tagRemoveCmd = &cobra.Command{
	Use:               "remove <note> <tag>...",
	Short:             "Remove one or more tags from a note's frontmatter",
	Args:              cobra.MinimumNArgs(2),
	ValidArgsFunction: completeDocPaths,
	RunE:              func(cmd *cobra.Command, args []string) error { return runTagMutate(cmd, args, false) },
}

func init() {
	tagCmd.AddCommand(tagAddCmd)
	tagCmd.AddCommand(tagRemoveCmd)
	tagCmd.GroupID = "docs"
	rootCmd.AddCommand(tagCmd)
}

func runTagMutate(cmd *cobra.Command, args []string, add bool) error {
	op := "remove"
	if add {
		op = "add"
	}

	// Tags come from args[1:], each of which may itself be comma-separated.
	var tagArgs []string
	for _, a := range args[1:] {
		tagArgs = append(tagArgs, splitCSV(a)...)
	}
	if len(tagArgs) == 0 {
		return exitWithError(ExitValidation, "error: at least one tag is required")
	}

	v, err := openVaultAndSetActive()
	if err != nil {
		return err
	}
	defer v.Close()
	setupFileLogging(v)

	absPath, relPath, err := resolveTargetArg(v, args[0])
	if err != nil {
		return err
	}
	doc, err := document.ParseFile(absPath)
	if err != nil {
		return exitWithError(ExitNotFound, fmt.Sprintf("error: %v", err))
	}
	doc.Path = relPath

	// .canvas/.base are read-only synthetic views; writing one back would
	// overwrite the original JSON/YAML with markdown.
	if document.IsReadOnlyType(doc.Type) {
		return exitWithError(ExitValidation, fmt.Sprintf("error: cannot edit tags of a read-only %s file (%s); .canvas/.base files are indexed read-only", doc.Type, relPath))
	}

	current := frontmatterTags(doc.Frontmatter)
	var newTags []string
	if add {
		// Validate each new tag against the schema (tags carry no enum by
		// default, so this is a no-op unless a type constrains them).
		for _, t := range tagArgs {
			if err := v.Schemas.ValidateField(doc.Type, "tags", t); err != nil {
				return exitWithError(ExitValidation, fmt.Sprintf("validation error: %v", err))
			}
		}
		newTags = mergeTagsList(current, tagArgs)
	} else {
		newTags = removeTagsList(current, tagArgs)
	}

	if sameStringSlice(current, newTags) {
		return reportTagResult(cmd, relPath, doc.Title, newTags, op, false)
	}

	// Store as a YAML list ([]any matches NewDocument's tags initialization).
	arr := make([]any, len(newTags))
	for i, t := range newTags {
		arr[i] = t
	}
	doc.SetMeta("tags", arr)

	if err := writeTagsFrontmatter(v, doc, absPath); err != nil {
		return err
	}
	return reportTagResult(cmd, relPath, doc.Title, newTags, op, true)
}

// mergeTagsList appends add-tags to current, deduped, preserving order (existing
// first, then new tags not already present).
func mergeTagsList(current, add []string) []string {
	seen := make(map[string]bool, len(current))
	out := make([]string, 0, len(current)+len(add))
	for _, t := range current {
		if !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	for _, t := range add {
		if !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	return out
}

// removeTagsList drops the named tags from current, preserving order.
func removeTagsList(current, remove []string) []string {
	drop := make(map[string]bool, len(remove))
	for _, t := range remove {
		drop[t] = true
	}
	out := make([]string, 0, len(current))
	for _, t := range current {
		if !drop[t] {
			out = append(out, t)
		}
	}
	return out
}

func sameStringSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// reportTagResult emits the standard result for a tag mutation: a JSON object
// under a machine format, otherwise a one-line confirmation.
func reportTagResult(cmd *cobra.Command, path, title string, tags []string, op string, changed bool) error {
	format := getFormat(cmd)
	if format != "" {
		return output.Write(os.Stdout, format, map[string]any{
			"path":      path,
			"title":     title,
			"tags":      tags,
			"operation": op,
			"changed":   changed,
		})
	}
	if !changed {
		fmt.Printf("%s: %s (unchanged)\n", op, path)
		return nil
	}
	fmt.Printf("%s: %s %v\n", op, path, tags)
	return nil
}
