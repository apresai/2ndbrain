package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/apresai/2ndbrain/internal/document"
	"github.com/apresai/2ndbrain/internal/polish"
	"github.com/apresai/2ndbrain/internal/vault"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

var migratePropertiesWrite bool

var obsidianMigratePropertiesCmd = &cobra.Command{
	Use:   "migrate-properties",
	Short: "Rewrite quoted date values into the plain form Obsidian types as Date and time",
	Long: `Obsidian infers a property's TYPE from how its value is written, and it reads a
QUOTED ISO value as Text: no date picker, no date sorting, no date-based query.
Every "created" and "modified" written by a 2nb before this release was quoted,
because Go strings are quoted by the YAML encoder whenever they would re-resolve
to a timestamp.

This is the one-time repair for notes already on disk. It rewrites ONLY the
properties 2nb owns ("created", "modified") plus any property the vault's own
schemas declare as "date" or "datetime". Properties you authored are reported
and left alone: a vault typically spells its own "date" field several ways, and
picking one of them is your call, not a migration's.

Every other byte of a note is preserved, key order and the comments on every
property it does NOT touch included: the rewrite goes through the surgical
frontmatter writer directly, never through the whole-map re-marshal that
alphabetizes keys and drops comments. The one exception is an inline comment
sitting on a date line it DOES respell, which is not carried onto the new value
and is named in the preview so you see it before anything is written.

It is idempotent. A value already written plain reads back as a date and is left
untouched, so running it twice changes nothing the second time.

PREVIEWS by default. With --write it applies the change and snapshots each note
first, so "2nb polish <path> --undo" restores it. A value that is not a
parseable date is never guessed at: it is named and skipped.`,
	Args: cobra.NoArgs,
	RunE: runObsidianMigrateProperties,
}

func init() {
	obsidianMigratePropertiesCmd.Flags().BoolVar(&migratePropertiesWrite, "write", false,
		"Apply the rewrite in place (opt-in; default previews only) and snapshot each note for `polish --undo`")
	obsidianCmd.AddCommand(obsidianMigratePropertiesCmd)
}

// MigratedField is one property whose value spelling changed.
type MigratedField struct {
	Field string `json:"field"`
	From  string `json:"from"`
	To    string `json:"to"`
}

// MigratedNote is one note the migration would rewrite, or did.
type MigratedNote struct {
	Path   string          `json:"path"`
	Fields []MigratedField `json:"fields"`
	// OtherLinesChanged names any line the rewrite moved that is NOT one of the
	// named fields. It should always be empty. It is not always empty: a
	// replaced node that carried a YAML anchor makes the writer resolve every
	// alias pointing into it, which materializes those aliases elsewhere in the
	// block. That is correct, and it is more than the property asked for, so it
	// is reported rather than sprung on you.
	OtherLinesChanged []string `json:"other_lines_changed,omitempty"`
	// CommentsDropped names each migrated property that carried an inline
	// comment the rewrite does not keep. The surgical writer deliberately does
	// not carry a comment onto a value it replaces (a comment described the OLD
	// value, and `meta --set status=published` must not leave one asserting
	// `draft`), and a respelling cannot opt out of that without an option on the
	// most safety-critical function in the package.
	//
	// So the loss is REPORTED instead, in the preview, before anything is
	// written. OtherLinesChanged cannot carry it: it skips any line keyed by a
	// migrated field, which is exactly the line the comment sat on, so the loss
	// was silent.
	CommentsDropped []DroppedComment `json:"comments_dropped,omitempty"`
}

// DroppedComment is an inline comment a respelling did not carry onto the new
// value node.
type DroppedComment struct {
	Field   string `json:"field"`
	Comment string `json:"comment"`
}

// SkippedNote is a note the migration did not touch, and why.
type SkippedNote struct {
	Path   string `json:"path"`
	Field  string `json:"field,omitempty"`
	Value  string `json:"value,omitempty"`
	Reason string `json:"reason"`
}

// UserDateField reports how a property 2nb does NOT own spells its dates. It is
// information, never an action: the migration does not touch these.
type UserDateField struct {
	Field    string `json:"field"`
	DateOnly int    `json:"date_only"`
	DateTime int    `json:"datetime"`
	Other    int    `json:"other"`
}

// MigratePropertiesResult is the command's record.
type MigratePropertiesResult struct {
	Scanned        int             `json:"scanned"`
	Changed        int             `json:"changed"`
	Written        bool            `json:"written"`
	Notes          []MigratedNote  `json:"notes"`
	Skipped        []SkippedNote   `json:"skipped"`
	UserDateFields []UserDateField `json:"user_date_fields"`
}

func runObsidianMigrateProperties(cmd *cobra.Command, args []string) error {
	if err := refuseBodylessFormat(cmd, "obsidian migrate-properties"); err != nil {
		return err
	}

	// A preview reads; only --write writes. openVault is the read opener, and
	// the write opener is what refuses a vault Obsidian does not know.
	var v *vault.Vault
	var err error
	if migratePropertiesWrite {
		v, err = openVaultAndSetActive()
	} else {
		v, err = openVault()
	}
	if err != nil {
		return err
	}
	defer v.Close()
	setupFileLogging(v)

	result := MigratePropertiesResult{
		Written:        migratePropertiesWrite,
		Notes:          []MigratedNote{},
		Skipped:        []SkippedNote{},
		UserDateFields: []UserDateField{},
	}
	userDates := map[string]*UserDateField{}
	excluded := vault.ObsidianTemplateFolders(v.Root)

	walkErr := filepath.Walk(v.Root, func(path string, info os.FileInfo, werr error) error {
		if werr != nil {
			return nil
		}
		if info.IsDir() {
			base := filepath.Base(path)
			if strings.HasPrefix(base, ".") || base == "node_modules" {
				return filepath.SkipDir
			}
			if vault.IsExcludedFolderPath(v.RelPath(path), excluded) {
				return filepath.SkipDir
			}
			return nil
		}
		// Markdown only. A .canvas/.base file is a synthetic read-only view
		// whose "created"/"modified" are the file's mtime, not properties, and
		// writing one back would overwrite the original JSON or YAML.
		if !strings.HasSuffix(strings.ToLower(path), ".md") {
			return nil
		}
		rel := v.RelPath(path)
		if vault.IsIgnored(rel) {
			return nil
		}
		result.Scanned++

		doc, perr := document.ParseFile(path)
		if perr != nil {
			result.Skipped = append(result.Skipped, SkippedNote{
				Path: rel, Reason: fmt.Sprintf("could not be read or parsed: %v", perr),
			})
			return nil
		}
		tallyUserDateFields(doc, v.Schemas, userDates)

		planned, refused := plannedDateRewrites(doc, v.Schemas)
		for _, r := range refused {
			r.Path = rel
			result.Skipped = append(result.Skipped, r)
		}
		if len(planned) == 0 {
			return nil
		}

		note, applyErr := applyDateRewrites(v, doc, path, rel, planned, migratePropertiesWrite)
		if applyErr != nil {
			result.Skipped = append(result.Skipped, SkippedNote{Path: rel, Reason: applyErr.Error()})
			return nil
		}
		result.Changed++
		result.Notes = append(result.Notes, *note)
		return nil
	})
	if walkErr != nil {
		return fmt.Errorf("walk vault: %w", walkErr)
	}

	for _, f := range userDates {
		result.UserDateFields = append(result.UserDateFields, *f)
	}
	sort.Slice(result.UserDateFields, func(i, j int) bool {
		return result.UserDateFields[i].Field < result.UserDateFields[j].Field
	})
	sort.Slice(result.Notes, func(i, j int) bool { return result.Notes[i].Path < result.Notes[j].Path })
	sort.Slice(result.Skipped, func(i, j int) bool { return result.Skipped[i].Path < result.Skipped[j].Path })

	if done, emitErr := emitStructured(cmd, result); done {
		return emitErr
	}
	printMigratePropertiesReport(result)
	return nil
}

// plannedDateRewrites decides, for one note, which date properties need their
// spelling changed and which cannot be touched.
//
// A value already held as a time.Time is a value yaml.v3 RESOLVED, which means
// it is already written plain: nothing to do. That is what makes the migration
// idempotent, and it is the same comparison the surgical writer's nodeHoldsValue
// makes, so a second run leaves the node exactly as the first run wrote it.
func plannedDateRewrites(doc *document.Document, schemas *vault.SchemaSet) (map[string]time.Time, []SkippedNote) {
	planned := map[string]time.Time{}
	var refused []SkippedNote
	for key, value := range doc.Frontmatter {
		if !schemas.IsDateField(doc.Type, key) {
			continue
		}
		switch t := value.(type) {
		case time.Time:
			// Already plain. A sub-second value is the one exception: the
			// reader formats with RFC3339 and drops the fraction, so the file
			// and the index column disagree until it is rewritten.
			if t.Nanosecond() != 0 {
				planned[key] = t.Truncate(time.Second)
			}
		case string:
			if t == "" {
				continue // an emptied property is not a date to migrate
			}
			parsed, ok := document.ParseFrontmatterDate(t)
			if !ok {
				refused = append(refused, SkippedNote{
					Field: key, Value: t,
					Reason: "not a parseable date; the migration never guesses at a value",
				})
				continue
			}
			planned[key] = parsed
		case nil:
			// An empty property. Nothing to migrate and nothing to report.
		default:
			refused = append(refused, SkippedNote{
				Field: key, Value: fmt.Sprintf("%v", value),
				Reason: fmt.Sprintf("a date property holding a %T is not a date", value),
			})
		}
	}
	return planned, refused
}

// applyDateRewrites rewrites one note's date properties, previewing when write
// is false.
//
// It calls UpdateDocumentFrontmatterAST DIRECTLY and treats an error as "skip
// this note". Document.Serialize silently FALLS BACK to the whole-map
// re-marshal when the AST writer errors, and that fallback alphabetizes keys
// and drops comments; across a whole vault that is a large, silent reformat
// nobody asked for.
func applyDateRewrites(v *vault.Vault, doc *document.Document, absPath, rel string, planned map[string]time.Time, write bool) (*MigratedNote, error) {
	original, err := os.ReadFile(absPath)
	if err != nil {
		return nil, fmt.Errorf("could not be read: %w", err)
	}

	updated := make(map[string]any, len(doc.Frontmatter))
	for k, val := range doc.Frontmatter {
		updated[k] = val
	}
	note := &MigratedNote{Path: rel}
	fields := make([]string, 0, len(planned))
	for key, t := range planned {
		updated[key] = t
		fields = append(fields, key)
	}
	sort.Strings(fields)

	out, err := document.UpdateDocumentFrontmatterAST(original, updated, doc.Body)
	if err != nil {
		return nil, fmt.Errorf("the surgical frontmatter writer refused this note (%v); it was left untouched rather than reformatted wholesale", err)
	}

	// Reparse the bytes that would be written, and read the new spelling off
	// the FILE rather than off the value handed in. A write that produces
	// mis-typed YAML is invisible until the next read, and this is the read.
	back, perr := document.Parse(rel, out)
	if perr != nil {
		return nil, fmt.Errorf("the rewritten note would not parse (%v); it was left untouched", perr)
	}
	for _, key := range fields {
		note.Fields = append(note.Fields, MigratedField{
			Field: key,
			From:  frontmatterLine(string(original), key),
			To:    frontmatterLine(string(out), key),
		})
	}
	// EVERY migrated field, not just the first. A note carrying both `created`
	// and a schema date field had only the alphabetically first one checked, so
	// a second field that came back as text rather than a date was written to
	// disk unverified.
	for _, key := range fields {
		if _, ok := back.Frontmatter[key].(time.Time); !ok {
			return nil, fmt.Errorf("the rewritten %q did not read back as a date; the note was left untouched", key)
		}
	}
	note.OtherLinesChanged = unexpectedChangedLines(string(original), string(out), fields)
	note.CommentsDropped = droppedValueComments(string(original), string(out), fields)

	if !write {
		return note, nil
	}

	if err := polish.WriteSnapshot(v, polish.PolishSnapshot{
		Path:          rel,
		OriginalFull:  string(original),
		PolishedBody:  doc.Body,
		Provider:      "migrate-properties",
		Timestamp:     time.Now().UTC().Format(time.RFC3339),
		PostWriteHash: polish.HashContent(out),
	}); err != nil {
		return nil, fmt.Errorf("could not snapshot for undo (%v); the note was left untouched", err)
	}

	tmp := absPath + ".tmp"
	if err := os.WriteFile(tmp, out, 0o644); err != nil {
		return nil, fmt.Errorf("write temp file: %w", err)
	}
	if err := os.Rename(tmp, absPath); err != nil {
		os.Remove(tmp)
		return nil, fmt.Errorf("rename: %w", err)
	}
	if err := vault.IndexSingleFile(v, absPath); err != nil {
		fmt.Fprintf(os.Stderr, "warning: %s was rewritten but could not be reindexed: %v\n", rel, err)
	}
	return note, nil
}

// frontmatterLine returns the first frontmatter line for key, trimmed, or ""
// when there is none. It reads the FILE text, which is the whole point: the
// spelling is what Obsidian types on, and the parsed value cannot show it.
func frontmatterLine(content, key string) string {
	for _, line := range strings.Split(frontmatterRegion(content), "\n") {
		if strings.HasPrefix(line, key+":") {
			return strings.TrimSpace(line)
		}
	}
	return ""
}

// frontmatterRegion returns the text between a note's opening and closing
// frontmatter delimiters, or "" when it has none.
func frontmatterRegion(content string) string {
	if !strings.HasPrefix(content, "---") {
		return ""
	}
	rest := content[3:]
	if end := strings.Index(rest, "\n---"); end >= 0 {
		return rest[:end]
	}
	return rest
}

// droppedValueComments names each migrated field whose value carried an inline
// comment before the rewrite and does not carry one after it.
//
// It reads the comment off the AST rather than looking for a `#` in the line,
// because a `#` inside a quoted value is not a comment. It compares before
// against after rather than assuming the writer always drops one, so the day
// the writer learns to carry a comment across, this reports nothing instead of
// reporting a loss that did not happen.
//
// A region that will not parse yields no comments and therefore no findings,
// which is the safe direction: this is a report, and inventing one from a
// half-read region would be worse than staying quiet.
func droppedValueComments(before, after string, fields []string) []DroppedComment {
	was := valueLineComments(before)
	if len(was) == 0 {
		return nil
	}
	is := valueLineComments(after)
	var out []DroppedComment
	for _, f := range fields {
		if was[f] != "" && is[f] == "" {
			out = append(out, DroppedComment{Field: f, Comment: was[f]})
		}
	}
	return out
}

// valueLineComments maps each frontmatter key to the comment attached to its
// VALUE node, which is the one a replacement drops. A comment attached to the
// KEY sits on the key node, which the writer never replaces.
func valueLineComments(content string) map[string]string {
	var node yaml.Node
	if err := yaml.Unmarshal([]byte(frontmatterRegion(content)), &node); err != nil {
		return nil
	}
	if node.Kind != yaml.DocumentNode || len(node.Content) == 0 {
		return nil
	}
	root := node.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil
	}
	out := map[string]string{}
	for i := 0; i+1 < len(root.Content); i += 2 {
		if c := root.Content[i+1].LineComment; c != "" {
			out[root.Content[i].Value] = c
		}
	}
	return out
}

// unexpectedChangedLines returns every frontmatter line that differs between
// before and after and is NOT one of the migrated fields. The migration's
// contract is that a note comes back byte-identical apart from the properties
// it named, so anything here is a deviation the user gets told about.
func unexpectedChangedLines(before, after string, fields []string) []string {
	named := map[string]bool{}
	for _, f := range fields {
		named[f] = true
	}
	beforeLines := map[string]int{}
	for _, l := range strings.Split(before, "\n") {
		beforeLines[l]++
	}
	var out []string
	for _, l := range strings.Split(after, "\n") {
		if beforeLines[l] > 0 {
			beforeLines[l]--
			continue
		}
		if key, _, found := strings.Cut(strings.TrimSpace(l), ":"); found && named[key] {
			continue
		}
		out = append(out, strings.TrimSpace(l))
	}
	return out
}

// documentTextFields are the properties Document mirrors as TEXT (the set
// frontmatter_scalar.go's file comment names, minus the list fields, which are
// never scalars and so never reach the tally below).
//
// They are excluded from the user-date tally, and that exclusion is a
// correctness rule rather than tidiness. A daily note is TITLED by its date, so
// `title: 2026-09-04` would otherwise be reported as a date-shaped property the
// user authors, right beside a line inviting them to declare such a field
// `date` in schemas.yaml. Taking that advice would make IsDateField true for
// `title`, route it through CoerceDate on every `meta --set`, and have this
// very migration rewrite the title line to `2026-09-04T00:00:00Z`, which is the
// exact regression 0.22.4 shipped to fix. A command must not recommend the
// thing a release was cut to prevent.
var documentTextFields = map[string]bool{"id": true, "title": true, "type": true, "status": true}

// tallyUserDateFields counts how a note spells the date-shaped properties 2nb
// does NOT own. Reported, never acted on: a vault commonly spells its own
// `date` field two ways, and choosing one is the user's call.
func tallyUserDateFields(doc *document.Document, schemas *vault.SchemaSet, into map[string]*UserDateField) {
	for key, value := range doc.Frontmatter {
		if schemas.IsDateField(doc.Type, key) || documentTextFields[key] {
			continue
		}
		text, ok := doc.MetaText(key)
		if !ok {
			if s, sok := document.ScalarText(value); sok {
				text = s
			} else {
				continue
			}
		}
		if _, isDate := document.ParseFrontmatterDate(text); !isDate {
			continue
		}
		f, seen := into[key]
		if !seen {
			f = &UserDateField{Field: key}
			into[key] = f
		}
		switch {
		case len(text) == len("2006-01-02"):
			f.DateOnly++
		case strings.Contains(text, ":"):
			f.DateTime++
		default:
			f.Other++
		}
	}
}

func printMigratePropertiesReport(r MigratePropertiesResult) {
	if r.Changed == 0 {
		fmt.Printf("Scanned %d note(s). Every date property 2nb owns is already written the way Obsidian types it.\n", r.Scanned)
	} else if r.Written {
		fmt.Printf("Scanned %d note(s), rewrote %d.\n", r.Scanned, r.Changed)
	} else {
		fmt.Printf("Scanned %d note(s). %d would be rewritten (preview; pass --write to apply).\n", r.Scanned, r.Changed)
	}

	for _, n := range r.Notes {
		fmt.Printf("\n  %s\n", n.Path)
		for _, f := range n.Fields {
			fmt.Printf("    %s  ->  %s\n", f.From, f.To)
		}
		for _, extra := range n.OtherLinesChanged {
			fmt.Printf("    also changed (a YAML anchor was resolved): %s\n", extra)
		}
		for _, c := range n.CommentsDropped {
			fmt.Printf("    comment NOT kept on %s: %s\n", c.Field, c.Comment)
		}
	}

	if len(r.Skipped) > 0 {
		fmt.Fprintf(os.Stderr, "\n%d note(s) skipped:\n", len(r.Skipped))
		for _, s := range r.Skipped {
			if s.Field != "" {
				fmt.Fprintf(os.Stderr, "  %s (%s: %q): %s\n", s.Path, s.Field, s.Value, s.Reason)
				continue
			}
			fmt.Fprintf(os.Stderr, "  %s: %s\n", s.Path, s.Reason)
		}
	}

	if len(r.UserDateFields) > 0 {
		fmt.Fprintf(os.Stderr, "\nDate-shaped properties you author, left untouched:\n")
		for _, f := range r.UserDateFields {
			fmt.Fprintf(os.Stderr, "  %s: %d date-only, %d with a time\n", f.Field, f.DateOnly, f.DateTime)
		}
		fmt.Fprintf(os.Stderr, "  Choosing one spelling for these is your call; declare the field as `date` or\n")
		fmt.Fprintf(os.Stderr, "  `datetime` in .2ndbrain/schemas.yaml and rerun to include it.\n")
	}

	if r.Written && r.Changed > 0 {
		fmt.Fprintf(os.Stderr, "\nEach rewritten note was snapshotted; undo one with `2nb polish <path> --undo`.\n")
	}
}
