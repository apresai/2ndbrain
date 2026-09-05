package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
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

// types.json is Obsidian's file, and "types" is only one of the top-level keys
// it may hold. Reading only that key and re-marshalling a struct that holds
// nothing else DELETED every sibling on write. This is the single file the
// write-surface exception grants 2nb permission to touch, so it has to come
// back with everything except the keys we deliberately add.
//
// Asserted by REPARSING the written file and comparing sibling VALUES, not by
// string matching key names: a key that survives with a mangled value is the
// same defect wearing a disguise.
func TestContract_RegisterTypes_PreservesUnknownTopLevelKeys(t *testing.T) {
	_, root := newContractVault(t)
	original := `{"types":{"tags":"multitext"},"siblingKey":"must survive","another":{"nested":1,"deep":["a","b"]},"count":42,"flag":false,"nothing":null}`
	if err := os.WriteFile(filepath.Join(root, ".obsidian", "types.json"), []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := registerTypesJSON(t, root, "--write"); err != nil {
		t.Fatalf("register-types --write: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(root, ".obsidian", "types.json"))
	if err != nil {
		t.Fatal(err)
	}
	var after map[string]any
	if err := json.Unmarshal(data, &after); err != nil {
		t.Fatalf("the written file is not parseable JSON: %v\n%s", err, data)
	}
	var before map[string]any
	if err := json.Unmarshal([]byte(original), &before); err != nil {
		t.Fatal(err)
	}

	// Every sibling key survives with its VALUE intact, scalar and nested alike.
	for key, want := range before {
		if key == "types" {
			continue
		}
		got, present := after[key]
		if !present {
			t.Errorf("top-level key %q was DELETED; types.json is the one Obsidian setting 2nb may touch:\n%s", key, data)
			continue
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("top-level key %q changed value: got %#v, want %#v", key, got, want)
		}
	}
	// A null sibling is a value too, and `present` above is what catches it
	// disappearing: reflect.DeepEqual(nil, nil) would pass on a missing key.
	if _, present := after["nothing"]; !present {
		t.Error("a null-valued sibling was dropped")
	}
	// And the merge still happened.
	types, _ := after["types"].(map[string]any)
	if types["created"] != "datetime" || types["tags"] != "multitext" {
		t.Errorf("the types merge did not happen or clobbered an existing entry: %#v", types)
	}
}

// A file that PARSES as JSON but is not an object cannot have a "types" key
// merged into it, and guessing what the user meant is not this command's job.
func TestContract_RegisterTypes_RefusesAJSONArray(t *testing.T) {
	_, root := newContractVault(t)
	original := []byte(`["not", "an", "object"]`)
	if err := os.WriteFile(filepath.Join(root, ".obsidian", "types.json"), original, 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := runCLIArgs(t, root, "obsidian", "register-types", "--write"); err == nil {
		t.Fatal("a JSON array was accepted as types.json")
	}
	got, err := os.ReadFile(filepath.Join(root, ".obsidian", "types.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(original) {
		t.Errorf("the array file was modified:\n%s", got)
	}
}

// A vault with siblings but NO "types" key gets it appended, so nothing the
// user already had moves position.
func TestContract_RegisterTypes_AddsTypesWithoutDisturbingSiblings(t *testing.T) {
	_, root := newContractVault(t)
	if err := os.WriteFile(filepath.Join(root, ".obsidian", "types.json"),
		[]byte(`{"somethingElse":{"a":1}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := registerTypesJSON(t, root, "--write"); err != nil {
		t.Fatalf("register-types --write: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(root, ".obsidian", "types.json"))
	if err != nil {
		t.Fatal(err)
	}
	var after map[string]any
	if err := json.Unmarshal(data, &after); err != nil {
		t.Fatalf("not parseable: %v\n%s", err, data)
	}
	sibling, ok := after["somethingElse"].(map[string]any)
	if !ok || sibling["a"] != float64(1) {
		t.Errorf("the sibling was lost or changed: %#v", after["somethingElse"])
	}
	if types, ok := after["types"].(map[string]any); !ok || types["created"] != "datetime" {
		t.Errorf("types was not added: %#v", after["types"])
	}
	// Appended, not hoisted: the sibling keeps the first position it had.
	if idx := strings.Index(string(data), `"somethingElse"`); idx < 0 || idx > strings.Index(string(data), `"types"`) {
		t.Errorf("the existing key was moved; a new key goes on the end:\n%s", data)
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

// obsidianConfigDirs returns every place ObsidianHasVaultOpen might look for
// Obsidian's registry under the test's redirected HOME. Both are written so the
// test is not macOS-only; the resolver reads whichever its platform picks.
func obsidianConfigDirs(t *testing.T, home string) []string {
	t.Helper()
	return []string{
		filepath.Join(home, "Library", "Application Support", "obsidian"),
		filepath.Join(home, ".config", "obsidian"),
	}
}

// fakeObsidianState plants the two facts the guard consults: the registry entry
// naming this vault as the open one, and (when running) the Chromium singleton
// lock whose symlink target is `<hostname>-<pid>`.
//
// Nothing is substituted here: the real registry reader and the real liveness
// probe run, against a redirected HOME. That is what makes this a test of the
// WIRING rather than of a stub.
func fakeObsidianState(t *testing.T, home, vaultRoot string, running bool) {
	t.Helper()
	registry := `{"vaults":{"a":{"path":` + strconv.Quote(vaultRoot) + `,"ts":100,"open":true}}}`
	for _, dir := range obsidianConfigDirs(t, home) {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "obsidian.json"), []byte(registry), 0o644); err != nil {
			t.Fatal(err)
		}
		lock := filepath.Join(dir, "SingletonLock")
		os.Remove(lock)
		if running {
			// This test process is the pid guaranteed to be alive.
			host, herr := os.Hostname()
			if herr != nil {
				t.Fatal(herr)
			}
			target := host + "-" + strconv.Itoa(os.Getpid())
			if err := os.Symlink(target, lock); err != nil {
				t.Fatal(err)
			}
		}
	}
}

// The guard needs BOTH facts, and this is the case that made it wrong: Obsidian
// sets `open` on open and never clears it on quit, so on the flag alone a user
// who had just quit was refused anyway, and the only way through was --force.
// A guard that fires for someone who did exactly what it asked teaches people
// to reach around it.
func TestContract_RegisterTypes_WritesWhenObsidianHasQuit(t *testing.T) {
	_, root := newContractVault(t)
	home := os.Getenv("HOME")
	fakeObsidianState(t, home, root, false) // registry still says open; process gone

	res, err := registerTypesJSON(t, root, "--write")
	if err != nil {
		t.Fatalf("register-types --write with Obsidian quit: %v", err)
	}
	if !res.Written {
		t.Errorf("the write was refused although Obsidian is not running: %+v", res)
	}
	if _, statErr := os.Stat(filepath.Join(root, ".obsidian", "types.json")); statErr != nil {
		t.Errorf("types.json was not written: %v", statErr)
	}
}

// Still refused while Obsidian is actually there, which is the case the guard
// exists for: Obsidian caches settings in memory and would overwrite the write.
func TestContract_RegisterTypes_RefusesWhileObsidianIsRunning(t *testing.T) {
	_, root := newContractVault(t)
	home := os.Getenv("HOME")
	fakeObsidianState(t, home, root, true)

	if _, err := runCLIArgs(t, root, "obsidian", "register-types", "--write"); err == nil {
		t.Fatal("--write was allowed while Obsidian is running")
	}
	if _, statErr := os.Stat(filepath.Join(root, ".obsidian", "types.json")); !os.IsNotExist(statErr) {
		t.Errorf("the refused write created types.json anyway (stat err = %v)", statErr)
	}

	// --force is the deliberate override, and it must still work.
	res, err := registerTypesJSON(t, root, "--write", "--force")
	if err != nil {
		t.Fatalf("register-types --write --force: %v", err)
	}
	if !res.Written {
		t.Errorf("--force did not override the running-Obsidian refusal: %+v", res)
	}
}

// `preserved` must name everything the merge leaves alone, not just the
// properties 2nb happens to declare itself. A real vault previewed as
// `preserved: {aliases, tags}` while its `cssclasses` went unmentioned, even
// though the write kept it perfectly. The preview is the safety mechanism here
// (it is what you read before allowing a write into Obsidian's own config), so
// under-reporting what it keeps undermines it exactly where it matters.
func TestContract_RegisterTypes_PreservedNamesEveryEntryItKeeps(t *testing.T) {
	_, root := newContractVault(t)
	obs := filepath.Join(root, ".obsidian")
	if err := os.MkdirAll(obs, 0o755); err != nil {
		t.Fatal(err)
	}
	// cssclasses is Obsidian's own and is not a property 2nb declares.
	seed := `{"types":{"aliases":"aliases","cssclasses":"multitext","tags":"multitext"}}`
	if err := os.WriteFile(filepath.Join(obs, "types.json"), []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}

	res := mustRegisterTypes(t, root)
	for _, key := range []string{"aliases", "cssclasses", "tags"} {
		if _, ok := res.Preserved[key]; !ok {
			t.Errorf("preserved omits %q, which the write keeps: %+v", key, res.Preserved)
		}
	}
	if got := res.Preserved["cssclasses"]; got != "multitext" {
		t.Errorf("preserved[cssclasses] = %q, want multitext", got)
	}
	// A preserved entry is never also an addition.
	for key := range res.Preserved {
		if _, dup := res.Added[key]; dup {
			t.Errorf("%q is reported as both preserved and added", key)
		}
	}
}

// mustRegisterTypes runs a PREVIEW and fails the test on error.
func mustRegisterTypes(t *testing.T, root string) RegisterTypesResult {
	t.Helper()
	res, err := registerTypesJSON(t, root)
	if err != nil {
		t.Fatalf("register-types preview: %v", err)
	}
	return res
}
