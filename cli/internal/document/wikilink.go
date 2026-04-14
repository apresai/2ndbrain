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
// Patterns inside fenced code blocks (``` or ~~~) and inline code spans
// (`foo`) are ignored so that documentation discussing wikilink syntax
// doesn't produce false-positive broken-link warnings from `2nb lint`.
func ExtractWikiLinks(body string) []WikiLink {
	scanBody := maskCodeRegions(body)
	matches := wikilinkRe.FindAllStringSubmatch(scanBody, -1)
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

// maskCodeRegions returns a copy of body where '[' and ']' bytes that appear
// inside fenced code blocks or inline code spans are replaced with spaces.
// Only the bracket bytes are touched, so multi-byte UTF-8 sequences in code
// remain intact and byte positions stay stable.
//
// This is a lightweight scanner, not a full CommonMark parser. It handles:
//   - Triple-backtick (```) and triple-tilde (~~~) fenced blocks at line start
//   - Single-backtick inline code on a single line
// Nested or indented fences and multi-backtick runs are intentionally ignored
// — they're rare in practice and the worst failure is an over-eager lint warning.
func maskCodeRegions(body string) string {
	result := []byte(body)
	inFence := false
	atLineStart := true
	i := 0
	for i < len(body) {
		// Fenced code block toggle at line start.
		if atLineStart && i+3 <= len(body) &&
			(body[i:i+3] == "```" || body[i:i+3] == "~~~") {
			inFence = !inFence
			// Skip to end of line so the fence marker + info string are left alone.
			for i < len(body) && body[i] != '\n' {
				i++
			}
			atLineStart = true
			continue
		}

		if inFence {
			if body[i] == '[' || body[i] == ']' {
				result[i] = ' '
			}
			if body[i] == '\n' {
				atLineStart = true
			} else {
				atLineStart = false
			}
			i++
			continue
		}

		// Inline code span: `...` on a single line.
		if body[i] == '`' {
			j := i + 1
			for j < len(body) && body[j] != '`' && body[j] != '\n' {
				j++
			}
			if j < len(body) && body[j] == '`' {
				for k := i + 1; k < j; k++ {
					if body[k] == '[' || body[k] == ']' {
						result[k] = ' '
					}
				}
				i = j + 1
				atLineStart = false
				continue
			}
		}

		if body[i] == '\n' {
			atLineStart = true
		} else {
			atLineStart = false
		}
		i++
	}
	return string(result)
}

func indexOf(s string, c byte) int {
	for i := range len(s) {
		if s[i] == c {
			return i
		}
	}
	return -1
}
