package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/apresai/2ndbrain/internal/document"
	"github.com/apresai/2ndbrain/internal/vault"
	"github.com/spf13/cobra"
)

var (
	registerTypesWrite bool
	registerTypesForce bool
)

var obsidianRegisterTypesCmd = &cobra.Command{
	Use:   "register-types",
	Short: "Declare 2nb's property types in .obsidian/types.json",
	Long: `Tells Obsidian what KIND each property 2nb writes is, so its Properties panel
shows the right editor: a date picker for "created" and "modified", a tag pill
for "tags", plain text for the rest.

Writing a value in the shape Obsidian recognizes (which is what
"obsidian migrate-properties" does) is what makes the type INFERRABLE. This
command makes it DECLARED, which is what survives a note where the property is
empty.

This is the one place 2nb writes an Obsidian setting, and it is deliberately
narrow: one user-invoked command, one file, merge-only, backup-first, never
automatic.

  - MERGE, never clobber. Every property already declared in types.json keeps
    the type it has, including ones Obsidian wrote itself.
  - The previous file is copied to .2ndbrain/recovery/obsidian/ first, inside
    2nb's own sidecar rather than beside the original, so this never leaves a
    stray .bak inside Obsidian's config directory.
  - Refused while Obsidian holds the vault open, unless --force. Obsidian caches
    its settings in memory and rewrites types.json on its own schedule, so a
    write underneath it can simply be overwritten.
  - Refused (for --write) while any note holds a date "migrate-properties" would
    rewrite: a QUOTED one, which Obsidian shows as Text, or a zone-less one 2nb
    normalizes to explicit UTC. Declaring "created" a datetime while notes still
    hold text makes Obsidian show a type mismatch on every one of them. Run
    "2nb obsidian migrate-properties --write" first; a preview here still runs
    and names the notes.

"status" is declared as text. Obsidian has no enum type, and its list editor
("multitext") would write a YAML sequence back, which 2nb reads as no status at
all: every --status filter and every status-transition check would break.

"id" is deliberately NOT declared. It is a UUID nobody reads, the vault's
identity model is path-based, and declaring it would add a visible Text row to
the Properties panel of every note that carries one.

PREVIEWS by default; --write applies.`,
	Args: cobra.NoArgs,
	RunE: runObsidianRegisterTypes,
}

func init() {
	obsidianRegisterTypesCmd.Flags().BoolVar(&registerTypesWrite, "write", false,
		"Apply the merge to .obsidian/types.json (opt-in; default previews only)")
	obsidianRegisterTypesCmd.Flags().BoolVar(&registerTypesForce, "force", false,
		"Write even though Obsidian currently has this vault open (it may overwrite the change)")
	obsidianCmd.AddCommand(obsidianRegisterTypesCmd)
}

// obsidianPropertyTypes is what 2nb declares for the properties it writes.
//
// Every value here is one of Obsidian's own property types. "id" is absent on
// purpose; see the command's help.
var obsidianPropertyTypes = map[string]string{
	"created":  "datetime",
	"modified": "datetime",
	"title":    "text",
	"type":     "text",
	"status":   "text",
	"tags":     "tags",
	"aliases":  "aliases",
}

// RegisterTypesResult is the command's record.
type RegisterTypesResult struct {
	Path      string            `json:"path"`
	Written   bool              `json:"written"`
	Added     map[string]string `json:"added"`
	Preserved map[string]string `json:"preserved"`
	Backup    string            `json:"backup,omitempty"`
	// Blocked names every note holding a date migrate-properties would rewrite,
	// which is broader than "quoted": a quoted value is the one Obsidian shows
	// as Text, and a zone-less one is the one 2nb normalizes to explicit UTC.
	// A non-empty list blocks --write, because declaring created a datetime
	// while notes still hold text shows a type mismatch on every one of them.
	Blocked  []string `json:"blocked_by_unmigrated_notes"`
	Warnings []string `json:"warnings"`
}

func runObsidianRegisterTypes(cmd *cobra.Command, args []string) error {
	if err := refuseBodylessFormat(cmd, "obsidian register-types"); err != nil {
		return err
	}

	var v *vault.Vault
	var err error
	if registerTypesWrite {
		v, err = openVaultAndSetActive()
	} else {
		v, err = openVault()
	}
	if err != nil {
		return err
	}
	defer v.Close()
	setupFileLogging(v)

	typesPath := filepath.Join(v.Root, ".obsidian", "types.json")
	existing, err := readObsidianTypes(typesPath)
	if err != nil {
		return exitWithError(ExitValidation, fmt.Sprintf(
			"error: %s could not be read as JSON (%v); refusing to replace a settings file 2nb cannot understand", typesPath, err))
	}

	want := desiredObsidianTypes(v.Schemas)
	result := RegisterTypesResult{
		Path:      v.RelPath(typesPath),
		Added:     map[string]string{},
		Preserved: map[string]string{},
		Blocked:   []string{},
		Warnings:  []string{},
	}
	for _, key := range sortedKeys(want) {
		if have, ok := existing[key]; ok {
			// MERGE: what is already declared keeps its type, whatever it is.
			// Obsidian writes some of these itself (a real vault had
			// "tags": "multitext"), and a user may have chosen deliberately.
			result.Preserved[key] = have
			continue
		}
		result.Added[key] = want[key]
	}

	result.Blocked = notesUnmigrated(v)
	if len(result.Blocked) > 0 {
		result.Warnings = append(result.Warnings, fmt.Sprintf(
			"%d note(s) hold a date property migrate-properties would rewrite (a QUOTED value, which Obsidian shows as Text, or a zone-less one 2nb normalizes to UTC); run `2nb obsidian migrate-properties --write` first",
			len(result.Blocked)))
	}

	if registerTypesWrite {
		if err := writeObsidianTypes(v, typesPath, existing, want, &result); err != nil {
			return err
		}
	}

	for _, w := range result.Warnings {
		fmt.Fprintf(os.Stderr, "warning: %s\n", w)
	}
	if done, emitErr := emitStructured(cmd, result); done {
		return emitErr
	}
	printRegisterTypesReport(result)
	return nil
}

// writeObsidianTypes performs the gated, backed-up, atomic merge.
func writeObsidianTypes(v *vault.Vault, typesPath string, existing, want map[string]string, result *RegisterTypesResult) error {
	if len(result.Blocked) > 0 {
		return exitWithError(ExitValidation, fmt.Sprintf(
			"error: %d note(s) hold a date property migrate-properties would rewrite, so declaring these types now would make Obsidian show a type mismatch on every one that is still quoted.\n"+
				"  Run: 2nb obsidian migrate-properties --write\n"+
				"  Then rerun this command. (`2nb obsidian register-types` with no --write previews the merge meanwhile.)",
			len(result.Blocked)))
	}
	if len(result.Added) == 0 {
		result.Warnings = append(result.Warnings, "every property 2nb writes is already declared; nothing to write")
		return nil
	}
	if open, ok := vault.ObsidianHasVaultOpen(v.Root); open && !registerTypesForce {
		return exitWithError(ExitValidation,
			"error: Obsidian currently has this vault open. It caches settings in memory and rewrites types.json on its own schedule, so a write now can simply be overwritten.\n"+
				"  Close the vault in Obsidian and rerun, or pass --force to write anyway.")
	} else if !ok {
		result.Warnings = append(result.Warnings,
			"could not read Obsidian's vault registry, so whether Obsidian has this vault open is unknown; if it is, reopen the vault after this write")
	}

	merged := make(map[string]string, len(existing)+len(want))
	for k, val := range existing {
		merged[k] = val
	}
	for k, val := range result.Added {
		merged[k] = val
	}

	// Back up into 2nb's OWN sidecar, never beside the original: a stray .bak
	// inside .obsidian/ would be a second, unannounced write to Obsidian's
	// config directory.
	if len(existing) > 0 {
		backupDir := filepath.Join(v.DotDir, "recovery", "obsidian")
		if err := os.MkdirAll(backupDir, 0o755); err != nil {
			return fmt.Errorf("create recovery dir: %w", err)
		}
		stamp := time.Now().UTC().Format("20060102T150405Z")
		backup := filepath.Join(backupDir, "types-"+stamp+".json")
		prev, err := os.ReadFile(typesPath)
		if err != nil {
			return fmt.Errorf("read %s to back it up: %w", typesPath, err)
		}
		if err := os.WriteFile(backup, prev, 0o644); err != nil {
			return fmt.Errorf("write backup: %w", err)
		}
		result.Backup = v.RelPath(backup)
	}

	data, err := json.MarshalIndent(struct {
		Types map[string]string `json:"types"`
	}{Types: merged}, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal types.json: %w", err)
	}
	data = append(data, '\n')

	if err := os.MkdirAll(filepath.Dir(typesPath), 0o755); err != nil {
		return fmt.Errorf("create .obsidian: %w", err)
	}
	tmp := typesPath + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := os.Rename(tmp, typesPath); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("rename: %w", err)
	}
	result.Written = true
	return nil
}

// readObsidianTypes reads the property-type map out of types.json. A MISSING
// file is an empty map and not an error (a vault that never declared a type),
// but an UNPARSEABLE one is: replacing a settings file 2nb cannot understand is
// exactly the write this command must never make.
func readObsidianTypes(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]string{}, nil
		}
		return nil, err
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return map[string]string{}, nil
	}
	var root struct {
		Types map[string]string `json:"types"`
	}
	if err := json.Unmarshal(data, &root); err != nil {
		return nil, err
	}
	if root.Types == nil {
		root.Types = map[string]string{}
	}
	return root.Types, nil
}

// desiredObsidianTypes is 2nb's own mapping plus every field the vault's schemas
// declare as a date or datetime, which is what finally gives FieldDef.Type a
// consumer on the Obsidian side as well as the write side.
func desiredObsidianTypes(schemas *vault.SchemaSet) map[string]string {
	want := make(map[string]string, len(obsidianPropertyTypes)+4)
	for k, val := range obsidianPropertyTypes {
		want[k] = val
	}
	if schemas == nil {
		return want
	}
	for _, schema := range schemas.Types {
		for field, def := range schema.Fields {
			switch def.Type {
			case "date", "datetime":
				// A field two types spell differently keeps the wider one:
				// datetime renders a date fine, a date editor truncates a time.
				if want[field] != "datetime" {
					want[field] = def.Type
				}
			}
		}
	}
	return want
}

// notesUnmigrated returns the vault-relative path of every note holding a date
// property that migrate-properties would rewrite, sorted. It reuses that
// command's own predicate, so the two cannot disagree about what "unmigrated"
// means.
//
// That is deliberately broader than "quoted", which is why it is not named for
// it: a QUOTED value is the one Obsidian shows as Text, and a zone-less
// `2026-09-04T12:34:56` (which Obsidian's own editor writes) is one 2nb
// normalizes to explicit UTC so the file says what the index reads. Both are
// things the migration moves, so both must be settled before types are
// declared.
func notesUnmigrated(v *vault.Vault) []string {
	var out []string
	excluded := vault.ObsidianTemplateFolders(v.Root)
	_ = filepath.Walk(v.Root, func(path string, info os.FileInfo, werr error) error {
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
		if !strings.HasSuffix(strings.ToLower(path), ".md") {
			return nil
		}
		rel := v.RelPath(path)
		if vault.IsIgnored(rel) {
			return nil
		}
		doc, perr := document.ParseFile(path)
		if perr != nil {
			return nil
		}
		if planned, _ := plannedDateRewrites(doc, v.Schemas); len(planned) > 0 {
			out = append(out, rel)
		}
		return nil
	})
	sort.Strings(out)
	return out
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func printRegisterTypesReport(r RegisterTypesResult) {
	switch {
	case r.Written:
		fmt.Printf("Declared %d property type(s) in %s.\n", len(r.Added), r.Path)
	case len(r.Added) == 0:
		fmt.Printf("Every property 2nb writes is already declared in %s.\n", r.Path)
	default:
		fmt.Printf("%d property type(s) would be declared in %s (preview; pass --write to apply).\n", len(r.Added), r.Path)
	}
	for _, k := range sortedKeys(r.Added) {
		fmt.Printf("  + %s: %s\n", k, r.Added[k])
	}
	if len(r.Preserved) > 0 {
		fmt.Printf("\nAlready declared, kept exactly as they are:\n")
		for _, k := range sortedKeys(r.Preserved) {
			fmt.Printf("    %s: %s\n", k, r.Preserved[k])
		}
	}
	if r.Backup != "" {
		fmt.Fprintf(os.Stderr, "\nThe previous types.json was copied to %s\n", r.Backup)
	}
	if r.Written {
		fmt.Fprintf(os.Stderr, "Reopen the vault in Obsidian to pick up the new property types.\n")
	}
}
