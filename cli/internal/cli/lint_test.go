package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestIsAssetOrAnchorTarget(t *testing.T) {
	cases := []struct {
		target string
		want   bool
	}{
		{"", true},              // anchor-only / same-doc link
		{"note", false},         // bare note reference
		{"note.md", false},      // markdown note
		{"board.canvas", false}, // indexed canvas
		{"cfg.base", false},     // indexed base
		{"img.png", true},       // image asset
		{"doc.pdf", true},       // pdf asset
		{"clip.mp4", true},      // video asset
		{"sub/dir/note.md", false},
		{"sub/dir/pic.jpg", true},
	}
	for _, c := range cases {
		if got := isAssetOrAnchorTarget(c.target); got != c.want {
			t.Errorf("isAssetOrAnchorTarget(%q) = %v, want %v", c.target, got, c.want)
		}
	}
}

// TestLint_SkipsAssetsAndAnchors runs `2nb lint --json` over a vault whose only
// genuinely broken link is [[missing]] — images, anchor-only links, and links
// to indexed .canvas files must NOT be reported (the noise the fix removed).
func TestLint_SkipsAssetsAndAnchors(t *testing.T) {
	_, root := newContractVault(t)

	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("overview.md", "---\nid: o1\ntitle: Overview\ntype: note\nstatus: draft\n---\nNo links here.\n")
	write("board.canvas", `{"nodes":[{"id":"n1","type":"text","text":"card"}],"edges":[]}`)
	write("note.md", "---\nid: n1\ntitle: Note\ntype: note\nstatus: draft\n---\n"+
		"See [[overview]] and [[missing]]. "+
		"Image ![[pic.png]] and ![alt](photo.jpg). "+
		"Anchor [top](#section). Canvas [[board.canvas]].\n")

	out, err := runCLIArgs(t, root, "lint", "--json")
	if err != nil {
		t.Fatalf("lint: %v", err)
	}

	var report struct {
		Issues []struct {
			Path    string `json:"path"`
			Level   string `json:"level"`
			Message string `json:"message"`
		} `json:"issues"`
		Errors int `json:"errors"`
		Warns  int `json:"warnings"`
	}
	if err := json.Unmarshal(out, &report); err != nil {
		t.Fatalf("decode lint report: %v\n%s", err, out)
	}

	if report.Errors != 0 {
		t.Errorf("expected 0 errors, got %d: %+v", report.Errors, report.Issues)
	}
	if report.Warns != 1 {
		t.Fatalf("expected exactly 1 broken-link warning ([[missing]]), got %d: %+v", report.Warns, report.Issues)
	}
	if got := report.Issues[len(report.Issues)-1].Message; got == "" ||
		!containsSubstr(got, "missing") {
		t.Errorf("expected the warning to be about 'missing', got %q", got)
	}
}

func containsSubstr(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// TestLint_RecursesIntoSubdirs proves `2nb lint` reaches notes in
// subdirectories. The pre-fix top-level "*.md" glob silently skipped every
// nested note (and any broken links inside them); only files in the vault root
// were ever checked.
func TestLint_RecursesIntoSubdirs(t *testing.T) {
	_, root := newContractVault(t)

	sub := filepath.Join(root, "resources", "deep")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	// No 'id' on purpose (path-based identity); one genuinely broken wikilink.
	if err := os.WriteFile(filepath.Join(sub, "buried.md"),
		[]byte("---\ntitle: Buried\ntype: note\nstatus: draft\n---\nLink to [[does-not-exist]].\n"),
		0o644); err != nil {
		t.Fatal(err)
	}
	// A note inside a dot-directory must be pruned by the recursive walk
	// (filepath.SkipDir on dot-dirs); its broken link must not be reported.
	dotdir := filepath.Join(root, ".obsidian")
	if err := os.MkdirAll(dotdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dotdir, "hidden.md"),
		[]byte("---\ntitle: Hidden\n---\nLink to [[ghost-in-dotdir]].\n"),
		0o644); err != nil {
		t.Fatal(err)
	}

	out, err := runCLIArgs(t, root, "lint", "--json")
	if err != nil {
		t.Fatalf("lint: %v", err)
	}

	var report struct {
		Issues []struct {
			Path    string `json:"path"`
			Level   string `json:"level"`
			Message string `json:"message"`
		} `json:"issues"`
		Files  int `json:"files_checked"`
		Errors int `json:"errors"`
		Warns  int `json:"warnings"`
	}
	if err := json.Unmarshal(out, &report); err != nil {
		t.Fatalf("decode lint report: %v\n%s", err, out)
	}

	if report.Errors != 0 {
		t.Errorf("expected 0 errors, got %d: %+v", report.Errors, report.Issues)
	}
	found := false
	for _, is := range report.Issues {
		if containsSubstr(is.Path, "buried.md") && containsSubstr(is.Message, "does-not-exist") {
			found = true
		}
		if containsSubstr(is.Message, "ghost-in-dotdir") {
			t.Errorf("dot-directory note must be pruned, but its link was checked: %+v", is)
		}
	}
	if !found {
		t.Errorf("expected a broken-link warning for the subdirectory note buried.md, got: %+v", report.Issues)
	}
}

// TestLint_ExplicitGlobArg confirms that passing a glob argument is honoured
// verbatim (top-level only here) rather than triggering the recursive default.
func TestLint_ExplicitGlobArg(t *testing.T) {
	_, root := newContractVault(t)

	if err := os.WriteFile(filepath.Join(root, "top.md"),
		[]byte("---\ntitle: Top\n---\nLink to [[missing-top]].\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(root, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "nested.md"),
		[]byte("---\ntitle: Nested\n---\nLink to [[missing-nested]].\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Explicit top-level glob: only top.md is in scope; sub/nested.md is not.
	out, err := runCLIArgs(t, root, "lint", "*.md")
	if err != nil {
		t.Fatalf("lint: %v", err)
	}

	var report struct {
		Issues []struct {
			Path    string `json:"path"`
			Message string `json:"message"`
		} `json:"issues"`
		Warns int `json:"warnings"`
	}
	if jerr := json.Unmarshal(out, &report); jerr != nil {
		t.Fatalf("decode lint report: %v\n%s", jerr, out)
	}

	sawTop, sawNested := false, false
	for _, is := range report.Issues {
		if containsSubstr(is.Message, "missing-top") {
			sawTop = true
		}
		if containsSubstr(is.Message, "missing-nested") {
			sawNested = true
		}
	}
	if !sawTop {
		t.Errorf("explicit glob *.md should lint top.md, got: %+v", report.Issues)
	}
	if sawNested {
		t.Errorf("explicit glob *.md must not recurse into sub/, got: %+v", report.Issues)
	}
}

// TestHasTemplatePlaceholders covers the discrimination between an Obsidian
// template (placeholder tokens in the frontmatter) and a genuinely malformed
// note. A real note with bad YAML but no placeholders must return false so lint
// still reports it as a parse error.
func TestHasTemplatePlaceholders(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want bool
	}{
		{"template frontmatter", "---\ntitle: {{date}}\ndate: {{date}}\n---\n# body\n", true},
		{"template no closing delim", "---\ndate: {{date}}\n", true},
		{"body mentions braces only", "---\ntitle: Real\n---\nUse {{mustache}} in templating.\n", false},
		{"genuinely malformed note", "---\ntitle: Broken\ntags: [a, b\n---\nbad YAML, no tokens\n", false},
		{"normal note", "---\ntitle: Fine\ntype: note\n---\nbody\n", false},
		{"no frontmatter", "# Just a heading\n", false},
		{"empty", "", false},
	}
	for _, c := range cases {
		if got := hasTemplatePlaceholders([]byte(c.raw)); got != c.want {
			t.Errorf("%s: hasTemplatePlaceholders = %v, want %v", c.name, got, c.want)
		}
	}
}

// TestLint_SkipsTemplatePlaceholders confirms that Obsidian template files —
// whose frontmatter holds unresolved {{placeholder}} tokens (invalid YAML) — do
// not produce false-positive parse errors once lint recurses into subfolders.
// (Kept to zero errors so runLint does not os.Exit; the "real malformed note
// still errors" discrimination is covered by TestHasTemplatePlaceholders.)
func TestLint_SkipsTemplatePlaceholders(t *testing.T) {
	_, root := newContractVault(t)

	tdir := filepath.Join(root, "templates")
	if err := os.MkdirAll(tdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tdir, "daily.md"),
		[]byte("---\ntitle: {{date}}\ndate: {{date}}\ntags: [daily]\n---\n# {{date}}\n"),
		0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "real.md"),
		[]byte("---\ntitle: Real\ntype: note\nstatus: draft\n---\nA normal note.\n"),
		0o644); err != nil {
		t.Fatal(err)
	}

	out, err := runCLIArgs(t, root, "lint", "--json")
	if err != nil {
		t.Fatalf("lint: %v", err)
	}

	var report struct {
		Issues []struct {
			Path    string `json:"path"`
			Message string `json:"message"`
		} `json:"issues"`
		Errors int `json:"errors"`
	}
	if jerr := json.Unmarshal(out, &report); jerr != nil {
		t.Fatalf("decode lint report: %v\n%s", jerr, out)
	}

	if report.Errors != 0 {
		t.Errorf("template scaffolding must not produce lint errors, got %d: %+v", report.Errors, report.Issues)
	}
	for _, is := range report.Issues {
		if containsSubstr(is.Path, "daily.md") {
			t.Errorf("template file should be skipped, but was flagged: %+v", is)
		}
	}
}

// TestLint_NoIDIsNotAnError confirms a missing frontmatter 'id' is not a lint
// error under the path-based identity model. Covers a note with frontmatter but
// no id, a note with no frontmatter at all, and an empty file — all valid in a
// vanilla Obsidian vault.
func TestLint_NoIDIsNotAnError(t *testing.T) {
	_, root := newContractVault(t)

	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("no-id.md", "---\ntitle: No ID\ntype: note\nstatus: draft\n---\nBody, no id field.\n")
	write("no-frontmatter.md", "# Just a heading\n\nPlain note, zero frontmatter.\n")
	write("empty.md", "")

	out, err := runCLIArgs(t, root, "lint", "--json")
	if err != nil {
		t.Fatalf("lint: %v", err)
	}

	var report struct {
		Issues []struct {
			Path    string `json:"path"`
			Message string `json:"message"`
		} `json:"issues"`
		Errors int `json:"errors"`
	}
	if err := json.Unmarshal(out, &report); err != nil {
		t.Fatalf("decode lint report: %v\n%s", err, out)
	}

	if report.Errors != 0 {
		t.Errorf("id-less / frontmatter-less notes must not be lint errors, got %d: %+v", report.Errors, report.Issues)
	}
	for _, is := range report.Issues {
		if containsSubstr(is.Message, "id") {
			t.Errorf("unexpected id-related lint issue: %+v", is)
		}
	}
}
