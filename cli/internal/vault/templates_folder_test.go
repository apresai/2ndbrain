package vault

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeObsidianJSON(t *testing.T, root string, parts []string, body string) {
	t.Helper()
	p := filepath.Join(append([]string{root}, parts...)...)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
}

// TestIndexSkipsTheObsidianTemplatesFolder: a template is placeholder syntax,
// not a note. One real vault logged "index file failed" three times on every
// single index run for templates/note.md and templates/daily.md, whose
// frontmatter is "{{date}}".
func TestIndexSkipsTheObsidianTemplatesFolder(t *testing.T) {
	root := t.TempDir()
	v, err := Init(root)
	if err != nil {
		t.Fatalf("init vault: %v", err)
	}
	defer v.Close()

	writeObsidianJSON(t, root, []string{".obsidian", "templates.json"}, `{"folder":"templates"}`)
	writeNote(t, root, "templates/note.md", "---\ndate: {{date}}\n---\n\n{{title}}\n")
	writeNote(t, root, "templates-archive/old.md", "---\ntitle: Archived\n---\n\nstill a real note\n")
	writeNote(t, root, "real.md", "---\ntitle: Real\n---\n\nan actual note\n")

	stats, err := IndexVault(v, nil)
	if err != nil {
		t.Fatalf("index: %v", err)
	}
	if len(stats.Unparseable) != 0 {
		t.Errorf("unparseable = %+v, want none: the template was never opened", stats.Unparseable)
	}
	if stats.Errors != 0 {
		t.Errorf("errors = %d, want 0", stats.Errors)
	}

	var indexed []string
	rows, err := v.DB.Conn().Query(`SELECT path FROM documents ORDER BY path`)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			t.Fatalf("scan: %v", err)
		}
		indexed = append(indexed, p)
	}
	for _, p := range indexed {
		if p == "templates/note.md" {
			t.Errorf("the template was indexed; indexed = %v", indexed)
		}
	}
	// A sibling whose name merely STARTS with the folder name is a real folder.
	var sawArchive, sawReal bool
	for _, p := range indexed {
		sawArchive = sawArchive || p == "templates-archive/old.md"
		sawReal = sawReal || p == "real.md"
	}
	if !sawArchive {
		t.Errorf("templates-archive/old.md was excluded; the match must be on whole path segments. indexed = %v", indexed)
	}
	if !sawReal {
		t.Errorf("real.md was not indexed; indexed = %v", indexed)
	}
}

// TestIndexSkipsTheTemplaterFolder: Templater stores its own folder setting, and
// a vault using Templater rather than the core plugin has the same problem.
func TestIndexSkipsTheTemplaterFolder(t *testing.T) {
	root := t.TempDir()
	v, err := Init(root)
	if err != nil {
		t.Fatalf("init vault: %v", err)
	}
	defer v.Close()

	writeObsidianJSON(t, root,
		[]string{".obsidian", "plugins", "templater-obsidian", "data.json"},
		`{"templates_folder":"_tmpl","other":"ignored"}`)
	writeNote(t, root, "_tmpl/daily.md", "---\ndate: {{date}}\n---\n\nbody\n")
	writeNote(t, root, "real.md", "---\ntitle: Real\n---\n\nbody\n")

	stats, err := IndexVault(v, nil)
	if err != nil {
		t.Fatalf("index: %v", err)
	}
	if stats.DocsIndexed != 1 {
		t.Errorf("docs_indexed = %d, want 1 (only real.md)", stats.DocsIndexed)
	}
	if got := ObsidianTemplateFolders(root); len(got) != 1 || got[0] != "_tmpl" {
		t.Errorf("ObsidianTemplateFolders = %v, want [_tmpl]", got)
	}
}

// TestVaultWithNoTemplateConfigIndexesEverything: the exclusion is opt-in by
// virtue of Obsidian's own setting, so a vault that never configured one must
// behave exactly as before.
func TestVaultWithNoTemplateConfigIndexesEverything(t *testing.T) {
	root := t.TempDir()
	v, err := Init(root)
	if err != nil {
		t.Fatalf("init vault: %v", err)
	}
	defer v.Close()

	writeNote(t, root, "templates/note.md", "---\ntitle: Looks Like A Template\n---\n\nbody\n")
	writeNote(t, root, "real.md", "---\ntitle: Real\n---\n\nbody\n")

	stats, err := IndexVault(v, nil)
	if err != nil {
		t.Fatalf("index: %v", err)
	}
	if stats.DocsIndexed != 2 {
		t.Errorf("docs_indexed = %d, want 2: with no template folder configured nothing is excluded", stats.DocsIndexed)
	}
	if got := ObsidianTemplateFolders(root); len(got) != 0 {
		t.Errorf("ObsidianTemplateFolders = %v, want none", got)
	}
}

// TestTemplateFolderConfigIsNeverFatal: a missing, unreadable or malformed
// config, or one naming a path outside the vault, means "nothing configured".
// The setting is a convenience; it can never fail an index.
func TestTemplateFolderConfigIsNeverFatal(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"not json", "this is not json at all"},
		{"field missing", `{"other":"x"}`},
		{"field not a string", `{"folder":42}`},
		{"empty", `{"folder":""}`},
		{"absolute path", `{"folder":"/etc"}`},
		{"escapes the vault", `{"folder":"../outside"}`},
		{"dot", `{"folder":"."}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			writeObsidianJSON(t, root, []string{".obsidian", "templates.json"}, tc.body)
			if got := ObsidianTemplateFolders(root); len(got) != 0 {
				t.Errorf("ObsidianTemplateFolders = %v, want none", got)
			}
		})
	}
}

// TestIndexSingleFileRefusesATemplate: the macOS app reindexes each note as it
// is saved, so without the same check there a saved template would walk straight
// back into the index the full run just excluded.
func TestIndexSingleFileRefusesATemplate(t *testing.T) {
	root := t.TempDir()
	v, err := Init(root)
	if err != nil {
		t.Fatalf("init vault: %v", err)
	}
	defer v.Close()

	writeObsidianJSON(t, root, []string{".obsidian", "templates.json"}, `{"folder":"templates"}`)
	abs := writeNote(t, root, "templates/note.md", "---\ntitle: T\n---\n\nbody\n")

	err = IndexSingleFile(v, abs)
	if err == nil {
		t.Fatal("IndexSingleFile indexed a template; the per-save path must honor the same exclusion")
	}
	// Naming the folder is the actionable half: "not indexed" alone does not say
	// which setting caused it.
	if !strings.Contains(err.Error(), `"templates"`) {
		t.Errorf("error = %v, want it to name the excluded folder", err)
	}
}
