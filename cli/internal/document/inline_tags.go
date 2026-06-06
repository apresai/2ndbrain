package document

import (
	"regexp"
	"strings"
)

// inlineTagRe matches an Obsidian inline #tag: a '#' preceded by start-of-line
// or whitespace, followed by a tag starting with a letter. A nested tag like
// #area/work captures only the first segment ("area"), matching the legacy
// importer's behavior. This is the single source of truth for what counts as an
// inline tag during indexing.
var inlineTagRe = regexp.MustCompile(`(?:^|\s)#([a-zA-Z][a-zA-Z0-9_-]*)`)

// ExtractInlineTags scans body for Obsidian inline #tags, skipping fenced code
// blocks (``` / ~~~) and markdown heading lines (so "# Heading" is not a tag),
// and returns the unique tags in order of first appearance. The body is not
// modified.
func ExtractInlineTags(body string) []string {
	lines := strings.Split(body, "\n")
	inCode := false
	seen := make(map[string]bool)
	var tags []string

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Toggle fenced code-block state; never extract tags from code.
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			inCode = !inCode
			continue
		}
		if inCode {
			continue
		}

		// A leading "# " is a markdown heading, not a tag.
		if isHeadingLine(trimmed) {
			continue
		}

		for _, m := range inlineTagRe.FindAllStringSubmatch(line, -1) {
			tag := m[1]
			if !seen[tag] {
				seen[tag] = true
				tags = append(tags, tag)
			}
		}
	}
	return tags
}

// isHeadingLine reports whether a trimmed line is a markdown ATX heading
// (one or more '#' followed by a space), which must not be treated as a tag.
func isHeadingLine(trimmed string) bool {
	if !strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "#!") {
		return false
	}
	for i, c := range trimmed {
		if c == '#' {
			continue
		}
		return c == ' ' && i > 0
	}
	return false
}
