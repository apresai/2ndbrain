package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// vaultTypesJSON is the shape Obsidian writes: {"types": {...}}. The three
// entries here are exactly what a real vault carried on 2026-09-04, including
// Obsidian's own choice of "multitext" for tags where 2nb would have written
// "tags". Merge means that choice survives untouched.
const vaultTypesJSON = `{
  "types": {
    "aliases": "aliases",
    "cssclasses": "multitext",
    "tags": "multitext"
  }
}`

func registerTypesJSON(t *testing.T, root string, extra ...string) (RegisterTypesResult, error) {
	t.Helper()
	argv := append([]string{"obsidian", "register-types", "--json"}, extra...)
	out, err := runCLIArgs(t, root, argv...)
	if err != nil {
		return RegisterTypesResult{}, err
	}
	var res RegisterTypesResult
	if jerr := json.Unmarshal(jsonPortion(out), &res); jerr != nil {
		t.Fatalf("register-types --json is not parseable: %v\n%s", jerr, out)
	}
	return res, nil
}

func readTypesFile(t *testing.T, root string) map[string]string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, ".obsidian", "types.json"))
	if err != nil {
		t.Fatalf("read types.json: %v", err)
	}
	var wrapper struct {
		Types map[string]string `json:"types"`
	}
	if err := json.Unmarshal(data, &wrapper); err != nil {
		t.Fatalf("types.json is not parseable after the write: %v\n%s", err, data)
	}
	return wrapper.Types
}

// The core promise: what Obsidian already declared survives byte for byte in
// meaning, and only genuinely absent properties are added. Clobbering here
// would silently retype a user's own properties in their editor.
func TestContract_RegisterTypes_MergesAndNeverClobbers(t *testing.T) {
	_, root := newContractVault(t)
	if err := os.WriteFile(filepath.Join(root, ".obsidian", "types.json"), []byte(vaultTypesJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := registerTypesJSON(t, root, "--write")
	if err != nil {
		t.Fatalf("register-types --write: %v", err)
	}
	if !res.Written {
		t.Fatalf("the command reported no write: %+v", res)
	}

	types := readTypesFile(t, root)
	// Preserved, including Obsidian's own multitext for tags and a property 2nb
	// knows nothing about.
	for key, want := range map[string]string{
		"aliases":    "aliases",
		"cssclasses": "multitext",
		"tags":       "multitext",
	} {
		if types[key] != want {
			t.Errorf("types.json[%q] = %q, want the existing %q kept", key, types[key], want)
		}
	}
	// Added.
	for key, want := range map[string]string{
		"created":  "datetime",
		"modified": "datetime",
		"title":    "text",
		"type":     "text",
		"status":   "text",
	} {
		if types[key] != want {
			t.Errorf("types.json[%q] = %q, want %q", key, types[key], want)
		}
	}
	// NEEDS-DECISION, resolved as "omit": declaring id would add a visible Text
	// row to the Properties panel of every note that carries one.
	if _, ok := types["id"]; ok {
		t.Errorf("id was declared; it is deliberately omitted: %+v", types)
	}
	// status must never be multitext: Obsidian's list editor would write a YAML
	// sequence back, which reads as no status at all.
	if types["status"] == "multitext" {
		t.Error("status was declared multitext; a sequence read back breaks every --status filter and the status machine")
	}

	if res.Backup == "" {
		t.Error("no backup was recorded for a file that already existed")
	} else if _, err := os.Stat(filepath.Join(root, res.Backup)); err != nil {
		t.Errorf("the recorded backup %q does not exist: %v", res.Backup, err)
	}
	if strings.HasPrefix(res.Backup, ".obsidian/") {
		t.Errorf("the backup landed inside Obsidian's config directory (%q); it belongs in 2nb's own sidecar", res.Backup)
	}
}

// Idempotent: a second write finds nothing to add, reports it, and leaves the
// file alone rather than churning a settings file.
func TestContract_RegisterTypes_IsIdempotent(t *testing.T) {
	_, root := newContractVault(t)
	if _, err := registerTypesJSON(t, root, "--write"); err != nil {
		t.Fatalf("first write: %v", err)
	}
	first := readTypesFile(t, root)

	res, err := registerTypesJSON(t, root, "--write")
	if err != nil {
		t.Fatalf("second write: %v", err)
	}
	if len(res.Added) != 0 {
		t.Errorf("second run added %+v, want nothing", res.Added)
	}
	got := readTypesFile(t, root)
	if len(got) != len(first) {
		t.Errorf("the second run changed the file: %+v -> %+v", first, got)
	}
}

// A preview writes nothing and does not create the file.
func TestContract_RegisterTypes_PreviewWritesNothing(t *testing.T) {
	_, root := newContractVault(t)
	res, err := registerTypesJSON(t, root)
	if err != nil {
		t.Fatalf("register-types: %v", err)
	}
	if res.Written {
		t.Error("a preview reported itself as written")
	}
	if len(res.Added) == 0 {
		t.Error("the preview listed nothing to add")
	}
	if _, err := os.Stat(filepath.Join(root, ".obsidian", "types.json")); !os.IsNotExist(err) {
		t.Errorf("the preview created types.json (stat err = %v)", err)
	}
}

// The ordering constraint, enforced rather than documented: declaring created a
// datetime while notes still hold quoted text makes Obsidian show a type
// mismatch on every one of them. The WRITE is refused; the PREVIEW still runs
// and says why, so the user is not left guessing.
//
// "Unmigrated" is broader than "quoted", and the second note here is why: a
// zone-less `2026-09-04T12:34:56` is what Obsidian's OWN datetime editor
// writes, it is not quoted, and the migration still rewrites it (to explicit
// UTC, so the file says what the index reads). Both must be settled before
// types are declared, which is why the predicate is shared with the migration
// rather than restated as a quoting check.
func TestContract_RegisterTypes_RefusesBeforeTheMigration(t *testing.T) {
	_, root := newContractVault(t)
	if err := os.WriteFile(filepath.Join(root, "quoted.md"), []byte(quotedNote), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "zoneless.md"),
		[]byte("---\ntitle: Zoneless\ntype: note\ncreated: 2026-09-04T12:34:56\n---\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := registerTypesJSON(t, root)
	if err != nil {
		t.Fatalf("the preview must still run: %v", err)
	}
	if len(res.Blocked) != 2 || res.Blocked[0] != "quoted.md" || res.Blocked[1] != "zoneless.md" {
		t.Errorf("the preview did not name both unmigrated notes: %+v", res.Blocked)
	}

	if _, err := runCLIArgs(t, root, "obsidian", "register-types", "--write"); err == nil {
		t.Fatal("--write was allowed while a note still carried a quoted date")
	}
	if _, err := os.Stat(filepath.Join(root, ".obsidian", "types.json")); !os.IsNotExist(err) {
		t.Errorf("the refused write created types.json anyway (stat err = %v)", err)
	}

	// After the migration it goes through.
	if out, merr := runCLIArgs(t, root, "obsidian", "migrate-properties", "--write"); merr != nil {
		t.Fatalf("migrate-properties: %v\n%s", merr, out)
	}
	after, err := registerTypesJSON(t, root, "--write")
	if err != nil {
		t.Fatalf("register-types --write after the migration: %v", err)
	}
	if !after.Written {
		t.Errorf("the write was still refused after the migration: %+v", after)
	}
}

// A types.json 2nb cannot parse is never replaced. Overwriting a settings file
// it does not understand is precisely the write this command must not make.
func TestContract_RegisterTypes_RefusesAnUnparseableTypesFile(t *testing.T) {
	_, root := newContractVault(t)
	broken := []byte("{not json at all")
	if err := os.WriteFile(filepath.Join(root, ".obsidian", "types.json"), broken, 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := runCLIArgs(t, root, "obsidian", "register-types", "--write"); err == nil {
		t.Fatal("an unparseable types.json was accepted")
	}
	got, err := os.ReadFile(filepath.Join(root, ".obsidian", "types.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(broken) {
		t.Errorf("the unparseable file was modified:\n%s", got)
	}
}

// A schema declaring a date field gets that field declared to Obsidian too,
// which is what gives FieldDef.Type a consumer on the Obsidian side.
func TestContract_RegisterTypes_DeclaresSchemaDateFields(t *testing.T) {
	_, root := newContractVault(t)
	schemas := "types:\n  meeting:\n    name: Meeting\n    fields:\n      held:\n        type: date\n      starts_at:\n        type: datetime\n      summary:\n        type: text\n    required: []\n"
	if err := os.WriteFile(filepath.Join(root, ".2ndbrain", "schemas.yaml"), []byte(schemas), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := registerTypesJSON(t, root, "--write"); err != nil {
		t.Fatalf("register-types --write: %v", err)
	}
	types := readTypesFile(t, root)
	if types["held"] != "date" {
		t.Errorf("types.json[held] = %q, want date", types["held"])
	}
	if types["starts_at"] != "datetime" {
		t.Errorf("types.json[starts_at] = %q, want datetime", types["starts_at"])
	}
	// A field the schema declares as text is NOT declared here: the mapping is
	// deliberately narrow, and every property 2nb has not been asked about
	// stays Obsidian's own inference.
	if _, ok := types["summary"]; ok {
		t.Errorf("a schema text field was declared; the mapping covers dates and 2nb's own properties only: %+v", types)
	}
}
