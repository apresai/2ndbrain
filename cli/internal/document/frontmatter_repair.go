package document

import (
	"bytes"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

var (
	yamlKeyLineRe      = regexp.MustCompile(`^[ \t]*([A-Za-z0-9_.-]+)[ \t]*:`)
	markdownHeadingRe  = regexp.MustCompile(`^[ \t]*#{1,6}[ \t]`)
)

// RawFrontmatterParts isolates the raw YAML frontmatter region and untouched
// body of a Markdown note, including notes whose YAML is malformed or whose
// opening `---` fence lacks a closing `---` fence.
type RawFrontmatterParts struct {
	OpenFence  string
	RawYAML    string
	CloseFence string
	Body       string
	Newline    string
	Unclosed   bool
}

// HasUnclosedFrontmatter reports whether content starts with an opening `---`
// fence, has no closing `---` fence, and contains YAML key-value lines at the
// top of the file (distinguishing unclosed frontmatter from a Markdown file that
// happens to start with a horizontal rule).
func HasUnclosedFrontmatter(content []byte) bool {
	s := string(content)
	openLen, ok := openingFenceLen(s)
	if !ok {
		return false
	}
	rest := s[openLen:]
	if _, empty := emptyFrontmatterBlock(rest); empty {
		return false
	}
	if idx, _ := closingFence(rest); idx != -1 {
		return false
	}

	// Scan the opening block before any Markdown heading or post-blank prose.
	// Standard YAML frontmatter begins immediately after `---`; an opening `---`
	// immediately followed by a blank line (e.g. `---\n\nNote: prose...`) is a
	// Markdown horizontal rule, not unclosed frontmatter.
	lines := strings.Split(rest, "\n")
	if len(lines) == 0 || isBlankLine(strings.TrimSuffix(lines[0], "\r")) {
		return false
	}
	sawBlankAfterStart := false
	for i, rawLine := range lines {
		line := strings.TrimSuffix(rawLine, "\r")
		if isBlankLine(line) {
			sawBlankAfterStart = true
			continue
		}
		if markdownHeadingRe.MatchString(line) && !isYAMLCommentLine(lines, i) {
			return false
		}
		if sawBlankAfterStart {
			// After a blank line, only continue if this still looks like a YAML
			// key or list item; otherwise we've reached body prose.
			trimmed := strings.TrimSpace(line)
			if !yamlKeyLineRe.MatchString(line) && !strings.HasPrefix(trimmed, "- ") && !strings.HasPrefix(trimmed, "#") {
				return false
			}
		}
		if yamlKeyLineRe.MatchString(line) {
			return true
		}
		trimmed := strings.TrimSpace(line)
		// Allow YAML comments or indented/list lines while scanning for a key.
		if strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
			continue
		}
		return false
	}
	return false
}

// isYAMLCommentLine reports whether lines[idx] (which matches markdownHeadingRe)
// acts as a YAML comment inside frontmatter rather than a Markdown heading.
// A `# ...` line is treated as a YAML comment only if subsequent lines before
// any blank line lead to a YAML key line (skipping consecutive `#` comments and
// indented YAML lines).
func isYAMLCommentLine(lines []string, idx int) bool {
	for j := idx + 1; j < len(lines); j++ {
		next := strings.TrimSuffix(lines[j], "\r")
		if isBlankLine(next) {
			return false
		}
		if yamlKeyLineRe.MatchString(next) {
			return true
		}
		trimmed := strings.TrimSpace(next)
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.HasPrefix(next, " ") || strings.HasPrefix(next, "\t") {
			continue
		}
		return false
	}
	return false
}

// SplitRawFrontmatter separates a note's opening fence, raw YAML text, closing
// fence, and body without parsing the YAML itself. Returns false if the note
// does not begin with a frontmatter block (and is not an unclosed frontmatter
// block).
func SplitRawFrontmatter(content []byte) (RawFrontmatterParts, bool) {
	s := string(content)
	openLen, hasOpen := openingFenceLen(s)
	if !hasOpen {
		return RawFrontmatterParts{}, false
	}
	openFence := s[:openLen]
	rest := s[openLen:]

	if _, empty := emptyFrontmatterBlock(rest); empty {
		block, body, isBlock := doubledFenceKeyBlock(rest)
		if !isBlock {
			return RawFrontmatterParts{}, false
		}
		idx, closeLen := closingFence(rest)
		if idx < 0 {
			return RawFrontmatterParts{}, false
		}
		closeRegion := rest[idx : idx+closeLen]
		sep := "\n"
		if strings.HasPrefix(closeRegion, "\r\n") {
			sep = "\r\n"
		}
		return RawFrontmatterParts{
			OpenFence:  openFence,
			RawYAML:    block,
			CloseFence: closeRegion[len(sep):],
			Body:       body,
			Newline:    noteNewline(openFence, body),
		}, true
	}

	if idx, closeLen := closingFence(rest); idx != -1 {
		candidateYAML := rest[:idx]
		var meta map[string]any
		if yamlErr := yaml.Unmarshal([]byte(candidateYAML), &meta); yamlErr != nil {
			if boundaryEnd, boundaryBodyStart := findUnclosedYAMLBoundary(candidateYAML); hasUnclosedBodyBoundary(candidateYAML, boundaryEnd, boundaryBodyStart) {
				nl := noteNewline(openFence, rest)
				rawYAML := strings.TrimRight(rest[:boundaryEnd], "\r\n")
				body := rest[boundaryBodyStart:]
				return RawFrontmatterParts{
					OpenFence:  openFence,
					RawYAML:    rawYAML,
					CloseFence: "---" + nl,
					Body:       body,
					Newline:    nl,
					Unclosed:   true,
				}, true
			}
		}
		closeRegion := rest[idx : idx+closeLen]
		sep := "\n"
		if strings.HasPrefix(closeRegion, "\r\n") {
			sep = "\r\n"
		}
		body := rest[idx+closeLen:]
		return RawFrontmatterParts{
			OpenFence:  openFence,
			RawYAML:    rest[:idx],
			CloseFence: closeRegion[len(sep):],
			Body:       body,
			Newline:    noteNewline(openFence, body),
		}, true
	}

	if !HasUnclosedFrontmatter(content) {
		return RawFrontmatterParts{}, false
	}

	nl := noteNewline(openFence, rest)
	yamlEnd, bodyStart := findUnclosedYAMLBoundary(rest)
	rawYAML := strings.TrimRight(rest[:yamlEnd], "\r\n")
	body := rest[bodyStart:]
	return RawFrontmatterParts{
		OpenFence:  openFence,
		RawYAML:    rawYAML,
		CloseFence: "---" + nl,
		Body:       body,
		Newline:    nl,
		Unclosed:   true,
	}, true
}

// findUnclosedYAMLBoundary locates where an unclosed frontmatter region ends
// and the Markdown body begins.
func findUnclosedYAMLBoundary(rest string) (yamlEnd, bodyStart int) {
	offset := 0
	lastYAMLContentEnd := 0
	pendingBlankStart := -1
	sawKey := false
	inBlockScalar := false
	blockKeyIndent := 0
	flowDepth := 0

	for offset < len(rest) {
		nextNL := strings.IndexByte(rest[offset:], '\n')
		var lineWithNL string
		var nextOffset int
		if nextNL == -1 {
			lineWithNL = rest[offset:]
			nextOffset = len(rest)
		} else {
			lineWithNL = rest[offset : offset+nextNL+1]
			nextOffset = offset + nextNL + 1
		}

		line := strings.TrimRight(lineWithNL, "\r\n")
		trimmed := strings.TrimSpace(line)

		if isBlankLine(line) {
			// Look ahead: if the next non-blank line continues YAML (a key,
			// indented list item, indented block scalar line, or open flow
			// collection), keep going. Otherwise the blank line separates
			// frontmatter from body.
			if sawKey && flowDepth == 0 && !nextNonBlankLooksLikeYAML(rest[nextOffset:], inBlockScalar, blockKeyIndent, flowDepth) {
				return lastYAMLContentEnd, offset
			}
			if pendingBlankStart == -1 {
				pendingBlankStart = offset
			}
			offset = nextOffset
			continue
		}

		boundaryStart := offset
		if pendingBlankStart != -1 {
			boundaryStart = pendingBlankStart
		}

		if inBlockScalar {
			if indentWidth(line) > blockKeyIndent {
				pendingBlankStart = -1
				lastYAMLContentEnd = offset + len(line)
				offset = nextOffset
				continue
			}
			inBlockScalar = false
		}

		if markdownHeadingRe.MatchString(line) && !nextLinesLookLikeYAMLCommentTarget(rest[nextOffset:]) {
			return lastYAMLContentEnd, boundaryStart
		}

		isYAMLish := yamlKeyLineRe.MatchString(line) ||
			strings.HasPrefix(trimmed, "- ") ||
			trimmed == "-" ||
			strings.HasPrefix(trimmed, "#") ||
			strings.HasPrefix(line, " ") ||
			strings.HasPrefix(line, "\t") ||
			strings.HasPrefix(trimmed, "]") ||
			strings.HasPrefix(trimmed, "}")
		if yamlKeyLineRe.MatchString(line) {
			sawKey = true
			if isYAMLBlockScalarStart(line) {
				inBlockScalar = true
				blockKeyIndent = indentWidth(line)
			}
		}
		if !isYAMLish && sawKey {
			return lastYAMLContentEnd, boundaryStart
		}

		if !inBlockScalar {
			flowDepth = updateYAMLFlowDepth(line, flowDepth)
		}
		pendingBlankStart = -1
		lastYAMLContentEnd = offset + len(line)
		offset = nextOffset
	}
	return lastYAMLContentEnd, len(rest)
}

// updateYAMLFlowDepth updates the YAML flow collection nesting depth ('['/'{'
// vs ']'/'}') across a single YAML line, respecting single-quoted strings,
// double-quoted strings, and '#' comments outside quotes.
func updateYAMLFlowDepth(line string, depth int) int {
	inSingle := false
	inDouble := false
	for i := 0; i < len(line); i++ {
		c := line[i]
		if inSingle {
			if c == '\'' {
				if i+1 < len(line) && line[i+1] == '\'' {
					i++
					continue
				}
				inSingle = false
			}
			continue
		}
		if inDouble {
			if c == '\\' {
				if i+1 < len(line) {
					i++
				}
				continue
			}
			if c == '"' {
				inDouble = false
			}
			continue
		}
		if c == '#' && (i == 0 || line[i-1] == ' ' || line[i-1] == '\t') {
			break
		}
		if c == '\'' && (i == 0 || isYAMLFlowDelimiterOrSpace(line[i-1])) {
			inSingle = true
			continue
		}
		if c == '"' && (i == 0 || isYAMLFlowDelimiterOrSpace(line[i-1])) {
			inDouble = true
			continue
		}
		switch c {
		case '[', '{':
			depth++
		case ']', '}':
			if depth > 0 {
				depth--
			}
		}
	}
	return depth
}

func isYAMLFlowDelimiterOrSpace(b byte) bool {
	switch b {
	case ' ', '\t', ':', '[', '{', ',', '-':
		return true
	default:
		return false
	}
}

func indentWidth(line string) int {
	w := 0
	for i := 0; i < len(line); i++ {
		switch line[i] {
		case ' ':
			w++
		case '\t':
			w += 2
		default:
			return w
		}
	}
	return w
}

func isYAMLBlockScalarStart(line string) bool {
	loc := yamlKeyLineRe.FindStringIndex(line)
	if loc == nil {
		return false
	}
	after := strings.TrimSpace(line[loc[1]:])
	if after == "" || (after[0] != '|' && after[0] != '>') {
		return false
	}
	rest := after[1:]
	for i := 0; i < len(rest); i++ {
		c := rest[i]
		if c == ' ' || c == '\t' || c == '#' {
			return true
		}
		if c == '+' || c == '-' || (c >= '1' && c <= '9') {
			continue
		}
		return false
	}
	return true
}

func nextLinesLookLikeYAMLCommentTarget(after string) bool {
	offset := 0
	for offset < len(after) {
		nextNL := strings.IndexByte(after[offset:], '\n')
		var rawLine string
		if nextNL == -1 {
			rawLine = after[offset:]
			offset = len(after)
		} else {
			rawLine = after[offset : offset+nextNL]
			offset += nextNL + 1
		}
		line := strings.TrimSuffix(rawLine, "\r")
		if isBlankLine(line) {
			return false
		}
		if yamlKeyLineRe.MatchString(line) {
			return true
		}
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
			continue
		}
		return false
	}
	return false
}

func nextNonBlankLooksLikeYAML(rest string, inBlockScalar bool, blockKeyIndent int, flowDepth int) bool {
	lines := strings.Split(rest, "\n")
	for i, rawLine := range lines {
		line := strings.TrimSuffix(rawLine, "\r")
		if isBlankLine(line) {
			continue
		}
		if inBlockScalar && indentWidth(line) > blockKeyIndent {
			return true
		}
		if markdownHeadingRe.MatchString(line) {
			if isYAMLCommentLine(lines, i) {
				continue
			}
			return false
		}
		if flowDepth > 0 {
			return true
		}
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		if (strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t")) &&
			(strings.HasPrefix(trimmed, "- ") || trimmed == "-") {
			return true
		}
		return yamlKeyLineRe.MatchString(line)
	}
	return false
}

func hasUnclosedBodyBoundary(candidateYAML string, boundaryEnd, boundaryBodyStart int) bool {
	if boundaryEnd <= 0 || boundaryBodyStart >= len(candidateYAML) {
		return false
	}
	if len(ExtractRawYAMLKeys(candidateYAML[:boundaryEnd])) == 0 {
		return false
	}
	// Do not reclassify notes starting with a leading blank line (`---\n\n...`)
	// as unclosed frontmatter.
	firstLine := candidateYAML
	if nl := strings.IndexByte(firstLine, '\n'); nl >= 0 {
		firstLine = firstLine[:nl]
	}
	if isBlankLine(strings.TrimSuffix(firstLine, "\r")) {
		return false
	}
	tail := candidateYAML[boundaryBodyStart:]
	if strings.TrimSpace(tail) == "" {
		return false
	}
	lines := strings.Split(tail, "\n")
	startsWithBlank := isBlankLine(strings.TrimSuffix(lines[0], "\r"))
	hasHeading := false
	hasProse := false
	hasFlowCloser := false
	hasYAMLKey := false
	for i, rawLine := range lines {
		line := strings.TrimSuffix(rawLine, "\r")
		if isBlankLine(line) {
			continue
		}
		trimmed := strings.TrimSpace(line)
		if trimmed == "]" || trimmed == "}" || trimmed == "]," || trimmed == "}," {
			hasFlowCloser = true
			continue
		}
		if markdownHeadingRe.MatchString(line) {
			if !isYAMLCommentLine(lines, i) {
				hasHeading = true
			}
			continue
		}
		if yamlKeyLineRe.MatchString(line) {
			hasYAMLKey = true
			continue
		}
		if !strings.HasPrefix(trimmed, "#") &&
			!strings.HasPrefix(trimmed, "- ") &&
			trimmed != "-" &&
			!strings.HasPrefix(line, " ") &&
			!strings.HasPrefix(line, "\t") {
			hasProse = true
		}
	}
	if hasFlowCloser {
		return false
	}
	if hasHeading {
		return true
	}
	if hasYAMLKey {
		return false
	}
	return startsWithBlank && hasProse
}

// NonYAMLProseLines returns any lines inside rawYAML that appear to be Markdown
// headings or unindented body prose rather than YAML keys, list items,
// comments, or delimiters. Used as a non-loss guard before/after YAML repair.
func NonYAMLProseLines(rawYAML string) []string {
	lines := strings.Split(rawYAML, "\n")
	var prose []string
	for i, rawLine := range lines {
		line := strings.TrimSuffix(rawLine, "\r")
		if isBlankLine(line) {
			continue
		}
		trimmed := strings.TrimSpace(line)
		if markdownHeadingRe.MatchString(line) {
			if !isYAMLCommentLine(lines, i) && hasNonBlankAfter(lines, i) {
				prose = append(prose, trimmed)
			}
			continue
		}
		if strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "- ") ||
			strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") ||
			yamlKeyLineRe.MatchString(line) {
			continue
		}
		switch trimmed {
		case "[", "]", "{", "}", "---", "...":
			continue
		}
		prose = append(prose, trimmed)
	}
	return prose
}

func hasNonBlankAfter(lines []string, idx int) bool {
	for j := idx + 1; j < len(lines); j++ {
		if !isBlankLine(strings.TrimSuffix(lines[j], "\r")) {
			return true
		}
	}
	return false
}

// ReassembleFrontmatter rebuilds a note from its split parts and repaired YAML
// frontmatter text, preserving the note's line endings and untouched body.
func ReassembleFrontmatter(parts RawFrontmatterParts, repairedYAML string) []byte {
	nl := parts.Newline
	if nl == "" {
		nl = "\n"
	}
	cleaned := strings.TrimRight(strings.ReplaceAll(repairedYAML, "\r\n", "\n"), "\n")
	var buf bytes.Buffer
	buf.WriteString(parts.OpenFence)
	if cleaned != "" {
		buf.WriteString(withNewline(cleaned, nl))
		buf.WriteString(nl)
	}
	closeFence := parts.CloseFence
	if closeFence == "" {
		closeFence = "---" + nl
	}
	if parts.Body != "" && !strings.HasSuffix(closeFence, "\n") && !strings.HasSuffix(closeFence, "\r") {
		closeFence += nl
	}
	buf.WriteString(closeFence)
	buf.WriteString(parts.Body)
	return buf.Bytes()
}

// RepairTabsInYAML replaces leading tab indentation on YAML lines with two
// spaces per tab.
func RepairTabsInYAML(rawYAML string) string {
	lines := strings.Split(rawYAML, "\n")
	changed := false
	for i, line := range lines {
		cr := ""
		if strings.HasSuffix(line, "\r") {
			cr = "\r"
			line = strings.TrimSuffix(line, "\r")
		}
		tabs := 0
		for tabs < len(line) && line[tabs] == '\t' {
			tabs++
		}
		if tabs > 0 {
			lines[i] = strings.Repeat("  ", tabs) + line[tabs:] + cr
			changed = true
		}
	}
	if !changed {
		return rawYAML
	}
	return strings.Join(lines, "\n")
}

// ExtractRawYAMLKeys returns top-level YAML-like key names from raw frontmatter
// text (skipping indented subkeys, including tab-indented nested child keys
// under mapping headers, and comment lines).
func ExtractRawYAMLKeys(rawYAML string) []string {
	seen := make(map[string]bool)
	var keys []string
	inNestedBlock := false
	for _, rawLine := range strings.Split(rawYAML, "\n") {
		line := strings.TrimRight(rawLine, "\r")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "-") {
			continue
		}
		if strings.HasPrefix(line, "\t") {
			if inNestedBlock {
				continue
			}
		} else {
			inNestedBlock = false
		}
		m := yamlKeyLineRe.FindStringSubmatch(line)
		if len(m) < 2 {
			if !strings.HasPrefix(line, "\t") {
				inNestedBlock = false
			}
			continue
		}
		k := m[1]
		if !seen[k] {
			seen[k] = true
			keys = append(keys, k)
		}
		if !strings.HasPrefix(line, "\t") {
			noComment := stripYAMLInlineComment(trimmed)
			inNestedBlock = strings.HasSuffix(noComment, ":")
		}
	}
	return keys
}

func stripYAMLInlineComment(trimmed string) string {
	inSingle := false
	inDouble := false
	escaped := false
	for i := 0; i < len(trimmed); i++ {
		ch := trimmed[i]
		if escaped {
			escaped = false
			continue
		}
		if inDouble && ch == '\\' {
			escaped = true
			continue
		}
		if ch == '\'' && !inDouble {
			inSingle = !inSingle
			continue
		}
		if ch == '"' && !inSingle {
			inDouble = !inDouble
			continue
		}
		if ch == '#' && !inSingle && !inDouble && (i == 0 || trimmed[i-1] == ' ' || trimmed[i-1] == '\t') {
			return strings.TrimSpace(trimmed[:i])
		}
	}
	return strings.TrimSpace(trimmed)
}
