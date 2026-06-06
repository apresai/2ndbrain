package document

import (
	"regexp"
	"strings"
)

var wikilinkRe = regexp.MustCompile(`\[\[([^\]]+)\]\]`)
var mdLinkRe = regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`)

type WikiLink struct {
	Raw     string `json:"raw"`
	Target  string `json:"target"`
	Heading string `json:"heading,omitempty"`
	// Block is the block-reference id from an Obsidian [[note#^block-id]] link
	// (the caret is stripped). Mutually exclusive with Heading.
	Block string `json:"block,omitempty"`
	Alias string `json:"alias,omitempty"`
	// Embed is true for transclusions: ![[note]] / ![[note#heading]] and the
	// markdown image form ![alt](target). The target still resolves as a link,
	// but the flag lets callers treat embeds (esp. of assets) differently.
	Embed bool `json:"embed,omitempty"`
}

// ExtractWikiLinks finds all [[target#heading|alias]], [[note#^block]],
// ![[embed]], and [alias](target#heading) patterns in the text. Patterns inside
// fenced code blocks (``` or ~~~) and inline code spans (`foo`) are ignored so
// that documentation discussing wikilink syntax doesn't produce false-positive
// broken-link warnings from `2nb lint`.
func ExtractWikiLinks(body string) []WikiLink {
	scanBody := maskCodeRegions(body)
	links := make([]WikiLink, 0)

	// Wikilinks: [[target#anchor|alias]], with an optional leading '!' embed.
	for _, m := range wikilinkRe.FindAllStringSubmatchIndex(scanBody, -1) {
		inner := scanBody[m[2]:m[3]]
		link := WikiLink{Raw: scanBody[m[0]:m[1]]}
		if m[0] > 0 && scanBody[m[0]-1] == '!' {
			link.Embed = true
			link.Raw = "!" + link.Raw
		}

		// Split alias: [[target|alias]]
		if idx := indexOf(inner, '|'); idx >= 0 {
			link.Alias = inner[idx+1:]
			inner = inner[:idx]
		}

		// Split anchor: [[target#heading]] or [[target#^block-id]]
		if idx := indexOf(inner, '#'); idx >= 0 {
			setAnchor(&link, inner[idx+1:])
			inner = inner[:idx]
		}

		link.Target = inner
		links = append(links, link)
	}

	// Standard markdown links: [alias](target#anchor), optionally an image embed ![alt](src).
	for _, m := range mdLinkRe.FindAllStringSubmatchIndex(scanBody, -1) {
		alias := scanBody[m[2]:m[3]]
		target := scanBody[m[4]:m[5]]

		if isExternalLink(target) {
			continue
		}

		link := WikiLink{Raw: scanBody[m[0]:m[1]], Alias: alias}
		if m[0] > 0 && scanBody[m[0]-1] == '!' {
			link.Embed = true
			link.Raw = "!" + link.Raw
		}

		// Split anchor: target#heading or target#^block-id
		if idx := strings.IndexByte(target, '#'); idx >= 0 {
			setAnchor(&link, target[idx+1:])
			target = target[:idx]
		}

		link.Target = target
		links = append(links, link)
	}

	return links
}

// setAnchor routes a link's "#anchor" suffix to Block (if it starts with '^',
// an Obsidian block reference) or Heading otherwise.
func setAnchor(link *WikiLink, anchor string) {
	if strings.HasPrefix(anchor, "^") {
		link.Block = anchor[1:]
	} else {
		link.Heading = anchor
	}
}

// isExternalLink reports whether a markdown link target points outside the
// vault (a URL or mail/file/ftp scheme) and should not be treated as a wikilink.
func isExternalLink(target string) bool {
	return strings.HasPrefix(target, "http://") ||
		strings.HasPrefix(target, "https://") ||
		strings.HasPrefix(target, "mailto:") ||
		strings.HasPrefix(target, "file://") ||
		strings.HasPrefix(target, "ftp://")
}

// maskCodeRegions returns a copy of body where '[' and ']' bytes that appear
// inside fenced code blocks or inline code spans are replaced with spaces.
// Only the bracket bytes are touched, so multi-byte UTF-8 sequences in code
// remain intact and byte positions stay stable.
//
// This is a lightweight scanner, not a full CommonMark parser. It handles:
//   - Triple-backtick (```) and triple-tilde (~~~) fenced blocks at line start
//   - Single-backtick inline code on a single line
//
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
