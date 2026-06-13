package cli

import (
	"fmt"
	"os"

	"github.com/apresai/2ndbrain/internal/document"
	"github.com/apresai/2ndbrain/internal/output"
	"github.com/apresai/2ndbrain/internal/vault"
	"github.com/spf13/cobra"
)

// tagsCmd is a parent command so future tag operations (e.g. `tags rename`)
// can attach as subcommands. Invoked bare, it lists every tag with its count.
var tagsCmd = &cobra.Command{
	Use:   "tags",
	Short: "List all tags in the vault with document counts",
	Long: `List every frontmatter tag in the vault with how many documents carry it,
ordered by descending count. Tags are a parent command: subcommands attach for
future tag operations.`,
	Example: `  2nb tags
  2nb tags --json`,
	// Default action when invoked without a subcommand: list tags.
	RunE: runTagsList,
}

var tagsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all tags in the vault with document counts",
	RunE:  runTagsList,
}

var tagsRenameDryRun bool

var tagsRenameCmd = &cobra.Command{
	Use:   "rename <old> <new>",
	Short: "Rename a frontmatter tag across every document that carries it",
	Long: `Rewrite the frontmatter "tags" array of every document carrying <old>,
replacing it with <new>. If a document already has <new>, the rename dedupes
(the doc keeps a single <new> and drops <old>). Each file is rewritten in place
with an atomic temp+rename and reindexed so the tags table reflects the change.

v1 is FRONTMATTER-ONLY: inline body tags written as #old are NOT rewritten. A
document whose <old> tag lives only inline (no frontmatter "tags" entry) is
skipped and reported under "skipped". Use --dry-run to preview the affected
documents and counts without writing anything.

Processing is per-file and per-file atomic: on any failure the command exits
non-zero but already-rewritten files are NOT rolled back (each file either fully
succeeded or was left untouched).`,
	Example: `  2nb tags rename draft in-progress
  2nb tags rename draft in-progress --dry-run
  2nb tags rename old new --json`,
	Args: cobra.ExactArgs(2),
	RunE: runTagsRename,
}

func init() {
	tagsCmd.GroupID = "docs"
	tagsRenameCmd.Flags().BoolVar(&tagsRenameDryRun, "dry-run", false, "List affected documents and counts without writing")
	tagsCmd.AddCommand(tagsListCmd)
	tagsCmd.AddCommand(tagsRenameCmd)
	rootCmd.AddCommand(tagsCmd)
}

func runTagsList(cmd *cobra.Command, args []string) error {
	v, err := openVault()
	if err != nil {
		return err
	}
	defer v.Close()

	tags, err := v.DB.TagCounts()
	if err != nil {
		return err
	}

	format := getFormat(cmd)
	return output.Write(os.Stdout, format, tags)
}

// tagsRenameFailure is one document that could not be rewritten, with the error.
type tagsRenameFailure struct {
	Path  string `json:"path"`
	Error string `json:"error"`
}

// tagsRenameResult is the summary emitted by `tags rename`. Renamed lists the
// vault-relative paths that were rewritten (or, under --dry-run, would be);
// Skipped lists docs that surfaced for the tag but had no frontmatter "tags"
// entry to rewrite (the tag lived only inline); Failed lists per-file errors.
// DryRun echoes whether this was a preview.
type tagsRenameResult struct {
	Old     string              `json:"old"`
	New     string              `json:"new"`
	DryRun  bool                `json:"dry_run"`
	Renamed []string            `json:"renamed"`
	Skipped []string            `json:"skipped"`
	Failed  []tagsRenameFailure `json:"failed"`
}

func runTagsRename(cmd *cobra.Command, args []string) error {
	old, newTag := args[0], args[1]
	if old == "" || newTag == "" {
		return exitWithError(ExitValidation, "error: both <old> and <new> tags must be non-empty")
	}
	if old == newTag {
		return exitWithError(ExitValidation, "error: <old> and <new> are identical; nothing to rename")
	}

	// --dry-run only reads; a real rename rewrites files, so it pins the active
	// vault (matching meta/append/polish --write).
	var v *vault.Vault
	var err error
	if tagsRenameDryRun {
		v, err = openVault()
	} else {
		v, err = openVaultAndSetActive()
	}
	if err != nil {
		return err
	}
	defer v.Close()
	setupFileLogging(v)

	refs, err := v.DB.DocsWithTag(old)
	if err != nil {
		return fmt.Errorf("find docs with tag %q: %w", old, err)
	}

	result := tagsRenameResult{
		Old:     old,
		New:     newTag,
		DryRun:  tagsRenameDryRun,
		Renamed: []string{},
		Skipped: []string{},
		Failed:  []tagsRenameFailure{},
	}

	for _, ref := range refs {
		absPath := v.AbsPath(ref.Path)
		doc, perr := document.ParseFile(absPath)
		if perr != nil {
			result.Failed = append(result.Failed, tagsRenameFailure{Path: ref.Path, Error: perr.Error()})
			continue
		}
		doc.Path = ref.Path

		// .canvas/.base parse into read-only synthetic views; never rewrite them.
		if document.IsReadOnlyType(doc.Type) {
			result.Skipped = append(result.Skipped, ref.Path)
			continue
		}

		newTags, changed := renameTagInFrontmatter(doc.Frontmatter, old, newTag)
		if !changed {
			// The doc surfaced via the tags table but carries no frontmatter
			// "tags" entry for <old> (it lived only inline). v1 is
			// frontmatter-only, so skip it.
			result.Skipped = append(result.Skipped, ref.Path)
			continue
		}

		if tagsRenameDryRun {
			result.Renamed = append(result.Renamed, ref.Path)
			continue
		}

		doc.SetMeta("tags", newTags)
		if werr := writeTagsFrontmatter(v, doc, absPath); werr != nil {
			result.Failed = append(result.Failed, tagsRenameFailure{Path: ref.Path, Error: werr.Error()})
			continue
		}
		result.Renamed = append(result.Renamed, ref.Path)
	}

	format := getFormat(cmd)
	if format != "" {
		if err := output.Write(os.Stdout, format, result); err != nil {
			return err
		}
	} else {
		verb := "Renamed"
		if tagsRenameDryRun {
			verb = "Would rename"
		}
		fmt.Fprintf(os.Stderr, "%s tag %q -> %q in %d document(s); skipped %d; failed %d.\n",
			verb, old, newTag, len(result.Renamed), len(result.Skipped), len(result.Failed))
		for _, p := range result.Renamed {
			fmt.Println(p)
		}
	}

	// Per-file atomic, no rollback: exit non-zero on any failure so scripts can
	// branch, but the files that succeeded stay written.
	if len(result.Failed) > 0 {
		return exitWithError(ExitValidation, fmt.Sprintf("error: %d document(s) failed to rewrite", len(result.Failed)))
	}
	return nil
}

// renameTagInFrontmatter computes the new frontmatter "tags" slice for a rename,
// returning the result and whether anything changed. It operates only on the
// frontmatter "tags" field (extracted via the same logic the indexer uses). When
// <old> is present it is replaced with <new>; if <new> already appears the result
// dedupes (a single <new>, <old> dropped). Order is otherwise preserved. Returns
// changed=false when <old> is not in the frontmatter tags at all.
func renameTagInFrontmatter(fm map[string]any, old, newTag string) ([]string, bool) {
	current := frontmatterTags(fm)
	hasOld := false
	for _, t := range current {
		if t == old {
			hasOld = true
			break
		}
	}
	if !hasOld {
		return nil, false
	}

	out := make([]string, 0, len(current))
	seen := make(map[string]bool, len(current))
	for _, t := range current {
		v := t
		if v == old {
			v = newTag
		}
		if seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out, true
}

// frontmatterTags extracts the frontmatter "tags" field as a string slice,
// handling the array form (tags: [a, b]) and the bare-string form (tags: a). It
// deliberately reads only the frontmatter map, not the indexer's merged
// frontmatter+inline view, because `tags rename` is frontmatter-only.
func frontmatterTags(fm map[string]any) []string {
	raw, ok := fm["tags"]
	if !ok {
		return nil
	}
	switch v := raw.(type) {
	case []any:
		tags := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				tags = append(tags, s)
			}
		}
		return tags
	case []string:
		return v
	case string:
		if v == "" {
			return nil
		}
		return []string{v}
	default:
		return nil
	}
}

// writeTagsFrontmatter rewrites a document's frontmatter (the "tags" field was
// just updated via SetMeta) and reindexes it. It mirrors meta.go's atomic
// temp+rename Serialize dance, but reindexes via vault.IndexSingleFile rather
// than DB.UpsertDocument: a tags change must rebuild the tags table, and only
// the full single-file reindex does that (UpsertDocument touches documents only).
// The body is unchanged, so there is no re-embed (Serialize preserves doc.Body
// and comments/key order in the frontmatter).
func writeTagsFrontmatter(v *vault.Vault, doc *document.Document, absPath string) error {
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

	// Reindex this file so the tags table reflects the renamed tag.
	if err := vault.IndexSingleFile(v, absPath); err != nil {
		return fmt.Errorf("reindex after tag rename: %w", err)
	}
	return nil
}
