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
//   - Inline code spans delimited by a run of N backticks (CommonMark: a span
//     opened by N backticks ends at the next run of exactly N backticks; runs of
//     a different length inside don't close it). This covers a single-backtick span, a
//     double-backtick span (used when the content itself holds a backtick), and longer runs.
//
// Nested or indented fences are intentionally ignored — they're rare in practice
// and the worst failure is an over-eager lint warning. Inline spans stay
// single-line; the fence branch above handles multi-line code.
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

		// Inline code span on a single line: a run of N backticks opens the span,
		// the next run of EXACTLY N backticks closes it (CommonMark code-span
		// rule). Runs of a different length inside don't close it, which is what
		// lets ``[[x]]`` hold a literal backtick. Stays single-line.
		if body[i] == '`' {
			// Measure the opening run length.
			open := i
			for open < len(body) && body[open] == '`' {
				open++
			}
			n := open - i // number of backticks in the opening run

			// Scan for a closing run of exactly n backticks on the same line.
			closeStart, closeEnd := -1, -1
			for j := open; j < len(body) && body[j] != '\n'; {
				if body[j] != '`' {
					j++
					continue
				}
				run := j
				for run < len(body) && body[run] == '`' {
					run++
				}
				if run-j == n {
					closeStart, closeEnd = j, run
					break
				}
				// Different-length run: not a closer, skip past it.
				j = run
			}

			if closeStart >= 0 {
				// Closing run found: mask brackets in the content between the
				// opening and closing delimiters, then resume after the closer.
				for k := open; k < closeStart; k++ {
					if body[k] == '[' || body[k] == ']' {
						result[k] = ' '
					}
				}
				i = closeEnd
				atLineStart = false
				continue
			}
			// No closing run on this line: treat the opening backticks as literal
			// text and fall through to advance one byte at a time.
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

// MaskCodeRegions exposes maskCodeRegions for callers outside this package that
// need to scan a body while ignoring fenced and inline code, e.g. matching a
// note title against prose without matching it inside a code sample. Only the
// '[' and ']' bytes inside code are blanked to spaces; byte positions are
// preserved, so spans found in the masked copy map 1:1 onto the original.
func MaskCodeRegions(body string) string { return maskCodeRegions(body) }

func indexOf(s string, c byte) int {
	for i := range len(s) {
		if s[i] == c {
			return i
		}
	}
	return -1
}
