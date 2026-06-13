package document

import "strings"

// SectionBounds locates the body content of a markdown section by heading and
// returns the [start, end) line range (0-based, end exclusive) of the section's
// CONTENT, i.e. the lines AFTER the heading line up to (but not including) the
// next heading at the same or a shallower level, or end-of-body.
//
// It operates on the RAW body (frontmatter already excluded by Document.Body),
// not on the comment-stripped chunk text, so a caller can splice the result
// back into the file losslessly: %% comments %%, code fences, and blank lines
// inside the section are preserved verbatim.
//
// headingPath may be the bare heading title ("Decision") or include leading
// hashes ("## Decision"); both match the same heading. Matching is
// case-insensitive on the trimmed title and ignores any leading "#" markers.
// When the path contains " > " separators (the same form ChunkDocument emits),
// only the LAST component is matched against the heading title; ancestors are
// not required to line up. The heading-level detection reuses the exact rule
// ChunkDocument uses (headingLevel), so section boundaries agree with chunking.
//
// First match wins: when the body has multiple headings with the same title,
// the earliest one is selected. ok is false when no heading matches.
//
// The returned range covers only the content, never the heading line itself,
// so replace operations keep the heading in place. An empty section (a heading
// immediately followed by a sibling/parent heading or EOF) returns start == end.
func SectionBounds(body, headingPath string) (start, end int, ok bool) {
	target := normalizeHeadingTarget(headingPath)
	if target == "" {
		return 0, 0, false
	}

	lines := strings.Split(body, "\n")

	headingLine := -1
	headingLvl := 0
	for i, line := range lines {
		lvl := headingLevel(line)
		if lvl == 0 {
			continue
		}
		title := strings.ToLower(strings.TrimSpace(line[lvl:]))
		if title == target {
			headingLine = i
			headingLvl = lvl
			break
		}
	}

	if headingLine == -1 {
		return 0, 0, false
	}

	// Content starts on the line after the heading.
	start = headingLine + 1
	// Content ends at the next heading whose level is <= the matched heading's
	// level (a sibling or an ancestor), or at end-of-body.
	end = len(lines)
	for i := start; i < len(lines); i++ {
		lvl := headingLevel(lines[i])
		if lvl > 0 && lvl <= headingLvl {
			end = i
			break
		}
	}

	return start, end, true
}

// normalizeHeadingTarget reduces a heading path to its match key: the last
// " > " component, with leading "#" markers and surrounding space stripped,
// lowercased.
func normalizeHeadingTarget(headingPath string) string {
	parts := strings.Split(headingPath, " > ")
	last := parts[len(parts)-1]
	last = strings.TrimSpace(last)
	last = strings.TrimLeft(last, "#")
	last = strings.TrimSpace(last)
	return strings.ToLower(last)
}

// ReplaceSection returns a copy of body with the named section's content
// replaced by newContent. The heading line is preserved; only the lines under
// it (up to the next sibling/parent heading or EOF) change. newContent is
// inserted as a block with a single trailing newline so the following heading
// (if any) stays on its own line. ok is false when the heading is not found,
// in which case body is returned unchanged.
func ReplaceSection(body, headingPath, newContent string) (string, bool) {
	start, end, ok := SectionBounds(body, headingPath)
	if !ok {
		return body, false
	}

	lines := strings.Split(body, "\n")

	// Build the replacement block. Normalize line endings and split so a
	// multi-line newContent splices cleanly.
	newContent = strings.ReplaceAll(newContent, "\r\n", "\n")
	newContent = strings.Trim(newContent, "\n")

	var rebuilt []string
	rebuilt = append(rebuilt, lines[:start]...)
	if newContent != "" {
		// Blank line after the heading for readability, then the content.
		rebuilt = append(rebuilt, "")
		rebuilt = append(rebuilt, strings.Split(newContent, "\n")...)
		rebuilt = append(rebuilt, "")
	}
	rebuilt = append(rebuilt, lines[end:]...)

	return strings.Join(rebuilt, "\n"), true
}
