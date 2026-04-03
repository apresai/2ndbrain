package document

import "regexp"

var wikilinkRe = regexp.MustCompile(`\[\[([^\]]+)\]\]`)

type WikiLink struct {
	Raw     string `json:"raw"`
	Target  string `json:"target"`
	Heading string `json:"heading,omitempty"`
	Alias   string `json:"alias,omitempty"`
}

// ExtractWikiLinks finds all [[target#heading|alias]] patterns in the text.
func ExtractWikiLinks(body string) []WikiLink {
	matches := wikilinkRe.FindAllStringSubmatch(body, -1)
	links := make([]WikiLink, 0, len(matches))

	for _, m := range matches {
		inner := m[1]
		link := WikiLink{Raw: m[0]}

		// Split alias: [[target|alias]]
		if idx := indexOf(inner, '|'); idx >= 0 {
			link.Alias = inner[idx+1:]
			inner = inner[:idx]
		}

		// Split heading: [[target#heading]]
		if idx := indexOf(inner, '#'); idx >= 0 {
			link.Heading = inner[idx+1:]
			inner = inner[:idx]
		}

		link.Target = inner
		links = append(links, link)
	}

	return links
}

func indexOf(s string, c byte) int {
	for i := range len(s) {
		if s[i] == c {
			return i
		}
	}
	return -1
}
