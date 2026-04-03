package document

import (
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
