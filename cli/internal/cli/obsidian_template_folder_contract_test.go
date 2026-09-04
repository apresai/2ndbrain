package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A vault with the Templates core plugin ENABLED and no templates.json is the
// common case, not an edge one: Obsidian writes templates.json only once you
// SAVE the Templates folder setting, so a user who never opened that setting
// has the plugin on and no file. The config-only exclusion was therefore inert
// on exactly the vault it was written for, and `2nb index` logged a parse
// failure per template on every single run.
//
// The convention fallback is gated on that plugin flag AND on the folder
// existing, because purgeStale DELETES rows, chunks and vectors under a newly
// excluded folder. TestContract_TemplatesFolderIsIndexedWithoutThePlugin is the
// other half of this pair and it is the one that matters most.

const templateNote = "---\ntitle: {{date}}\ndate: {{date}}\ntags: [daily]\n---\n# {{date}}\n"

// writeCorePlugins writes .obsidian/core-plugins.json in the object-of-booleans
// shape Obsidian currently uses (verified live against a real vault).
func writeCorePlugins(t *testing.T, root string, templatesEnabled bool) {
	t.Helper()
	data, err := json.Marshal(map[string]bool{"file-explorer": true, "templates": templatesEnabled})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".obsidian"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".obsidian", "core-plugins.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// writeTemplatesFolder puts one unparseable Obsidian template and one ordinary
// note inside <root>/templates.
func writeTemplatesFolder(t *testing.T, root string) {
	t.Helper()
	dir := filepath.Join(root, "templates")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "daily.md"), []byte(templateNote), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "meeting-notes.md"),
		[]byte("---\ntitle: Meeting Notes\ntype: note\nstatus: draft\n---\nAgenda template.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func indexJSON(t *testing.T, root string) IndexResult {
	t.Helper()
	out, err := runCLIArgs(t, root, "index", "--json")
	if err != nil {
		t.Fatalf("index --json: %v\n%s", err, out)
	}
	var res IndexResult
	if err := json.Unmarshal(jsonPortion(out), &res); err != nil {
		t.Fatalf("index --json is not parseable: %v\n%s", err, out)
	}
	return res
}

func listPaths(t *testing.T, root string) []string {
	t.Helper()
	out, err := runCLIArgs(t, root, "list", "--format", "paths")
	if err != nil {
		t.Fatalf("list: %v\n%s", err, out)
	}
	var paths []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			paths = append(paths, line)
		}
	}
	return paths
}

func TestContract_TemplatesFolderIsExcludedWhenThePluginIsOn(t *testing.T) {
	_, root := newContractVault(t)
	writeCorePlugins(t, root, true)
	writeTemplatesFolder(t, root)
	if err := os.WriteFile(filepath.Join(root, "real.md"),
		[]byte("---\ntitle: Real\ntype: note\nstatus: draft\n---\nA note that links to [[Meeting Notes]].\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	res := indexJSON(t, root)
	if len(res.Unparseable) != 0 {
		t.Errorf("index reported unparseable notes, so the template folder was still walked: %+v", res.Unparseable)
	}
	for _, p := range listPaths(t, root) {
		if strings.HasPrefix(p, "templates/") {
			t.Errorf("%s is indexed; the template folder must not be", p)
		}
	}

	// vault status must NAME the folder: "not indexed" is not actionable
	// without knowing which rule did it.
	out, err := runCLIArgs(t, root, "vault", "status", "--json")
	if err != nil {
		t.Fatalf("vault status: %v\n%s", err, out)
	}
	var status struct {
		ExcludedFolders []string `json:"excluded_folders"`
	}
	if err := json.Unmarshal(jsonPortion(out), &status); err != nil {
		t.Fatalf("vault status --json: %v\n%s", err, out)
	}
	if len(status.ExcludedFolders) != 1 || status.ExcludedFolders[0] != "templates" {
		t.Errorf("vault status excluded_folders = %v, want [templates]", status.ExcludedFolders)
	}

	// And the resolver must not see it either. CollectLiveDocs feeds lint,
	// repair-links, relink, suggest-target and the move ambiguity guard, so a
	// template left in that set is a candidate by TITLE for every one of them.
	out, err = runCLIArgs(t, root, "lint", "--json")
	if err != nil {
		t.Fatalf("lint: %v\n%s", err, out)
	}
	if strings.Contains(string(out), "templates/") {
		t.Errorf("lint reported on the template folder:\n%s", out)
	}
}

// GUARD, and the one the gate exists for: a vault with the Templates plugin OFF
// (or no core-plugins.json at all) that keeps REAL notes in a folder it happens
// to have named "templates" must index them, list them, and resolve links to
// them. Without the gate, purgeStale would delete their rows, chunks and
// vectors on the next index, silently.
func TestContract_TemplatesFolderIsIndexedWithoutThePlugin(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T, root string)
	}{
		{"plugin disabled", func(t *testing.T, root string) { writeCorePlugins(t, root, false) }},
		{"no core-plugins.json at all", func(t *testing.T, root string) {}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, root := newContractVault(t)
			tc.setup(t, root)
			// Real notes only: this vault's "templates" folder is a topic, not
			// a scaffolding folder.
			dir := filepath.Join(root, "templates")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "meeting-notes.md"),
				[]byte("---\ntitle: Meeting Notes\ntype: note\nstatus: draft\n---\nHow we run meetings.\n"), 0o644); err != nil {
				t.Fatal(err)
			}

			res := indexJSON(t, root)
			if res.ExcludedPurged != 0 {
				t.Errorf("index purged %d rows from a folder it must not exclude", res.ExcludedPurged)
			}
			found := false
			for _, p := range listPaths(t, root) {
				if p == "templates/meeting-notes.md" {
					found = true
				}
			}
			if !found {
				t.Errorf("templates/meeting-notes.md is not indexed; the convention fired without the plugin flag. list gave %v", listPaths(t, root))
			}

			out, err := runCLIArgs(t, root, "vault", "status", "--json")
			if err != nil {
				t.Fatalf("vault status: %v\n%s", err, out)
			}
			if strings.Contains(string(out), `"templates"`) {
				t.Errorf("vault status excluded a folder it must not:\n%s", out)
			}
		})
	}
}

// An explicit Obsidian configuration stays authoritative and is never widened
// by the convention: a vault that names "scaffolding/" excludes that and only
// that, even with a templates/ folder full of real notes sitting beside it.
func TestContract_ConfiguredTemplateFolderWinsOverTheConvention(t *testing.T) {
	_, root := newContractVault(t)
	writeCorePlugins(t, root, true)
	if err := os.WriteFile(filepath.Join(root, ".obsidian", "templates.json"),
		[]byte(`{"folder":"scaffolding"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	for dir, body := range map[string]string{
		"scaffolding": templateNote,
		"templates":   "---\ntitle: Real Templates Note\ntype: note\nstatus: draft\n---\nA real note.\n",
	} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, dir, "n.md"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	res := indexJSON(t, root)
	if len(res.Unparseable) != 0 {
		t.Errorf("the configured folder was walked anyway: %+v", res.Unparseable)
	}
	paths := listPaths(t, root)
	sawTemplates, sawScaffolding := false, false
	for _, p := range paths {
		if p == "templates/n.md" {
			sawTemplates = true
		}
		if strings.HasPrefix(p, "scaffolding/") {
			sawScaffolding = true
		}
	}
	if !sawTemplates {
		t.Errorf("the convention excluded templates/ even though Obsidian named a different folder; list gave %v", paths)
	}
	if sawScaffolding {
		t.Errorf("the configured scaffolding/ folder was indexed; list gave %v", paths)
	}
}

// A folder that becomes excluded AFTER its notes were indexed has its rows
// purged, and the run that does it must SAY so on the human summary. A
// deletion reported only in --json is a deletion most users never see.
func TestContract_PurgingAnExcludedFolderIsReported(t *testing.T) {
	_, root := newContractVault(t)
	writeTemplatesFolder(t, root)

	// First index: no plugin flag, so the folder is ordinary and gets indexed.
	if res := indexJSON(t, root); res.ExcludedPurged != 0 {
		t.Fatalf("first index purged %d rows before the folder was excluded", res.ExcludedPurged)
	}

	// Now the user saves the Templates setting, and the folder becomes excluded.
	writeCorePlugins(t, root, true)
	stderr := captureStderr(t, func() {
		if out, err := runCLIArgs(t, root, "index"); err != nil {
			t.Fatalf("index: %v\n%s", err, out)
		}
	})
	if !strings.Contains(stderr, "excluded template folder") {
		t.Errorf("the human summary did not report the purge:\n%s", stderr)
	}
	for _, p := range listPaths(t, root) {
		if strings.HasPrefix(p, "templates/") {
			t.Errorf("%s survived the purge", p)
		}
	}
}
