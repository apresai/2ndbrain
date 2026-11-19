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
		{"templater frontmatter unquoted", "---\ntitle: <% tp.file.title %>\ncreated: <% tp.date.now() %>\n---\n# body\n", true},
		{"templater frontmatter quoted", "---\ntitle: \"<% tp.file.title %>\"\ncreated: \"<% tp.date.now() %>\"\n---\n# body\n", true},
		{"templater execution tag", "---\n<%* let d = tp.date.now(); %>\ntitle: <% d %>\n---\n# body\n", true},
		{"templater execution tag unclosed before body", "---\n<%* let d = tp.date.now(); %>\ntitle: <% d %>\n\nBody\n", true},
		{"templater no closing delim", "---\ncreated: <% tp.date.now() %>\n", true},
		{"body mentions braces only", "---\ntitle: Real\n---\nUse {{mustache}} in templating.\n", false},
		{"body mentions templater only", "---\ntitle: Real\n---\nUse <% tp.date.now() %> in templating.\n", false},
		{"hr at top followed by templater in body", "---\n\n# Guide to Templater\n\nUse `<% tp.date.now() %>` in templates, and see [[definitely-broken-link]].\n", false},
		{"hr at top CRLF followed by mustache in body", "---\r\n\r\n# Guide\r\n\r\nUse {{date}} in templates.\r\n", false},
		{"unclosed normal frontmatter followed by templater in body", "---\ntitle: Normal\n\nBody with <% tp.date.now() %>\n", false},
		{"unclosed normal frontmatter followed by prose without blank line", "---\ntitle: Normal\nBody prose with <% tp.date.now() %>\n", false},
		{"four-dash divider not frontmatter", "----\ntitle: {{date}}\n", false},
		{"dash-text line not frontmatter", "--- text\ntitle: {{date}}\n", false},
		{"four-dash divider in body not closing fence", "---\ntitle: Normal\n----\nBody with <% tp.date.now() %>\n", false},
		{"percent without templater opener", "---\ntitle: \"100% < 200%\"\ntype: note\n---\nbody\n", false},
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
