package document

import (
	"reflect"
	"strings"
	"testing"
)

func TestHasUnclosedFrontmatter(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    bool
	}{
		{
			name:    "closed valid frontmatter",
			content: "---\ntitle: Good\n---\n\nBody\n",
			want:    false,
		},
		{
			name:    "empty frontmatter block",
			content: "---\n---\n\nBody\n",
			want:    false,
		},
		{
			name:    "plain markdown without fence",
			content: "# Heading\n\nBody text.\n",
			want:    false,
		},
		{
			name:    "empty file",
			content: "",
			want:    false,
		},
		{
			name:    "horizontal rule at top followed by heading",
			content: "---\n\n# Heading\n\nBody\n",
			want:    false,
		},
		{
			name:    "horizontal rule at top followed by colon prose",
			content: "---\n\nNote: this is prose after a horizontal rule, not frontmatter.\n\nMore prose.\n",
			want:    false,
		},
		{
			name:    "horizontal rule followed by heading and blank then key",
			content: "---\n# Heading\n\ntitle: Not Frontmatter\n",
			want:    false,
		},
		{
			name:    "unclosed frontmatter starting with YAML comment",
			content: "---\n# Frontmatter comment\ntitle: Missing Close\ntype: note\n\n# Body Heading\nBody.\n",
			want:    true,
		},
		{
			name:    "unclosed frontmatter before heading",
			content: "---\ntitle: Missing Close\ntype: note\n\n# Heading\nBody.\n",
			want:    true,
		},
		{
			name:    "unclosed frontmatter directly above heading",
			content: "---\ntitle: Missing Close\ntype: note\n# Heading\nBody.\n",
			want:    true,
		},
		{
			name:    "unclosed frontmatter at EOF",
			content: "---\ntitle: Only Frontmatter\nstatus: draft\n",
			want:    true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := HasUnclosedFrontmatter([]byte(tc.content)); got != tc.want {
				t.Errorf("HasUnclosedFrontmatter() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestSplitAndReassembleRawFrontmatter_UnclosedAndMalformed(t *testing.T) {
	t.Run("unclosed fence preserves body", func(t *testing.T) {
		orig := "---\ntitle: Broken Note\ntype: note\n\n# Heading\n\nBody with [[link]].\n"
		parts, ok := SplitRawFrontmatter([]byte(orig))
		if !ok {
			t.Fatal("expected SplitRawFrontmatter to succeed")
		}
		if !parts.Unclosed {
			t.Fatal("expected Unclosed = true")
		}
		if !strings.Contains(parts.RawYAML, "title: Broken Note") || !strings.Contains(parts.RawYAML, "type: note") {
			t.Fatalf("unexpected RawYAML: %q", parts.RawYAML)
		}
		if parts.Body != "\n# Heading\n\nBody with [[link]].\n" {
			t.Fatalf("unexpected Body: %q", parts.Body)
		}

		rebuilt := ReassembleFrontmatter(parts, parts.RawYAML)
		doc, err := Parse("broken.md", rebuilt)
		if err != nil {
			t.Fatalf("Parse(rebuilt) failed: %v\n%s", err, rebuilt)
		}
		if doc.Title != "Broken Note" || doc.Type != "note" {
			t.Fatalf("unexpected parsed doc: %+v", doc)
		}
		if !strings.Contains(doc.Body, "# Heading\n\nBody with [[link]].") {
			t.Fatalf("body lost during reassembly: %q", doc.Body)
		}
	})

	t.Run("malformed YAML with CRLF preserves line endings", func(t *testing.T) {
		orig := "---\r\ntitle: Foo\r\ntags:\r\n\t- alpha\r\n---\r\n\r\nBody\r\n"
		parts, ok := SplitRawFrontmatter([]byte(orig))
		if !ok {
			t.Fatal("expected SplitRawFrontmatter to succeed")
		}
		if parts.Unclosed {
			t.Fatal("expected Unclosed = false")
		}
		if parts.Newline != "\r\n" {
			t.Fatalf("Newline = %q, want CRLF", parts.Newline)
		}
		repaired := RepairTabsInYAML(parts.RawYAML)
		rebuilt := ReassembleFrontmatter(parts, repaired)
		if !strings.Contains(string(rebuilt), "\r\n") {
			t.Fatalf("expected CRLF preserved in rebuilt output:\n%q", string(rebuilt))
		}
		doc, err := Parse("crlf.md", rebuilt)
		if err != nil {
			t.Fatalf("Parse(rebuilt) failed: %v\n%s", err, rebuilt)
		}
		if len(doc.Tags) != 1 || doc.Tags[0] != "alpha" {
			t.Fatalf("unexpected tags: %v", doc.Tags)
		}
	})

	t.Run("unclosed fence with leading and interior YAML comments preserves comments and body", func(t *testing.T) {
		orig := "---\n# Leading comment\ntitle: Note With Comments\n# Interior comment\ntype: note\n\n# Body Heading\n\nBody text.\n"
		parts, ok := SplitRawFrontmatter([]byte(orig))
		if !ok {
			t.Fatal("expected SplitRawFrontmatter to succeed")
		}
		if !parts.Unclosed {
			t.Fatal("expected Unclosed = true")
		}
		if !strings.Contains(parts.RawYAML, "# Leading comment") || !strings.Contains(parts.RawYAML, "# Interior comment") || !strings.Contains(parts.RawYAML, "type: note") {
			t.Fatalf("RawYAML lost comment or key: %q", parts.RawYAML)
		}
		if parts.Body != "\n# Body Heading\n\nBody text.\n" {
			t.Fatalf("unexpected Body: %q", parts.Body)
		}
		rebuilt := ReassembleFrontmatter(parts, parts.RawYAML)
		doc, err := Parse("comments.md", rebuilt)
		if err != nil {
			t.Fatalf("Parse(rebuilt) failed: %v\n%s", err, rebuilt)
		}
		if doc.Title != "Note With Comments" || doc.Type != "note" {
			t.Fatalf("unexpected doc metadata: %+v", doc)
		}
		if !strings.Contains(doc.Body, "# Body Heading\n\nBody text.") {
			t.Fatalf("body lost: %q", doc.Body)
		}
	})

	t.Run("unclosed fence followed by body horizontal rule preserves all body sections", func(t *testing.T) {
		orig := "---\ntitle: Unclosed With HR\ntype: note\nstatus: draft\n\n# Section 1\n\nSome body text.\n\n---\n\n# Section 2\n"
		parts, ok := SplitRawFrontmatter([]byte(orig))
		if !ok {
			t.Fatal("expected SplitRawFrontmatter to succeed")
		}
		if !parts.Unclosed {
			t.Fatalf("expected Unclosed = true when body contains HR, got parts=%+v", parts)
		}
		wantYAML := "title: Unclosed With HR\ntype: note\nstatus: draft"
		if parts.RawYAML != wantYAML {
			t.Fatalf("RawYAML = %q, want %q", parts.RawYAML, wantYAML)
		}
		wantBody := "\n# Section 1\n\nSome body text.\n\n---\n\n# Section 2\n"
		if parts.Body != wantBody {
			t.Fatalf("Body = %q, want %q", parts.Body, wantBody)
		}
		rebuilt := ReassembleFrontmatter(parts, parts.RawYAML)
		doc, err := Parse("hr.md", rebuilt)
		if err != nil {
			t.Fatalf("Parse(rebuilt) failed: %v\n%s", err, rebuilt)
		}
		if doc.Title != "Unclosed With HR" || doc.Status != "draft" {
			t.Fatalf("unexpected parsed doc: %+v", doc)
		}
		for _, mustKeep := range []string{"# Section 1", "Some body text.", "---", "# Section 2"} {
			if !strings.Contains(doc.Body, mustKeep) {
				t.Fatalf("body lost %q during reassembly:\n%s", mustKeep, doc.Body)
			}
		}
	})

	t.Run("unclosed fence with CRLF and body horizontal rule preserves CRLF and sections", func(t *testing.T) {
		orig := "---\r\ntitle: Unclosed CRLF HR\r\ntype: note\r\n\r\n# Section 1\r\n\r\nBody.\r\n\r\n---\r\n\r\n# Section 2\r\n"
		parts, ok := SplitRawFrontmatter([]byte(orig))
		if !ok || !parts.Unclosed {
			t.Fatalf("expected unclosed split, got ok=%v parts=%+v", ok, parts)
		}
		if parts.Newline != "\r\n" {
			t.Fatalf("Newline = %q, want CRLF", parts.Newline)
		}
		rebuilt := string(ReassembleFrontmatter(parts, parts.RawYAML))
		if !strings.Contains(rebuilt, "\r\n# Section 1\r\n\r\nBody.\r\n\r\n---\r\n\r\n# Section 2\r\n") {
			t.Fatalf("CRLF body sections altered:\n%q", rebuilt)
		}
	})

	t.Run("malformed closed YAML with blank line inside indented block scalar stays closed", func(t *testing.T) {
		orig := "---\ntitle: Broken [unclosed\nsummary: |\n  para 1\n\n  para 2\nstatus: draft\n---\n\nBody.\n"
		parts, ok := SplitRawFrontmatter([]byte(orig))
		if !ok {
			t.Fatal("expected SplitRawFrontmatter to succeed")
		}
		if parts.Unclosed {
			t.Fatalf("expected Unclosed = false for closed malformed YAML with block scalar blank line, got parts=%+v", parts)
		}
		if !strings.Contains(parts.RawYAML, "para 2") || !strings.Contains(parts.RawYAML, "status: draft") {
			t.Fatalf("RawYAML truncated block scalar or trailing key: %q", parts.RawYAML)
		}
		if parts.Body != "\nBody.\n" {
			t.Fatalf("Body = %q, want \\nBody.\\n", parts.Body)
		}
		if got := string(ReassembleFrontmatter(parts, parts.RawYAML)); got != orig {
			t.Fatalf("ReassembleFrontmatter altered closed note:\ngot:\n%q\nwant:\n%q", got, orig)
		}
	})

	t.Run("malformed closed YAML with blank line inside indented list stays closed", func(t *testing.T) {
		orig := "---\ntitle: Broken [unclosed\ntags:\n  - alpha\n\n  - beta\nstatus: draft\n---\n\nBody.\n"
		parts, ok := SplitRawFrontmatter([]byte(orig))
		if !ok {
			t.Fatal("expected SplitRawFrontmatter to succeed")
		}
		if parts.Unclosed {
			t.Fatalf("expected Unclosed = false for closed malformed YAML with indented list blank line, got parts=%+v", parts)
		}
		if !strings.Contains(parts.RawYAML, "- beta") || !strings.Contains(parts.RawYAML, "status: draft") {
			t.Fatalf("RawYAML truncated indented list or trailing key: %q", parts.RawYAML)
		}
		if parts.Body != "\nBody.\n" {
			t.Fatalf("Body = %q, want \\nBody.\\n", parts.Body)
		}
		if got := string(ReassembleFrontmatter(parts, parts.RawYAML)); got != orig {
			t.Fatalf("ReassembleFrontmatter altered closed note:\ngot:\n%q\nwant:\n%q", got, orig)
		}
	})

	t.Run("unclosed frontmatter with block scalar blank line still splits before body heading", func(t *testing.T) {
		orig := "---\ntitle: Unclosed Scalar\nsummary: |\n  para 1\n\n  para 2\n\n# Body Heading\n\nBody text.\n"
		parts, ok := SplitRawFrontmatter([]byte(orig))
		if !ok || !parts.Unclosed {
			t.Fatalf("expected Unclosed = true, got ok=%v parts=%+v", ok, parts)
		}
		if !strings.Contains(parts.RawYAML, "para 2") {
			t.Fatalf("RawYAML lost block scalar paragraph 2: %q", parts.RawYAML)
		}
		if parts.Body != "\n# Body Heading\n\nBody text.\n" {
			t.Fatalf("Body = %q, want \\n# Body Heading\\n\\nBody text.\\n", parts.Body)
		}
	})

	t.Run("malformed closed YAML with blank line inside multiline flow list stays closed", func(t *testing.T) {
		orig := "---\ntitle: Flow List Note\ntags: [\n  alpha,\n\n  beta\n]\nbad: [unclosed\ntype: note\nstatus: draft\n---\n\n# Flow List Note\n\nBody text.\n"
		parts, ok := SplitRawFrontmatter([]byte(orig))
		if !ok {
			t.Fatal("expected SplitRawFrontmatter to succeed")
		}
		if parts.Unclosed {
			t.Fatalf("expected Unclosed = false for closed malformed YAML with flow list blank line, got parts=%+v", parts)
		}
		if !strings.Contains(parts.RawYAML, "beta") || !strings.Contains(parts.RawYAML, "bad: [unclosed") || !strings.Contains(parts.RawYAML, "status: draft") {
			t.Fatalf("RawYAML truncated flow list or trailing keys: %q", parts.RawYAML)
		}
		if parts.Body != "\n# Flow List Note\n\nBody text.\n" {
			t.Fatalf("Body = %q, want \\n# Flow List Note\\n\\nBody text.\\n", parts.Body)
		}
		if got := string(ReassembleFrontmatter(parts, parts.RawYAML)); got != orig {
			t.Fatalf("ReassembleFrontmatter altered closed note:\ngot:\n%q\nwant:\n%q", got, orig)
		}
	})

	t.Run("malformed closed YAML with blank line inside flow map and quoted brackets stays closed", func(t *testing.T) {
		orig := "---\ntitle: \"Quoted [bracket\"\nmeta: {\n  one: \"a ] b\",\n\n  two: 'c [ d'\n} # [comment\nbad: [unclosed\nstatus: draft\n---\n\nBody.\n"
		parts, ok := SplitRawFrontmatter([]byte(orig))
		if !ok {
			t.Fatal("expected SplitRawFrontmatter to succeed")
		}
		if parts.Unclosed {
			t.Fatalf("expected Unclosed = false, got parts=%+v", parts)
		}
		if parts.Body != "\nBody.\n" {
			t.Fatalf("Body = %q, want \\nBody.\\n", parts.Body)
		}
		if got := string(ReassembleFrontmatter(parts, parts.RawYAML)); got != orig {
			t.Fatalf("ReassembleFrontmatter altered closed note:\ngot:\n%q\nwant:\n%q", got, orig)
		}
	})

	t.Run("unclosed frontmatter plain prose followed by body HR splits cleanly", func(t *testing.T) {
		orig := "---\ntitle: Plain Prose HR\ntype: note\n\nFirst paragraph without a heading.\n\n---\n\n# Section 2\n"
		parts, ok := SplitRawFrontmatter([]byte(orig))
		if !ok || !parts.Unclosed {
			t.Fatalf("expected Unclosed = true, got ok=%v parts=%+v", ok, parts)
		}
		if parts.RawYAML != "title: Plain Prose HR\ntype: note" {
			t.Fatalf("RawYAML = %q", parts.RawYAML)
		}
		if parts.Body != "\nFirst paragraph without a heading.\n\n---\n\n# Section 2\n" {
			t.Fatalf("Body = %q", parts.Body)
		}
	})

	t.Run("unclosed malformed YAML followed by body heading and HR splits cleanly", func(t *testing.T) {
		orig := "---\ntitle: Broken [unclosed\ntags:\n\t- alpha\n\n# Section 1\n\nBody before HR.\n\n---\n\n# Section 2\n"
		parts, ok := SplitRawFrontmatter([]byte(orig))
		if !ok || !parts.Unclosed {
			t.Fatalf("expected Unclosed = true, got ok=%v parts=%+v", ok, parts)
		}
		if !strings.Contains(parts.RawYAML, "title: Broken [unclosed") || !strings.Contains(parts.RawYAML, "- alpha") {
			t.Fatalf("unexpected RawYAML: %q", parts.RawYAML)
		}
		if parts.Body != "\n# Section 1\n\nBody before HR.\n\n---\n\n# Section 2\n" {
			t.Fatalf("Body = %q", parts.Body)
		}
	})
}

func TestNonYAMLProseLines(t *testing.T) {
	if got := NonYAMLProseLines("title: Broken: colon\ntags:\n\t- a\n# comment\n"); len(got) != 0 {
		t.Fatalf("expected no prose lines for broken YAML, got %v", got)
	}
	got := NonYAMLProseLines("title: X\n# Section 1\n\nBody prose sentence.\n")
	if len(got) != 2 || got[0] != "# Section 1" || got[1] != "Body prose sentence." {
		t.Fatalf("NonYAMLProseLines() = %v, want [# Section 1, Body prose sentence.]", got)
	}
}

func TestExtractRawYAMLKeys(t *testing.T) {
	raw := "# comment\ntitle: Hello\ntype: note\ntags:\n  - a\ncreated: 2026-01-01\n"
	got := ExtractRawYAMLKeys(raw)
	want := []string{"title", "type", "tags", "created"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ExtractRawYAMLKeys() = %v, want %v", got, want)
	}

	nestedTabRaw := "title: Bad: Title\nmeta: # nested map\n\towner: alice\n\tteam: eng\nstatus: draft\n"
	gotNested := ExtractRawYAMLKeys(nestedTabRaw)
	wantNested := []string{"title", "meta", "status"}
	if !reflect.DeepEqual(gotNested, wantNested) {
		t.Fatalf("ExtractRawYAMLKeys(tab nested) = %v, want %v", gotNested, wantNested)
	}

	tabTypoRaw := "title: Hello\n\tstatus: draft\n"
	gotTypo := ExtractRawYAMLKeys(tabTypoRaw)
	wantTypo := []string{"title", "status"}
	if !reflect.DeepEqual(gotTypo, wantTypo) {
		t.Fatalf("ExtractRawYAMLKeys(single tab typo) = %v, want %v", gotTypo, wantTypo)
	}
}
