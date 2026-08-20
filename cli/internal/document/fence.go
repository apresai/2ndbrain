package document

import "strings"

// fenceTracker tracks CommonMark-lite fenced code block state across a
// line-oriented scan. Opening and closing fences must use the same delimiter
// (` or ~) and the closer's run must be at least as long as the opener.
// Info strings on the opener are ignored. A closer may only have trailing
// whitespace after the delimiter run (CommonMark).
//
// Leading whitespace is trimmed (spaces and tabs), matching ExtractTasks /
// ExtractInlineTags, so a fence indented inside a list item is still recognized.
// This is slightly more permissive than CommonMark's 0-3 space indent.
type fenceTracker struct {
	delim byte // 0 if not in a fence
	run   int
}

// Inside reports whether the scanner is currently inside a fenced code block.
func (f *fenceTracker) Inside() bool { return f.delim != 0 }

// Feed consumes one line. It returns true if the line is a fence delimiter
// (open or close). After Feed, Inside() reflects the new state.
func (f *fenceTracker) Feed(line string) bool {
	delim, n, hasInfo := parseFenceLine(line)
	if delim == 0 {
		return false
	}
	if f.delim == 0 {
		f.delim = delim
		f.run = n
		return true
	}
	// Close: same character, long enough, no info string.
	if delim == f.delim && n >= f.run && !hasInfo {
		f.delim = 0
		f.run = 0
		return true
	}
	return false
}

// parseFenceLine reports whether line is a fence marker. delim is '`' or '~',
// run is the delimiter length (>= 3), and hasInfo is true when non-whitespace
// follows the run (an info string on an opener; forbidden on a closer).
func parseFenceLine(line string) (delim byte, run int, hasInfo bool) {
	trimmed := strings.TrimLeft(line, " \t")
	if len(trimmed) < 3 {
		return 0, 0, false
	}
	ch := trimmed[0]
	if ch != '`' && ch != '~' {
		return 0, 0, false
	}
	i := 1
	for i < len(trimmed) && trimmed[i] == ch {
		i++
	}
	if i < 3 {
		return 0, 0, false
	}
	rest := strings.TrimLeft(trimmed[i:], " \t")
	return ch, i, rest != ""
}

// headingLevelOutsideFence is the heading rule shared by ChunkDocument and
// SectionBounds. A line inside a fenced code block is never a heading, even
// when it looks like ATX (`# comment` in a shell snippet).
func headingLevelOutsideFence(line string, f *fenceTracker) int {
	if f.Feed(line) || f.Inside() {
		return 0
	}
	return headingLevel(line)
}
