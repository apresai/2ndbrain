package vault

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// writeTemplateVault builds a vault root with an optional core-plugins.json and
// an optional templates/ folder, so each gate can be tested on its own.
func writeTemplateVault(t *testing.T, corePlugins string, withFolder bool) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".obsidian"), 0o755); err != nil {
		t.Fatal(err)
	}
	if corePlugins != "" {
		if err := os.WriteFile(filepath.Join(root, ".obsidian", "core-plugins.json"), []byte(corePlugins), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if withFolder {
		if err := os.MkdirAll(filepath.Join(root, "templates"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// Both gates must hold, and each failure mode must fail CLOSED, because
// purgeStale deletes rows, chunks and vectors under a newly excluded folder.
func TestObsidianTemplateFolders_ConventionGates(t *testing.T) {
	for _, tc := range []struct {
		name        string
		corePlugins string
		withFolder  bool
		want        []string
	}{
		{"object form, enabled, folder exists", `{"templates":true}`, true, []string{"templates"}},
		{"object form, disabled", `{"templates":false}`, true, nil},
		{"object form, field absent", `{"graph":true}`, true, nil},
		{"enabled but no folder on disk", `{"templates":true}`, false, nil},
		{"no core-plugins.json at all", "", true, nil},
		{"unreadable json", `{not json`, true, nil},
		{"empty file", "", true, nil},
		{"json null", `null`, true, nil},
		// Older Obsidian releases wrote an ARRAY of enabled plugin ids. This is
		// handled defensively; the object form above is the one verified live.
		{"array form, enabled", `["file-explorer","templates"]`, true, []string{"templates"}},
		{"array form, not enabled", `["file-explorer","graph"]`, true, nil},
		{"array form of non-strings", `[1,2,3]`, true, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := ObsidianTemplateFolders(writeTemplateVault(t, tc.corePlugins, tc.withFolder))
			if !slices.Equal(got, tc.want) {
				t.Errorf("ObsidianTemplateFolders = %v, want %v", got, tc.want)
			}
		})
	}
}

// A file named "templates" rather than a directory must not exclude anything:
// the gate asks whether the FOLDER exists, and a same-named note beside it is
// exactly the sort of coincidence that would otherwise purge an index.
func TestObsidianTemplateFolders_AFileNamedTemplatesIsNotAFolder(t *testing.T) {
	root := writeTemplateVault(t, `{"templates":true}`, false)
	if err := os.WriteFile(filepath.Join(root, "templates"), []byte("not a folder"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := ObsidianTemplateFolders(root); len(got) != 0 {
		t.Errorf("ObsidianTemplateFolders = %v, want none", got)
	}
}

// Obsidian's own configuration stays authoritative: when it names a folder, the
// convention never adds a second one.
func TestObsidianTemplateFolders_ConfigurationWinsOverTheConvention(t *testing.T) {
	root := writeTemplateVault(t, `{"templates":true}`, true)
	if err := os.WriteFile(filepath.Join(root, ".obsidian", "templates.json"), []byte(`{"folder":"scaffolding"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	got := ObsidianTemplateFolders(root)
	if !slices.Equal(got, []string{"scaffolding"}) {
		t.Errorf("ObsidianTemplateFolders = %v, want [scaffolding]", got)
	}
}

// CollectLiveDocs feeds store.NewResolver for lint, repair-links, relink,
// suggest-target and the move/rename ambiguity guard. It had NO template
// exclusion at all, so templates/note.md was a resolver candidate by title, and
// a template could make a legitimate [[name]] read as ambiguous and refuse a
// non-force move.
func TestCollectLiveDocs_ExcludesTemplateFolders(t *testing.T) {
	root := writeTemplateVault(t, `{"templates":true}`, true)
	if err := os.WriteFile(filepath.Join(root, "templates", "note.md"),
		[]byte("---\ntitle: Meeting Notes\naliases: [Standup]\n---\n{{date}}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "real.md"),
		[]byte("---\ntitle: Meeting Notes\n---\nThe real one.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	docs, aliases, err := CollectLiveDocs(root)
	if err != nil {
		t.Fatalf("CollectLiveDocs: %v", err)
	}
	for _, d := range docs {
		if d.Path == "templates/note.md" {
			t.Errorf("the template is still a resolver candidate: %+v", d)
		}
	}
	if len(docs) != 1 || docs[0].Path != "real.md" {
		t.Errorf("docs = %+v, want only real.md", docs)
	}
	if got := aliases["Standup"]; len(got) != 0 {
		t.Errorf("the template donated the alias %q -> %v", "Standup", got)
	}
}

// The other half: with the gate off, those same files ARE resolver candidates,
// because the folder is then an ordinary one holding ordinary notes.
func TestCollectLiveDocs_KeepsATemplatesFolderWithoutThePlugin(t *testing.T) {
	root := writeTemplateVault(t, `{"templates":false}`, true)
	if err := os.WriteFile(filepath.Join(root, "templates", "meeting-notes.md"),
		[]byte("---\ntitle: Meeting Notes\n---\nHow we run meetings.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	docs, _, err := CollectLiveDocs(root)
	if err != nil {
		t.Fatalf("CollectLiveDocs: %v", err)
	}
	if len(docs) != 1 || docs[0].Path != "templates/meeting-notes.md" {
		t.Errorf("docs = %+v, want templates/meeting-notes.md", docs)
	}
}
