package vault

import "testing"

// TestHasTemplatePlaceholders covers the discrimination between an Obsidian
// template (placeholder tokens in the frontmatter) and a genuinely malformed
// note. A real note with bad YAML but no placeholders must return false so lint
// still reports it as a parse error.
//
// Moved here from package cli with the function: lint, import and the indexer
// share one definition of a template now, so its test belongs with it.
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
		if got := HasTemplatePlaceholders([]byte(c.raw)); got != c.want {
			t.Errorf("%s: HasTemplatePlaceholders = %v, want %v", c.name, got, c.want)
		}
	}
}
