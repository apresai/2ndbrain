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
	Use:     "meta <path>",
	Aliases: []string{"frontmatter", "fm", "properties"},
	Short:   "View, read, set, or remove document frontmatter",
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
	Args:              exactArgsHint(1, metaArgsHint),
	ValidArgsFunction: completeDocPaths,
	RunE:              runMeta,
}

// metaArgsHint builds the error for a malformed `meta` invocation. When the
// first positional is a stale verb (set/get/remove) that the preprocessor did
// not rewrite (e.g. a missing value), it points at the exact flag form;
// otherwise it prints the general usage. cobra prepends "Error: ", so the
// message carries no prefix of its own.
func metaArgsHint(args []string) string {
	var b strings.Builder
	if len(args) > 1 {
		switch args[0] {
		case "set", "get", "remove":
			fmt.Fprintf(&b, "`meta` has no `%s` subcommand — use the flag form:\n", args[0])
			b.WriteString("  2nb meta <path> --set key=value    # write a field\n")
			b.WriteString("  2nb meta <path> --get key          # read a field\n")
			b.WriteString("  2nb meta <path> --remove key       # delete a field")
			return b.String()
		}
	}
	fmt.Fprintf(&b, "meta takes exactly one <path> argument (got %d)\n", len(args))
	b.WriteString("  2nb meta <path>                    # view frontmatter\n")
	b.WriteString("  2nb meta <path> --set key=value    # write a field\n")
	b.WriteString("  2nb meta <path> --get key          # read a field\n")
	b.WriteString("  2nb meta <path> --remove key       # delete a field")
	return b.String()
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
	// Validate the flag combination BEFORE opening anything: which opener this
	// command needs is decided by the flags, so an ambiguous combination has to
	// be refused first.
	//
	// --get is read-only and takes precedence, so combining it with
	// --set/--remove would be ambiguous. Reject the combo.
	if metaGet != "" && (len(metaSet) > 0 || len(metaRemove) > 0) {
		return exitWithError(ExitValidation, "error: --get cannot be combined with --set or --remove")
	}
	// --set and --remove each rewrite the whole file via one Serialize() pass;
	// running both in one invocation would mean two writes with overlapping
	// intent. Keep one write path per invocation.
	if len(metaSet) > 0 && len(metaRemove) > 0 {
		return exitWithError(ExitValidation, "error: --set cannot be combined with --remove (run them as separate invocations)")
	}

	// Pick the opener from what this invocation actually does. --get and the
	// bare view only READ frontmatter, so they take openVault(); only --set and
	// --remove rewrite the file and need the write guard. Opening every meta
	// invocation through the write path made `meta <p> --get title` and bare
	// `meta <p>` refuse with "refusing to write" on any vault Obsidian does not
	// know, which the app, the plugin, and the MCP server hit constantly since
	// they all pin --vault. Same idiom as polish.go and tags.go.
	writes := len(metaSet) > 0 || len(metaRemove) > 0
	open := openVault
	if writes {
		open = openVaultAndSetActive
	}
	v, err := open()
	if err != nil {
		return err
	}
	defer v.Close()

	path, _, err := resolveTargetArg(v, args[0])
	if err != nil {
		return err
	}
	doc, err := document.ParseFile(path)
	if err != nil {
		return exitWithError(ExitNotFound, fmt.Sprintf("error: %v", err))
	}

	doc.Path = v.RelPath(path)

	if metaGet != "" {
		return getMeta(cmd, doc)
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
	return writeOut(cmd, format, doc.Frontmatter)
}

// getMeta prints a single frontmatter value. With a machine-readable --format
// it emits the raw scalar/array via output.Write; otherwise it prints a plain
// representation. A missing key exits ExitNotFound so scripts can branch on it.
func getMeta(cmd *cobra.Command, doc *document.Document) error {
	val, ok := doc.Frontmatter[metaGet]
	if !ok {
		return exitWithError(ExitNotFound, fmt.Sprintf("error: frontmatter key %q not found", metaGet))
	}

	// What the FIELD says, which is not always what the parsed value renders
	// as: yaml.v3 resolves an unquoted `2026-09-04` to a time.Time, and every
	// rendering of that instant is `2026-09-04T00:00:00Z`. `meta --get` answers
	// "what does this field say", so it answers with the note's own text.
	val = metaGetValue(doc, metaGet, val)

	if format := getFormat(cmd); format != "" {
		return writeOut(cmd, format, val)
	}

	// Pretty (default) output: print the value plainly. Arrays print one item
	// per line so the common `tags` case reads naturally in a terminal.
	var sb strings.Builder
	switch t := val.(type) {
	case []any:
		for _, item := range t {
			fmt.Fprintln(&sb, metaScalarLine(item))
		}
	default:
		fmt.Fprintln(&sb, metaScalarLine(val))
	}
	fmt.Print(sb.String())
	if flagCopy {
		return copyToClipboard(strings.TrimRight(sb.String(), "\n"))
	}
	return nil
}

// metaGetValue substitutes the note's VERBATIM text for a value the parsed form
// cannot reproduce, and leaves every other value exactly as parsed.
//
// The rule is fidelity, not type: a value is replaced only where rendering the
// PARSED value would differ from what the file says, which is precisely the
// lossy case (an unquoted date, whose text `2026-09-04` and whose resolved
// instant `2026-09-04T00:00:00Z` are different strings; an unquoted `007`,
// which resolves to the int 7). A string, a plain integer, a boolean and a
// float all render identically either way, so they keep their parsed type and
// `--json` still emits `42` rather than `"42"`.
//
// A list is handled element by element under the same rule, so a tag written
// `- 2026-09-04` reads back as `2026-09-04` while a numeric element stays a
// number. A non-scalar element (a nested list or mapping) has no text and is
// left alone.
func metaGetValue(doc *document.Document, key string, val any) any {
	if s, ok := doc.MetaText(key); ok {
		if parsed, ok := document.ScalarText(val); !ok || parsed != s {
			return s
		}
		return val
	}
	items, ok := val.([]any)
	if !ok {
		return val
	}
	out := make([]any, len(items))
	for i, item := range items {
		out[i] = item
		if s, ok := doc.MetaTextItem(key, i); ok {
			if parsed, ok := document.ScalarText(item); !ok || parsed != s {
				out[i] = s
			}
		}
	}
	return out
}

// metaScalarLine renders one frontmatter value for the DEFAULT (pretty) output
// of `meta --get`. Only this branch needed it: --json marshals a time.Time as
// RFC3339 and output.delimitedCell renders one through MarshalText, while plain
// %v prints Go's own "2020-01-01 00:00:00 +0000 UTC". So `meta --get modified`
// disagreed with every other view of the same note, and disagreed with itself
// depending on whether the date in the file was quoted. Anything ScalarText
// does not recognize (a nested mapping) keeps its %v form.
func metaScalarLine(v any) string {
	if s, ok := document.ScalarText(v); ok {
		return s
	}
	return fmt.Sprint(v)
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

		// Array-typed fields (tags, aliases, or any schema "list"/"tags" field)
		// must be stored as a YAML list, not a scalar. Without this, a multi-value
		// `--set tags=a,b` became the single literal tag "a,b" (so `list --tag a`
		// found nothing), and even a single value wrote a non-idiomatic scalar.
		// Comma-split with replace semantics (`--set tags=a,b` -> [a, b];
		// `--set tags=` clears). Validate each element so enum-constrained list
		// fields still validate. `status` is excluded: it is always a scalar
		// state-machine field, so a (pathological) schema declaring it `type: list`
		// must not let the array branch skip the status-transition validation below.
		if key != "status" && v.Schemas.IsListField(doc.Type, key) {
			parts := splitCSV(value)
			elems := make([]any, len(parts))
			for i, p := range parts {
				if err := v.Schemas.ValidateField(doc.Type, key, p); err != nil {
					return exitWithError(ExitValidation, fmt.Sprintf("validation error: %v", err))
				}
				elems[i] = p
			}
			doc.SetMeta(key, elems)
			continue
		}

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

		// A DATE field is stored as a time.Time so the writer emits it
		// UNQUOTED, which is what Obsidian types as Date and time. Writing the
		// raw CLI string here is what would silently requote a date node
		// `2nb create` had just written plain, one note at a time. Validation
		// runs on the text the user typed, above; only the STORED value
		// changes. Text that is not a date falls through and is stored
		// verbatim, exactly as before.
		if t, ok := v.Schemas.CoerceDate(doc.Type, key, value); ok {
			doc.SetMeta(key, t)
			continue
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
	if err := vault.IndexSingleFile(v, absPath); err != nil {
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

		doc.ForgetMetaText(key)
		delete(doc.Frontmatter, key)

		// Mirror SetMeta's struct-field sync in reverse: clearing a key that
		// shadows a struct field must also clear that field so the re-index
		// (UpsertDocument reads the struct, not Frontmatter) stays consistent.
		// created/modified are removable (not identity/required keys) and
		// UpsertDocument writes doc.CreatedAt/doc.ModifiedAt into the index, so
		// they must be cleared here too or the index keeps the stale timestamp
		// until the next full re-index.
		switch key {
		case "title":
			doc.Title = ""
		case "type":
			doc.Type = ""
		case "status":
			doc.Status = ""
		case "tags":
			doc.Tags = nil
		case "created":
			doc.CreatedAt = ""
		case "modified":
			doc.ModifiedAt = ""
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
	if err := vault.IndexSingleFile(v, absPath); err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to update index: %v\n", err)
	}

	format := getFormat(cmd)
	return output.Write(os.Stdout, format, doc.Frontmatter)
}
