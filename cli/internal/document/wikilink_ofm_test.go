package document

import "testing"

func findLink(links []WikiLink, target string) *WikiLink {
	for i := range links {
		if links[i].Target == target {
			return &links[i]
		}
	}
	return nil
}

func TestExtractWikiLinks_EmbedFlag(t *testing.T) {
	links := ExtractWikiLinks("normal [[note]] and embed ![[note]] and image ![alt](pic.png)")

	// Two [[note]] occurrences (one normal, one embed) plus the image embed.
	var normal, embedded *WikiLink
	for i := range links {
		if links[i].Target == "note" {
			if links[i].Embed {
				embedded = &links[i]
			} else {
				normal = &links[i]
			}
		}
	}
	if normal == nil {
		t.Fatal("missing normal [[note]] link")
	}
	if embedded == nil {
		t.Fatal("missing embedded ![[note]] link")
	}

	img := findLink(links, "pic.png")
	if img == nil || !img.Embed {
		t.Errorf("expected image embed flagged, got %+v", img)
	}
}

func TestExtractWikiLinks_BlockReference(t *testing.T) {
	links := ExtractWikiLinks("see [[doc#^block-1]] and [[doc#Heading]]")

	blk := links[0]
	if blk.Target != "doc" || blk.Block != "block-1" || blk.Heading != "" {
		t.Errorf("block link parsed wrong: %+v", blk)
	}

	head := links[1]
	if head.Target != "doc" || head.Heading != "Heading" || head.Block != "" {
		t.Errorf("heading link parsed wrong: %+v", head)
	}
}

func TestExtractWikiLinks_MarkdownBlockReference(t *testing.T) {
	links := ExtractWikiLinks("[label](notes/x.md#^abc)")
	if len(links) != 1 {
		t.Fatalf("expected 1 link, got %d", len(links))
	}
	if links[0].Target != "notes/x.md" || links[0].Block != "abc" || links[0].Heading != "" {
		t.Errorf("markdown block link parsed wrong: %+v", links[0])
	}
}

func TestExtractWikiLinks_ExternalSchemesSkipped(t *testing.T) {
	body := "[a](http://x.com) [b](https://x.com) [c](mailto:x@y.com) [d](ftp://h/f) [e](file:///tmp/x)"
	if links := ExtractWikiLinks(body); len(links) != 0 {
		t.Errorf("expected external links skipped, got %+v", links)
	}
}
