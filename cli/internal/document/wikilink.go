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

// CodeFenceTracker tracks CommonMark fenced code block state across lines,
// honoring leading whitespace, blockquote ('>') and list-marker prefixes,
// opening fence character ('`' or '~') and run length (>= 3), no-backtick info
// strings on backtick fences, and closing fences of the same character with
// run >= openLen and only optional whitespace to end-of-line.
type CodeFenceTracker struct {
	openChar       byte
	openLen        int
	openQuoteDepth int
}

// Step advances the tracker by one line (without trailing '\n') and returns:
//   - isDelimiter: true when the line opens or closes a fenced code block
//   - inCode: true when the line is body content inside an open fenced block
func (f *CodeFenceTracker) Step(line string) (isDelimiter, inCode bool) {
	if f.openChar == 0 {
		if ch, n, quoteDepth, ok := parseCodeFenceOpen(line); ok {
			f.openChar = ch
			f.openLen = n
			f.openQuoteDepth = quoteDepth
			return true, false
		}
		return false, false
	}
	if isCodeFenceClose(line, f.openChar, f.openLen, f.openQuoteDepth) {
		f.openChar = 0
		f.openLen = 0
		f.openQuoteDepth = 0
		return true, false
	}
	return false, true
}

func stripFenceLinePrefix(line string, allowListMarker bool, maxQuoteDepth int) (string, int) {
	s := strings.TrimSuffix(line, "\r")
	quoteDepth := 0
	for {
		s = strings.TrimLeft(s, " \t")
		if len(s) == 0 {
			return s, quoteDepth
		}
		if s[0] == '>' && (maxQuoteDepth < 0 || quoteDepth < maxQuoteDepth) {
			s = s[1:]
			quoteDepth++
			continue
		}
		if allowListMarker {
			if (s[0] == '-' || s[0] == '*' || s[0] == '+') && len(s) > 1 && (s[1] == ' ' || s[1] == '\t') {
				s = s[1:]
				continue
			}
			d := 0
			for d < len(s) && d < 9 && s[d] >= '0' && s[d] <= '9' {
				d++
			}
			if d > 0 && d+1 < len(s) && (s[d] == '.' || s[d] == ')') && (s[d+1] == ' ' || s[d+1] == '\t') {
				s = s[d+1:]
				continue
			}
		}
		return s, quoteDepth
	}
}

func parseCodeFenceOpen(line string) (openChar byte, openLen int, quoteDepth int, ok bool) {
	s, quoteDepth := stripFenceLinePrefix(line, true, -1)
	if len(s) < 3 {
		return 0, 0, 0, false
	}
	ch := s[0]
	if ch != '`' && ch != '~' {
		return 0, 0, 0, false
	}
	n := 0
	for n < len(s) && s[n] == ch {
		n++
	}
	if n < 3 {
		return 0, 0, 0, false
	}
	// CommonMark §4.5: backtick fence info strings cannot contain backticks,
	// so inline code spans at line start (e.g. ```[[a]]``` or `a` and `b`)
	// are not misclassified as fenced blocks.
	if ch == '`' && strings.IndexByte(s[n:], '`') >= 0 {
		return 0, 0, 0, false
	}
	return ch, n, quoteDepth, true
}

func isCodeFenceClose(line string, openChar byte, openLen int, openQuoteDepth int) bool {
	if openChar == 0 || openLen < 3 {
		return false
	}
	s, quoteDepth := stripFenceLinePrefix(line, false, openQuoteDepth)
	if quoteDepth != openQuoteDepth {
		return false
	}
	if len(s) < openLen || s[0] != openChar {
		return false
	}
	n := 0
	for n < len(s) && s[n] == openChar {
		n++
	}
	if n < openLen {
		return false
	}
	return strings.Trim(s[n:], " \t") == ""
}

// scanInlineCodeSpans finds CommonMark inline code spans on a single line:
// a run of N backticks opens the span, and the next run of EXACTLY N backticks
// on the same line closes it. Runs of a different length inside don't close it,
// which lets ``foo ` bar`` hold literal backticks. Unmatched backtick runs are
// ignored.
func scanInlineCodeSpans(s string, fn func(openStart, openEnd, closeStart, closeEnd int)) {
	i := 0
	for i < len(s) {
		if s[i] != '`' {
			i++
			continue
		}
		open := i
		for open < len(s) && s[open] == '`' {
			open++
		}
		n := open - i

		closeStart, closeEnd := -1, -1
		for j := open; j < len(s) && s[j] != '\n'; {
			if s[j] != '`' {
				j++
				continue
			}
			run := j
			for run < len(s) && s[run] == '`' {
				run++
			}
			if run-j == n {
				closeStart, closeEnd = j, run
				break
			}
			j = run
		}

		if closeStart >= 0 {
			fn(i, open, closeStart, closeEnd)
			i = closeEnd
			continue
		}
		i = open
	}
}

// MaskInlineCodeSpans replaces every CommonMark inline code span in s
// (including its opening and closing backtick runs) with ASCII spaces. Unmatched
// backtick runs are left untouched.
func MaskInlineCodeSpans(s string) string {
	if !strings.ContainsRune(s, '`') {
		return s
	}
	var out []byte
	scanInlineCodeSpans(s, func(openStart, _, _, closeEnd int) {
		if out == nil {
			out = []byte(s)
		}
		for k := openStart; k < closeEnd; k++ {
			out[k] = ' '
		}
	})
	if out == nil {
		return s
	}
	return string(out)
}

// ExtractInlineCodeSpans returns every CommonMark inline code span in s (each
// including its opening and closing backtick runs), matching equal-length
// backtick runs on a single line. Unmatched backtick runs are ignored.
func ExtractInlineCodeSpans(s string) []string {
	if !strings.ContainsRune(s, '`') {
		return nil
	}
	var spans []string
	scanInlineCodeSpans(s, func(openStart, _, _, closeEnd int) {
		spans = append(spans, s[openStart:closeEnd])
	})
	return spans
}

// maskCodeRegions returns a copy of body where '[' and ']' bytes that appear
// inside fenced code blocks or inline code spans are replaced with spaces.
// Only the bracket bytes are touched, so multi-byte UTF-8 sequences in code
// remain intact and byte positions stay stable.
//
// Fenced blocks honor CommonMark rules:
//   - Leading indentation and blockquote/list-marker prefixes before fences
//   - Opening character ('`' or '~') and run length N >= 3, with backtick info
//     strings forbidden from containing backticks
//   - Closing fence of the same character and run length >= N with only
//     optional trailing whitespace (otherwise masked until EOF)
//   - Inline code spans delimited by a run of N backticks on a single line
func maskCodeRegions(body string) string {
	result := []byte(body)
	var fence CodeFenceTracker
	lineStart := 0
	for lineStart < len(body) {
		relNL := strings.IndexByte(body[lineStart:], '\n')
		lineEnd := len(body)
		nextStart := len(body)
		if relNL >= 0 {
			lineEnd = lineStart + relNL
			nextStart = lineEnd + 1
		}

		isDelim, inCode := fence.Step(body[lineStart:lineEnd])
		if inCode {
			for k := lineStart; k < lineEnd; k++ {
				if body[k] == '[' || body[k] == ']' {
					result[k] = ' '
				}
			}
		} else if !isDelim {
			scanInlineCodeSpans(body[lineStart:lineEnd], func(_, open, closeStart, _ int) {
				for k := lineStart + open; k < lineStart+closeStart; k++ {
					if body[k] == '[' || body[k] == ']' {
						result[k] = ' '
					}
				}
			})
		}
		lineStart = nextStart
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
