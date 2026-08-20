package document

import "strings"

// fenceTracker skips ATX heading detection inside fenced code blocks so a
// `# comment` in a shell snippet cannot reparent later sections.
//
// Closers must use the same delimiter (` or ~) and a run at least as long as
// the opener. An info string is allowed on open and rejected on close.
// Leading space/tab is trimmed, matching ExtractTasks, so a fence indented
// inside a list item still counts.
type fenceTracker struct {
	delim byte // 0 if not in a fence
	run   int
}

// headingLevel is the heading rule shared by ChunkDocument and SectionBounds.
func (f *fenceTracker) headingLevel(line string) int {
	delim, n, hasInfo := parseFenceLine(line)
	switch {
	case f.delim == 0 && delim != 0:
		f.delim, f.run = delim, n
		return 0
	case f.delim != 0 && delim == f.delim && n >= f.run && !hasInfo:
		f.delim, f.run = 0, 0
		return 0
	case f.delim != 0:
		return 0
	default:
		return headingLevel(line)
	}
}

// parseFenceLine reports a fence marker: delim is '`' or '~', run is its
// length (>= 3), and hasInfo is true when non-whitespace follows the run.
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
	return ch, i, strings.TrimSpace(trimmed[i:]) != ""
}
