package document

import (
	"reflect"
	"strings"
	"testing"
)

func TestExtractWikiLinks_Simple(t *testing.T) {
	links := ExtractWikiLinks("See [[target-doc]] for more.")
	if len(links) != 1 {
		t.Fatalf("expected 1 link, got %d", len(links))
	}
	if links[0].Target != "target-doc" {
		t.Errorf("target = %q, want %q", links[0].Target, "target-doc")
	}
}

func TestExtractWikiLinks_WithHeading(t *testing.T) {
	links := ExtractWikiLinks("See [[doc#Section]].")
	if len(links) != 1 {
		t.Fatalf("expected 1 link, got %d", len(links))
	}
	if links[0].Target != "doc" {
		t.Errorf("target = %q", links[0].Target)
	}
	if links[0].Heading != "Section" {
		t.Errorf("heading = %q, want %q", links[0].Heading, "Section")
	}
}

func TestExtractWikiLinks_WithAlias(t *testing.T) {
	links := ExtractWikiLinks("See [[doc|Display Name]].")
	if len(links) != 1 {
		t.Fatalf("expected 1 link, got %d", len(links))
	}
	if links[0].Alias != "Display Name" {
		t.Errorf("alias = %q, want %q", links[0].Alias, "Display Name")
	}
}

func TestExtractWikiLinks_Full(t *testing.T) {
	links := ExtractWikiLinks("[[doc#heading|alias]]")
	if len(links) != 1 {
		t.Fatalf("expected 1 link, got %d", len(links))
	}
	l := links[0]
	if l.Target != "doc" || l.Heading != "heading" || l.Alias != "alias" {
		t.Errorf("got target=%q heading=%q alias=%q", l.Target, l.Heading, l.Alias)
	}
}

func TestExtractWikiLinks_Multiple(t *testing.T) {
	body := "See [[a]], [[b]], and [[c]]."
	links := ExtractWikiLinks(body)
	if len(links) != 3 {
		t.Errorf("expected 3 links, got %d", len(links))
	}
}

func TestExtractWikiLinks_None(t *testing.T) {
	links := ExtractWikiLinks("No links here.")
	if len(links) != 0 {
		t.Errorf("expected 0 links, got %d", len(links))
	}
}

func TestExtractWikiLinks_IgnoresInlineCode(t *testing.T) {
	// Discussion prose mentions wikilink syntax via backticks — must not
	// produce broken-link warnings from lint.
	body := "Use `[[Title]]` as a wikilink, or write [[real-doc]]."
	links := ExtractWikiLinks(body)
	if len(links) != 1 {
		t.Fatalf("expected 1 link, got %d: %+v", len(links), links)
	}
	if links[0].Target != "real-doc" {
		t.Errorf("target = %q, want real-doc", links[0].Target)
	}
}

func TestExtractWikiLinks_IgnoresFencedCode(t *testing.T) {
	// A fenced code block demonstrating wikilinks shouldn't count as a link.
	body := "See docs:\n\n```markdown\n[[example-link]]\n```\n\nThen [[real-doc]] applies."
	links := ExtractWikiLinks(body)
	if len(links) != 1 {
		t.Fatalf("expected 1 link, got %d: %+v", len(links), links)
	}
	if links[0].Target != "real-doc" {
		t.Errorf("target = %q, want real-doc", links[0].Target)
	}
}

func TestExtractWikiLinks_IgnoresTildeFencedCode(t *testing.T) {
	body := "~~~\n[[inside]]\n~~~\n[[outside]]"
	links := ExtractWikiLinks(body)
	if len(links) != 1 || links[0].Target != "outside" {
		t.Errorf("got %+v, want [outside]", links)
	}
}

func TestExtractWikiLinks_MultipleInlineCodeMixed(t *testing.T) {
	// Multiple inline code spans interleaved with real links.
	body := "Real [[alpha]], code `[[fake]]`, real [[beta]], code `[[also-fake]]`, real [[gamma]]."
	links := ExtractWikiLinks(body)
	if len(links) != 3 {
		t.Fatalf("expected 3 real links, got %d: %+v", len(links), links)
	}
	for i, want := range []string{"alpha", "beta", "gamma"} {
		if links[i].Target != want {
			t.Errorf("links[%d].Target = %q, want %q", i, links[i].Target, want)
		}
	}
}

func TestExtractWikiLinks_IgnoresDoubleBacktickCode(t *testing.T) {
	// A double-backtick span is used when the inline code itself contains a
	// backtick. The [[old]] inside must not be treated as a real link, while a
	// normal [[real-doc]] still resolves.
	body := "Literal ``[[old]]`` here, but [[real-doc]] is a link."
	links := ExtractWikiLinks(body)
	if len(links) != 1 {
		t.Fatalf("expected 1 link, got %d: %+v", len(links), links)
	}
	if links[0].Target != "real-doc" {
		t.Errorf("target = %q, want real-doc", links[0].Target)
	}
}

func TestExtractWikiLinks_DoubleBacktickSpanWithLiteralBacktick(t *testing.T) {
	// A double-backtick span whose content holds a literal single backtick AND a
	// wikilink must be fully masked: the single backtick inside does not close
	// the span (CommonMark requires a run of exactly N), so [[link]] stays code.
	body := "Code ``a ` b [[link]]`` and then [[real]]."
	links := ExtractWikiLinks(body)
	if len(links) != 1 {
		t.Fatalf("expected 1 link, got %d: %+v", len(links), links)
	}
	if links[0].Target != "real" {
		t.Errorf("target = %q, want real", links[0].Target)
	}
}

func TestExtractWikiLinks_MarkdownLinksAndEmbeds(t *testing.T) {
	body := "Here is a standard markdown link [to doc](docs/notes.md#heading), an external one [Google](https://google.com), and an embed ![[my-image.png]]."
	links := ExtractWikiLinks(body)

	// We expect two links extracted (the standard markdown link and the embed wikilink).
	// The external Google link is ignored.
	if len(links) != 2 {
		t.Fatalf("expected 2 links, got %d: %+v", len(links), links)
	}

	// First link (extracted first via wikilink/embed regex): ![[my-image.png]]
	if links[0].Target != "my-image.png" {
		t.Errorf("expected target 'my-image.png', got %q", links[0].Target)
	}

	// Second link (extracted second via markdown link regex): [to doc](docs/notes.md#heading)
	if links[1].Target != "docs/notes.md" {
		t.Errorf("expected target 'docs/notes.md', got %q", links[1].Target)
	}
	if links[1].Heading != "heading" {
		t.Errorf("expected heading 'heading', got %q", links[1].Heading)
	}
	if links[1].Alias != "to doc" {
		t.Errorf("expected alias 'to doc', got %q", links[1].Alias)
	}
}

func TestExtractWikiLinks_IgnoresIndentedAndBlockquoteFences(t *testing.T) {
	body := strings.Join([]string{
		"- Example:",
		"  ```markdown",
		"  [[list-indented-backtick]]",
		"  ```",
		"1. Ordered example:",
		"   ~~~md",
		"   [[ordered-indented-tilde]]",
		"   ~~~",
		"> Blockquote example:",
		"> ```markdown",
		"> [[blockquote-fence]]",
		"> ```",
		"- ```markdown",
		"  [[bullet-same-line-fence]]",
		"  ```",
		"Then [[real-after-indented]] applies.",
	}, "\n")

	links := ExtractWikiLinks(body)
	if len(links) != 1 || links[0].Target != "real-after-indented" {
		t.Fatalf("expected only [real-after-indented], got %+v", links)
	}
}

func TestExtractWikiLinks_IgnoresNestedFourTickAndTildeFences(t *testing.T) {
	body := strings.Join([]string{
		"````markdown",
		"Example fence:",
		"```md",
		"[[nested-three-tick]]",
		"```",
		"~~~",
		"[[nested-three-tilde]]",
		"~~~",
		"```not-a-closer",
		"````not-a-closer-either",
		"[[still-inside-four-tick]]",
		"````",
		"After backtick fence [[real-one]].",
		"~~~~markdown",
		"```",
		"[[inside-four-tilde]]",
		"```",
		"~~~~",
		"After tilde fence [[real-two]].",
	}, "\n")

	links := ExtractWikiLinks(body)
	if len(links) != 2 || links[0].Target != "real-one" || links[1].Target != "real-two" {
		t.Fatalf("expected [real-one, real-two], got %+v", links)
	}
}

func TestExtractWikiLinks_LineStartInlineBacktickNotFencedBlock(t *testing.T) {
	body := strings.Join([]string{
		"```[[inline-triple]]``` and [[real-one]].",
		"`a` and `[[inline-single]]` and [[real-two]].",
		"    [[not-fenced-plain-prose]]",
	}, "\n")

	links := ExtractWikiLinks(body)
	if len(links) != 3 {
		t.Fatalf("expected 3 real links, got %d: %+v", len(links), links)
	}
	want := []string{"real-one", "real-two", "not-fenced-plain-prose"}
	for i, w := range want {
		if links[i].Target != w {
			t.Errorf("links[%d].Target = %q, want %q", i, links[i].Target, w)
		}
	}
}

func TestExtractWikiLinks_CRLFFencedAndUnclosedToEOF(t *testing.T) {
	crlf := "- Item\r\n  ````md\r\n  ```text\r\n  [[crlf-nested]]\r\n  ```\r\n  ````\r\n[[real-crlf]]\r\n```go\r\n[[unclosed-eof]]"
	links := ExtractWikiLinks(crlf)
	if len(links) != 1 || links[0].Target != "real-crlf" {
		t.Fatalf("expected only [real-crlf], got %+v", links)
	}
}

func TestExtractWikiLinks_BlockquoteDepthFenceClose(t *testing.T) {
	body := strings.Join([]string{
		"```md",
		"> ```",
		"[[broken-and-target]]",
		"```",
		"[[real-after-toplevel]]",
		"> ```md",
		"> > ```",
		"> [[inside-bq1]]",
		"> ```",
		"[[real-after-bq1]]",
		"> > ```md",
		"> > [[inside-bq2]]",
		"> > ```",
		"[[real-after-bq2]]",
		"- ```md",
		"  > ```",
		"  [[inside-list-fence]]",
		"  ```",
		"[[real-after-list]]",
	}, "\n")

	links := ExtractWikiLinks(body)
	want := []string{"real-after-toplevel", "real-after-bq1", "real-after-bq2", "real-after-list"}
	if len(links) != len(want) {
		t.Fatalf("expected %d links (%v), got %d: %+v", len(want), want, len(links), links)
	}
	for i, w := range want {
		if links[i].Target != w {
			t.Errorf("links[%d].Target = %q, want %q", i, links[i].Target, w)
		}
	}
}

func TestMaskAndExtractInlineCodeSpans(t *testing.T) {
	line := "Check ``foo ` bar Core Target baz ` qux`` and `single` and ```a `` b ` c``` end."
	masked := MaskInlineCodeSpans(line)
	if strings.Contains(masked, "Core Target") || strings.Contains(masked, "single") || strings.Contains(masked, "a `` b") {
		t.Fatalf("inline code leaked into masked output: %q", masked)
	}
	if !strings.HasPrefix(masked, "Check ") || !strings.HasSuffix(masked, " end.") {
		t.Fatalf("prose surrounding inline code damaged: %q", masked)
	}
	if len(masked) != len(line) {
		t.Fatalf("MaskInlineCodeSpans changed byte length: got %d, want %d", len(masked), len(line))
	}

	gotSpans := ExtractInlineCodeSpans(line)
	wantSpans := []string{
		"``foo ` bar Core Target baz ` qux``",
		"`single`",
		"```a `` b ` c```",
	}
	if !reflect.DeepEqual(gotSpans, wantSpans) {
		t.Fatalf("ExtractInlineCodeSpans = %#v, want %#v", gotSpans, wantSpans)
	}

	// Unmatched backtick on line 1 must not pair with line 2.
	multi := "Unclosed `backtick\nPlain Core Target `prose"
	if got := MaskInlineCodeSpans(multi); got != multi {
		t.Fatalf("unmatched backticks across lines should stay untouched, got %q", got)
	}
	if got := ExtractInlineCodeSpans(multi); len(got) != 0 {
		t.Fatalf("expected no spans for unmatched backticks, got %#v", got)
	}
}

func TestExtractWikiLinks_ShallowerQuoteFenceDoesNotCloseBlockquoteFence(t *testing.T) {
	body := strings.Join([]string{
		// bq1_with_top_trap: top-level ``` must NOT close > ```md
		"> ```md",
		"```",
		"> [[broken-inside-bq1-after-top]] and [[target]]",
		"> ```",
		"[[real-after-bq1]]",
		// bq2_with_bq1_trap: > ``` and ``` must NOT close > > ```md
		"> > ```md",
		"> ```",
		"```",
		"> > [[broken-inside-bq2-after-bq1]] and [[target]]",
		"> > ```",
		"[[real-after-bq2]]",
		// Tilde equivalents
		"> ~~~md",
		"~~~",
		"> [[broken-tilde-bq1]]",
		"> ~~~",
		"[[real-tilde-bq1]]",
		"> > ~~~md",
		"> ~~~",
		"> > [[broken-tilde-bq2]]",
		"> > ~~~",
		"[[real-tilde-bq2]]",
	}, "\n")

	links := ExtractWikiLinks(body)
	want := []string{"real-after-bq1", "real-after-bq2", "real-tilde-bq1", "real-tilde-bq2"}
	if len(links) != len(want) {
		t.Fatalf("expected %d links (%v), got %d: %+v", len(want), want, len(links), links)
	}
	for i, w := range want {
		if links[i].Target != w {
			t.Errorf("links[%d].Target = %q, want %q", i, links[i].Target, w)
		}
	}
}
